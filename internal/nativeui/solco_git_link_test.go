package nativeui

/**
 * [INPUT]: solco_git_link.go, a fake solcoGitLink, a real temp Git repository with changes (sidePanelRepoShell), the headless ui.Tester, the Command Center registry
 * [OUTPUT]: the Solco half of the link — the three entries (panel header, project row menu, Command Center) draw only once Solco Git is found, each hands over the right repository / view / file, a refusal reaches the status line, and the log never carries what was opened
 * [POS]: 2026-11-07 决定「Solco Git and the right panel」第 2、5 条的回归防线
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"bytes"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thewei/solco/internal/commandcenter"
	"github.com/thewei/solco/internal/solcogit"
)

type solcoGitCall struct {
	repo string
	view solcogit.View
	file string
}

type fakeSolcoGit struct {
	available bool
	err       error
	calls     []solcoGitCall
}

func (f *fakeSolcoGit) Available() bool { return f.available }

func (f *fakeSolcoGit) Open(repo string, view solcogit.View, file string) error {
	f.calls = append(f.calls, solcoGitCall{repo, view, file})
	return f.err
}

// Hidden until Solco Git is found: a shell without a link (every headless
// test) and one whose link finds nothing draw none of the three entries; once
// found they all appear.
func TestSolcoGitEntriesStayHiddenUntilFound(t *testing.T) {
	s := sidePanelRepoShell(t)
	tt := sidePanelTester(s, 1280, 800)
	s.toggleSidePanel()
	tt.Frame()

	check := func(label string, want bool) {
		t.Helper()
		tt.Frame()
		if _, ok := tt.Access("Open in Solco Git"); ok != want {
			t.Errorf("%s: the panel button present = %v, want %v", label, ok, want)
		}
		if _, ok := commandCenterAction(s, "app.open-solco-git"); ok != want {
			t.Errorf("%s: the Command Center entry present = %v, want %v", label, ok, want)
		}
		if err := tt.RightClick("demo"); err != nil {
			t.Fatalf("%s: no project row to right-click: %v", label, err)
		}
		menu := strings.Join(tt.Menu(), "\n")
		tt.CloseMenu()
		if got := strings.Contains(menu, "Open in Solco Git"); got != want {
			t.Errorf("%s: the project menu entry present = %v, want %v\n%s", label, got, want, menu)
		}
		for _, keep := range []string{"New Tab", "Rename Project…", "Close Project…"} {
			if !strings.Contains(menu, keep) {
				t.Errorf("%s: the project menu lost %q", label, keep)
			}
		}
	}

	s.solcoGit = nil
	check("no link", false)
	s.solcoGit = &fakeSolcoGit{available: false}
	check("not installed", false)
	s.solcoGit = &fakeSolcoGit{available: true}
	check("installed", true)
}

// The panel's button takes the page the panel shows and the file selected there.
func TestSolcoGitPanelButtonCarriesThePageAndTheSelectedFile(t *testing.T) {
	s := sidePanelRepoShell(t)
	link := &fakeSolcoGit{available: true}
	s.solcoGit = link
	tt := sidePanelTester(s, 1280, 800)
	root := s.git.root

	s.toggleSidePanel() // the worktree has changes: the first page is Changes
	tt.Frame()
	if s.sidePanelPageNow() != sidePanelChanges {
		t.Fatalf("page = %q, want changes", s.sidePanelPageNow())
	}
	if err := tt.Click("Open in Solco Git"); err != nil {
		t.Fatal(err)
	}
	s.surface.diff.SelectedPath = "README.md"
	tt.Frame()
	if err := tt.Click("Open in Solco Git"); err != nil {
		t.Fatal(err)
	}

	s.setSidePanelPage(sidePanelFiles)
	tt.Frame()
	s.rightPanel.selectedFilePath = filepath.Join(root, "notes.txt")
	tt.Frame()
	if err := tt.Click("Open in Solco Git"); err != nil {
		t.Fatal(err)
	}
	s.rightPanel.selectedFilePath = filepath.Join(filepath.Dir(root), "elsewhere.txt") // outside the repository
	tt.Frame()
	if err := tt.Click("Open in Solco Git"); err != nil {
		t.Fatal(err)
	}
	s.rightPanel.selectedFilePath = ""
	tt.Frame()
	if err := tt.Click("Open in Solco Git"); err != nil {
		t.Fatal(err)
	}

	want := []solcoGitCall{
		{root, solcogit.Changes, ""},          // nothing selected yet
		{root, solcogit.Changes, "README.md"}, // the file the Diff shows
		{root, solcogit.Files, "notes.txt"},   // the file the Files panel previews, repository-relative
		{root, solcogit.Files, ""},            // a file outside the repository is not sent
		{root, solcogit.Files, ""},            // none selected
	}
	if len(link.calls) != len(want) {
		t.Fatalf("calls = %+v, want %d", link.calls, len(want))
	}
	for i, call := range link.calls {
		if call != want[i] {
			t.Errorf("call %d = %+v, want %+v", i, call, want[i])
		}
	}
}

// The palette acts on the selected Tab's repository, always on Changes; the
// project menu acts on the row that was clicked.
func TestSolcoGitPaletteAndProjectMenuHandTheRepositoryOver(t *testing.T) {
	s := sidePanelRepoShell(t)
	link := &fakeSolcoGit{available: true}
	s.solcoGit = link
	tt := sidePanelTester(s, 1280, 800)
	root := s.git.root

	action, ok := commandCenterAction(s, "app.open-solco-git")
	if !ok || !action.Available || action.Section != "App" || action.Title != "Open in Solco Git" {
		t.Fatalf("palette entry = %+v, present %v", action, ok)
	}
	s.executeCommandCenterResult(commandcenter.Result{Action: action})
	if len(link.calls) != 1 || link.calls[0] != (solcoGitCall{root, solcogit.Changes, ""}) {
		t.Fatalf("palette call = %+v", link.calls)
	}

	if err := tt.RightClick("demo"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Open in Solco Git"); err != nil {
		t.Fatal(err)
	}
	if len(link.calls) != 2 || link.calls[1].view != solcogit.Changes || link.calls[1].file != "" {
		t.Fatalf("menu call = %+v", link.calls)
	}
	if got := link.calls[1].repo; got != root && got != s.projection.Projects[0].CWD {
		t.Errorf("menu repository = %q, want the clicked row's project %q", got, s.projection.Projects[0].CWD)
	}
}

// A refusal shows in the status line; the log names the operation and the
// outcome only — never the repository, the file or the link.
func TestSolcoGitRefusalReachesTheStatusAndTheLogStaysClean(t *testing.T) {
	var logged bytes.Buffer
	previous := slog.Default()
	// Without the time attribute: its digits must not look like a path component.
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	s := sidePanelRepoShell(t)
	link := &fakeSolcoGit{available: true, err: errors.New("exit status 1")}
	s.solcoGit = link
	root := s.git.root

	s.openInSolcoGit(root, solcogit.Files, "secret/plan.md")
	if s.status != "Could not open Solco Git" {
		t.Errorf("status = %q", s.status)
	}
	link.err = nil
	s.status = ""
	s.openInSolcoGit(root, solcogit.Changes, "secret/plan.md")
	if s.status != "" {
		t.Errorf("a success must not touch the status line: %q", s.status)
	}
	out := logged.String()
	if !strings.Contains(out, "open in solco git") {
		t.Errorf("the operation is not logged: %q", out)
	}
	for _, leak := range []string{root, filepath.Base(root), "secret", "plan.md", "solcogit://"} {
		if strings.Contains(out, leak) {
			t.Errorf("the log carries %q: %s", leak, out)
		}
	}

	s.openInSolcoGit("", solcogit.Changes, "")
	if s.status != "Select a workspace to open in Solco Git" || len(link.calls) != 2 {
		t.Errorf("no repository: status %q, calls %d", s.status, len(link.calls))
	}
}
