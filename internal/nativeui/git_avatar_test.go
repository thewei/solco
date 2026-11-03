package nativeui

import (
	"testing"

	"github.com/thewei/solco/internal/gitworkbench"
)

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

func TestSplitConventionalSubject(t *testing.T) {
	for in, want := range map[string][2]string{
		"feat: add x":          {"feat:", "add x"},
		"fix(ui)!: crash":      {"fix(ui)!:", "crash"},
		"wip: before conflict": {"wip:", "before conflict"},
	} {
		kind, rest, ok := splitConventionalSubject(in)
		if !ok || kind != want[0] || rest != want[1] {
			t.Errorf("%q -> %q %q %v", in, kind, rest, ok)
		}
	}
	for _, in := range []string{"Merge branch 'main': x", "remote fourth", "feat:", "feat: ", "a very long prefix indeed here: x", "http://x: y"} {
		if _, _, ok := splitConventionalSubject(in); ok {
			t.Errorf("%q must stay one plain subject", in)
		}
	}
}

func TestHeadAncestorsMarkTheMergedLine(t *testing.T) {
	head := gitworkbench.CommitInfo{Hash: "c", Parents: []string{"b"}, Refs: []gitworkbench.CommitRef{{Head: true}}}
	side := gitworkbench.CommitInfo{Hash: "s", Parents: []string{"b"}}
	b := gitworkbench.CommitInfo{Hash: "b", Parents: []string{"a"}}
	a := gitworkbench.CommitInfo{Hash: "a"}
	got := gitHeadAncestors([]gitworkbench.CommitInfo{head, side, b, a})
	for h, want := range map[string]bool{"c": true, "b": true, "a": true, "s": false} {
		if got[h] != want {
			t.Errorf("merged[%s]=%v want %v", h, got[h], want)
		}
	}
	if gitHeadAncestors([]gitworkbench.CommitInfo{side, b}) != nil {
		t.Error("without a HEAD ref nothing may read as unmerged")
	}
}
