package config

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

// AUD-09: each CLI or Host request must operate on the latest YAML revision
// under the same process-independent lock.
func TestConcurrentDistinctPutsKeepEveryAutomation(t *testing.T) {
	withConfig(t, "automations: []\n")
	const count = 12
	start := make(chan struct{})
	errs := make(chan error, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			name := fmt.Sprintf("job-%d", i)
			result, err := Put("", Automation{Name: name, Cron: "@daily", Repo: "/tmp", Prompt: "audit"}, false)
			if err == nil && result.Problem != "" {
				err = fmt.Errorf("%s: %s", name, result.Problem)
			}
			if err != nil {
				errs <- err
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("write error: %v", err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Automations) != count {
		t.Fatalf("lost updates: %d of %d automations", len(cfg.Automations), count)
	}
}

// Each test subprocess uses its own file descriptor/SQLite-independent CLI
// process, exercising the advisory flock across process boundaries.
func TestConcurrentPutSubprocessHelper(t *testing.T) {
	name := os.Getenv("AUDIT_PUT_NAME")
	if name == "" {
		return
	}
	result, err := Put("", Automation{Name: name, Cron: "@daily", Repo: "/tmp", Prompt: "audit"}, false)
	if err != nil || result.Problem != "" {
		t.Fatalf("%s: %v %s", name, err, result.Problem)
	}
}

func TestConcurrentDifferentProcessesKeepEveryAutomation(t *testing.T) {
	withConfig(t, "automations: []\n")
	const count = 6
	start := make(chan struct{})
	errs := make(chan error, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			name := fmt.Sprintf("process-%d", i)
			cmd := exec.Command(os.Args[0], "-test.run=^TestConcurrentPutSubprocessHelper$")
			cmd.Env = append(os.Environ(), "AUDIT_PUT_NAME="+name)
			if out, err := cmd.CombinedOutput(); err != nil {
				errs <- fmt.Errorf("%s: %v: %s", name, err, out)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Automations) != count {
		t.Fatalf("cross-process lost updates: %d/%d", len(cfg.Automations), count)
	}
}

func TestSaveRefusesStaleSnapshotAfterConcurrentPut(t *testing.T) {
	withConfig(t, "automations: []\n")
	stale, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Put("", Automation{Name: "new-one", Cron: "@daily", Repo: "/tmp", Prompt: "new"}, false); err != nil {
		t.Fatal(err)
	}
	stale.Paused = true
	if err := Save(stale); !errors.Is(err, ErrDefinitionChanged) {
		t.Fatalf("stale whole-file save: %v", err)
	}
	current, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if current.Find("new-one") == nil || current.Paused {
		t.Fatalf("stale save overwrote other edits: %+v", current)
	}
}

// A recovery process may restore a Draft flag only for the exact prompt
// originally checked. A stale recovery may not overwrite a later edit.
func TestRestoreDraftIfRefusesChangedDefinition(t *testing.T) {
	withConfig(t, "")
	initial := Automation{Name: "job", Cron: "@daily", Repo: "/tmp", Prompt: "Original", Agent: "claude", Draft: true}
	if result, err := Put("", initial, false); err != nil || result.Problem != "" {
		t.Fatalf("create draft: %+v %v", result, err)
	}
	if err := Approve("job"); err != nil {
		t.Fatal(err)
	}
	if err := RestoreDraftIf("job", func(a Automation) bool { return a.Prompt == "Original" }); err != nil {
		t.Fatalf("restore same definition: %v", err)
	}
	cfg, err := Load()
	if err != nil || !cfg.Find("job").Draft {
		t.Fatalf("failed to restore Draft: %+v %v", cfg, err)
	}
	updated := *cfg.Find("job")
	updated.Prompt = "Changed after original approval"
	updated.Draft = false
	if result, err := Put("job", updated, false); err != nil || result.Problem != "" {
		t.Fatalf("edit: %+v %v", result, err)
	}
	if err := RestoreDraftIf("job", func(a Automation) bool { return a.Prompt == "Original" }); !errors.Is(err, ErrDefinitionChanged) {
		t.Fatalf("stale repair did not refuse changed prompt: %v", err)
	}
	cfg, err = Load()
	if err != nil || cfg.Find("job").Prompt != updated.Prompt || cfg.Find("job").Draft {
		t.Fatalf("stale repair overwrote prompt or Draft: %+v %v", cfg.Find("job"), err)
	}
}

// Atomic writes must not leave temp files, and the YAML containing
// unattended commands must be private to the local account after replacement.
func TestAtomicConfigWriteLeavesRestrictedFileAndNoTempFiles(t *testing.T) {
	withConfig(t, "automations: []\n")
	a := Automation{Name: "job", Cron: "@daily", Repo: "/tmp", Prompt: "private command", Draft: true}
	for i := 0; i < 2; i++ {
		original := ""
		if i > 0 {
			original = "job"
		}
		result, err := Put(original, a, false)
		if err != nil || result.Problem != "" {
			t.Fatalf("write %d: %+v %v", i, result, err)
		}
	}
	info, err := os.Stat(Path())
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("config mode=%o, want 0600", got)
	}
	entries, err := os.ReadDir(Dir())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".automations-") && strings.HasSuffix(e.Name(), ".yaml") {
			t.Fatalf("atomic write leaked a temp file: %s", e.Name())
		}
	}
}

// AUD-18: the action actually cleared by ApproveIf must be the one whose
// normalized definition was checked, rather than a later YAML revision.
func TestApproveIfRefusesChangedDraft(t *testing.T) {
	withConfig(t, "")
	original := Automation{Name: "daily-review", Cron: "@daily", Repo: "/tmp", Agent: "claude", Prompt: "owner-approved prompt", Draft: true}
	if _, err := Put("", original, false); err != nil {
		t.Fatal(err)
	}
	err := ApproveIf("daily-review", func(a Automation) bool { return a.Prompt == "owner-approved prompt" })
	if err != nil {
		t.Fatalf("approved unchanged draft: %v", err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Find("daily-review").Draft {
		t.Fatal("approved draft not enabled")
	}
	// It must not clear the draft after a rejection.
	original.Prompt = "not the authorized text"
	original.Draft = true
	if _, err := Put("daily-review", original, false); err != nil {
		t.Fatal(err)
	}
	err = ApproveIf("daily-review", func(a Automation) bool { return a.Prompt == "owner-approved prompt" })
	if err == nil {
		t.Fatal("changed draft was approved")
	}
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Find("daily-review").Draft {
		t.Fatal("refused approval nevertheless enabled rule")
	}
}
