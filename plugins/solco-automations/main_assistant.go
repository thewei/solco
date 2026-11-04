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
	var body []byte
	if st, err := os.Stdin.Stat(); err == nil && st.Mode()&os.ModeCharDevice == 0 {
		body, _ = io.ReadAll(io.LimitReader(os.Stdin, 4<<20))
	}
	wt := &personalagent.Worktrees{Store: store, Root: personalagent.DefaultWorktreeRoot()}
	api := &personalagent.API{Store: store, Reviewer: &personalagent.Reviewer{Store: store, Worktrees: wt}}
	out, err := api.Handle(append([]string{"assistant"}, args...), body)
	if err != nil {
		return printAssistantError(err)
	}
	if len(args) >= 2 && args[0] == "coordinator" && args[1] == "start" {
		ensureDaemon()
	}
	return printJSON(out, nil)
}

// ensureDaemon starts the scheduler when nothing holds its lock. Herdr runs
// the plugin's startup hook only with its server, so a plugin linked or
// updated while the server is up has no daemon; a queued Coordinator would sit
// on "starting" for ever. A second daemon exits on its own (flock), so this is
// idempotent. It never fails the command: the queue row is already written.
func ensureDaemon() {
	if _, running := daemon.Running(); running {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(exe, "daemon")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := os.MkdirAll(config.StateDir(), 0o755); err == nil {
		if f, err := os.OpenFile(filepath.Join(config.StateDir(), "daemon.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
			defer f.Close()
			cmd.Stdout, cmd.Stderr = f, f
		}
	}
	if cmd.Start() == nil {
		go func() { _ = cmd.Wait() }()
	}
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
