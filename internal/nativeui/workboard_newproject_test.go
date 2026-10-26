package nativeui

import (
	"context"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/wh-studio/herdr-client/internal/workboard"
)

// newProjectShell is a supported shell whose plugin answers `add` the way the
// real one does ("KEY-n: added") and names the Project from `details`.
func newProjectShell(t *testing.T, ran *[]string, failCoordinator bool) *Shell {
	t.Helper()
	s := writeShell(t, ran)
	newWorkboardClient = func(string, string) *workboard.Client {
		return workboard.NewClientWithExec(func(_ context.Context, args ...string) (string, error) {
			switch args[0] {
			case "list", "automations", "autopilot":
				return `[]`, nil
			case "details":
				*ran = append(*ran, strings.Join(args, " "))
				return `{"task":{"id":"My.Proj/1"}}`, nil
			}
			*ran = append(*ran, strings.Join(args, " "))
			switch {
			case args[0] == "add":
				return "PRJ-1: added\n", nil
			case args[0] == "coordinator" && failCoordinator:
				return "", context.DeadlineExceeded
			}
			return "", nil
		})
	}
	return s
}

func TestNewProjectRegistersPinsAndStartsTheCoordinator(t *testing.T) {
	var ran []string
	s := newProjectShell(t, &ran, false)
	s.workboard.newProject = newProjectDraft{open: true, folder: "/tmp/My.Proj", title: "First thing", coordinator: true}
	s.submitNewProject()
	want := "add First thing --repo /tmp/My.Proj|details PRJ-1 --json|pin My.Proj|coordinator --project My.Proj restart"
	if got := strings.Join(ran, "|"); got != want {
		t.Fatalf("ran = %q, want %q", got, want)
	}
	if s.workboard.newProject.open || s.pendingToast != "Added project My.Proj" {
		t.Fatalf("draft=%+v toast=%q", s.workboard.newProject, s.pendingToast)
	}
}

func TestNewProjectWithoutCoordinatorStopsAfterThePin(t *testing.T) {
	var ran []string
	s := newProjectShell(t, &ran, false)
	s.workboard.newProject = newProjectDraft{open: true, folder: "/tmp/My.Proj", title: "First thing"}
	s.submitNewProject()
	if got := strings.Join(ran, "|"); strings.Contains(got, "coordinator") || !strings.HasSuffix(got, "pin My.Proj") {
		t.Fatalf("ran = %q", got)
	}
}

func TestNewProjectKeepsTheProjectWhenTheCoordinatorWillNotStart(t *testing.T) {
	var ran []string
	s := newProjectShell(t, &ran, true)
	s.workboard.newProject = newProjectDraft{open: true, folder: "/tmp/My.Proj", title: "First thing", coordinator: true}
	s.submitNewProject()
	if !strings.HasPrefix(s.pendingToast, "Added project My.Proj; its coordinator didn't start") {
		t.Fatalf("toast = %q", s.pendingToast)
	}
}

func TestNewProjectNeedsAFolderThePluginCanAddTo(t *testing.T) {
	var ran []string
	s := newProjectShell(t, &ran, false)
	for _, d := range []newProjectDraft{
		{folder: "relative/dir", title: "x"}, {folder: "/tmp/p", title: "-x"}, {folder: "/tmp/p", title: "  "},
	} {
		s.workboard.newProject = d
		s.submitNewProject()
		if len(ran) != 0 || s.pendingToast == "" {
			t.Fatalf("draft %+v ran %q toast %q: must write nothing and say why", d, ran, s.pendingToast)
		}
		s.pendingToast = ""
	}
}

func TestNewProjectPicksTheFolderThenOpensTheDialog(t *testing.T) {
	var ran []string
	s := newProjectShell(t, &ran, false)
	old := pickProjectFolder
	pickProjectFolder = func() (string, error) { return "/tmp/My.Proj", nil }
	t.Cleanup(func() { pickProjectFolder = old })
	done := make(chan struct{})
	s.uiApplyOverride = func(fn func()) { fn(); close(done) }
	s.openNewProject()
	<-done
	if d := s.workboard.newProject; !d.open || d.folder != "/tmp/My.Proj" || !d.coordinator {
		t.Fatalf("draft = %+v", d)
	}
}

func TestNewProjectDialogShowsTheFolderAndTheFirstTask(t *testing.T) {
	var ran []string
	s := newProjectShell(t, &ran, false)
	s.workboard.newProject = newProjectDraft{open: true, folder: "/tmp/My.Proj", coordinator: true}
	tt := ui.NewTester(s.newProjectDialog, 900, 700)
	for _, want := range []string{"New Project", "My.Proj", "/tmp/My.Proj", "Start its coordinator now", "Create", "Cancel"} {
		if !tt.HasText(want) {
			t.Fatalf("dialog missing %q: %q", want, tt.Texts())
		}
	}
	if err := tt.Click("Create"); err != nil || len(ran) != 0 {
		t.Fatalf("an empty first task must not create anything: err=%v ran=%q", err, ran)
	}
}
