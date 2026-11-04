package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/thewei/solco/plugins/solco-automations/internal/config"
	"github.com/thewei/solco/plugins/solco-automations/internal/daemon"
	"github.com/thewei/solco/plugins/solco-automations/internal/personalagent"
)

// assistantCmd serves `solco-automations assistant …`: JSON in on stdin (so a
// brief never lands in argv or `ps`), one JSON object out. Failures are JSON
// too, with a stable code, and exit 1.
func assistantCmd(args []string) error {
	store, err := personalagent.Open(personalagent.DBPath())
	if err != nil {
		return printAssistantError(err)
	}
	defer store.Close()
	// Finish an interrupted YAML -> SQLite Routine draft before any CLI
	// request sees the state. This never approves or runs the draft.
	if n, err := store.RecoverRoutineIntents(); err != nil {
		if !errors.Is(err, config.ErrDefinitionChanged) {
			return printAssistantError(err)
		}
		// A changed interrupted draft remains quarantined from the
		// legacy scheduler; unrelated Assistant commands stay available.
		fmt.Fprintf(os.Stderr, "assistants: routine recovery needs review: %v\n", err)
	} else if n > 0 {
		fmt.Fprintf(os.Stderr, "assistants: recovered %d interrupted routine draft(s)\n", n)
	}
	var body []byte
	if st, err := os.Stdin.Stat(); err == nil && st.Mode()&os.ModeCharDevice == 0 {
		body, _ = io.ReadAll(io.LimitReader(os.Stdin, 4<<20))
	}
	wt := &personalagent.Worktrees{Store: store, Root: personalagent.DefaultWorktreeRoot()}
	api := &personalagent.API{Store: store, Reviewer: &personalagent.Reviewer{Store: store, Worktrees: wt}, PaneGone: personalagent.HerdrPaneGone}
	out, err := api.Handle(append([]string{"assistant"}, args...), body)
	if err != nil {
		return printAssistantError(err)
	}
	if assistantNeedsDaemon(args) {
		ensureDaemon()
	}
	return printJSON(out, nil)
}

// assistantNeedsDaemon selects successful commands that can queue or unblock
// future work. A phone may create a Task, approve a Routine or post a Group
// message without starting a Coordinator, so none of those operations may
// silently depend on a previously running scheduler. Read-only polling does
// not spawn background processes.
func assistantNeedsDaemon(args []string) bool {
	if len(args) == 0 {
		return false
	}
	if args[0] == "create" || args[0] == "enable" {
		return true
	}
	if len(args) < 2 {
		return false
	}
	op := args[1]
	switch args[0] {
	case "coordinator":
		return op == "start"
	case "task":
		return op == "create" || op == "retry" || op == "publish" || op == "ack" || op == "deps"
	case "goal":
		return op == "create" || op == "set-plan" || op == "approve" || op == "resume" || op == "complete"
	case "routine":
		return op == "draft" || op == "resume" || op == "pause" || op == "cancel"
	case "approval":
		return op == "decide" || op == "request"
	case "heartbeat":
		return op == "set" || op == "request-wake"
	case "group":
		return op == "create" || op == "add" || op == "remove" || op == "post" || op == "say"
	case "rule":
		return op == "adopt" || op == "rollback"
	case "review":
		return op == "start" || op == "agent" || op == "merge" || op == "request-merge"
	case "proactivity":
		return op == "set"
	case "checks":
		return op == "set"
	}
	return false
}

// assistantDaemonArgs starts the Assistant fallback as a persistent scheduler.
// Herdr's startup-hook daemon may exit with the last TUI session; an
// Assistants-initiated daemon cannot, because mobile requests and scheduled
// work continue without a Terminal.
func assistantDaemonArgs() []string { return []string{"daemon", "--standby"} }

// ensureDaemon starts the scheduler when nothing holds its lock. Herdr runs
// the plugin's startup hook only with its server, so a plugin linked or
// updated while the server is up has no daemon; a queued Coordinator would sit
// on "starting" for ever. A second daemon exits on its own (flock), so this is
// idempotent. It never fails the command: the queue row is already written.
func ensureDaemon() {
	if err := os.MkdirAll(config.StateDir(), 0o700); err != nil {
		fmt.Fprintf(os.Stderr, "assistants: cannot prepare scheduler state: %v\n", err)
		return
	}
	// daemon.Running() is a snapshot, not an atomic claim. Two simultaneous
	// Host/CLI calls must not each spawn a standby child while the first is
	// still acquiring daemon.pid. Serialize bootstrap across processes and
	// check readiness before releasing this lock.
	lockPath := filepath.Join(config.StateDir(), ".assistant-daemon-bootstrap.lock")
	fd, err := syscall.Open(lockPath, syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "assistants: cannot lock scheduler bootstrap: %v\n", err)
		return
	}
	lock := os.NewFile(uintptr(fd), lockPath)
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	if _, running := daemon.Running(); running {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(exe, assistantDaemonArgs()...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if f, err := os.OpenFile(filepath.Join(config.StateDir(), "daemon.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
		defer f.Close()
		cmd.Stdout, cmd.Stderr = f, f
	}
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "assistants: scheduler start failed: %v\n", err)
		return
	}
	go func() { _ = cmd.Wait() }()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, running := daemon.Running(); running {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	fmt.Fprintln(os.Stderr, "assistants: scheduler launch did not become ready; inspect daemon.log")
}

func printAssistantError(err error) error {
	var ce *personalagent.CodedError
	if !errors.As(err, &ce) {
		ce = &personalagent.CodedError{Code: "internal", Message: err.Error()}
		if errors.Is(err, personalagent.ErrBusy) {
			ce.Code = "busy"
		}
	}
	raw, _ := json.Marshal(map[string]any{"schema": 1, "error": ce})
	fmt.Println(string(raw))
	os.Exit(1)
	return nil
}
