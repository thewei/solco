package workboard

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// The snapshot is a real `show --json` of the pinned plugin (1.2.26). When the
// pin moves, re-capture it: the checks below then say which table rows drifted.
func pinnedShow(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "show_everywhere_1.2.26.json"))
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Settings []Setting `json:"settings"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	values := map[string]any{}
	for _, s := range wire.Settings {
		values[s.Name] = s.Value
	}
	return values
}

func TestSpecTableMatchesThePinnedPlugin(t *testing.T) {
	values := pinnedShow(t)
	for _, name := range SpecNames() {
		value, ok := values[name]
		if !ok {
			t.Errorf("%s: pinned in the table but absent from the plugin's show (renamed or removed?)", name)
			continue
		}
		if !SpecFor(name).Fits(value) {
			t.Errorf("%s: the plugin's built-in value %v (%T) does not fit its spec %+v", name, value, value, SpecFor(name))
		}
	}
}

func TestUnknownSettingsFallBackToText(t *testing.T) {
	for _, name := range []string{"worker.fallback", "attempts.by", "autopilot.when", "board.theme", "somethingNew.setting"} {
		if SpecFor(name).Ty != TyText {
			t.Errorf("%s must stay a text field", name)
		}
	}
}

func TestFitsRefusesValuesTheControlCannotShow(t *testing.T) {
	cases := []struct {
		spec  Spec
		value any
		want  bool
	}{
		{SpecFor("merge.method"), "squash", true},
		{SpecFor("merge.method"), "fast-forward", false}, // a new word the table does not know
		{SpecFor("merge.method"), true, false},
		{SpecFor("review.rounds"), float64(3), true},
		{SpecFor("review.rounds"), "3", false}, // type changed under the table
		{SpecFor("review.rounds"), float64(1.5), false},
		{SpecFor("check.timeout"), "15m", true},
		{SpecFor("check.timeout"), "1h30m", false}, // compound time: text
		{SpecFor("check.timeout"), "none", false},  // this one has no none
		{SpecFor("limit.time"), "none", true},
		{SpecFor("limit.tokens"), "500k", true},
		{SpecFor("limit.tokens"), "1.5m", false},
	}
	for _, c := range cases {
		if got := c.spec.Fits(c.value); got != c.want {
			t.Errorf("Fits(%v) with %+v = %v, want %v", c.value, c.spec, got, c.want)
		}
	}
}

func TestAmountRoundTrip(t *testing.T) {
	for _, text := range []string{"15m", "60s", "6h", "500k", "2m", "none"} {
		units := timeUnits
		if text == "500k" || text == "2m" {
			units = tokenUnits
		}
		a, ok := ParseAmount(text, units, true)
		if !ok || a.String() != text {
			t.Errorf("%q → %+v ok=%v → %q", text, a, ok, a.String())
		}
	}
	if _, ok := ParseAmount("none", timeUnits, false); ok {
		t.Error("none is refused where the setting has none of it")
	}
	if _, ok := ParseAmount("0m", timeUnits, true); ok {
		t.Error("a zero amount is not a value the control writes")
	}
}

const modelsJSON = `[
 {"agent":"claude","how":"x","models":["default","opus"],"signin":"","efforts":{"levels":["low","high"],"by":{}}},
 {"agent":"codex","how":"x","models":["gpt-6-sol"],"signin":"","efforts":{"levels":["low","ultra","none"],"by":{"gpt-6-sol":["low","ultra"]}}},
 {"agent":"agy","how":"x","models":null,"signin":"","efforts":{"levels":[],"by":{}}}
]`

func TestModelsAndEffortsComeFromThePlugin(t *testing.T) {
	c := NewClientWithExec(func(_ context.Context, args ...string) (string, error) {
		if !slices.Equal(args, []string{"models", "--json"}) {
			t.Fatalf("args = %v", args)
		}
		return modelsJSON, nil
	})
	v, err := c.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(v.Programs(), []string{"claude", "codex", "agy"}) {
		t.Fatalf("programs = %v", v.Programs())
	}
	if a, _ := v.Program("agy"); len(a.Models) != 0 {
		t.Fatalf("a program with null models takes no model: %v", a.Models)
	}
	for agent, want := range map[string][]string{
		"claude":          {"low", "high"},
		"codex":           {"low", "ultra", "none"}, // no model: the program's levels
		"codex/gpt-6-sol": {"low", "ultra"},         // the model's own list
		"codex/other":     {"low", "ultra", "none"},
		"nobody":          nil,
	} {
		if got := v.Efforts(agent); !slices.Equal(got, want) {
			t.Errorf("Efforts(%q) = %v, want %v", agent, got, want)
		}
	}
	if p, m := SplitAgent("codex/gpt-6-sol"); p != "codex" || m != "gpt-6-sol" || JoinAgent(p, m) != "codex/gpt-6-sol" || JoinAgent("claude", "") != "claude" {
		t.Fatal("split/join")
	}
}
