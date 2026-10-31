package remotehost

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"
)

type fakeGit struct {
	mu     sync.Mutex
	dirs   []string
	status GitStatus
	diff   GitDiff
	err    error
}

func (f *fakeGit) Status(_ context.Context, dir string) (GitStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dirs = append(f.dirs, dir)
	return f.status, f.err
}

func (f *fakeGit) Diff(_ context.Context, dir, path string) (GitDiff, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dirs = append(f.dirs, dir)
	if path == "gone.go" {
		return GitDiff{}, ErrGitFileNotFound
	}
	return f.diff, f.err
}

func sampleGit() *fakeGit {
	return &fakeGit{
		status: GitStatus{Repo: true, Branch: "main", Head: "abc1234", Upstream: &GitUpstream{Name: "origin/main", Ahead: 1},
			Files:     []GitFile{{Path: "a.go", Status: "modified", Unstaged: true, Additions: 2, Deletions: 1}},
			Additions: 2, Deletions: 1},
		diff: GitDiff{Path: "a.go", Status: "modified", Hunks: []GitHunk{{Header: "@@ -1,2 +1,3 @@", Lines: []GitLine{
			{Kind: " ", Old: 1, New: 1, Text: "package a"}, {Kind: "-", Old: 2, Text: "old"}, {Kind: "+", New: 2, Text: "new"}}}}},
	}
}

func gitHarness(t *testing.T) (*harness, *fakeGit) {
	t.Helper()
	h := newHarness(t)
	g := sampleGit()
	h.srv.opts.Git = g
	return h, g
}

func TestGitAbsentWithoutReader(t *testing.T) {
	h := newHarness(t)
	for _, path := range []string{"/api/v3/workspaces/w1/git", "/api/v3/workspaces/w1/git/diff?path=a.go"} {
		if resp := h.get(path, h.token, nil); resp.StatusCode != http.StatusNotFound || errCode(t, resp) != "git_unavailable" {
			t.Fatalf("%s: status %d", path, resp.StatusCode)
		}
	}
}

func TestGitNeedsAuth(t *testing.T) {
	h, g := gitHarness(t)
	if resp := h.get("/api/v3/workspaces/w1/git", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", resp.StatusCode)
	}
	if len(g.dirs) != 0 {
		t.Fatal("an unauthenticated read reached Git")
	}
}

func TestGitStatusUsesTheWorkspaceDirectory(t *testing.T) {
	h, g := gitHarness(t)
	resp := h.get("/api/v3/workspaces/w1/git", h.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var out GitStatus
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.WorkspaceID != "w1" || !out.Repo || out.Branch != "main" || len(out.Files) != 1 || out.Upstream == nil || out.Upstream.Ahead != 1 {
		t.Fatalf("status %+v", out)
	}
	if len(g.dirs) != 1 || g.dirs[0] != "/work/solco" {
		t.Fatalf("dirs %v", g.dirs)
	}
}

func TestGitNotARepoIsAnAnswerNotAnError(t *testing.T) {
	h, g := gitHarness(t)
	g.status = GitStatus{}
	resp := h.get("/api/v3/workspaces/w1/git", h.token, nil)
	var raw map[string]json.RawMessage
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&raw) != nil || string(raw["repo"]) != "false" || string(raw["files"]) != "[]" {
		t.Fatalf("status %d body %s", resp.StatusCode, raw)
	}
}

func TestGitUnknownWorkspaceInstanceAndFile(t *testing.T) {
	h, _ := gitHarness(t)
	if resp := h.get("/api/v3/workspaces/w9/git", h.token, nil); resp.StatusCode != http.StatusNotFound || errCode(t, resp) != "unknown_workspace" {
		t.Fatalf("ghost workspace: %d", resp.StatusCode)
	}
	if resp := h.get("/api/v3/workspaces/w1/git?instance=ghost", h.token, nil); resp.StatusCode != http.StatusNotFound || errCode(t, resp) != "unknown_instance" {
		t.Fatalf("ghost instance: %d", resp.StatusCode)
	}
	if resp := h.get("/api/v3/workspaces/w1/git/diff", h.token, nil); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing path: %d", resp.StatusCode)
	}
	if resp := h.get("/api/v3/workspaces/w1/git/diff?path=gone.go", h.token, nil); resp.StatusCode != http.StatusNotFound || errCode(t, resp) != "unknown_file" {
		t.Fatalf("gone file: %d", resp.StatusCode)
	}
}

func TestGitFailureIsOpaque(t *testing.T) {
	h, g := gitHarness(t)
	g.err = errors.New("fatal: /secret/path exploded")
	resp := h.get("/api/v3/workspaces/w1/git", h.token, nil)
	if resp.StatusCode != http.StatusBadGateway || errCode(t, resp) != "git_failed" {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestGitBootstrapAdvertisesCapability(t *testing.T) {
	h, _ := gitHarness(t)
	var body struct {
		Capabilities map[string]bool `json:"capabilities"`
	}
	if err := json.NewDecoder(h.get("/api/v3/bootstrap", h.token, nil).Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Capabilities["git"] {
		t.Fatalf("capabilities %v", body.Capabilities)
	}
}
