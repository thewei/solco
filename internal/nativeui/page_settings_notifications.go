package nativeui

/**
 * [INPUT]: 依赖 settings.Notifications（渠道清单 + 宽限秒数）、notifyhook（校验/加载/测试发送）、appSecrets（渠道的 {url,token} 只进 Solco 自己的加密文件）
 * [OUTPUT]: 对外提供 Shell.settingsNotifications（/settings/notifications：已添加的 webhook 渠道（名称、开关、移除）→ 添加渠道（名称/URL/可选令牌）→ 手机宽限 → 发送测试）
 * [POS]: nativeui Settings 的通知渠道面板（2026-11-09 决策）：渠道是用户自己填的 https URL；手机告知不到时由 Host 调用，本页只管配置，不发任何通知；URL 与令牌永不回显、不进 settings.json、不进日志
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/notifyhook"
	"github.com/thewei/solco/internal/settings"
)

// notifyChannelsUI is the page's drafts and the last test's outcome.
type notifyChannelsUI struct {
	name, url, token string
	testing          bool
	result           string
}

// notifyGraceChoices are the grace periods the page offers, in seconds.
var notifyGraceChoices = []struct {
	label   string
	seconds int
}{{"15 seconds", 15}, {"30 seconds", 30}, {"1 minute", 60}, {"2 minutes", 120}, {"5 minutes", 300}}

func notifyGraceLabel(seconds int) string {
	for _, ch := range notifyGraceChoices {
		if ch.seconds == seconds {
			return ch.label
		}
	}
	return notifyGraceChoices[1].label
}

func notifyGraceLabels() []string {
	out := make([]string, len(notifyGraceChoices))
	for i, ch := range notifyGraceChoices {
		out[i] = ch.label
	}
	return out
}

// notifyChannelsMax bounds the list: a handful of destinations, not a directory.
const notifyChannelsMax = 8

func newChannelID() string {
	b := make([]byte, 5)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Shell) settingsNotifications(c *ui.Context) {
	sp := Spacing()
	t := c.Theme()
	ui.Text(c, "When no paired phone can be told, Solco calls these URLs with the notification's words — the project and Agent, the question, a short excerpt of what the Agent said — and a solco:// link that opens the app. Nothing else of this Mac leaves it.").
		FontSize(Typography().Caption).TextColor(t.TextMuted)

	if runtime.GOOS == "darwin" {
		settingsGroup(c, "On this Mac",
			row("Show alerts in the notch", "An Agent that needs you, finished work, a Feedback question or a failed Automation grows out of the notch for a few seconds. Off by default.", func() {
				if v, changed := settingSwitch(c, "Show alerts in the notch", s.settings.Notifications.Island); changed {
					s.applySettings(func(cur *settings.Settings) error { cur.Notifications.Island = v; return nil })
					if !v {
						s.islandOff()
					}
				}
			}),
		)
	}

	channels := s.settings.Notifications.Channels
	rows := make([]settingsRow, 0, len(channels))
	for _, ch := range channels {
		id := ch.ID
		name := ch.Name
		if name == "" {
			name = id
		}
		rows = append(rows, row(name, "", func() {
			ui.Row(c.Key("nch:" + id)).Gap(sp.S).AlignItems(ui.Center).Children(func() {
				if v, changed := settingSwitch(c, "Enable "+name, ch.Enabled); changed {
					s.applySettings(func(cur *settings.Settings) error {
						for i := range cur.Notifications.Channels {
							if cur.Notifications.Channels[i].ID == id {
								cur.Notifications.Channels[i].Enabled = v
							}
						}
						return nil
					})
				}
				if regularButton(c, "Remove").Label("Remove " + name).Clicked() {
					_ = s.commitStore().Delete(notifyhook.SecretKey(id))
					s.applySettings(func(cur *settings.Settings) error {
						kept := cur.Notifications.Channels[:0:0]
						for _, x := range cur.Notifications.Channels {
							if x.ID != id {
								kept = append(kept, x)
							}
						}
						cur.Notifications.Channels = kept
						return nil
					})
					s.pendingToast = "Channel removed."
				}
			})
		}))
	}
	if len(rows) == 0 {
		rows = append(rows, row("No channels", "Add a webhook below, for example your own Feishu Worker.", func() {}))
	}
	settingsGroup(c, "Webhook channels", rows...)

	settingsCard(c, "Add a channel", func() {
		ui.Column(c).FillWidth().Gap(sp.S).Children(func() {
			textField(c, &s.notifyUI.name).FillWidth().Label("Channel name").Placeholder("Name, e.g. Feishu")
			textField(c, &s.notifyUI.url).FillWidth().Label("Webhook URL").Placeholder("https://… (the URL is stored encrypted and never shown again)").Password()
			textField(c, &s.notifyUI.token).FillWidth().Label("Bearer token").Placeholder("Bearer token (optional)").Password()
			add := regularPrimaryButton(c, "Add Channel").Label("Add channel")
			add.Disabled(len(channels) >= notifyChannelsMax)
			if add.Clicked() {
				s.addNotificationChannel()
			}
		})
	})

	settingsGroup(c, "Delivery",
		row("Phone grace", "A connected phone gets this long to open a waiting question before the channels are called. A phone that is not connected does not wait.", func() {
			label := notifyGraceLabel(s.settings.Notifications.Grace())
			if selectField(c, &label, notifyGraceLabels()).Label("Phone grace").Width(160).Changed() {
				for _, ch := range notifyGraceChoices {
					if ch.label == label {
						seconds := ch.seconds
						s.applySettings(func(cur *settings.Settings) error { cur.Notifications.GraceSeconds = seconds; return nil })
					}
				}
			}
		}),
		row("Check", "Sends one test message to every enabled channel.", func() {
			ui.Column(c).Gap(sp.XXS).Children(func() {
				ui.Row(c).Gap(sp.S).AlignItems(ui.Center).Children(func() {
					test := regularButton(c, "Send Test").Label("Send test")
					test.Disabled(s.notifyUI.testing)
					if test.Clicked() {
						s.startNotificationTest()
					}
					if s.notifyUI.testing {
						ui.Spinner(c).Size(13, 13)
					}
				})
				if s.notifyUI.result != "" {
					ui.Text(c, s.notifyUI.result).FontSize(Typography().Caption).TextColor(t.TextMuted)
				}
			})
		}),
	)
}

// addNotificationChannel validates the drafts, keeps the URL and token in the credential file and the channel's
// identity in settings.
func (s *Shell) addNotificationChannel() {
	d := &s.notifyUI
	raw := strings.TrimSpace(d.url)
	if err := notifyhook.ValidateURL(raw); err != nil {
		s.pendingToast = err.Error()
		return
	}
	token := strings.TrimSpace(d.token)
	if len(raw) > 2048 || len(token) > 512 {
		s.pendingToast = "The URL or token is too long."
		return
	}
	name := strings.TrimSpace(d.name)
	if name == "" {
		if u, err := url.Parse(raw); err == nil {
			name = u.Hostname()
		}
	}
	if len([]rune(name)) > 60 {
		name = string([]rune(name)[:60])
	}
	id := newChannelID()
	if err := s.commitStore().Set(notifyhook.SecretKey(id), notifyhook.EncodeSecret(raw, token)); err != nil {
		s.pendingToast = "Could not save the channel: " + firstErrLine(err)
		return
	}
	s.applySettings(func(cur *settings.Settings) error {
		cur.Notifications.Channels = append(cur.Notifications.Channels, settings.NotificationChannel{ID: id, Name: name, Enabled: true})
		return nil
	})
	*d = notifyChannelsUI{result: d.result}
	s.pendingToast = "Channel added."
}

// startNotificationTest posts one test message to every enabled channel and reports per channel, by name only.
func (s *Shell) startNotificationTest() {
	if s.notifyUI.testing || s.userDataDir == "" {
		return
	}
	dir := s.userDataDir
	s.notifyUI.testing, s.notifyUI.result = true, ""
	go func() {
		targets, _ := notifyhook.Load(dir)
		var lines []string
		for _, tg := range targets {
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			err := notifyhook.Sender{}.Test(ctx, tg)
			cancel()
			if err != nil {
				lines = append(lines, tg.Name+": "+err.Error())
			} else {
				lines = append(lines, tg.Name+": delivered")
			}
		}
		if len(lines) == 0 {
			lines = []string{"No enabled channel."}
		}
		s.applyOnUI(func() {
			s.notifyUI.testing = false
			s.notifyUI.result = strings.Join(lines, " · ")
		})
	}()
}
