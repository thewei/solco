package remotehost

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
)

type fakeGitWriter struct {
	mu    sync.Mutex
	calls []string
	dirs  []string
	err   error
}

func (f *fakeGitWriter) note(dir, call string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dirs = append(f.dirs, dir)
	f.calls = append(f.calls, call)
}

func (f *fakeGitWriter) Stage(_ context.Context, dir string, paths []string) error {
	f.note(dir, "stage "+strings.Join(paths, ","))
	if len(paths) == 1 && paths[0] == "gone.go" {
		return ErrGitFileNotFound
	}
	return f.err
}

func (f *fakeGitWriter) Unstage(_ context.Context, dir string, paths []string) error {
	f.note(dir, "unstage "+strings.Join(paths, ","))
	return f.err
}

func (f *fakeGitWriter) Commit(_ context.Context, dir string, c GitCommit) (string, error) {
	f.note(dir, "commit "+c.Subject+"|"+c.Body+"|"+strings.Join(c.Paths, ",")+"|"+c.Head)
	if f.err != nil {
		return "", f.err
	}
	return "def5678", nil
}

func (f *fakeGitWriter) Sync(_ context.Context, dir string, op GitSyncOp, head string) error {
	f.note(dir, string(op)+" "+head)
	return f.err
}

func gitWriteHarness(t *testing.T) (*harness, *fakeGitWriter) {
	t.Helper()
	h, _ := gitHarness(t)
	w := &fakeGitWriter{}
	h.srv.opts.GitWrite = w
	return h, w
}

func TestGitWriteAbsentWithoutWriter(t *testing.T) {
	h, _ := gitHarness(t)
	if resp := h.launch("/api/v3/workspaces/w1/git/stage", h.token, `{"paths":["a.go"]}`); resp.StatusCode != http.StatusNotFound || errCode(t, resp) != "git_write_unavailable" {
		t.Fatalf("status %d", resp.StatusCode)
	}
	body := bodyOf(t, h.get("/api/v3/bootstrap", h.token, nil))
	if strings.Contains(string(body), "gitWrite") {
		t.Fatalf("bootstrap advertises gitWrite: %s", body)
	}
}

func TestGitWriteNeedsManageScope(t *testing.T) {
	h, w := gitWriteHarness(t)
	if resp := h.launch("/api/v3/workspaces/w1/git/stage", "", `{"paths":["a.go"]}`); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", resp.StatusCode)
	}
	if len(w.calls) != 0 {
		t.Fatal("an unauthenticated write reached Git")
	}
}

func TestGitWriteRoutesForwardAndAnswerFreshStatus(t *testing.T) {
	h, w := gitWriteHarness(t)
	for path, body := range map[string]string{
		"/api/v3/workspaces/w1/git/stage":   `{"paths":["a.go","b.go"]}`,
		"/api/v3/workspaces/w1/git/unstage": `{"paths":["a.go"]}`,
		"/api/v3/workspaces/w1/git/commit":  `{"subject":" Fix it ","body":"why","paths":["a.go"],"head":"abc1234"}`,
		"/api/v3/workspaces/w1/git/fetch":   ``,
		"/api/v3/workspaces/w1/git/pull":    `{"head":"abc1234"}`,
		"/api/v3/workspaces/w1/git/push":    `{}`,
	} {
		resp := h.launch(path, h.token, body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d", path, resp.StatusCode)
		}
		out := string(bodyOf(t, resp))
		if !strings.Contains(out, `"workspaceId":"w1"`) || !strings.Contains(out, `"branch":"main"`) {
			t.Fatalf("%s: no fresh status in %s", path, out)
		}
	}
	want := []string{"stage a.go,b.go", "unstage a.go", "commit Fix it|why|a.go|abc1234", "fetch ", "pull abc1234", "push "}
	got := map[string]bool{}
	for _, c := range w.calls {
		got[c] = true
	}
	for _, c := range want {
		if !got[c] {
			t.Fatalf("calls %v lack %q", w.calls, c)
		}
	}
	for _, d := range w.dirs {
		if d != "/work/solco" {
			t.Fatalf("dir %q", d)
		}
	}
}

func TestGitWriteRefusesBadRequests(t *testing.T) {
	h, w := gitWriteHarness(t)
	for _, tc := range []struct{ path, body string }{
		{"stage", `{}`},
		{"stage", `{"paths":[]}`},
		{"stage", `{"paths":[""]}`},
		{"stage", `not json`},
		{"commit", `{"subject":"","paths":["a.go"]}`},
		{"commit", `{"subject":"two\nlines","paths":["a.go"]}`},
		{"commit", `{"subject":"x","paths":[]}`},
		{"commit", `{"subject":"x","paths":["a.go"],"head":"HEAD; rm"}`},
		{"push", `{"head":"nothex!"}`},
	} {
		if resp := h.launch("/api/v3/workspaces/w1/git/"+tc.path, h.token, tc.body); resp.StatusCode != http.StatusBadRequest || errCode(t, resp) != "bad_request" {
			t.Fatalf("%s %s: status %d", tc.path, tc.body, resp.StatusCode)
		}
	}
	if len(w.calls) != 0 {
		t.Fatalf("a bad request reached Git: %v", w.calls)
	}
}

func TestGitWriteRelaysRefusalsByCodeOnly(t *testing.T) {
	h, w := gitWriteHarness(t)
	w.err = &GitWriteError{Code: "git_stale", Status: http.StatusConflict}
	resp := h.launch("/api/v3/workspaces/w1/git/commit", h.token, `{"subject":"x","paths":["a.go"]}`)
	if resp.StatusCode != http.StatusConflict || errCode(t, resp) != "git_stale" {
		t.Fatalf("status %d", resp.StatusCode)
	}
	w.err = context.DeadlineExceeded
	resp = h.launch("/api/v3/workspaces/w1/git/push", h.token, ``)
	if resp.StatusCode != http.StatusBadGateway || errCode(t, resp) != "git_failed" {
		t.Fatalf("status %d", resp.StatusCode)
	}
	w.err = nil
	resp = h.launch("/api/v3/workspaces/w1/git/stage", h.token, `{"paths":["gone.go"]}`)
	if resp.StatusCode != http.StatusNotFound || errCode(t, resp) != "unknown_file" {
		t.Fatalf("status %d", resp.StatusCode)
	}
}
