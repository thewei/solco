package remotehost

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestPeekLineTakesTheLastLineThatSaysSomething(t *testing.T) {
	cases := map[string]string{
		"":               "",
		"\n  \n\t\n":     "",
		"build ok\n\n\n": "build ok",
		"npm test\n  3 passing\n╰──────╯": "3 passing",
		"╭──╮\n│  │\n╰──╯":                "",
		"a\x1b[31mred\x1b[0m":             "a[31mred[0m", // stray ESC dropped, the rest is plain text
		"tab\tseparated":                  "tab separated",
		"  中文输出完成  ":                      "中文输出完成",
		"Working on task\nesc to cancel · tab amend": "Working on task",
		"Running command\nctrl+g edit":               "Running command",
		"esc to cancel":                              "",
	}
	for in, want := range cases {
		if got := PeekLine(in); got != want {
			t.Errorf("PeekLine(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPeekLineCutsOnARuneBoundaryWithinTheLimit(t *testing.T) {
	long := strings.Repeat("字", 200) // 600 bytes
	got := PeekLine(long)
	if len(got) > peekMaxBytes || !utf8.ValidString(got) || got == "" {
		t.Fatalf("len=%d valid=%v", len(got), utf8.ValidString(got))
	}
	if ascii := PeekLine(strings.Repeat("x", 500)); len(ascii) != peekMaxBytes {
		t.Fatalf("ascii len = %d", len(ascii))
	}
}

func peekGet(t *testing.T, h *harness, pane string) (int, map[string]any) {
	t.Helper()
	resp := h.get("/api/v3/panes/"+pane+"/peek", h.token, nil)
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}

func TestPeekEndpointReturnsTheLineAndThrottlesHerdrReads(t *testing.T) {
	h := newHarness(t)
	now := time.Now()
	h.srv.opts.Now = func() time.Time { return now }
	h.back.screen = "ls\nfoo bar\n│       │"

	code, body := peekGet(t, h, "w1:p1")
	if code != 200 || body["line"] != "foo bar" || body["paneId"] != "w1:p1" {
		t.Fatalf("%d %v", code, body)
	}
	peekGet(t, h, "w1:p1")
	if h.back.reads != 1 {
		t.Fatalf("second poll inside 1s reached Herdr: reads=%d", h.back.reads)
	}
	peekGet(t, h, "w1:p2") // another pane is not throttled by the first
	if h.back.reads != 2 {
		t.Fatalf("reads=%d", h.back.reads)
	}
	now = now.Add(1100 * time.Millisecond)
	h.back.screen = "done"
	if _, body = peekGet(t, h, "w1:p1"); body["line"] != "done" || h.back.reads != 3 {
		t.Fatalf("%v reads=%d", body, h.back.reads)
	}
}

func TestPeekEndpointRefusesStrangersAndUnknownPanesWithoutTouchingHerdr(t *testing.T) {
	h := newHarness(t)
	if resp := h.get("/api/v3/panes/w1:p1/peek", "", nil); resp.StatusCode != 401 {
		t.Fatalf("no token: %d", resp.StatusCode)
	}
	for _, pane := range []string{"nope", "..%2f..%2fetc", "w1:p1;rm"} {
		if code, _ := peekGet(t, h, pane); code != 404 {
			t.Fatalf("%q: %d", pane, code)
		}
	}
	if resp := h.get("/api/v3/panes/w1:p1/peek?instance=ghost", h.token, nil); resp.StatusCode != 404 {
		t.Fatalf("ghost instance: %d", resp.StatusCode)
	}
	if h.back.reads != 0 {
		t.Fatalf("unvalidated IDs reached Herdr: reads=%d", h.back.reads)
	}
}

func TestPeekEndpointFailureIsAGenericBadGateway(t *testing.T) {
	h := newHarness(t)
	h.back.readErr = errBoom("socket /Users/x/secret.sock refused")
	resp := h.get("/api/v3/panes/w1:p1/peek", h.token, nil)
	var raw strings.Builder
	buf := make([]byte, 512)
	n, _ := resp.Body.Read(buf)
	raw.Write(buf[:n])
	if resp.StatusCode != 502 || strings.Contains(raw.String(), "secret") {
		t.Fatalf("%d %s", resp.StatusCode, raw.String())
	}
}

type errBoom string

func (e errBoom) Error() string { return string(e) }
