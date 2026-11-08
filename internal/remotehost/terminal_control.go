package remotehost

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"os/exec"
	"strings"
	"time"

	"github.com/coder/websocket"
)

/**
 * [INPUT]: 依赖 terminal.go 的 grid/coalesceResize/observeLine/writeFrame、Backend（可选 ControlBackend）、Projection 的 Pane.Scroll/Agent
 * [OUTPUT]: 对外提供 ControlBackend、`?mode=control` 的终端流（能力位 terminalControl）：`{t:control,on,cols,rows|reason}` 与客户端 `scroll{lines}`/`tap{col,row}`（Herdr terminal.mouse 左键点击；手机放手后调 Options.OnControlReleased）
 * [POS]: 手机「接管」模式（2026-11-07，用户批准）：手机显式点接管后，Host 起一个可写 `herdr terminal session control --takeover`，PTY 按手机栅格重排（桌面 attach 随即被 Herdr 关掉），
 *        resize 不重启子进程而是写 terminal.resize，滚动一律交给 Herdr 自己的 terminal.scroll 按窗格实际在跑的东西路由（实测：主屏滚 scrollback、备用屏程序每条命令一个滚轮 tick、纯提示符不漏字节）——有 scrollback 范围的窗格发一条 lines=N，其余每行一条 lines=1（≤30）；不再自己拼 SGR；
 *        退出时先把 PTY 还原成 Mac 窗格栅格再 release；被桌面抢回时发 `{t:control,on:false,reason:taken_over}` 后正常关闭，手机自行改回只读观察；永不自动接管
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// ControlBackend is the optional Backend capability behind takeover: it builds
// the writable `terminal session control --takeover` child. A Host whose
// backend lacks it does not advertise `terminalControl` and refuses mode=control.
type ControlBackend interface {
	ControlCommand(instance, terminalID string, cols, rows int) ([]string, error)
}

// defaultControlStop is how long a released controller gets to exit before it is killed (Server.controlStop
// overrides it in tests, where a loaded machine may start the fake shell slowly).
const defaultControlStop = time.Second

// snapToLive travels the scroll channel when the phone typed: not a line count (no real step is this large).
const snapToLive = math.MinInt32

const (
	maxScrollLines   = 200
	maxScrollTicks   = 30
	scrollInfoMaxAge = time.Second
	closeTakenOver   = "taken_over"
	takenOverMarker  = "taken over"
)

// streamControl runs the controller child until the socket ends, the desktop
// takes the terminal back, or Herdr ends the stream. resize and scroll come
// from readClient. It returns a close reason like streamTerminal ("" = done).
func (s *Server) streamControl(ctx context.Context, conn *websocket.Conn, cb ControlBackend, instance, paneID, terminalID string, g grid, resize <-chan grid, scroll <-chan int, tap <-chan pointer) string {
	argv, err := cb.ControlCommand(instance, terminalID, g.cols, g.rows)
	if err != nil {
		slog.Warn("remote host control command failed", "error", err)
		return "terminal_unavailable"
	}
	// The child outlives ctx just long enough to give the PTY back (see finish).
	child, stop := context.WithCancel(context.Background())
	defer stop()
	cmd := exec.CommandContext(child, argv[0], argv[1:]...)
	cmd.WaitDelay = 2 * time.Second
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "terminal_unavailable"
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "terminal_unavailable"
	}
	if err := cmd.Start(); err != nil {
		slog.Warn("remote host control start failed", "error", err)
		return "terminal_unavailable"
	}
	defer func() { _ = cmd.Wait() }()

	done := make(chan string, 1)
	go func() { done <- pumpFrames(ctx, conn, stdout) }()
	if writeFrame(ctx, conn, map[string]any{"t": "control", "on": true, "cols": g.cols, "rows": g.rows}) != nil {
		stop()
		<-done
		return ""
	}

	send := func(v map[string]any) {
		b, err := json.Marshal(v)
		if err != nil {
			return
		}
		_, _ = stdin.Write(append(b, '\n'))
	}
	wait := s.controlStop
	if wait <= 0 {
		wait = defaultControlStop
	}
	scrolled := false // set once a scroll moved Herdr's viewport, so finish can park it at the bottom again
	// finish gives the Mac pane its grid back, releases the controller and waits a moment for it to exit.
	finish := func() {
		if scrolled { // the Mac's pane must not stay parked in the scrollback the phone left it in
			send(map[string]any{"type": "terminal.scroll", "direction": "down", "lines": math.MaxUint16})
		}
		if pg, ok := s.paneGrid(instance, paneID); ok {
			send(map[string]any{"type": "terminal.resize", "cols": pg.cols, "rows": pg.rows})
		}
		send(map[string]any{"type": "terminal.release"})
		_ = stdin.Close()
		// done carries exactly one value (the pump's end): take it once, whichever way the child ends.
		select {
		case <-done:
			stop()
		case <-time.After(wait):
			stop()
			<-done
		}
	}

	var info scrollInfo
	for {
		select {
		case next := <-resize:
			next = coalesceResize(next, resize)
			send(map[string]any{"type": "terminal.resize", "cols": next.cols, "rows": next.rows})
			g = next
		case n := <-scroll:
			snap := n == snapToLive
			if snap {
				n = 0
			}
			for more := true; more; { // a drag delivers many small steps: send their sum once
				select {
				case m := <-scroll:
					if m == snapToLive {
						snap = true
					} else if !snap {
						n += m
					}
				default:
					more = false
				}
			}
			if snap { // input arrived: back to the live screen once, and only if a scroll moved the viewport
				if scrolled {
					send(map[string]any{"type": "terminal.scroll", "direction": "down", "lines": math.MaxUint16})
					scrolled = false
				}
				continue
			}
			s.scrollControl(send, &info, instance, paneID, n)
			scrolled = scrolled || info.max > 0
		case p := <-tap:
			// Herdr forwards a mouse command as SGR only to a pane whose program turned mouse reporting on
			// (probed on 0.9.5: a shell prompt receives nothing), so a click or drag cannot leak bytes. A tap is a
			// left click; mouse mode sends down / drag / up as they happen (drag shows up as SGR motion with the button
			// held, for programs that asked for button-motion reporting).
			mouse := func(action string) {
				send(map[string]any{"type": "terminal.mouse", "action": action, "button": "left", "column": p.col, "row": p.row})
			}
			if p.action == "click" {
				mouse("down")
				mouse("up")
			} else {
				mouse(p.action)
			}
		case reason := <-done:
			stop()
			if reason == closeTakenOver {
				_ = writeFrame(ctx, conn, map[string]any{"t": "control", "on": false, "reason": closeTakenOver})
				return ""
			}
			return reason
		case <-ctx.Done():
			finish()
			if s.opts.OnControlReleased != nil {
				s.opts.OnControlReleased(instance, terminalID)
			}
			return ""
		}
	}
}

// scrollInfo memoizes the pane's scrollback range for scrollInfoMaxAge: a drag sends many scroll messages and
// Projection is a Herdr round trip.
type scrollInfo struct {
	at  time.Time
	max uint64
}

// scrollControl turns "lines" (> 0 = older output) into Herdr's own terminal.scroll, which routes by what the
// pane is running (probed on Herdr 0.9.5): a primary screen moves its scrollback viewport by `lines`; an
// alternate-screen program gets ONE wheel tick (or alternate-scroll arrow) per command whatever `lines` says; a plain
// prompt with nothing to scroll gets nothing (no bytes leak into a cooked tty). So a pane with a known scrollback
// range takes one command; anything else takes one `lines:1` command per line (capped), which is exact for a primary
// screen whose range Herdr has not reported yet and one tick per line for less/vim/htop/tmux/Agent TUIs alike.
func (s *Server) scrollControl(send func(map[string]any), info *scrollInfo, instance, paneID string, lines int) {
	if lines == 0 {
		return
	}
	if time.Since(info.at) > scrollInfoMaxAge {
		*info = s.readScrollInfo(instance, paneID)
	}
	dir, n := "up", lines
	if lines < 0 {
		dir, n = "down", -lines
	}
	if info.max > 0 {
		send(map[string]any{"type": "terminal.scroll", "direction": dir, "lines": min(n, maxScrollLines)})
		return
	}
	for range min(n, maxScrollTicks) {
		send(map[string]any{"type": "terminal.scroll", "direction": dir, "lines": 1})
	}
}

func (s *Server) readScrollInfo(instance, paneID string) scrollInfo {
	info := scrollInfo{at: time.Now()}
	p, err := s.opts.Backend.Projection(instance)
	if err != nil {
		return info
	}
	for _, pane := range p.Panes {
		if pane.ID == paneID && pane.Scroll != nil {
			info.max = pane.Scroll.MaxOffsetFromBottom
		}
	}
	return info
}

// pumpFrames forwards a Herdr terminal JSON-lines stream to the socket (a JSON header text frame plus the
// ANSI binary frame, as observe does) and returns why it ended: "taken_over" when the desktop took the
// terminal back, "terminal_closed" otherwise, "" for a cancelled ctx.
func pumpFrames(ctx context.Context, conn *websocket.Conn, r io.Reader) string {
	sc := bufio.NewScanner(r)
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
			if strings.Contains(line.Reason, takenOverMarker) {
				return closeTakenOver
			}
			return "terminal_closed"
		}
	}
	if ctx.Err() != nil {
		return ""
	}
	return "terminal_closed"
}
