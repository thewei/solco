package nativeui

/**
 * [INPUT]: bounded selected Git review and a fake asynchronous API completion
 * [OUTPUT]: success/cancel/stale-workspace fences for native Commit suggestion; an unconfigured channel refuses to start
 * [POS]: UI coordinator tests, no provider API, no Keychain and no real Git mutation
 * [PROTOCOL]: update header then check CLAUDE.md
 */

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/settings"
)

// commitMapSecrets is the in-memory credential store for coordinator tests.
type commitMapSecrets map[string]string

func (m commitMapSecrets) Get(key string) (string, bool) { value, ok := m[key]; return value, ok }
func (m commitMapSecrets) Set(key, value string) error   { m[key] = value; return nil }
func (m commitMapSecrets) Delete(key string) error       { delete(m, key); return nil }

// commitTestShell is a Commit surface whose API channel is fully configured
// through an injected memory store; no Keychain and no network is touched.
func commitTestShell(t *testing.T) *Shell {
	t.Helper()
	s := gitSurfaceTestShell(t, t.TempDir())
	// These tests seed the review and mark it Captured by hand, so the
	// background Git snapshot and preflight loads are noise — and their
	// goroutines end in applyGuarded, reading uiApplyOverride while the test
	// installs its own. An empty root makes both loaders return before they
	// start a goroutine.
	root := s.git.root
	s.git.root = ""
	showChangesPanel(s)
	s.openCommitSurface()
	s.git.root = root
	s.surface.commit.Captured = true
	s.commitSecrets = commitMapSecrets{commitAPIKeyName: "test-key-123"}
	s.settings.Workbench.CommitAPI = settings.CommitAPI{Endpoint: "https://commit.test/v1", Model: "test-model"}
	s.commitAPIKeySet = true
	return s
}

func awaitCommitApply(t *testing.T, queue chan func()) {
	t.Helper()
	select {
	case f := <-queue:
		f()
	case <-time.After(3 * time.Second):
		t.Fatal("AI completion timeout")
	}
}

func TestCommitAIUsesSelectedSnapshotAndNeverCommits(t *testing.T) {
	s := commitTestShell(t)
	queue := make(chan func(), 1)
	s.uiApplyOverride = func(f func()) { queue <- f }
	s.commitGenExecutor = func(ctx context.Context, req agent.CommitRequest) (agent.CommitResponse, error) {
		if req.Endpoint != "https://commit.test/v1" || req.Model != "test-model" ||
			req.APIKey != "test-key-123" ||
			!strings.Contains(req.Prompt, "File: src/main.go") ||
			!strings.Contains(req.Prompt, "Conventional Commits") || !strings.Contains(req.Prompt, "Do NOT stage, commit, push") ||
			strings.Contains(req.Prompt, "fmt.Println") {
			return agent.CommitResponse{}, context.Canceled
		}
		return agent.CommitResponse{Message: "fix: update Git interface"}, nil
	}
	s.startCommitAISuggestion()
	if !s.commitGenRunning {
		t.Fatal("AI generation did not start")
	}
	awaitCommitApply(t, queue)
	if s.commitGenRunning || s.commitAIResponse != "fix: update Git interface" || s.surface.commit.Subject != "" || s.git.committing {
		t.Fatalf("AI must provide a candidate, not commit or silently overwrite: %q", s.commitAIResponse)
	}
	s.applyManualAISuggestion()
	if s.surface.commit.Subject != "fix: update Git interface" || s.surface.commit.InFlight {
		t.Fatal("candidate acceptance must not execute Git")
	}
}

func TestCommitAIRejectsStaleRepoAndCancel(t *testing.T) {
	s := commitTestShell(t)
	queue := make(chan func(), 2)
	s.uiApplyOverride = func(f func()) { queue <- f }
	s.commitGenExecutor = func(ctx context.Context, req agent.CommitRequest) (agent.CommitResponse, error) {
		return agent.CommitResponse{Message: "feat: stale data"}, nil
	}
	s.startCommitAISuggestion()
	s.git.root = t.TempDir()
	awaitCommitApply(t, queue)
	if s.commitAIResponse != "" || !strings.Contains(s.commitGenError, "changed") {
		t.Fatalf("old repository result applied: %q, %q", s.commitAIResponse, s.commitGenError)
	}
	s.git.root = s.projection.Projects[0].CWD
	s.startCommitAISuggestion()
	s.resetCommitAIFlow()
	awaitCommitApply(t, queue)
	if s.commitAIResponse != "" || s.commitGenRunning {
		t.Fatal("canceled AI result applied after editor closed")
	}
}

func TestCommitAIRefusesUnconfiguredChannel(t *testing.T) {
	s := commitTestShell(t)
	s.commitSecrets = commitMapSecrets{}
	s.commitAPIKeySet = false
	s.commitGenExecutor = func(context.Context, agent.CommitRequest) (agent.CommitResponse, error) {
		t.Error("generation ran without a configured channel")
		return agent.CommitResponse{}, nil
	}
	s.startCommitAISuggestion()
	if s.commitGenRunning || s.commitAIResponse != "" {
		t.Fatal("unconfigured channel started a generation")
	}
	if !strings.Contains(s.commitGenError, "Settings → Git") {
		t.Fatalf("unconfigured error = %q", s.commitGenError)
	}
}
