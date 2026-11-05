package personalagent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// E03: review and merge as one state machine.
//
//	review_required ─Start→ ready ─RequestMerge→ awaiting_approval ─(person approves)→ Merge → merged
//	                    └→ checks_failed                     └→ stale / merge_failed (back to the task, work kept)
//
// Nothing merges by itself. The approval is for one exact head, one diff and
// one target branch tip; the merge re-checks all three at the instant it acts
// and merges the reviewed commit hash, never the branch name.

// Review states.
const (
	ReviewReady        = "ready"
	ReviewChecksFailed = "checks_failed"
	ReviewAwaiting     = "awaiting_approval"
	ReviewMerging      = "merging"
	ReviewMerged       = "merged"
	ReviewMergeFailed  = "merge_failed"
	ReviewStale        = "stale"
	// The Owner's two other answers to the evidence (Gap Q02): send the work
	// back with words, or close the task. Neither merges anything.
	ReviewChangesRequested = "changes_requested"
	ReviewRejected         = "rejected"
)

// MaxChecks bounds the approved check list.
const MaxChecks = 8

// CheckResult is one check command's outcome. Output is the last few KB.
type CheckResult struct {
	Command    string `json:"command"`
	OK         bool   `json:"ok"`
	ExitCode   int    `json:"exitCode"`
	Output     string `json:"output"`
	DurationMS int64  `json:"durationMs"`
}

// Review is the evidence for one head of one task.
type Review struct {
	ID                string        `json:"id"`
	TaskID            string        `json:"taskId"`
	AssistantID       string        `json:"assistantId"`
	State             string        `json:"state"`
	Branch            string        `json:"branch"`
	Head              string        `json:"head"`
	BaseHead          string        `json:"baseHead"`
	TargetBranch      string        `json:"targetBranch"`
	TargetHead        string        `json:"targetHead"`
	Files             int           `json:"files"`
	Insertions        int           `json:"insertions"`
	Deletions         int           `json:"deletions"`
	Paths             []string      `json:"paths"`
	DiffHash          string        `json:"diffHash"`
	Checks            []CheckResult `json:"checks"`
	AgentReviewTaskID string        `json:"agentReviewTaskId"`
	ApprovalID        string        `json:"approvalId"`
	Error             string        `json:"error"`
	Revision          int           `json:"revision"`
	CreatedAt         int64         `json:"createdAt"`
	UpdatedAt         int64         `json:"updatedAt"`
}

const reviewCols = `id,task_id,assistant_id,state,branch,head,base_head,target_branch,target_head,files,insertions,deletions,paths,diff_hash,checks,agent_review_task_id,approval_id,error,revision,created_at,updated_at`

func scanReview(r rowScanner) (Review, error) {
	var x Review
	var paths, checks string
	err := r.Scan(&x.ID, &x.TaskID, &x.AssistantID, &x.State, &x.Branch, &x.Head, &x.BaseHead, &x.TargetBranch, &x.TargetHead,
		&x.Files, &x.Insertions, &x.Deletions, &paths, &x.DiffHash, &checks, &x.AgentReviewTaskID, &x.ApprovalID, &x.Error,
		&x.Revision, &x.CreatedAt, &x.UpdatedAt)
	if err != nil {
		return x, err
	}
	x.Paths, x.Checks = []string{}, []CheckResult{}
	_ = json.Unmarshal([]byte(paths), &x.Paths)
	_ = json.Unmarshal([]byte(checks), &x.Checks)
	return x, nil
}

func getReviewTx(q queryRower, id string) (Review, error) {
	r, err := scanReview(q.QueryRow(`SELECT `+reviewCols+` FROM reviews WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, wrapBusy(err)
}

// ---- approved checks -------------------------------------------------------

// Checks returns the Assistant's approved check commands.
func (s *Store) Checks(assistantID string) ([]string, error) {
	var raw string
	err := s.db.QueryRow(`SELECT active FROM review_checks WHERE assistant_id=?`, assistantID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return []string{}, nil
	}
	if err != nil {
		return nil, wrapBusy(err)
	}
	out := []string{}
	_ = json.Unmarshal([]byte(raw), &out)
	return out, nil
}

// RequestChecks proposes a new check list. It runs nothing and changes nothing
// until a person approves the returned card; each proposal replaces the last
// pending one. An empty list is applied at once (fewer checks is not riskier).
func (s *Store) RequestChecks(requestID, assistantID string, cmds []string) (Approval, bool, error) {
	if len(cmds) > MaxChecks {
		return Approval{}, false, fmt.Errorf("%w: at most %d checks", ErrInvalid, MaxChecks)
	}
	clean := make([]string, 0, len(cmds))
	for _, c := range cmds {
		c = strings.TrimSpace(c)
		if c == "" || len(c) > 500 || strings.ContainsRune(c, 0) {
			return Approval{}, false, fmt.Errorf("%w: a check is 1–500 characters", ErrInvalid)
		}
		clean = append(clean, c)
	}
	raw, _ := json.Marshal(clean)
	_, replayed, err := execute(s, sub(requestID, "/set"), "checks.set", map[string]any{"a": assistantID, "c": clean}, func(tx *sql.Tx) (struct{}, error) {
		if _, err := getAssistantTx(tx, assistantID); err != nil {
			return struct{}{}, err
		}
		if len(clean) == 0 {
			_, err := tx.Exec(`INSERT INTO review_checks(assistant_id,active,pending,revision) VALUES(?,?,?,1)
				ON CONFLICT(assistant_id) DO UPDATE SET active='[]', pending='[]', revision=revision+1`, assistantID, "[]", "[]")
			return struct{}{}, err
		}
		_, err := tx.Exec(`INSERT INTO review_checks(assistant_id,active,pending,revision) VALUES(?,?,?,1)
			ON CONFLICT(assistant_id) DO UPDATE SET pending=excluded.pending, revision=revision+1`, assistantID, "[]", string(raw))
		return struct{}{}, err
	})
	if err != nil || len(clean) == 0 {
		return Approval{}, replayed, err
	}
	action := "Allow Solco to run these commands in each reviewed task worktree: " + strings.Join(clean, " ; ")
	return s.RequestApproval(sub(requestID, "/card"), RequestApprovalInput{AssistantID: assistantID, SubjectKind: "checks", SubjectID: assistantID, Action: action})
}

func sub(requestID, suffix string) string {
	if requestID == "" {
		return ""
	}
	return requestID + suffix
}

// CheckRunner runs one approved command in a worktree.
type CheckRunner interface {
	Run(dir, command string) CheckResult
}

// ShellChecks is the production runner: `/bin/sh -c`, a scrubbed environment
// (no tokens or API keys inherited), a timeout, and bounded output.
type ShellChecks struct{ Timeout time.Duration }

func (c ShellChecks) Run(dir, command string) CheckResult {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "LANG=C.UTF-8", "TMPDIR=" + os.TempDir(), "CI=1"}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &tail{buf: &out, max: 2048}, &tail{buf: &out, max: 2048}
	start := time.Now()
	err := cmd.Run()
	res := CheckResult{Command: command, OK: err == nil, DurationMS: time.Since(start).Milliseconds(), Output: strings.ToValidUTF8(strings.TrimSpace(out.String()), "")}
	if err != nil {
		res.ExitCode = -1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			res.ExitCode = ee.ExitCode()
		}
		if ctx.Err() != nil {
			res.Output += "\n[timed out]"
		}
	}
	if len(res.Output) > 2048 {
		res.Output = res.Output[len(res.Output)-2048:]
	}
	return res
}

// tail keeps only the last max bytes written through it (shared buffer).
type tail struct {
	buf *bytes.Buffer
	max int
}

func (t *tail) Write(p []byte) (int, error) {
	t.buf.Write(p)
	if over := t.buf.Len() - t.max; over > 0 {
		t.buf.Next(over)
	}
	return len(p), nil
}

// ---- the reviewer ----------------------------------------------------------

// Reviewer turns a finished coding task into reviewed evidence, and a human's
// approval into one safe merge.
type Reviewer struct {
	Store     *Store
	Worktrees *Worktrees
	Git       Git
	Checks    CheckRunner

	mu sync.Mutex // one merge executes at a time in this process
}

func (r *Reviewer) git() Git {
	if r.Git != nil {
		return r.Git
	}
	return r.Worktrees.git()
}

func (r *Reviewer) checks() CheckRunner {
	if r.Checks != nil {
		return r.Checks
	}
	return ShellChecks{}
}

// Get reads one review.
func (r *Reviewer) Get(id string) (Review, error) { return getReviewTx(r.Store.db, id) }

// ForTask returns the task's newest review.
func (r *Reviewer) ForTask(taskID string) (Review, bool, error) {
	x, err := scanReview(r.Store.db.QueryRow(`SELECT `+reviewCols+` FROM reviews WHERE task_id=? ORDER BY created_at DESC, rowid DESC LIMIT 1`, taskID))
	if errors.Is(err, sql.ErrNoRows) {
		return x, false, nil
	}
	return x, err == nil, wrapBusy(err)
}

// parseNumstat reads `git diff --numstat -z -M`: `ins\tdel\tpath\0`, and for a
// rename `ins\tdel\t\0old\0new\0`. Names are NUL-terminated, so a tab, newline
// or quote in a name stays literal.
func parseNumstat(out string) (files, ins, del int, paths []string) {
	parts := strings.Split(out, "\x00")
	for i := 0; i < len(parts); i++ {
		rec := parts[i]
		if rec == "" {
			continue
		}
		f := strings.SplitN(rec, "\t", 3)
		if len(f) < 3 {
			continue
		}
		path := f[2]
		if path == "" && i+2 < len(parts) { // a rename: the next two fields are old and new
			path = parts[i+2]
			i += 2
		}
		if n, err := strconv.Atoi(f[0]); err == nil {
			ins += n
		}
		if n, err := strconv.Atoi(f[1]); err == nil {
			del += n
		}
		files++
		paths = append(paths, path)
	}
	return
}

// evidence gathers the facts of the worktree's current head.
type evidence struct {
	info             WorktreeInfo
	head, targetHead string
	files, ins, del  int
	paths            []string
	diffHash         string
}

func (r *Reviewer) gather(task Task) (evidence, error) {
	ev := evidence{}
	info, ok, err := r.Worktrees.Get(task.ID)
	if err != nil {
		return ev, err
	}
	if !ok || info.State != "active" {
		return ev, fmt.Errorf("%w: the task has no worktree to review", ErrBadState)
	}
	ev.info = info
	if _, err := os.Stat(info.Path); err != nil {
		return ev, fmt.Errorf("%w: the task's worktree folder is gone", ErrBadState)
	}
	if !r.Worktrees.onBranch(info.Path, info.Branch) {
		return ev, fmt.Errorf("%w: the worktree is no longer on %s", ErrBadState, info.Branch)
	}
	dirty, err := r.Worktrees.Status(info)
	if err != nil {
		return ev, fmt.Errorf("%w: %v", ErrBadState, err)
	}
	if len(dirty) > 0 {
		return ev, fmt.Errorf("%w: the worktree has uncommitted changes; commit or discard them before review", ErrBadState)
	}
	g := r.git()
	if ev.head, err = g.Run(info.Path, "rev-parse", "--verify", "HEAD^{commit}"); err != nil {
		return ev, fmt.Errorf("%w: %v", ErrBadState, err)
	}
	if ev.head == info.BaseHead {
		return ev, fmt.Errorf("%w: the task has no commits to review", ErrBadState)
	}
	if ev.targetHead, err = g.Run(info.Repo, "rev-parse", "--verify", "refs/heads/"+info.TargetBranch+"^{commit}"); err != nil {
		return ev, fmt.Errorf("%w: target branch %s: %v", ErrBadState, info.TargetBranch, err)
	}
	diff := []string{"diff", "--no-ext-diff", "--no-textconv", "-M"}
	stat, err := g.Run(info.Path, append(append([]string{}, diff...), "--numstat", "-z", info.BaseHead, ev.head)...)
	if err != nil {
		return ev, fmt.Errorf("%w: %v", ErrBadState, err)
	}
	ev.files, ev.ins, ev.del, ev.paths = parseNumstat(stat)
	full, err := g.Run(info.Path, append(append([]string{}, diff...), "--binary", info.BaseHead, ev.head)...)
	if err != nil {
		return ev, fmt.Errorf("%w: %v", ErrBadState, err)
	}
	sum := sha256.Sum256([]byte(full))
	ev.diffHash = hex.EncodeToString(sum[:])
	return ev, nil
}

// Start collects evidence for the task's current head, runs the approved
// checks, and records one review. Starting again (a new request) supersedes
// the earlier review and withdraws any card still waiting on it.
func (r *Reviewer) Start(requestID, taskID string) (Review, bool, error) {
	payload := map[string]string{"task": taskID}
	if prev, ok, err := peekReceipt[Review](r.Store, requestID, "review.start", payload); err != nil || ok {
		return prev, ok, err
	}
	task, err := r.Store.GetTask(taskID)
	if err != nil {
		return Review{}, false, err
	}
	if task.Kind != KindCoding {
		return Review{}, false, fmt.Errorf("%w: only a coding task has a diff to review", ErrInvalid)
	}
	if task.State != TaskReviewRequired {
		return Review{}, false, fmt.Errorf("%w: the task is %s, not waiting for review", ErrBadState, task.State)
	}
	ev, err := r.gather(task)
	if err != nil {
		return Review{}, false, err
	}
	cmds, err := r.Store.Checks(task.AssistantID)
	if err != nil {
		return Review{}, false, err
	}
	results := []CheckResult{}
	state := ReviewReady
	for _, c := range cmds {
		res := r.checks().Run(ev.info.Path, c)
		results = append(results, res)
		if !res.OK {
			state = ReviewChecksFailed
		}
	}
	// Checks may have written files; evidence is of the commit, so a check that
	// dirties the tree does not change it, but the head must not have moved.
	if head, err := r.git().Run(ev.info.Path, "rev-parse", "--verify", "HEAD^{commit}"); err != nil || head != ev.head {
		return Review{}, false, fmt.Errorf("%w: the branch moved while the checks ran", ErrStaleRevision)
	}
	pathsJSON, _ := json.Marshal(ev.paths)
	checksJSON, _ := json.Marshal(results)
	return execute(r.Store, requestID, "review.start", payload, func(tx *sql.Tx) (Review, error) {
		now := r.Store.nowMS()
		id := newID("v")
		if _, err := tx.Exec(`INSERT INTO reviews(`+reviewCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			id, task.ID, task.AssistantID, state, ev.info.Branch, ev.head, ev.info.BaseHead, ev.info.TargetBranch, ev.targetHead,
			ev.files, ev.ins, ev.del, string(pathsJSON), ev.diffHash, string(checksJSON), "", "", "", 1, now, now); err != nil {
			return Review{}, err
		}
		// Earlier reviews of this task no longer describe the task.
		if _, err := tx.Exec(`UPDATE reviews SET state=?, updated_at=? WHERE task_id=? AND id<>? AND state IN (?,?,?,?)`,
			ReviewStale, now, task.ID, id, ReviewReady, ReviewChecksFailed, ReviewAwaiting, ReviewMergeFailed); err != nil {
			return Review{}, err
		}
		if _, err := tx.Exec(`UPDATE tasks SET review_state=?, needs_you=1, revision=revision+1, updated_at=? WHERE id=?`, state, now, task.ID); err != nil {
			return Review{}, err
		}
		kind := "review.ready"
		if state == ReviewChecksFailed {
			kind = "review.failed"
		}
		if err := emitReview(tx, task, id, state, kind, now); err != nil {
			return Review{}, err
		}
		return getReviewTx(tx, id)
	})
}

func emitReview(tx *sql.Tx, task Task, reviewID, state, kind string, now int64) error {
	_, err := emitTx(tx, Event{AssistantID: task.AssistantID, Source: "review", Kind: kind, Resource: reviewID, Revision: state, TaskID: task.ID,
		Detail: fmt.Sprintf("review %s of task %s is %s", reviewID, task.ID, state)}, now)
	return err
}

func shortOf(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

// mergeAction is the exact sentence a person approves; the approval's hash
// binds it, so a different head or diff is a different card.
func mergeAction(rv Review) string {
	passed := "no checks configured"
	if n := len(rv.Checks); n > 0 {
		ok := 0
		for _, c := range rv.Checks {
			if c.OK {
				ok++
			}
		}
		passed = fmt.Sprintf("checks %d/%d passed", ok, n)
	}
	return fmt.Sprintf("Merge %s (head %s, diff %s, %d files +%d −%d, %s) into %s at %s",
		rv.Branch, shortOf(rv.Head), shortOf(rv.DiffHash), rv.Files, rv.Insertions, rv.Deletions, passed, rv.TargetBranch, shortOf(rv.TargetHead))
}

// fresh re-reads head and target tip and says whether the review still holds.
func (r *Reviewer) fresh(rv Review, info WorktreeInfo) error {
	g := r.git()
	head, err := g.Run(info.Path, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil || head != rv.Head || !r.Worktrees.onBranch(info.Path, rv.Branch) {
		return fmt.Errorf("%w: the branch is no longer at the reviewed commit", ErrStaleRevision)
	}
	tip, err := g.Run(info.Repo, "rev-parse", "--verify", "refs/heads/"+rv.TargetBranch+"^{commit}")
	if err != nil || tip != rv.TargetHead {
		return fmt.Errorf("%w: %s has moved since the review", ErrStaleRevision, rv.TargetBranch)
	}
	return nil
}

func (r *Reviewer) setState(id, state, errText string, from ...string) (bool, error) {
	q := `UPDATE reviews SET state=?, error=?, revision=revision+1, updated_at=? WHERE id=?`
	args := []any{state, errText, r.Store.nowMS(), id}
	if len(from) > 0 {
		q += ` AND state IN (` + strings.TrimSuffix(strings.Repeat("?,", len(from)), ",") + `)`
		for _, f := range from {
			args = append(args, f)
		}
	}
	res, err := r.Store.db.Exec(q, args...)
	if err != nil {
		return false, wrapBusy(err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// markStale records that a review no longer describes the branch.
func (r *Reviewer) markStale(rv Review, why error) {
	_, _ = r.setState(rv.ID, ReviewStale, why.Error(), ReviewReady, ReviewAwaiting, ReviewMerging)
	_, _ = r.Store.db.Exec(`UPDATE tasks SET review_state=?, needs_you=1, updated_at=? WHERE id=?`, ReviewStale, r.Store.nowMS(), rv.TaskID)
}

// RequestMerge asks a person to approve merging exactly this review.
func (r *Reviewer) RequestMerge(requestID, reviewID string) (Review, Approval, bool, error) {
	rv, err := r.Get(reviewID)
	if err != nil {
		return rv, Approval{}, false, err
	}
	if rv.State == ReviewAwaiting && rv.ApprovalID != "" {
		ap, err := r.Store.GetApproval(rv.ApprovalID)
		return rv, ap, true, err
	}
	if rv.State != ReviewReady {
		return rv, Approval{}, false, fmt.Errorf("%w: the review is %s", ErrBadState, rv.State)
	}
	info, ok, err := r.Worktrees.Get(rv.TaskID)
	if err != nil || !ok || info.State != "active" {
		return rv, Approval{}, false, fmt.Errorf("%w: the task has no worktree", ErrBadState)
	}
	if err := r.fresh(rv, info); err != nil {
		r.markStale(rv, err)
		return rv, Approval{}, false, err
	}
	ap, replayed, err := r.Store.RequestApproval(requestID, RequestApprovalInput{
		AssistantID: rv.AssistantID, SubjectKind: "task", SubjectID: rv.TaskID, Action: mergeAction(rv), TTL: 24 * time.Hour})
	if err != nil {
		return rv, ap, replayed, err
	}
	if _, err := r.Store.db.Exec(`UPDATE reviews SET state=?, approval_id=?, revision=revision+1, updated_at=? WHERE id=? AND state=?`,
		ReviewAwaiting, ap.ID, r.Store.nowMS(), rv.ID, ReviewReady); err != nil {
		return rv, ap, replayed, wrapBusy(err)
	}
	_, _ = r.Store.db.Exec(`UPDATE tasks SET review_state=? WHERE id=?`, ReviewAwaiting, rv.TaskID)
	rv, err = r.Get(rv.ID)
	return rv, ap, replayed, err
}

// RequestAgentReview is the optional second opinion: a read-only research task
// (a child of the coding task) in the task's worktree, briefed with the exact
// evidence. Its verdict is information for the person; it gates nothing.
func (r *Reviewer) RequestAgentReview(requestID, reviewID string) (Review, Task, bool, error) {
	rv, err := r.Get(reviewID)
	if err != nil {
		return rv, Task{}, false, err
	}
	info, ok, err := r.Worktrees.Get(rv.TaskID)
	if err != nil || !ok || info.State != "active" {
		return rv, Task{}, false, fmt.Errorf("%w: the task has no worktree", ErrBadState)
	}
	task, err := r.Store.GetTask(rv.TaskID)
	if err != nil {
		return rv, Task{}, false, err
	}
	brief := fmt.Sprintf("Review branch %s at commit %s against %s (%d files, +%d −%d, diff %s). Read the diff with `git diff %s %s`, check it for bugs, missing tests and unrelated changes, and report your findings. Do not edit files, commit, merge or push.",
		rv.Branch, shortOf(rv.Head), shortOf(rv.BaseHead), rv.Files, rv.Insertions, rv.Deletions, shortOf(rv.DiffHash), rv.BaseHead, rv.Head)
	child, replayed, err := r.Store.CreateTask(requestID, CreateTaskInput{
		AssistantID: rv.AssistantID, Kind: KindResearch, Title: "Review: " + task.Title, Brief: brief,
		RepoPath: info.Path, ParentTaskID: task.ID, Agent: task.Agent, Model: task.Model})
	if err != nil {
		return rv, child, replayed, err
	}
	if _, err := r.Store.db.Exec(`UPDATE reviews SET agent_review_task_id=?, updated_at=? WHERE id=?`, child.ID, r.Store.nowMS(), rv.ID); err != nil {
		return rv, child, replayed, wrapBusy(err)
	}
	rv, err = r.Get(rv.ID)
	return rv, child, replayed, err
}

var repoLocks sync.Map // repo path -> *sync.Mutex

func lockRepo(repo string) func() {
	m, _ := repoLocks.LoadOrStore(repo, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// cleanTitle makes a title safe for a one-line commit message.
func cleanTitle(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if len(s) > 100 {
		s = strings.ToValidUTF8(s[:100], "")
	}
	return s
}

// Merge performs the approved merge. It acts only on an approval a person gave
// for exactly this review's action, then re-checks the worktree head, the
// target tip, and the main checkout at the instant it acts, and merges the
// reviewed commit hash. A second call after success is a no-op.
func (r *Reviewer) Merge(reviewID string) (Review, error) {
	rv, err := r.Get(reviewID)
	if err != nil {
		return rv, err
	}
	switch rv.State {
	case ReviewMerged:
		return rv, nil
	case ReviewMerging:
		return rv, fmt.Errorf("%w: a merge of this review is already running", ErrBusy)
	case ReviewAwaiting:
	default:
		return rv, fmt.Errorf("%w: the review is %s; no merge was approved", ErrBadState, rv.State)
	}
	ap, err := r.Store.GetApproval(rv.ApprovalID)
	if err != nil {
		return rv, err
	}
	if ap.State != ApprovalApproved {
		return rv, fmt.Errorf("%w: the merge is %s, not approved", ErrBadState, ap.State)
	}
	task, err := r.Store.GetTask(rv.TaskID)
	if err != nil {
		return rv, err
	}
	if ap.SubjectKind != "task" || ap.SubjectID != rv.TaskID || ap.Action != mergeAction(rv) ||
		ap.ActionHash != actionHash(rv.AssistantID, "task", rv.TaskID, ap.Action, ap.ExpectedRevision) {
		return rv, fmt.Errorf("%w: the approval is not for this review", ErrInvalid)
	}
	if ap.ExpectedRevision != task.Revision {
		err := fmt.Errorf("%w: the task changed after the approval", ErrStaleRevision)
		r.markStale(rv, err)
		return r.mustGet(rv.ID), err
	}
	info, ok, err := r.Worktrees.Get(rv.TaskID)
	if err != nil || !ok || info.State != "active" {
		return rv, fmt.Errorf("%w: the task has no worktree", ErrBadState)
	}

	unlock := lockRepo(info.Repo)
	defer unlock()
	r.mu.Lock()
	defer r.mu.Unlock()

	// Claim: exactly one caller moves awaiting → merging.
	claimed, err := r.setState(rv.ID, ReviewMerging, "", ReviewAwaiting)
	if err != nil {
		return rv, err
	}
	if !claimed {
		cur := r.mustGet(rv.ID)
		if cur.State == ReviewMerged {
			return cur, nil
		}
		return cur, fmt.Errorf("%w: the review is %s", ErrBusy, cur.State)
	}
	// From here every refusal must release the claim.
	release := func(state, why string) { _, _ = r.setState(rv.ID, state, why, ReviewMerging) }

	if err := r.fresh(rv, info); err != nil {
		r.markStale(rv, err)
		return r.mustGet(rv.ID), err
	}
	g := r.git()
	if cur, err := g.Run(info.Repo, "symbolic-ref", "--short", "-q", "HEAD"); err != nil || cur != rv.TargetBranch {
		release(ReviewAwaiting, "")
		return r.mustGet(rv.ID), fmt.Errorf("%w: the main checkout is not on %s; switch back and retry", ErrBadState, rv.TargetBranch)
	}
	if out, err := g.Run(info.Repo, "status", "--porcelain=v1", "-z", "--untracked-files=no"); err != nil || strings.TrimSpace(out) != "" {
		release(ReviewAwaiting, "")
		return r.mustGet(rv.ID), fmt.Errorf("%w: the main checkout has uncommitted changes; commit or stash them and retry", ErrBadState)
	}
	if _, err := os.Stat(filepath.Join(gitDir(g, info.Repo), "MERGE_HEAD")); err == nil {
		release(ReviewAwaiting, "")
		return r.mustGet(rv.ID), fmt.Errorf("%w: a merge is already in progress in the main checkout", ErrBadState)
	}
	// The status check above leaves untracked and ignored files alone, and git
	// itself overwrites an ignored file when a merge adds that path. Refuse by
	// name before merging anything the person already has on disk.
	if out, err := g.Run(info.Repo, "diff", "--name-only", "-z", "--diff-filter=A", "HEAD", rv.Head); err == nil {
		for _, name := range strings.Split(out, "\x00") {
			if name == "" {
				continue
			}
			if _, serr := os.Lstat(filepath.Join(info.Repo, filepath.FromSlash(name))); serr == nil {
				release(ReviewAwaiting, "")
				return r.mustGet(rv.ID), fmt.Errorf("%w: %q already exists in the main checkout and the merge would replace it; move it away and retry", ErrBadState, name)
			}
		}
	} else {
		release(ReviewAwaiting, "")
		return r.mustGet(rv.ID), fmt.Errorf("%w: could not compare the reviewed commit with the main checkout", ErrBadState)
	}

	msg := fmt.Sprintf("Merge task %s (%s)", cleanTitle(task.Title), task.ID)
	if _, err := g.Run(info.Repo, "merge", "--no-ff", "--no-edit", "-m", msg, rv.Head); err != nil {
		why := firstLine(err.Error())
		if _, serr := os.Stat(filepath.Join(gitDir(g, info.Repo), "MERGE_HEAD")); serr == nil {
			why = "merge conflict: resolve it in the task's worktree and review again"
		}
		_, _ = g.Run(info.Repo, "merge", "--abort")
		release(ReviewMergeFailed, why)
		_, _ = r.Store.db.Exec(`UPDATE tasks SET review_state=?, needs_you=1, state=?, updated_at=? WHERE id=?`, ReviewMergeFailed, TaskReviewRequired, r.Store.nowMS(), rv.TaskID)
		_ = r.emitFailed(rv)
		return r.mustGet(rv.ID), fmt.Errorf("%w: %s", ErrBadState, why)
	}
	if _, err := g.Run(info.Repo, "merge-base", "--is-ancestor", rv.Head, "HEAD"); err != nil {
		release(ReviewMergeFailed, "the merge did not contain the reviewed commit")
		return r.mustGet(rv.ID), fmt.Errorf("%w: the merge did not contain the reviewed commit", ErrBadState)
	}
	if err := r.finalizeMerged(rv); err != nil {
		return r.mustGet(rv.ID), err
	}
	// The branch is merged and the folder clean: nothing is lost by removing them.
	_ = r.Worktrees.Cleanup(rv.TaskID, false)
	return r.mustGet(rv.ID), nil
}

func (r *Reviewer) mustGet(id string) Review { rv, _ := r.Get(id); return rv }

func (r *Reviewer) emitFailed(rv Review) error {
	task, err := r.Store.GetTask(rv.TaskID)
	if err != nil {
		return err
	}
	_, _, err = execute(r.Store, "", "review.event", nil, func(tx *sql.Tx) (struct{}, error) {
		return struct{}{}, emitReview(tx, task, rv.ID, ReviewMergeFailed, "review.failed", r.Store.nowMS())
	})
	return err
}

// finalizeMerged records a completed merge: review, task and event in one
// transaction.
func (r *Reviewer) finalizeMerged(rv Review) error {
	task, err := r.Store.GetTask(rv.TaskID)
	if err != nil {
		return err
	}
	_, _, err = execute(r.Store, "", "review.merged", nil, func(tx *sql.Tx) (struct{}, error) {
		now := r.Store.nowMS()
		res, err := tx.Exec(`UPDATE reviews SET state=?, error='', revision=revision+1, updated_at=? WHERE id=? AND state=?`, ReviewMerged, now, rv.ID, ReviewMerging)
		if err != nil {
			return struct{}{}, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return struct{}{}, nil
		}
		if _, err := tx.Exec(`UPDATE tasks SET state=?, review_state=?, needs_you=0, revision=revision+1, updated_at=? WHERE id=?`, TaskDone, ReviewMerged, now, rv.TaskID); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, emitReview(tx, task, rv.ID, ReviewMerged, "review.merged", now)
	})
	return err
}

func gitDir(g Git, repo string) string {
	if out, err := g.Run(repo, "rev-parse", "--absolute-git-dir"); err == nil && out != "" {
		return out
	}
	return filepath.Join(repo, ".git")
}

// Recover settles merges a crash interrupted, from what git actually holds:
// the reviewed commit is in the target branch (merged) or it is not (failed,
// nothing was merged). It returns how many it settled.
func (r *Reviewer) Recover() (int, error) {
	rows, err := r.Store.db.Query(`SELECT `+reviewCols+` FROM reviews WHERE state=?`, ReviewMerging)
	if err != nil {
		return 0, wrapBusy(err)
	}
	var stuck []Review
	for rows.Next() {
		x, err := scanReview(rows)
		if err != nil {
			_ = rows.Close()
			return 0, err
		}
		stuck = append(stuck, x)
	}
	_ = rows.Close()
	n := 0
	for _, rv := range stuck {
		info, ok, _ := r.Worktrees.Get(rv.TaskID)
		if !ok {
			_, _ = r.setState(rv.ID, ReviewMergeFailed, "interrupted; the task's worktree record is gone", ReviewMerging)
			n++
			continue
		}
		unlock := lockRepo(info.Repo)
		g := r.git()
		if _, err := os.Stat(filepath.Join(gitDir(g, info.Repo), "MERGE_HEAD")); err == nil {
			_, _ = g.Run(info.Repo, "merge", "--abort")
		}
		if _, err := g.Run(info.Repo, "merge-base", "--is-ancestor", rv.Head, "refs/heads/"+rv.TargetBranch); err == nil {
			_ = r.finalizeMerged(rv)
			_ = r.Worktrees.Cleanup(rv.TaskID, false)
		} else {
			_, _ = r.setState(rv.ID, ReviewMergeFailed, "interrupted before the merge completed; nothing was merged", ReviewMerging)
			_, _ = r.Store.db.Exec(`UPDATE tasks SET review_state=?, needs_you=1, updated_at=? WHERE id=?`, ReviewMergeFailed, r.Store.nowMS(), rv.TaskID)
		}
		unlock()
		n++
	}
	return n, nil
}
