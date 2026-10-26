package nativeui

/**
 * [INPUT]: 依赖 internal/workboard/settingspec.go（SpecFor/Fits/Amount/ModelsView）、workboard_settings.go 的 setWorkboardSetting 与页面状态、官方 ui.Segmented/Select/Combobox/NumberInput/Switch/TextInput
 * [OUTPUT]: Shell.workboardSettingControl（一行设置的取值控件）、Shell.loadWorkboardModels
 * [POS]: 设置页「选择而非填写」：钉住的规格表命中且当前值合规时画选择型控件（≤3 个词用分段、更多用下拉；数量用步进；时间/token 用数字+单位分段；agent=程序下拉+模型可搜下拉；effort 取兄弟 agent 的档位），否则降级文本输入——插件新增/改名/改类型不会坏页面，校验与拒绝原文仍全归插件。选择即写；数字输入失焦或回车才写（输入过程不连发 CLI）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"log/slog"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/workboard"
)

// workboardDefaultModel is the model dropdown's entry for "no model after the
// slash": the program's own default.
const workboardDefaultModel = "Program default"

// workboardSettingControl draws the value control of one setting; a choice
// writes it in the open scope.
func (s *Shell) workboardSettingControl(key *ui.Context, setting workboard.Setting) {
	s.workboardSettingControlWith(key, setting, s.workboard.settings.view.Settings,
		func(value string) { s.setWorkboardSetting(setting.Name, value, false) })
}

// workboardSettingControlWith is workboardSettingControl for any home of the
// value: set takes the choice, and siblings are the settings an effort level
// reads its program from (the page's, or a dialog's with its own choices).
func (s *Shell) workboardSettingControlWith(key *ui.Context, setting workboard.Setting, siblings []workboard.Setting, set func(string)) {
	spec := workboard.SpecFor(setting.Name)
	switch {
	case setting.IsBool():
		on, _ := setting.Value.(bool)
		if value, changed := settingSwitch(key, setting.Name, on); changed {
			set(boolWord(value))
		}
		return
	case !spec.Fits(setting.Value):
	case spec.Ty == workboard.TyChoice:
		s.workboardChoice(key, setting, spec.Values, set)
		return
	case spec.Ty == workboard.TyCount:
		s.workboardCount(key, setting, spec, set)
		return
	case spec.Ty == workboard.TyAmount:
		s.workboardAmount(key, setting, spec, set)
		return
	case spec.Ty == workboard.TyAgent && s.workboardAgent(key, setting, spec, set):
		return
	case spec.Ty == workboard.TyEffort && s.workboardEffort(key, setting, siblings, set):
		return
	}
	s.workboardText(key, setting, set)
}

// workboardChoice is a few words: a segmented control up to three, a
// drop-down beyond.
func (s *Shell) workboardChoice(key *ui.Context, setting workboard.Setting, values []string, set func(string)) {
	current, _ := setting.Value.(string)
	if len(values) <= 3 {
		idx := indexOf(values, current)
		if ui.Segmented(key, &idx, values...).Label(setting.Name).Changed() {
			set(values[idx])
		}
		return
	}
	value := current
	if ui.Select(key, &value, values).Label(setting.Name).Changed() && value != current {
		set(value)
	}
}

// workboardCount is a whole number with a floor.
func (s *Shell) workboardCount(key *ui.Context, setting workboard.Setting, spec workboard.Spec, set func(string)) {
	stored, _ := setting.Value.(float64)
	draft := s.workboardNumberDraft(setting.Name)
	field := ui.NumberInput(key, draft, float64(spec.Min), 1e6, 1).Width(120).Label(setting.Name)
	s.settleNumber(setting.Name, field, draft, stored, func() { set(strconv.Itoa(int(*draft))) })
}

// workboardAmount is a number with a unit, or none.
func (s *Shell) workboardAmount(key *ui.Context, setting workboard.Setting, spec workboard.Spec, set func(string)) {
	text, _ := setting.Value.(string)
	amount, _ := workboard.ParseAmount(text, spec.Units, spec.None)
	labels := append([]string{}, spec.Units...)
	if spec.None {
		labels = append(labels, "none")
	}
	idx := len(spec.Units)
	if !amount.None {
		idx = indexOf(spec.Units, amount.Unit)
	}
	draft := s.workboardNumberDraft(setting.Name)
	if amount.None && *draft < 1 {
		*draft = 1
	}
	ui.Row(key).Gap(Spacing().S).AlignItems(ui.Center).Children(func() {
		if !amount.None {
			field := ui.NumberInput(key, draft, 1, 1e6, 1).Width(110).Label(setting.Name)
			s.settleNumber(setting.Name, field, draft, float64(amount.N), func() {
				set(workboard.Amount{N: int(*draft), Unit: amount.Unit}.String())
			})
		}
		picked := idx
		if ui.Segmented(key, &picked, labels...).Label(setting.Name+" unit").Changed() && picked != idx {
			if picked == len(spec.Units) {
				set(workboard.Amount{None: true}.String())
				return
			}
			n := int(*draft)
			if n < 1 {
				n = 1
			}
			set(workboard.Amount{N: n, Unit: spec.Units[picked]}.String())
		}
	})
}

// workboardAgent is program[/model]: a program drop-down and, when the
// program lists models, a searchable model drop-down. It reports false when
// the plugin's lists are not there (or do not know the value): text instead.
func (s *Shell) workboardAgent(key *ui.Context, setting workboard.Setting, spec workboard.Spec, set func(string)) bool {
	models := s.workboard.settings.models
	text, _ := setting.Value.(string)
	program, model := workboard.SplitAgent(text)
	noneNow := spec.None && strings.EqualFold(text, "none")
	var info workboard.AgentModels
	if !noneNow {
		var ok bool
		if info, ok = models.Program(program); !ok {
			return false
		}
		if len(info.Models) == 0 && model != "" {
			return false // a model the plugin gives no list for: leave it to text
		}
	}
	programs := models.Programs()
	if spec.None {
		programs = append([]string{"none"}, programs...)
	}
	ui.Row(key).Gap(Spacing().S).AlignItems(ui.Center).Children(func() {
		chosen := program
		if noneNow {
			chosen = "none"
		}
		if ui.Select(key.Key("program"), &chosen, programs).Label(setting.Name+" program").Changed() && chosen != program {
			set(chosen) // a program's models are its own: the model resets
		}
		if noneNow || len(info.Models) == 0 {
			return
		}
		options := append([]string{workboardDefaultModel}, info.Models...)
		if model != "" && indexOf(info.Models, model) < 0 {
			options = append(options, model)
		}
		shown := model
		if shown == "" {
			shown = workboardDefaultModel
		}
		picked := shown
		if ui.Combobox(key.Key("model"), &picked, options).Width(220).Label(setting.Name+" model").Changed() && picked != shown {
			if picked == workboardDefaultModel {
				picked = ""
			}
			set(workboard.JoinAgent(program, picked))
		}
	})
	return true
}

// workboardEffort is an effort level of the sibling agent setting's program
// and model, or default; text when the plugin lists none for it.
func (s *Shell) workboardEffort(key *ui.Context, setting workboard.Setting, siblings []workboard.Setting, set func(string)) bool {
	st := &s.workboard.settings
	role, _, _ := strings.Cut(setting.Name, ".")
	var agent string
	for _, other := range siblings {
		if other.Name == role+".agent" {
			agent, _ = other.Value.(string)
		}
	}
	levels := st.models.Efforts(agent)
	if len(levels) == 0 {
		return false
	}
	current, _ := setting.Value.(string)
	options := []string{"default"}
	for _, l := range levels {
		if l != "default" && indexOf(options, l) < 0 {
			options = append(options, l)
		}
	}
	if indexOf(options, current) < 0 {
		options = append(options, current)
	}
	value := current
	if ui.Select(key, &value, options).Width(160).Label(setting.Name).Changed() && value != current {
		set(value)
	}
	return true
}

// workboardText is the fallback: a text field committed with Return.
func (s *Shell) workboardText(key *ui.Context, setting workboard.Setting, set func(string)) {
	st := &s.workboard.settings
	if st.drafts == nil {
		st.drafts = map[string]*string{}
	}
	draft := st.drafts[setting.Name]
	if draft == nil {
		draft = new(string)
		*draft = setting.ValueText()
		st.drafts[setting.Name] = draft
	}
	field := ui.TextInput(key, draft).Width(220).Label(setting.Name)
	if field.Submitted() {
		text := strings.TrimSpace(*draft)
		if text != "" && text != setting.ValueText() {
			set(text)
		}
	}
	if !field.Focused() {
		*draft = setting.ValueText()
	}
}

func (s *Shell) workboardNumberDraft(name string) *float64 {
	st := &s.workboard.settings
	if st.numbers == nil {
		st.numbers = map[string]*float64{}
	}
	if st.numbers[name] == nil {
		st.numbers[name] = new(float64)
	}
	return st.numbers[name]
}

// settleNumber writes a number once it is settled: stepper clicks apply at
// once, typing waits for Return or for the field to lose focus, so a number
// typed digit by digit is one write. Idle, the field mirrors the stored value.
func (s *Shell) settleNumber(name string, field ui.Element, draft *float64, stored float64, commit func()) {
	st := &s.workboard.settings
	if st.pending == nil {
		st.pending = map[string]bool{}
	}
	if field.Changed() {
		st.pending[name] = true
	}
	switch {
	case st.pending[name] && (!field.Focused() || field.Submitted()):
		delete(st.pending, name)
		if *draft != stored {
			commit()
		}
	case !st.pending[name] && !st.writing[name]:
		*draft = stored
	}
}

// loadWorkboardModels reads the plugin's programs/models/efforts once; a
// failed read leaves them empty, so those rows stay text fields.
func (s *Shell) loadWorkboardModels() {
	st := &s.workboard.settings
	if st.modelsLoading || !s.workboardAvailable() {
		return
	}
	st.modelsLoading = true
	client := newWorkboardClient(s.workboard.avail.Root, herdr.ParseRef(s.activeInstance).Session)
	workboardGo(func() {
		ctx, cancel := context.WithTimeout(context.Background(), workboardRefreshTimeout)
		defer cancel()
		models, err := client.Models(ctx)
		if err != nil {
			slog.Warn("work board models read failed", "error", err)
		}
		s.applyOnWindow(func() {
			st := &s.workboard.settings
			st.modelsLoading = false
			st.models = models
		})
	})
}

func indexOf(list []string, want string) int {
	for i, v := range list {
		if v == want {
			return i
		}
	}
	return -1
}
