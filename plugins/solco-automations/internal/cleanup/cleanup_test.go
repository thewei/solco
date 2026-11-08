package cleanup

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/thewei/solco/plugins/solco-automations/internal/config"
	"github.com/thewei/solco/plugins/solco-automations/internal/herdr"
)

// ── the production adapter, against a real repo ─────────────────────

// repoWithBranches builds a repo on main plus two run branches: one that never
// committed anything, one carrying work of its own.
func repoWithBranches(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	script := [][]string{
		{"init", "-b", "main"},
		{"-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "--allow-empty", "-m", "base"},
		{"branch", "auto/did-nothing"},
		{"checkout", "-b", "auto/did-work"},
		{"-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "--allow-empty", "-m", "work"},
		{"checkout", "main"},
	}
	for _, args := range script {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func TestVerdict(t *testing.T) {
	repo := repoWithBranches(t)
	r := gitRepos{}
	base, err := r.DefaultBranch(repo)
	if err != nil {
		t.Fatal(err)
	}
	if base != "main" {
		t.Fatalf("expected the main fallback, got %q", base)
	}

	cases := []struct {
		name string
		wt   herdr.Worktree
		want Verdict
	}{
		{"produced nothing", herdr.Worktree{Branch: "auto/did-nothing"}, Removable},
		{"has its own commits", herdr.Worktree{Branch: "auto/did-work"}, KeptUnmerged},
		{
			// An open workspace outranks everything: it means nobody has read
			// the run yet, whatever git thinks of the branch.
			"still open",
			herdr.Worktree{Branch: "auto/did-nothing", OpenWorkspaceID: "w42"},
			KeptOpen,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := verdict(r, repo, c.wt, base, 0, time.Now()); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

// ── the plan, against a fake ────────────────────────────────────────

// fakeRepos answers for whatever worktrees the test declares, with no repo on
// disk. merged names the branches git would call ancestors of the default one.
type fakeRepos struct {
	worktrees map[string][]herdr.Worktree
	merged    map[string]bool
	// born dates each branch's head; the zero time means "cannot tell".
	born    map[string]time.Time
	baseErr error
	// baseErrFor fails the default-branch lookup of single repos only.
	baseErrFor map[string]error
	listErr    error
	removeErr  map[string]error

	removed []string
	visits  int
}

func (f *fakeRepos) Worktrees(repo string) ([]herdr.Worktree, error) {
	f.visits++
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.worktrees[repo], nil
}

func (f *fakeRepos) DefaultBranch(repo string) (string, error) {
	if err := f.baseErrFor[repo]; err != nil {
		return "", err
	}
	if f.baseErr != nil {
		return "", f.baseErr
	}
	return "main", nil
}

func (f *fakeRepos) Merged(_, branch, _ string) bool       { return f.merged[branch] }
func (f *fakeRepos) BranchTime(_, branch string) time.Time { return f.born[branch] }

func (f *fakeRepos) Remove(c Candidate) error {
	if err := f.removeErr[c.Branch]; err != nil {
		return err
	}
	f.removed = append(f.removed, c.Branch)
	return nil
}

func cfgOf(repos ...string) *config.Config {
	cfg := &config.Config{}
	for i, r := range repos {
		cfg.Automations = append(cfg.Automations,
			config.Automation{Name: string(rune('a' + i)), Repo: r})
	}
	return cfg
}

func TestScanSortsWorktreesIntoRemovableAndKept(t *testing.T) {
	r := &fakeRepos{
		worktrees: map[string][]herdr.Worktree{"/repo": {
			{Branch: "main", Path: "/repo"},
			{Branch: "auto/landed", Path: "/wt/1"},
			{Branch: "auto/open", Path: "/wt/2", OpenWorkspaceID: "w1"},
			{Branch: "auto/unmerged", Path: "/wt/3"},
			{Branch: "feature/mine", Path: "/wt/4"},
		}},
		merged: map[string]bool{"auto/landed": true},
	}

	plan, err := scan(cfgOf("/repo"), r, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Removable) != 1 || plan.Removable[0].Branch != "auto/landed" {
		t.Fatalf("removable = %+v, want just auto/landed", plan.Removable)
	}
	if len(plan.Kept) != 2 {
		t.Fatalf("kept = %+v, want the open one and the unmerged one", plan.Kept)
	}
	// The source checkout and somebody's own feature branch are never ours.
	for _, c := range plan.All() {
		if !strings.HasPrefix(c.Branch, BranchPrefix) {
			t.Errorf("%s is not a run worktree and must be left alone", c.Branch)
		}
	}
}

func TestScanVisitsARepoOnceEvenWhenAutomationsShareIt(t *testing.T) {
	r := &fakeRepos{worktrees: map[string][]herdr.Worktree{"/repo": nil}}
	if _, err := scan(cfgOf("/repo", "/repo", "/repo"), r, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	if r.visits != 1 {
		t.Fatalf("visited %d times, want 1", r.visits)
	}
}

func TestScanFailsLoudlyWhenItCannotTellWhichBranchIsDefault(t *testing.T) {
	// Guessing here would classify unmerged work as removable.
	r := &fakeRepos{
		worktrees: map[string][]herdr.Worktree{"/repo": {{Branch: "auto/x"}}},
		baseErr:   errors.New("cannot tell which branch is the default one"),
	}
	if _, err := scan(cfgOf("/repo"), r, nil, time.Now()); err == nil {
		t.Fatal("want an error rather than a guess")
	}
}

func TestSummaryTellsAnEmptyBoardApartFromOneWithNothingToRemove(t *testing.T) {
	// The distinction the board used to lose: "nothing to remove" reads as "no
	// run worktrees exist", and sends you hunting for worktrees that are right
	// there, held back for a reason.
	empty := Plan{}
	if got := empty.Summary(); got != "no run worktrees" {
		t.Errorf("empty summary = %q", got)
	}

	held := Plan{Kept: []Candidate{
		{Branch: "auto/a", Verdict: KeptOpen},
		{Branch: "auto/b", Verdict: KeptOpen},
		{Branch: "auto/c", Verdict: KeptUnmerged},
	}}
	got := held.Summary()
	if !strings.HasPrefix(got, "nothing to remove: ") {
		t.Fatalf("summary = %q", got)
	}
	if !strings.Contains(got, "2 workspace still open") ||
		!strings.Contains(got, "1 commits not in the default branch") {
		t.Errorf("summary = %q, want it to name what is being held and why", got)
	}
}

func TestSummaryCountsEverythingWhenSomethingCanGo(t *testing.T) {
	plan := Plan{
		Removable: []Candidate{{Branch: "auto/a", Verdict: Removable}},
		Kept:      []Candidate{{Branch: "auto/b", Verdict: KeptOpen}},
	}
	got := plan.Summary()
	if !strings.HasPrefix(got, "2 run worktrees: ") {
		t.Fatalf("summary = %q", got)
	}
	if !strings.Contains(got, "1 merged") {
		t.Errorf("summary = %q, want the removable ones named", got)
	}
}

func TestApplyRemovesOnlyWhatThePlanSaidItWould(t *testing.T) {
	r := &fakeRepos{
		worktrees: map[string][]herdr.Worktree{"/repo": {
			{Branch: "auto/landed", Path: "/wt/1"},
			{Branch: "auto/open", Path: "/wt/2", OpenWorkspaceID: "w1"},
		}},
		merged: map[string]bool{"auto/landed": true},
	}
	plan, err := scan(cfgOf("/repo"), r, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	var announced []string
	removed, err := plan.Apply(func(c Candidate) { announced = append(announced, c.Branch) })
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 || len(r.removed) != 1 || r.removed[0] != "auto/landed" {
		t.Fatalf("removed %v, want just auto/landed", r.removed)
	}
	if len(announced) != 1 {
		t.Errorf("announced %v, want each removal reported once", announced)
	}
}

func TestApplyStopsAtTheFirstRefusal(t *testing.T) {
	// git declining a dirty checkout means the plan was wrong about something.
	// Ploughing on would turn one readable refusal into a tally.
	r := &fakeRepos{
		worktrees: map[string][]herdr.Worktree{"/repo": {
			{Branch: "auto/a", Path: "/wt/a"},
			{Branch: "auto/b", Path: "/wt/b"},
			{Branch: "auto/c", Path: "/wt/c"},
		}},
		merged:    map[string]bool{"auto/a": true, "auto/b": true, "auto/c": true},
		removeErr: map[string]error{"auto/b": errors.New("worktree contains modified files")},
	}
	plan, err := scan(cfgOf("/repo"), r, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	removed, err := plan.Apply(nil)
	if err == nil {
		t.Fatal("want the refusal returned")
	}
	if removed != 1 {
		t.Errorf("removed = %d, want the one that went before the refusal", removed)
	}
	if len(r.removed) != 1 {
		t.Errorf("removed %v, want it to have stopped", r.removed)
	}
}

// ── the cleanup TTL ─────────────────────────────────────────────────

func TestTTLOnlyTakesMergedWorktreesPastTheirAge(t *testing.T) {
	now := time.Now()
	old := now.Add(-48 * time.Hour)
	r := &fakeRepos{
		worktrees: map[string][]herdr.Worktree{"/repo": {
			{Branch: "auto/cleanup-aged", Path: "/wt/1"},
			{Branch: "auto/cleanup-fresh", Path: "/wt/2"},
			{Branch: "auto/cleanup-unmerged", Path: "/wt/3"},
			{Branch: "auto/cleanup-undated", Path: "/wt/4"},
		}},
		merged: map[string]bool{
			"auto/cleanup-aged": true, "auto/cleanup-fresh": true, "auto/cleanup-undated": true,
		},
		born: map[string]time.Time{
			"auto/cleanup-aged":     old,
			"auto/cleanup-fresh":    now.Add(-time.Hour),
			"auto/cleanup-unmerged": old,
		},
	}
	cfg := cfgOf("/repo")
	cfg.Automations[0].Name = "cleanup"
	cfg.Automations[0].CleanupTTLHours = 24

	plan, err := scan(cfg, r, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Removable) != 1 || plan.Removable[0].Branch != "auto/cleanup-aged" {
		t.Fatalf("removable = %+v, want just the old merged one", plan.Removable)
	}
	for _, c := range plan.Kept {
		if c.Branch == "auto/cleanup-unmerged" {
			continue // unmerged outranks the TTL, whichever way its age points
		}
		if c.Verdict != KeptYoung {
			t.Errorf("%s kept as %q, want within cleanup ttl", c.Branch, c.Verdict)
		}
	}
}

func TestTTLOverrideReplacesEveryAutomationsOwn(t *testing.T) {
	now := time.Now()
	r := &fakeRepos{
		worktrees: map[string][]herdr.Worktree{"/repo": {
			{Branch: "auto/cleanup-aged", Path: "/wt/1"},
		}},
		merged: map[string]bool{"auto/cleanup-aged": true},
		born:   map[string]time.Time{"auto/cleanup-aged": now.Add(-2 * time.Hour)},
	}
	cfg := cfgOf("/repo")
	cfg.Automations[0].Name = "cleanup"
	cfg.Automations[0].CleanupTTLHours = 24 // would keep it; the override does not

	plan, err := scan(cfg, r, func(string) time.Duration { return time.Hour }, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Removable) != 1 {
		t.Fatalf("removable = %+v, want the 2h-old branch under a 1h override", plan.Removable)
	}
}

// Gap L07: the Assistants' own task worktrees are tidied by the same rules —
// merged, workspace closed, old enough — but only the branches the core
// recorded for its runs, never another `auto/` branch in the same repo.
func TestScanOwnedTouchesOnlyTheNamedBranches(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	old := now.Add(-30 * 24 * time.Hour)
	r := &fakeRepos{
		worktrees: map[string][]herdr.Worktree{"/repo": {
			{Branch: "auto/r-1-mine", Path: "/wt/1"},
			{Branch: "auto/r-2-young", Path: "/wt/2"},
			{Branch: "auto/r-3-open", Path: "/wt/3", OpenWorkspaceID: "w9"},
			{Branch: "auto/r-4-unmerged", Path: "/wt/4"},
			{Branch: "auto/someone-elses", Path: "/wt/5"}, // an automation's branch, not recorded
		}},
		merged: map[string]bool{"auto/r-1-mine": true, "auto/r-2-young": true, "auto/r-3-open": true, "auto/someone-elses": true},
		born: map[string]time.Time{
			"auto/r-1-mine": old, "auto/r-2-young": now.Add(-time.Hour), "auto/r-3-open": old,
			"auto/r-4-unmerged": old, "auto/someone-elses": old,
		},
	}
	owned := map[string][]string{"/repo": {"auto/r-1-mine", "auto/r-2-young", "auto/r-3-open", "auto/r-4-unmerged"}}
	plan, err := scanOwned(owned, r, 14*24*time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Removable) != 1 || plan.Removable[0].Branch != "auto/r-1-mine" {
		t.Fatalf("removable = %+v, want only the merged, closed, old one", plan.Removable)
	}
	verdicts := map[string]Verdict{}
	for _, c := range plan.Kept {
		verdicts[c.Branch] = c.Verdict
	}
	if verdicts["auto/r-2-young"] != KeptYoung || verdicts["auto/r-3-open"] != KeptOpen || verdicts["auto/r-4-unmerged"] != KeptUnmerged {
		t.Fatalf("kept = %+v", plan.Kept)
	}
	if _, listed := verdicts["auto/someone-elses"]; listed {
		t.Fatal("a branch the core never recorded was classified")
	}
	if none, _ := scanOwned(owned, r, 0, now); len(none.Removable)+len(none.Kept) != 0 {
		t.Fatalf("without an age rule nothing may be classified for removal: %+v", none)
	}
}

// One refusal (git declines a checkout with uncommitted work) must not stop the
// rest of the pass, and the refused one stays.
func TestApplyEachKeepsGoingPastARefusal(t *testing.T) {
	r := &fakeRepos{removeErr: map[string]error{"auto/dirty": errors.New("contains modified files")}}
	plan := Plan{repos: r, Removable: []Candidate{{Branch: "auto/dirty"}, {Branch: "auto/clean"}}}
	var refused []string
	removed := plan.ApplyEach(func(c Candidate, err error) { refused = append(refused, c.Branch) })
	if removed != 1 || len(r.removed) != 1 || r.removed[0] != "auto/clean" || len(refused) != 1 || refused[0] != "auto/dirty" {
		t.Fatalf("removed=%d %v refused=%v", removed, r.removed, refused)
	}
}

// The unattended daily pass must not be stopped for every repository by one
// whose default branch cannot be told (seen on a real Mac: a repo with no
// commits made the whole pass skip, every day). The interactive scan stays
// loud; the lenient one passes over that repo, names it, and cleans the rest.
func TestLenientScanSkipsOneUnreadableRepoAndStillCleansTheOthers(t *testing.T) {
	r := &fakeRepos{
		worktrees: map[string][]herdr.Worktree{
			"/bad":  {{Branch: "auto/x", Path: "/wt/x"}},
			"/good": {{Branch: "auto/landed", Path: "/wt/1"}},
		},
		merged:     map[string]bool{"auto/landed": true, "auto/x": true},
		baseErrFor: map[string]error{"/bad": errors.New("cannot tell which branch is the default one")},
	}
	if _, err := scan(cfgOf("/bad", "/good"), r, nil, time.Now()); err == nil {
		t.Fatal("the strict scan must still fail loudly")
	}
	plan, err := scanLenient(cfgOf("/bad", "/good"), r, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Removable) != 1 || plan.Removable[0].Branch != "auto/landed" {
		t.Fatalf("removable = %+v, want only the readable repo's landed branch", plan.Removable)
	}
	if len(plan.Skipped) != 1 || !strings.Contains(plan.Skipped[0], "/bad") {
		t.Fatalf("skipped = %v, want the unreadable repo named", plan.Skipped)
	}
}
