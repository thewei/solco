package personalagent

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// E02: the Go equivalent of Shepherdr's task worktrees. A coding task works on
// its own branch in its own folder, started from a recorded baseline commit,
// and the person's main checkout is never touched. Everything here is plain
// `git`, run so that nothing the repository carries (hooks, fsmonitor, an
// external diff, a prompt) can execute or ask a question.

// Git runs one git command and returns its trimmed stdout. Tests may replace
// it; the default is hardened (see ExecGit).
type Git interface {
	Run(dir string, args ...string) (string, error)
}

// ExecGit is the production Git.
type ExecGit struct{}

// hardening are `-c` overrides placed before every command: they beat the
// repository's own config, so a hostile or merely odd `.git/config` cannot
// run code through git.
var hardening = []string{
	"-c", "core.fsmonitor=false",
	"-c", "core.hooksPath=/dev/null",
	"-c", "core.pager=cat",
	"-c", "diff.external=",
	"-c", "protocol.ext.allow=never",
	"-c", "commit.gpgsign=false",
	"--no-optional-locks",
}

func (ExecGit) Run(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append(append([]string{}, hardening...), args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "GIT_EXTERNAL_DIFF=", "GIT_PAGER=cat", "LC_ALL=C")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return strings.TrimSpace(out.String()), &gitError{args: args, stderr: strings.TrimSpace(errb.String()), err: err}
	}
	return strings.TrimRight(out.String(), "\n"), nil
}

type gitError struct {
	args   []string
	stderr string
	err    error
}

func (e *gitError) Error() string {
	sub := "git"
	if len(e.args) > 0 {
		sub = "git " + e.args[0]
	}
	if e.stderr != "" {
		return sub + ": " + firstLine(e.stderr)
	}
	return sub + ": " + e.err.Error()
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// DefaultWorktreeRoot is the controlled folder of coding-task worktrees, with
// every symlink along its path already resolved (Worktrees refuses a root that
// still has one). The daemon and the CLI must agree on it.
func DefaultWorktreeRoot() string {
	root := filepath.Join(WorkDirRoot(), "worktrees")
	_ = os.MkdirAll(root, 0o700)
	if real, err := filepath.EvalSymlinks(root); err == nil {
		return real
	}
	return root
}

// WorktreeInfo is one task's isolated checkout.
type WorktreeInfo struct {
	TaskID       string `json:"taskId"`
	Repo         string `json:"repo"`
	Path         string `json:"path"`
	Branch       string `json:"branch"`
	TargetBranch string `json:"targetBranch"`
	BaseHead     string `json:"baseHead"`
	State        string `json:"state"` // active | removed
}

// Worktrees owns the controlled root and the per-task records.
type Worktrees struct {
	Store *Store
	// Root holds one folder per task. It must be a real directory path: no
	// symlink may sit anywhere along it.
	Root string
	Git  Git
}

func (w *Worktrees) git() Git {
	if w.Git != nil {
		return w.Git
	}
	return ExecGit{}
}

var safeID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
var slugBad = regexp.MustCompile(`[^a-z0-9]+`)

func branchFor(task Task) string {
	slug := strings.Trim(slugBad.ReplaceAllString(strings.ToLower(task.Title), "-"), "-")
	if len(slug) > 30 {
		slug = strings.Trim(slug[:30], "-")
	}
	if slug == "" {
		return "solco/" + task.ID
	}
	return "solco/" + task.ID + "-" + slug
}

// root validates the controlled root and returns it.
func (w *Worktrees) root() (string, error) {
	if w.Root == "" || !filepath.IsAbs(w.Root) {
		return "", fmt.Errorf("%w: the worktree root must be an absolute path", ErrInvalid)
	}
	clean := filepath.Clean(w.Root)
	if err := os.MkdirAll(clean, 0o700); err != nil {
		return "", fmt.Errorf("%w: worktree root: %v", ErrInvalid, err)
	}
	real, err := filepath.EvalSymlinks(clean)
	if err != nil || real != clean {
		return "", fmt.Errorf("%w: the worktree root must not pass through a symlink", ErrInvalid)
	}
	return clean, nil
}

// pathFor is the one place a task's folder is computed; it re-checks the id
// and that the answer is a direct child of the root.
func (w *Worktrees) pathFor(root, taskID string) (string, error) {
	if !safeID.MatchString(taskID) {
		return "", fmt.Errorf("%w: %q is not a task id", ErrInvalid, taskID)
	}
	p := filepath.Join(root, taskID)
	if filepath.Dir(p) != root {
		return "", fmt.Errorf("%w: the worktree would leave its root", ErrInvalid)
	}
	return p, nil
}

func within(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Prepare makes (or finds again) the task's worktree. It is idempotent: a
// retry or a continue lands in the same folder with the same baseline.
func (w *Worktrees) Prepare(task Task) (WorktreeInfo, error) {
	root, err := w.root()
	if err != nil {
		return WorktreeInfo{}, err
	}
	path, err := w.pathFor(root, task.ID)
	if err != nil {
		return WorktreeInfo{}, err
	}
	repo, err := w.checkRepo(task.RepoPath, root)
	if err != nil {
		return WorktreeInfo{}, err
	}
	g := w.git()
	target, err := g.Run(repo, "symbolic-ref", "--short", "-q", "HEAD")
	if err != nil || target == "" {
		return WorktreeInfo{}, fmt.Errorf("%w: the repository is on a detached HEAD; check out a branch first", ErrInvalid)
	}

	if prev, ok, err := w.Get(task.ID); err != nil {
		return WorktreeInfo{}, err
	} else if ok && prev.State == "active" {
		if prev.Path != path || prev.Repo != repo {
			return WorktreeInfo{}, fmt.Errorf("%w: the recorded worktree does not match this task", ErrBadState)
		}
		if w.registered(repo, path) {
			return prev, nil
		}
		// The folder was removed under us: forget the registration and rebuild
		// on the branch the task already has.
		_, _ = g.Run(repo, "worktree", "prune")
	}

	branch := branchFor(task)
	if prev, ok, _ := w.Get(task.ID); ok && prev.Branch != "" {
		branch = prev.Branch
	}
	baseHead, err := g.Run(repo, "rev-parse", "--verify", "-q", "HEAD^{commit}")
	if err != nil || baseHead == "" {
		return WorktreeInfo{}, fmt.Errorf("%w: the repository has no commit to start from", ErrInvalid)
	}
	if prev, ok, _ := w.Get(task.ID); ok && prev.BaseHead != "" {
		baseHead = prev.BaseHead // the baseline never drifts across retries
	}

	if _, err := os.Lstat(path); err == nil {
		// Something is already there. Only a worktree we can prove is ours and
		// unchanged is adopted (a crash between `git worktree add` and the
		// record); anything else is left strictly alone.
		if w.registered(repo, path) && w.onBranch(path, branch) {
			return w.record(task, repo, path, branch, target, baseHead)
		}
		return WorktreeInfo{}, fmt.Errorf("%w: %s already exists and is not this task's worktree", ErrBadState, path)
	}
	args := []string{"worktree", "add", "-b", branch, path, baseHead}
	if w.branchExists(repo, branch) {
		args = []string{"worktree", "add", path, branch}
	}
	if _, err := g.Run(repo, args...); err != nil {
		return WorktreeInfo{}, fmt.Errorf("%w: creating the worktree: %v", ErrInvalid, err)
	}
	info, err := w.record(task, repo, path, branch, target, baseHead)
	if err != nil {
		_, _ = g.Run(repo, "worktree", "remove", "--force", path)
		return WorktreeInfo{}, err
	}
	return info, nil
}

// checkRepo resolves the repository root the task names and refuses anything
// that is not exactly one: not a repo, bare, a subfolder, empty, or inside the
// controlled root.
func (w *Worktrees) checkRepo(path, root string) (string, error) {
	if strings.TrimSpace(path) == "" || !filepath.IsAbs(path) {
		return "", fmt.Errorf("%w: a coding task needs an absolute repository path", ErrInvalid)
	}
	real, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("%w: repository: %v", ErrInvalid, err)
	}
	if within(root, real) || within(real, root) {
		return "", fmt.Errorf("%w: the repository and the worktree root must be separate folders", ErrInvalid)
	}
	g := w.git()
	if bare, err := g.Run(real, "rev-parse", "--is-bare-repository"); err != nil {
		return "", fmt.Errorf("%w: %s is not a git repository", ErrInvalid, real)
	} else if bare == "true" {
		return "", fmt.Errorf("%w: a bare repository has no checkout to branch from", ErrInvalid)
	}
	top, err := g.Run(real, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("%w: %s is not a git repository", ErrInvalid, real)
	}
	if topReal, err := filepath.EvalSymlinks(top); err != nil || topReal != real {
		return "", fmt.Errorf("%w: pass the repository root, not a folder inside it", ErrInvalid)
	}
	return real, nil
}

func (w *Worktrees) branchExists(repo, branch string) bool {
	_, err := w.git().Run(repo, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

func (w *Worktrees) onBranch(path, branch string) bool {
	got, err := w.git().Run(path, "symbolic-ref", "--short", "-q", "HEAD")
	return err == nil && got == branch
}

// registered says whether git itself lists path as a linked worktree of repo.
func (w *Worktrees) registered(repo, path string) bool {
	out, err := w.git().Run(repo, "worktree", "list", "--porcelain")
	if err != nil {
		return false
	}
	want := path
	if real, err := filepath.EvalSymlinks(path); err == nil {
		want = real
	}
	first := true
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "worktree ") {
			continue
		}
		p := strings.TrimPrefix(line, "worktree ")
		if first { // the main checkout is never a task worktree
			first = false
			continue
		}
		if real, err := filepath.EvalSymlinks(p); err == nil {
			p = real
		}
		if p == want {
			return true
		}
	}
	return false
}

func (w *Worktrees) record(task Task, repo, path, branch, target, baseHead string) (WorktreeInfo, error) {
	now := w.Store.nowMS()
	_, err := w.Store.db.Exec(`INSERT INTO worktrees(task_id,repo,path,branch,target_branch,base_head,state,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?)
		ON CONFLICT(task_id) DO UPDATE SET repo=excluded.repo, path=excluded.path, branch=excluded.branch,
			target_branch=excluded.target_branch, base_head=excluded.base_head, state='active', updated_at=excluded.updated_at`,
		task.ID, repo, path, branch, target, baseHead, "active", now, now)
	if err != nil {
		return WorktreeInfo{}, wrapBusy(err)
	}
	return WorktreeInfo{TaskID: task.ID, Repo: repo, Path: path, Branch: branch, TargetBranch: target, BaseHead: baseHead, State: "active"}, nil
}

// Get returns a task's worktree record.
func (w *Worktrees) Get(taskID string) (WorktreeInfo, bool, error) {
	var i WorktreeInfo
	err := w.Store.db.QueryRow(`SELECT task_id,repo,path,branch,target_branch,base_head,state FROM worktrees WHERE task_id=?`, taskID).
		Scan(&i.TaskID, &i.Repo, &i.Path, &i.Branch, &i.TargetBranch, &i.BaseHead, &i.State)
	if errors.Is(err, sql.ErrNoRows) {
		return i, false, nil
	}
	return i, err == nil, wrapBusy(err)
}

// Status lists uncommitted paths in the worktree (empty = clean). Names are
// NUL-separated by git, so a name with a tab, newline or quote stays literal.
func (w *Worktrees) Status(info WorktreeInfo) ([]string, error) {
	out, err := w.git().Run(info.Path, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, e := range strings.Split(out, "\x00") {
		if len(e) > 3 {
			paths = append(paths, e[3:])
		}
	}
	return paths, nil
}

// Cleanup removes a task's worktree and branch. Without force it keeps
// everything that holds work: uncommitted changes, or commits the target
// branch does not contain. The path is re-validated from scratch, so a damaged
// record can never aim a removal at the main checkout or outside the root.
func (w *Worktrees) Cleanup(taskID string, force bool) error {
	info, ok, err := w.Get(taskID)
	if err != nil {
		return err
	}
	if !ok || info.State != "active" {
		return nil
	}
	root, err := w.root()
	if err != nil {
		return err
	}
	want, err := w.pathFor(root, taskID)
	if err != nil {
		return err
	}
	if info.Path != want || !within(root, info.Path) {
		return fmt.Errorf("%w: the recorded path is not this task's worktree", ErrInvalid)
	}
	g := w.git()
	if _, err := os.Lstat(info.Path); err == nil {
		if !w.registered(info.Repo, info.Path) {
			return fmt.Errorf("%w: %s is not a worktree of the repository", ErrInvalid, info.Path)
		}
		if !force {
			if dirty, err := w.Status(info); err != nil {
				return err
			} else if len(dirty) > 0 {
				return fmt.Errorf("%w: the worktree has uncommitted changes", ErrBadState)
			}
		}
		rm := []string{"worktree", "remove", info.Path}
		if force {
			rm = []string{"worktree", "remove", "--force", info.Path}
		}
		if !force {
			if _, err := g.Run(info.Repo, "merge-base", "--is-ancestor", info.Branch, info.TargetBranch); err != nil {
				return fmt.Errorf("%w: the branch has commits %s does not contain", ErrBadState, info.TargetBranch)
			}
		}
		if _, err := g.Run(info.Repo, rm...); err != nil {
			return fmt.Errorf("%w: removing the worktree: %v", ErrBadState, err)
		}
	} else {
		_, _ = g.Run(info.Repo, "worktree", "prune")
	}
	if w.branchExists(info.Repo, info.Branch) {
		del := "-d"
		if force {
			del = "-D"
		}
		if _, err := g.Run(info.Repo, "branch", del, info.Branch); err != nil {
			return fmt.Errorf("%w: removing the branch: %v", ErrBadState, err)
		}
	}
	_, err = w.Store.db.Exec(`UPDATE worktrees SET state='removed', updated_at=? WHERE task_id=?`, w.Store.nowMS(), taskID)
	return wrapBusy(err)
}
