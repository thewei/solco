package voice

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/thewei/solco/internal/usage"
)

/**
 * [INPUT]: 本机 Codex 的登录态文件 `$CODEX_HOME|~/.codex/auth.json`（auth_mode=chatgpt）；需要刷新时的 `codex app-server`
 * [OUTPUT]: ChatGPT 转写器（POST chatgpt.com/backend-api/transcribe）、Status（只含状态与套餐名，不含令牌）
 * [POS]: Settings → Voice 的第二来源。ChatGPT 桌面端听写走同一接口（2026-10-31 解包 + 实测 200）；只读、不刷新、令牌不落盘不进日志
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// DefaultEndpoint is the ChatGPT backend base the desktop app's dictation calls.
const DefaultEndpoint = "https://chatgpt.com/backend-api"

// The request carries the identity the ChatGPT desktop app itself sends for
// this call (unpacked 2026-10-31): `User-Agent: Codex Desktop/<app version>
// (Mac OS; <arch>)`, `originator: Codex Desktop`, `OAI-Product-Sku: CODEX`.
// chatgpt.com sits behind Cloudflare, which answers 403 (an HTML challenge)
// to library default agents such as Go-http-client even with a valid token;
// the app's own shape is what it expects. The version is read from the
// installed app, with the verified one as a fallback.
const (
	desktopFallbackVersion = "26.1023.50603"
	desktopOriginator      = "Codex Desktop"
	desktopProductSKU      = "CODEX"
)

// desktopInfoPlist is where the desktop app's version lives (a var for tests).
var desktopInfoPlist = "/Applications/ChatGPT.app/Contents/Info.plist"

var desktopVersionRE = regexp.MustCompile(`<key>CFBundleShortVersionString</key>\s*<string>([0-9][0-9.]*)</string>`)

func desktopUserAgent() string {
	version := desktopFallbackVersion
	if raw, err := os.ReadFile(desktopInfoPlist); err == nil {
		if m := desktopVersionRE.FindSubmatch(raw); m != nil {
			version = string(m[1])
		}
	}
	arch := runtime.GOARCH
	if arch == "amd64" {
		arch = "x64"
	}
	return "Codex Desktop/" + version + " (Mac OS; " + arch + ")"
}

// State is where the local Codex sign-in stands.
type State int

const (
	// SignedOut: no auth file, an API-key login, or no tokens in it.
	SignedOut State = iota
	// Expired: the access token's own exp has passed.
	Expired
	// Ready: a ChatGPT sign-in with a live access token.
	Ready
)

// Status is what the UI may show: never a token.
type Status struct {
	State State
	Plan  string // "plus", "pro", … from the token's own claim; empty if absent
}

var (
	// ErrSignedOut: Codex holds no ChatGPT sign-in Solco can use.
	ErrSignedOut = errors.New("no ChatGPT sign-in found — sign in to Codex first")
	// ErrExpired: the sign-in expired and Codex could not renew it.
	ErrExpired = errors.New("ChatGPT sign-in expired — open Codex to sign in again")
	// ErrBlocked: chatgpt.com's edge refused the request (an HTML 403).
	ErrBlocked = errors.New("ChatGPT blocked the request — try again later")
	// ErrLimited: the plan's dictation allowance is used up (HTTP 429).
	ErrLimited = errors.New("ChatGPT transcription limit reached — try again later")
)

// ChatGPT transcribes with the sign-in the Codex CLI/app already holds.
// Tokens are read per call and never retained or written.
type ChatGPT struct {
	// Home is the Codex home; empty = $CODEX_HOME or ~/.codex.
	Home string
	// Endpoint is the backend base; empty = DefaultEndpoint.
	Endpoint string
	Client   *http.Client
	// Refresh asks Codex itself to renew its tokens (it owns the rotating
	// refresh token); nil = run `codex app-server`. Tests inject a fake.
	Refresh func(ctx context.Context) error
	Now     func() time.Time
}

type authFile struct {
	Mode   string `json:"auth_mode"`
	Tokens struct {
		Access  string `json:"access_token"`
		Account string `json:"account_id"`
	} `json:"tokens"`
}

type claims struct {
	Exp  int64 `json:"exp"`
	Auth struct {
		Plan string `json:"chatgpt_plan_type"`
	} `json:"https://api.openai.com/auth"`
}

func (c *ChatGPT) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *ChatGPT) authPath() string {
	home := c.Home
	if home == "" {
		home = os.Getenv("CODEX_HOME")
	}
	if home == "" {
		user, _ := os.UserHomeDir()
		home = filepath.Join(user, ".codex")
	}
	return filepath.Join(home, "auth.json")
}

// credentials reads the file once. The returned token is for the caller's
// single request only.
func (c *ChatGPT) credentials() (token, account string, status Status) {
	raw, err := os.ReadFile(c.authPath())
	if err != nil {
		return "", "", Status{State: SignedOut}
	}
	var file authFile
	if json.Unmarshal(raw, &file) != nil || file.Mode != "chatgpt" || file.Tokens.Access == "" || file.Tokens.Account == "" {
		return "", "", Status{State: SignedOut}
	}
	var parsed claims
	parts := strings.Split(file.Tokens.Access, ".")
	if len(parts) != 3 {
		return "", "", Status{State: SignedOut}
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil || json.Unmarshal(payload, &parsed) != nil {
		return "", "", Status{State: SignedOut}
	}
	status = Status{State: Ready, Plan: parsed.Auth.Plan}
	// A minute of slack so a token does not die mid-upload.
	if parsed.Exp != 0 && c.now().Add(time.Minute).Unix() >= parsed.Exp {
		status.State = Expired
	}
	return file.Tokens.Access, file.Tokens.Account, status
}

// Status reports the sign-in without exposing it.
func (c *ChatGPT) Status() Status {
	_, _, status := c.credentials()
	return status
}

// Transcribe uploads one recording and returns the text. An expired token is
// renewed by Codex (never by Solco) and the file re-read once.
func (c *ChatGPT) Transcribe(ctx context.Context, audio []byte, mime string) (string, error) {
	token, account, status := c.credentials()
	switch status.State {
	case SignedOut:
		return "", ErrSignedOut
	case Expired:
		if err := c.renew(ctx); err != nil {
			return "", ErrExpired
		}
		if token, account, status = c.credentials(); status.State != Ready {
			return "", ErrExpired
		}
	}
	text, code, err := c.upload(ctx, token, account, audio, mime)
	if code == http.StatusUnauthorized {
		// The server disagrees with the token's own exp: one renewal, one retry.
		if c.renew(ctx) != nil {
			return "", ErrExpired
		}
		if token, account, status = c.credentials(); status.State != Ready {
			return "", ErrExpired
		}
		text, code, err = c.upload(ctx, token, account, audio, mime)
		if code == http.StatusUnauthorized {
			return "", ErrExpired
		}
	}
	return text, err
}

func (c *ChatGPT) renew(ctx context.Context) error {
	if c.Refresh != nil {
		return c.Refresh(ctx)
	}
	return codexRefresh(ctx)
}

func (c *ChatGPT) upload(ctx context.Context, token, account string, audio []byte, mime string) (string, int, error) {
	if mime == "" {
		mime = "audio/webm"
	}
	ext := "webm"
	if _, sub, ok := strings.Cut(mime, "/"); ok {
		ext, _, _ = strings.Cut(sub, ";")
	}
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="codex.%s"`, ext))
	header.Set("Content-Type", mime)
	part, err := writer.CreatePart(header)
	if err == nil {
		_, err = part.Write(audio)
	}
	if err == nil {
		err = writer.Close()
	}
	if err != nil {
		return "", 0, err
	}
	endpoint := c.Endpoint
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(endpoint, "/")+"/transcribe", body)
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("ChatGPT-Account-Id", account)
	req.Header.Set("User-Agent", desktopUserAgent())
	req.Header.Set("originator", desktopOriginator)
	req.Header.Set("OAI-Product-Sku", desktopProductSKU)
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		// url.Error carries the URL only, never headers.
		return "", 0, errors.New("could not reach ChatGPT for transcription")
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", resp.StatusCode, err
	}
	switch {
	case resp.StatusCode == http.StatusOK:
		var payload struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			return "", resp.StatusCode, errors.New("unexpected response from ChatGPT")
		}
		return strings.TrimSpace(payload.Text), resp.StatusCode, nil
	case resp.StatusCode == http.StatusTooManyRequests:
		return "", resp.StatusCode, ErrLimited
	case resp.StatusCode == http.StatusForbidden && bytes.Contains(bytes.ToLower(raw), []byte("<html")):
		// An HTML 403 is the edge (Cloudflare) refusing the request, not the
		// account: renewing the sign-in cannot help, so do not claim it expired.
		return "", http.StatusBadGateway, ErrBlocked
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return "", resp.StatusCode, ErrExpired
	case resp.StatusCode == http.StatusBadRequest && bytes.Contains(raw, []byte("audio_too_short")):
		return "", resp.StatusCode, errors.New("recording too short")
	}
	// Status only: the body may echo request details.
	return "", resp.StatusCode, fmt.Errorf("ChatGPT transcription failed (%d)", resp.StatusCode)
}

// codexRefresh runs a short-lived `codex app-server` and asks it to renew the
// sign-in itself (account/read with refreshToken), so Codex — the owner of the
// rotating refresh token — rewrites its own auth file.
func codexRefresh(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	binary, err := usage.FindTool("codex")
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, binary, "app-server")
	cmd.Env = usage.ToolEnv(binary)
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	defer func() {
		_ = in.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	return refreshExchange(ctx, in, out)
}

// refreshExchange speaks initialize → initialized → account/read{refreshToken}.
func refreshExchange(ctx context.Context, in io.Writer, out io.Reader) error {
	send := func(v any) error {
		line, err := json.Marshal(v)
		if err != nil {
			return err
		}
		_, err = in.Write(append(line, '\n'))
		return err
	}
	type reply struct {
		ID    *int `json:"id"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	replies := make(chan reply, 4)
	go func() {
		defer close(replies)
		scanner := bufio.NewScanner(out)
		scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
		for scanner.Scan() {
			var r reply
			if json.Unmarshal(scanner.Bytes(), &r) == nil && r.ID != nil {
				replies <- r
			}
		}
	}()
	wait := func(id int) error {
		for {
			select {
			case <-ctx.Done():
				return errors.New("codex did not answer")
			case r, ok := <-replies:
				if !ok {
					return errors.New("codex closed")
				}
				if *r.ID != id {
					continue
				}
				if r.Error != nil {
					return errors.New("codex refused the refresh")
				}
				return nil
			}
		}
	}
	if err := send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"clientInfo": map[string]any{"name": "solco", "version": "1"}}}); err != nil {
		return err
	}
	if err := wait(1); err != nil {
		return err
	}
	_ = send(map[string]any{"jsonrpc": "2.0", "method": "initialized"})
	if err := send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "account/read",
		"params": map[string]any{"refreshToken": true}}); err != nil {
		return err
	}
	return wait(2)
}
