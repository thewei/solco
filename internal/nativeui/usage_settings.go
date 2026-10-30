package nativeui

import (
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/usage"
)

/**
 * [INPUT]: 依赖 usage_state.go 的开关/令牌/刷新动作、internal/usage 的 Result 与 ID、controls.go 的字段与按钮、settingsGroup/row/settingSwitch 共享设置组件
 * [OUTPUT]: usageSettingsPath、Shell.settingsUsage（/settings/quota：每家一组——开关、令牌字段（Keychain）、智谱平台、状态与 Check Now）、Shell.usageProviderOn
 * [POS]: Usage 页的设置宿主：只放开关与令牌；令牌只写进 mygo.Secrets（Keychain，key 为 usage.<id>.token），字段永不回显已存值，设置文件里只有开关与智谱平台
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const usageSettingsPath = "/settings/quota"

// usageProviderOn is a provider's switch as shown: the saved preference, or
// the pending first-run choice before the first run was committed.
func (s *Shell) usageProviderOn(id usage.ID) bool {
	if !s.settings.Usage.Configured {
		return s.usage.firstRun[id]
	}
	return s.usageEnabledFor(id)
}

// usageTokenLabel names what a provider's token field takes.
func usageTokenLabel(id usage.ID) string {
	switch id {
	case usage.Zhipu:
		return "API key"
	case usage.Copilot:
		return "Personal access token"
	}
	return "Access token"
}

func (s *Shell) settingsUsage(c *ui.Context) {
	t := c.Theme()
	st := s.usage
	s.loadUsageTokens()
	ui.Text(c, "Quota reads each agent's own limits when you open its page or its Header tab, or refresh it. Nothing runs in the background, and nothing is recorded.").
		FontSize(Typography().BodySmall).TextColor(t.TextMuted)

	for _, id := range usage.All {
		id := id
		rows := []settingsRow{
			row("Show in Quota", usageConsentNote(id), func() {
				if on, changed := settingSwitch(c, "Show "+id.Title()+" in Quota", s.usageProviderOn(id)); changed {
					s.requestUsageProvider(id, on)
				}
			}),
		}
		if id == usage.Zhipu {
			rows = append(rows, row("Platform", "Where your key was issued", func() {
				idx := 0
				if s.settings.Usage.ZhipuSite == usage.ZhipuChina {
					idx = 1
				}
				if segmentedControl(c, &idx, "International", "China").Label("Zhipu platform").Changed() {
					site := usage.ZhipuGlobal
					if idx == 1 {
						site = usage.ZhipuChina
					}
					s.setUsageZhipuSite(site)
				}
			}))
		}
		if usageHasToken(id) {
			label := usageTokenLabel(id)
			note := "Stored in the Keychain; never shown again"
			if id != usage.Zhipu {
				note = "Optional — replaces the sign-in Solco would read. Stored in the Keychain"
			}
			rows = append(rows, row(label, note, func() {
				draft := st.drafts[id]
				if draft == nil {
					draft = new(string)
					st.drafts[id] = draft
				}
				ui.Row(c).Gap(Spacing().M).AlignItems(ui.Center).Children(func() {
					placeholder := label
					if st.tokens[id] {
						placeholder = "Saved — paste a new one to replace"
					}
					field := textField(c, draft).Width(240).Label(label).Placeholder(placeholder).Password()
					submitted := field.Submitted()
					if (compactPrimaryButton(c, "Save").Clicked() || submitted) && strings.TrimSpace(*draft) != "" {
						s.saveUsageToken(id, *draft)
					}
					if st.tokens[id] {
						if compactButton(c, "Remove").Clicked() {
							s.removeUsageToken(id)
						}
					}
				})
			}))
		}
		if s.usageProviderOn(id) && s.settings.Usage.Configured {
			rows = append(rows, row("Status", "", func() {
				res := st.svc.Result(id)
				badge, _ := usageBadge(res, st.svc.Busy(id), time.Now())
				ui.Row(c).Gap(Spacing().M).AlignItems(ui.Center).Children(func() {
					valueText(c, badge)
					if compactButton(c, "Check Now").Clicked() {
						s.refreshUsageIDs([]usage.ID{id}, true)
					}
				})
			}))
		}
		r := rows[0]
		r.lead = func() { usageMark(c, id, 20) }
		rows[0] = r
		settingsGroup(c, id.Title(), rows...)
	}
}
