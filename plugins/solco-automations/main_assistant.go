package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

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
	return printJSON(out, nil)
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
