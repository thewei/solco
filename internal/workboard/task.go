package workboard

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

/**
 * [INPUT]: 依赖 herdr-projects 1.2.x 的 `list --json` / `details <id> --json` 输出（插件自己的 task.json 视图，无类型 JSON）
 * [OUTPUT]: 对外提供 Task/Detail 解析（ParseTasks/ParseDetail）与保守分组推导 GroupOf（needs you / ready to approve / working / to do / done）
 * [POS]: work board 的纯数据层：不起进程、不碰 UI；插件不在 --json 里暴露自己的 group 与 mergeFailed/syncBlocked/prFailure，所以这里只在「全部条件明确满足」时才给出 ready to approve（含插件自己的 "Approve to merge?" 就绪说明——PR 失败时插件会改写它），任何存疑一律归 needs you
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// Group is where a task sits on the work board, in attention-priority order.
type Group int

const (
	// GroupNeedsYou is the safe default: blocked, failed, or anything we cannot
	// prove is fine.
	GroupNeedsYou Group = iota
	// GroupReady is finished work waiting for the human's Approve.
	GroupReady
	GroupWorking
	GroupTodo
	GroupDone
)

// Label is the group's section title.
func (g Group) Label() string {
	switch g {
	case GroupNeedsYou:
		return "Needs you"
	case GroupReady:
		return "Ready to approve"
	case GroupWorking:
		return "Working"
	case GroupTodo:
		return "To do"
	default:
		return "Done"
	}
}

// Task is one row of `list --json`. Only the fields the client reads are kept;
// the plugin owns the rest.
type Task struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	State string `json:"state"`
	Kind  string `json:"kind"`
	Stage string `json:"stage"`
	// Msg is the plugin's status note ("note" in its store), the sentence its
	// own board shows on the row.
	Msg string `json:"msg"`
	// PR is the pull request URL, empty when there is none.
	PR string `json:"pr"`
	// App is the agent program on the task ("claude", "codex", …).
	App string `json:"app"`
	// Queue is the place in the queue for a free slot (1 starts next), 0 when
	// the task is not waiting for one.
	Queue int `json:"queue"`
	// ReviewRound is the review the task is on, or the last it had (R#n).
	ReviewRound int `json:"reviewRound"`
	// Research and Chat mark the two kinds of task that change nothing in the repo.
	Research bool `json:"research"`
	Chat     bool `json:"chat"`
}

// PRNumber is the pull request's number ("18" of .../pull/18), empty when the
// row has none.
func (t Task) PRNumber() string {
	if t.PR == "" {
		return ""
	}
	return t.PR[strings.LastIndex(t.PR, "/")+1:]
}

// Tags are the short marks a row wears: agent, review round, pull request,
// queue place — the plugin's own R#n / PR#n / Q#n.
func (t Task) Tags() []string {
	var tags []string
	if t.App != "" {
		tags = append(tags, t.App)
	}
	if t.ReviewRound > 0 {
		tags = append(tags, "R#"+strconv.Itoa(t.ReviewRound))
	}
	if n := t.PRNumber(); n != "" {
		tags = append(tags, "PR#"+n)
	}
	if t.Queue > 0 {
		tags = append(tags, "Q#"+strconv.Itoa(t.Queue))
	}
	return tags
}

// PlanReady reports a task whose plan is written and waits for the human's
// yes (stage "plan", blocked: the plugin's state.rs). Its verbs are approve
// (yes, build it) and tell (send the plan back with words).
func (t Task) PlanReady() bool { return t.Stage == "plan" && t.State == "blocked" }

// NeedsPick reports several attempts that passed and wait for the human to
// keep one (stage "pick"). Which attempts exist is not in the list rows.
func (t Task) NeedsPick() bool { return t.Stage == "pick" }

// Detail is the slice of `details <id> --json` the client reads.
type Detail struct {
	// Worktree is the task's checkout; empty until it is delegated.
	Worktree string
	Branch   string
	// PR is the pull request URL, empty when the task has none.
	PR string
	// Failure reports a recorded failure object.
	Failure bool
	// ReviewFailed reports a reviewer verdict that is not a pass.
	ReviewFailed bool

	// The fields below are display-only (the task page); grouping never reads
	// them. All stay empty when the plugin's shape is not the one we know.
	Description   string
	Agent         string
	FailureTitle  string
	FailureDetail string
	FailureNext   string
	ReviewBy      string
	ReviewText    string
	// ReviewFindings counts the reviewer's findings.
	ReviewFindings int

	// Read surface of the task page (`details`: plan, reviews, report,
	// limits, handovers, attempts, queue). Shapes follow the plugin's
	// finish_line::details; each stays empty when the plugin sends another.
	Plan         string
	Findings     []Finding // the current review's
	Reviews      []Review  // every round, oldest first
	ReportLink   string    // the report's link, else its file's path
	LimitTime    string    // "1h12m of 2h" / "limit 2h"
	LimitTokens  string    // "40k of 200k" / "limit 200k"
	LimitStopped string    // the words of the stop when a limit was reached
	Handovers    []string  // "codex took over from claude: …"
	Attempts     []Attempt
	QueueWords   string
}

// Finding is one thing a reviewer found, with the plugin's severity word
// (critical, major, minor, trivial).
type Finding struct {
	Severity    string `json:"severity"`
	Subject     string `json:"subject"`
	Where       string `json:"where"`
	Proof       string `json:"proof"`
	Preexisting bool   `json:"preexisting"`
}

// Review is one review round.
type Review struct {
	Failed   bool
	By       string
	Text     string
	Findings []Finding
}

// Attempt is one of several parallel tries at a task (stage "pick" compares them).
type Attempt struct {
	Letter   string `json:"letter"`
	Agent    string `json:"agent"`
	Standing string `json:"standing"` // passed | works | waits | needs | out
	Stat     string `json:"stat"`
}

// ParseTasks reads `list --json`.
func ParseTasks(raw string) ([]Task, error) {
	var tasks []Task
	if err := json.Unmarshal([]byte(raw), &tasks); err != nil {
		return nil, fmt.Errorf("workboard: list: %w", err)
	}
	live := tasks[:0]
	for _, t := range tasks {
		if t.ID != "" {
			live = append(live, t)
		}
	}
	return live, nil
}

type detailWire struct {
	Task     json.RawMessage `json:"task"`
	Agent    json.RawMessage `json:"agent"`
	Failure  json.RawMessage `json:"failure"`
	Review   json.RawMessage `json:"review"`
	PR       json.RawMessage `json:"pr"`
	Worktree json.RawMessage `json:"worktree"`
	Branch   json.RawMessage `json:"branch"`

	Plan      json.RawMessage   `json:"plan"`
	Reviews   []json.RawMessage `json:"reviews"`
	Report    json.RawMessage   `json:"report"`
	Limits    json.RawMessage   `json:"limits"`
	Handovers []struct {
		Words string `json:"words"`
	} `json:"handovers"`
	Attempts []Attempt `json:"attempts"`
	Queue    struct {
		Words string `json:"words"`
	} `json:"queue"`
}

// ParseDetail reads `details <id> --json`. A field of an unexpected shape reads
// as "present but unknown": Failure/ReviewFailed turn true, so the caller's
// fail-safe grouping applies.
func ParseDetail(raw string) (Detail, error) {
	var wire detailWire
	if err := json.Unmarshal([]byte(raw), &wire); err != nil {
		return Detail{}, fmt.Errorf("workboard: details: %w", err)
	}
	// Display facts: an object of another shape just leaves them empty.
	var task struct{ Description string }
	_ = json.Unmarshal(wire.Task, &task)
	var failure struct{ Title, Detail, Next string }
	_ = json.Unmarshal(wire.Failure, &failure)
	review := readReview(wire.Review)
	return Detail{
		Worktree:     text(wire.Worktree),
		Branch:       text(wire.Branch),
		PR:           text(wire.PR),
		Failure:      present(wire.Failure),
		ReviewFailed: reviewFailed(wire.Review),

		Description:    task.Description,
		Agent:          text(wire.Agent),
		FailureTitle:   failure.Title,
		FailureDetail:  failure.Detail,
		FailureNext:    failure.Next,
		ReviewBy:       review.By,
		ReviewText:     review.Text,
		ReviewFindings: len(review.Findings),

		Plan:         planText(wire.Plan),
		Findings:     review.Findings,
		Reviews:      readReviews(wire.Reviews),
		ReportLink:   reportLink(wire.Report),
		LimitTime:    limitTime(wire.Limits),
		LimitTokens:  limitTokens(wire.Limits),
		LimitStopped: limitStopped(wire.Limits),
		Handovers:    handoverWords(wire),
		Attempts:     wire.Attempts,
		QueueWords:   wire.Queue.Words,
	}, nil
}

func readReview(raw json.RawMessage) Review {
	var r struct {
		By       string
		Text     string
		Findings []Finding
	}
	if json.Unmarshal(raw, &r) != nil {
		// Findings of an unknown shape still count by length.
		var loose struct {
			By       string
			Text     string
			Findings []json.RawMessage
		}
		_ = json.Unmarshal(raw, &loose)
		r.By, r.Text = loose.By, loose.Text
		if len(loose.Findings) > 0 {
			r.Findings = make([]Finding, len(loose.Findings))
		}
	}
	return Review{Failed: !isNull(raw) && reviewFailed(raw), By: r.By, Text: r.Text, Findings: r.Findings}
}

func readReviews(raws []json.RawMessage) []Review {
	var out []Review
	for _, raw := range raws {
		if !isNull(raw) {
			out = append(out, readReview(raw))
		}
	}
	return out
}

func planText(raw json.RawMessage) string {
	var p struct{ Text string }
	_ = json.Unmarshal(raw, &p)
	return p.Text
}

// reportLink prefers the served link; a report no page server serves is a
// file's path (docs/research.md).
func reportLink(raw json.RawMessage) string {
	var r struct{ Link, File string }
	_ = json.Unmarshal(raw, &r)
	if r.Link != "" {
		return r.Link
	}
	return r.File
}

type limitsWire struct {
	Time struct {
		Limit string `json:"limit"`
		Used  string `json:"used"`
	} `json:"time"`
	Tokens struct {
		Limit string   `json:"limit"`
		Used  *float64 `json:"used"`
	} `json:"tokens"`
	Stopped struct {
		Line string `json:"line"`
	} `json:"stopped"`
}

func readLimits(raw json.RawMessage) limitsWire {
	var l limitsWire
	_ = json.Unmarshal(raw, &l)
	return l
}

// limitTime reads "1h12m of 2h" (while an agent runs) or "limit 2h"; "none" is no limit.
func limitTime(raw json.RawMessage) string {
	l := readLimits(raw).Time
	switch {
	case l.Limit == "" || l.Limit == "none":
		return ""
	case l.Used != "":
		return l.Used + " of " + l.Limit
	}
	return "limit " + l.Limit
}

func limitTokens(raw json.RawMessage) string {
	l := readLimits(raw).Tokens
	switch {
	case l.Limit == "" || l.Limit == "none":
		return ""
	case l.Used != nil && *l.Used > 0:
		return tokensSay(*l.Used) + " of " + l.Limit
	}
	return "limit " + l.Limit
}

func limitStopped(raw json.RawMessage) string { return readLimits(raw).Stopped.Line }

// tokensSay is the plugin's own wording of a token count (values.rs tokens_say).
func tokensSay(n float64) string {
	cut := func(v float64) string {
		prec := 0
		switch {
		case v < 10:
			prec = 2
		case v < 100:
			prec = 1
		}
		s := strconv.FormatFloat(v, 'f', prec, 64)
		if strings.Contains(s, ".") {
			s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
		}
		return s
	}
	n = math.Round(math.Max(n, 0))
	switch {
	case n >= 1e6:
		return cut(n/1e6) + "m"
	case n >= 1e3:
		return cut(n/1e3) + "k"
	}
	return strconv.FormatFloat(n, 'f', 0, 64)
}

func handoverWords(w detailWire) []string {
	var out []string
	for _, h := range w.Handovers {
		if h.Words != "" {
			out = append(out, h.Words)
		}
	}
	return out
}

func isNull(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	return s == "" || s == "null"
}

func present(raw json.RawMessage) bool { return !isNull(raw) }

func text(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return s
}

// reviewFailed mirrors the plugin's own rule: a tagged verdict fails when
// `pass` is false, an older one when `recommend` is false. An absent review is
// not a failure; a review of a shape we do not know is.
func reviewFailed(raw json.RawMessage) bool {
	if isNull(raw) {
		return false
	}
	var verdict struct {
		Pass      *bool `json:"pass"`
		Recommend *bool `json:"recommend"`
	}
	if json.Unmarshal(raw, &verdict) != nil {
		return true
	}
	switch {
	case verdict.Pass != nil:
		return !*verdict.Pass
	case verdict.Recommend != nil:
		return !*verdict.Recommend
	default:
		return true
	}
}

// PRNumber is the pull request's number ("18" of .../pull/18), empty when the
// task has none.
func (d Detail) PRNumber() string {
	if d.PR == "" {
		return ""
	}
	return d.PR[strings.LastIndex(d.PR, "/")+1:]
}

// readyNote opens the status note the plugin writes when finished work waits
// for the human's merge ("Approve to merge? …", finish/shared.rs); the plugin
// parses the same prefix itself. When its pull request later fails, the plugin
// replaces the note ("Local checks passed; pull request still blocked: …",
// "<title>: … Send details to the agent …"), and that replacement is how a
// failure the CLI does not otherwise expose (`prFailure`) shows here.
const readyNote = "Approve to merge?"

// ReadyCandidate reports whether the list row alone allows "ready to approve":
// finished work waiting on the human, asking for permission, still wearing the
// plugin's own ready note. Whether it really is ready needs its details.
func (t Task) ReadyCandidate() bool {
	return t.Stage == "approval" && t.State == "blocked" && t.Kind == "permission" &&
		strings.HasPrefix(strings.TrimSpace(t.Msg), readyNote)
}

// NeedsDetail reports whether GroupOf can only decide with the task's details.
func (t Task) NeedsDetail() bool { return t.ReadyCandidate() }

// Project is the repo a task belongs to: the plugin names a task "<repo>/<n>".
// "" when the id has no repo part.
func (t Task) Project() string {
	if i := strings.LastIndex(t.ID, "/"); i > 0 {
		return t.ID[:i]
	}
	return ""
}

// GroupOf derives the board group. The plugin's own grouping is not in
// `--json`, so the direction of every doubt is "needs you": ready to approve
// requires a ready candidate (including the plugin's own ready note, which a
// failing pull request overwrites), details that were read, no recorded
// failure and no failed review. Approve itself never passes `--anyway`, so the
// plugin's own checks stay the last backstop.
func GroupOf(t Task, d *Detail) Group {
	switch {
	case t.Stage == "done" || t.State == "done":
		return GroupDone
	case t.ReadyCandidate():
		if d == nil || d.Failure || d.ReviewFailed {
			return GroupNeedsYou
		}
		return GroupReady
	case t.State == "blocked" || t.State == "error":
		return GroupNeedsYou
	case t.State == "exited" && t.Stage != "todo":
		// An agent that ended mid-flight is doubt, not "to do".
		return GroupNeedsYou
	case t.State == "working":
		return GroupWorking
	default:
		return GroupTodo
	}
}
