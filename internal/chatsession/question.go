package chatsession

import (
	"encoding/json"
	"strings"

	"github.com/thewei/solco/internal/conversation"
)

/**
 * [INPUT]: 依赖 conversation.TimelineTurn（尚无结果的 ask 工具调用及其完整 Input）
 * [OUTPUT]: 提供 TranscriptQuestion：把 omp/pi 的 `ask` 提问读成结构化问题序列（标题、问题、选项、说明、起始高亮行）；多问题按 omp 的顺序逐题出卡
 * [POS]: 非 Claude 终端提问的结构来源（桌面 Shell 与手机 Remote Host 共用，2026-10-31 自 nativeui 迁入）：问题就写在转录里（工具调用已落盘、结果未到），不必刮屏；
 *        答案仍走各自的 CAS 与方向键（conversation.AnswerKeys），Claude 的屏幕解析路径见 screenprompt.go
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// AskOtherOption is the free-text row omp's ask prompt appends after the
// provider's own options (its TUI draws "Other (type your own)").
const AskOtherOption = "Other (type your own)"

// AgyWriteInOption is the free-text row Antigravity CLI's ask_question adds
// after its own options (its TUI draws "Write-in...").
const AgyWriteInOption = "Write-in..."

// PiTypeSomethingOption is the free-text row Pi's ask_user_question dialog
// (@juicesharp/rpiv-ask-user-question) appends after the authored options.
const PiTypeSomethingOption = "Type something."

// AskQuestion is one single-select question of an `ask` call.
type AskQuestion struct {
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
	// CommitRow is the row index of a multi-select question's separate commit
	// row (Pi's dialog: Type something. is row len(options), Next follows); 0
	// when enter on the last picked row submits (omp).
	CommitRow int
}

// TranscriptAsk is the pending `ask` call read from the transcript: the call
// id (identity across republishes) and its questions, which omp presents one
// after another.
type TranscriptAsk struct {
	ToolID    string
	Questions []AskQuestion
	// ParksWorking marks Pi's ask_user_question: its dialog never reports the
	// agent blocked, so the card is derived while the status reads `working`.
	ParksWorking bool
}

// TranscriptQuestion returns the `ask` (omp/pi) or `ask_question` (Antigravity
// CLI: questions one after another, options plain strings, `is_multi_select`,
// the cursor always opens on the first row even when a "(Recommended)" label
// sits elsewhere) call that is the very last thing the
// conversation did and has no result yet — the agent is parked on its
// prompt. Every question needs ≥2 options; a multi-select question that also
// carries `recommended` (whose pre-ticking is unverified) and anything
// unparseable return false (the TUI scrape or the Terminal remains).
func TranscriptQuestion(turns []conversation.TimelineTurn) (TranscriptAsk, bool) {
	if len(turns) == 0 {
		return TranscriptAsk{}, false
	}
	last := turns[len(turns)-1]
	if last.UserRow || len(last.Steps) == 0 {
		return TranscriptAsk{}, false
	}
	tool := last.Steps[len(last.Steps)-1].Tool
	if tool == nil || (tool.Name != "ask" && tool.Name != "ask_question" && tool.Name != "ask_user_question") || tool.Output != nil || tool.Input == nil {
		return TranscriptAsk{}, false
	}
	agy := tool.Name == "ask_question"
	piDialog := tool.Name == "ask_user_question"
	var payload struct {
		Questions []struct {
			Header      string            `json:"header"`
			Question    string            `json:"question"`
			Multi       bool              `json:"multi"`
			Multiple    bool              `json:"multiSelect"`
			AgyMulti    bool              `json:"is_multi_select"`
			Recommended *int              `json:"recommended"`
			Options     []json.RawMessage `json:"options"`
		} `json:"questions"`
	}
	if json.Unmarshal([]byte(*tool.Input), &payload) != nil || len(payload.Questions) == 0 {
		return TranscriptAsk{}, false
	}
	ask := TranscriptAsk{ToolID: tool.ID, ParksWorking: piDialog}
	if ask.ToolID == "" {
		ask.ToolID = *tool.Input
	}
	for _, question := range payload.Questions {
		multi := question.Multi || question.Multiple || question.AgyMulti
		if strings.TrimSpace(question.Question) == "" || (multi && question.Recommended != nil) {
			return TranscriptAsk{}, false
		}
		item := AskQuestion{Multi: multi, Header: strings.TrimSpace(question.Header), Prompt: strings.TrimSpace(question.Question)}
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
			return TranscriptAsk{}, false
		}
		if multi && piDialog {
			// Rows: the options, "Type something." (an inline input), then
			// the commit row; none of the last two is offered here.
			item.CommitRow = len(item.Options) + 1
		}
		if !multi {
			if r := question.Recommended; !agy && !piDialog && r != nil && *r > 0 && *r < len(item.Options) {
				item.Start = *r
			}
			other := AskOtherOption
			if agy {
				other = AgyWriteInOption
			}
			if piDialog {
				other = PiTypeSomethingOption
			}
			item.Options = append(item.Options, other)
			item.Descriptions = append(item.Descriptions, "")
		}
		ask.Questions = append(ask.Questions, item)
	}
	return ask, true
}
