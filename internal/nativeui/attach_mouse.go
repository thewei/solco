package nativeui

import (
	"strconv"
	"strings"
)

/**
 * [INPUT]: 依赖 terminalSurface 的 paneID/agent/agentStatus/scrollOffset/scrollMax、terminal_scroll 的 pane.scroll/send_text 派发通道
 * [OUTPUT]: 对外提供 sgrMouseSplitter（Write 侧 SGR 鼠标事件流式分离）、parseSGRMouse、isHoverMotion、isInteractiveMouseAgent、Shell.routeAttachMouse（真终端语义路由）
 * [POS]: nativeui 的 attach 鼠标语义收口（F144 真终端改造）：客户端像真终端一样把指针事件交给程序/herdr——普通 pane 滚轮走 pane.scroll 权重视口，仅交互式鼠标 Agent TUI（pi/omp）的活跃事件经白名单转发；高频无按键悬停位移（Button 35）全局物理绝缘阻断，彻底杜绝 cooked tty 回显乱码
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// sgrMouseSplitter splits an outgoing attach byte stream (what the MyGo
// terminal view writes: typed keys plus, with the daemon's forced mouse
// tracking, SGR pointer events) into plain bytes and complete SGR mouse
// sequences. Plain bytes go to the daemon's PTY stdin untouched; every
// mouse sequence goes to the router (routeAttachMouse) instead, because
// the daemon's attach input drops mouse bytes (probed 2026-10-06) and the
// client owes them their real-terminal meaning itself.
//
// The view encodes one event per write, so sequences are atomic in
// practice; a sequence cut across writes degrades to plain bytes (the
// daemon drops them, exactly today's behavior) rather than stalling typed
// input — a bare ESC keypress must never wait in a pending buffer.
type sgrMouseSplitter struct {
	pending []byte
}

const sgrMouseMaxLen = 32

// split consumes one more write; results are valid until the next call.
func (s *sgrMouseSplitter) split(chunk []byte) (plain []byte, mouse [][]byte) {
	s.pending = s.pending[:0]
	buf := s.pending
	flush := func() { plain = append(plain, buf...); buf = buf[:0] }
	for i := 0; i < len(chunk); {
		b := chunk[i]
		switch {
		case len(buf) == 0 && b == 0x1b:
			buf = append(buf, b)
		case len(buf) == 0:
			plain = append(plain, b)
		case len(buf) >= 3 && buf[1] == '[' && buf[2] == '<':
			buf = append(buf, b)
			if b == 'M' || b == 'm' {
				seq := make([]byte, len(buf))
				copy(seq, buf)
				mouse = append(mouse, seq)
				buf = buf[:0]
			} else if len(buf) > sgrMouseMaxLen || !(b == ';' || (b >= '0' && b <= '9')) {
				flush()
			}
		default:
			buf = append(buf, b)
			if (len(buf) == 2 && b != '[') || (len(buf) == 3 && b != '<') {
				flush()
			}
		}
		i++
	}
	// Never hold typed input across writes: an unfinished escape at the
	// chunk edge goes out as plain bytes.
	plain = append(plain, buf...)
	s.pending = buf[:0]
	return plain, mouse
}

// parseSGRMouse decodes one SGR mouse sequence ("\x1b[<b;x;yM" or …m)
// into its button code. wheel is true for 64/65 (wheel up/down).
func parseSGRMouse(seq string) (button int, wheel bool, ok bool) {
	rest, ok := strings.CutPrefix(seq, "\x1b[<")
	if !ok || len(rest) < 2 {
		return 0, false, false
	}
	final := rest[len(rest)-1]
	if final != 'M' && final != 'm' {
		return 0, false, false
	}
	params := strings.Split(rest[:len(rest)-1], ";")
	if len(params) != 3 {
		return 0, false, false
	}
	button, err := strconv.Atoi(params[0])
	if err != nil {
		return 0, false, false
	}
	return button, button >= 64, true
}

// isHoverMotion reports whether the event is mouse motion without any button pressed
// (button 35 in DECSET 1003 Any-Event Tracking / SGR).
func isHoverMotion(button int) bool {
	return (button &^ 28) == 35
}

// isInteractiveMouseAgent reports whether the agent is known to run as a
// full-screen alt-screen TUI that consumes mouse pointer events (pi, omp).
// Stream/scrollback CLIs (agy, claude, codex) do not track the mouse.
func isInteractiveMouseAgent(agent string) bool {
	switch strings.ToLower(agent) {
	case "pi", "omp":
		return true
	default:
		return false
	}
}

// isTerminalMouseInteractiveStatus reports whether an agent's runtime status
// represents a live, interactive session where pointer events may be consumed.
// Terminated states (done, error, released, unknown) and empty status close the gate.
func isTerminalMouseInteractiveStatus(status string) bool {
	switch status {
	case "working", "idle", "attention", "review", "pending", "running":
		return true
	default:
		return false
	}
}

// routeAttachMouse gives one SGR mouse event reported by the terminal view
// its real-terminal meaning (F144, user-approved): the client behaves like
// any terminal hosting Herdr. This function is the single narrative home
// for the probe evidence; docs/herdr-attach-mouse-forwarding.md records the
// Herdr-side spec.
//
// 1. Hover Motion (Button 35):
// Bare hover motion (cursor moving without button pressed) is unconditionally
// dropped. No coding assistant tracks passive cursor hover, and injecting
// hover coordinates into a returning shell echoes garbled escape codes (^[[<35;…M).
//
// 2. Capability & State Gating:
// Non-wheel events (clicks/drags) and verbatim wheel are forwarded via pane.send_text
// only for explicit interactive mouse TUIs (pi/omp) that have empty scrollback
// range and an active interactive status (working/idle). Stream/scrollback CLIs
// (agy, claude, codex) and exited/shell-idle panes never receive injected mouse bytes.
//
// 3. Wheel:
// Ordinary panes and scrollback-backed CLIs move Herdr's authoritative viewport
// one row per event (pane.scroll).
func (s *Shell) routeAttachMouse(surface *terminalSurface, seq string) {
	if surface == nil || surface.paneID == "" || s.activeInstance == "" {
		return
	}
	button, wheel, ok := parseSGRMouse(seq)
	if !ok {
		return
	}
	if isHoverMotion(button) {
		return
	}
	altScreenAgent := isInteractiveMouseAgent(surface.agent) &&
		surface.scrollMax == 0 &&
		isTerminalMouseInteractiveStatus(surface.agentStatus) &&
		!s.paneShellIdle[surface.paneID]
	if !wheel {
		if altScreenAgent {
			s.queueAgentMouse(surface.paneID, seq)
		}
		return
	}
	if altScreenAgent {
		s.queueAgentMouse(surface.paneID, seq)
		return
	}
	offset := int(surface.scrollOffset)
	if button == 64 { // wheel up: further back, offset_from_bottom grows
		offset++
	} else {
		offset--
	}
	if max := int(surface.scrollMax); offset > max {
		offset = max
	}
	if offset < 0 {
		offset = 0
	}
	if uint64(offset) == surface.scrollOffset {
		return
	}
	surface.scrollOffset = uint64(offset)
	s.dispatchPaneScroll(surface.paneID, offset)
}
