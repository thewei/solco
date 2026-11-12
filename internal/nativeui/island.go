package nativeui

/**
 * [INPUT]: island_model.go（islandToasts）、island_roster.go（名册模型）、island_darwin.go 的原生缝、Shell.BuildStatusCenterSnapshot / feedback 线程行（名册的事实）、Shell.routeAgentNotification（点「Open」走与系统横幅点击同一条路由）、feedback.Client（行内作答）、settings.Notifications.Island、designRoles
 * [OUTPUT]: 对外提供 islandHost、Shell.islandNotify（三个通知出口各调用一次）、Shell.islandOff（设置里关掉开关）、Shell.islandSync（开关/启动时按设置装上或撤掉指针监视）、Shell.islandRefresh（状态变了让展开的名册和翅膀重画）、Shell.islandQuiets（岛在场时系统横幅静默）
 * [POS]: 刘海岛 P1–P3（2026-11-12 决策，方向 C）：开关打开后，需要你的 Agent / 完成待 review / Feedback 提问 / 自动化失败从刘海长出一条提示，7 秒后收回；指针在刘海（或提示）上停 300 ms 就展开名册（Needs you / Working / Idle，与菜单栏面板同一份快照），离开 300 ms 收回；Feedback 提问若是一个短单选就在行内点选项作答，其余行点 Open 去 Solco 里对应处（Agent 的 Allow/Deny 仍在 Chat 里答）。P3：翅膀（刘海两侧的静态状态：工作中数 / 等你的数，默认关）、无刘海屏的常驻小岛、横幅静默开关、共享/录屏时对岛隐藏（`SetContentProtection`，默认隐藏）。岛不拥有状态、不碰 Herdr；窗口只在第一次需要时才建（翅膀或常驻开着时随开关建好），指针只在壳外时窗口对鼠标透明，默认关时一行代码都不跑
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"errors"
	"log/slog"
	"runtime"
	"strconv"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/feedback"
)

const (
	islandShapeW  = 470 // the grown shape's width for one alert
	islandRosterW = 560 // …and for the roster
	islandWinW    = 620 // the window: the widest shape plus its flare, so the flare is never clipped
	islandWinBody = 680 // the window's height under the housing; the shape never needs more, and the rest is click-through
	islandRowH    = 34  // the row under the housing that carries the alert
	islandShowFor = 7 * time.Second
	islandHoldFor = 2 * time.Second // re-check while the pointer rests on it
	islandHideLag = 600 * time.Millisecond
	islandRestFor = 300 * time.Millisecond // the pointer rests this long to open the roster, and stays away this long to close it
	islandSlack   = 6                      // points around the shape that still count as on it
	islandWingW   = 56                     // each wing beside the notch
)

// islandHost shows one alert at a time in a window that grows out of the
// notch. Every method runs on the main thread (islandNotify marshals).
type islandHost struct {
	s      *Shell
	toasts islandToasts
	win    *mygo.Window
	open   bool // the shape is grown
	roster bool // …into the roster rather than one alert
	gen    int  // bumped on every show and close: a stale timer does nothing
	w0, h0 float32
	notch  bool // the display has a camera housing; otherwise the top-centre edge is the hot zone

	enteredAt time.Time // since when the pointer rests on the housing or the alert; zero when it does not
	leftAt    time.Time // since when the pointer is off the open roster; zero when it is on it
	passing   bool      // the window currently lets the mouse through
	stopWatch func()
	rosterH   float32 // the roster shape's height, from the last draw: the hit test reads it

	// asks are the one-click Asks of waiting Feedback threads, read off the
	// lane when the roster opens; asksAt remembers which LastAt each was read at.
	asks   map[string]islandAsk
	asksAt map[string]int64
	// sent is the option the island has sent for an Ask ("" = it did not go).
	sent map[string]string

	// Seams for tests: the pointer's screen position, the clock, and how an
	// answer is sent.
	cursor   func() (x, y float32)
	now      func() time.Time
	answerFn func(a islandAsk, option string) error
}

func (s *Shell) islandHostOf() *islandHost {
	if s.island == nil {
		s.island = &islandHost{s: s, asks: map[string]islandAsk{}, asksAt: map[string]int64{}, sent: map[string]string{}}
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
	h.watch(false)
	h.toasts.Clear()
	h.open, h.roster = false, false
	h.enteredAt, h.leftAt = time.Time{}, time.Time{}
	h.gen++
	if h.win != nil {
		h.win.Hide()
	}
}

// islandSync follows the switch: on installs the pointer watch (the roster
// opens from hovering, so it must listen before any alert exists), off
// removes everything. Window lane.
func (s *Shell) islandSync() {
	if !s.settings.Notifications.Island {
		s.islandOff()
		return
	}
	h := s.islandHostOf()
	h.watch(true)
	h.syncRest()
}

// islandQuiets says whether the system banners for what the island shows stay
// quiet: the island is on and working, and the user asked for it. The phone
// and the webhook channels are never affected. Window lane.
func (s *Shell) islandQuiets() bool {
	n := s.settings.Notifications
	return n.Island && n.IslandQuiet && s.island != nil && s.island.stopWatch != nil
}

// islandRefresh redraws an open roster after the facts under it moved (the
// Agent directory, the Feedback thread rows). Window lane; nothing when the
// island is shut.
func (s *Shell) islandRefresh() {
	h := s.island
	if h == nil || h.win == nil {
		return
	}
	if h.roster {
		h.fetchAsks()
		h.win.Invalidate()
	} else if h.restOn() {
		h.win.Invalidate() // the wings carry the counts
	}
}

// measure finds the housing of the display with the menu bar; a display
// without one gets a housing as high as the menu bar.
func (h *islandHost) measure() {
	h.w0, h.h0, h.notch = 200, 32, false
	if w, hh, ok := islandNotchSize(); ok {
		h.w0, h.h0, h.notch = float32(w), float32(hh), true
	} else if hh := islandMenuBarHeight(); hh > 0 {
		h.h0 = float32(hh)
	}
}

// wingsOn says whether a status shows on each side of the notch at rest: the
// switch is on and there is a notch to flank (or the user keeps the island
// showing on a display without one).
func (h *islandHost) wingsOn() bool {
	n := h.s.settings.Notifications
	return n.Island && n.IslandWings && (h.notch || n.IslandVirtual)
}

// restOn says whether the island is on screen at rest: wings, or a small island
// hanging from the top edge of a display that has no notch.
func (h *islandHost) restOn() bool {
	n := h.s.settings.Notifications
	return h.wingsOn() || (n.Island && n.IslandVirtual && !h.notch)
}

// restSize is the shape's size at rest.
func (h *islandHost) restSize() (w, hh float32) {
	switch {
	case h.wingsOn():
		return h.w0 + 2*islandWingW, h.h0
	case h.restOn():
		return h.w0, h.h0
	case h.notch:
		return h.w0, h.h0 // the housing itself: nothing to see
	}
	return h.w0, 0
}

// syncRest shows or hides the window for the rest state after a setting or
// the display changed, and keeps the sharing protection in step.
func (h *islandHost) syncRest() {
	if h.win != nil {
		h.win.SetContentProtection(!h.s.settings.Notifications.IslandVisibleInShares)
	}
	if h.restOn() {
		if win := h.ensureWindow(); win != nil {
			win.ShowInactive()
			h.pin()
			win.Invalidate()
		}
		return
	}
	if !h.open && h.win != nil {
		h.win.Invalidate()
		h.win.Hide()
	}
}

func (h *islandHost) push(a islandAlert) {
	if h.roster {
		return // the open roster shows the live state: an alert would only repeat a row
	}
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
		h.passThrough(!h.pointerInside())
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
	h.open, h.roster = false, false
	h.enteredAt, h.leftAt = time.Time{}, time.Time{}
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
			if h.gen == g && !h.open && h.win != nil && !h.restOn() {
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

// shapeSize is the grown shape's size now: the alert's row or the roster.
func (h *islandHost) shapeSize() (w, hh float32) {
	if h.roster {
		return islandRosterW, h.rosterH
	}
	return islandShapeW, h.h0 + islandRowH
}

// overShape says whether the pointer is over the grown shape (with a few
// points of slack), so a reader is never cut off.
func (h *islandHost) overShape(x, y float32) bool {
	if !h.open {
		return false
	}
	w, hh := h.shapeSize()
	left := h.screenCentreX() - w/2
	return x >= left-islandSlack && x <= left+w+islandSlack && y <= hh+islandSlack
}

func (h *islandHost) pointerInside() bool {
	x, y := h.pointer()
	return h.overShape(x, y)
}

// inHotZone says whether the pointer is on the housing, or, on a display
// without one, at the very top of the screen near the centre.
func (h *islandHost) inHotZone(x, y float32) bool {
	cx := h.screenCentreX()
	if w, hh := h.restSize(); h.restOn() {
		return x >= cx-w/2-islandSlack && x <= cx+w/2+islandSlack && y <= hh
	}
	if h.notch {
		return x >= cx-h.w0/2-islandSlack && x <= cx+h.w0/2+islandSlack && y <= h.h0
	}
	return x >= cx-80 && x <= cx+80 && y <= 3
}

func (h *islandHost) clock() time.Time {
	if h.now != nil {
		return h.now()
	}
	return time.Now()
}

// watch installs or removes the pointer monitor. Headless and off macOS there
// is none.
func (h *islandHost) watch(on bool) {
	if !on {
		if h.stopWatch != nil {
			h.stopWatch()
			h.stopWatch = nil
		}
		return
	}
	if h.stopWatch != nil || h.s.win == nil || runtime.GOOS != "darwin" {
		return
	}
	stop, ok := islandWatchPointer(h.poll)
	if !ok {
		stop()
		return
	}
	h.stopWatch = stop
}

// recheck polls again once the rest time can have passed: a resting pointer
// sends no events to wake the watch.
func (h *islandHost) recheck() {
	if h.s.win == nil {
		return
	}
	time.AfterFunc(islandRestFor+5*time.Millisecond, func() { mygo.RunOnMain(h.poll) })
}

// poll runs on every pointer move (and on its own rechecks): rest on the
// housing or the alert for 300 ms opens the roster, 300 ms off the open
// roster closes it, and the window lets the mouse through everywhere but on
// the shape. Cheap when nothing is near.
func (h *islandHost) poll() {
	if !h.s.settings.Notifications.Island {
		return
	}
	x, y := h.pointer()
	now := h.clock()
	over := h.overShape(x, y)
	if h.roster {
		switch {
		case over:
			h.leftAt = time.Time{}
		case h.leftAt.IsZero():
			h.leftAt = now
			h.recheck()
		case now.Sub(h.leftAt) >= islandRestFor:
			h.closeRoster()
			return
		}
	} else if over || h.inHotZone(x, y) {
		switch {
		case h.enteredAt.IsZero():
			h.enteredAt = now
			h.recheck()
		case now.Sub(h.enteredAt) >= islandRestFor:
			h.openRoster()
			return
		}
	} else {
		h.enteredAt = time.Time{}
	}
	h.passThrough(!over)
}

// passThrough lets the mouse go to what is under the window while the pointer
// is not on the shape.
func (h *islandHost) passThrough(v bool) {
	if h.win == nil || h.passing == v {
		return
	}
	h.passing = v
	h.win.SetIgnoreMouseEvents(v)
}

// openRoster grows the shape into the roster. Alerts waiting to be shown are
// dropped: the roster lists what they were about.
func (h *islandHost) openRoster() {
	h.toasts.Clear()
	h.enteredAt, h.leftAt = time.Time{}, time.Time{}
	h.gen++
	h.roster, h.open = true, true
	h.rosterH = h.rosterHeight(h.buildRoster())
	if win := h.ensureWindow(); win != nil {
		win.ShowInactive()
		h.pin()
		h.passThrough(!h.pointerInside())
		win.Invalidate()
	}
	h.fetchAsks()
}

func (h *islandHost) closeRoster() {
	h.passThrough(true)
	h.close()
}

// buildRoster reads the same facts the menu-bar panel reads.
func (h *islandHost) buildRoster() islandRoster {
	return buildIslandRoster(h.s.BuildStatusCenterSnapshot(), h.s.feedback.threads, h.asks)
}

// fetchAsks reads, off the lane, the Asks of waiting threads whose rows the
// island has not read yet, so a row can offer its options in a click.
func (h *islandHost) fetchAsks() {
	client := h.s.feedback.client
	if client == nil {
		return
	}
	for _, t := range h.s.feedback.threads {
		if t.Waiting == 0 {
			delete(h.asks, t.ID)
			continue
		}
		if at, ok := h.asksAt[t.ID]; ok && at == t.LastAt {
			continue
		}
		h.asksAt[t.ID] = t.LastAt
		id := t.ID
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			th, err := client.Thread(ctx, id)
			h.s.applyOnWindow(func() {
				if err != nil {
					delete(h.asksAt, id) // try again at the next refresh
					return
				}
				if a, ok := islandAskOf(th); ok {
					h.asks[id] = a
				} else {
					delete(h.asks, id)
				}
				if h.win != nil {
					h.win.Invalidate()
				}
			})
		}()
	}
}

// answer sends one option for an Ask: the row says "Sent" at once, and the
// Ask leaves the roster when the broker's event refreshes the thread rows.
func (h *islandHost) answer(a islandAsk, option string) {
	if _, done := h.sent[a.Ask]; done {
		return
	}
	h.sent[a.Ask] = option
	send := h.answerFn
	if send == nil {
		send = h.sendAnswer
	}
	if h.win != nil {
		h.win.Invalidate()
	}
	done := func() {
		err := send(a, option)
		h.s.applyOnWindow(func() {
			if err != nil {
				slog.Warn("island answer failed", "error", err) // the error carries no answer text
				h.sent[a.Ask] = ""
			}
			if h.win != nil {
				h.win.Invalidate()
			}
		})
	}
	if h.s.win == nil {
		done() // headless: no lane to hop to, so tests see the result at once
		return
	}
	go done()
}

func (h *islandHost) sendAnswer(a islandAsk, option string) error {
	client := h.s.feedback.client
	if client == nil {
		return errors.New("feedback is not available")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return client.Answer(ctx, a.Ask, map[string]feedback.Answer{a.Question: {Selected: []string{option}}}, nil, nil)
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

func (h *islandHost) winH() float32 { return h.h0 + islandWinBody }

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
	win.OnClosed(func() { h.win, h.passing = nil, false }) // Escape and the like close a panel: the next show builds a new one
	islandRaise(win.NativeHandle())
	win.SetContentProtection(!h.s.settings.Notifications.IslandVisibleInShares) // out of screenshots, recordings and shares unless the user said otherwise
	win.SetIgnoreMouseEvents(true)                                              // click-through until the pointer is on the shape
	h.passing = true
	mygo.Screen.OnDisplaysChanged(func() {
		h.measure()
		if h.open || h.restOn() {
			h.pin()
		}
		h.syncRest()
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

	targetW, targetH := h.restSize()
	opened := float32(0)
	spring := ui.Animation{Spring: ui.SpringOptions{Duration: motionIsland, Bounce: 0.12}}
	var roster islandRoster
	if h.open {
		targetW, targetH, opened = islandShapeW, h.h0+islandRowH, 1
		spring.Spring.Bounce = 0.24
	}
	if h.roster {
		roster = h.buildRoster()
		h.rosterH = h.rosterHeight(roster)
		targetW, targetH = islandRosterW, h.rosterH
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
		if h.roster && grown >= 0.05 {
			body := ui.Box(c).Absolute().Top(0).Left(cx-w/2).Size(w, hh).Radius(0, 0, foot, foot).Clip()
			body.Children(func() { h.rosterView(c, roster, max(0, grown*1.4-0.4)) })
			return
		}
		if h.wingsOn() && grown < 0.5 {
			h.wingsView(c, cx, w, hh, 1-grown*2)
		}
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

// Roster geometry, in points: every height is fixed so the shape can be sized
// before it is drawn.
const (
	islandGroupH = 24 // a group's header line
	islandItemH  = 40 // one row
	islandChipH  = 26 // the options under a row that can be answered here
	islandMoreH  = 20 // "+N more"
	islandEmptyH = 28 // "Nothing is waiting for you"
	islandFootH  = 36
	islandPadTop = 4
)

func groupHeight(rows []islandRow, more int) float32 {
	if len(rows) == 0 {
		return 0
	}
	n := float32(islandGroupH)
	for _, r := range rows {
		n += islandItemH
		if r.Ask != nil {
			n += islandChipH
		}
	}
	if more > 0 {
		n += islandMoreH
	}
	return n
}

// rosterHeight is the roster shape's height: the housing, the groups that
// have rows (Needs says so when it has none), and the foot.
func (h *islandHost) rosterHeight(r islandRoster) float32 {
	n := h.h0 + islandPadTop + islandFootH
	if len(r.Needs) == 0 {
		n += islandEmptyH
	}
	return n + groupHeight(r.Needs, r.NeedsMore) + groupHeight(r.Working, r.WorkingMore) + groupHeight(r.Idle, r.IdleMore)
}

// rosterView lays the roster out under the housing.
func (h *islandHost) rosterView(c *ui.Context, r islandRoster, opacity float32) {
	roles := designRoles(true)
	white := ui.RGB(255, 255, 255)
	muted := white.Alpha(0.55)
	col := ui.Column(c).Absolute().Top(h.h0 + islandPadTop).Left(0).Right(0).PaddingX(14).Opacity(opacity)
	col.Children(func() {
		group := func(name string, tone ui.Color, rows []islandRow, more int) {
			if len(rows) == 0 {
				return
			}
			ui.Row(c).Height(islandGroupH).AlignItems(ui.Center).Gap(6).Children(func() {
				ui.Text(c, name).FontSize(11).Bold().TextColor(tone)
				ui.Text(c, strconv.Itoa(len(rows)+more)).FontSize(11).TextColor(muted)
			})
			for _, row := range rows {
				h.rosterRow(c, row, white, muted)
			}
			if more > 0 {
				ui.Row(c).Height(islandMoreH).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "+"+strconv.Itoa(more)+" more").FontSize(11).TextColor(muted)
				})
			}
		}
		if len(r.Needs) == 0 {
			ui.Row(c).Height(islandEmptyH).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "Nothing is waiting for you").FontSize(12).TextColor(muted)
			})
		}
		group("Needs you", roles.Warn, r.Needs, r.NeedsMore)
		group("Working", roles.Accent, r.Working, r.WorkingMore)
		group("Idle", muted, r.Idle, r.IdleMore)
		foot := ui.Row(c).Height(islandFootH).AlignItems(ui.Center).Gap(8)
		foot.Children(func() {
			ui.Text(c, "Same list as the menu-bar panel").FontSize(11).TextColor(white.Alpha(0.4)).Grow(1).SingleLine()
			solco := ui.Box(c.Key("island-solco")).Height(22).PaddingX(10).Radius(11).Center().Shrink(0).
				Cursor(ui.CursorPointer).Role(ui.RoleButton).Label("Open Solco").Focusable().KeepFocus()
			if solco.Hovered() {
				solco.Background(white.Alpha(0.14))
			}
			solco.Children(func() { ui.Text(c, "Open Solco").FontSize(11.5).Bold().TextColor(white) })
			if solco.Clicked() {
				h.later(func() {
					h.s.activateWindow()
					h.closeRoster()
				})
			}
		})
	})
}

// rosterRow is one Agent or Feedback line: dot, words, Open, and under it the
// options when the Ask can be answered here.
func (h *islandHost) rosterRow(c *ui.Context, row islandRow, white, muted ui.Color) {
	ui.Row(c).Height(islandItemH).AlignItems(ui.Center).Gap(10).Children(func() {
		ui.Box(c).Size(8, 8).Radius(4).Background(islandTone(row.Kind)).Shrink(0)
		ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
			ui.Text(c, row.Title).FontSize(12.5).Bold().TextColor(white).SingleLine()
			if row.Sub != "" {
				ui.Text(c, row.Sub).FontSize(11.5).TextColor(muted).SingleLine()
			}
		})
		if row.Route != "" {
			open := ui.Box(c.Key("island-row-open:" + row.Key)).Height(22).PaddingX(8).Radius(11).Center().Shrink(0).
				Cursor(ui.CursorPointer).Role(ui.RoleButton).Label("Open " + row.Title).Focusable().KeepFocus()
			tone := muted
			if open.Hovered() {
				open.Background(white.Alpha(0.14))
				tone = white
			}
			open.Children(func() { ui.Text(c, "Open").FontSize(11.5).TextColor(tone) })
			if open.Clicked() {
				route := row.Route
				h.later(func() {
					h.s.applyOnWindow(func() { h.s.routeAgentNotification(route) })
					h.closeRoster()
				})
			}
		}
	})
	if row.Ask == nil {
		return
	}
	ask := *row.Ask
	ui.Row(c).Height(islandChipH).AlignItems(ui.Center).Gap(6).Margin(0, 0, 0, 18).Children(func() {
		if opt, done := h.sent[ask.Ask]; done {
			switch opt {
			case "":
				ui.Text(c, "Not sent — use Open to answer").FontSize(11.5).TextColor(islandTone(islandAttention))
			default:
				ui.Text(c, "Sent · "+opt).FontSize(11.5).TextColor(muted).SingleLine()
			}
			return
		}
		for _, opt := range ask.Options {
			chip := ui.Box(c.Key("island-opt:" + ask.Ask + ":" + opt)).Height(22).PaddingX(10).Radius(11).Center().Shrink(0).
				Background(white.Alpha(0.12)).Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(opt).Focusable().KeepFocus()
			if chip.Hovered() {
				chip.Background(white.Alpha(0.24))
			}
			chip.Children(func() { ui.Text(c, opt).FontSize(11.5).TextColor(white).SingleLine() })
			if chip.Clicked() {
				opt := opt
				h.later(func() { h.answer(ask, opt) })
			}
		}
	})
}

// wingsView is the rest state's two small statuses, one each side of the notch:
// the Agents that work (an accent dot and their number) and what waits for the
// user (the colour of the most urgent kind and the bell's own number). A quiet
// side shows a dim dot. Static: nothing animates at rest.
func (h *islandHost) wingsView(c *ui.Context, cx, w, hh, opacity float32) {
	roles := designRoles(true)
	white := ui.RGB(255, 255, 255)
	snap := h.s.BuildStatusCenterSnapshot()
	waiting := h.s.waitingTotal()
	tone := islandReview
	switch {
	case snap.NeedsAttention > 0 || snap.Interactions > 0:
		tone = islandAttention
	case h.s.feedbackWaiting() > 0:
		tone = islandFeedback
	}
	side := func(left float32, dot ui.Color, n int) {
		ui.Row(c).Absolute().Top(0).Left(left).Size(islandWingW, hh).Center().Gap(5).Opacity(opacity).Children(func() {
			ui.Box(c).Size(7, 7).Radius(3.5).Background(dot).Shrink(0)
			if n > 0 {
				ui.Text(c, strconv.Itoa(n)).FontSize(12).Bold().TextColor(white)
			}
		})
	}
	l := cx - w/2
	workDot := white.Alpha(0.28)
	if snap.Working > 0 {
		workDot = roles.Accent
	}
	waitDot := white.Alpha(0.28)
	if waiting > 0 {
		waitDot = islandTone(tone)
	}
	side(l, workDot, snap.Working)
	side(cx+w/2-islandWingW, waitDot, waiting)
}
