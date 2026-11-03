package macos

import (
	"fmt"
	"os"
	"testing"
)

func TestKeyCodes(t *testing.T) {
	// Spot checks against HIToolbox/Events.h.
	want := map[string]uint16{
		"KeyA": 0, "KeyC": 8, "KeyV": 9, "Enter": 36, "Tab": 48, "Space": 49, "Backspace": 51, "Escape": 53,
		"MetaLeft": 55, "ShiftLeft": 56, "AltLeft": 58, "ControlLeft": 59, "ArrowLeft": 123, "ArrowUp": 126,
		"F1": 122, "Home": 115, "End": 119, "PageUp": 116, "PageDown": 121, "Delete": 117, "Digit1": 18, "Digit0": 29,
	}
	for name, code := range want {
		got, err := KeyCode(name)
		if err != nil || got != code {
			t.Errorf("KeyCode(%q) = %d, %v; want %d", name, got, err, code)
		}
	}
}

func TestKeyCodesAreUniqueAndComplete(t *testing.T) {
	seen := map[uint16]string{}
	for name, code := range keyCodes {
		if prev, dup := seen[code]; dup {
			t.Errorf("code %d is both %s and %s", code, prev, name)
		}
		seen[code] = name
	}
	// Every letter, digit and F1–F12 the phone's keyboard can send resolves.
	for r := 'A'; r <= 'Z'; r++ {
		if _, err := KeyCode("Key" + string(r)); err != nil {
			t.Error(err)
		}
	}
	for d := 0; d <= 9; d++ {
		if _, err := KeyCode(fmt.Sprintf("Digit%d", d)); err != nil {
			t.Error(err)
		}
	}
	for f := 1; f <= 12; f++ {
		if _, err := KeyCode(fmt.Sprintf("F%d", f)); err != nil {
			t.Error(err)
		}
	}
	if _, err := KeyCode("Nope\x00"); err == nil {
		t.Error("unknown key must fail")
	}
}

func TestLocatePrefersEnvThenBundleThenSibling(t *testing.T) {
	dir := t.TempDir()
	write := func(p string) string {
		full := dir + "/" + p
		mustWrite(t, full)
		return full
	}
	override := write("override/helper")
	if got, ok := Locate("", "", func(k string) string {
		if k == EnvHelper {
			return override
		}
		return ""
	}); !ok || got.Path != override || got.Bundled {
		t.Fatalf("env override: %+v %v", got, ok)
	}
	bundled := write("Solco.app/Contents/Helpers/" + HelperName)
	if got, ok := Locate(dir+"/Solco.app/Contents/MacOS/Solco", "", func(string) string { return "" }); !ok || got.Path != bundled || !got.Bundled {
		t.Fatalf("bundle: %+v %v", got, ok)
	}
	if err := os.Remove(bundled); err != nil {
		t.Fatal(err)
	}
	shipped := write("Solco.app/Contents/Resources/" + HelperName)
	if got, ok := Locate(dir+"/Solco.app/Contents/MacOS/Solco", "", func(string) string { return "" }); !ok || got.Path != shipped || !got.Bundled {
		t.Fatalf("resources: %+v %v", got, ok)
	}
	sibling := write("solco-remote-helper/.build/release/" + HelperName)
	if got, ok := Locate("", dir+"/solco-worktrees/remote-desktop", func(string) string { return "" }); !ok || got.Path != sibling {
		t.Fatalf("sibling: %+v %v", got, ok)
	}
	if _, ok := Locate("", t.TempDir(), func(string) string { return "" }); ok {
		t.Fatal("nothing to find must report not found")
	}
}
