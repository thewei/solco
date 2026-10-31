package agentcatalog

import (
	"os"
	"path/filepath"
	"testing"

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
