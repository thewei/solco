package remotehost

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/thewei/solco/internal/herdr"
)

/**
 * [INPUT]: server_test.go 的 harness/fakeBackend、terminal_pane_test.go 的 layoutProjection/readPane
 * [OUTPUT]: 手机接管（`?mode=control`）的测试：以手机栅格接管并先告知、resize 写命令而非重启、滚动按窗格分流（有 scrollback 范围→一条 terminal.scroll；其余每行一条 lines=1，封顶 30）、退出时还原 Mac 窗格栅格并 release、被桌面抢回时收到 control off 后正常关闭、无 ControlBackend 不能接管且不宣告能力
 * [POS]: 接管是用户显式动作；这里钉住它的协议与副作用边界
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// controlBackend adds the takeover child to the fake: script builds the argv per start.
type controlBackend struct {
	*fakeBackend
	cmu     sync.Mutex
	script  func(terminalID string, cols, rows int) []string
	started []grid
}

func (b *controlBackend) ControlCommand(_, terminalID string, cols, rows int) ([]string, error) {
	b.cmu.Lock()
	b.started = append(b.started, grid{cols, rows})
	b.cmu.Unlock()
	return b.script(terminalID, cols, rows), nil
}

func (b *controlBackend) starts() []grid {
	b.cmu.Lock()
	defer b.cmu.Unlock()
	return append([]grid(nil), b.started...)
}

// recordingControl prints one frame then appends every stdin line to record until stdin closes (the shell
// stays the parent so the stdout pipe stays open while cat runs).
func recordingControl(record string) func(string, int, int) []string {
	return func(_ string, cols, rows int) []string {
		line := frameLine(fmt.Sprintf("CTL%dx%d", cols, rows), 1, true)
		return []string{"/bin/sh", "-c", fmt.Sprintf("echo '%s'; cat >> %s", line, record)}
	}
}

func controlHarness(t *testing.T) (*harness, *controlBackend, string) {
	t.Helper()
	h := newHarness(t)
	h.srv.controlStop = 10 * time.Second // never kill a slow-starting fake before it read what was written
	record := filepath.Join(t.TempDir(), "stdin")
	cb := &controlBackend{fakeBackend: h.back, script: recordingControl(record)}
	h.srv.opts.Backend = cb
	return h, cb, record
}

func dialControl(t *testing.T, h *harness, terminal string) *websocket.Conn {
	t.Helper()
	conn, _, err := h.dial("/api/v3/terminals/"+terminal+"/stream?mode=control&cols=60&rows=30", true)
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

func sendJSON(t *testing.T, conn *websocket.Conn, payload string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, []byte(payload)); err != nil {
		t.Fatal(err)
	}
}

// waitRecord polls the controller's recorded stdin until it contains want.
func waitRecord(t *testing.T, record, want string) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		b, _ := os.ReadFile(record)
		if strings.Contains(string(b), want) {
			return string(b)
		}
		if time.Now().After(deadline) {
			t.Fatalf("controller stdin never contained %q; got %q", want, b)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func scrollbackProjection(scrollMax uint64) herdr.Projection {
	p := fixtureProjection()
	p.Panes[1].Scroll = &herdr.PaneScroll{MaxOffsetFromBottom: scrollMax}
	return p
}

func TestControlTakesOverAtThePhonesGridAndSaysSo(t *testing.T) {
	h, cb, _ := controlHarness(t)
	conn := dialControl(t, h, "term_b")
	got := readJSON(t, conn)
	if got["t"] != "control" || got["on"] != true || got["cols"] != float64(60) || got["rows"] != float64(30) {
		t.Fatalf("first message = %v, want control on 60x30", got)
	}
	if head := readJSON(t, conn); head["t"] != "frame" {
		t.Fatalf("second message = %v, want the frame header", head)
	}
	if string(readBinary(t, conn)) != "CTL60x30" {
		t.Fatal("the stream was not the controller's")
	}
	if len(cb.starts()) != 1 || cb.starts()[0] != (grid{60, 30}) {
		t.Fatalf("controller starts = %v", cb.starts())
	}
	if len(h.back.observedGrids()) != 0 {
		t.Fatal("takeover must not also run an observer")
	}
}

func TestControlResizeWritesACommandInsteadOfRestarting(t *testing.T) {
	h, cb, record := controlHarness(t)
	conn := dialControl(t, h, "term_b")
	readJSON(t, conn)
	sendJSON(t, conn, `{"t":"resize","cols":50,"rows":25}`)
	waitRecord(t, record, `"type":"terminal.resize"`)
	got := waitRecord(t, record, `"cols":50`)
	if !strings.Contains(got, `"rows":25`) {
		t.Fatalf("resize command = %q", got)
	}
	if len(cb.starts()) != 1 {
		t.Fatalf("a resize restarted the controller: %v", cb.starts())
	}
}

func TestControlScrollSplitsByWhatThePaneCanDo(t *testing.T) {
	t.Run("a pane with scrollback moves Herdr's viewport", func(t *testing.T) {
		h, _, record := controlHarness(t)
		h.back.set(scrollbackProjection(27))
		conn := dialControl(t, h, "term_b")
		readJSON(t, conn)
		sendJSON(t, conn, `{"t":"scroll","lines":3}`)
		waitRecord(t, record, `"direction":"up"`)
		got := waitRecord(t, record, `"lines":3`)
		if !strings.Contains(got, `"type":"terminal.scroll"`) {
			t.Fatalf("scroll command = %q", got)
		}
		sendJSON(t, conn, `{"t":"scroll","lines":-2}`)
		waitRecord(t, record, `"direction":"down"`)
	})
	t.Run("a pane without a known scrollback range gets one tick per line", func(t *testing.T) {
		h, _, record := controlHarness(t)
		conn := dialControl(t, h, "term_a") // an Agent / alt-screen program: no scrollback range
		readJSON(t, conn)
		sendJSON(t, conn, `{"t":"scroll","lines":3}`)
		sendJSON(t, conn, `{"t":"resize","cols":50,"rows":25}`) // a later command proves the scroll was consumed first
		got := waitRecord(t, record, `terminal.resize`)
		if n := strings.Count(got, `"direction":"up","lines":1`); n != 3 {
			t.Fatalf("want 3 one-line scroll commands, got %d in %q", n, got)
		}
		if strings.Contains(got, "terminal.input") {
			t.Fatalf("the Host must not hand-write wheel bytes any more: %q", got)
		}
	})
	t.Run("ticks are capped so a fling cannot flood Herdr", func(t *testing.T) {
		h, _, record := controlHarness(t)
		conn := dialControl(t, h, "term_b")
		readJSON(t, conn)
		sendJSON(t, conn, `{"t":"scroll","lines":-150}`)
		sendJSON(t, conn, `{"t":"resize","cols":50,"rows":25}`)
		got := waitRecord(t, record, `terminal.resize`)
		if n := strings.Count(got, `"direction":"down","lines":1`); n != maxScrollTicks {
			t.Fatalf("got %d ticks, want the %d cap", n, maxScrollTicks)
		}
	})
}

// Typing while the viewport is scrolled back returns it to the live screen once (the phone's own view follows);
// a pane nothing scrolled never gets a down-scroll from typing, which would be a stray wheel tick for an alt-screen program.
func TestControlTypingSnapsAScrolledViewportBackOnce(t *testing.T) {
	snaps := func(record string) int {
		b, _ := os.ReadFile(record)
		return strings.Count(string(b), `"direction":"down","lines":65535`)
	}
	t.Run("after a scroll", func(t *testing.T) {
		h, _, record := controlHarness(t)
		h.back.set(scrollbackProjection(27))
		conn := dialControl(t, h, "term_b")
		readJSON(t, conn)
		sendJSON(t, conn, `{"t":"scroll","lines":5}`)
		waitRecord(t, record, `"direction":"up"`)
		writeClient(t, conn, map[string]any{"t": "input", "text": "ls", "keys": []string{"enter"}})
		waitRecord(t, record, `"direction":"down","lines":65535`)
		writeClient(t, conn, map[string]any{"t": "input", "text": "pwd", "keys": []string{"enter"}})
		sendJSON(t, conn, `{"t":"resize","cols":50,"rows":25}`) // proves the second input was consumed
		waitRecord(t, record, `"cols":50`)
		if n := snaps(record); n != 1 {
			t.Fatalf("snapped %d times, want once", n)
		}
	})
	t.Run("never scrolled", func(t *testing.T) {
		h, _, record := controlHarness(t)
		h.back.set(scrollbackProjection(27))
		conn := dialControl(t, h, "term_b")
		readJSON(t, conn)
		writeClient(t, conn, map[string]any{"t": "input", "text": "ls", "keys": []string{"enter"}})
		sendJSON(t, conn, `{"t":"resize","cols":50,"rows":25}`)
		waitRecord(t, record, `"cols":50`)
		if n := snaps(record); n != 0 {
			t.Fatalf("snapped %d times without a scroll", n)
		}
	})
}

// literalBackend adds the LiteralBackend capability to the recording fake.
type literalBackend struct {
	*fakeBackend
	mu    sync.Mutex
	typed []string
}

func (b *literalBackend) SendPaneText(_, _, text string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.typed = append(b.typed, text)
	return nil
}

func (b *literalBackend) texts() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.typed...)
}

func TestTypedTextGoesInAsKeystrokesAndPastesStayPastes(t *testing.T) {
	h := newHarness(t)
	lb := &literalBackend{fakeBackend: h.back}
	h.srv.opts.Backend = lb
	conn, _, err := h.dial("/api/v3/terminals/term_b/stream?cols=51&rows=19", true)
	if err != nil {
		t.Fatal(err)
	}
	readJSON(t, conn)
	readBinary(t, conn)

	for _, text := range []string{"3jx", "\r", "你好", "ls\x1b[A", "one\ntwo", "a\r"} {
		writeClient(t, conn, map[string]any{"t": "input", "text": text})
	}
	writeClient(t, conn, map[string]any{"t": "input", "text": "ls", "keys": []string{"enter"}})
	waitFor(t, func() bool { return len(lb.texts()) == 4 })
	if got := lb.texts(); fmt.Sprint(got) != fmt.Sprint([]string{"3jx", "\r", "你好", "ls\x1b[A"}) {
		t.Fatalf("literal texts = %q", got)
	}
	waitFor(t, func() bool { h.back.mu.Lock(); defer h.back.mu.Unlock(); return len(h.back.inputs) == 3 })
	h.back.mu.Lock()
	defer h.back.mu.Unlock()
	if h.back.inputs[0].text != "one\ntwo" || h.back.inputs[1].text != "a\r" || len(h.back.inputs[2].keys) != 1 {
		t.Fatalf("pastes and keyed input must keep send_input: %+v", h.back.inputs)
	}
}

func TestControlTapIsALeftClickAtThatCell(t *testing.T) {
	h, _, record := controlHarness(t)
	conn := dialControl(t, h, "term_b")
	readJSON(t, conn)
	sendJSON(t, conn, `{"t":"tap","col":12,"row":4}`)
	got := waitRecord(t, record, `"action":"up"`)
	down := strings.Index(got, `{"action":"down","button":"left","column":12,"row":4,"type":"terminal.mouse"}`)
	up := strings.Index(got, `{"action":"up","button":"left","column":12,"row":4,"type":"terminal.mouse"}`)
	if down < 0 || up < down {
		t.Fatalf("want a left down then up at (12,4), got %q", got)
	}
	// Out-of-range or incomplete taps never reach Herdr; a later command proves they were consumed.
	for _, bad := range []string{`{"t":"tap","col":-1,"row":2}`, `{"t":"tap","col":2}`, `{"t":"tap","col":9999,"row":1}`} {
		sendJSON(t, conn, bad)
	}
	sendJSON(t, conn, `{"t":"resize","cols":50,"rows":25}`)
	got = waitRecord(t, record, `terminal.resize`)
	if n := strings.Count(got, "terminal.mouse"); n != 2 {
		t.Fatalf("a malformed tap reached Herdr: %q", got)
	}
}

func TestControlMouseModeForwardsPressDragRelease(t *testing.T) {
	h, _, record := controlHarness(t)
	conn := dialControl(t, h, "term_b")
	readJSON(t, conn)
	for _, m := range []string{
		`{"t":"mouse","a":"down","col":5,"row":2}`,
		`{"t":"mouse","a":"drag","col":9,"row":2}`,
		`{"t":"mouse","a":"drag","col":14,"row":3}`,
		`{"t":"mouse","a":"up","col":14,"row":3}`,
		`{"t":"mouse","a":"sideways","col":1,"row":1}`, // unknown phase: dropped
		`{"t":"mouse","a":"drag","col":-4,"row":1}`,    // out of range: dropped
	} {
		sendJSON(t, conn, m)
	}
	sendJSON(t, conn, `{"t":"resize","cols":50,"rows":25}`)
	got := waitRecord(t, record, `terminal.resize`)
	var seq []string
	for _, line := range strings.Split(strings.TrimSpace(got), "\n") {
		if strings.Contains(line, "terminal.mouse") {
			var m struct {
				Action string `json:"action"`
				Button string `json:"button"`
				Column int    `json:"column"`
				Row    int    `json:"row"`
			}
			if err := json.Unmarshal([]byte(line), &m); err != nil {
				t.Fatal(err)
			}
			seq = append(seq, fmt.Sprintf("%s/%s@%d,%d", m.Action, m.Button, m.Column, m.Row))
		}
	}
	want := "[down/left@5,2 drag/left@9,2 drag/left@14,3 up/left@14,3]"
	if fmt.Sprint(seq) != want {
		t.Fatalf("mouse commands = %v, want %s", seq, want)
	}
}

func TestControlReleaseIsReportedOnlyWhenThePhoneLetsGo(t *testing.T) {
	released := make(chan [2]string, 4)
	h, cb, record := controlHarness(t)
	h.srv.opts.OnControlReleased = func(instance, terminalID string) { released <- [2]string{instance, terminalID} }
	conn := dialControl(t, h, "term_b")
	readJSON(t, conn)
	sendJSON(t, conn, `{"t":"resize","cols":50,"rows":25}`)
	waitRecord(t, record, `terminal.resize`)
	_ = conn.Close(websocket.StatusNormalClosure, "")
	select {
	case got := <-released:
		if got[1] != "term_b" {
			t.Fatalf("released = %v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the desktop was never told the phone let go")
	}

	cb.script = func(_ string, _, _ int) []string {
		closed := `{"type":"terminal.closed","reason":"terminal attach taken over"}`
		return []string{"/bin/sh", "-c", fmt.Sprintf("echo '%s'; exec sleep 30", closed)}
	}
	taken := dialControl(t, h, "term_b")
	readJSON(t, taken) // control on
	readJSON(t, taken) // control off (taken_over)
	_, _, _ = taken.Read(context.Background())
	select {
	case got := <-released:
		t.Fatalf("taken over by the desktop must not report a release: %v", got)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestControlGivesTheMacPaneItsGridBackOnExit(t *testing.T) {
	h, _, record := controlHarness(t)
	h.back.set(layoutProjection(120, 40))
	conn := dialControl(t, h, "term_b")
	readJSON(t, conn) // control on
	readJSON(t, conn) // frame header
	readBinary(t, conn)
	_ = conn.Close(websocket.StatusNormalClosure, "")
	got := waitRecord(t, record, `terminal.release`)
	resize := strings.Index(got, `"cols":120`)
	release := strings.Index(got, `terminal.release`)
	if resize < 0 || !strings.Contains(got, `"rows":40`) || resize > release {
		t.Fatalf("exit must resize to the Mac pane (120x40) before releasing: %q", got)
	}
}

// A real controller answers `terminal.release` with terminal.closed and exits at once; the handler must then
// return and free the device's terminal slot (a double wait on the pump's single result once leaked it, and two
// takeovers locked the phone out with too_many_terminals) and park the Mac pane at the bottom again.
func TestControlReleaseFreesTheSlotWhenTheChildExitsPromptly(t *testing.T) {
	h, cb, record := controlHarness(t)
	h.back.set(scrollbackProjection(27))
	cb.script = func(_ string, cols, rows int) []string {
		frame := frameLine("X", 1, true)
		closed := `{"type":"terminal.closed","reason":"detached"}`
		script := fmt.Sprintf(`echo '%s'; while read l; do echo "$l" >> %s; case "$l" in *terminal.release*) echo '%s'; exit 0;; esac; done`, frame, record, closed)
		return []string{"/bin/sh", "-c", script}
	}
	conn := dialControl(t, h, "term_b")
	readJSON(t, conn)
	sendJSON(t, conn, `{"t":"scroll","lines":5}`)
	waitRecord(t, record, `"direction":"up"`)
	_ = conn.Close(websocket.StatusNormalClosure, "")
	got := waitRecord(t, record, `terminal.release`)
	if !strings.Contains(got, `"direction":"down","lines":65535`) {
		t.Fatalf("the pane was left scrolled back: %q", got)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		h.srv.mu.Lock()
		n := len(h.srv.terminals)
		h.srv.mu.Unlock()
		if n == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the terminal slot was never freed after release")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestControlTakenOverByTheDesktopSaysSoAndClosesNormally(t *testing.T) {
	h, cb, _ := controlHarness(t)
	cb.script = func(_ string, cols, rows int) []string {
		frame := frameLine("X", 1, true)
		closed := `{"type":"terminal.closed","reason":"terminal attach taken over"}`
		return []string{"/bin/sh", "-c", fmt.Sprintf("echo '%s'; echo '%s'; exec sleep 30", frame, closed)}
	}
	conn := dialControl(t, h, "term_b")
	readJSON(t, conn) // control on
	readJSON(t, conn) // frame header
	readBinary(t, conn)
	got := readJSON(t, conn)
	if got["t"] != "control" || got["on"] != false || got["reason"] != "taken_over" {
		t.Fatalf("message = %v, want control off (taken_over)", got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, err := conn.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusNormalClosure {
		t.Fatalf("close = %v, want a normal closure so the phone can reopen as observer", err)
	}
}

func TestControlIsRefusedWithoutAControlBackendAndNotAdvertised(t *testing.T) {
	h := newHarness(t)
	if _, resp, err := h.dial("/api/v3/terminals/term_b/stream?mode=control&cols=60&rows=30", true); err == nil || resp == nil || resp.StatusCode != 400 {
		t.Fatalf("an observe-only backend must refuse takeover: err=%v resp=%v", err, resp)
	}
	if caps := bootstrapCaps(t, h); caps["terminalControl"] != nil {
		t.Fatalf("advertised without a ControlBackend: %v", caps)
	}
	h2, _, _ := controlHarness(t)
	if caps := bootstrapCaps(t, h2); caps["terminalControl"] != true {
		t.Fatalf("capability missing: %v", caps)
	}
}

func bootstrapCaps(t *testing.T, h *harness) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(h.get("/api/v3/bootstrap", h.token, nil).Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	caps, _ := body["capabilities"].(map[string]any)
	return caps
}

// The two server messages of takeover are wire contracts the phone's Zod schema pins.
func TestControlMessagesGolden(t *testing.T) {
	h, cb, _ := controlHarness(t)
	conn := dialControl(t, h, "term_b")
	golden(t, "terminal-control.json", readRaw(t, conn))

	cb.script = func(_ string, _, _ int) []string {
		closed := `{"type":"terminal.closed","reason":"terminal attach taken over"}`
		return []string{"/bin/sh", "-c", fmt.Sprintf("echo '%s'; exec sleep 30", closed)}
	}
	taken := dialControl(t, h, "term_b")
	readRaw(t, taken) // control on
	golden(t, "terminal-control-taken-over.json", readRaw(t, taken))
}
