/**
 * [INPUT]: stream.go 的 Event/Buffer 与 chatsession.ChatTurn
 * [OUTPUT]: 流式文本、乱序、跨消息、epoch、上界、转录真值对账的契约回归
 * [POS]: chatstream 纯函数测试；无 Herdr/OMP 进程副作用
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */
package chatstream

import (
	"fmt"
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
	if b.Reconciled(canonical) {
		t.Fatal("retired tool before provider result")
	}
	canonical[0].Steps = []chatsession.ChatStep{{Kind: "tool", ToolName: "bash", ToolDone: true}}
	if !b.Reconciled(canonical) || len(b.Overlay(canonical)) != 1 {
		t.Fatal("failed to settle")
	}
}
func TestAgentEndMeansWholeTurnCompleteNotOneMessage(t *testing.T) {
	var b Buffer
	if !b.Apply(frame(1, "message_start", "m1", "")) {
		t.Fatal("start")
	}
	if b.Completed {
		t.Fatal("a message_start is never a terminal status")
	}
	if !b.Apply(frame(2, "message_end", "m1", "")) {
		t.Fatal("message end")
	}
	start := frame(3, "tool_start", "m1", "")
	start.ToolCallID, start.ToolName = "call-1", "read"
	if !b.Apply(start) {
		t.Fatal("tool start")
	}
	if b.Completed {
		t.Fatal("unfinished tool should not look complete until agent_end")
	}
	end := frame(4, "tool_end", "m1", "")
	end.ToolCallID, end.ToolName = "call-1", "read"
	if !b.Apply(end) || !b.Apply(frame(5, "agent_end", "m1", "")) || !b.Completed {
		t.Fatalf("agent_end did not settle completed assistant + tool: %+v", b)
	}
	if b.Apply(frame(6, "agent_end", "m1", "")) {
		t.Fatal("duplicated agent_end")
	}
	if !b.Apply(frame(6, "message_start", "m2", "")) || b.Completed {
		t.Fatal("a new message must restart the working state")
	}
}

func TestAgentEndRecoversLostMessageBoundaryWithoutLyingAboutText(t *testing.T) {
	t.Run("provider ended without message_end", func(t *testing.T) {
		var b Buffer
		b.Apply(frame(1, "message_start", "m1", ""))
		b.Apply(frame(2, "text_delta", "m1", "last answer"))
		if !b.Apply(frame(3, "agent_end", "m1", "")) || !b.Completed {
			t.Fatalf("explicit provider run end rejected: %+v", b)
		}
		if b.Apply(frame(4, "agent_end", "m1", "")) {
			t.Fatal("duplicated end")
		}
	})
	t.Run("dropped text still permits completion but never provisional text", func(t *testing.T) {
		var b Buffer
		b.Apply(frame(1, "message_start", "m1", ""))
		b.Apply(frame(2, "text_delta", "m1", "partial"))
		b.Apply(frame(4, "text_delta", "m1", " unsafe")) // lost seq 3
		if !b.Desynced || b.Text != "" {
			t.Fatal("lost delta was still shown")
		}
		if !b.Apply(frame(6, "agent_end", "m1", "")) || !b.Completed {
			t.Fatalf("completion did not survive dropped content: %+v", b)
		}
		if got := b.Overlay([]chatsession.ChatTurn{{User: true, Text: "prompt"}}); len(got) != 1 {
			t.Fatalf("desynced text leaked despite end: %+v", got)
		}
	})
}

func TestPiStreamUsesSameBoundedProtocol(t *testing.T) {
	var b Buffer
	start := frame(1, "message_start", "m1", "")
	start.Agent = "pi"
	if !b.Apply(start) {
		t.Fatal("Pi start rejected")
	}
	text := frame(2, "text_delta", "m1", "Hello Pi")
	text.Agent = "pi"
	if !b.Apply(text) || b.Text != "Hello Pi" {
		t.Fatalf("Pi token stream lost: %+v", b)
	}
}

func TestBufferIdentityAndBounds(t *testing.T) {
	var b Buffer
	invalid := []Event{
		{Agent: "unmanaged", PaneID: "p", SessionPath: "/tmp/f", Epoch: "e", Seq: 1, Kind: "message_start", MessageID: "m"},
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
	if !b.Apply(frame(seq, "text_delta", "m", "one extra")) {
		t.Fatal("oversized delta must invalidate the rendered turn")
	}
	if !b.Desynced || len(b.Text) != 0 {
		t.Fatalf("unbounded text escaped into UI: %+v", b)
	}
	restart := frame(1, "message_start", "new", "")
	restart.Epoch = "epoch-B"
	if !b.Apply(restart) || b.Text != "" || b.Epoch != "epoch-B" {
		t.Fatalf("restart state: %+v", b)
	}
}
func TestBufferOverflowDropsProvisionalTextUntilNextMessage(t *testing.T) {
	for _, kind := range []string{"text_delta", "thinking_delta"} {
		t.Run(kind, func(t *testing.T) {
			var b Buffer
			if !b.Apply(frame(1, "message_start", "m1", "")) {
				t.Fatal("start")
			}
			seq := uint64(2)
			for i := 0; i < MaxTextBytes/MaxDeltaBytes; i++ {
				if !b.Apply(frame(seq, kind, "m1", strings.Repeat("A", MaxDeltaBytes))) {
					t.Fatal("in-bound content")
				}
				seq++
			}
			if !b.Apply(frame(seq, kind, "m1", "overflow")) {
				t.Fatal("overflow must invalidate UI overlay")
			}
			if !b.Desynced || b.MessageID != "" || b.Text != "" || b.Thinking != "" {
				t.Fatalf("overflow exposed stale content: %+v", b)
			}
			if b.Apply(frame(seq+1, "message_end", "m1", "")) {
				t.Fatal("oversized turn resumed")
			}
			if !b.Apply(frame(seq+5, "message_start", "m2", "")) || b.Desynced {
				t.Fatal("new turn did not recover")
			}
		})
	}
}

func TestBufferToolLimitDropsProvisionalActivity(t *testing.T) {
	var b Buffer
	if !b.Apply(frame(1, "message_start", "m1", "")) {
		t.Fatal("start")
	}
	for i := 0; i < MaxTools+1; i++ {
		e := frame(uint64(i+2), "tool_start", "m1", "")
		e.ToolCallID = fmt.Sprintf("tool-%d", i)
		e.ToolName = "read"
		if !b.Apply(e) {
			t.Fatalf("tool event %d refused", i)
		}
	}
	if !b.Desynced || len(b.Tools) != 0 || b.MessageID != "" {
		t.Fatalf("tool limit leaked activity: %+v", b)
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
	if !b.Apply(toolEnd) || b.Reconciled(base) {
		t.Fatal("tool must await authoritative result")
	}
	base[0].Steps = append(base[0].Steps, chatsession.ChatStep{Kind: "tool", ToolName: "bash", ToolDone: true})
	if !b.Reconciled(base) {
		t.Fatal("settled provider tool not reconciled")
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

func TestBufferDropsIncompleteResponseAfterMissingFrame(t *testing.T) {
	var b Buffer
	if !b.Apply(frame(1, "message_start", "m1", "")) || !b.Apply(frame(2, "text_delta", "m1", "Hello")) {
		t.Fatal("initial stream")
	}
	if !b.Apply(frame(4, "text_delta", "m1", " world")) {
		t.Fatal("missing frame not detected")
	}
	if !b.Desynced || b.Text != "" || b.MessageID != "" {
		t.Fatalf("unsafe text after gap: %+v", b)
	}
	canonical := []chatsession.ChatTurn{{Narration: "provider text"}}
	if len(b.Overlay(canonical)) != 1 {
		t.Fatal("desynced overlay leaked")
	}
	if b.Apply(frame(5, "text_delta", "m1", " more")) {
		t.Fatal("old message resumed while desynced")
	}
	if !b.Apply(frame(8, "message_start", "m2", "")) || b.Desynced {
		t.Fatal("next message did not recover")
	}
	if !b.Apply(frame(9, "text_delta", "m2", "new")) || b.Text != "new" {
		t.Fatalf("recovery: %+v", b)
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
