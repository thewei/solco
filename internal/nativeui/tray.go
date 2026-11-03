// [INPUT]: 依赖 mygo.Tray 的 SetIcon/OnClick、statusTray 平台缝的 SetTitle/SetToolTip、TrayCounts/TrayTitle/TrayToolTip/TrayFingerprint（tray_model.go）、Shell.BuildStatusCenterSnapshot 与 Shell.ToggleQuickPanel
// [OUTPUT]: 导出 AttachTray（Shell）、TrayIconPNG、updateTray（Shell）；内部持有 statusTray 控制器
// [POS]: nativeui 托盘的绑定层。main.go 创建 mygo.Tray 后交 AttachTray；状态投影与图标渲染在此收口，点击进 quick_panel.go 的浮层。
// [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md

package nativeui

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log/slog"
	"math"

	"github.com/egoist/mygo"
)

// trayPlatform is the MyGo tray seam (0.7 §17): production binds the real
// mygo.Tray; deterministic tests record applied projections.
type trayPlatform interface {
	SetTitle(title string)
	SetToolTip(tip string)
}

// statusTray owns the macOS menu-bar projection (0.7 §17-18): the shared
// StatusCenterSnapshot projects onto title/tooltip only when the tray
// fingerprint changes, so idle frames and navigation selection never rebuild
// the platform tray.
type statusTray struct {
	platform trayPlatform
	// applied reports whether any projection reached the platform; the
	// first apply always writes.
	applied     bool
	fingerprint string
	// remote names the phone on this Mac's screen ("" = none); a change re-projects like any other.
	remote string
}

func newStatusTray(platform trayPlatform) *statusTray {
	return &statusTray{platform: platform}
}

// apply projects one snapshot onto the platform (0.7 §18): writes happen
// only when the tray fingerprint changed since the last apply.
func (t *statusTray) apply(snapshot StatusCenterSnapshot, connected bool) {
	fingerprint := TrayFingerprint(snapshot, connected) + "|rd=" + t.remote
	if t.applied && fingerprint == t.fingerprint {
		return
	}
	t.fingerprint = fingerprint
	t.applied = true
	counts := TrayCountsFromSnapshot(snapshot)
	title, tip := TrayRemoteDesktop(TrayTitle(counts), TrayToolTip(counts, connected), t.remote)
	t.platform.SetTitle(title)
	t.platform.SetToolTip(tip)
}

// AttachTray binds the macOS menu-bar tray (0.7 §17): the normal click
// toggles the §17.1 quick-panel flyout anchored beneath the menu-bar item;
// without a bound tray the in-window Status Center stays the surface. The
// click routes as an action — never a direct runtime mutation.
func (s *Shell) AttachTray(tray *mygo.Tray) {
	s.trayHandle = tray
	s.tray = newStatusTray(tray)
	// The status-first title is empty when nothing is actionable; the
	// template glyph is the always-visible menu-bar mark, so bind it once
	// at attach (0.7 §17).
	if png, err := TrayIconPNG(); err != nil {
		slog.Warn("render tray icon failed", "error", err)
	} else if err := tray.SetIcon(png, true); err != nil {
		slog.Warn("set tray icon failed", "error", err)
	}
	tray.OnClick(func() {
		s.applyOnWindow(func() {
			s.ToggleQuickPanel()
		})
	})
	s.updateTray()
}

// updateTray projects the current status-center snapshot onto the menu bar
// (0.7 §17-18). No-op when no tray is bound: headless sessions, tests, and
// platforms where tray creation failed keep the in-window Status Center as
// the only surface.
func (s *Shell) updateTray() {
	if s.tray == nil {
		return
	}
	s.tray.remote = s.remoteDesktopTrayLabel()
	s.tray.apply(s.BuildStatusCenterSnapshot(), !s.offline)
}

// TrayIconPNG renders the menu-bar template glyph (0.7 §17): the Solco ram
// head in template monochrome so macOS tints it against the menu bar. The
// rasterizer walks the design-path Béziers directly; at 18pt only the horn
// spiral and the head wedge remain legible, with the `>—` prompt carved as
// negative space in the face.
func TrayIconPNG() ([]byte, error) {
	const size = 128
	const scale = float64(size) / 36.0 // design viewBox is 36×36
	bounds := image.Rect(0, 0, size, size)
	canvas := image.NewRGBA(bounds)
	draw.Draw(canvas, bounds, image.Transparent, image.Point{}, draw.Src)

	horn := bezierChain([][8]float64{
		{17, 5.5, 10, 4, 4.5, 9, 4.5, 15.5},
		{4.5, 15.5, 4.5, 22, 10, 27, 16.5, 27},
		{16.5, 27, 20, 27, 22.2, 24.5, 21.8, 21.5},
		{21.8, 21.5, 21.4, 18.8, 19.2, 17, 16.8, 17.5},
		{16.8, 17.5, 15, 17.8, 14.2, 19.5, 15, 21},
	})
	head := bezierPolygon([][8]float64{
		{18, 5, 23, 5, 27, 9, 28.8, 14},
		{28.8, 14, 30, 17.5, 30.2, 21.5, 29.2, 25},
		{29.2, 25, 28.4, 27.8, 26.2, 29.8, 23.5, 30},
	}, []pt{{22, 30}, {20.5, 25.5}, {18.8, 25.5}, {16.8, 10.5}},
		[][8]float64{{16.8, 10.5, 16.8, 7, 16.8, 5.3, 18, 5}})
	muzzle := bezierPolygon([][8]float64{
		{23.5, 30, 26.5, 31.2, 27.3, 31.5, 28.8, 30.5},
		{28.8, 30.5, 29.3, 29.8, 29.4, 28.8, 29.2, 25},
		{29.2, 25, 28.4, 27.8, 26.2, 29.8, 23.5, 30},
	}, nil, nil)
	prompt := []pt{{21, 14}, {23.5, 16.5}, {21, 19}}

	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			vx := float64(px) / scale
			vy := float64(py) / scale
			var col color.RGBA
			switch {
			case distToPolyline(vx, vy, prompt) < 0.75:
				col = color.RGBA{} // prompt is the negative space
			case distToPolyline(vx, vy, horn) < 1.5:
				col = color.RGBA{A: 255}
			case pointInPolygon(vx, vy, head) || pointInPolygon(vx, vy, muzzle):
				col = color.RGBA{A: 255}
			}
			canvas.SetRGBA(px, py, col)
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, canvas); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

type pt struct{ x, y float64 }

func bezierChain(segments [][8]float64) []pt {
	var out []pt
	for _, s := range segments {
		out = append(out, cubic(s)...)
	}
	return out
}

// bezierPolygon builds a closed outline: Bézier segments, then straight
// line points, then one closing Bézier back to the start.
func bezierPolygon(curves [][8]float64, lines []pt, closeCurve [][8]float64) []pt {
	var out []pt
	for _, s := range curves {
		out = append(out, cubic(s)...)
	}
	out = append(out, lines...)
	for _, s := range closeCurve {
		out = append(out, cubic(s)...)
	}
	return out
}

func cubic(s [8]float64) []pt {
	const steps = 24
	out := make([]pt, steps+1)
	for i := 0; i <= steps; i++ {
		t := float64(i) / steps
		u := 1 - t
		out[i] = pt{
			u*u*u*s[0] + 3*u*u*t*s[2] + 3*u*t*t*s[4] + t*t*t*s[6],
			u*u*u*s[1] + 3*u*u*t*s[3] + 3*u*t*t*s[5] + t*t*t*s[7],
		}
	}
	return out
}

func distToPolyline(px, py float64, pts []pt) float64 {
	best := math.MaxFloat64
	for i := 0; i+1 < len(pts); i++ {
		a, b := pts[i], pts[i+1]
		dx, dy := b.x-a.x, b.y-a.y
		len2 := dx*dx + dy*dy
		t := 0.0
		if len2 > 0 {
			t = ((px-a.x)*dx + (py-a.y)*dy) / len2
			t = math.Min(1, math.Max(0, t))
		}
		d := math.Hypot(px-(a.x+t*dx), py-(a.y+t*dy))
		best = math.Min(best, d)
	}
	return best
}

func pointInPolygon(px, py float64, poly []pt) bool {
	inside := false
	j := len(poly) - 1
	for i := 0; i < len(poly); i++ {
		xi, yi := poly[i].x, poly[i].y
		xj, yj := poly[j].x, poly[j].y
		if (yi > py) != (yj > py) && px < (xj-xi)*(py-yi)/(yj-yi)+xi {
			inside = !inside
		}
		j = i
	}
	return inside
}
