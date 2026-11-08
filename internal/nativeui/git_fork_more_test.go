package nativeui

import (
	"testing"
	"time"

	"github.com/thewei/solco/internal/settings"
)

func TestCommitAndPushFlagLifecycle(t *testing.T) {
	shell, _, _ := treeTestShell(t)
	shell.openQuickCommit(true)
	if !shell.gitMore.pushAfterCommit {
		t.Fatal("Commit and Push… sets the flag")
	}
	shell.cancelCommitSurface()
	if shell.gitMore.pushAfterCommit {
		t.Fatal("cancelling clears it")
	}
	shell.openQuickCommit(true)
	shell.commitDialogOpen = false
	shell.openQuickCommit(false)
	if shell.gitMore.pushAfterCommit {
		t.Fatal("a plain Commit never pushes")
	}
	shell.gitMore.pushAfterCommit = true
	shell.afterCommitPush()
	if shell.gitMore.pushAfterCommit {
		t.Fatal("the push continuation is consumed")
	}
}

func TestAutoFetchCadence(t *testing.T) {
	shell := NewShell()
	if shell.autoFetchInterval() != 0 {
		t.Fatal("auto-fetch is off by default")
	}
	for minutes, want := range map[int]time.Duration{5: 5 * time.Minute, 60: time.Hour, 7: 0, -1: 0} {
		shell.settings.Workbench.AutoFetchMinutes = minutes
		if got := shell.autoFetchInterval(); got != want {
			t.Errorf("%d minutes -> %v, want %v", minutes, got, want)
		}
	}
	if autoFetchLabel(15) != "Every 15 minutes" || autoFetchLabel(3) != "Off" {
		t.Fatal("labels")
	}
	shell.settings.Workbench.AutoFetchMinutes = 5
	shell.rearmAutoFetch()
	if shell.gitMore.autoFetchTimer == nil {
		t.Fatal("a cadence arms the timer")
	}
	shell.settings.Workbench.AutoFetchMinutes = 0
	shell.rearmAutoFetch()
	if shell.gitMore.autoFetchTimer != nil {
		t.Fatal("off disarms it")
	}
	var _ settings.Settings
}
