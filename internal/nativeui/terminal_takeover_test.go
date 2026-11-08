package nativeui

import (
	"testing"
)

/**
 * [INPUT]: pane_header_buttons_test.go 的 splitHeaderTestShell、attachConn.noteTakeover
 * [OUTPUT]: 接管告别行的跨块识别、被接管卡片的横幅与按钮、Take Back 的标记/摘除语义
 * [POS]: 钉住「只在被手机接管时才出现 Take Back，且只有点击才标记下一次 attach 为 takeover」
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

func TestAttachConnFindsTheTakeoverFarewellAcrossChunks(t *testing.T) {
	c := &attachConn{}
	for _, chunk := range []string{"\x1b[?1049lherdr: server shut down: terminal at", "tach taken", " over\r\n"} {
		if c.takenOver.Load() {
			t.Fatal("marker reported before it was complete")
		}
		c.noteTakeover([]byte(chunk))
	}
	if !c.takenOver.Load() {
		t.Fatal("the split farewell line was not recognised")
	}

	other := &attachConn{}
	other.noteTakeover([]byte("herdr: server shut down: terminal closed\r\n"))
	if other.takenOver.Load() {
		t.Fatal("an unrelated shutdown line counted as a takeover")
	}
}

func TestAttachConnTreatsARefusedAttachAsHeldElsewhere(t *testing.T) {
	c := &attachConn{}
	for _, chunk := range []string{"herdr: server shut down: terminal attach failed: terminal term_1 already has an at", "tached client; retry with --takeover\r\n"} {
		c.noteTakeover([]byte(chunk))
	}
	if !c.takenOver.Load() || !c.heldElsewhere.Load() {
		t.Fatalf("a refused attach must offer Take Back and say who holds it: takenOver=%v held=%v", c.takenOver.Load(), c.heldElsewhere.Load())
	}
	farewell := &attachConn{}
	farewell.noteTakeover([]byte("herdr: server shut down: terminal attach taken over\r\n"))
	if farewell.heldElsewhere.Load() {
		t.Fatal("a replaced attach was reported as never started")
	}
}

func TestPhoneReleaseReattachesOnlyATakenOverCardOfThatInstance(t *testing.T) {
	shell, _ := splitHeaderTestShell(t)
	shell.terminals["t2"].takenOver = true

	shell.phoneReleasedTerminal("another-instance", "t2")
	if shell.terminals["t2"] == nil {
		t.Fatal("a release on another Herdr instance touched this card")
	}
	shell.phoneReleasedTerminal(shell.activeInstance, "t1") // not taken over
	if shell.terminals["t1"] == nil {
		t.Fatal("a healthy card was re-attached")
	}
	shell.phoneReleasedTerminal(shell.activeInstance, "t2")
	if shell.terminals["t2"] != nil {
		t.Fatal("the dead attach was kept after the phone let go")
	}
	if shell.takeBack["t2"] {
		t.Fatal("re-attaching after a release must be an ordinary attach, never --takeover")
	}
}

func TestTakenOverCardOffersTakeBackAndOnlyThenReattachesWithTakeover(t *testing.T) {
	shell, tt := splitHeaderTestShell(t)
	if tt.HasText("Take Back") {
		t.Fatal("a healthy card must not offer Take Back")
	}

	shell.terminals["t2"].takenOver = true
	tt.Frame()
	if !tt.HasText("Your phone took over this terminal") || !tt.HasText("Take Back") {
		t.Fatalf("a taken-over card must say so and offer Take Back; texts=%v", tt.Texts())
	}
	if shell.takeBack["t2"] {
		t.Fatal("takeover was requested before the user pressed anything")
	}

	// A card that is not taken over ignores the action; the taken-over one is detached and marked for one --takeover attach.
	shell.takeBackTerminal("t1")
	if shell.terminals["t1"] == nil || shell.takeBack["t1"] {
		t.Fatal("a healthy terminal was taken back")
	}
	if err := tt.Click("Take Back"); err != nil {
		t.Fatal(err)
	}
	if shell.terminals["t2"] != nil || !shell.takeBack["t2"] {
		t.Fatalf("Take Back must drop the dead attach and mark the next one as --takeover (terminal=%v mark=%v)", shell.terminals["t2"], shell.takeBack["t2"])
	}
}
