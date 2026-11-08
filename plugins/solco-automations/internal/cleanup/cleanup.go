// Package cleanup removes the worktrees runs leave behind — but only the ones
// whose work has demonstrably landed somewhere.
//
// Accumulating worktrees are not purely litter: a run whose workspace is still
// open is a run nobody has read, which makes Herdr's sidebar the inbox for
// unattended work. That is why nothing here runs on a schedule and why the
// default is to keep. A reaper that tidied on its own would delete the only
// signal saying which runs still want a human.
//
// Scan hands back a Plan rather than a list, because two frontends were
// otherwise both partitioning it, tallying it, phrasing a summary and looping
// the removals — four jobs each, done four different ways, with the wordings
// already drifted apart.
package cleanup

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/thewei/solco/plugins/solco-automations/internal/config"
	"github.com/thewei/solco/plugins/solco-automations/internal/herdr"
	"github.com/thewei/solco/plugins/solco-automations/internal/host"
)

// BranchPrefix marks the branches runs create; anything else in the repo is
// somebody's own work and is never a candidate.
const BranchPrefix = "auto/"

// Verdict is why a worktree is or isn't going away.
type Verdict string

const (
	// Removable: the branch is an ancestor of the default branch, so its
	// commits — if it made any — are already there.
	Removable Verdict = "merged"
	// KeptOpen: the workspace is still open. Whether the run was any good is
	// not something this command can know, so it doesn't guess.
	KeptOpen Verdict = "workspace still open"
	// KeptUnmerged: commits that exist nowhere else. Squash-merged branches
	// land here too, which is the safe direction to be wrong in.
	KeptUnmerged Verdict = "commits not in the default branch"
	// KeptYoung: merged, but inside its automation's cleanup TTL. The daily
	// pass comes for it when the TTL is up; until then the checkout stays
	// readable. An age that cannot be determined counts as young — cleanup
	// acts on evidence, and this is none.
	KeptYoung Verdict = "within cleanup ttl"
)

// Candidate is one run worktree and what should happen to it.
type Candidate struct {
	Repo    string
	Branch  string
	Path    string
	Verdict Verdict
}

func (c Candidate) removable() bool { return c.Verdict == Removable }

// Plan is every run worktree, already sorted into the ones that can go and the
// ones that stay, and able to phrase and carry out its own conclusion.
type Plan struct {
	// Removable is what Apply would remove.
	Removable []Candidate
	// Kept is everything held back, each with the reason why.
	Kept []Candidate
	// Skipped names repositories a lenient scan passed over because they could
	// not be read ("<repo>: <why>"); a strict scan never fills it.
	Skipped []string

	repos repos
}

// All is every candidate, removable first — the order the CLI lists them in.
func (p Plan) All() []Candidate {
	return append(append([]Candidate{}, p.Removable...), p.Kept...)
}

// Empty reports whether there are any run worktrees at all. Distinct from
// having nothing to remove, which is the distinction the board used to lose.
func (p Plan) Empty() bool { return len(p.Removable) == 0 && len(p.Kept) == 0 }

// Summary is one sentence of facts, phrased once for every frontend. It says
// nothing about what to do next: a TTY and a board word that differently.
func (p Plan) Summary() string {
	if p.Empty() {
		return "no run worktrees"
	}
	tally := p.tally()
	if len(p.Removable) == 0 {
		// "Nothing to remove" on its own reads as "no run worktrees exist",
		// which sends you looking for worktrees that are in fact right there,
		// held back for a reason worth naming.
		return "nothing to remove: " + tally
	}
	return fmt.Sprintf("%d run worktrees: %s", len(p.Removable)+len(p.Kept), tally)
}

// tally counts by verdict, removable first, then in the order encountered.
func (p Plan) tally() string {
	counts := map[Verdict]int{}
	var order []Verdict
	for _, c := range p.All() {
		if counts[c.Verdict] == 0 {
			order = append(order, c.Verdict)
		}
		counts[c.Verdict]++
	}
	parts := make([]string, 0, len(order))
	for _, v := range order {
		parts = append(parts, fmt.Sprintf("%d %s", counts[v], v))
	}
	return strings.Join(parts, ", ")
}

// Apply removes what can be removed, calling done for each one that goes.
//
// It stops at the first refusal rather than reporting a tally: git declines a
// dirty checkout and declines an unmerged branch, and those refusals are worth
// reading in full — they are the plan having been wrong about something.
func (p Plan) Apply(done func(Candidate)) (removed int, err error) {
	for _, c := range p.Removable {
		if err := p.repos.Remove(c); err != nil {
			return removed, err
		}
		removed++
		if done != nil {
			done(c)
		}
	}
	return removed, nil
}

// ApplyEach removes what can be removed and keeps going past a refusal: the
// unattended daily pass must not be stopped for good by one checkout git will
// not drop (uncommitted work). refused is called for each one that stays, and
// the number removed comes back.
func (p Plan) ApplyEach(refused func(Candidate, error)) (removed int) {
	for _, c := range p.Removable {
		if err := p.repos.Remove(c); err != nil {
			if refused != nil {
				refused(c, err)
			}
			continue
		}
		removed++
	}
	return removed
}

// ScanOwned classifies only the worktrees of the branches the caller owns
// (repo -> branches): the Assistants' own task runs, whose branches the core
// recorded. Every other branch in those repos — an automation's, a person's —
// is never looked at. ttl is how old a merged branch must be before it goes.
func ScanOwned(owned map[string][]string, ttl time.Duration, now time.Time) (Plan, error) {
	return scanOwned(owned, gitRepos{}, ttl, now)
}

// TidyOwned scans the owned branches and removes what the rules allow. refused
// is called for each checkout git declined (uncommitted work); it returns how
// many were removed.
func TidyOwned(owned map[string][]string, ttl time.Duration, now time.Time, refused func(Candidate, error)) (int, error) {
	plan, err := ScanOwned(owned, ttl, now)
	if err != nil {
		return 0, err
	}
	return plan.ApplyEach(refused), nil
}

func scanOwned(owned map[string][]string, r repos, ttl time.Duration, now time.Time) (Plan, error) {
	plan := Plan{repos: r}
	if ttl <= 0 {
		return plan, nil // no age rule, no removal: this pass never acts on "merged" alone
	}
	for repo, branches := range owned {
		mine := map[string]bool{}
		for _, b := range branches {
			mine[b] = true
		}
		worktrees, err := r.Worktrees(repo)
		if err != nil {
			continue // a repo that is gone or unreadable is not ours to guess about
		}
		base, err := r.DefaultBranch(repo)
		if err != nil {
			continue // without a default branch "merged" cannot be told: keep everything
		}
		for _, w := range worktrees {
			if !mine[w.Branch] || !strings.HasPrefix(w.Branch, BranchPrefix) {
				continue
			}
			c := Candidate{Repo: repo, Branch: w.Branch, Path: w.Path, Verdict: verdict(r, repo, w, base, ttl, now)}
			if c.removable() {
				plan.Removable = append(plan.Removable, c)
			} else {
				plan.Kept = append(plan.Kept, c)
			}
		}
	}
	return plan, nil
}

// Scan classifies every run worktree across the repos the config names, each
// candidate held to its own automation's cleanup_ttl_hours (0: no TTL, so
// merged worktrees stay removable at any age, as they always were).
func Scan(cfg *config.Config) (Plan, error) { return scan(cfg, gitRepos{}, nil, time.Now()) }

// ScanWithTTL scans like Scan, but ttl overrides every automation's own —
// the one-shot `cleanup --ttl N`. Zero or negative keeps per-automation TTLs.
func ScanWithTTL(cfg *config.Config, ttl time.Duration, now time.Time) (Plan, error) {
	return scan(cfg, gitRepos{}, func(string) time.Duration { return ttl }, now)
}

// repos is this package's internal seam: what it needs to know about a
// checkout, and the one destructive thing it does to one. A fake lets the
// classification be tested without building a git repo per case.
type repos interface {
	Worktrees(repo string) ([]herdr.Worktree, error)
	DefaultBranch(repo string) (string, error)
	Merged(repo, branch, base string) bool
	// BranchTime is when the branch's head was last committed. The zero time
	// when it cannot be told — which counts as young, never as old.
	BranchTime(repo, branch string) time.Time
	Remove(c Candidate) error
}

// ttlFor says how old a run branch may get before the TTL pass takes it,
// or nil when no TTL is in force anywhere.
type ttlFor func(branch string) time.Duration

// scan visits each repo once, even when several automations share one.
func scan(cfg *config.Config, r repos, ttl ttlFor, now time.Time) (Plan, error) {
	return scanRepos(cfg, r, ttl, now, false)
}

// scanLenient is scan for the unattended daily pass: a repository that cannot
// be read is named in Plan.Skipped and the others are still cleaned, instead of
// one bad repo stopping every automation's cleanup for good.
func scanLenient(cfg *config.Config, r repos, ttl ttlFor, now time.Time) (Plan, error) {
	return scanRepos(cfg, r, ttl, now, true)
}

// ScanLenient is the production entry for the daily pass; ttl as in ScanWithTTL.
func ScanLenient(cfg *config.Config, ttl time.Duration, now time.Time) (Plan, error) {
	return scanLenient(cfg, gitRepos{}, func(string) time.Duration { return ttl }, now)
}

func scanRepos(cfg *config.Config, r repos, ttl ttlFor, now time.Time, lenient bool) (Plan, error) {
	plan := Plan{repos: r}
	seen := map[string]bool{}
	for _, a := range cfg.Automations {
		if seen[a.Repo] {
			continue
		}
		seen[a.Repo] = true

		worktrees, err := r.Worktrees(a.Repo)
		if err != nil {
			err = fmt.Errorf("listing worktrees of %s: %w", a.Repo, err)
			if lenient {
				plan.Skipped = append(plan.Skipped, err.Error())
				continue
			}
			return Plan{}, err
		}
		base, err := r.DefaultBranch(a.Repo)
		if err != nil {
			err = fmt.Errorf("%s: %w", a.Repo, err)
			if lenient {
				plan.Skipped = append(plan.Skipped, err.Error())
				continue
			}
			return Plan{}, err
		}
		for _, w := range worktrees {
			if !strings.HasPrefix(w.Branch, BranchPrefix) {
				continue
			}
			c := Candidate{
				Repo: a.Repo, Branch: w.Branch, Path: w.Path,
				Verdict: verdict(r, a.Repo, w, base, branchTTL(ttl, cfg, w.Branch), now),
			}
			if c.removable() {
				plan.Removable = append(plan.Removable, c)
			} else {
				plan.Kept = append(plan.Kept, c)
			}
		}
	}
	return plan, nil
}

// branchTTL resolves the TTL a branch is held to: the explicit override when
// there is one, else its own automation's cleanup_ttl_hours.
func branchTTL(override ttlFor, cfg *config.Config, branch string) time.Duration {
	if override != nil {
		if d := override(branch); d > 0 {
			return d
		}
	}
	var best int
	ttl := time.Duration(0)
	for _, a := range cfg.Automations {
		p := BranchPrefix + host.Slug(a.Name) + "-"
		if strings.HasPrefix(branch, p) && len(p) > best {
			best = len(p)
			ttl = a.CleanupTTL()
		}
	}
	return ttl
}

func verdict(r repos, repo string, w herdr.Worktree, base string, ttl time.Duration, now time.Time) Verdict {
	if w.OpenWorkspaceID != "" {
		return KeptOpen
	}
	if !r.Merged(repo, w.Branch, base) {
		return KeptUnmerged
	}
	if ttl > 0 {
		born := r.BranchTime(repo, w.Branch)
		if born.IsZero() || now.Sub(born) <= ttl {
			return KeptYoung
		}
	}
	return Removable
}

// gitRepos is the production adapter: Herdr for the worktree list, git for
// everything about branches.
type gitRepos struct{}

func (gitRepos) Worktrees(repo string) ([]herdr.Worktree, error) {
	return herdr.Client{}.WorktreeList(repo)
}

// DefaultBranch asks the remote what it considers default, falling back to the
// usual names when the repo has no origin/HEAD — a fresh clone often doesn't.
func (gitRepos) DefaultBranch(repo string) (string, error) {
	out, err := output(repo, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if err == nil && out != "" {
		return out, nil
	}
	for _, candidate := range []string{"origin/main", "origin/master", "main", "master"} {
		if err := git(repo, "rev-parse", "--verify", "--quiet", candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("cannot tell which branch is the default one")
}

func (gitRepos) Merged(repo, branch, base string) bool {
	return git(repo, "merge-base", "--is-ancestor", branch, base) == nil
}

// BranchTime dates a branch by its head commit: for a run branch that is
// when the run (or the worktree it was cut from) last said anything. A branch
// git cannot date has no age worth acting on, so the zero time it gets
// counts as young.
func (gitRepos) BranchTime(repo, branch string) time.Time {
	out, err := output(repo, "log", "-1", "--format=%ct", branch)
	if err != nil {
		return time.Time{}
	}
	sec, err := strconv.ParseInt(out, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(sec, 0)
}

// Remove drops the checkout and then the branch. Neither step is forced: git
// refuses a dirty worktree and refuses an unmerged branch, and those refusals
// are worth more than the tidiness they cost.
func (gitRepos) Remove(c Candidate) error {
	if err := git(c.Repo, "worktree", "remove", c.Path); err != nil {
		return fmt.Errorf("%s: %w", c.Branch, err)
	}
	if err := git(c.Repo, "branch", "-d", c.Branch); err != nil {
		return fmt.Errorf("%s: checkout removed, branch kept: %w", c.Branch, err)
	}
	return nil
}

func git(repo string, args ...string) error {
	return exec.Command("git", append([]string{"-C", repo}, args...)...).Run()
}

func output(repo string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).Output()
	return strings.TrimSpace(string(out)), err
}
