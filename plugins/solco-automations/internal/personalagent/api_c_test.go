package personalagent

import (
	"strings"
	"testing"
)

// The CLI surface of stage C: heartbeat, events, progress.
func TestAPIStageCCommands(t *testing.T) {
	s, _ := openTest(t)
	api := &API{Store: s}
	created, _ := call(t, api, `{"requestId":"r1","name":"Ada"}`, "assistant", "create")
	id := created["assistant"].(map[string]any)["id"].(string)

	shown, err := call(t, api, ``, "assistant", "heartbeat", "show", id)
	if err != nil || shown["heartbeat"].(map[string]any)["revision"] != float64(0) {
		t.Fatalf("default heartbeat: %v %v", shown, err)
	}
	set, err := call(t, api, `{"requestId":"h1","expectedRevision":0,"cron":"*/10 * * * *","wakeBudget":2,"events":{"task.failed":"wake"}}`, "assistant", "heartbeat", "set", id)
	if err != nil {
		t.Fatal(err)
	}
	hb := set["heartbeat"].(map[string]any)
	if hb["revision"] != float64(1) || hb["wakeApproved"] != false {
		t.Fatalf("heartbeat = %v", hb)
	}
	if _, err := call(t, api, `{"requestId":"h2","expectedRevision":0,"cron":"*/15 * * * *"}`, "assistant", "heartbeat", "set", id); codeOf(err) != "stale_revision" {
		t.Fatalf("stale code = %q (%v)", codeOf(err), err)
	}
	if _, err := call(t, api, `{"requestId":"h3","expectedRevision":1,"cron":"* * * * *"}`, "assistant", "heartbeat", "set", id); codeOf(err) != "invalid" {
		t.Fatalf("too fast = %q (%v)", codeOf(err), err)
	}
	card, err := call(t, api, `{"requestId":"w1"}`, "assistant", "heartbeat", "request-wake", id)
	if err != nil {
		t.Fatal(err)
	}
	ap := card["approval"].(map[string]any)
	if ap["subjectKind"] != "heartbeat" || !strings.Contains(ap["action"].(string), "at most 2") {
		t.Fatalf("card = %v", ap)
	}
	dec, err := call(t, api, `{"requestId":"w2","actionHash":"`+ap["actionHash"].(string)+`","approve":true}`, "assistant", "approval", "decide", ap["id"].(string))
	if err != nil || dec["approval"].(map[string]any)["state"] != "approved" {
		t.Fatalf("decide: %v %v", dec, err)
	}
	after, _ := call(t, api, ``, "assistant", "heartbeat", "show", id)
	if after["heartbeat"].(map[string]any)["wakeApproved"] != true {
		t.Fatalf("approval did not reach the heartbeat: %v", after)
	}

	// Events and progress.
	g, _ := call(t, api, `{"requestId":"g1","assistantId":"`+id+`","title":"Be useful"}`, "assistant", "goal", "create")
	gid := g["goal"].(map[string]any)["id"].(string)
	if _, err := call(t, api, `{"requestId":"p1","note":"checked"}`, "assistant", "goal", "progress", gid); err != nil {
		t.Fatal(err)
	}
	evs, err := call(t, api, ``, "assistant", "event", "list", id, "--after", "0")
	if err != nil || len(evs["events"].([]any)) != 1 {
		t.Fatalf("events = %v %v", evs, err)
	}
}
