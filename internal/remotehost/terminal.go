package remotehost

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"os/exec"
	"regexp"
	"strconv"
	"time"

	"github.com/coder/websocket"
)

/**
 * [INPUT]: 依赖 Backend.ObserveCommand/SendPaneInput、server.go 的 currentTree/trackConn/guard、coder/websocket
 * [OUTPUT]: 对外提供 GET /api/v3/terminals/{terminalID}/stream 的 WebSocket 处理器与 maxTerminalsPerDevice
 * [POS]: 手机终端流：每个 WS 一个只读 `herdr terminal session observe` 子进程（Herdr 以手机自己的栅格渲染 ANSI 帧，不占 controller 位、不重排 Mac 窗格、永不 attach/takeover），帧转发为「JSON 头文本帧 + ANSI 二进制帧」；输入只走 Herdr 的 pane.send_input；resize = 用新栅格重启 observe；terminalID 必须先在当前树里解出 pane，未知串永不进 argv
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const (
	maxTerminalsPerDevice = 2
	maxInputText          = 64 << 10
	maxInputKeys          = 32
	maxFrameLine          = 8 << 20
	minCols, maxCols      = 10, 400
	minRows, maxRows      = 3, 200
	resizeDebounce        = 100 * time.Millisecond
	inputTimeout          = 10 * time.Second
	closeReasonMax        = 100
	// statusTerminalGone is the application close code for "this terminal no
	// longer exists / could not be observed".
	statusTerminalGone websocket.StatusCode = 4404
)

var keyName = regexp.MustCompile(`^[A-Za-z0-9+_-]{1,32}$`)

type grid struct{ cols, rows int }

func parseGrid(r *http.Request) (grid, bool) {
	cols, err1 := strconv.Atoi(r.URL.Query().Get("cols"))
	rows, err2 := strconv.Atoi(r.URL.Query().Get("rows"))
	g := grid{cols, rows}
	return g, err1 == nil && err2 == nil && g.valid()
}

func (g grid) valid() bool {
	return g.cols >= minCols && g.cols <= maxCols && g.rows >= minRows && g.rows <= maxRows
}

type clientMessage struct {
	T    string   `json:"t"`
	Text string   `json:"text"`
	Keys []string `json:"keys"`
	Cols int      `json:"cols"`
	Rows int      `json:"rows"`
}

type observeLine struct {
	Type   string `json:"type"`
	Bytes  string `json:"bytes"`
	Full   bool   `json:"full"`
	Seq    uint64 `json:"seq"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Reason string `json:"reason"`
}

func (s *Server) handleTerminal(w http.ResponseWriter, r *http.Request, dev Device) {
	instance, ok := s.resolveAnyInstance(w, r)
	if !ok {
		return
	}
	g, ok := parseGrid(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_grid", "")
		return
	}
	terminalID := r.PathValue("terminalID")
	tree, err := s.currentTree(instance)
	if err != nil {
		slog.Warn("remote host terminal: tree failed", "instance", instance, "error", err)
		writeError(w, http.StatusBadGateway, "backend_unavailable", "")
		return
	}
	paneID, ok := tree.paneForTerminal(terminalID)
	if !ok {
		writeError(w, http.StatusNotFound, "unknown_terminal", "")
		return
	}
	s.mu.Lock()
	if s.terminals[dev.ID] >= maxTerminalsPerDevice {
		s.mu.Unlock()
		writeError(w, http.StatusTooManyRequests, "too_many_terminals", "")
		return
	}
	s.terminals[dev.ID]++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if s.terminals[dev.ID]--; s.terminals[dev.ID] <= 0 {
			delete(s.terminals, dev.ID)
		}
		s.mu.Unlock()
	}()

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{subprotocolBearer}})
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(2 * maxInputText)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	defer s.trackConn(dev.ID, func() { _ = conn.Close(websocket.StatusPolicyViolation, "revoked"); cancel() })()

	resize := make(chan grid, 1)
	go s.readClient(ctx, cancel, conn, dev, instance, paneID, resize)
	slog.Info("remote host terminal open", "device", dev.ID, "terminal", terminalID)
	defer slog.Info("remote host terminal closed", "device", dev.ID, "terminal", terminalID)

	reason := s.streamTerminal(ctx, conn, instance, terminalID, g, resize)
	if reason != "" {
		_ = conn.Close(statusTerminalGone, truncate(reason, closeReasonMax))
		return
	}
	_ = conn.Close(websocket.StatusNormalClosure, "")
}

// streamTerminal runs observe children until the socket ends. A resize
// restarts the child on the new grid. It returns a non-empty close reason when
// Herdr ended the stream (terminal gone / observe refused).
func (s *Server) streamTerminal(ctx context.Context, conn *websocket.Conn, instance, terminalID string, g grid, resize <-chan grid) string {
	for {
		child, stop := context.WithCancel(ctx)
		done := make(chan string, 1)
		go func() { done <- s.runObserve(child, conn, instance, terminalID, g) }()
		select {
		case next := <-resize:
			next = coalesceResize(next, resize)
			stop()
			<-done
			if next != g {
				g = next
			}
			continue
		case reason := <-done:
			stop()
			return reason
		case <-ctx.Done():
			stop()
			<-done
			return ""
		}
	}
}

// coalesceResize waits a beat so the many resizes of a keyboard animation
// become one restart.
func coalesceResize(first grid, more <-chan grid) grid {
	latest := first
	for {
		select {
		case g := <-more:
			latest = g
		case <-time.After(resizeDebounce):
			return latest
		}
	}
}

// runObserve runs one observe child to its end, forwarding frames. It returns
// the close reason when Herdr ended the stream, "" for a cancelled context.
func (s *Server) runObserve(ctx context.Context, conn *websocket.Conn, instance, terminalID string, g grid) string {
	argv, err := s.opts.Backend.ObserveCommand(instance, terminalID, g.cols, g.rows)
	if err != nil {
		slog.Warn("remote host observe command failed", "error", err)
		return "terminal_unavailable"
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.WaitDelay = 2 * time.Second
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "terminal_unavailable"
	}
	if err := cmd.Start(); err != nil {
		slog.Warn("remote host observe start failed", "error", err)
		return "terminal_unavailable"
	}
	defer func() { _ = cmd.Wait() }()

	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64<<10), maxFrameLine)
	for sc.Scan() {
		var line observeLine
		if json.Unmarshal(sc.Bytes(), &line) != nil {
			continue
		}
		switch line.Type {
		case "terminal.frame":
			raw, err := base64.StdEncoding.DecodeString(line.Bytes)
			if err != nil {
				continue
			}
			head, _ := json.Marshal(map[string]any{"t": "frame", "seq": line.Seq, "full": line.Full, "w": line.Width, "h": line.Height})
			if writeBytes(ctx, conn, websocket.MessageText, head) != nil || writeBytes(ctx, conn, websocket.MessageBinary, raw) != nil {
				return ""
			}
		case "terminal.closed":
			return "terminal_closed"
		}
	}
	if ctx.Err() != nil {
		return ""
	}
	return "terminal_closed"
}

// readClient serves the phone's upstream messages until the socket closes.
func (s *Server) readClient(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn, dev Device, instance, paneID string, resize chan grid) {
	defer cancel()
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var msg clientMessage
		if json.Unmarshal(data, &msg) != nil {
			continue
		}
		switch msg.T {
		case "ping":
			_ = writeFrame(ctx, conn, map[string]string{"t": "pong"})
		case "resize":
			g := grid{msg.Cols, msg.Rows}
			if !g.valid() {
				continue
			}
			select { // keep only the latest pending size
			case <-resize:
			default:
			}
			resize <- g
		case "input":
			s.handleInput(ctx, conn, dev, instance, paneID, msg)
		}
	}
}

func (s *Server) handleInput(ctx context.Context, conn *websocket.Conn, dev Device, instance, paneID string, msg clientMessage) {
	fail := func(code string) { _ = writeFrame(ctx, conn, map[string]string{"t": "input.error", "code": code}) }
	if scopesFor(dev)&ScopeTerminalInput == 0 {
		fail("forbidden")
		return
	}
	if len(msg.Text) > maxInputText || len(msg.Keys) > maxInputKeys || (msg.Text == "" && len(msg.Keys) == 0) {
		fail("bad_input")
		return
	}
	for _, k := range msg.Keys {
		if !keyName.MatchString(k) {
			fail("bad_input")
			return
		}
	}
	ictx, cancel := context.WithTimeout(ctx, inputTimeout)
	defer cancel()
	if err := s.opts.Backend.SendPaneInput(ictx, instance, paneID, msg.Text, msg.Keys); err != nil {
		slog.Warn("remote host input refused", "device", dev.ID, "pane", paneID, "error", err)
		_ = writeFrame(ctx, conn, map[string]string{"t": "input.error", "code": "refused", "message": truncate(err.Error(), 200)})
	}
}

func writeBytes(ctx context.Context, conn *websocket.Conn, typ websocket.MessageType, b []byte) error {
	ctx, cancel := context.WithTimeout(ctx, wsWriteTimeout)
	defer cancel()
	return conn.Write(ctx, typ, b)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
