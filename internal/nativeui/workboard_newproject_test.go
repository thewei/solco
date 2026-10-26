package nativeui

import (
	"context"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/wh-studio/herdr-client/internal/workboard"
)

// newProjectShell is a supported shell whose plugin registers a folder the way
// the real one does: a merged-event automation that names the Project, removed
// again at once.
func newProjectShell(t *testing.T, ran *[]string, failCoordinator bool) *Shell {
	t.Helper()
	s := writeShell(t, ran)
	newWorkboardClient = func(string, string) *workboard.Client {
		var autoName string
		return workboard.NewClientWithExec(func(_ context.Context, args ...string) (string, error) {
			switch {
			case args[0] == "list":
				return `[]`, nil
			case args[0] == "automations" && len(args) == 2:
				return `[{"id":"` + autoName + `","name":"` + autoName + `","repo":"My.Proj"}]`, nil
			}
			if args[0] == "automations" && args[1] == "add" {
				autoName = args[2]
				*ran = append(*ran, "automations add … --repo "+args[len(args)-1])
				return "", nil
			}
			*ran = append(*ran, strings.Join(args, " "))
			if args[0] == "coordinator" && failCoordinator {
				return "", context.DeadlineExceeded
			}
			return "", nil
		})
	}
	return s
}

func TestNewProjectRegistersPinsAndStartsTheCoordinatorWithoutATask(t *testing.T) {
	var ran []string
	s := newProjectShell(t, &ran, false)
	s.workboard.newProject = newProjectDraft{open: true, folder: "/tmp/My.Proj", coordinator: true}
	s.submitNewProject()
	got := strings.TrimSuffix(strings.Join(ran, "|"), "|autopilot --json") // the refresh after a write
	if strings.Contains(got, "|add ") || strings.HasPrefix(got, "add ") {
		t.Fatalf("a Project starts with no task: %q", got)
	}
	if !strings.HasPrefix(got, "automations add … --repo /tmp/My.Proj|automations remove shardlane-new-project-") ||
		!strings.HasSuffix(got, "|pin My.Proj|coordinator --project My.Proj restart") {
		t.Fatalf("ran = %q", got)
	}
	if s.workboard.newProject.open || s.pendingToast != "Added project My.Proj" {
		t.Fatalf("draft=%+v toast=%q", s.workboard.newProject, s.pendingToast)
	}
}

func TestNewProjectWithoutCoordinatorStopsAfterThePin(t *testing.T) {
	var ran []string
	s := newProjectShell(t, &ran, false)
	s.workboard.newProject = newProjectDraft{open: true, folder: "/tmp/My.Proj"}
	s.submitNewProject()
	if got := strings.TrimSuffix(strings.Join(ran, "|"), "|autopilot --json"); strings.Contains(got, "coordinator") || !strings.HasSuffix(got, "pin My.Proj") {
		t.Fatalf("ran = %q", got)
	}
}

func TestNewProjectKeepsTheProjectWhenTheCoordinatorWillNotStart(t *testing.T) {
	var ran []string
	s := newProjectShell(t, &ran, true)
	s.workboard.newProject = newProjectDraft{open: true, folder: "/tmp/My.Proj", coordinator: true}
	s.submitNewProject()
	if !strings.HasPrefix(s.pendingToast, "Added project My.Proj; its coordinator didn't start") {
		t.Fatalf("toast = %q", s.pendingToast)
	}
}

func TestNewProjectNeedsAnAbsoluteFolder(t *testing.T) {
	var ran []string
	s := newProjectShell(t, &ran, false)
	s.workboard.newProject = newProjectDraft{folder: "relative/dir"}
	s.submitNewProject()
	if strings.Contains(strings.Join(ran, "|"), "automations") || s.pendingToast == "" {
		t.Fatalf("ran %q toast %q: must write nothing and say why", ran, s.pendingToast)
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

func TestNewProjectDialogAsksForTheFolderOnly(t *testing.T) {
	var ran []string
	s := newProjectShell(t, &ran, false)
	s.workboard.newProject = newProjectDraft{open: true, folder: "/tmp/My.Proj", coordinator: true}
	tt := ui.NewTester(s.newProjectDialog, 900, 700)
	for _, want := range []string{"New Project", "My.Proj", "/tmp/My.Proj", "Start its coordinator now", "Create", "Cancel"} {
		if !tt.HasText(want) {
			t.Fatalf("dialog missing %q: %q", want, tt.Texts())
		}
	}
	if tt.HasText("New Task") || tt.HasText("First task") {
		t.Fatalf("a Project dialog must not ask for a task: %q", tt.Texts())
	}
}

func TestProjectsSectionMenuLeadsWithNewProject(t *testing.T) {
	s := projectsSectionShell(t)
	tt := ui.NewTester(s.View, 1200, 800)
	if !tt.HasText("Projects") {
		t.Fatalf("section missing: %q", tt.Texts())
	}
	if err := tt.RightClick("Projects"); err != nil {
		t.Fatal(err)
	}
	menu := strings.Join(tt.Menu(), "|")
	if !strings.Contains(menu, "New Project…") || !strings.Contains(menu, "New Task") {
		t.Fatalf("section menu = %q", menu)
	}
}
