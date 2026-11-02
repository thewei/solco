package remotehost

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
)

// fakeTrasher answers scripted results and records what it was asked.
type fakeTrasher struct {
	mu      sync.Mutex
	trashed []string
	freed   []string
	entries []TrashEntry
	err     error
}

func (f *fakeTrasher) Trash(_ context.Context, sessionID string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.trashed = append(f.trashed, sessionID)
	return "1785549600000-0a1b2c3d", f.err
}

func (f *fakeTrasher) List(context.Context) ([]TrashEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.entries, f.err
}

func (f *fakeTrasher) Restore(_ context.Context, entryID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.freed = append(f.freed, entryID)
	return f.err
}

func (f *fakeTrasher) asked() (trashed, freed []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.trashed...), append([]string(nil), f.freed...)
}

func sampleTrash() []TrashEntry {
	return []TrashEntry{{ID: "1785549600000-0a1b2c3d", Agent: "codex", Title: "Refactor the tab bar", Project: "solco", DeletedMs: 1785549600000, Messages: 18}}
}

func trashHarness(t *testing.T) (*harness, *fakeTrasher) {
	t.Helper()
	h := newHarness(t)
	f := &fakeTrasher{entries: sampleTrash()}
	h.srv.opts.Sessions = (&fakeSessions{}).serve
	h.srv.opts.Trasher = f
	return h, f
}

func TestSessionTrashAbsentWithoutSource(t *testing.T) {
	h := newHarness(t)
	if resp := h.launch("/api/v3/sessions/codex:19bd/trash", h.token, ""); resp.StatusCode != http.StatusNotFound || errCode(t, resp) != "trash_unavailable" {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if resp := h.get("/api/v3/sessions/trash", h.token, nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("list status %d", resp.StatusCode)
	}
}

func TestSessionTrashNeedsAuthentication(t *testing.T) {
	h, f := trashHarness(t)
	if resp := h.launch("/api/v3/sessions/codex:19bd/trash", "", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("trash status %d, want 401", resp.StatusCode)
	}
	if resp := h.launch("/api/v3/sessions/trash/1785549600000-0a1b2c3d/restore", "", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("restore status %d, want 401", resp.StatusCode)
	}
	if resp := h.get("/api/v3/sessions/trash", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("list status %d, want 401", resp.StatusCode)
	}
	if trashed, freed := f.asked(); len(trashed)+len(freed) != 0 {
		t.Fatal("an unauthenticated request reached the Trasher")
	}
}

func TestSessionTrashMovesThenRestores(t *testing.T) {
	h, f := trashHarness(t)
	resp := h.launch("/api/v3/sessions/codex:19bd/trash", h.token, "")
	var moved trashAnswer
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&moved) != nil || moved.Entry != "1785549600000-0a1b2c3d" {
		t.Fatalf("trash answer %d %+v", resp.StatusCode, moved)
	}
	var listed trashList
	if resp := h.get("/api/v3/sessions/trash", h.token, nil); resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&listed) != nil || len(listed.Entries) != 1 {
		t.Fatalf("list %d %+v", resp.StatusCode, listed)
	}
	if resp := h.launch("/api/v3/sessions/trash/"+moved.Entry+"/restore", h.token, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("restore status %d", resp.StatusCode)
	}
	trashed, freed := f.asked()
	if len(trashed) != 1 || trashed[0] != "codex:19bd" || len(freed) != 1 || freed[0] != moved.Entry {
		t.Fatalf("asked trash=%v restore=%v", trashed, freed)
	}
}

func TestSessionTrashPassesTheDesktopsRefusal(t *testing.T) {
	h, f := trashHarness(t)
	f.err = &TrashError{Code: "session_in_use", Status: http.StatusConflict, Detail: "An Agent is still using this session."}
	resp := h.launch("/api/v3/sessions/codex:19bd/trash", h.token, "")
	if resp.StatusCode != http.StatusConflict || errCode(t, resp) != "session_in_use" {
		t.Fatalf("status %d", resp.StatusCode)
	}
	f.err = context.DeadlineExceeded
	if resp := h.launch("/api/v3/sessions/codex:19bd/trash", h.token, ""); resp.StatusCode != http.StatusBadGateway || errCode(t, resp) != "trash_failed" {
		t.Fatalf("generic failure status %d", resp.StatusCode)
	}
}

func TestSessionTrashCapabilityFollowsTheSeam(t *testing.T) {
	h, _ := trashHarness(t)
	var body struct {
		Capabilities map[string]bool `json:"capabilities"`
	}
	if err := json.NewDecoder(h.get("/api/v3/bootstrap", h.token, nil).Body).Decode(&body); err != nil || !body.Capabilities["sessionTrash"] {
		t.Fatalf("capabilities %v, %v", body.Capabilities, err)
	}
}
