package history

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// benchCodexTree builds a Codex sessions tree: days × perDay ordinary
// rollouts whose first line carries a ~30 KiB instruction block (as real
// session_meta rows do), the parent in the oldest day and `kids` children.
func benchCodexTree(b *testing.B, days, perDay, kids int) (parent string) {
	b.Helper()
	root := filepath.Join(b.TempDir(), "sessions")
	pad := strings.Repeat("x", 30<<10)
	now := time.Now()
	for d := days - 1; d >= 0; d-- {
		day := now.AddDate(0, 0, -d)
		dir := filepath.Join(root, day.Format("2006/01/02"))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			b.Fatal(err)
		}
		ts := day.Add(-time.Minute).UTC().Format(time.RFC3339Nano)
		for i := 0; i < perDay; i++ {
			body := fmt.Sprintf(`{"type":"session_meta","payload":{"id":"S%d-%d","timestamp":%q,"source":"cli","base_instructions":%q}}`+"\n", d, i, ts, pad)
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("rollout-%04d.jsonl", i)), []byte(body), 0o644); err != nil {
				b.Fatal(err)
			}
		}
		if d == days-1 {
			parent = filepath.Join(dir, "rollout-parent.jsonl")
			meta := fmt.Sprintf(`{"type":"session_meta","payload":{"id":"P","timestamp":%q,"source":"cli"}}`+"\n", ts)
			if err := os.WriteFile(parent, []byte(meta), 0o644); err != nil {
				b.Fatal(err)
			}
			for k := 0; k < kids; k++ {
				child := fmt.Sprintf(`{"type":"session_meta","payload":{"id":"C%d","timestamp":%q,"source":{"subagent":{"thread_spawn":{"parent_thread_id":"P","depth":1,"agent_path":"/root/k%d"}}}}}`+"\n", k, ts, k)
				if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("rollout-child%d.jsonl", k)), []byte(child), 0o644); err != nil {
					b.Fatal(err)
				}
			}
		}
	}
	return parent
}

// BenchmarkCodexSubagentsCold is the first scan after launch (every first
// line read); BenchmarkCodexSubagentsWarm is every later refresh (stat only).
func BenchmarkCodexSubagentsCold(b *testing.B) {
	parent := benchCodexTree(b, 3, 400, 3)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		codexClassMu.Lock()
		codexClassCache = map[string]codexClass{}
		codexClassMu.Unlock()
		if got := codexSubagents(parent); len(got) != 3 {
			b.Fatalf("children = %d", len(got))
		}
	}
}

func BenchmarkCodexSubagentsWarm(b *testing.B) {
	parent := benchCodexTree(b, 3, 400, 3)
	codexSubagents(parent)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if got := codexSubagents(parent); len(got) != 3 {
			b.Fatalf("children = %d", len(got))
		}
	}
}
