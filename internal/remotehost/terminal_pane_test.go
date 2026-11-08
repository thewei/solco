package remotehost

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/thewei/solco/internal/herdr"
)

/**
 * [INPUT]: server_test.go 的 harness/fakeBackend、herdr.Layout
 * [OUTPUT]: 终端流 `pane` 消息的测试：布局已知时首条文本帧、布局未知时无该帧、窗格被改尺寸时补发、栅格夹紧到流的上下限
 * [POS]: 手机只镜像 Mac 窗格栅格才能看全 Herdr observer 的左上窗口；Host 负责告知尺寸
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

func layoutProjection(width, height uint16) herdr.Projection {
	p := fixtureProjection()
	p.Layouts = []herdr.Layout{{TabID: "w1:t1", Panes: []herdr.LayoutPane{
		{PaneID: "w1:p2", Rect: herdr.LayoutRect{Width: width, Height: height}},
	}}}
	return p
}

type paneMessage struct {
	T    string `json:"t"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

func readPane(t *testing.T, raw []byte) paneMessage {
	t.Helper()
	var m paneMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestTerminalAnnouncesThePaneGridFirst(t *testing.T) {
	h := newHarness(t)
	h.back.set(layoutProjection(120, 40))
	conn, _, err := h.dial("/api/v3/terminals/term_b/stream?cols=51&rows=19", true)
	if err != nil {
		t.Fatal(err)
	}
	if got := readPane(t, readRaw(t, conn)); got != (paneMessage{T: "pane", Cols: 120, Rows: 40}) {
		t.Fatalf("first message = %+v, want the 120x40 pane grid", got)
	}
	if head := readPane(t, readRaw(t, conn)); head.T != "frame" {
		t.Fatalf("second message = %+v, want the frame header", head)
	}
}

func TestTerminalWithoutALayoutSendsNoPaneMessage(t *testing.T) {
	h := newHarness(t)
	conn, _, err := h.dial("/api/v3/terminals/term_b/stream?cols=51&rows=19", true)
	if err != nil {
		t.Fatal(err)
	}
	if head := readPane(t, readRaw(t, conn)); head.T != "frame" {
		t.Fatalf("first message = %+v, want the frame header (no layout known)", head)
	}
}

func TestTerminalReannouncesAResizedPane(t *testing.T) {
	h := newHarness(t)
	h.srv.paneWatch = 20 * time.Millisecond
	h.back.set(layoutProjection(120, 40))
	conn, _, err := h.dial("/api/v3/terminals/term_b/stream?cols=51&rows=19", true)
	if err != nil {
		t.Fatal(err)
	}
	readRaw(t, conn) // pane 120x40
	readRaw(t, conn) // frame header
	readBinary(t, conn)
	h.back.set(layoutProjection(90, 30))
	got := readPane(t, readRaw(t, conn))
	if got != (paneMessage{T: "pane", Cols: 90, Rows: 30}) {
		t.Fatalf("after the desktop resized the pane = %+v, want 90x30", got)
	}
}

func TestPaneGridIsClampedToTheStreamLimits(t *testing.T) {
	h := newHarness(t)
	h.back.set(layoutProjection(900, 2))
	g, ok := h.srv.paneGrid("default", "w1:p2")
	if !ok || g.cols != maxCols || g.rows != minRows {
		t.Fatalf("paneGrid = %+v, %v; want %dx%d", g, ok, maxCols, minRows)
	}
}
