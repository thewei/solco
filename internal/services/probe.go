/**
 * [INPUT]: stdlib net/strings/syscall/context
 * [OUTPUT]: ProbeClass/ProbeResult、ProbePort
 * [POS]: 服务健康探测（2026-11-03 用户拍板，修正 services-surfaces 的"健康检查"
 *   非目标）：对 pane 归因服务的监听端口做一次轻量本地探测，给侧栏服务方块
 *   提供绿/橙/红的判据。语义：TCP 连上且应答 HTTP 状态行 → ProbeHTTP（2xx/
 *   3xx/4xx 都是活，5xx 是应用层错误=红）；连上但应答不是 HTTP（postgres/
 *   redis 等裸 TCP）→ ProbeAlive（活）；连接被拒/重置 → ProbeDown（进程不在
 *   了，交给扫描状态机分橙/红）；连接或读超时 → ProbeHung（端口占着但不收
 *   连接 = 挂起，橙，连续两轮转红）。IPv4 失败回退 IPv6。每次探测 ≤1.4s，
 *   由调用方绑定在现有扫描超时内；分类 sticky 化由调用方持有（HTTP 端口才发
 *   GET，避免对裸 TCP 服务灌垃圾字节）。
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */
package services

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ProbeClass grades what one local probe learned about a listening port.
type ProbeClass uint8

const (
	// ProbeAlive: TCP connected and the answer is not HTTP — a bare TCP
	// service (postgres, redis) that accepts connections is alive.
	ProbeAlive ProbeClass = iota
	// ProbeHTTP: an HTTP service answered with the parsed status Code.
	ProbeHTTP
	// ProbeDown: connection refused or reset — nothing accepted.
	ProbeDown
	// ProbeHung: connect or read timed out — the port is held but the
	// server is not serving.
	ProbeHung
)

func (c ProbeClass) String() string {
	switch c {
	case ProbeAlive:
		return "alive"
	case ProbeHTTP:
		return "http"
	case ProbeDown:
		return "down"
	case ProbeHung:
		return "hung"
	default:
		return "unknown"
	}
}

// ProbeResult is one probe's verdict. Code carries the HTTP status when
// Class is ProbeHTTP.
type ProbeResult struct {
	Class ProbeClass
	Code  int
}

// Probe deadlines: dev servers answer in milliseconds; anything past these
// is the hung case, and the caller's scan deadline caps the whole batch.
const (
	probeDialTimeout = 700 * time.Millisecond
	probeReadTimeout = 700 * time.Millisecond
	probeStatusLimit = 128 // bytes of the status line we bother reading
)

// ProbePort probes one loopback listener. It connects (IPv4 first, IPv6 as
// the fallback for ::-bound servers), sends one minimal HTTP request, and
// classifies the answer. The request is deliberately tiny and one-shot
// (HTTP/1.0 + Connection: close): dev servers log requests all day, and we
// never pipeline or retry.
func ProbePort(ctx context.Context, port uint16) ProbeResult {
	dialer := net.Dialer{Timeout: probeDialTimeout}
	address := func(family string) string {
		return net.JoinHostPort(family, strconv.Itoa(int(port)))
	}
	conn, err := dialer.DialContext(ctx, "tcp4", address("127.0.0.1"))
	if isRefused(err) {
		// A ::-only listener refuses 127.0.0.1; try the IPv6 loopback
		// once before calling it down.
		conn, err = dialer.DialContext(ctx, "tcp6", address("::1"))
	}
	if err != nil {
		if isRefused(err) || errors.Is(err, syscall.ECONNRESET) {
			return ProbeResult{Class: ProbeDown}
		}
		return ProbeResult{Class: ProbeHung}
	}
	defer conn.Close()
	deadline := time.Now().Add(probeReadTimeout)
	if ctxDeadline := ctxDeadlineOr(ctx, deadline); ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	_ = conn.SetDeadline(deadline)

	// One minimal request; any HTTP server answers with a status line,
	// anything else answers garbage (alive) or nothing (hung).
	request := "GET / HTTP/1.0\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	if _, err := conn.Write([]byte(request)); err != nil {
		return ProbeResult{Class: ProbeHung}
	}
	buffer := make([]byte, probeStatusLimit)
	read, err := conn.Read(buffer)
	if err != nil && read == 0 {
		return ProbeResult{Class: ProbeHung}
	}
	if code, ok := parseHTTPStatus(string(buffer[:read])); ok {
		return ProbeResult{Class: ProbeHTTP, Code: code}
	}
	return ProbeResult{Class: ProbeAlive}
}

// ProbePortTCP is the connect-only probe for ports known to speak a bare
// TCP protocol: it never sends bytes, so database servers see a silent
// connect instead of a stray GET. Alive/Down/Hung as in ProbePort.
func ProbePortTCP(ctx context.Context, port uint16) ProbeResult {
	dialer := net.Dialer{Timeout: probeDialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(int(port))))
	if isRefused(err) {
		conn, err = dialer.DialContext(ctx, "tcp6", net.JoinHostPort("::1", strconv.Itoa(int(port))))
	}
	if err != nil {
		if isRefused(err) || errors.Is(err, syscall.ECONNRESET) {
			return ProbeResult{Class: ProbeDown}
		}
		return ProbeResult{Class: ProbeHung}
	}
	conn.Close()
	return ProbeResult{Class: ProbeAlive}
}

// parseHTTPStatus reads "HTTP/1.x 200 …" out of the first bytes a server
// sends. Anything else is a bare TCP service answering its own protocol.

// parseHTTPStatus reads "HTTP/1.x 200 …" out of the first bytes a server
// sends. Anything else is a bare TCP service answering its own protocol.
func parseHTTPStatus(head string) (int, bool) {
	line, _, _ := strings.Cut(head, "\n")
	line = strings.TrimRight(line, "\r")
	parts := strings.SplitN(line, " ", 3)
	if len(parts) < 2 || !strings.HasPrefix(parts[0], "HTTP/") {
		return 0, false
	}
	var code int
	if _, err := fmt.Sscanf(parts[1], "%d", &code); err != nil || code < 100 || code > 599 {
		return 0, false
	}
	return code, true
}

// isRefused reports connection-refused-shaped errors (the nothing-listens
// case; the IPv6 fallback turns a family mismatch into one more try).
func isRefused(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED)
}

// ctxDeadlineOr returns ctx's deadline, or fallback when it has none.
func ctxDeadlineOr(ctx context.Context, fallback time.Time) time.Time {
	if deadline, ok := ctx.Deadline(); ok {
		return deadline
	}
	return fallback
}
