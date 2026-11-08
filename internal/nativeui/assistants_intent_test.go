package nativeui

import (
	"context"
	"testing"
	"time"
)

/**
 * [INPUT]: assistants_intent/avatar 与 assistants_test 的假插件夹具
 * [OUTPUT]: create intent 回归（Snapshot 反复读也不丢、ACK 后才撤、新意图不被旧 ACK 吃掉、过期丢弃）与头像纯函数（首字回退、改名不换色、叠头像上限）
 * [POS]: MU-02/MU-04 的自动化部分；真实窗口/菜单见 docs/debug/2026-11-04-assistants-v3.md
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

func TestAssistantsIntentSurvivesEverySnapshotUntilAcked(t *testing.T) {
	stubAssistants(t, false)
	s := openAssistantsShell(t)
	b := NewAssistantsBridge(s)
	s.postAssistantsIntent(intentNewAssistant)
	// The old flag was cleared by the first read: a poll that raced the click
	// (or a read whose answer the page never mounted for) ate the sheet.
	for i := 0; i < 3; i++ {
		in := b.PendingIntent()
		if in == nil || in.Kind != intentNewAssistant || in.Seq != 1 {
			t.Fatalf("read %d lost the intent: %+v", i, in)
		}
		if _, err := b.Snapshot(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	b.AckIntent(1)
	if in := b.PendingIntent(); in != nil {
		t.Fatalf("an acknowledged intent must go: %+v", in)
	}
	if s.router.Path() != routeAssistants {
		t.Fatalf("posting an intent opens the page, path=%q", s.router.Path())
	}
}

func TestAssistantsIntentSequenceIsMonotonicAndStaleAckIsHarmless(t *testing.T) {
	stubAssistants(t, false)
	s := openAssistantsShell(t)
	b := NewAssistantsBridge(s)
	s.postAssistantsIntent(intentNewAssistant)
	s.postAssistantsIntent(intentNewAssistant)
	b.AckIntent(1) // the page answered the first; the second is still owed
	if in := b.PendingIntent(); in == nil || in.Seq != 2 || in.Kind != intentNewAssistant {
		t.Fatalf("the second ask must survive the first's ack: %+v", in)
	}
	b.AckIntent(2)
	b.AckIntent(1) // a late, older ack never resurrects or lowers anything
	s.postAssistantsIntent(intentNewAssistant)
	if in := b.PendingIntent(); in == nil || in.Seq != 3 {
		t.Fatalf("a third post after acks must be pending: %+v", in)
	}
}

// The failure the real window showed: the plugin CLI is unreachable, every
// Snapshot errors — and the sheet must still open (the intent is not carried
// by a Snapshot).
func TestAssistantsIntentDoesNotNeedAWorkingPluginRead(t *testing.T) {
	stubAssistants(t, false)
	s := openAssistantsShell(t)
	b := NewAssistantsBridge(s)
	stubAssistants(t, true) // from here on every plugin call fails
	s.postAssistantsIntent(intentNewAssistant)
	if _, err := b.Snapshot(context.Background()); err == nil {
		t.Fatal("the stubbed plugin should be failing")
	}
	if in := b.PendingIntent(); in == nil || in.Kind != intentNewAssistant {
		t.Fatalf("a failing plugin read hid the click: %+v", in)
	}
}

func TestAssistantsIntentExpiresWhenNobodyTakesIt(t *testing.T) {
	stubAssistants(t, false)
	s := openAssistantsShell(t)
	s.postAssistantsIntent(intentNewAssistant)
	real := timeNow
	t.Cleanup(func() { timeNow = real })
	timeNow = func() time.Time { return real().Add(assistantsIntentTTL + time.Second) }
	if got := s.pendingIntent(); got != nil {
		t.Fatalf("a stale intent must not pop a sheet later: %+v", got)
	}
}

func TestAssistantsIntentIsRefusedWhileTheGateIsClosed(t *testing.T) {
	stubAssistants(t, false)
	s := NewShell()
	s.postAssistantsIntent(intentNewAssistant)
	if s.pendingIntent() != nil || s.router.Path() == routeAssistants {
		t.Fatal("a closed gate must leave no trace")
	}
}

func TestAvatarInitialAndToneAreStable(t *testing.T) {
	for name, want := range map[string]string{
		"research assistant": "R", "  ada": "A", "研发助理": "研", "7th": "7", "": "?", "   ": "?", "🙂 smile": "S", "🙂": "🙂",
	} {
		if got := avatarInitial(name); got != want {
			t.Fatalf("avatarInitial(%q) = %q, want %q", name, got, want)
		}
	}
	// The colour follows the id, so renaming never repaints a contact.
	if avatarToneForID("a_1", "Ada") != avatarToneForID("a_1", "Grace") {
		t.Fatal("rename changed the avatar colour")
	}
	if avatarToneForID("", "Ada") != avatarToneForID("", " ada ") {
		t.Fatal("without an id the name (case/space folded) decides")
	}
	seen := map[TileTone]bool{}
	for _, id := range []string{"a_1", "a_2", "a_3", "a_4", "a_5", "a_6", "a_7", "a_8", "a_9"} {
		seen[avatarToneForID(id, "")] = true
	}
	if len(seen) < 3 {
		t.Fatalf("ids should spread over the palette, got %d tones", len(seen))
	}
}
