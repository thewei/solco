package chatsession

import (
	"testing"

	"github.com/thewei/solco/internal/conversation"
	"github.com/thewei/solco/internal/gitworkbench"
)

func editStep(name, preview string) conversation.TimelineStep {
	return conversation.TimelineStep{Kind: conversation.StepTool, Tool: &conversation.ConversationToolCall{Name: name, InputPreview: preview}}
}

func TestChatTurnChangesNamesAndCounts(t *testing.T) {
	snap := &gitworkbench.ChangesSnapshot{Root: "/repo", Files: []gitworkbench.ChangeFile{
		{Path: "internal/a.go", Additions: 24, Deletions: 6},
	}}
	steps := []conversation.TimelineStep{
		editStep("Read", "/repo/internal/a.go"),
		editStep("Edit", "/repo/internal/a.go"),
		editStep("Write", "/repo/internal/b_test.go"),
		editStep("Edit", "/repo/internal/a.go"), // duplicate: one row
		editStep("TodoWrite", "plan the work"),  // never an edit
		editStep("Edit", "/elsewhere/c.go"),     // outside the repo: name alone
		editStep("apply_patch", "*** Begin Patch\n*** Update File: x"),
	}

	got := TurnChanges(steps, "/repo", snap, true)
	want := []ChatChange{
		{Path: "internal/a.go", Additions: 24, Deletions: 6, Counted: true},
		{Path: "internal/b_test.go"},
		{Path: "/elsewhere/c.go"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("row %d: got %+v, want %+v", i, got[i], want[i])
		}
	}

	// An older turn (or a read-only/remote pane) never carries counts.
	for _, change := range TurnChanges(steps, "/repo", snap, false) {
		if change.Counted {
			t.Fatalf("older turn must not be counted: %+v", change)
		}
	}
	if got := TurnChanges(steps[:1], "/repo", snap, true); len(got) != 0 {
		t.Fatalf("a read is not a change: %+v", got)
	}
}

// omp's hashline edit has no path key: its one-line preview is the arguments'
// JSON, clipped. The card still finds the file.
func TestChatTurnChangesSniffsJSONPreview(t *testing.T) {
	preview := `{ "i": "Making tab row wear the pane agent brand", "input": "[internal/nativeui/project_tree.go#9F0A\n- old\n+ new` + "\"}"
	got := TurnChanges([]conversation.TimelineStep{editStep("edit", preview)}, "/repo", nil, false)
	if len(got) != 1 || got[0].Path != "internal/nativeui/project_tree.go" {
		t.Fatalf("got %+v", got)
	}
	if got := TurnChanges([]conversation.TimelineStep{editStep("edit", `{ "i": "no path here" }`)}, "/repo", nil, false); len(got) != 0 {
		t.Fatalf("a pathless JSON preview must yield no row: %+v", got)
	}
}

func inputStep(name, input string) conversation.TimelineStep {
	return conversation.TimelineStep{Kind: conversation.StepTool, Tool: &conversation.ConversationToolCall{Name: name, Input: &input}}
}

// Codex's apply_patch names its files in the patch headers; a multi-file
// patch is several rows, a move names its destination too.
func TestChatTurnChangesReadsApplyPatchHeaders(t *testing.T) {
	patch := "*** Begin Patch\n*** Update File: /repo/a.go\n@@\n-x\n+y\n*** Add File: b.go\n+z\n*** Delete File: old.go\n*** Update File: c.go\n*** Move to: d.go\n*** End Patch"
	got := TurnChanges([]conversation.TimelineStep{inputStep("apply_patch", patch)}, "/repo", nil, false)
	var paths []string
	for _, change := range got {
		paths = append(paths, change.Path)
	}
	want := []string{"a.go", "b.go", "old.go", "c.go", "d.go"}
	if len(paths) != len(want) {
		t.Fatalf("got %v, want %v", paths, want)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Fatalf("got %v, want %v", paths, want)
		}
	}
}

// omp's hashline edit carries "[path#HASH]" headers inside the arguments'
// JSON; a relative path with no directory still counts.
func TestChatTurnChangesReadsHashlineHeaders(t *testing.T) {
	input := `{"i":"Adding helpers","input":"[calc.py#D111]\nPUT >2:\n+def f(): pass"}`
	got := TurnChanges([]conversation.TimelineStep{inputStep("edit", input)}, "/repo", nil, false)
	if len(got) != 1 || got[0].Path != "calc.py" {
		t.Fatalf("got %+v", got)
	}
	clipped := `{"i":"Adding helpers","input":"[pkg/calc.py#D111]\nPUT >2:\n+def f(`
	got = TurnChanges([]conversation.TimelineStep{inputStep("edit", clipped)}, "/repo", nil, false)
	if len(got) != 1 || got[0].Path != "pkg/calc.py" {
		t.Fatalf("clipped input: got %+v", got)
	}
}

// Cursor's and Antigravity's edit tools read as edits; their shell tools do not.
func TestTurnChangesRecognisesCursorAndAntigravityEdits(t *testing.T) {
	call := func(name, preview string) conversation.TimelineStep {
		return conversation.TimelineStep{Kind: conversation.StepTool, Tool: &conversation.ConversationToolCall{Name: name, InputPreview: preview}}
	}
	steps := []conversation.TimelineStep{
		call("StrReplace", "/repo/a.go"),
		call("Write", "/repo/b.go"),
		call("Delete", "/repo/c.go"),
		call("edit_file", "/repo/d.go"),
		call("write_file", "/repo/e.go"),
		call("Shell", "/repo/f.go"),
		call("run_command", "/repo/g.go"),
	}
	got := TurnChanges(steps, "/repo", nil, false)
	var paths []string
	for _, change := range got {
		paths = append(paths, change.Path)
	}
	want := []string{"a.go", "b.go", "c.go", "d.go", "e.go"}
	if len(paths) != len(want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Fatalf("paths = %v, want %v", paths, want)
		}
	}
}
