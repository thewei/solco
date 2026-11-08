package history

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func writeLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func dayMap(days []DayTokens) map[string]int64 {
	out := map[string]int64{}
	for _, d := range days {
		out[d.Day.Format(dayLayout)] = d.Tokens
	}
	return out
}

func TestDailyUsageClaudeDedupesAndBucketsByLocalDay(t *testing.T) {
	dir := t.TempDir()
	tz := time.FixedZone("UTC+8", 8*3600)
	now := time.Now().In(tz)
	today := now.Format(dayLayout)
	yesterday := now.AddDate(0, 0, -1).Format(dayLayout)
	// 17:30 UTC is 01:30 next day in UTC+8.
	stamp := func(day string, hh string) string { return day + "T" + hh + ":00:00.000Z" }
	y17 := stamp(now.AddDate(0, 0, -2).Format(dayLayout), "17") // local: yesterday 01:xx
	line := func(id, ts string, in, out int) string {
		return `{"type":"assistant","timestamp":"` + ts + `","message":{"id":"` + id + `","usage":{"input_tokens":` + itoa(in) + `,"output_tokens":` + itoa(out) + `}}}`
	}
	writeLines(t, filepath.Join(dir, "p", "a.jsonl"),
		line("m1", y17, 10, 5),
		line("m1", y17, 10, 5), // same response, logged per content block
		line("m2", stamp(yesterday, "03"), 100, 50),
		`{"type":"user","message":{"content":"hi"}}`,
		line("m3", now.UTC().Format("2006-01-02T15:04:05.000Z"), 1, 1),
	)
	r := NewDailyUsageReader([]SourceRoot{{Agent: AgentClaudeCode, Directory: dir, NativeID: func(s string) string { return s }}})
	got, err := r.Read(context.Background(), now.AddDate(0, 0, -3), tz)
	if err != nil {
		t.Fatal(err)
	}
	days := dayMap(got[AgentClaudeCode])
	if len(got[AgentClaudeCode]) != 4 {
		t.Fatalf("series length = %d, want 4 (three days back through today)", len(got[AgentClaudeCode]))
	}
	if days[yesterday] != 15+150 {
		t.Errorf("yesterday = %d, want 165 (m1 once, 01:xx local, plus m2)", days[yesterday])
	}
	if days[today] != 2 {
		t.Errorf("today = %d, want 2", days[today])
	}
	if SumDays(got[AgentClaudeCode]) != 167 || Busiest(got[AgentClaudeCode]) != 165 {
		t.Errorf("sum = %d busiest = %d", SumDays(got[AgentClaudeCode]), Busiest(got[AgentClaudeCode]))
	}
}

func TestDailyUsageCodexCountsGrowthOnly(t *testing.T) {
	dir := t.TempDir()
	tz := time.UTC
	now := time.Now().In(tz)
	day := now.Format(dayLayout)
	ev := func(ts string, in, cached, out int) string {
		return `{"type":"event_msg","timestamp":"` + ts + `","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":` +
			itoa(in) + `,"cached_input_tokens":` + itoa(cached) + `,"output_tokens":` + itoa(out) + `}}}}`
	}
	at := day + "T10:00:00.000Z"
	writeLines(t, filepath.Join(dir, "rollout-1.jsonl"),
		ev(at, 1000, 400, 100), // 700
		ev(at, 1000, 400, 100), // repeated total: nothing new
		ev(at, 1500, 600, 200), // +300 input net (900-... ) → (1500-600)+200 = 1100 total, +400
		`{"type":"event_msg","timestamp":"`+at+`","payload":{"type":"token_count","info":null}}`,
	)
	r := NewDailyUsageReader([]SourceRoot{{Agent: AgentCodex, Directory: dir, NativeID: rolloutNativeID}})
	got, err := r.Read(context.Background(), now, tz)
	if err != nil {
		t.Fatal(err)
	}
	if n := dayMap(got[AgentCodex])[day]; n != 1100 {
		t.Fatalf("today = %d, want 1100", n)
	}
}

func TestDailyUsageSkipsStaleFilesAndCachesUnchangedOnes(t *testing.T) {
	dir := t.TempDir()
	tz := time.UTC
	now := time.Now().In(tz)
	path := filepath.Join(dir, "p", "a.jsonl")
	writeLines(t, path, `{"type":"assistant","timestamp":"`+now.Format("2006-01-02T15:04:05.000Z")+`","message":{"id":"m","usage":{"input_tokens":7,"output_tokens":3}}}`)
	r := NewDailyUsageReader([]SourceRoot{{Agent: AgentClaudeCode, Directory: dir, NativeID: func(s string) string { return s }}})
	read := func() int64 {
		t.Helper()
		got, err := r.Read(context.Background(), now.AddDate(0, 0, -1), tz)
		if err != nil {
			t.Fatal(err)
		}
		return SumDays(got[AgentClaudeCode])
	}
	if read() != 10 {
		t.Fatal("first read")
	}
	// Same mtime and size: the cached parse is used even if the bytes change.
	info, _ := os.Stat(path)
	mod := info.ModTime()
	if err := os.WriteFile(path, []byte(strings.Repeat("x", int(info.Size()))), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
	if read() != 10 {
		t.Fatal("an unchanged (mtime,size) file must come from the cache")
	}
	// A file whose mtime is older than the window is never opened.
	old := now.AddDate(0, 0, -40)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if read() != 0 {
		t.Fatal("a file untouched since before the window holds nothing in it")
	}
}

func TestDailyUsageMissingRootIsEmpty(t *testing.T) {
	r := NewDailyUsageReader([]SourceRoot{{Agent: AgentCodex, Directory: filepath.Join(t.TempDir(), "none"), NativeID: rolloutNativeID}})
	got, err := r.Read(context.Background(), time.Now(), time.UTC)
	if err != nil || SumDays(got[AgentCodex]) != 0 {
		t.Fatalf("got = %v, err = %v", got, err)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
