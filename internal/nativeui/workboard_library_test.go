package nativeui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/projectlibrary"
	"github.com/thewei/solco/internal/workboard"
)

// libShell is a project-page shell whose plugin CLI is faked: `show` answers
// from own (a setting's own value per scope key), every other write is logged.
func libShell(t *testing.T, ran *[]string, own map[string]string) *Shell {
	t.Helper()
	tasks, err := workboard.ParseTasks(boardList)
	if err != nil {
		t.Fatal(err)
	}
	newWorkboardClient = func(string, string) *workboard.Client {
		return workboard.NewClientWithExec(func(_ context.Context, args ...string) (string, error) {
			switch {
			case args[0] == "show":
				key := "everywhere"
				if len(args) > 3 {
					key = args[2] + ":" + args[3]
				}
				var rows []string
				for _, name := range []string{"worker.instructions", "reviewer.instructions", "coordinator.instructions"} {
					if v, ok := own[key+"/"+name]; ok {
						rows = append(rows, `{"name":"`+name+`","kind":"work","value":`+jsonString(v)+`,"from":"you","place":"repo","own":true}`)
					} else {
						rows = append(rows, `{"name":"`+name+`","kind":"work","value":"none","from":"built in","place":"builtin","own":false}`)
					}
				}
				return `{"settings":[` + strings.Join(rows, ",") + `],"problems":[]}`, nil
			case args[0] == "details":
				return `{"task":{"id":"` + args[1] + `"}}`, nil
			}
			*ran = append(*ran, strings.Join(args, " "))
			return "", nil
		})
	}
	workboardGo = func(f func()) { f() }
	t.Cleanup(func() { newWorkboardClient, workboardGo = workboard.NewClient, func(f func()) { go f() } })
	s := supportedWorkboardShell()
	s.workboard.board = workboardBoard{loadedAt: timeNow(), tasks: tasks, details: map[string]workboardDetail{}}
	s.workboard.library.store = projectlibrary.New(t.TempDir())
	s.openWorkboardProjectPage("r")
	return s
}

func jsonString(v string) string {
	out := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(v)
	return `"` + out + `"`
}

func TestSyncLibraryBlockSplicesAndKeepsTheUsersWords(t *testing.T) {
	var ran []string
	s := libShell(t, &ran, map[string]string{
		"--repo:r/worker.instructions": "my own rules",
	})
	// Add one Always item: every target gets the block; the user's value keeps its words.
	items := s.addLibraryPaths("r", projectlibrary.Always, []string{writeTemp(t, "brief.md", "# Brief\nship it")})
	if len(items) != 1 {
		t.Fatalf("items = %+v, toast %q", items, s.pendingToast)
	}
	joined := strings.Join(ran, "\n")
	for _, want := range []string{"set worker.instructions", "set reviewer.instructions", "set coordinator.instructions", "--project-coordinator r", "my own rules", projectlibrary.BlockBegin} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in\n%s", want, joined)
		}
	}
	if !strings.Contains(s.workboard.library.note, "Delivered") {
		t.Fatalf("note = %q", s.workboard.library.note)
	}
}

func TestSyncLibraryBlockRemovesAnEmptyBlockAndUnsetsAnEmptyValue(t *testing.T) {
	block := projectlibrary.BlockBegin + "\nx\n" + projectlibrary.BlockEnd
	var ran []string
	s := libShell(t, &ran, map[string]string{
		"--repo:r/worker.instructions":   block,
		"--repo:r/reviewer.instructions": "keep me\n\n" + block,
	})
	client := newWorkboardClient("", "")
	if err := syncLibraryBlock(context.Background(), client, "r", ""); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(ran, "\n")
	if !strings.Contains(joined, "unset worker.instructions") {
		t.Fatalf("a value that is only the block must be unset:\n%s", joined)
	}
	if !strings.Contains(joined, "set reviewer.instructions keep me") || strings.Contains(joined, projectlibrary.BlockBegin) {
		t.Fatalf("the user's words must stay and the block go:\n%s", joined)
	}
	_ = s
}

func TestLibraryViewListsChangesModeAndRemoves(t *testing.T) {
	var ran []string
	s := libShell(t, &ran, nil)
	s.addLibraryPaths("r", projectlibrary.Reference, []string{writeTemp(t, "spec.md", "# Spec")})
	lib := s.projectsView().Project.Library
	if !lib.Available || len(lib.Items) != 1 || lib.Items[0].Name != "spec.md" || lib.Items[0].Mode != "reference" {
		t.Fatalf("library = %+v", lib)
	}
	b := NewProjectsBridge(s)
	id := lib.Items[0].ID
	b.SetLibraryMode(id, "always")
	if got := s.workboard.library.items[0].Mode; got != projectlibrary.Always {
		t.Fatalf("mode = %q", got)
	}
	if got := s.projectsView().Project.Library; got.AlwaysUsed == 0 || got.AlwaysCap != projectlibrary.MaxAlwaysBytes || got.Items[0].Mode != "always" {
		t.Fatalf("always budget = %+v", got)
	}
	b.RemoveLibraryItem(id)
	if len(s.workboard.library.items) != 0 || len(s.projectsView().Project.Library.Items) != 0 {
		t.Fatalf("items = %+v", s.workboard.library.items)
	}
}

func TestReportsViewOpensAReport(t *testing.T) {
	var ran []string
	s := libShell(t, &ran, nil)
	s.workboard.board.details["r/5"] = workboardDetail{detail: workboard.Detail{ReportLink: "http://127.0.0.1:1/r/5"}}
	var opened string
	previous := openBoardURL
	openBoardURL = func(u string) error { opened = u; return nil }
	t.Cleanup(func() { openBoardURL = previous })
	rows := s.projectsView().Project.Reports.Rows
	if len(rows) != 1 || rows[0].TaskID != "r/5" || rows[0].Local {
		t.Fatalf("reports = %+v", rows)
	}
	NewProjectsBridge(s).OpenReport(rows[0].Link)
	if opened != "http://127.0.0.1:1/r/5" {
		t.Fatalf("opened = %q", opened)
	}
}

func TestStopAllStopsOnlyWorkingTasksAfterConfirm(t *testing.T) {
	var ran []string
	s := libShell(t, &ran, nil)
	project, _ := s.workboardProjectByName("r")
	if n := len(s.projectWorkingTasks(project)); n != 1 {
		t.Fatalf("working = %d", n)
	}
	s.openConfirm(workboardStopAllConfirm, "r", "t", "m")
	s.submitConfirm(s.confirmKind, s.confirmTarget)
	got := strings.Join(ran, "|")
	if !strings.Contains(got, "stop r/3") || strings.Contains(got, "stop r/1") || strings.Contains(got, "stop r/2") {
		t.Fatalf("ran = %q", got)
	}
}

func TestSaveDescriptionDescribesAndTellsTheAgent(t *testing.T) {
	var ran []string
	s := libShell(t, &ran, nil)
	// r/3 is working with an agent pane in its worktree.
	s.workboard.board.details["r/3"] = workboardDetail{detail: workboard.Detail{Worktree: "/wt/r3"}}
	s.projection = herdr.Projection{
		Panes:  []herdr.Pane{{ID: "p3", TerminalID: "t3", CWD: "/wt/r3", Agent: "claude"}},
		Agents: []herdr.Agent{{PaneID: "p3", TerminalID: "t3", Name: "claude"}},
	}
	NewProjectsBridge(s).SaveDescription("r/3", "new words", true)
	got := strings.Join(ran, "|")
	if !strings.Contains(got, "describe r/3 new words") || !strings.Contains(got, "tell r/3 The task description changed") {
		t.Fatalf("ran = %q", got)
	}
	ran = nil
	NewProjectsBridge(s).SaveDescription("r/3", "again", false)
	if got := strings.Join(ran, "|"); !strings.Contains(got, "describe r/3 again") || strings.Contains(got, "tell") {
		t.Fatalf("without the tick the agent is not told: %q", got)
	}
}

func TestNewTaskAttachesLibraryFilesAsPathLines(t *testing.T) {
	var ran []string
	s := libShell(t, &ran, nil)
	items := s.addLibraryPaths("r", projectlibrary.Task, []string{writeTemp(t, "ctx.md", "# ctx")})
	if len(items) != 1 {
		t.Fatalf("items = %+v", items)
	}
	ran = nil
	s.openWorkboardAddIn("r", false)
	c := s.projectsView().Compose
	if c == nil || c.Project != "r" || !c.CanAttach || len(c.Items) != 1 || c.Items[0].Name != "ctx.md" {
		t.Fatalf("the sheet must offer r's library: %+v", c)
	}
	NewProjectsBridge(s).AddTask(ComposeInput{Title: "Use it", Description: "Read the context", Kind: "task", Attach: []string{items[0].ID}})
	got := strings.Join(ran, "|")
	if !strings.Contains(got, "Read the context") || !strings.Contains(got, "Context files:") || !strings.Contains(got, "ctx.md") {
		t.Fatalf("ran = %q", got)
	}
}

func TestLibraryBriefSortsItems(t *testing.T) {
	items := []projectlibrary.Item{
		{ID: "a", Name: "a.md", Mode: projectlibrary.Always},
		{ID: "b", Name: "b.md", Mode: projectlibrary.Reference},
		{ID: "c", Name: "c.md", Mode: projectlibrary.Task},
	}
	b := libraryBrief(items, map[string]bool{"c": true})
	if len(b.Always) != 1 || b.OnDemand != 1 || len(b.Attached) != 1 || b.Attached[0] != "c.md" {
		t.Fatalf("brief = %+v", b)
	}
}

func TestTaskViewCarriesContextChangesAndSettings(t *testing.T) {
	var ran []string
	s := libShell(t, &ran, nil)
	s.workboard.board.details["r/3"] = workboardDetail{detail: workboard.Detail{Description: "Do the thing"}}
	s.openWorkboardTaskPage("r/3")
	s.workboard.page.detail = &workboard.Detail{Description: "Do the thing"}
	task := s.projectsView().Task
	if task.Context.Description != "Do the thing" || task.Context.Full != "Do the thing" || !task.Context.CanEdit || !task.Context.Live {
		t.Fatalf("context = %+v", task.Context)
	}
	if task.Changes.Loaded || task.Settings.Loaded {
		t.Fatal("tabs read on demand, not by the snapshot")
	}
	NewProjectsBridge(s).LoadTab("settings")
	if !s.projectsView().Task.Settings.Loaded {
		t.Fatalf("LoadTab(settings) reads the task's settings: %+v", s.workboard.page)
	}
}

func writeTemp(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
