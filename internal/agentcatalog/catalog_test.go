package agentcatalog

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/thewei/solco/internal/history"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSkillsFirstDirWinsAndFolds(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a", "think", "SKILL.md"), "---\nname: think\ndescription: >\n  Plan first,\n  then build.\n---\nbody")
	write(t, filepath.Join(root, "b", "think", "SKILL.md"), "---\nname: think\ndescription: shadowed\n---\n")
	write(t, filepath.Join(root, "b", "bare", "SKILL.md"), "no frontmatter")
	write(t, filepath.Join(root, "b", ".hidden", "SKILL.md"), "---\nname: hidden\n---\n")
	got := skills("$", filepath.Join(root, "a"), filepath.Join(root, "b"))
	if len(got) != 2 || got[0].Name != "$bare" || got[1].Name != "$think" {
		t.Fatalf("got %+v", got)
	}
	if got[1].Detail != "Plan first, then build." || got[1].Insert != "$think " {
		t.Fatalf("think = %+v", got[1])
	}
}

func TestPromptsNestNamesWithColon(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "ship.md"), "---\ndescription: \"Ship it\"\n---\n")
	write(t, filepath.Join(dir, "git", "sync.md"), "do it")
	got := prompts(dir, "/")
	if len(got) != 2 || got[0].Name != "/git:sync" || got[1].Name != "/ship" || got[1].Detail != "Ship it" {
		t.Fatalf("got %+v", got)
	}
}

func TestParseModelListings(t *testing.T) {
	pi := parsePiModels("provider model context\nantigravity claude-sonnet-4-6 1M\ndeepseek deepseek-flash 1M\n")
	if len(pi) != 2 || pi[1].ID != "deepseek/deepseek-flash" {
		t.Fatalf("pi %+v", pi)
	}
	omp := parseOmpModels("google-antigravity (2)\n┌──┬──┐\n│ model │ context │\n├──┼──┤\n│ gemini-3.8-flash │ 1M │\n│ claude-x │ 1M │\n└──┴──┘\nmagpie (1)\n│ a/b │ 1M │\n")
	if len(omp) != 3 || omp[0].ID != "google-antigravity/gemini-3.8-flash" || omp[2].ID != "magpie/a/b" {
		t.Fatalf("omp %+v", omp)
	}
}

func TestModelListingsCarryTheContextWindow(t *testing.T) {
	pi := parsePiModels("provider model context max-out\nantigravity claude-sonnet-4-6 1M 65.5K\nx y 131K 33K\nz w - -\n")
	if pi[0].Window != 1_000_000 || pi[1].Window != 131_000 || pi[2].Window != 0 {
		t.Fatalf("pi windows %+v", pi)
	}
	omp := parseOmpModels("g (2)\n│ model │ context │ max-out │\n│ a │ 250K │ 64K │\n│ b │ 1M │ 66K │\n")
	if omp[0].Window != 250_000 || omp[1].Window != 1_000_000 {
		t.Fatalf("omp windows %+v", omp)
	}
	if parseTokenCount("65.5K") != 65_500 || parseTokenCount("junk") != 0 || parseTokenCount("") != 0 {
		t.Fatal("token count spellings")
	}
}

func TestModelWindowMatchesFullAndUniqueBareIds(t *testing.T) {
	modelMu.Lock()
	modelCache["omp"] = modelEntry{at: time.Now(), models: []Model{
		{ID: "g/claude-opus", Window: 250_000}, {ID: "g/gemini", Window: 1_000_000}, {ID: "h/gemini", Window: 2_000_000},
		{ID: "magpie/group/glm-flash", Window: 1_000_000},
	}}
	modelMu.Unlock()
	ctx := context.Background()
	if got := ModelWindow(ctx, history.AgentOMP, "g/gemini"); got != 1_000_000 {
		t.Fatalf("full id = %d", got)
	}
	if got := ModelWindow(ctx, history.AgentOMP, "claude-opus"); got != 250_000 {
		t.Fatalf("unique bare id = %d", got)
	}
	if got := ModelWindow(ctx, history.AgentOMP, "group/glm-flash"); got != 1_000_000 {
		t.Fatalf("a slash-bearing id without its provider = %d", got)
	}
	if got := ModelWindow(ctx, history.AgentOMP, "gemini"); got != 0 {
		t.Fatalf("ambiguous bare id must not guess, got %d", got)
	}
	if ModelWindow(ctx, history.AgentCodex, "g/gemini") != 0 || ModelWindow(ctx, history.AgentOMP, "") != 0 {
		t.Fatal("only pi/omp list windows, and an empty id has none")
	}
}

func TestClassifyHandsTerminalCommandsOver(t *testing.T) {
	cases := []struct {
		provider history.AgentID
		text     string
		want     Kind
	}{
		{history.AgentClaudeCode, "/model", KindPicker},
		{history.AgentClaudeCode, "/model haiku", ""}, // typed switch saves a default: never structured
		{history.AgentClaudeCode, "/status", KindTerminal},
		{history.AgentClaudeCode, "/effort", KindTerminal}, // bare: the agent's own dialog
		{history.AgentClaudeCode, "/effort high", ""},      // an applied level is sent as typed
		{history.AgentClaudeCode, "/effort ultracode on", KindTerminal},
		{history.AgentOMP, "/effort low", ""},
		{history.AgentPi, "/thinking max", ""},
		{history.AgentClaudeCode, "/compact", ""},
		{history.AgentCodex, "/diff", KindTerminal},
		{history.AgentCodex, "/permissions", KindPicker},
		{history.AgentPi, "/model", KindTerminal},
		{history.AgentPi, "/model group/glm-flash", ""}, // pi's typed switch stays in Chat
		{history.AgentOMP, "/switch group/glm-flash", ""},
		{history.AgentOMP, "/git", KindTerminal},
		{history.AgentOMP, "just text", ""},
		{history.AgentAntigravity, "/model", KindTerminal},
		{history.AgentAntigravity, "/model Claude Sonnet 4.6 (Thinking)", ""}, // typed switch stays in Chat
		{history.AgentAntigravity, "/permissions", KindTerminal},
		{history.AgentCursor, "/model", KindTerminal},
		{history.AgentCursor, "/model gemini 3.8", KindTerminal}, // first match may be a dearer model: Terminal
		{history.AgentCursor, "/plan make a plan", KindTerminal},
		{history.AgentCursor, "/chrome-devtools", ""}, // a skill is an ordinary prompt
	}
	for _, tc := range cases {
		if got := Classify(tc.provider, tc.text); got != tc.want {
			t.Errorf("Classify(%s, %q) = %q, want %q", tc.provider, tc.text, got, tc.want)
		}
	}
}

func TestBuiltinsNeverEndTheAgent(t *testing.T) {
	for _, provider := range []history.AgentID{history.AgentClaudeCode, history.AgentCodex, history.AgentPi, history.AgentOMP, history.AgentCursor, history.AgentAntigravity} {
		seen := map[string]bool{}
		for _, entry := range builtins(provider) {
			switch entry.Name {
			case "/exit", "/quit", "/logout", "/delete":
				t.Errorf("%s offers %s, which ends the agent", provider, entry.Name)
			}
			if seen[entry.Name] {
				t.Errorf("%s lists %s twice", provider, entry.Name)
			}
			seen[entry.Name] = true
		}
	}
}

func TestDefaultModelFromTheAgentsOwnConfig(t *testing.T) {
	omp := "theme:\n  dark: titanium\nmodelRoles:\n  plan: openai/gpt-5.6-luna:high\n  default: magpie/group/glm-flash:high\ndev:\n  default: nope\n"
	if got := ompDefaultModel(omp); got != "group/glm-flash" {
		t.Errorf("omp default = %q, want group/glm-flash", got)
	}
	if got := ompDefaultModel("modelRoles:\n  default: openai/gpt-5.6-luna\n"); got != "gpt-5.6-luna" {
		t.Errorf("omp default without level = %q", got)
	}
	if got := ompDefaultModel("dev:\n  default: x\n"); got != "" {
		t.Errorf("a default outside modelRoles must not count: %q", got)
	}
	if got := codexDefaultModel("model_reasoning_effort = \"max\"\nmodel = \"group/glm-flash\"\n[profiles.x]\nmodel = \"other\"\n"); got != "group/glm-flash" {
		t.Errorf("codex default = %q", got)
	}
	if got := codexDefaultModel("[profiles.x]\nmodel = \"other\"\n"); got != "" {
		t.Errorf("a table's model is not the default: %q", got)
	}
	if got := claudeDefaultModel(`{"model":"haiku","other":1}`); got != "haiku" {
		t.Errorf("claude default = %q", got)
	}
	if got := claudeDefaultModel("not json"); got != "" {
		t.Errorf("bad json = %q", got)
	}
}

func TestIsCompactTakesTheCommandAndItsInstructionsOnly(t *testing.T) {
	for text, want := range map[string]bool{"/compact": true, " /compact keep the tests ": true, "/compaction": false, "compact it": false, "/init": false} {
		if IsCompact(text) != want {
			t.Fatalf("IsCompact(%q) = %v", text, !want)
		}
	}
}

func TestAgyModelsDropMaxModeAndKeepTheDisplayName(t *testing.T) {
	out := "Fetching available models...\ngemini-3.8-flash-high\tGemini 3.8 Flash (High)\nclaude-opus-4-6-thinking\tClaude Opus 4.6 (Thinking)\nclaude-x-max\tClaude X Max\nclaude-x-thinking-max-fast\tClaude X Max Thinking Fast\n"
	got := parseAgyModels(out)
	if len(got) != 2 || got[0].ID != "Gemini 3.8 Flash (High)" || got[0].Detail != "gemini-3.8-flash-high" || got[1].ID != "Claude Opus 4.6 (Thinking)" {
		t.Fatalf("got %+v", got)
	}
}

func TestIsMaxModeMatchesTheMaxTokenOnly(t *testing.T) {
	for id, want := range map[string]bool{
		"claude-opus-5-5-max": true, "claude-opus-5-5-max-fast": true, "claude-4.6-opus-max-thinking": true,
		"Claude Opus 5 1M Max Thinking": true, "kimi-k3-max": true,
		"gemini-3.8-flash-high": false, "maxwell-1": false, "claude-opus-5-5-xhigh": false, "gpt-5.6-sol-high": false,
	} {
		if IsMaxMode(id) != want {
			t.Errorf("IsMaxMode(%q) = %v", id, !want)
		}
	}
}

func TestCursorAndAgyCatalogs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	write(t, filepath.Join(home, ".cursor", "skills-cursor", "create-rule", "SKILL.md"), "---\nname: create-rule\ndescription: Make a rule\n---\n")
	write(t, filepath.Join(home, ".claude", "skills", "agent-browser", "SKILL.md"), "---\nname: agent-browser\n---\n")
	write(t, filepath.Join(home, ".agents", "skills", "magpie", "SKILL.md"), "---\nname: magpie\n---\n")
	write(t, filepath.Join(home, ".cursor", "cli-config.json"), `{"model":{"modelId":"composer-2.5"}}`)
	write(t, filepath.Join(home, ".gemini", "antigravity-cli", "settings.json"), `{"model":"Gemini 3.8 Flash (High)"}`)

	cursor := Load(context.Background(), history.AgentCursor, "")
	names := map[string]bool{}
	for _, entry := range cursor.Skills {
		names[entry.Name] = true
	}
	if !names["/create-rule"] || !names["/agent-browser"] || !names["/magpie"] {
		t.Fatalf("cursor skills %v", names)
	}
	if cursor.DefaultModel != "composer-2.5" || cursor.ModelVerb != "" || len(cursor.Models) != 0 || cursor.ModelPicker != "" {
		t.Fatalf("cursor offers no model switch: %+v", cursor)
	}
	for _, entry := range cursor.Commands {
		switch entry.Name {
		case "/max-mode", "/fast", "/run-everything", "/auto-review":
			t.Errorf("cursor offers %s", entry.Name)
		}
	}

	modelMu.Lock()
	modelCache["agy"] = modelEntry{at: time.Now(), models: []Model{{ID: "Gemini 3.8 Flash (High)", Detail: "gemini-3.8-flash-high"}}}
	modelMu.Unlock()
	agy := Load(context.Background(), history.AgentAntigravity, "")
	if agy.DefaultModel != "Gemini 3.8 Flash (High)" || agy.ModelVerb != "/model " || len(agy.Models) != 1 || agy.ModelNote == "" {
		t.Fatalf("agy %+v", agy)
	}
	if names := agy.Skills; len(names) != 1 || names[0].Name != "/agent-browser" {
		t.Fatalf("agy skills (own dirs and ~/.claude, not ~/.agents): %+v", names)
	}
}

func TestAgyDefaultModelFallbacks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Fallback to ~/.gemini/settings.json when antigravity-cli/settings.json is absent
	write(t, filepath.Join(home, ".gemini", "settings.json"), `{"model":"Gemini 3.8 Flash (Medium)"}`)
	if got := defaultModel(history.AgentAntigravity, home); got != "Gemini 3.8 Flash (Medium)" {
		t.Fatalf("got %q, want Gemini 3.8 Flash (Medium)", got)
	}

	// Fallback to first model in agy models when no settings.json specifies a model
	home2 := t.TempDir()
	t.Setenv("HOME", home2)
	modelMu.Lock()
	modelCache["agy"] = modelEntry{at: time.Now(), models: []Model{
		{ID: "Gemini 3.8 Flash (High)", Detail: "gemini-3.8-flash-high"},
		{ID: "Gemini 3.8 Flash (Medium)", Detail: "gemini-3.8-flash-medium"},
	}}
	modelMu.Unlock()
	agy := Load(context.Background(), history.AgentAntigravity, "")
	if agy.DefaultModel != "Gemini 3.8 Flash (High)" {
		t.Fatalf("default model fallback = %q, want Gemini 3.8 Flash (High)", agy.DefaultModel)
	}
}

func TestPiEffortsFollowPisOwnModelsJSON(t *testing.T) {
	home := t.TempDir()
	// Shapes captured from a real ~/.pi/agent/models.json: a map with nulls for
	// unsupported levels, and a reasoning model with no map at all.
	write(t, filepath.Join(home, ".pi", "agent", "models.json"), `{"providers":{"magpie":{"models":[
		{"id":"group/glm-flash","reasoning":true,"thinkingLevelMap":{"high":"high","low":"low","max":"max","medium":null,"minimal":null,"off":null,"xhigh":null}},
		{"id":"opencode-zen-free/mimo-v2.6-flash-free","reasoning":true}]}}}`)
	models := parsePiModels("provider model context max-out thinking images\nmagpie group/glm-flash 1M 131K yes no\nmagpie opencode-zen-free/mimo-v2.6-flash-free 200K 8K yes no\nmagpie plain/no-think 8K 1K no no\n")
	got := withPiEfforts(models, home)
	labels := func(m Model) string {
		out := ""
		for _, e := range m.Efforts {
			out += e.Label + ","
		}
		return out
	}
	if labels(got[0]) != "low,high,max," || got[0].Efforts[0].Send != "/thinking low" {
		t.Fatalf("mapped model: %+v", got[0].Efforts)
	}
	if labels(got[1]) != "off,minimal,low,medium,high," {
		t.Fatalf("unmapped reasoning model: %+v", got[1].Efforts)
	}
	if len(got[2].Efforts) != 0 || len(models[0].Efforts) != 0 {
		t.Fatalf("a non-thinking model has no efforts and the cached rows stay untouched: %+v %+v", got[2], models[0])
	}
}

func TestOmpEffortsComeFromTheThinkingColumn(t *testing.T) {
	out := "magpie (2)\n┌──┬──┬──┬──┬──┐\n│ model │ context │ max-out │ thinking │ images │\n├──┼──┼──┼──┼──┤\n" +
		"│ group/glm-flash │ 1M │ 131K │ low,high,max │ yes │\n│ group/ourfreemodel │ 1M │ 131K │ - │ yes │\n└──┴──┴──┴──┴──┘\n"
	models := parseOmpModels(out)
	if len(models) != 2 || len(models[1].Efforts) != 0 {
		t.Fatalf("models %+v", models)
	}
	want := []string{"off", "auto", "low", "high", "max"}
	if len(models[0].Efforts) != len(want) {
		t.Fatalf("efforts %+v", models[0].Efforts)
	}
	for i, level := range want {
		if models[0].Efforts[i] != (Effort{Label: level, Send: "/effort " + level}) {
			t.Fatalf("effort %d = %+v", i, models[0].Efforts[i])
		}
	}
}

func TestAgyEffortVariantsFoldIntoOneFamily(t *testing.T) {
	// `agy models` as captured on agy 1.4.12.
	out := "Fetching available models...\ngemini-3.8-flash-high\tGemini 3.8 Flash (High)\ngemini-3.8-flash-medium\tGemini 3.8 Flash (Medium)\ngemini-3.8-flash-low\tGemini 3.8 Flash (Low)\n" +
		"claude-sonnet-4-6\tClaude Sonnet 4.6 (Thinking)\nclaude-opus-4-6-thinking\tClaude Opus 4.6 (Thinking)\ngpt-oss-120b-medium\tGPT-OSS 120B (Medium)\n"
	got := groupAgyEfforts(parseAgyModels(out))
	if len(got) != 4 {
		t.Fatalf("rows %+v", got)
	}
	flash := got[0]
	if flash.ID != "Gemini 3.8 Flash" || flash.Pick != "/model Gemini 3.8 Flash (High)" || len(flash.Efforts) != 3 ||
		flash.Efforts[2] != (Effort{Label: "Low", Send: "/model Gemini 3.8 Flash (Low)"}) {
		t.Fatalf("family %+v", flash)
	}
	if got[1].ID != "Claude Sonnet 4.6 (Thinking)" || len(got[1].Efforts) != 0 || got[3].ID != "GPT-OSS 120B (Medium)" || got[3].Pick != "" {
		t.Fatalf("a Thinking name or a lone variant stays a plain row: %+v %+v", got[1], got[3])
	}
}

func TestClaudeEffortsAreFixedAndNoted(t *testing.T) {
	cat := Load(context.Background(), history.AgentClaudeCode, t.TempDir())
	if len(cat.Efforts) != 6 || cat.Efforts[0] != (Effort{Label: "low", Send: "/effort low"}) || cat.EffortNote == "" {
		t.Fatalf("efforts %+v note %q", cat.Efforts, cat.EffortNote)
	}
	for _, provider := range []history.AgentID{history.AgentCodex, history.AgentCursor} {
		if got := Load(context.Background(), provider, t.TempDir()); len(got.Efforts) != 0 {
			t.Fatalf("%s must not offer efforts: %+v", provider, got.Efforts)
		}
	}
}
