/**
 * [INPUT]: stream.go 的 Event/Buffer 与 chatsession.ChatTurn
 * [OUTPUT]: 流式文本、乱序、跨消息、epoch、上界、转录真值对账的契约回归
 * [POS]: chatstream 纯函数测试；无 Herdr/OMP 进程副作用
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */
package chatstream

import (
	"strings"
	"testing"

	"github.com/thewei/solco/internal/chatsession"
)

func frame(seq uint64, kind, msg, delta string) Event {
	return Event{Agent: "omp", PaneID: "pane-1", SessionPath: "/tmp/session.jsonl", Epoch: "epoch-A", Seq: seq, Kind: kind, MessageID: msg, Delta: delta}
}
func TestBufferDeltaSequenceAndCanonicalReconcile(t *testing.T) {
	var b Buffer
	if !b.Apply(frame(1, "message_start", "m1", "")) {
		t.Fatal("start")
	}
	if !b.Apply(frame(2, "text_delta", "m1", "hel")) || !b.Apply(frame(3, "text_delta", "m1", "lo")) {
		t.Fatal("deltas")
	}
	if b.Apply(frame(2, "text_delta", "m1", "duplicate")) || b.Apply(frame(4, "text_delta", "other", "bad")) {
		t.Fatal("stale or mismatched accepted")
	}
	if b.Text != "hello" {
		t.Fatalf("text: %q", b.Text)
	}
	if !b.Apply(frame(4, "thinking_delta", "m1", "check")) {
		t.Fatal("thinking")
	}
	start := frame(5, "tool_start", "m1", "")
	start.ToolCallID, start.ToolName = "call-1", "bash"
	if !b.Apply(start) {
		t.Fatal("tool start")
	}
	end := frame(6, "tool_end", "m1", "")
	end.ToolCallID, end.ToolName = "call-1", "bash"
	if !b.Apply(end) {
		t.Fatal("tool end")
	}
	view := b.Overlay([]chatsession.ChatTurn{{User: true, Text: "question"}})
	if len(view) != 2 || !view[1].Live || view[1].Narration != "hello" || len(view[1].Tools) != 1 || !view[1].Steps[2].ToolDone {
		t.Fatalf("overlay: %+v", view)
	}
	if !b.Apply(frame(7, "message_end", "m1", "")) {
		t.Fatal("end")
	}
	thinking := "check"
	canonical := []chatsession.ChatTurn{{Narration: "hello", Thinking: &thinking}}
	if !b.Reconciled(canonical) || len(b.Overlay(canonical)) != 1 {
		t.Fatal("failed to settle")
	}
}
func TestBufferIdentityAndBounds(t *testing.T) {
	var b Buffer
	invalid := []Event{
		{Agent: "pi", PaneID: "p", SessionPath: "/tmp/f", Epoch: "e", Seq: 1, Kind: "message_start", MessageID: "m"},
		{Agent: "omp", PaneID: "p", SessionPath: "relative/f", Epoch: "e", Seq: 1, Kind: "message_start", MessageID: "m"},
		frame(1, "unexpected", "m", ""),
		frame(1, "text_delta", "m", strings.Repeat("x", MaxDeltaBytes+1)),
	}
	for _, ev := range invalid {
		if b.Apply(ev) {
			t.Fatalf("accepted invalid: %+v", ev)
		}
	}
	if !b.Apply(frame(1, "message_start", "m", "")) {
		t.Fatal("start")
	}
	seq := uint64(2)
	for len(b.Text)+MaxDeltaBytes <= MaxTextBytes {
		if !b.Apply(frame(seq, "text_delta", "m", strings.Repeat("x", MaxDeltaBytes))) {
			t.Fatal("in-bound delta")
		}
		seq++
	}
	if b.Apply(frame(seq, "text_delta", "m", "one extra")) {
		t.Fatal("unbounded content")
	}
	if len(b.Text) > MaxTextBytes {
		t.Fatalf("buffer over bound: %d", len(b.Text))
	}
	restart := frame(1, "message_start", "new", "")
	restart.Epoch = "epoch-B"
	if !b.Apply(restart) || b.Text != "" || b.Epoch != "epoch-B" {
		t.Fatalf("restart state: %+v", b)
	}
}
func TestStreamAddsRunningToolToCommittedNarrationWithoutDuplicatingTurn(t *testing.T) {
	var b Buffer
	_ = b.Apply(frame(1, "message_start", "m", ""))
	_ = b.Apply(frame(2, "text_delta", "m", "hello"))
	_ = b.Apply(frame(3, "message_end", "m", ""))
	toolStart := frame(4, "tool_start", "m", "")
	toolStart.ToolCallID, toolStart.ToolName = "id", "bash"
	if !b.Apply(toolStart) {
		t.Fatal("tool start after message_end")
	}
	base := []chatsession.ChatTurn{{Narration: "hello", Steps: []chatsession.ChatStep{{Kind: "narration", Text: "hello"}}}}
	if b.Reconciled(base) {
		t.Fatal("unfinished tool should keep overlay")
	}
	got := b.Overlay(base)
	if len(got) != 1 || len(got[0].Steps) != 2 || got[0].Steps[1].ToolDone || len(base[0].Steps) != 1 {
		t.Fatalf("running tool overlay: %+v", got)
	}
	toolEnd := frame(5, "tool_end", "m", "")
	toolEnd.ToolCallID, toolEnd.ToolName = "id", "bash"
	if !b.Apply(toolEnd) || !b.Reconciled(base) {
		t.Fatal("tool did not settle")
	}
}

func TestToolOnlyEventAfterTranscriptReconcileMergesWithLastAssistant(t *testing.T) {
	var b Buffer
	e := frame(6, "tool_start", "m", "")
	e.ToolCallID, e.ToolName = "call-1", "read"
	if !b.Apply(e) {
		t.Fatal("tool-only restart")
	}
	base := []chatsession.ChatTurn{{Narration: "done", Steps: []chatsession.ChatStep{{Kind: "narration", Text: "done"}}}}
	got := b.Overlay(base)
	if len(got) != 1 || len(got[0].Steps) != 2 || got[0].Steps[1].ToolName != "read" {
		t.Fatalf("tool only did not merge: %+v", got)
	}
}

func TestOldEpochCannotOverwriteNewStream(t *testing.T) {
	var b Buffer
	first := frame(1, "message_start", "a", "")
	first.Epoch = "0000000000100-old"
	second := frame(1, "message_start", "b", "")
	second.Epoch = "0000000000200-new"
	if !b.Apply(first) || !b.Apply(second) || b.MessageID != "b" {
		t.Fatal("epoch switch")
	}
	if b.Apply(first) || b.MessageID != "b" {
		t.Fatal("late old epoch erased new state")
	}
}

func TestOverlayDoesNotMutateCanonical(t *testing.T) {
	var b Buffer
	_ = b.Apply(frame(1, "message_start", "m", ""))
	_ = b.Apply(frame(2, "text_delta", "m", "hello world"))
	base := []chatsession.ChatTurn{{Narration: "hello", Steps: []chatsession.ChatStep{{Kind: "narration", Text: "hello"}}}}
	got := b.Overlay(base)
	if got[0].Narration != "hello world" || got[0].Steps[0].Text != "hello world" ||
		base[0].Narration != "hello" || base[0].Steps[0].Text != "hello" {
		t.Fatalf("canonical mutated: base=%+v overlay=%+v", base, got)
	}
}
