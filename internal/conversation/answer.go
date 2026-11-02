package conversation

import (
	"regexp"
	"slices"
)

/**
 * [INPUT]: 依赖 PendingInteraction（选项、起始高亮行、确认键、EscapeOption）
 * [OUTPUT]: 提供 (PendingInteraction).AnswerKeys（单选 → 方向键序列）与 MultiSelectKeys（多选 → space/down/enter 序列）
 * [POS]: 把"选这一项要按哪些键"收成纯函数（2026-10-31 自 nativeui.answerInteraction 抽出）：桌面 Chat 桥与手机 Remote Host 同一套规则；
 *        只算键，不发送——发送仍走 Herdr agent.send_keys，不用数字键（数字会被 TUI 当文本）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// chatAboutOption matches the "discuss instead of picking" affordance.
// Selecting it makes Claude Code tear the question down (resolved as
// declined) and return to its normal input, so a typed answer delivers as an
// ordinary prompt after an Escape — never as arrow navigation.
var chatAboutOption = regexp.MustCompile(`(?i)^chat about`)

// AnswerKeys returns the named keys that choose option: Escape for the
// EscapeOption (and for "Chat about this" when an answer is typed), else the
// arrow navigation relative to the row the selector opens on, then the
// provider's confirm key. An option outside Options fails closed.
func (p PendingInteraction) AnswerKeys(option string, typed bool) ([]string, error) {
	index := slices.Index(p.Options, option)
	if index < 0 {
		return nil, ErrUnknownOption
	}
	if len(p.OptionKeys) == len(p.Options) && len(p.OptionKeys[index]) > 0 {
		return slices.Clone(p.OptionKeys[index]), nil
	}
	if p.EscapeOption != "" && option == p.EscapeOption {
		return []string{"esc"}, nil
	}
	if typed && chatAboutOption.MatchString(option) {
		return []string{"esc"}, nil
	}
	keys := make([]string, 0, max(index, p.Start)+1)
	for i := p.Start; i < index; i++ {
		keys = append(keys, "down")
	}
	for i := index; i < p.Start; i++ {
		keys = append(keys, "up")
	}
	commit := "enter"
	if p.Commit != "" {
		commit = p.Commit
	}
	return append(keys, commit), nil
}

// MultiSelectKeys ticks the picked rows of a multi-select prompt whose
// cursor opens on the first row: space on each picked row, down between
// them, enter to submit.
func MultiSelectKeys(options, picks []string) []string {
	last := -1
	for index, option := range options {
		if slices.Contains(picks, option) {
			last = index
		}
	}
	keys := make([]string, 0, 2*(last+1)+1)
	for index := 0; index <= last; index++ {
		if slices.Contains(picks, options[index]) {
			keys = append(keys, "space")
		}
		if index < last {
			keys = append(keys, "down")
		}
	}
	return append(keys, "enter")
}

// MultiKeys is the key sequence that ticks picks of this multi-select prompt
// and submits it: MultiSelectKeys unless the prompt has a separate commit row
// (CommitRow), in which case it ticks the picked rows (space, down between),
// walks down to the commit row and presses enter there — enter on an option
// row toggles it instead of committing.
func (p PendingInteraction) MultiKeys(picks []string) []string {
	if p.CommitRow <= 0 {
		return MultiSelectKeys(p.Options, picks)
	}
	var keys []string
	at := 0
	for index, option := range p.Options {
		if !slices.Contains(picks, option) {
			continue
		}
		for ; at < index; at++ {
			keys = append(keys, "down")
		}
		keys = append(keys, "space")
	}
	for ; at < p.CommitRow; at++ {
		keys = append(keys, "down")
	}
	return append(keys, "enter")
}
