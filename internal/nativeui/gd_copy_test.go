package nativeui

import (
	"testing"

	"github.com/thewei/solco/internal/gitworkbench"
)

func TestGdHunkAndFilePatchText(t *testing.T) {
	h := gitworkbench.Hunk{OldStart: 3, OldLines: 2, NewStart: 3, NewLines: 3, Section: "func main()", Lines: []gitworkbench.DiffLine{
		{Kind: gitworkbench.KindContext, OldLine: 3, NewLine: 3, Text: "a"},
		{Kind: gitworkbench.KindDelete, OldLine: 4, Text: "old"},
		{Kind: gitworkbench.KindAdd, NewLine: 4, Text: "new"},
		{Kind: gitworkbench.KindAdd, NewLine: 5, Text: "tail", NoNewline: true},
	}}
	want := "@@ -3,2 +3,3 @@ func main()\n a\n-old\n+new\n+tail\n\\ No newline at end of file\n"
	if got := gdHunkPatch(h); got != want {
		t.Fatalf("hunk patch =\n%q\nwant\n%q", got, want)
	}
	if got := gdHunkPatch(gitworkbench.Hunk{OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 1}); got != "@@ -1 +1 @@\n" {
		t.Fatalf("single-line ranges drop the count: %q", got)
	}

	f := &gdFile{cf: &gitworkbench.ChangeFile{Path: "x.go", Status: gitworkbench.StatusModified, Hunks: []gitworkbench.Hunk{h}}}
	if got, want := gdFilePatch(f), "--- a/x.go\n+++ b/x.go\n"+want; got != want {
		t.Fatalf("file patch = %q", got)
	}
	added := &gdFile{cf: &gitworkbench.ChangeFile{Path: "n.go", Status: gitworkbench.StatusAdded, Hunks: []gitworkbench.Hunk{h}}}
	if got := gdFilePatch(added); got[:len("--- /dev/null\n+++ b/n.go\n")] != "--- /dev/null\n+++ b/n.go\n" {
		t.Fatalf("an added file diffs from /dev/null: %q", got)
	}
	if got := gdFilePatch(&gdFile{cf: &gitworkbench.ChangeFile{Path: "bin"}}); got != "" {
		t.Fatalf("a file without hunks has no patch, got %q", got)
	}
}
