package nativeui

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	qrcode "github.com/skip2/go-qrcode"
	"github.com/thewei/solco/internal/remotehost"
)

/**
 * [INPUT]: 依赖 remote_settings.go 的 remoteState/remoteServer、internal/remotehost 的 Server.StartPairing/CancelPairing/Devices、skip2/go-qrcode（只负责把配对 URL 编成模块矩阵）、modalTitle/regularButton 共享控件
 * [OUTPUT]: Shell.openPairing / closePairing、Shell.remotePairDialog（Pair a Phone 二维码模态：真实 mm:ss 倒计时、过期 New Code、Copy Pairing Link、设备真实绑定后的 `<name> paired`、无法生成时的 Pairing is unavailable + Retry）、remoteQRView
 * [POS]: 配对 UI 只呈现 Server 的真实事实：二维码 = StartPairing 的 v2 URL（只含真正在服务的路线），成功 = DeviceStore 里出现了打开对话框之后才有的设备（不凭手机发起握手），过期/关闭/Esc/开关变化都经 CancelPairing 立刻让旧码作废；二维码固定黑白、整数像素模块以便扫描，不随主题反色；URL 与码永不进日志或界面文字
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// remoteQRSize is the QR's largest edge. Modules are whole points, so the picture stays crisp on every scale.
const remoteQRSize = 300

func (s *Shell) openPairing() {
	r := &s.remote
	r.pairErr, r.pairedName = "", ""
	r.offer, r.qr = remotehost.PairingOffer{}, nil
	srv := s.remoteServer()
	if srv == nil {
		r.pairErr = "Remote Access is not available on this Mac."
		r.pairOpen = true
		return
	}
	baseline := map[string]bool{}
	for _, d := range srv.Devices().List() {
		baseline[d.ID] = true
	}
	offer, err := srv.StartPairing()
	if err != nil {
		r.pairErr = pairingProblem(err)
		r.pairOpen = true
		return
	}
	code, err := qrcode.New(offer.URL, qrcode.Low)
	if err != nil {
		srv.CancelPairing()
		r.pairErr = "The pairing code is too large to draw. Turn off routes you do not use in Advanced and try again."
		r.pairOpen = true
		return
	}
	r.offer, r.qr, r.pairBaseline, r.pairOpen = offer, code.Bitmap(), baseline, true
}

// pairingProblem puts a Host refusal in words a person can act on.
func pairingProblem(err error) string {
	switch msg := err.Error(); {
	case strings.Contains(msg, "no route"):
		return "No connection is ready yet. Wait for the secure connection to start, then try again."
	case strings.Contains(msg, "off"):
		return "Remote Access is off. Turn it on to pair a phone."
	default:
		return "The Host could not make a pairing code."
	}
}

// closePairing drops the code and its picture (the sheet closed, or the routes it names changed).
func (s *Shell) closePairing() {
	r := &s.remote
	if r.offer.Code == "" && !r.pairOpen && r.pairErr == "" {
		return
	}
	if r.srv != nil {
		r.srv.CancelPairing()
	}
	r.pairOpen, r.offer, r.qr, r.pairErr, r.pairedName, r.pairBaseline = false, remotehost.PairingOffer{}, nil, "", "", nil
}

// phonePaired names the phone that bound itself to the code just shown: a device the store did not have when the
// dialog opened. "" while none has.
func (s *Shell) phonePaired() string {
	r := &s.remote
	if r.pairedName != "" || r.srv == nil || r.pairBaseline == nil {
		return r.pairedName
	}
	for _, d := range r.srv.Devices().List() {
		if !r.pairBaseline[d.ID] {
			name := strings.TrimSpace(d.Name)
			if name == "" {
				name = "Phone"
			}
			r.pairedName = name
			break
		}
	}
	return r.pairedName
}

func (s *Shell) remotePairDialog(c *ui.Context) {
	r := &s.remote
	if !r.pairOpen {
		s.closePairing()
		return
	}
	s.remoteTick()
	ui.Modal(c, &r.pairOpen, func() {
		t := c.Theme()
		sp := Spacing()
		typ := Typography()
		tokens := designTokens(t.Dark)
		paired := s.phonePaired()
		remaining := time.Until(r.offer.ExpiresAt)
		expired := r.pairErr == "" && paired == "" && remaining <= 0
		ui.Column(c).Width(380).Gap(sp.L).AlignItems(ui.Center).Children(func() {
			modalTitle(c, "Pair a Phone")
			switch {
			case r.pairErr != "":
				ui.Text(c, "Pairing is unavailable").FontSize(typ.Section).FontWeight(WeightSemibold)
				ui.Text(c, r.pairErr).FontSize(typ.BodySmall).TextColor(t.TextMuted).TextAlign(ui.Center)
			case paired != "":
				ui.Box(c).Size(remoteQRSize, 0)
				ui.Row(c).Gap(sp.M).AlignItems(ui.Center).Children(func() {
					ui.Box(c).Size(10, 10).Radius(5).Background(tokens.StatusColor(ToneSuccess, t.Dark))
					ui.Text(c, paired+" paired").FontSize(typ.Section).FontWeight(WeightSemibold)
				})
				ui.Text(c, "You can now close this window.").FontSize(typ.BodySmall).TextColor(t.TextMuted)
			default:
				ui.Text(c, "Scan with Solco on your iPhone or Android.").FontSize(typ.BodySmall).TextColor(t.TextMuted)
				s.remoteQRView(c, expired)
				if expired {
					ui.Text(c, "This code expired.").FontSize(typ.BodySmall).TextColor(tokens.StatusColor(ToneError, t.Dark))
				} else {
					secs := int(remaining.Seconds())
					ui.Text(c, fmt.Sprintf("Valid for %02d:%02d · One-time code", secs/60, secs%60)).
						FontSize(typ.Caption).TextColor(t.TextMuted).
						Label(fmt.Sprintf("Valid for about %d minutes", (secs+59)/60)) // spoken per minute, not per second
				}
			}
			ui.Row(c).FillWidth().Gap(sp.M).Justify(ui.End).Children(func() {
				switch {
				case r.pairErr != "":
					if regularButton(c, "Retry").Clicked() {
						s.openPairing()
					}
				case paired != "":
				case expired:
					if regularButton(c, "New Code").Clicked() {
						s.openPairing()
					}
				default:
					if regularButton(c, "Copy Pairing Link").Clicked() {
						s.copyToClipboard(r.offer.URL)
						s.pendingToast = "Pairing link copied"
					}
				}
				if regularPrimaryButton(c, "Done").Clicked() {
					r.pairOpen = false
				}
			})
		})
	})
}

// remoteQRView draws the code in fixed black on white: a QR must keep its contrast in every theme, and the
// bitmap already carries its quiet zone. An expired code is nearly invisible (the Host has voided it anyway).
func (s *Shell) remoteQRView(c *ui.Context, expired bool) {
	grid := s.remote.qr
	n := len(grid)
	if n == 0 {
		return
	}
	ink := ui.RGB(0, 0, 0)
	if expired {
		ink = ui.RGB(228, 228, 228)
	}
	cell := float32(int(remoteQRSize / float32(n)))
	if cell < 2 {
		cell = 2
	}
	side := cell * float32(n)
	ui.Box(c).Width(side).Height(side).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
		p.Fill(r, ui.RGB(255, 255, 255), 8)
		for y, line := range grid {
			for x, dark := range line {
				if dark {
					p.Fill(ui.Rect{X: r.X + float32(x)*cell, Y: r.Y + float32(y)*cell, W: cell, H: cell}, ink, 0)
				}
			}
		}
	})
}
