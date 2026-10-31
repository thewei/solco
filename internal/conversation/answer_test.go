package conversation

import (
	"strings"
	"testing"
)

func TestMultiSelectKeysTickRowsThenSubmit(t *testing.T) {
	options := []string{"auth", "logging", "cache", "metrics"}
	got := MultiSelectKeys(options, []string{"auth", "cache"})
	want := []string{"space", "down", "down", "space", "enter"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("keys = %v, want %v", got, want)
	}
	if got := MultiSelectKeys(options, []string{"metrics"}); strings.Join(got, ",") != "down,down,down,space,enter" {
		t.Fatalf("keys = %v", got)
	}
	if got := MultiSelectKeys(options, nil); strings.Join(got, ",") != "enter" {
		t.Fatalf("an empty pick only submits: %v", got)
	}
}

func TestAnswerKeysNavigateRelativeToStart(t *testing.T) {
	p := PendingInteraction{Options: []string{"a", "b", "c", "Chat about this"}, Start: 1}
	for _, tc := range []struct {
		option string
		typed  bool
		want   string
	}{
		{"b", false, "enter"},
		{"c", false, "down,enter"},
		{"a", false, "up,enter"},
		{"Chat about this", false, "down,down,enter"},
		{"Chat about this", true, "esc"},
	} {
		got, err := p.AnswerKeys(tc.option, tc.typed)
		if err != nil || strings.Join(got, ",") != tc.want {
			t.Fatalf("%q typed=%v = %v, %v; want %s", tc.option, tc.typed, got, err, tc.want)
		}
	}
	if _, err := p.AnswerKeys("nope", false); err != ErrUnknownOption {
		t.Fatalf("unknown option = %v", err)
	}
	p.Commit = "s"
	if got, _ := p.AnswerKeys("b", false); strings.Join(got, ",") != "s" {
		t.Fatalf("commit key = %v", got)
	}
	p.EscapeOption = "c"
	if got, _ := p.AnswerKeys("c", false); strings.Join(got, ",") != "esc" {
		t.Fatalf("escape option = %v", got)
	}
}
