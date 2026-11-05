package personalagent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const reportProbeMarker = "0123456789abcdef0123456789abcdef"

func testOMPSource(t *testing.T, home, name, sessionID, content string) string {
	t.Helper()
	root := filepath.Join(home, ".omp", "agent", "sessions", "2026", "11", "05")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, name+".jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"session","id":"`+sessionID+`"}`+"\n"+content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func testOMPUser(marker string) string {
	return `{"type":"message","id":"u1","message":{"role":"user","content":[{"type":"text","text":"Solco-Report-Id: ` + marker + `\nStatus please"}]}}` + "\n"
}

const testOMPDone = `{"type":"message","id":"final-123","message":{"role":"assistant","stopReason":"stop","content":[{"type":"text","text":"Verified progress."}]}}` + "\n"

func TestOMPReportReplyRequiresExactProviderCompletedTurn(t *testing.T) {
	for _, tc := range []struct {
		name, content string
		state         string
	}{
		{"real final response", testOMPUser(reportProbeMarker) + testOMPDone, ReplyVerified},
		{"partial text stream", testOMPUser(reportProbeMarker) + `{"type":"message","id":"partial","message":{"role":"assistant","content":[{"type":"text","text":"draft"}]}}` + "\n", ""},
		{"toolUse not completed", testOMPUser(reportProbeMarker) + `{"type":"message","id":"thinking","message":{"role":"assistant","stopReason":"toolUse","content":[{"type":"toolCall","name":"read"}]}}` + "\n", ""},
		{"tool turn then final", testOMPUser(reportProbeMarker) + `{"type":"message","id":"tool","message":{"role":"assistant","stopReason":"toolUse","content":[]}}` + "\n" + testOMPDone, ReplyVerified},
		{"assistant aborted", testOMPUser(reportProbeMarker) + `{"type":"message","id":"aborted","message":{"role":"assistant","stopReason":"aborted","content":[]}}` + "\n", ReplyInterrupted},
		{"assistant error", testOMPUser(reportProbeMarker) + `{"type":"message","id":"err","message":{"role":"assistant","stopReason":"error","content":[]}}` + "\n", ReplyInterrupted},
		{"empty assistant final", testOMPUser(reportProbeMarker) + `{"type":"message","id":"empty","message":{"role":"assistant","stopReason":"stop","content":[]}}` + "\n", ""},
		{"new user turn intervenes", testOMPUser(reportProbeMarker) + testOMPUser(strings.Repeat("a", 32)) + testOMPDone, ""},
		{"same marker repeated after a completed turn", testOMPUser(reportProbeMarker) + testOMPDone + testOMPUser(reportProbeMarker) + testOMPDone, ""},
		{"unrelated later user turn after completed reply", testOMPUser(reportProbeMarker) + testOMPDone + testOMPUser(strings.Repeat("a", 32)) + testOMPDone, ReplyVerified},
		{"unrelated assistant final", testOMPUser(strings.Repeat("a", 32)) + testOMPDone, ""},
		{"marker only in assistant", `{"type":"message","message":{"role":"assistant","stopReason":"stop","id":"f","content":[{"type":"text","text":"Solco-Report-Id: ` + reportProbeMarker + `"}]}}` + "\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			testOMPSource(t, home, "report", "session-owner", tc.content)
			ev, err := FindOMPReportReply(home, reportProbeMarker)
			if err != nil || ev.State != tc.state {
				t.Fatalf("evidence = %+v error=%v, want state=%q", ev, err, tc.state)
			}
			if tc.state == ReplyVerified && (ev.SessionID != "session-owner" || ev.MessageID != "final-123") {
				t.Fatalf("wrong provider evidence: %+v", ev)
			}
		})
	}
}
func TestOMPReportReplyRejectsDuplicateSessionMarker(t *testing.T) {
	home := t.TempDir()
	testOMPSource(t, home, "one", "session-1", testOMPUser(reportProbeMarker)+testOMPDone)
	testOMPSource(t, home, "two", "session-2", testOMPUser(reportProbeMarker)+testOMPDone)
	ev, err := FindOMPReportReply(home, reportProbeMarker)
	if err != nil || ev.State != "" {
		t.Fatalf("ambiguous OMP sessions must not verify: %+v %v", ev, err)
	}
}
func TestOMPReportReplyIgnoresSubagentSessionsAndSymlinks(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".omp", "agent", "sessions")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	src := testOMPSource(t, home, "subagent", "child", testOMPUser(reportProbeMarker)+testOMPDone)
	// A child session cannot impersonate the coordinator's user turn.
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), `"id":"child"`, `"id":"child","parentSession":"/other.jsonl"`, 1))
	if err := os.WriteFile(src, data, 0600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.jsonl")
	if err := os.WriteFile(outside, []byte(`{"type":"session","id":"outside"}`+"\n"+testOMPUser(reportProbeMarker)+testOMPDone), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "shortcut.jsonl")); err != nil {
		t.Fatal(err)
	}
	ev, err := FindOMPReportReply(home, reportProbeMarker)
	if err != nil || ev.State != "" {
		t.Fatalf("child/symlink source accepted: %+v %v", ev, err)
	}
}
func TestOMPReportReplyWorksWithLongRealSessionTail(t *testing.T) {
	home := t.TempDir()
	filler := `{"type":"custom","value":"` + strings.Repeat("x", 4000) + `"}` + "\n"
	path := testOMPSource(t, home, "long", "session-long", strings.Repeat(filler, 2100)+testOMPUser(reportProbeMarker)+testOMPDone)
	info, err := os.Stat(path)
	if err != nil || info.Size() < maxReplyFileTail {
		t.Fatalf("invalid oversized test input: %v %v", info, err)
	}
	ev, err := FindOMPReportReply(home, reportProbeMarker)
	if err != nil || ev.State != ReplyVerified || ev.MessageID != "final-123" {
		t.Fatalf("long tail: %+v %v", ev, err)
	}
}
func TestRealOMPReportScannerFixture(t *testing.T) {
	home := os.Getenv("SOLCO_OMP_REPORT_E2E_HOME")
	marker := os.Getenv("SOLCO_OMP_REPORT_E2E_MARKER")
	if home == "" || marker == "" {
		t.Skip("explicit isolated OMP acceptance only")
	}
	ev, err := FindOMPReportReply(home, marker)
	if err != nil || ev.State != ReplyVerified || ev.SessionID == "" || ev.MessageID == "" {
		t.Fatalf("real isolated OMP response was not provider-verified: %+v, %v", ev, err)
	}
}

// OMP 18.16.0 real TUI can write a title row before its session row. The
// parser must find the actual provider-owned session header in a bounded
// prefix rather than assuming it is always record 0.
func TestOMPReportReplyHandlesTitleBeforeSessionHeader(t *testing.T) {
	home := t.TempDir()
	path := testOMPSource(t, home, "titled", "session-title", testOMPUser(reportProbeMarker)+testOMPDone)
	existing, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	prepend := []byte(`{"type":"title","title":"Sandbox progress"}` + "\n")
	if err := os.WriteFile(path, append(prepend, existing...), 0600); err != nil {
		t.Fatal(err)
	}
	ev, err := FindOMPReportReply(home, reportProbeMarker)
	if err != nil || ev.State != ReplyVerified || ev.SessionID != "session-title" || ev.MessageID != "final-123" {
		t.Fatalf("real OMP title-before-session format not verified: %+v %v", ev, err)
	}
}

// A user with many historical OMP sessions should not spend the report
// verification budget on files last modified before the report was sent.
// If more current candidates exist than the bounded scan can inspect,
// the answer is unknown, NEVER an apparently unique verification.
func TestOMPReportReplyRecentScopeAndIncompleteCandidateSet(t *testing.T) {
	home := t.TempDir()
	accepted := time.Now()
	testOMPSource(t, home, "current", "session-current", testOMPUser(reportProbeMarker)+testOMPDone)
	old := accepted.Add(-2 * time.Hour)
	for i := 0; i < maxReplyFiles+5; i++ {
		path := testOMPSource(t, home, fmt.Sprintf("old-%03d", i), fmt.Sprintf("old-%03d", i), testOMPUser(strings.Repeat("a", 32))+testOMPDone)
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	ev, err := FindOMPReportReplySince(home, reportProbeMarker, accepted.UnixMilli())
	if err != nil || ev.State != ReplyVerified || ev.SessionID != "session-current" {
		t.Fatalf("recent report blocked by unrelated historical files: %+v %v", ev, err)
	}
	for i := 0; i < maxReplyFiles; i++ {
		testOMPSource(t, home, fmt.Sprintf("recent-%03d", i), fmt.Sprintf("new-%03d", i), testOMPUser(strings.Repeat("a", 32))+testOMPDone)
	}
	ev, err = FindOMPReportReplySince(home, reportProbeMarker, accepted.UnixMilli())
	if err != nil || ev.State != "" {
		t.Fatalf("truncated scan claimed uniquely verified reply: %+v %v", ev, err)
	}
}
