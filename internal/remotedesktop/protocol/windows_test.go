package protocol

import (
	"fmt"
	"strings"
	"testing"
)

func TestWindowMessagesDecodeAndMarshal(t *testing.T) {
	for typ, want := range map[string]any{
		`{"type":"target.select","kind":"window","id":"42"}`:         &TargetSelect{},
		`{"type":"app.open","bundleId":"com.apple.Notes"}`:           &BundleRef{},
		`{"type":"app.icon.get","bundleId":"com.apple.Notes"}`:       &BundleRef{},
		`{"type":"window.thumb.get","windowId":"42","maxWidth":320}`: &ThumbGet{},
		`{"type":"windows.list"}`:                                    &Envelope{},
		`{"type":"apps.list"}`:                                       &Envelope{},
		`{"type":"target.activate"}`:                                 &Envelope{},
	} {
		got, err := Decode([]byte(typ))
		if err != nil {
			t.Fatalf("%s: %v", typ, err)
		}
		if fmt.Sprintf("%T", got) != fmt.Sprintf("%T", want) {
			t.Errorf("%s decoded as %T", typ, got)
		}
	}
	b, err := Marshal(WindowsSnapshot{})
	if err != nil || string(b) != `{"type":"windows.snapshot","windows":[]}` {
		t.Fatalf("empty snapshot = %s %v", b, err)
	}
	b, _ = Marshal(WindowGone{WindowID: "7", Reason: GoneAppQuit})
	if string(b) != `{"type":"window.gone","windowId":"7","reason":"app_quit"}` {
		t.Fatalf("gone = %s", b)
	}
	// A display session's state carries none of the window fields.
	b, _ = Marshal(SessionStateMsg{SessionState: SessionState{Mode: ModeFullControl, DisplayID: "1"}})
	if strings.Contains(string(b), "target") || strings.Contains(string(b), "window") {
		t.Fatalf("display state leaked window fields: %s", b)
	}
}
