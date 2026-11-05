package personalagent

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
)

// Q04: one proof shape for every kind of finished work. A research task's
// answer, the sources it named, what the run demonstrably did, a coding
// task's diff and its checks are all Artifacts with a SourceRef (where Solco
// read them from) and, when there is one, a digest. A card built from these
// can show what backs a "done"; a done task with nothing behind it says
// `Missing` instead of looking proven. Everything here is read from rows the
// core already keeps; nothing is generated, nothing is stored twice.

// Artifact kinds.
const (
	ArtifactResult  = "result"  // the Agent's answer, read back from its transcript
	ArtifactNote    = "note"    // a run's own closing line with no transcript behind it
	ArtifactSources = "sources" // URLs the answer names, grounded or not
	ArtifactWork    = "work"    // what the run demonstrably did (tools, skills, tool errors)
	ArtifactDiff    = "diff"    // a coding task's changes and the review state
	ArtifactChecks  = "checks"  // the approved checks that ran on that head
)

// ProofItem is one line inside an Artifact. State is grounded/ungrounded for a
// source, passed/failed for a check, empty otherwise.
type ProofItem struct {
	Label string `json:"label"`
	State string `json:"state,omitempty"`
}

// Artifact is one piece of evidence.
type Artifact struct {
	Kind  string `json:"kind"`
	Title string `json:"title"`
	// SourceRef says where Solco read it: omp_transcript:<session>#<message>
	// or git:<branch>@<short head>. Empty means there is no provenance.
	SourceRef string      `json:"sourceRef,omitempty"`
	Hash      string      `json:"hash,omitempty"`
	State     string      `json:"state,omitempty"`
	Text      string      `json:"text,omitempty"`
	Items     []ProofItem `json:"items,omitempty"`
	RunID     string      `json:"runId,omitempty"`
}

// TaskProof is a task's evidence and what is missing from it.
type TaskProof struct {
	TaskID    string     `json:"taskId"`
	State     string     `json:"state"`
	Artifacts []Artifact `json:"artifacts"`
	// Missing names evidence a task in this state should have and lacks:
	// result_evidence (a done task whose answer has no transcript behind it)
	// or diff (a coding task waiting for review with no evidence collected).
	Missing []string `json:"missing"`
}

// TaskProof reads one task's evidence.
func (s *Store) TaskProof(taskID string) (TaskProof, error) {
	t, err := s.GetTask(taskID)
	if err != nil {
		return TaskProof{}, err
	}
	p := TaskProof{TaskID: t.ID, State: t.State, Artifacts: []Artifact{}, Missing: []string{}}
	runs, err := s.ListRuns(t.ID) // newest first
	if err != nil {
		return p, err
	}
	var result *Artifact
	for _, r := range runs {
		res, found, err := s.RunResult(r.ID)
		if err != nil {
			return p, err
		}
		if found {
			p.Artifacts = append(p.Artifacts, resultArtifacts(r.ID, res)...)
			result = &p.Artifacts[0]
			break
		}
		if r.Status == RunDone && r.Result != "" {
			p.Artifacts = append(p.Artifacts, Artifact{Kind: ArtifactNote, Title: "The agent's closing note (nothing verifies it)", Text: r.Result, RunID: r.ID})
			break
		}
	}
	if t.Kind == KindCoding {
		rv, err := scanReview(s.db.QueryRow(`SELECT `+reviewCols+` FROM reviews WHERE task_id=? ORDER BY created_at DESC, rowid DESC LIMIT 1`, t.ID))
		switch {
		case errors.Is(err, sql.ErrNoRows):
			if t.State == TaskReviewRequired {
				p.Missing = append(p.Missing, "diff")
			}
		case err != nil:
			return p, wrapBusy(err)
		default:
			p.Artifacts = append(p.Artifacts, reviewArtifacts(rv)...)
		}
	}
	if t.Kind != KindCoding && t.State == TaskDone && result == nil {
		p.Missing = append(p.Missing, "result_evidence")
	}
	return p, nil
}

func resultArtifacts(runID string, res WorkerResult) []Artifact {
	title := "Result"
	if res.Truncated {
		title += " (shortened)"
	}
	out := []Artifact{{Kind: ArtifactResult, Title: title, SourceRef: fmt.Sprintf("%s:%s#%s", res.Evidence, res.AgentSessionID, res.MessageID),
		Hash: res.SHA256, Text: res.Summary, RunID: runID}}
	if len(res.Sources) > 0 {
		a := Artifact{Kind: ArtifactSources, Title: "Sources", RunID: runID}
		for _, src := range res.Sources {
			state := "ungrounded"
			if src.Grounded {
				state = "grounded"
			}
			a.Items = append(a.Items, ProofItem{Label: src.URL, State: state})
		}
		out = append(out, a)
	}
	work := Artifact{Kind: ArtifactWork, Title: "What the run did", RunID: runID}
	names := make([]string, 0, len(res.Tools))
	for name := range res.Tools {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		work.Items = append(work.Items, ProofItem{Label: fmt.Sprintf("%s × %d", name, res.Tools[name])})
	}
	for _, sk := range res.Skills {
		work.Items = append(work.Items, ProofItem{Label: "skill: " + sk})
	}
	if res.ToolErrors > 0 {
		label := fmt.Sprintf("%d tool errors", res.ToolErrors)
		if res.ToolErrors == 1 {
			label = "1 tool error"
		}
		work.Items = append(work.Items, ProofItem{Label: label})
	}
	if len(work.Items) > 0 {
		out = append(out, work)
	}
	return out
}

func reviewArtifacts(rv Review) []Artifact {
	diff := Artifact{Kind: ArtifactDiff, Title: fmt.Sprintf("Changes on %s (%d files, +%d −%d)", rv.Branch, rv.Files, rv.Insertions, rv.Deletions),
		SourceRef: "git:" + rv.Branch + "@" + shortOf(rv.Head), Hash: rv.DiffHash, State: rv.State}
	for _, path := range rv.Paths {
		diff.Items = append(diff.Items, ProofItem{Label: path})
	}
	out := []Artifact{diff}
	if len(rv.Checks) > 0 {
		checks := Artifact{Kind: ArtifactChecks, Title: "Checks on that commit", SourceRef: "git:" + rv.Branch + "@" + shortOf(rv.Head)}
		for _, c := range rv.Checks {
			state := "passed"
			if !c.OK {
				state = "failed"
			}
			checks.Items = append(checks.Items, ProofItem{Label: c.Command, State: state})
		}
		out = append(out, checks)
	}
	return out
}
