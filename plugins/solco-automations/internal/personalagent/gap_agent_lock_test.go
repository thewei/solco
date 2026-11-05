package personalagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Gap R07 (docs/assistant-shepherdr-3.1.3-alignment-roadmap.md): Coordinator
// and every Worker are OMP. A model wake that the Heartbeat files for itself
// must not drift to another provider.
func TestGapR07HeartbeatWakeTaskIsOMP(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	now := time.Unix(1_800_000_000, 0)
	h := setBeat(t, s, a, HeartbeatInput{Cron: "*/10 * * * *", Checklist: "- look around", WakeBudget: 2})
	b := beater(s, &now)
	_, _ = b.Tick()
	approveWake(t, s, h)
	now = now.Add(11 * time.Minute)
	if r, _ := b.Tick(); r.Woke != 1 {
		t.Fatalf("wake did not start: %+v", r)
	}
	tasks := taskTitles(t, s, a, "Heartbeat")
	if len(tasks) != 1 {
		t.Fatalf("heartbeat tasks = %d", len(tasks))
	}
	if tasks[0].Agent != AssistantAgentKind {
		t.Fatalf("heartbeat Worker agent = %q, want %q", tasks[0].Agent, AssistantAgentKind)
	}
}

// Gap R07: a static guard so no new code path can quietly default a Worker to
// another provider. Provider names may appear only where Solco reads the
// user's own provider config (MCP registry, effort flags).
func TestGapR07NoHiddenProviderDefaults(t *testing.T) {
	allowed := map[string]bool{"mcp.go": true, "persona.go": true}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || allowed[f] {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(raw), "\n") {
			trim := strings.TrimSpace(line)
			if strings.HasPrefix(trim, "//") || strings.Contains(line, "`json:") {
				continue
			}
			for _, p := range []string{`"claude"`, `"codex"`, `"cursor"`, `"pi"`} {
				if strings.Contains(line, p) {
					t.Errorf("%s:%d names provider %s outside the OMP rollout allow-list", f, i+1, p)
				}
			}
		}
	}
}
