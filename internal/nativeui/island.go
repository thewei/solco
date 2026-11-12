package nativeui

/**
 * [INPUT]: island_model.go（islandToasts）、island_darwin.go 的原生缝、Shell.routeAgentNotification（点「Open」走与系统横幅点击同一条路由）、settings.Notifications.Island、designRoles
 * [OUTPUT]: 对外提供 islandHost、Shell.islandNotify（三个通知出口各调用一次）、Shell.islandOff（设置里关掉开关）
 * [POS]: 刘海岛 P1（2026-11-12 决策，方向 C）：开关打开后，需要你的 Agent / 完成待 review / Feedback 提问 / 自动化失败从刘海长出一条提示，7 秒后收回，指针停在上面就不收；点 Open 打开 Solco 里对应处，点 ✕ 忽略。岛不拥有状态（只读通知出口给它的一条文字）、不碰 Herdr；窗口只在第一条提示到来时才建，默认关时一行代码都不跑
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"runtime"
	"strconv"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const (
	islandShapeW  = 470 // the grown shape's width
	islandWinW    = 500 // the window: the shape plus its flare, so the flare is never clipped
	islandRowH    = 34  // the row under the housing that carries the alert
	islandShowFor = 7 * time.Second
	islandHoldFor = 2 * time.Second // re-check while the pointer rests on it
	islandHideLag = 600 * time.Millisecond
)

// islandHost shows one alert at a time in a window that grows out of the
// notch. Every method runs on the main thread (islandNotify marshals).
type islandHost struct {
	s      *Shell
	toasts islandToasts
	win    *mygo.Window
	open   bool // the shape is grown
	gen    int  // bumped on every show and close: a stale timer does nothing
	w0, h0 float32
	// cursor is the pointer's screen position; a seam for tests.
	cursor func() (x, y float32)
}

func (s *Shell) islandHostOf() *islandHost {
	if s.island == nil {
		s.island = &islandHost{s: s}
		s.island.measure()
	}
	return s.island
}

// islandNotify offers an alert to the island: a no-op while the switch is
// off. The three notification sources (Agent transitions, Feedback asks,
// failed Automation runs) call it beside the system banner; it adds no
// notification of its own.
func (s *Shell) islandNotify(a islandAlert) {
	s.applyOnWindow(func() {
		if !s.settings.Notifications.Island {
			return
		}
		s.islandHostOf().push(a)
	})
}

// islandOff takes the island away at once: the switch went off.
func (s *Shell) islandOff() {
	if s.island == nil {
		return
	}
	h := s.island
	h.toasts.Clear()
	h.open = false
	h.gen++
	if h.win != nil {
		h.win.Hide()
	}
}

// measure finds the housing of the display with the menu bar; a display
// without one gets a housing as high as the menu bar.
func (h *islandHost) measure() {
	h.w0, h.h0 = 200, 32
	if w, hh, ok := islandNotchSize(); ok {
		h.w0, h.h0 = float32(w), float32(hh)
	} else if hh := islandMenuBarHeight(); hh > 0 {
		h.h0 = float32(hh)
	}
}

func (h *islandHost) push(a islandAlert) {
	h.toasts.Push(a)
	if _, showing := h.toasts.Showing(); !showing {
		h.advance()
	} else if h.win != nil {
		h.win.Invalidate()
	}
}

// advance puts the next waiting alert on show, or closes the island when none
// waits.
func (h *islandHost) advance() {
	if _, ok := h.toasts.Next(); !ok {
		h.close()
		return
	}
	h.gen++
	h.open = true
	if win := h.ensureWindow(); win != nil {
		win.ShowInactive()
		h.pin()
		win.Invalidate()
	}
	h.arm(islandShowFor)
}

// finish takes the alert on show away and moves on.
func (h *islandHost) finish() {
	h.toasts.Finish()
	h.advance()
}

// close shrinks the shape back into the housing and hides the window once
// that is done.
func (h *islandHost) close() {
	h.open = false
	h.gen++
	if h.win == nil {
		return
	}
	h.win.Invalidate()
	g := h.gen
	if h.s.win == nil {
		return
	}
	time.AfterFunc(islandHideLag, func() {
		mygo.RunOnMain(func() {
			if h.gen == g && !h.open && h.win != nil {
				h.win.Hide()
			}
		})
	})
}

// arm schedules the end of the alert on show. Headless there is no app loop to
// hop to, so tests call expire themselves.
func (h *islandHost) arm(d time.Duration) {
	if h.s.win == nil {
		return
	}
	g := h.gen
	time.AfterFunc(d, func() { mygo.RunOnMain(func() { h.expire(g) }) })
}

// expire ends the alert of generation g unless the pointer rests on it.
func (h *islandHost) expire(g int) {
	if g != h.gen || !h.open {
		return
	}
	if h.pointerInside() {
		h.arm(islandHoldFor)
		return
	}
	h.finish()
}

// pointerInside says whether the pointer is over the grown shape (with a few
// points of slack), so a reader is never cut off.
func (h *islandHost) pointerInside() bool {
	x, y := h.pointer()
	left := h.screenCentreX() - islandShapeW/2
	const slack = 6
	return x >= left-slack && x <= left+islandShapeW+slack && y <= h.h0+islandRowH+slack
}

func (h *islandHost) pointer() (x, y float32) {
	if h.cursor != nil {
		return h.cursor()
	}
	p := mygo.Screen.CursorScreenPoint()
	d := mygo.Screen.PrimaryDisplay()
	return float32(p.X), float32(p.Y) - float32(d.Bounds.Y)
}

func (h *islandHost) screenCentreX() float32 {
	if h.cursor != nil {
		return 0 // tests place the pointer relative to the centre
	}
	d := mygo.Screen.PrimaryDisplay()
	return float32(d.Bounds.X) + float32(d.Bounds.Width)/2
}

func (h *islandHost) winH() float32 { return h.h0 + islandRowH + 24 }

func (h *islandHost) pin() {
	if h.win != nil {
		islandPin(h.win.NativeHandle(), float64(islandWinW), float64(h.winH()))
	}
}

// ensureWindow builds the window on the first alert: a transparent frameless
// panel that never takes the keyboard from what the user is doing.
func (h *islandHost) ensureWindow() *mygo.Window {
	if h.win != nil {
		return h.win
	}
	if h.s.win == nil || runtime.GOOS != "darwin" {
		return nil
	}
	h.measure()
	win := mygo.NewWindow(mygo.WindowOptions{
		Title:         "Solco Island",
		Panel:         true,
		Hidden:        true,
		Frameless:     true,
		Transparent:   true,
		DisableShadow: true,
		DisableResize: true,
		Width:         islandWinW,
		Height:        int(h.winH()),
		Content:       ui.View(h.view),
	})
	win.OnClosed(func() { h.win = nil }) // Escape and the like close a panel: the next alert builds a new one
	islandRaise(win.NativeHandle())
	mygo.Screen.OnDisplaysChanged(func() {
		h.measure()
		if h.open {
			h.pin()
		}
	})
	h.win = win
	return win
}

// later runs fn on the main thread after the current view pass.
func (h *islandHost) later(fn func()) {
	if h.s.win == nil {
		fn()
		return
	}
	go mygo.RunOnMain(fn)
}

// islandTone is the dot's colour for an alert kind.
func islandTone(k islandKind) ui.Color {
	r := designRoles(true)
	switch k {
	case islandAttention:
		return r.Warn
	case islandFeedback:
		return r.Accent
	case islandReview:
		return r.Ok
	default:
		return r.Danger
	}
}

// islandShape is the housing grown to w×h: straight sides that flare into the
// top edge, and a rounded foot.
func islandShape(cx, w, h, flare, foot float32) *ui.Path {
	const k = 0.5523 // a quarter circle's control points
	l, r := cx-w/2, cx+w/2
	p := &ui.Path{}
	p.MoveTo(l-flare, 0)
	p.CubeTo(l-flare+flare*k, 0, l, flare-flare*k, l, flare)
	p.LineTo(l, h-foot)
	p.CubeTo(l, h-foot+foot*k, l+foot-foot*k, h, l+foot, h)
	p.LineTo(r-foot, h)
	p.CubeTo(r-foot+foot*k, h, r, h-foot+foot*k, r, h-foot)
	p.LineTo(r, flare)
	p.CubeTo(r, flare-flare*k, r+flare-flare*k, 0, r+flare, 0)
	p.Close()
	return p
}

// view draws the island: black like the housing whatever the theme, growing
// from the housing's size to the alert's row and back.
func (h *islandHost) view(c *ui.Context) {
	c.Root().Background(ui.Transparent)
	roles := designRoles(true)
	white := ui.RGB(255, 255, 255)
	black := ui.RGB(0, 0, 0)

	targetW, targetH, opened := h.w0, h.h0, float32(0)
	spring := ui.Animation{Spring: ui.SpringOptions{Duration: motionIsland, Bounce: 0.12}}
	if h.open {
		targetW, targetH, opened = islandShapeW, h.h0+islandRowH, 1
		spring.Spring.Bounce = 0.24
	}
	stage := ui.Box(c).Fill()
	// A spring overshoots: never below nothing.
	w := max(0, stage.AnimateTo("w", targetW, spring))
	hh := max(0, stage.AnimateTo("h", targetH, spring))
	grown := stage.AnimateTo("open", opened, spring)
	foot := 8 + 6*grown
	flare := 5 + 3*grown
	cx := float32(islandWinW) / 2

	stage.Draw(func(p *ui.Painter, r ui.Rect) {
		if w < 1 || hh < 1 {
			return
		}
		p.FillPath(islandShape(r.X+cx, w, hh, flare, foot), black)
	})
	stage.Children(func() {
		alert, on := h.toasts.Showing()
		if !on || grown < 0.05 {
			return
		}
		body := ui.Box(c).Absolute().Top(0).Left(cx-w/2).Size(w, hh).Radius(0, 0, foot, foot).Clip()
		body.Children(func() {
			row := ui.Row(c).Absolute().Top(h.h0).Left(0).Right(0).Height(islandRowH).PaddingX(16).Gap(10).AlignItems(ui.Center).
				Opacity(max(0, grown*1.4-0.4))
			row.Children(func() {
				ui.Box(c).Size(8, 8).Radius(4).Background(islandTone(alert.Kind)).Shrink(0)
				ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
					ui.Text(c, alert.Title).FontSize(12.5).Bold().TextColor(white).SingleLine()
					if alert.Body != "" {
						ui.Text(c, alert.Body).FontSize(11.5).TextColor(white.Alpha(0.55)).SingleLine()
					}
				})
				if n := h.toasts.Pending(); n > 0 {
					ui.Text(c, "+"+strconv.Itoa(n)).FontSize(11.5).TextColor(white.Alpha(0.55)).Shrink(0)
				}
				open := ui.Box(c.Key("island-open")).Height(22).PaddingX(11).Radius(11).Center().Background(white).Shrink(0).
					Cursor(ui.CursorPointer).Role(ui.RoleButton).Label("Open").Focusable().KeepFocus()
				open.Children(func() { ui.Text(c, "Open").FontSize(11.5).Bold().TextColor(black) })
				dismiss := ui.Box(c.Key("island-dismiss")).Size(22, 22).Radius(11).Center().Shrink(0).
					Cursor(ui.CursorPointer).Role(ui.RoleButton).Label("Dismiss").Focusable().KeepFocus()
				if dismiss.Hovered() {
					dismiss.Background(white.Alpha(0.14))
				}
				dismiss.Children(func() { ui.Icon(c, iconClose).Size(12, 12).TextColor(roles.Muted) })
				if open.Clicked() {
					h.later(func() { h.openAlert(alert) })
				}
				if dismiss.Clicked() {
					h.later(h.finish)
				}
			})
		})
	})
}

// openAlert follows the alert to where it lives, through the same route a
// system banner's click takes, and lets the island go.
func (h *islandHost) openAlert(a islandAlert) {
	h.s.applyOnWindow(func() { h.s.routeAgentNotification(a.ID) })
	if cur, ok := h.toasts.Showing(); ok && cur.ID == a.ID {
		h.finish()
	}
}
