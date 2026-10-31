package chatsession

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/thewei/solco/internal/history"
)

func claudeUser(text string) string {
	return fmt.Sprintf(`{"type":"user","cwd":"/w","timestamp":"2026-08-01T01:00:00Z","message":{"content":%q}}`+"\n", text)
}

func TestReaderSyncsOnlyWhenTheFileChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(path, []byte(claudeUser("one")), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := Open(history.AgentClaudeCode, path)
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := r.Sync(); err != nil || !changed {
		t.Fatalf("first sync = %v, %v", changed, err)
	}
	if changed, _ := r.Sync(); changed {
		t.Fatal("an idle file must not report a change")
	}
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	_, _ = f.WriteString(claudeUser("two"))
	f.Close()
	if changed, _ := r.Sync(); !changed {
		t.Fatal("an appended row must report a change")
	}
	st := r.State(time.Now())
	if st.Provider != string(history.AgentClaudeCode) || len(st.Turns) != 2 || !st.Turns[1].User {
		t.Fatalf("state = %+v", st)
	}
}

func TestOpenFailsClosed(t *testing.T) {
	if _, err := Open(history.AgentClaudeCode, ""); err == nil {
		t.Fatal("an empty path must be refused")
	}
	if _, err := Open(history.AgentID("nope"), "/x"); err == nil {
		t.Fatal("an unknown provider must be refused")
	}
}

func TestFoldOpensTheNewestPageAndKeepsItsStart(t *testing.T) {
	var rows []history.TranscriptMessage
	for i := 0; i < history.WindowLimit*2+5; i++ {
		rows = append(rows, history.TranscriptMessage{Role: "user", Text: fmt.Sprint("m", i)})
	}
	w := Fold(rows, nil, -1)
	if w.Start != len(rows)-history.WindowLimit || !w.HasOlder || len(w.Turns) != history.WindowLimit {
		t.Fatalf("newest page: start=%d older=%v turns=%d", w.Start, w.HasOlder, len(w.Turns))
	}
	rows = append(rows, history.TranscriptMessage{Role: "user", Text: "new"})
	if w2 := Fold(rows, nil, w.Start); w2.Start != w.Start || len(w2.Turns) != history.WindowLimit+1 {
		t.Fatalf("a new row must extend the window: start=%d turns=%d", w2.Start, len(w2.Turns))
	}
}
