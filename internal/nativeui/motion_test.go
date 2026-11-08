package nativeui

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// literalDuration matches a transition that spells its own length.
var literalDuration = regexp.MustCompile(`Duration:\s*\d+\s*\*\s*time\.`)

// TestTransitionsUseTheMotionTokens: every ElementTransition names one of the
// four lengths in motion.go; none writes a duration of its own.
func TestTransitionsUseTheMotionTokens(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if loc := literalDuration.FindIndex(body); loc != nil {
			line := 1 + strings.Count(string(body[:loc[0]]), "\n")
			t.Errorf("%s:%d spells a transition duration; use motionColor/motionRow/motionPanel/motionHandOver", f, line)
		}
	}
}
