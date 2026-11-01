package chatsession

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/thewei/solco/internal/conversation"
)

/**
 * [INPUT]: 依赖 Herdr pane.read 的可见屏文本（调用方读取，本文件不做 IO）
 * [OUTPUT]: 提供 DetectAgentPrompt（Cursor Agent 与 Antigravity CLI 的 shell 权限页 → 带按键的 PendingInteraction）与 DetectTerminalDialog（只能在终端里答的页：工作区信任、Cursor AskQuestion、agy 首次引导）
 * [POS]: 两个新 Provider 的刮屏提示（2026-11-01 真机矩阵，见 designs/cursor-agy-chat/）：只认已验证的形状，认不出就返回 false 退回 Terminal；
 *        永不提供持久化选项（Cursor 的 allowlist / Run Everything，agy 的 “Persist to settings.json”）；按键走 PendingInteraction.OptionKeys / 方向键，发送仍是 Herdr agent.send_keys
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

var (
	cursorRunOnce  = regexp.MustCompile(`Run \(once\) \(y\)`)
	cursorSkipRow  = regexp.MustCompile(`Skip & tell the agent`)
	cursorCommand  = regexp.MustCompile(`^\s*\$\s+(.+?)(?:\s+in\s+\S+)?\s*$`)
	agyRequesting  = regexp.MustCompile(`^\s*Requesting permission`)
	agyPersistRow  = regexp.MustCompile(`(?i)persist to settings`)
	agySelectedRow = regexp.MustCompile(`^\s*[❯>]\s*\d+\.`)
)

const (
	maxPromptCommand = 300
	maxOptionLabel   = 120
)

// DetectAgentPrompt reads the approval sheet of Cursor Agent or Antigravity
// CLI. The returned interaction carries the exact keys of each offered option.
func DetectAgentPrompt(screen string) (conversation.PendingInteraction, bool) {
	lines := screenLines(screen)
	if interaction, ok := detectCursorApproval(lines); ok {
		return interaction, true
	}
	return detectAgyApproval(lines)
}

func screenLines(screen string) []string {
	lines := strings.Split(strings.ReplaceAll(screen, "\r\n", "\n"), "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// detectCursorApproval parses
//
//	$  <command> in .
//	Run this command?
//	Not in allowlist: echo
//	→ Run (once) (y)
//	  Add Shell(echo) to allowlist? (tab)
//	  Run Everything (shift+tab)
//	  Skip & tell the agent what to do instead (esc or n)
//
// `y` runs once; `n` opens a text step that an empty Enter answers. The
// allowlist and Run Everything rows persist, so they are not offered.
func detectCursorApproval(lines []string) (conversation.PendingInteraction, bool) {
	once := lastLineIndex(lines, cursorRunOnce)
	if once < 0 {
		return conversation.PendingInteraction{}, false
	}
	skip := false
	for i := once + 1; i < len(lines) && i <= once+6; i++ {
		if cursorSkipRow.MatchString(lines[i]) {
			skip = true
			break
		}
	}
	if !skip {
		return conversation.PendingInteraction{}, false
	}
	question, command := "", ""
	for i := once - 1; i >= 0 && i >= once-8; i-- {
		text := strings.TrimSpace(lines[i])
		switch {
		case question == "" && strings.HasSuffix(text, "?") && !strings.Contains(text, "allowlist"):
			question = text
		case question != "" && command == "":
			if match := cursorCommand.FindStringSubmatch(lines[i]); match != nil {
				command = strings.TrimSpace(match[1])
			}
		}
	}
	if question == "" {
		return conversation.PendingInteraction{}, false
	}
	return conversation.PendingInteraction{
		Kind:       conversation.InteractionPermission,
		Prompt:     approvalPrompt(question, command),
		Options:    []string{"Run once", "Skip"},
		OptionKeys: [][]string{{"y"}, {"n", "enter"}},
	}, true
}

// detectAgyApproval parses
//
//	Requesting permission for:
//	   <command>
//
//	Run this command?
//	> 1. Yes, run command
//	  2. Yes, and always allow in this conversation for commands that start with '…'
//	  3. Yes, and always allow for commands that start with '…' (Persist to settings.json)
//	  4. No, cancel
//
// Rows are chosen by arrow keys from the highlighted row (a bare digit also
// selects, but arrows are the one transport every provider shares); the row
// that persists to settings.json is not offered.
func detectAgyApproval(lines []string) (conversation.PendingInteraction, bool) {
	head := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if agyRequesting.MatchString(lines[i]) {
			head = i
			break
		}
	}
	if head < 0 {
		return conversation.PendingInteraction{}, false
	}
	var labels []string
	selected := 0
	question := ""
	var command []string
	for i := head + 1; i < len(lines); i++ {
		if match := optionLine.FindStringSubmatch(lines[i]); match != nil {
			if agySelectedRow.MatchString(lines[i]) {
				selected = len(labels)
			}
			labels = append(labels, strings.TrimSpace(match[2]))
			continue
		}
		text := strings.TrimSpace(lines[i])
		switch {
		case len(labels) > 0:
			// first non-option row after the run (the key hints) ends it
			i = len(lines)
		case text == "":
		case strings.HasSuffix(text, "?"):
			question = text
		default:
			command = append(command, text)
		}
	}
	if question == "" || len(labels) < 2 {
		return conversation.PendingInteraction{}, false
	}
	interaction := conversation.PendingInteraction{
		Kind:   conversation.InteractionPermission,
		Prompt: approvalPrompt(question, strings.Join(command, " ")),
	}
	for index, label := range labels {
		if agyPersistRow.MatchString(label) {
			continue
		}
		keys := make([]string, 0, 4)
		for step := selected; step < index; step++ {
			keys = append(keys, "down")
		}
		for step := index; step < selected; step++ {
			keys = append(keys, "up")
		}
		interaction.Options = append(interaction.Options, clip(label, maxOptionLabel))
		interaction.OptionKeys = append(interaction.OptionKeys, append(keys, "enter"))
	}
	if len(interaction.Options) < 2 {
		return conversation.PendingInteraction{}, false
	}
	return interaction, true
}

func approvalPrompt(question, command string) string {
	command = strings.Join(strings.Fields(command), " ")
	if command == "" {
		return question
	}
	return fmt.Sprintf("%s `%s`", question, clip(command, maxPromptCommand))
}

func clip(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…"
}

// terminalDialogs are the full-screen pages the agent can only be answered
// on in its own terminal: Chat cannot structure them (or must not decide for
// the user). The status Herdr reports while they are up is not `blocked`
// (Cursor: idle; Antigravity onboarding: done), so the screen is the only
// signal. Each reason is shown to the user.
var terminalDialogs = []struct {
	marker *regexp.Regexp
	reason string
}{
	{regexp.MustCompile(`Workspace Trust Required`), "Cursor Agent is asking whether to trust this folder."},
	{regexp.MustCompile(`Space select · Enter next/submit`), "Cursor Agent is asking a question in the terminal."},
	{regexp.MustCompile(`Do you trust the contents of this project\?`), "Antigravity CLI is asking whether to trust this folder."},
	{regexp.MustCompile(`Terms of Service & Data Use|Choose your color scheme`), "Antigravity CLI is showing its first-run setup."},
}

// DetectTerminalDialog reports a terminal-only page showing on screen.
func DetectTerminalDialog(screen string) (string, bool) {
	for _, dialog := range terminalDialogs {
		if dialog.marker.MatchString(screen) {
			return dialog.reason, true
		}
	}
	return "", false
}
