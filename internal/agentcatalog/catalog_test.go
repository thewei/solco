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
		{history.AgentClaudeCode, "/effort high", KindTerminal},
		{history.AgentClaudeCode, "/compact", ""},
		{history.AgentCodex, "/diff", KindTerminal},
		{history.AgentCodex, "/permissions", KindPicker},
		{history.AgentPi, "/model", KindTerminal},
		{history.AgentPi, "/model group/glm-flash", ""}, // pi's typed switch stays in Chat
		{history.AgentOMP, "/switch group/glm-flash", ""},
		{history.AgentOMP, "/git", KindTerminal},
		{history.AgentOMP, "just text", ""},
	}
	for _, tc := range cases {
		if got := Classify(tc.provider, tc.text); got != tc.want {
			t.Errorf("Classify(%s, %q) = %q, want %q", tc.provider, tc.text, got, tc.want)
		}
	}
}

func TestBuiltinsNeverEndTheAgent(t *testing.T) {
	for _, provider := range []history.AgentID{history.AgentClaudeCode, history.AgentCodex, history.AgentPi, history.AgentOMP} {
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
