package personalagent

import (
	"slices"
	"strings"
	"testing"
)

func artifactOf(p TaskProof, kind string) (Artifact, bool) {
	for _, a := range p.Artifacts {
		if a.Kind == kind {
			return a, true
		}
	}
	return Artifact{}, false
}

func finishWithResult(t *testing.T, s *Store, taskID string, res WorkerResult) Run {
	t.Helper()
	r, _, err := s.ClaimTask(taskID, "d")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkStarting(r.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordLaunch(r.ID, Session{HerdrSession: "default", WorkspaceID: "w1", PaneID: "w1:p1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishRunWithResult(r.ID, res); err != nil {
		t.Fatal(err)
	}
	return r
}

// Q04: a finished research task shows what it proves — the result with where it
// was read from and its digest, the sources it named (grounded or not), the
// skills and tools it demonstrably used — never just "done".
func TestTaskProofListsTheResultItsSourceAndWhatBackedIt(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	task := mkTask(t, s, "t1", a.ID, "Find the pricing page")
	r := finishWithResult(t, s, task.ID, WorkerResult{
		Summary: "Pricing starts at $10.", Evidence: EvidenceOMPTranscript, AgentSessionID: "sess-1", MessageID: "msg-9",
		SHA256: strings.Repeat("a", 64),
		Tools:  map[string]int{"web_fetch": 2, "read": 1}, ToolErrors: 1, Skills: []string{"web-research"},
		Sources: []SourceRef{{URL: "https://example.com/pricing", Grounded: true}, {URL: "https://example.com/made-up"}}})

	p, err := s.TaskProof(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	res, ok := artifactOf(p, ArtifactResult)
	if !ok || res.Text != "Pricing starts at $10." || res.SourceRef != "omp_transcript:sess-1#msg-9" || res.Hash != strings.Repeat("a", 64) || res.RunID != r.ID {
		t.Fatalf("result = %+v", res)
	}
	src, ok := artifactOf(p, ArtifactSources)
	if !ok || len(src.Items) != 2 || src.Items[0].State != "grounded" || src.Items[1].State != "ungrounded" {
		t.Fatalf("sources = %+v", src)
	}
	work, ok := artifactOf(p, ArtifactWork)
	if !ok || !slices.Contains(itemLabels(work), "web_fetch × 2") || !slices.Contains(itemLabels(work), "skill: web-research") || !slices.Contains(itemLabels(work), "1 tool error") {
		t.Fatalf("work = %+v", work)
	}
	if len(p.Missing) != 0 {
		t.Fatalf("missing = %v", p.Missing)
	}
}

func itemLabels(a Artifact) []string {
	var out []string
	for _, it := range a.Items {
		out = append(out, it.Label)
	}
	return out
}

// A "done" with no transcript behind it is said to have none, so a card can
// never read as proven when it is only claimed.
func TestTaskProofSaysWhenADoneTaskHasNoEvidence(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	task := mkTask(t, s, "t1", a.ID, "Old style")
	r, _, _ := s.ClaimTask(task.ID, "d")
	_ = s.MarkStarting(r.ID)
	_ = s.RecordLaunch(r.ID, Session{PaneID: "w1:p1"})
	if err := s.FinishRun(r.ID, RunDone, "all good"); err != nil {
		t.Fatal(err)
	}
	p, _ := s.TaskProof(task.ID)
	note, ok := artifactOf(p, ArtifactNote)
	if !ok || note.Text != "all good" || note.Hash != "" || note.SourceRef != "" {
		t.Fatalf("note = %+v", note)
	}
	if !slices.Equal(p.Missing, []string{"result_evidence"}) {
		t.Fatalf("missing = %v", p.Missing)
	}
	if _, ok := artifactOf(p, ArtifactResult); ok {
		t.Fatal("an unproven note must not be drawn as a result")
	}

	// Nothing is expected of a task that has not finished.
	open := mkTask(t, s, "t2", a.ID, "Still queued")
	if p, _ := s.TaskProof(open.ID); len(p.Missing) != 0 || len(p.Artifacts) != 0 {
		t.Fatalf("queued task proof = %+v", p)
	}
}

// Q04: a coding task's proof is its diff and its checks, from the review.
func TestTaskProofOfACodingTaskIsItsDiffAndChecks(t *testing.T) {
	f := newReview(t)
	f.agentWork(map[string]string{"x.txt": "1\n", "y.txt": "2\n"}, "work")
	f.finish()
	f.approveChecks("test -f x.txt", "test -f nope.txt")
	rv := f.start("s1")

	p, err := f.s.TaskProof(f.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	diff, ok := artifactOf(p, ArtifactDiff)
	if !ok || diff.State != rv.State || diff.Hash != rv.DiffHash || !strings.HasPrefix(diff.SourceRef, "git:"+rv.Branch+"@") || len(diff.Items) != 2 {
		t.Fatalf("diff = %+v", diff)
	}
	checks, ok := artifactOf(p, ArtifactChecks)
	if !ok || len(checks.Items) != 2 || checks.Items[0].State != "passed" || checks.Items[1].State != "failed" {
		t.Fatalf("checks = %+v", checks)
	}
	// Waiting for review with no evidence collected yet is a gap, not a pass.
	g := newReview(t)
	g.agentWork(map[string]string{"x.txt": "1\n"}, "work")
	g.finish()
	if p, _ := g.s.TaskProof(g.task.ID); !slices.Equal(p.Missing, []string{"diff"}) {
		t.Fatalf("missing = %v", p.Missing)
	}
}

func TestTaskProofIsAReadOfYourOwnTasksOnly(t *testing.T) {
	api, ada, _, bobTask, _ := scopedPair(t)
	_ = ada
	if _, err := call(t, api, ``, "assistant", "--as", ada, "task", "proof", bobTask); codeOf(err) != "forbidden" {
		t.Fatalf("another Assistant's proof = %q", codeOf(err))
	}
	own, err := call(t, api, ``, "assistant", "task", "proof", bobTask)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := own["proof"]; !ok {
		t.Fatalf("answer = %v", own)
	}
}
