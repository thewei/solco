package personalagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSkill(t *testing.T, root, name string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+name+"\ndescription: d\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func skillTask(t *testing.T, s *Store, skills ...string) (Assistant, Task) {
	t.Helper()
	a := mustAssistant(t, s, "sk-a", "Ada")
	task, _, err := s.CreateTask("sk-t", CreateTaskInput{AssistantID: a.ID, Kind: KindResearch, Title: "Digest", Brief: "read x.com", RequiredSkills: skills})
	if err != nil {
		t.Fatal(err)
	}
	return a, task
}

// Gap W04: OMP discovers skills in ~/.omp/agent/skills and ~/.agents/skills;
// Solco must see exactly what OMP sees, never a skill it cannot load.
func TestGapW04DiscoverSkillsReadsTheFoldersOMPReads(t *testing.T) {
	home := t.TempDir()
	writeSkill(t, filepath.Join(home, ".agents", "skills"), "ego-browser")
	writeSkill(t, filepath.Join(home, ".omp", "agent", "skills"), "fixture")
	_ = os.MkdirAll(filepath.Join(home, ".agents", "skills", "empty-dir"), 0o755) // no SKILL.md
	real := t.TempDir()
	writeSkill(t, real, "linked")
	_ = os.Symlink(filepath.Join(real, "linked"), filepath.Join(home, ".agents", "skills", "linked"))

	got := DiscoverSkills(home)
	for _, want := range []string{"ego-browser", "fixture", "linked"} {
		if _, ok := got[want]; !ok {
			t.Errorf("skill %q not discovered: %v", want, got)
		}
	}
	if _, ok := got["empty-dir"]; ok {
		t.Error("a folder without SKILL.md is not a skill")
	}
}

func TestGapW04RequiredSkillsAreValidatedAndStored(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "sk-a", "Ada")
	for _, bad := range [][]string{{"../etc"}, {"Has Space"}, {""}, {"a", "b", "c", "d", "e", "f", "g", "h", "i"}} {
		if _, _, err := s.CreateTask("bad-"+strings.Join(bad, ""), CreateTaskInput{AssistantID: a.ID, Kind: KindResearch, Title: "t", Brief: "b", RequiredSkills: bad}); err == nil {
			t.Errorf("skills %v must be refused", bad)
		}
	}
	task, _, err := s.CreateTask("ok", CreateTaskInput{AssistantID: a.ID, Kind: KindResearch, Title: "t", Brief: "b", RequiredSkills: []string{"ego-browser", "ego-browser", "zeta"}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.TaskSkills(task.ID)
	if err != nil || strings.Join(got, ",") != "ego-browser,zeta" {
		t.Fatalf("skills = %v err=%v", got, err)
	}
}

// W04/AC-09: a skill that is not installed stops the run before any pane is
// opened. No other tool stands in for it.
func TestGapW04MissingSkillFailsClosedBeforeLaunch(t *testing.T) {
	s, _ := openTest(t)
	_, task := skillTask(t, s, "ego-browser")
	l := &collectingLauncher{collect: []func() (WorkerResult, error){okResult("x")}}
	d := newResultDispatcher(s, l)
	d.Skills = func() map[string]SkillInfo { return map[string]SkillInfo{} }
	run, _ := d.RunTask(t.Context(), task.ID)
	if run.Status != RunFailed || !strings.HasPrefix(run.Error, "skill_missing: ego-browser") {
		t.Fatalf("run = %+v", run)
	}
	if l.provisions != 0 {
		t.Fatalf("a pane was opened for a task whose skill is missing")
	}
	got, _ := s.GetTask(task.ID)
	if got.State != TaskFailed || !got.NeedsYou {
		t.Fatalf("task = %+v", got)
	}
}

// C10/AC-09: an installed skill still needs the person's yes for this
// Assistant. The card is the existing approval card; approving it grants the
// skill and requeues exactly the tasks it stopped.
func TestGapC10UngrantedSkillAsksOnceAndApprovalRequeues(t *testing.T) {
	s, _ := openTest(t)
	a, task := skillTask(t, s, "ego-browser")
	l := &collectingLauncher{collect: []func() (WorkerResult, error){
		func() (WorkerResult, error) {
			r, _ := okResult("Top posts: https://x.example/1")()
			r.Skills = []string{"ego-browser"}
			return r, nil
		}}}
	d := newResultDispatcher(s, l)
	d.Skills = func() map[string]SkillInfo {
		return map[string]SkillInfo{"ego-browser": {Name: "ego-browser", Path: "/p"}}
	}

	run, _ := d.RunTask(t.Context(), task.ID)
	if run.Status != RunFailed || !strings.HasPrefix(run.Error, "skill_not_granted: ego-browser") || l.provisions != 0 {
		t.Fatalf("run = %+v provisions=%d", run, l.provisions)
	}
	cards, _ := s.ListApprovals(a.ID)
	if len(cards) != 1 || cards[0].SubjectKind != "skill" || cards[0].State != ApprovalPending || !strings.Contains(cards[0].Action, "ego-browser") {
		t.Fatalf("cards = %+v", cards)
	}
	// Retrying while still ungranted must not pile up cards.
	tk, _ := s.GetTask(task.ID)
	if _, _, err := s.RetryTask("retry-1", task.ID, tk.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := d.RunTask(t.Context(), task.ID); err != nil && !strings.Contains(err.Error(), "skill") {
		t.Logf("second run: %v", err)
	}
	if cards, _ = s.ListApprovals(a.ID); len(cards) != 1 {
		t.Fatalf("duplicate approval cards: %+v", cards)
	}

	if _, _, err := s.DecideApproval("decide-1", cards[0].ID, cards[0].ActionHash, true); err != nil {
		t.Fatal(err)
	}
	granted, _ := s.SkillGranted(a.ID, "ego-browser")
	if !granted {
		t.Fatal("approving the card must grant the skill")
	}
	if tk, _ = s.GetTask(task.ID); tk.State != TaskQueued {
		t.Fatalf("the stopped task must be requeued by the grant: %+v", tk)
	}
	run, err := d.RunTask(t.Context(), task.ID)
	if err != nil || run.Status != RunDone {
		t.Fatalf("run = %+v err=%v", run, err)
	}
	if !strings.Contains(l.specs[len(l.specs)-1].Prompt, "ego-browser") {
		t.Fatalf("the worker prompt must name the required skill:\n%s", l.specs[len(l.specs)-1].Prompt)
	}
}

// W04: naming a skill is not using it. A final answer whose transcript never
// loaded the skill is not a result.
func TestGapW04AnswerThatNeverUsedTheSkillIsNotDone(t *testing.T) {
	s, _ := openTest(t)
	a, task := skillTask(t, s, "ego-browser")
	if err := s.GrantSkill(a.ID, "ego-browser"); err != nil {
		t.Fatal(err)
	}
	l := &collectingLauncher{collect: []func() (WorkerResult, error){okResult("I made it up")}} // Skills empty
	d := newResultDispatcher(s, l)
	d.Skills = func() map[string]SkillInfo { return map[string]SkillInfo{"ego-browser": {Name: "ego-browser"}} }
	run, _ := d.RunTask(t.Context(), task.ID)
	if run.Status != RunFailed || !strings.HasPrefix(run.Error, "skill_not_used: ego-browser") {
		t.Fatalf("run = %+v", run)
	}
	if _, ok, _ := s.RunResult(run.ID); ok {
		t.Fatal("no result may be stored for an answer that skipped the required skill")
	}
}
