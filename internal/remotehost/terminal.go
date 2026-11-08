package remotehost

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
)

/**
 * [INPUT]: 依赖 Backend.ObserveCommand/SendPaneInput、server.go 的 currentTree/trackConn/guard、coder/websocket
 * [OUTPUT]: 对外提供 GET /api/v3/terminals/{terminalID}/stream 的 WebSocket 处理器与 maxTerminalsPerDevice
 * [POS]: 手机终端流：每个 WS 一个只读 `herdr terminal session observe` 子进程（Herdr 以手机自己的栅格渲染 ANSI 帧，不占 controller 位、不重排 Mac 窗格、永不 attach/takeover），帧转发为「JSON 头文本帧 + ANSI 二进制帧」；首条文本帧（布局已知时）`{t:pane,cols,rows}` 告知 Mac 窗格栅格（Herdr observer 只是窗格屏幕的左上窗口，不重排、不跟光标），窗格被桌面改尺寸时每 3 s 补发；输入只走 Herdr 的 pane.send_input；resize = 用新栅格重启 observe；`?mode=control`（2026-11-07 手机接管，需 ControlBackend + ScopeTerminalInput，见 terminal_control.go）改走显式 takeover 的 controller：PTY 取手机栅格、resize 写命令不重启、`scroll{lines}` 滚 Herdr 视口，不发 pane 消息；terminalID 必须先在当前树里解出 pane，未知串永不进 argv
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

// defaultPaneWatch is how often an open terminal re-checks its pane's size (Server.paneWatch overrides it in tests).
const defaultPaneWatch = 3 * time.Second

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
	// Lines is a takeover scroll step: > 0 shows older output, < 0 newer.
	Lines int `json:"lines"`
	// Col/Row are a takeover tap's or mouse event's 0-based cell (nil = absent); Action is the mouse event's
	// phase: down | drag | up.
	Col    *int   `json:"col"`
	Row    *int   `json:"row"`
	Action string `json:"a"`
}

// pointer is one takeover pointer event at a 0-based cell: a tap (action "click" = left down+up) or one phase of the
// phone's mouse mode (down | drag | up).
type pointer struct {
	action   string
	col, row int
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
	// ?mode=control is the phone's explicit takeover (terminal_control.go); it needs the backend's
	// controller command and the same scope as typing.
	var control ControlBackend
	if r.URL.Query().Get("mode") == "control" {
		cb, ok := s.opts.Backend.(ControlBackend)
		if !ok {
			writeError(w, http.StatusBadRequest, "control_unsupported", "")
			return
		}
		if scopesFor(dev)&ScopeTerminalInput == 0 {
			writeError(w, http.StatusForbidden, "forbidden", "")
			return
		}
		control = cb
	}
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
	var scroll chan int
	var tap chan pointer
	if control != nil {
		scroll = make(chan int, 16)
		tap = make(chan pointer, 16)
	}
	go s.readClient(ctx, cancel, conn, dev, instance, paneID, resize, scroll, tap)
	slog.Info("remote host terminal open", "device", dev.ID, "terminal", terminalID, "control", control != nil)
	defer slog.Info("remote host terminal closed", "device", dev.ID, "terminal", terminalID)

	var reason string
	if control != nil {
		reason = s.streamControl(ctx, conn, control, instance, paneID, terminalID, g, resize, scroll, tap)
	} else {
		// Herdr's observer is a top-left window onto the Mac pane's own screen (it neither reflows nor follows the
		// cursor), so the phone needs the pane's cell size to show all of it: sent first, and again when it moves.
		last := s.announcePane(ctx, conn, instance, paneID, grid{})
		go s.watchPane(ctx, conn, instance, paneID, last)
		reason = s.streamTerminal(ctx, conn, instance, terminalID, g, resize)
	}
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

// paneGrid is the Mac pane's cell size from Herdr's layout rect, clamped into the stream's grid limits;
// ok is false while Herdr has no layout for the pane.
func (s *Server) paneGrid(instance, paneID string) (grid, bool) {
	p, err := s.opts.Backend.Projection(instance)
	if err != nil {
		return grid{}, false
	}
	for _, l := range p.Layouts {
		for _, lp := range l.Panes {
			if lp.PaneID != paneID || lp.Rect.Width == 0 || lp.Rect.Height == 0 {
				continue
			}
			return grid{
				cols: min(max(int(lp.Rect.Width), minCols), maxCols),
				rows: min(max(int(lp.Rect.Height), minRows), maxRows),
			}, true
		}
	}
	return grid{}, false
}

// announcePane sends `{"t":"pane","cols","rows"}` when the pane's size is known and differs from last, and
// returns the size now in force (last when nothing was sent).
func (s *Server) announcePane(ctx context.Context, conn *websocket.Conn, instance, paneID string, last grid) grid {
	g, ok := s.paneGrid(instance, paneID)
	if !ok || g == last {
		return last
	}
	if writeFrame(ctx, conn, map[string]any{"t": "pane", "cols": g.cols, "rows": g.rows}) != nil {
		return last
	}
	return g
}

// watchPane re-announces the pane's size when the desktop window moves it.
func (s *Server) watchPane(ctx context.Context, conn *websocket.Conn, instance, paneID string, last grid) {
	every := s.paneWatch
	if every <= 0 {
		every = defaultPaneWatch
	}
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			last = s.announcePane(ctx, conn, instance, paneID, last)
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

	return pumpFrames(ctx, conn, stdout)
}

// readClient serves the phone's upstream messages until the socket closes.
func (s *Server) readClient(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn, dev Device, instance, paneID string, resize chan grid, scroll chan<- int, tap chan<- pointer) {
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
		case "scroll":
			if scroll != nil && msg.Lines != 0 {
				select {
				case scroll <- msg.Lines:
				default: // a burst the writer has not drained yet: dropping a step beats blocking the reader
				}
			}
		case "tap", "mouse":
			if tap == nil || msg.Col == nil || msg.Row == nil || *msg.Col < 0 || *msg.Col >= maxCols || *msg.Row < 0 || *msg.Row >= maxRows {
				continue
			}
			ev := pointer{action: "click", col: *msg.Col, row: *msg.Row}
			if msg.T == "mouse" {
				if msg.Action != "down" && msg.Action != "drag" && msg.Action != "up" {
					continue
				}
				ev.action = msg.Action
			}
			if ev.action == "drag" { // a drag step may be dropped under a burst; a press or release must not be
				select {
				case tap <- ev:
				default:
				}
				continue
			}
			select {
			case tap <- ev:
			case <-ctx.Done():
			case <-time.After(pointerWait):
			}
		case "input":
			s.handleInput(ctx, conn, dev, instance, paneID, msg)
			if scroll != nil { // typing returns a scrolled-back viewport to the live screen (a takeover only)
				select {
				case scroll <- snapToLive:
				default:
				}
			}
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
	var err error
	if lb, ok := s.opts.Backend.(LiteralBackend); ok && len(msg.Keys) == 0 && typedLiterally(msg.Text) {
		err = lb.SendPaneText(instance, paneID, msg.Text)
	} else {
		err = s.opts.Backend.SendPaneInput(ictx, instance, paneID, msg.Text, msg.Keys)
	}
	if err != nil {
		slog.Warn("remote host input refused", "device", dev.ID, "pane", paneID, "error", err)
		_ = writeFrame(ctx, conn, map[string]string{"t": "input.error", "code": "refused", "message": truncate(err.Error(), 200)})
	}
}

// LiteralBackend is the optional Backend capability for typing: Herdr pane.send_text writes the bytes into the PTY
// as keystrokes, where pane.send_input wraps them as a bracketed paste (probed on Herdr 0.9.5: `3jx` sent that way
// is inserted into an nvim buffer, sent literally it runs as Normal-mode commands). A backend without it keeps
// send_input for everything.
type LiteralBackend interface {
	SendPaneText(instance, paneID, text string) error
}

// typedLiterally says whether a terminal input is something a person typed (key by key, an IME commit, a single
// Enter) rather than a pasted block: text with a line break inside stays on the paste path, so a multi-line paste
// into a shell cannot run its first line before the rest has arrived.
func typedLiterally(text string) bool {
	return len(text) == 1 || !strings.ContainsAny(text, "\r\n")
}

// pointerWait bounds how long a press/release waits for the controller writer before it is dropped.
const pointerWait = 500 * time.Millisecond

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
