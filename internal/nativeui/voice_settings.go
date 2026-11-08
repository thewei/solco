package nativeui

import (
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/settings"
	"github.com/thewei/solco/internal/voice"
)

/**
 * [INPUT]: settings.Voice（Source / ChatGPTConsent / 端点三项）、internal/voice 的 Codex 登录态状态
 * [OUTPUT]: Settings → Voice 页、"使用你的 ChatGPT 登录？"同意弹窗
 * [POS]: 听写来源的呈现层。来源只有两个：API 端点 | ChatGPT 订阅（读取 Codex 登录态，需弹窗同意，令牌不进设置）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const voiceStatusTTL = 5 * time.Second

// voiceChatGPTOn reports the ChatGPT source is chosen and consented.
func voiceChatGPTOn(v settings.VoiceSettings) bool {
	return v.Source == "chatgpt" && v.ChatGPTConsent
}

// voiceCurrentStatus reads the Codex sign-in state at most every few
// seconds (it is one small file; the page re-renders often).
func (s *Shell) voiceCurrentStatus(force bool) voice.Status {
	if force || time.Since(s.voiceStatusAt) > voiceStatusTTL {
		s.voiceStatus = (&voice.ChatGPT{}).Status()
		s.voiceStatusAt = time.Now()
	}
	return s.voiceStatus
}

func voiceStatusLine(status voice.Status) string {
	switch status.State {
	case voice.Ready:
		if status.Plan != "" {
			return "Signed in with ChatGPT · " + titleCase(status.Plan)
		}
		return "Signed in with ChatGPT"
	case voice.Expired:
		return "Sign-in expired — Codex renews it on the next recording"
	}
	return "Not signed in — sign in to Codex first"
}

func titleCase(v string) string {
	if v == "" {
		return v
	}
	return strings.ToUpper(v[:1]) + v[1:]
}

// settingsVoice is Settings → Voice: where the Chat composer's Mic sends
// the recording (2026-10-30 UX audit 3 moved it off General; 2026-10-31 added
// the ChatGPT-subscription source).
func (s *Shell) settingsVoice(c *ui.Context) {
	chatgpt := voiceChatGPTOn(s.settings.Voice)
	t := c.Theme()
	sourceNote := "Any OpenAI-compatible transcription endpoint, including local proxies."
	if chatgpt {
		sourceNote = "Uses the ChatGPT sign-in Codex already holds on this Mac. No key to paste."
	}
	rows := []settingsRow{
		row("Source", sourceNote, func() {
			chosen := 0
			if chatgpt {
				chosen = 1
			}
			if segmentedControl(c, &chosen, "API endpoint", "ChatGPT subscription").Label("Source").Changed() {
				if chosen == 1 {
					s.voiceConsentOpen = true // nothing changes until the user agrees
					return
				}
				s.applySettings(func(v *settings.Settings) error {
					v.Voice.Source, v.Voice.ChatGPTConsent = "api", false
					return nil
				})
			}
		}),
	}
	if chatgpt {
		status := s.voiceCurrentStatus(false)
		rows = append(rows,
			row("Account", "From Codex. The token stays in memory and is never saved or logged.", func() {
				color := t.TextMuted
				if status.State == voice.Ready {
					color = t.Success
				}
				ui.Row(c).Gap(Spacing().M).Children(func() {
					ui.Text(c, voiceStatusLine(status)).FontSize(Typography().BodySmall).TextColor(color)
					if status.State != voice.Ready && compactButton(c, "Check Again").Clicked() {
						s.voiceCurrentStatus(true)
					}
				})
			}),
			row("What is sent", "The clip goes to chatgpt.com — the same call the ChatGPT desktop app makes for dictation. OpenAI may keep it for about 30 days, and it counts against your plan's limits. Chat text is never sent.", func() {}),
		)
		settingsGroup(c, "Transcription", rows...)
		return
	}
	rows = append(rows,
		row("Endpoint", "", func() {
			s.settingText(c, "Endpoint", "https://api.openai.com/v1", &s.settingsVoiceEndpointDraft, s.settings.Voice.EndpointURL, false,
				func(v *settings.Settings, text string) { v.Voice.EndpointURL = text })
		}),
		row("API key", "", func() {
			s.settingText(c, "API key", "Uses OPENAI_API_KEY", &s.settingsVoiceKeyDraft, s.settings.Voice.APIKey, true,
				func(v *settings.Settings, text string) { v.Voice.APIKey = text })
		}),
		row("Model", "", func() {
			s.settingText(c, "Model", "whisper-1", &s.settingsVoiceModelDraft, s.settings.Voice.Model, false,
				func(v *settings.Settings, text string) { v.Voice.Model = text })
		}),
	)
	settingsGroup(c, "Transcription", rows...)
}

// voiceConsentDialog is the "may Solco use this sign-in?" confirmation.
func (s *Shell) voiceConsentDialog(c *ui.Context) {
	if !s.voiceConsentOpen {
		return
	}
	// Allow is index 0 and Cancel last: AlertDialog focuses the last button,
	// so Enter dismisses safely.
	if ui.AlertDialog(c, &s.voiceConsentOpen, "Use your ChatGPT sign-in for dictation?",
		"Solco reads the sign-in Codex stores on this Mac and uses it only to transcribe your recordings. "+
			"The audio is sent to chatgpt.com and OpenAI may keep it for about 30 days; it counts against your ChatGPT plan's limits. "+
			"The token stays in memory — Solco never saves it and never renews it (Codex does). "+
			"This is the same call the ChatGPT desktop app makes; OpenAI does not document it as an API for other apps, so using it is at your own risk. "+
			"You can switch back to an API endpoint at any time.",
		"Use ChatGPT Sign-In", "Cancel") == 0 {
		s.applySettings(func(v *settings.Settings) error {
			v.Voice.Source, v.Voice.ChatGPTConsent = "chatgpt", true
			return nil
		})
		s.voiceCurrentStatus(true)
	}
}

// voiceSourceName is the dictation source the mic tooltip names.
func voiceSourceName(v settings.VoiceSettings) string {
	if voiceChatGPTOn(v) {
		return "chatgpt"
	}
	return "api"
}
