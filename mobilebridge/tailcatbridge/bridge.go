// Package tailcatbridge is the narrow gomobile facade the phone clients (Android AAR / iOS XCFramework) use to
// reach a Solco Host through the built-in Tailcat tunnel.
//
// [INPUT]: a pairing's tailcat address + the Host's certificate fingerprint (64 hex SHA-256 of the leaf DER) + the phone's own node key
// [OUTPUT]: Client (Request / OpenSocket / Ping / Close), Socket, SocketListener, GenerateNodeKey, PublicKeyOf
// [POS]: the only Go code that ships to phones. It speaks exactly two things — pinned HTTPS and pinned WebSocket — to
//
//	one fixed virtual port of one paired Mac, so it is not a proxy: no listening socket on the phone, no URL scheme
//	other than https/wss, no host other than the paired tunnel, no redirect following. Certificate pin failure happens
//	in the TLS handshake, before any request byte is written. The tunnel's encryption never replaces the Host's own
//	Bearer auth or the pin. Secrets (node key, tailcat address, tokens) never reach a log.
//
// [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
package tailcatbridge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/tailscale/tailcat"
	"tailscale.com/types/key"
)

// Port is the Host's virtual HTTPS port inside the tunnel (internal/tailcatnet.Port).
const Port = 7788

const (
	maxResponseBytes = 16 << 20
	// ErrPinMismatch is the message prefix callers match to tell a wrong certificate from a network failure.
	ErrPinMismatch = "pin_mismatch"
)

// GenerateNodeKey returns a new phone node private key in text form. The phone stores it in its own encrypted
// storage; the Mac only ever sees the derived public key, over the authenticated tunnel handshake.
func GenerateNodeKey() string {
	text, _ := key.NewNode().MarshalText()
	return string(text)
}

// PublicKeyOf derives the public key text from a node private key text.
func PublicKeyOf(nodeKey string) (string, error) {
	var priv key.NodePrivate
	if err := priv.UnmarshalText([]byte(nodeKey)); err != nil || priv.IsZero() {
		return "", errors.New("invalid node key")
	}
	text, err := priv.Public().MarshalText()
	if err != nil {
		return "", err
	}
	return string(text), nil
}

// Client is one tunnel to one Mac. Create it per (tailcat address, node key); Close releases the tunnel.
type Client struct {
	tc   *tailcat.Client
	pin  [sha256.Size]byte
	http *http.Client

	mu     sync.Mutex
	closed bool
}

// NewClient prepares a client; no network happens until the first call. pinHex is the Host certificate's
// SHA-256 fingerprint, nodeKey the phone's persistent node private key text (see GenerateNodeKey).
func NewClient(tailcatAddr, nodeKey, pinHex string) (*Client, error) {
	pinHex = strings.ToLower(strings.TrimSpace(pinHex))
	raw, err := hex.DecodeString(pinHex)
	if err != nil || len(raw) != sha256.Size {
		return nil, errors.New("invalid fingerprint")
	}
	var priv key.NodePrivate
	if err := priv.UnmarshalText([]byte(nodeKey)); err != nil || priv.IsZero() {
		return nil, errors.New("invalid node key")
	}
	addr := strings.TrimSpace(tailcatAddr)
	if !strings.HasPrefix(addr, "tc") || len(addr) < 16 {
		return nil, errors.New("invalid tailcat address")
	}
	c := &Client{tc: &tailcat.Client{Server: tailcat.Addr(addr), Key: priv, Logf: func(string, ...any) {}}}
	copy(c.pin[:], raw)
	c.http = &http.Client{
		Transport: &http.Transport{
			DialContext:         c.dialTunnel,
			TLSClientConfig:     c.tlsConfig(),
			ForceAttemptHTTP2:   false,
			DisableCompression:  true,
			TLSHandshakeTimeout: 20 * time.Second,
			MaxIdleConnsPerHost: 2,
			IdleConnTimeout:     60 * time.Second,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return c, nil
}

// the tunnel is dialled by port, never by the URL's host: the URL host is a label only.
func (c *Client) dialTunnel(ctx context.Context, _, _ string) (net.Conn, error) {
	return c.tc.DialTCPPort(ctx, Port)
}

func (c *Client) tlsConfig() *tls.Config {
	pin := c.pin
	return &tls.Config{
		MinVersion:         tls.VersionTLS13,
		InsecureSkipVerify: true, // trust is the fingerprint below, not a CA chain
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return errors.New(ErrPinMismatch)
			}
			sum := sha256.Sum256(rawCerts[0])
			if sum != pin {
				return errors.New(ErrPinMismatch)
			}
			return nil
		},
	}
}

// Ping dials the tunnel once and reports the round trip in milliseconds (tunnel setup included on first use).
func (c *Client) Ping(timeoutMs int64) (int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), ms(timeoutMs))
	defer cancel()
	began := time.Now()
	res, err := c.tc.Ping(ctx)
	if err != nil {
		return 0, err
	}
	_ = res
	return time.Since(began).Milliseconds(), nil
}

// Response is a finished HTTP exchange.
type Response struct {
	status  int
	headers string
	body    []byte
}

func (r *Response) Status() int         { return r.status }
func (r *Response) HeadersJSON() string { return r.headers }
func (r *Response) Body() []byte        { return r.body }

// Request performs one HTTPS request to the paired Mac. path must be an absolute path (with optional query);
// headersJSON is a JSON object of string values. A wrong certificate fails the handshake before the request
// is written; redirects are returned, never followed.
func (c *Client) Request(method, path, headersJSON string, body []byte, timeoutMs int64) (*Response, error) {
	u, err := tunnelURL("https", path)
	if err != nil {
		return nil, err
	}
	headers, err := parseHeaders(headersJSON)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), ms(timeoutMs))
	defer cancel()
	var rd io.Reader
	if len(body) > 0 {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, strings.ToUpper(method), u, rd)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, wrapNet(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, wrapNet(err)
	}
	if len(data) > maxResponseBytes {
		return nil, errors.New("response too large")
	}
	out := map[string]string{}
	for k := range resp.Header {
		out[strings.ToLower(k)] = resp.Header.Get(k)
	}
	hj, _ := json.Marshal(out)
	return &Response{status: resp.StatusCode, headers: string(hj), body: data}, nil
}

// SocketListener receives one socket's events. Calls are serialised per socket; OnClose is delivered once, last.
type SocketListener interface {
	OnOpen()
	OnText(text string)
	OnBinary(data []byte)
	OnClose(code int, reason string)
	OnError(message string, pinMismatch bool, httpStatus int)
}

// Socket is one open WebSocket to the paired Mac.
type Socket struct {
	conn   *websocket.Conn
	cancel context.CancelFunc
	wmu    sync.Mutex
}

// OpenSocket dials wss://<paired Mac><path> with the given subprotocols (comma separated); events arrive on
// the listener. The call returns immediately; the outcome is OnOpen or OnError+OnClose.
func (c *Client) OpenSocket(path, protocolsCSV string, l SocketListener) (*Socket, error) {
	u, err := tunnelURL("wss", path)
	if err != nil {
		return nil, err
	}
	var protos []string
	for _, p := range strings.Split(protocolsCSV, ",") {
		if p = strings.TrimSpace(p); p != "" {
			protos = append(protos, p)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Socket{cancel: cancel}
	go c.runSocket(ctx, s, u, protos, l)
	return s, nil
}

func (c *Client) runSocket(ctx context.Context, s *Socket, u string, protos []string, l SocketListener) {
	dctx, dcancel := context.WithTimeout(ctx, 30*time.Second)
	conn, resp, err := websocket.Dial(dctx, u, &websocket.DialOptions{HTTPClient: c.http, Subprotocols: protos})
	dcancel()
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		l.OnError(wrapNet(err).Error(), strings.Contains(err.Error(), ErrPinMismatch), status)
		l.OnClose(1006, "")
		return
	}
	conn.SetReadLimit(maxResponseBytes)
	s.wmu.Lock()
	s.conn = conn
	s.wmu.Unlock()
	l.OnOpen()
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			code := 1006
			reason := ""
			if ce := websocket.CloseStatus(err); ce != -1 {
				code = int(ce)
				var cerr websocket.CloseError
				if errors.As(err, &cerr) {
					reason = cerr.Reason
				}
			} else if ctx.Err() != nil {
				code = 1000
			} else {
				l.OnError(wrapNet(err).Error(), false, 0)
			}
			l.OnClose(code, reason)
			return
		}
		if typ == websocket.MessageText {
			l.OnText(string(data))
		} else {
			l.OnBinary(data)
		}
	}
}

func (s *Socket) get() (*websocket.Conn, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if s.conn == nil {
		return nil, errors.New("socket not open")
	}
	return s.conn, nil
}

func (s *Socket) SendText(text string) error {
	conn, err := s.get()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return conn.Write(ctx, websocket.MessageText, []byte(text))
}

func (s *Socket) SendBinary(data []byte) error {
	conn, err := s.get()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return conn.Write(ctx, websocket.MessageBinary, data)
}

// Close ends the socket with a close frame when one can be sent.
func (s *Socket) Close(code int, reason string) {
	s.wmu.Lock()
	conn := s.conn
	s.wmu.Unlock()
	if conn != nil {
		if len(reason) > 120 {
			reason = reason[:120]
		}
		_ = conn.Close(websocket.StatusCode(code), reason)
	}
	s.cancel()
}

// Close releases the tunnel and idle connections.
func (c *Client) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.mu.Unlock()
	c.http.CloseIdleConnections()
	_ = c.tc.Close()
}

func tunnelURL(scheme, path string) (string, error) {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return "", errors.New("path must be absolute")
	}
	u := url.URL{Scheme: scheme, Host: "solco.tailcat"}
	parsed, err := url.ParseRequestURI(path)
	if err != nil {
		return "", errors.New("invalid path")
	}
	u.Path, u.RawQuery = parsed.Path, parsed.RawQuery
	return u.String(), nil
}

func parseHeaders(s string) (map[string]string, error) {
	out := map[string]string{}
	if strings.TrimSpace(s) == "" {
		return out, nil
	}
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil, errors.New("invalid headers")
	}
	return out, nil
}

func ms(v int64) time.Duration {
	if v <= 0 {
		v = 15000
	}
	return time.Duration(v) * time.Millisecond
}

// wrapNet keeps the pin mismatch recognisable and strips anything that could carry the tailcat address.
func wrapNet(err error) error {
	if strings.Contains(err.Error(), ErrPinMismatch) {
		return errors.New(ErrPinMismatch)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.New("timeout")
	}
	if errors.Is(err, context.Canceled) {
		return errors.New("cancelled")
	}
	return fmt.Errorf("tunnel: %s", truncate(err.Error(), 160))
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
