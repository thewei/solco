package chatsession

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/thewei/solco/internal/conversation"
)

/**
 * [INPUT]: 一屏可见文本（Herdr pane.read 的 visible 源）
 * [OUTPUT]: DetectPicker——Claude Code / Codex 编号列表选择器（/model、/permissions）→ 一张 Question 卡（`Picker`、可选 `Commit` 键、自带 Close 行）
 * [POS]: 桌面 chat_picker.go 与手机 Remote Host 共用的纯解析（自 nativeui 迁入）：只在 Solco 刚替用户发出 picker 命令后才读屏，所以不是对任意 Agent 提示的猜测；读不了的选择器（pi/omp 列表、/resume 搜索框）交给 Terminal；不含任何 IO
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// Chat structures the numbered list pickers of Claude Code and Codex
// (/model, /permissions) as a Question card. Only Solco opens them — right
// after the user sent the command from the composer — so the screen read is
// never a guess about an arbitrary agent prompt. Pickers it cannot read
// (pi/omp lists, /resume search boxes) hand over to Terminal instead.

// pickerRow matches a numbered picker row: indent, optional selector mark
// (❯ › > →) or scroll arrow (↓ ↑), number, text.
var pickerRow = regexp.MustCompile(`^(\s*)([❯›>→↓↑]?)\s*(\d+)\.\s+(.*?)\s*$`)

// pickerFooter is the key hint every such picker ends with
// ("enter select · esc back", "Enter to set as default · … · Esc to cancel").
var pickerFooter = regexp.MustCompile(`(?i)\benter\b.*\besc\b`)

var pickerTitle = regexp.MustCompile(`^\s*(Select|Choose|Update|Switch|Set|Pick)\b`)

// pickerSessionOnly spots the footer key that applies a choice to this
// session only instead of saving it as the default ("s to use this session
// only" in Claude Code, "s session" in Codex's effort step).
var pickerSessionOnly = regexp.MustCompile(`(?i)\bs (?:to use this )?session(?: only)?\b`)

var pickerGap = regexp.MustCompile(`\s{2,}`)

// PickerClose is the card's own row for the picker's Escape.
const PickerClose = "Close"

// DetectPicker reads a visible screen for a numbered list picker.
func DetectPicker(screen string) (conversation.PendingInteraction, bool) {
	lines := strings.Split(strings.ReplaceAll(screen, "\r\n", "\n"), "\n")
	footer := -1
	for index := len(lines) - 1; index >= 0; index-- {
		if pickerFooter.MatchString(lines[index]) {
			footer = index
			break
		}
	}
	// Some picker steps carry no key hint at all (Claude Code's "Switch
	// model?" confirmation is two rows); then the rows must end the screen
	// and one of them must carry the selector mark.
	hinted := footer >= 0
	if !hinted {
		footer = len(lines)
		for footer > 0 && strings.TrimSpace(lines[footer-1]) == "" {
			footer--
		}
	}
	// The last run of rows above the footer; a gap of more than four lines
	// ends it (conversation text above may contain its own "1. foo").
	var rows []int
	for index := footer - 1; index >= 0; index-- {
		if !pickerRow.MatchString(lines[index]) {
			continue
		}
		if len(rows) > 0 && rows[0]-index > 4 {
			break
		}
		rows = append([]int{index}, rows...)
		if len(rows) > 40 {
			break
		}
	}
	if len(rows) < 2 {
		return conversation.PendingInteraction{}, false
	}
	if !hinted {
		marked := false
		for _, index := range rows {
			marked = marked || strings.ContainsAny(pickerRow.FindStringSubmatch(lines[index])[2], "❯›>→")
		}
		if !marked || footer-rows[len(rows)-1] > 3 {
			return conversation.PendingInteraction{}, false
		}
	}
	var interaction conversation.PendingInteraction
	interaction.Kind = conversation.InteractionQuestion
	interaction.Prompt = "Choose an option"
	for back := rows[0] - 1; back >= 0 && back >= rows[0]-12; back-- {
		text := strings.TrimSpace(lines[back])
		if strings.HasPrefix(text, "▔") || strings.HasPrefix(text, "─") {
			break
		}
		if pickerTitle.MatchString(text) {
			interaction.Prompt = text // keep walking: the top-most match is the title
		}
	}
	// A pane shorter than the list clips it; say so instead of passing a
	// partial list off as the whole one.
	partial := pickerRow.FindStringSubmatch(lines[rows[0]])[3] != "1"
	for _, index := range rows {
		partial = partial || strings.ContainsAny(pickerRow.FindStringSubmatch(lines[index])[2], "↓↑")
	}
	for _, line := range lines[rows[len(rows)-1]+1 : footer] {
		partial = partial || strings.HasPrefix(strings.TrimSpace(line), "…")
	}
	if partial {
		interaction.Prompt += " (rows beyond the screen: use Terminal)"
	}
	seen := map[string]int{}
	for position, index := range rows {
		match := pickerRow.FindStringSubmatch(lines[index])
		if strings.ContainsAny(match[2], "❯›>→") && interaction.Start == 0 {
			interaction.Start = position
		}
		label, desc := match[4], ""
		descCol := 0
		if parts := pickerGap.Split(label, 2); len(parts) == 2 {
			label, desc = parts[0], parts[1]
			descCol = utf8.RuneCountInString(lines[index][:strings.LastIndex(lines[index], desc)])
		}
		end := footer
		if position+1 < len(rows) {
			end = rows[position+1]
		}
		for _, line := range lines[index+1 : end] {
			indent := utf8.RuneCountInString(line) - utf8.RuneCountInString(strings.TrimLeft(line, " "))
			text := strings.TrimSpace(line)
			if text == "" || descCol == 0 || indent < descCol-2 {
				continue
			}
			desc = strings.TrimSpace(desc + " " + text)
		}
		seen[label]++
		interaction.Options = append(interaction.Options, label)
		interaction.Descriptions = append(interaction.Descriptions, desc)
	}
	// Duplicate labels would make an answer ambiguous: the description (or
	// the position) tells them apart.
	count := map[string]int{}
	for index, label := range interaction.Options {
		if seen[label] < 2 {
			continue
		}
		count[label]++
		if desc := interaction.Descriptions[index]; desc != "" {
			interaction.Options[index] = label + " · " + desc
		} else {
			interaction.Options[index] = fmt.Sprintf("%s #%d", label, count[label])
		}
	}
	interaction.Options = append(interaction.Options, PickerClose)
	interaction.Descriptions = append(interaction.Descriptions, "")
	interaction.EscapeOption = PickerClose
	interaction.Picker = true
	if pickerSessionOnly.MatchString(lines[footer]) {
		interaction.Commit = "s"
	}
	return interaction, true
}
