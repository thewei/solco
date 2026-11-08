package nativeui

/**
 * [INPUT]: tempGitRepo fixture, gitworkbench.Cache, Shell.uiApplyOverride apply lane
 * [OUTPUT]: Regression coverage for window-activation Git refresh and its debounce fence
 * [POS]: Companion of git_service_snapshot.go's refreshGitOnActivation; pure headless state assertions
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestWindowActivationRefreshesGit pins the dynamic-refresh seam: an
// activation refresh pulls external work into the visible snapshot — the fresh
// data is the notification — and the Refresh button's explicit path is
// untouched.
func TestWindowActivationRefreshesGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	root := tempGitRepo(t)
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}

	shell := NewShell()
	shell.git.root = root
	var mu sync.Mutex
	shell.uiApplyOverride = func(fn func()) {
		mu.Lock()
		defer mu.Unlock()
		fn()
	}

	snap, err := shell.git.cache.Refresh(context.Background(), root)
	if err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}
	mu.Lock()
	shell.applyGitSnapshot(snap)
	mu.Unlock()

	// Work done outside the app while it was backgrounded.
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	shell.refreshGitOnActivation()
	mu.Unlock()

	deadline := time.Now().Add(15 * time.Second)
	for {
		mu.Lock()
		visible := shell.git.snapshot != nil && len(shell.git.snapshot.Files) == 1
		mu.Unlock()
		if visible {
			break
		}
		if time.Now().After(deadline) {
			mu.Lock()
			t.Fatalf("activation refresh did not land: files=%d", len(shell.git.snapshot.Files))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestWindowActivationRefreshDebounces pins the fence around the activation
// refresh: calls inside focusRefreshGap are dropped before any state changes,
// and an activation that lands while a refresh is already in flight starts no
// second one.
func TestWindowActivationRefreshDebounces(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	root := tempGitRepo(t)
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	shell := NewShell()
	shell.git.root = root
	var mu sync.Mutex
	shell.uiApplyOverride = func(fn func()) {
		mu.Lock()
		defer mu.Unlock()
		fn()
	}
	_, err := shell.git.cache.Refresh(context.Background(), root)
	if err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}

	// Inside the gap: the debounced call mutates nothing and no refresh lane
	// starts (checked synchronously, so no async landing can race the
	// assertion).
	mu.Lock()
	shell.git.lastActivationRefresh = timeNow()
	shell.refreshGitOnActivation()
	debounced := !shell.git.refreshing
	// In-flight refresh: the activation starts no second one.
	shell.git.lastActivationRefresh = time.Time{}
	shell.git.refreshing = true
	gen := shell.git.snapshotGen.Load()
	shell.refreshGitOnActivation()
	single := shell.git.refreshing && shell.git.snapshotGen.Load() == gen
	shell.git.refreshing = false
	mu.Unlock()

	if !debounced {
		t.Fatal("activation inside the debounce gap must be a full no-op")
	}
	if !single {
		t.Fatal("activation during an in-flight refresh must not start another")
	}
}
