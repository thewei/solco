package nativeui

import "testing"

func TestAuthorToneIsStableAndCaseBlind(t *testing.T) {
	if authorTone("Wilson") != authorTone("  wilson ") {
		t.Fatal("same author must keep the same tone")
	}
}

func TestAuthorInitial(t *testing.T) {
	for in, want := range map[string]string{"wilson": "W", "  张伟": "张", "9lives": "9", "": "?", "--": "?"} {
		if got := authorInitial(in); got != want {
			t.Errorf("authorInitial(%q)=%q want %q", in, got, want)
		}
	}
}
