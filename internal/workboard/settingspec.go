package workboard

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

/**
 * [INPUT]: 依赖 settings.go 的 Setting，插件 CLI `models --json`（[{agent,models|null,efforts{levels,by{model:[…]}}}]，1.2.26 实测）
 * [OUTPUT]: 对外提供 Spec/SpecFor（设置名 → 控件类型与取值范围）、Spec.Fits（值与规格不符即降级）、Amount 解析/格式化、Client.Models、ModelsView.Efforts
 * [POS]: 设置页「选择而非填写」的规格表（用户 2026-10-26 决定）：插件 JSON 不带选项表，所以此处钉住 1.2.x 的 settings_list.rs 子集（名字→类型/选项/下限）；规格表之外的名字、以及当前值与规格不符的设置一律降级为文本输入，插件新增/改名/改类型都不会让页面坏掉，校验与拒绝原文仍全归插件；agent/model/effort 的候选不入表，运行时来自 `models --json`。settingspec_test.go 用 testdata 里钉住版本的 show 快照做漂移检查
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// Ty is the control a setting gets. TyText is the zero value: every unknown
// setting is a text field.
type Ty int

const (
	TyText   Ty = iota
	TyChoice    // a few words: Values
	TyCount     // a whole number, Min or more
	TyAmount    // a number with a unit (or none): Units, None
	TyAgent     // program[/model], from `models`
	TyEffort    // an effort level of the sibling agent setting, or default
)

// Spec is what Shardlane pins about one setting of plugin 1.2.x.
type Spec struct {
	Ty     Ty
	Values []string // TyChoice
	Min    int      // TyCount
	Units  []string // TyAmount: time s/m/h, tokens k/m
	None   bool     // TyAmount, TyAgent: "none" is a value
}

var (
	timeUnits   = []string{"s", "m", "h"}
	tokenUnits  = []string{"k", "m"}
	yesNo       = []string{"no", "yes"}
	yesNoYes    = []string{"yes", "no"}
	accessWords = []string{"full", "ask", "auto"}
)

func choice(v ...string) Spec { return Spec{Ty: TyChoice, Values: v} }
func count(min int) Spec      { return Spec{Ty: TyCount, Min: min} }
func timeOf(none bool) Spec   { return Spec{Ty: TyAmount, Units: timeUnits, None: none} }

// specs is the pinned table. Names the plugin has but this table lacks stay
// text: Agents/List/Branch/From/Pilot/Keys/… are free-form there too.
var specs = map[string]Spec{
	"worker.agent":       {Ty: TyAgent},
	"reviewer.agent":     {Ty: TyAgent},
	"coordinator.agent":  {Ty: TyAgent, None: true},
	"watcher.agent":      {Ty: TyAgent, None: true},
	"plan.agent":         {Ty: TyAgent, None: true},
	"worker.effort":      {Ty: TyEffort},
	"reviewer.effort":    {Ty: TyEffort},
	"coordinator.effort": {Ty: TyEffort},
	"worker.access":      choice(accessWords...),
	"reviewer.access":    choice(accessWords...),
	"coordinator.access": choice(accessWords...),

	"autopilot.scope":         choice("own", "all"),
	"autopilot.approve":       choice(yesNo...),
	"autopilot.switchOthers":  choice(yesNo...),
	"autopilot.releases":      choice(yesNo...),
	"autopilot.startAtOnce":   choice(yesNoYes...),
	"autopilot.repoFileWaits": choice(yesNoYes...),
	"autopilot.every":         timeOf(true),
	"autopilot.gap":           timeOf(false),
	"autopilot.wakesPerDay":   count(1),
	"autopilot.tasksPerDay":   count(0),
	"autopilot.tasksAtOnce":   count(1),
	"autopilot.retries":       count(0),
	"autopilot.needsYouAfter": count(1),

	"watcher.every": timeOf(false),

	"attempts.pick": choice("you", "reviewer"),
	"plan.approve":  choice("none", "you", "reviewer"),

	"check.timeout":    timeOf(false),
	"review.when":      choice("always", "repos", "never"),
	"review.mustFix":   choice("critical", "major", "minor", "trivial"),
	"review.rounds":    count(0),
	"review.timeout":   timeOf(true),
	"review.afterChat": timeOf(false),

	"limit.time":   timeOf(true),
	"limit.tokens": {Ty: TyAmount, Units: tokenUnits, None: true},

	"merge.by":          choice("you", "itself"),
	"merge.method":      choice("squash", "merge", "rebase"),
	"merge.pushTimeout": timeOf(true),
	"merge.checks":      choice("github", "none"),
	"merge.sync":        choice("always", "approve"),

	"board.agents":    count(1),
	"board.diskFree":  count(0),
	"board.oldBuilds": count(0),
	"board.open":      choice("tab", "popup"),
}

// SpecFor is the pinned spec of a setting; the zero Spec (text) for the rest.
func SpecFor(name string) Spec { return specs[name] }

// SpecNames lists the names the table pins (the drift test checks them
// against a real `show`).
func SpecNames() []string {
	names := make([]string, 0, len(specs))
	for n := range specs {
		names = append(names, n)
	}
	return names
}

// Fits reports whether the setting's current value is something the spec's
// control can show. A value that does not fit means the plugin changed under
// the table: the caller falls back to text.
func (sp Spec) Fits(value any) bool {
	switch sp.Ty {
	case TyChoice:
		s, ok := value.(string)
		if !ok {
			return false
		}
		for _, v := range sp.Values {
			if v == s {
				return true
			}
		}
		return false
	case TyCount:
		f, ok := value.(float64)
		return ok && f == float64(int64(f)) && int(f) >= sp.Min
	case TyAmount:
		s, ok := value.(string)
		if !ok {
			return false
		}
		_, ok = ParseAmount(s, sp.Units, sp.None)
		return ok
	case TyAgent, TyEffort:
		_, ok := value.(string)
		return ok
	}
	return true
}

// Amount is a number with one of a spec's units, or none.
type Amount struct {
	N    int
	Unit string
	None bool
}

// ParseAmount reads "15m", "500k" or "none"; anything else (compound times,
// decimals, a missing unit) is not representable and reports false.
func ParseAmount(text string, units []string, none bool) (Amount, bool) {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "none" {
		return Amount{None: true}, none
	}
	for _, u := range units {
		if !strings.HasSuffix(text, u) {
			continue
		}
		digits := strings.TrimSuffix(text, u)
		n, err := strconv.Atoi(digits)
		if err != nil || n < 1 || strconv.Itoa(n) != digits {
			return Amount{}, false
		}
		return Amount{N: n, Unit: u}, true
	}
	return Amount{}, false
}

// String is the amount as the plugin takes it back.
func (a Amount) String() string {
	if a.None {
		return "none"
	}
	return strconv.Itoa(a.N) + a.Unit
}

// AgentModels is one program of `models --json`.
type AgentModels struct {
	Agent   string
	Models  []string // empty: the program takes no model choice
	Efforts struct {
		Levels []string
		By     map[string][]string
	}
}

// ModelsView is `models --json`.
type ModelsView []AgentModels

// Programs lists the program names in the plugin's order.
func (v ModelsView) Programs() []string {
	names := make([]string, 0, len(v))
	for _, a := range v {
		names = append(names, a.Agent)
	}
	return names
}

// Program finds one program.
func (v ModelsView) Program(name string) (AgentModels, bool) {
	for _, a := range v {
		if a.Agent == name {
			return a, true
		}
	}
	return AgentModels{}, false
}

// SplitAgent splits "codex/gpt-6-sol" into program and model ("" without one).
func SplitAgent(value string) (program, model string) {
	program, model, _ = strings.Cut(strings.TrimSpace(value), "/")
	return program, model
}

// JoinAgent is the inverse; an empty model is the program alone.
func JoinAgent(program, model string) string {
	if model == "" {
		return program
	}
	return program + "/" + model
}

// Efforts are the levels an agent value accepts: its model's own list when
// the plugin gives one, else the program's. Nil when the program is unknown.
func (v ModelsView) Efforts(agent string) []string {
	program, model := SplitAgent(agent)
	a, ok := v.Program(program)
	if !ok {
		return nil
	}
	if levels, ok := a.Efforts.By[model]; ok && model != "" {
		return levels
	}
	return a.Efforts.Levels
}

// Models reads the programs, their models and effort levels.
func (c *Client) Models(ctx context.Context) (ModelsView, error) {
	out, err := c.exec(ctx, "models", "--json")
	if err != nil {
		return nil, err
	}
	var wire []struct {
		Agent   string   `json:"agent"`
		Models  []string `json:"models"`
		Efforts struct {
			Levels []string            `json:"levels"`
			By     map[string][]string `json:"by"`
		} `json:"efforts"`
	}
	if err := json.Unmarshal([]byte(out), &wire); err != nil {
		return nil, fmt.Errorf("workboard: models: %w", err)
	}
	view := make(ModelsView, 0, len(wire))
	for _, w := range wire {
		if w.Agent == "" {
			continue
		}
		a := AgentModels{Agent: w.Agent, Models: w.Models}
		a.Efforts.Levels, a.Efforts.By = w.Efforts.Levels, w.Efforts.By
		view = append(view, a)
	}
	return view, nil
}
