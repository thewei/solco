package chatsession

import (
	"regexp"
	"strings"

	"github.com/thewei/solco/internal/conversation"
)

/**
 * [INPUT]: 依赖 Herdr pane.read 的可见屏文本（调用方读取，本文件不做 IO）
 * [OUTPUT]: 提供 DetectScreenPrompt（Claude Code AskUserQuestion / 权限页 / omp 收尾 Review 页 → 类型、问题、选项）与 IsOmpReviewSheet
 * [POS]: 刮屏提问的纯解析（2026-10-31 自 nativeui/chat_hooks.go 迁入）：桌面 Shell 与手机 Remote Host 共用；只认已验证的提示形状，认不出就返回 false 退回 Terminal
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// IsOmpReviewSheet reports omp's closing "Review answers" sheet, whose only
// row is the highlighted Submit (Cancel is Escape).
func IsOmpReviewSheet(screen string) bool { return ompReviewSheet.MatchString(screen) }

// optionLine matches Claude Code's numbered option rows ("❯ 1. Yes",
// "  2. herdr adapter"), with or without the selector mark.
var optionLine = regexp.MustCompile(`^[❯>•·\s]*(\d+)\.\s+(.+?)\s*$`)

// promptFooter matches the AskUserQuestion/permission footer — the
// strongest "an interactive prompt is showing" signal.
var promptFooter = regexp.MustCompile(`Enter to select`)

// dividerLine matches the full-width rule AskUserQuestion draws between
// questions and extra options.
var dividerLine = regexp.MustCompile(`^─+\s*$`)

// questionLine matches a prompt question in ASCII or full-width punctuation.
var questionSuffix = regexp.MustCompile(`[?？]\s*$`)

// ompReviewSheet matches omp's closing "Review answers" sheet of a
// multi-question ask, whose only row is the highlighted Submit (Cancel is ⎋).
var ompReviewSheet = regexp.MustCompile(`(?s)Review answers.*❯\s*Submit`)

// DetectScreenPrompt parses Claude Code's visible TUI text for its
// interactive prompts — AskUserQuestion (tabs + numbered options with
// description rows) and the permission sheet ("Do you want …"). A minimal
// port of the reference parser's core; the full provider table lives in
// herdr-web-ui's prompt.ts and ports incrementally.
func DetectScreenPrompt(screen string) (conversation.InteractionKind, string, []string, bool) {
	if ompReviewSheet.MatchString(screen) {
		return conversation.InteractionQuestion, "Submit your answers?", []string{"Submit", "Cancel"}, true
	}
	lines := strings.Split(strings.ReplaceAll(screen, "\r\n", "\n"), "\n")
	// Drop trailing empties so the last row is meaningful.
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	// Strong signal: the footer. Collect the numbered options between the
	// footer (and its divider) and the question line above them.
	if footer := lastLineIndex(lines, promptFooter); footer != -1 {
		if kind, prompt, options, ok := parseOptionRun(lines, footer-1); ok {
			return kind, prompt, options, true
		}
	}
	// Fallback: the last numbered-option run anywhere, with a question line
	// above it.
	return parseOptionRun(lines, len(lines)-1)
}

// parseOptionRun walks up from `from`, collecting the numbered-option run —
// description rows, blanks and dividers between options are part of the
// block — and then picks the closest row below-reading-as-a-question: the
// first candidate (bottom-up) ending in ?/？ or opening "Do you want".
// Plain numbered transcript content never qualifies, so the fallback pass
// cannot misfire on ordinary lists.
func parseOptionRun(lines []string, from int) (conversation.InteractionKind, string, []string, bool) {
	options := make([]string, 0, 4)
	var candidates []string
	for i := from; i >= 0 && i > from-40; i-- {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if dividerLine.MatchString(trimmed) {
			continue
		}
		if match := optionLine.FindStringSubmatch(line); match != nil {
			options = append([]string{strings.TrimSpace(match[2])}, options...)
			continue
		}
		if trimmed == "" {
			continue
		}
		candidates = append(candidates, trimmed)
	}
	if len(options) < 2 {
		return "", "", nil, false
	}
	prompt := ""
	for _, candidate := range candidates {
		if questionSuffix.MatchString(candidate) || strings.HasPrefix(candidate, "Do you want") {
			prompt = candidate
			break
		}
	}
	if prompt == "" {
		return "", "", nil, false
	}
	kind := conversation.InteractionQuestion
	if strings.Contains(prompt, "Do you want") {
		kind = conversation.InteractionPermission
	}
	return kind, prompt, options, true
}

// lastLineIndex returns the last line matching re, or -1.
func lastLineIndex(lines []string, re *regexp.Regexp) int {
	for i := len(lines) - 1; i >= 0; i-- {
		if re.MatchString(lines[i]) {
			return i
		}
	}
	return -1
}
