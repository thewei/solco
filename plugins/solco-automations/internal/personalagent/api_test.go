package personalagent

import (
	"encoding/json"
	"errors"
	"testing"
)

func call(t *testing.T, a *API, stdin string, args ...string) (map[string]any, error) {
	t.Helper()
	out, err := a.Handle(args, []byte(stdin))
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(out)
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("not an object: %s", raw)
	}
	return m, nil
}

func codeOf(err error) string {
	var ce *CodedError
	if errors.As(err, &ce) {
		return ce.Code
	}
	return ""
}

func TestAPIAssistantRoundTrip(t *testing.T) {
	s, _ := openTest(t)
	a := &API{Store: s}

	created, err := call(t, a, `{"requestId":"r1","name":"Ada","description":"does things"}`, "assistant", "create")
	if err != nil {
		t.Fatal(err)
	}
	if created["schema"] != float64(1) {
		t.Fatalf("no schema version: %v", created)
	}
	asst := created["assistant"].(map[string]any)
	id := asst["id"].(string)
	if asst["name"] != "Ada" || asst["revision"] != float64(1) {
		t.Fatalf("assistant = %v", asst)
	}

	// Same request replays; a different body is a coded conflict.
	again, _ := call(t, a, `{"requestId":"r1","name":"Ada","description":"does things"}`, "assistant", "create")
	if again["replayed"] != true || again["assistant"].(map[string]any)["id"] != id {
		t.Fatalf("replay = %v", again)
	}
	if _, err := call(t, a, `{"requestId":"r1","name":"Other"}`, "assistant", "create"); codeOf(err) != "request_conflict" {
		t.Fatalf("conflict code = %q (%v)", codeOf(err), err)
	}

	list, _ := call(t, a, ``, "assistant", "list")
	if len(list["assistants"].([]any)) != 1 {
		t.Fatalf("list = %v", list)
	}
	show, _ := call(t, a, ``, "assistant", "show", id)
	if show["assistant"].(map[string]any)["id"] != id {
		t.Fatalf("show = %v", show)
	}

	renamed, err := call(t, a, `{"requestId":"r2","name":"Grace","expectedRevision":1}`, "assistant", "rename", id)
	if err != nil || renamed["assistant"].(map[string]any)["name"] != "Grace" {
		t.Fatalf("rename = %v, %v", renamed, err)
	}
	if _, err := call(t, a, `{"requestId":"r3","name":"Stale","expectedRevision":1}`, "assistant", "rename", id); codeOf(err) != "stale_revision" {
		t.Fatalf("stale code = %q", codeOf(err))
	}
	if _, err := call(t, a, ``, "assistant", "show", "a_missing"); codeOf(err) != "not_found" {
		t.Fatalf("missing code = %q", codeOf(err))
	}
}

func TestAPITaskLifecycleAndOverview(t *testing.T) {
	s, _ := openTest(t)
	a := &API{Store: s}
	created, _ := call(t, a, `{"requestId":"r1","name":"Ada"}`, "assistant", "create")
	id := created["assistant"].(map[string]any)["id"].(string)

	task, err := call(t, a, `{"requestId":"t1","assistantId":"`+id+`","kind":"research","title":"Look","brief":"Look at things"}`, "assistant", "task", "create")
	if err != nil {
		t.Fatal(err)
	}
	tid := task["task"].(map[string]any)["id"].(string)
	if task["task"].(map[string]any)["state"] != "queued" {
		t.Fatalf("task = %v", task)
	}
	if _, err := call(t, a, `{"requestId":"t2","assistantId":"`+id+`","kind":"bogus","title":"x","brief":"y"}`, "assistant", "task", "create"); codeOf(err) != "invalid" {
		t.Fatalf("bad kind code = %q", codeOf(err))
	}

	list, _ := call(t, a, ``, "assistant", "task", "list", "--assistant", id)
	if len(list["tasks"].([]any)) != 1 {
		t.Fatalf("tasks = %v", list)
	}
	ov, err := call(t, a, ``, "assistant", "overview")
	if err != nil {
		t.Fatal(err)
	}
	row := ov["assistants"].([]any)[0].(map[string]any)
	if row["id"] != id || row["tasks"] != float64(1) || row["queued"] != float64(1) {
		t.Fatalf("overview row = %v", row)
	}
	show, _ := call(t, a, ``, "assistant", "task", "show", tid)
	if show["task"].(map[string]any)["id"] != tid {
		t.Fatalf("show = %v", show)
	}
	// The brief is part of the task wire; a run list is empty until claimed.
	runs, _ := call(t, a, ``, "assistant", "run", "list", "--task", tid)
	if len(runs["runs"].([]any)) != 0 {
		t.Fatalf("runs = %v", runs)
	}
}

func TestAPIUnknownCommand(t *testing.T) {
	s, _ := openTest(t)
	a := &API{Store: s}
	if _, err := a.Handle([]string{"assistant", "frobnicate"}, nil); codeOf(err) != "invalid" {
		t.Fatalf("code = %q", codeOf(err))
	}
}

func asCodedErr(err error, out **CodedError) bool { return errors.As(err, out) }

// A misspelled field must be refused, not dropped: an agent that writes
// `depndsOn` would otherwise get a task with no dependencies and no word of it.
// (Shepherdr core-task-options: "unknown flags refuse instead of silently disappearing".)
func TestAnUnknownFieldIsRefusedNotDropped(t *testing.T) {
	s, _ := openTest(t)
	a := &API{Store: s}
	created, _ := call(t, a, `{"requestId":"r1","name":"Ada"}`, "assistant", "create")
	id := created["assistant"].(map[string]any)["id"].(string)
	_, err := call(t, a, `{"requestId":"t1","assistantId":"`+id+`","kind":"research","title":"x","brief":"y","depndsOn":["t_1"]}`, "assistant", "task", "create")
	if codeOf(err) != "invalid" {
		t.Fatalf("a misspelled field code = %q, want invalid", codeOf(err))
	}
	if all, _ := s.ListTasks(id); len(all) != 0 {
		t.Fatalf("the refused command still created %d task(s)", len(all))
	}
}
