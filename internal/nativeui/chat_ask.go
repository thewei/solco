package nativeui

import (
	"encoding/json"
	"strings"

	"github.com/thewei/solco/internal/conversation"
)

/**
 * [INPUT]: 依赖 conversation.TimelineTurn（尚无结果的 ask 工具调用及其完整 Input）
 * [OUTPUT]: 提供 transcriptQuestion：把 omp/pi 的 `ask` 提问读成结构化问题序列（标题、问题、选项、说明、起始高亮行）；多问题按 omp 的顺序逐题出卡
 * [POS]: 非 Claude 终端提问的结构来源：问题就写在转录里（工具调用已落盘、结果未到），不必刮屏；
 *        答案仍走 InteractionBroker 的 CAS 与方向键 bridge，Claude 的屏幕解析路径不变
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// askOtherOption is the free-text row omp's ask prompt appends after the
// provider's own options (its TUI draws "Other (type your own)").
const askOtherOption = "Other (type your own)"

// askQuestion is one single-select question of an `ask` call.
type askQuestion struct {
	Header       string
	Prompt       string
	Options      []string
	Descriptions []string
	// Start is the row omp's selector highlights when the question opens
	// (the call's `recommended` index, else the first row): arrow
	// navigation is relative to it.
	Start int
	// Multi marks a multi-select question: ticked with space, submitted with
	// enter; it carries no Other row and no recommended start.
	Multi bool
}

// transcriptAsk is the pending `ask` call read from the transcript: the call
// id (identity across republishes) and its questions, which omp presents one
// after another.
type transcriptAsk struct {
	ToolID    string
	Questions []askQuestion
}

// transcriptQuestion returns the `ask` call that is the very last thing the
// conversation did and has no result yet — the agent is parked on its
// prompt. Every question needs ≥2 options; a multi-select question that also
// carries `recommended` (whose pre-ticking is unverified) and anything
// unparseable return false (the TUI scrape or the Terminal remains).
func transcriptQuestion(turns []conversation.TimelineTurn) (transcriptAsk, bool) {
	if len(turns) == 0 {
		return transcriptAsk{}, false
	}
	last := turns[len(turns)-1]
	if last.UserRow || len(last.Steps) == 0 {
		return transcriptAsk{}, false
	}
	tool := last.Steps[len(last.Steps)-1].Tool
	if tool == nil || tool.Name != "ask" || tool.Output != nil || tool.Input == nil {
		return transcriptAsk{}, false
	}
	var payload struct {
		Questions []struct {
			Header      string            `json:"header"`
			Question    string            `json:"question"`
			Multi       bool              `json:"multi"`
			Multiple    bool              `json:"multiSelect"`
			Recommended *int              `json:"recommended"`
			Options     []json.RawMessage `json:"options"`
		} `json:"questions"`
	}
	if json.Unmarshal([]byte(*tool.Input), &payload) != nil || len(payload.Questions) == 0 {
		return transcriptAsk{}, false
	}
	ask := transcriptAsk{ToolID: tool.ID}
	if ask.ToolID == "" {
		ask.ToolID = *tool.Input
	}
	for _, question := range payload.Questions {
		multi := question.Multi || question.Multiple
		if strings.TrimSpace(question.Question) == "" || (multi && question.Recommended != nil) {
			return transcriptAsk{}, false
		}
		item := askQuestion{Multi: multi, Header: strings.TrimSpace(question.Header), Prompt: strings.TrimSpace(question.Question)}
		for _, raw := range question.Options {
			var option struct {
				Label       string `json:"label"`
				Description string `json:"description"`
			}
			if json.Unmarshal(raw, &option) != nil {
				if json.Unmarshal(raw, &option.Label) != nil {
					continue
				}
			}
			if label := strings.TrimSpace(option.Label); label != "" {
				item.Options = append(item.Options, label)
				item.Descriptions = append(item.Descriptions, strings.TrimSpace(option.Description))
			}
		}
		if len(item.Options) < 2 {
			return transcriptAsk{}, false
		}
		if !multi {
			if r := question.Recommended; r != nil && *r > 0 && *r < len(item.Options) {
				item.Start = *r
			}
			item.Options = append(item.Options, askOtherOption)
			item.Descriptions = append(item.Descriptions, "")
		}
		ask.Questions = append(ask.Questions, item)
	}
	return ask, true
}
