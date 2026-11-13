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
		`{"type":"window.fit","viewport":{"w":390,"h":844,"scale":3,"insets":{"top":47,"bottom":34,"left":0,"right":0}}}`: &WindowFit{},
		`{"type":"window.fit.off"}`: &Envelope{},
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

func TestViewportSizeAndBounds(t *testing.T) {
	v := Viewport{W: 390, H: 844, Scale: 3, Insets: Insets{Top: 47, Bottom: 34}}
	if w, h := v.Size(); w != 390 || h != 763 || !v.Valid() {
		t.Fatalf("size %v×%v valid=%v", w, h, v.Valid())
	}
	for name, bad := range map[string]Viewport{
		"tiny":        {W: 50, H: 50, Scale: 2},
		"huge":        {W: 9000, H: 800, Scale: 2},
		"all inset":   {W: 390, H: 844, Scale: 2, Insets: Insets{Top: 500, Bottom: 400}},
		"no scale":    {W: 390, H: 844},
		"absurdscale": {W: 390, H: 844, Scale: 40},
	} {
		if bad.Valid() {
			t.Errorf("%s viewport accepted", name)
		}
	}
	b, _ := Marshal(WindowRestored{WindowID: "7", Outcome: RestoreSkipped})
	if string(b) != `{"type":"window.restored","windowId":"7","outcome":"skipped"}` {
		t.Fatalf("restored = %s", b)
	}
	b, _ = Marshal(SessionStateMsg{SessionState: SessionState{Mode: ModeFullControl, DisplayID: "1"}})
	if strings.Contains(string(b), "fitted") || strings.Contains(string(b), "clamped") {
		t.Fatalf("an unfitted state leaked fit fields: %s", b)
	}
}
