package nativeui

/**
 * [INPUT]: 依赖 persisted Workbench Settings、配置的 OpenAI 兼容 API 通道（endpoint/model + Keychain key git.commitapi.token）与用户可编辑的 commit rules
 * [OUTPUT]: 对外提供 Shell.settingsGit 渲染 Git 提交风格规则（仅风格；安全约束固定在 agent.BuildCommitPrompt，不展示）、AI commit 通道配置（endpoint/model/key/测试）与通道就绪状态
 * [POS]: nativeui Settings 中的 Git 偏好配置子面板；key 只进 Keychain，字段永不回显已存值；未配置通道时生成入口在别处禁用
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/settings"
)

// commitAPIPresets prefill the endpoint field; every entry is an
// OpenAI-compatible chat/completions base URL.
var commitAPIPresets = []string{
	"https://api.openai.com/v1",
	"https://api.anthropic.com/v1",
	"https://open.bigmodel.cn/api/paas/v4",
	"https://api.deepseek.com",
}

// commitAPITestTimeout bounds the Settings test request.
const commitAPITestTimeout = 15 * time.Second

func commitPromptPreference(value string) string {
	if strings.TrimSpace(value) == "" {
		return settings.DefaultCommitPrompt
	}
	return value
}

// commitEndpointHost is the display host of an endpoint; "" when unset.
func commitEndpointHost(endpoint string) string {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil {
		return ""
	}
	return parsed.Host
}

func (s *Shell) settingsGit(c *ui.Context) {
	sp := Spacing()
	t := c.Theme()
	settingsCard(c, "Commit message rules", func() {
		textArea(c, &s.settingsGitPromptDraft).FillWidth().Height(112).Label("Git commit rules")
		ui.Row(c).Gap(sp.S).Children(func() {
			if regularPrimaryButton(c, "Save").Label("Save commit rules").Clicked() {
				draft := strings.TrimSpace(s.settingsGitPromptDraft)
				if len(draft) == 0 || len(draft) > 5000 {
					s.pendingToast = "Commit rules must contain 1–5000 characters."
					return
				}
				s.applySettings(func(v *settings.Settings) error { v.Workbench.CommitPrompt = draft; return nil })
				s.pendingToast = "Commit rules saved."
			}
			if regularButton(c, "Restore Default").Label("Restore default commit rules").Clicked() {
				s.settingsGitPromptDraft = settings.DefaultCommitPrompt
				s.applySettings(func(v *settings.Settings) error { v.Workbench.CommitPrompt = ""; return nil })
				s.pendingToast = "Default commit rules restored."
			}
		})
	})
	settingsGroup(c, "Commit message generator",
		row("Status", "", func() {
			cfg := s.settings.Workbench.CommitAPI
			if !s.commitGeneratorUsable() {
				ui.Text(c, "Unavailable until endpoint, model and API key are configured.").
					FontSize(Typography().Caption).TextColor(t.TextMuted)
				return
			}
			ui.Textf(c, "Ready: %s at %s.", strings.TrimSpace(cfg.Model), commitEndpointHost(cfg.Endpoint)).
				FontSize(Typography().Caption).TextColor(t.TextMuted)
		}),
		row("Endpoint", "OpenAI-compatible base URL; /chat/completions is appended.", func() {
			ui.Row(c).Gap(sp.S).AlignItems(ui.Center).Children(func() {
				comboField(c, &s.settingsCommitEndpointDraft, commitAPIPresets).Width(340).
					Label("API endpoint").Placeholder("https://api.openai.com/v1")
				if regularButton(c, "Save").Label("Save endpoint").Clicked() {
					text := strings.TrimSpace(s.settingsCommitEndpointDraft)
					if _, err := agent.CommitChatURL(text); err != nil {
						s.pendingToast = firstErrLine(err)
						return
					}
					s.applySettings(func(v *settings.Settings) error { v.Workbench.CommitAPI.Endpoint = text; return nil })
					s.pendingToast = "Endpoint saved."
				}
			})
		}),
		row("Model", "The provider's model name, e.g. glm-4.6 or gpt-4o-mini.", func() {
			ui.Row(c).Gap(sp.S).AlignItems(ui.Center).Children(func() {
				textField(c, &s.settingsCommitModelDraft).Width(240).Label("Model").Placeholder("glm-4.6")
				if regularButton(c, "Save").Label("Save model").Clicked() {
					text := strings.TrimSpace(s.settingsCommitModelDraft)
					if text == "" || len(text) > 200 {
						s.pendingToast = "Model must contain 1–200 characters."
						return
					}
					s.applySettings(func(v *settings.Settings) error { v.Workbench.CommitAPI.Model = text; return nil })
					s.pendingToast = "Model saved."
				}
			})
		}),
		row("API key", "Stored in the macOS Keychain; never echoed, logged or written to settings.", func() {
			ui.Row(c).Gap(sp.S).AlignItems(ui.Center).Children(func() {
				textField(c, &s.settingsCommitKeyDraft).Width(240).Label("API key").
					Placeholder("Paste API key").Password()
				if regularButton(c, "Save").Label("Save key").Clicked() {
					key := strings.TrimSpace(s.settingsCommitKeyDraft)
					if key == "" || len(key) > 4096 {
						s.pendingToast = "API key must contain 1–4096 characters."
						return
					}
					if err := s.commitStore().Set(commitAPIKeyName, key); err != nil {
						s.pendingToast = "Could not save the API key: " + firstErrLine(err)
						return
					}
					s.settingsCommitKeyDraft = ""
					s.commitAPIKeySet = true
					s.pendingToast = "API key saved."
				}
				remove := regularButton(c, "Remove").Label("Remove key")
				remove.Disabled(!s.commitAPIKeySet)
				if remove.Clicked() {
					if err := s.commitStore().Delete(commitAPIKeyName); err != nil {
						s.pendingToast = "Could not remove the API key: " + firstErrLine(err)
						return
					}
					s.commitAPIKeySet = false
					s.pendingToast = "API key removed."
				}
			})
		}),
		row("Check", "Sends a one-word request through the saved channel.", func() {
			ui.Column(c).Gap(sp.XXS).Children(func() {
				ui.Row(c).Gap(sp.S).AlignItems(ui.Center).Children(func() {
					test := regularButton(c, "Send Test Request")
					test.Disabled(!s.commitGeneratorUsable() || s.commitAPITestRunning)
					if test.Clicked() {
						s.startCommitAPITest()
					}
					if s.commitAPITestRunning {
						ui.Spinner(c).Size(13, 13)
					}
				})
				if s.commitAPITestResult != "" {
					ui.Text(c, s.commitAPITestResult).FontSize(Typography().Caption).TextColor(t.TextMuted)
				}
			})
		}),
	)
}

// startCommitAPITest verifies the whole channel (endpoint, model, key) with
// one bounded request; the result surfaces as a caption, never a dialog.
func (s *Shell) startCommitAPITest() {
	if s.commitAPITestRunning {
		return
	}
	cfg := s.settings.Workbench.CommitAPI
	key, ok := s.commitAPIKey()
	if !ok {
		s.commitAPITestResult = "No API key is stored."
		return
	}
	s.commitAPITestRunning = true
	s.commitAPITestResult = ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), commitAPITestTimeout)
		defer cancel()
		err := agent.CheckCommitAPI(ctx, agent.CommitRequest{Endpoint: cfg.Endpoint, Model: cfg.Model, APIKey: key})
		s.applyOnUI(func() {
			s.commitAPITestRunning = false
			if ctx.Err() != nil {
				s.commitAPITestResult = "The test request timed out."
				return
			}
			if err != nil {
				s.commitAPITestResult = firstErrLine(err)
				return
			}
			s.commitAPITestResult = "OK — " + strings.TrimSpace(cfg.Model) + " replied."
		})
	}()
}
