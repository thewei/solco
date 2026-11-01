package nativeui

import (
	"testing"

	"github.com/thewei/solco/internal/conversation"
)

func TestTerminalDialogHoldsOnlyTheBoundPane(t *testing.T) {
	s := &Shell{chatPaneID: "p1", chatTerminalPane: "p1", chatTerminalReason: "Cursor Agent is asking whether to trust this folder."}
	if got := s.chatDispositionNow(); got != conversation.DispositionNeedsTerminal {
		t.Fatalf("disposition = %q, want needs-terminal", got)
	}
	if s.terminalReason() == "" {
		t.Fatal("the card needs its sentence")
	}
	s.chatPaneID = "p2" // the chat moved to another pane: the hold is not its own
	if s.terminalDialogHeld() || s.terminalReason() != "" {
		t.Fatal("a hold must not leak onto another pane")
	}
}
