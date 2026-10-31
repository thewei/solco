package remotehost

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func filesHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	root := t.TempDir()
	for _, p := range []string{"src/app.go", "src/util.go", "README.md", ".env"} {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	h.back.mu.Lock()
	for i := range h.back.proj.Panes {
		if h.back.proj.Panes[i].ID == "w1:p1" {
			h.back.proj.Panes[i].CWD = root
		}
	}
	h.back.mu.Unlock()
	return h
}

func filesOf(t *testing.T, h *harness, query string) []string {
	t.Helper()
	resp := h.get("/api/v3/panes/w1:p1/chat/files?instance=default&token="+query, h.token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var out filesBody
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out.Files
}

func TestFilesListsThePaneFolderForTheAtToken(t *testing.T) {
	h := filesHarness(t)
	if got := filesOf(t, h, ""); !reflect.DeepEqual(got, []string{"README.md", "src/"}) {
		t.Fatalf("root %v", got)
	}
	if got := filesOf(t, h, "src/ut"); !reflect.DeepEqual(got, []string{"util.go"}) {
		t.Fatalf("sub %v", got)
	}
}

func TestFilesNeverLeavesThePaneFolderAndAnswersAnArrayWhenEmpty(t *testing.T) {
	h := filesHarness(t)
	if got := filesOf(t, h, "../"); got == nil || len(got) != 0 {
		t.Fatalf("escape %v", got)
	}
	resp := h.get("/api/v3/panes/w1:p1/chat/files?instance=default&token=zzz", h.token, nil)
	var raw map[string]json.RawMessage
	_ = json.NewDecoder(resp.Body).Decode(&raw)
	if string(raw["files"]) != "[]" {
		t.Fatalf("wire %s", raw["files"])
	}
}

func TestFilesNeedsAViewTokenAndAKnownPane(t *testing.T) {
	h := filesHarness(t)
	if resp := h.get("/api/v3/panes/w1:p1/chat/files?instance=default", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token: %d", resp.StatusCode)
	}
	if resp := h.get("/api/v3/panes/nope/chat/files?instance=default", h.token, nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown pane: %d", resp.StatusCode)
	}
}
