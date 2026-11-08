package remotehost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

// historyBackend adds the optional history read to the shared fake; only the
// tests that opt in see the capability (the plain fake stays without it).
type historyBackend struct {
	*fakeBackend
	hmu     sync.Mutex
	history PaneHistory
	errs    []error // consumed one per read, then history is returned
	reads   int
}

func (b *historyBackend) ReadPaneHistory(_ context.Context, _, _ string) (PaneHistory, error) {
	b.hmu.Lock()
	defer b.hmu.Unlock()
	b.reads++
	if len(b.errs) > 0 {
		err := b.errs[0]
		b.errs = b.errs[1:]
		return PaneHistory{}, err
	}
	return b.history, nil
}

func (b *historyBackend) readCount() int {
	b.hmu.Lock()
	defer b.hmu.Unlock()
	return b.reads
}

func historyHarness(t *testing.T, text string) (*harness, *historyBackend) {
	t.Helper()
	h := newHarness(t)
	hb := &historyBackend{fakeBackend: h.back, history: PaneHistory{Text: text, Rows: 1234, Revision: 42}}
	h.srv.opts.Backend = hb
	return h, hb
}

func historyGet(t *testing.T, h *harness, terminal string) (int, map[string]any) {
	t.Helper()
	resp := h.get("/api/v3/terminals/"+terminal+"/history", h.token, nil)
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}

func TestTailHistoryKeepsTheEndOnALineBoundary(t *testing.T) {
	if got, cut := tailHistory("a\nb\n", 100); got != "a\nb\n" || cut {
		t.Fatalf("short text changed: %q %v", got, cut)
	}
	text := "line1\nline2\nline3\nline4\n"
	got, cut := tailHistory(text, 14) // the last 14 bytes start mid-"line2"; the tail begins at the next line
	if !cut || got != "line3\nline4\n" {
		t.Fatalf("got %q cut=%v", got, cut)
	}
	// a single line longer than the limit still yields a valid, bounded tail
	long := strings.Repeat("字", 100)
	got, cut = tailHistory(long, 50)
	if !cut || len(got) > 50 || !utf8.ValidString(got) || got == "" {
		t.Fatalf("len=%d valid=%v cut=%v", len(got), utf8.ValidString(got), cut)
	}
}

func TestTerminalHistoryAnswersPlainTextAndCaches(t *testing.T) {
	h, hb := historyHarness(t, "one\ntwo\nthree\n")
	now := time.Now()
	h.srv.opts.Now = func() time.Time { return now }

	code, body := historyGet(t, h, "term_a")
	if code != 200 || body["text"] != "one\ntwo\nthree\n" || body["retainedRows"].(float64) != 1234 ||
		body["revision"].(float64) != 42 || body["truncated"] != false {
		t.Fatalf("%d %v", code, body)
	}
	historyGet(t, h, "term_a")
	if hb.readCount() != 1 {
		t.Fatalf("a second read inside the TTL hit Herdr: %d reads", hb.readCount())
	}
	now = now.Add(historyTTL + time.Second)
	historyGet(t, h, "term_a")
	if hb.readCount() != 2 {
		t.Fatalf("an expired entry was not re-read: %d reads", hb.readCount())
	}
}

func TestTerminalHistoryCapsTheTextAtTheTail(t *testing.T) {
	big := strings.Repeat("0123456789abcdef\n", (historyMaxBytes/17)+500)
	h, _ := historyHarness(t, big)
	code, body := historyGet(t, h, "term_a")
	text, _ := body["text"].(string)
	if code != 200 || body["truncated"] != true || len(text) > historyMaxBytes || !strings.HasSuffix(text, "abcdef\n") || !strings.HasPrefix(text, "0123456789abcdef\n") {
		t.Fatalf("%d truncated=%v len=%d", code, body["truncated"], len(text))
	}
}

func TestTerminalHistoryRetriesAChangingPaneThenSaysBusy(t *testing.T) {
	h, hb := historyHarness(t, "ok\n")
	hb.errs = []error{errors.New("Herdr pane content is changing"), errors.New("rpc stale_content")}
	code, body := historyGet(t, h, "term_a")
	if code != 200 || body["text"] != "ok\n" {
		t.Fatalf("a pane that settles within the retries must answer: %d %v", code, body)
	}

	h2, hb2 := historyHarness(t, "never\n")
	for i := 0; i < historyAttempts+2; i++ {
		hb2.errs = append(hb2.errs, errors.New("Herdr pane history changed during read"))
	}
	code, body = historyGet(t, h2, "term_a")
	errObj, _ := body["error"].(map[string]any)
	if code != 409 || errObj["code"] != "history_busy" || hb2.readCount() != historyAttempts {
		t.Fatalf("%d %v reads=%d", code, body, hb2.readCount())
	}
}

func TestTerminalHistoryFailuresAreNotRetriedAndNameTheirCause(t *testing.T) {
	h, hb := historyHarness(t, "x")
	hb.errs = []error{errors.New("socket closed")}
	code, body := historyGet(t, h, "term_a")
	errObj, _ := body["error"].(map[string]any)
	if code != 502 || errObj["code"] != "backend_unavailable" || hb.readCount() != 1 {
		t.Fatalf("%d %v reads=%d", code, body, hb.readCount())
	}
	if code, body = historyGet(t, h, "nope"); code != 404 || body["error"].(map[string]any)["code"] != "unknown_terminal" {
		t.Fatalf("%d %v", code, body)
	}
}

func TestTerminalHistoryNeedsTheViewScopeAndIsAdvertisedOnlyWhenBackedBySomething(t *testing.T) {
	h := newHarness(t)
	var plain map[string]any
	_ = json.NewDecoder(h.get("/api/v3/bootstrap", h.token, nil).Body).Decode(&plain)
	if caps, _ := plain["capabilities"].(map[string]any); caps["terminalHistory"] != nil {
		t.Fatalf("a Backend without history advertised it: %v", caps)
	}
	if resp := h.get("/api/v3/terminals/term_a/history", h.token, nil); resp.StatusCode != 404 {
		t.Fatalf("no history backend must not serve the route: %d", resp.StatusCode)
	}

	h2, _ := historyHarness(t, "x")
	var withIt map[string]any
	_ = json.NewDecoder(h2.get("/api/v3/bootstrap", h2.token, nil).Body).Decode(&withIt)
	if caps, _ := withIt["capabilities"].(map[string]any); caps["terminalHistory"] != true {
		t.Fatalf("capability missing: %v", caps)
	}
	if resp := h2.get("/api/v3/terminals/term_a/history", "", nil); resp.StatusCode != 401 {
		t.Fatalf("unauthenticated read: %d", resp.StatusCode)
	}
}

func TestTerminalHistoryNeverLogsTheContent(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })

	h, hb := historyHarness(t, "SECRET-CONVERSATION-BODY\n")
	historyGet(t, h, "term_a")
	hb.errs = []error{errors.New("socket closed")}
	h.srv.opts.Now = func() time.Time { return time.Now().Add(time.Hour) }
	historyGet(t, h, "term_a")
	if strings.Contains(buf.String(), "SECRET-CONVERSATION-BODY") {
		t.Fatalf("history content reached the log:\n%s", buf.String())
	}
}
