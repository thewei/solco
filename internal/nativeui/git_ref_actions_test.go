package nativeui

import (
	"strings"
	"testing"

	"github.com/thewei/solco/internal/gitworkbench"
)

func TestStashTitleDropsGitFraming(t *testing.T) {
	for in, want := range map[string]string{
		"On main: parked work":            "parked work",
		"WIP on main: 1a2b3c4 first line": "1a2b3c4 first line",
		"WIP parser":                      "WIP parser",
		"On feature/x: a: b":              "a: b",
	} {
		if got := stashTitle(in); got != want {
			t.Errorf("stashTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUpstreamBadge(t *testing.T) {
	for _, c := range []struct {
		st   gitworkbench.UpstreamStatus
		want string
	}{
		{gitworkbench.UpstreamStatus{}, ""},
		{gitworkbench.UpstreamStatus{OK: true}, ""},
		{gitworkbench.UpstreamStatus{OK: true, Ahead: 1}, "↑1"},
		{gitworkbench.UpstreamStatus{OK: true, Behind: 2}, "↓2"},
		{gitworkbench.UpstreamStatus{OK: true, Ahead: 1, Behind: 1}, "↑1 ↓1"},
	} {
		if got := upstreamBadge(c.st); got != c.want {
			t.Errorf("upstreamBadge(%+v) = %q, want %q", c.st, got, c.want)
		}
	}
}

func TestRefRowMenusCarryTheForkActions(t *testing.T) {
	for _, c := range []struct {
		row   string
		items []string
	}{
		{"login", []string{"Rebase Current Onto This…", "Rename Branch…"}},
		{"develop (current)", []string{"Pull (Fast-Forward Only)", "Push", "Rename Branch…"}},
		{"v1.0", []string{"Push Tag"}},
		{"WIP parser", []string{"Apply Stash…", "Rename Stash…", "Delete Stash…", "Save as Patch…"}},
	} {
		shell, tester, _ := treeTestShell(t)
		shell.git.remoteBranches = []gitworkbench.RemoteBranch{{Remote: "origin", Name: "main"}}
		shell.git.branches[0].Current, shell.git.branches[1].Current = false, c.row == "develop (current)"
		shell.git.upstream = gitworkbench.UpstreamStatus{OK: true, Remote: "origin", Branch: "main"}
		tester.Frame()
		if err := tester.RightClick(strings.TrimSuffix(c.row, " (current)")); err != nil {
			t.Fatalf("%s: %v", c.row, err)
		}
		tester.Frame()
		menu := strings.Join(tester.Menu(), "|")
		for _, item := range c.items {
			if !strings.Contains(menu, item) {
				t.Errorf("%s menu = %q, missing %q", c.row, menu, item)
			}
		}
	}
}

func TestRenameBranchDialogOpensForTheMenu(t *testing.T) {
	shell, tester, root := treeTestShell(t)
	if err := tester.RightClick("develop"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if err := tester.ChooseMenuItem("Rename Branch…"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if !shell.dialogOpen || shell.dialogKind != "git-rename-branch" || shell.dialogTarget != root+"\x00develop" || shell.dialogAction != "Rename" {
		t.Fatalf("dialog open %v kind %q target %q action %q", shell.dialogOpen, shell.dialogKind, shell.dialogTarget, shell.dialogAction)
	}
}
