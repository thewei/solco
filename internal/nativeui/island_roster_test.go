package nativeui

import (
	"testing"

	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/feedback"
)

func rosterEntry(id, title string, b agent.AgentDirectoryBucket) StatusCenterEntry {
	return StatusCenterEntry{
		Card:   agent.AgentCardModel{Key: agent.AgentKey{InstanceID: "inst", TerminalID: id}, Title: title, ProjectName: "solco"},
		Bucket: b,
	}
}

func TestRosterGroupsFeedbackFirstThenAgents(t *testing.T) {
	snap := StatusCenterSnapshot{Entries: []StatusCenterEntry{
		rosterEntry("t1", "Fix tests", agent.BucketNeedsAttention),
		rosterEntry("t2", "Refactor", agent.BucketWorking),
		rosterEntry("t3", "Docs", agent.BucketReadyForReview),
		rosterEntry("t4", "Spare", agent.BucketIdle),
	}}
	threads := []feedback.ThreadSummary{
		{ID: "th_1", Project: "solco", Client: "Cursor", Preview: "Keep it?", Waiting: 1},
		{ID: "th_2", Waiting: 0},
	}
	asks := map[string]islandAsk{"th_1": {Thread: "th_1", Ask: "a_1", Question: "q", Options: []string{"Yes", "No"}}}
	r := buildIslandRoster(snap, threads, asks)

	if len(r.Needs) != 3 || r.Needs[0].Key != "fb:th_1" || r.Needs[1].Key != "ag:t1" || r.Needs[2].Key != "ag:t3" {
		t.Fatalf("needs = %+v", r.Needs)
	}
	if r.Needs[0].Ask == nil || r.Needs[0].Route != feedbackNotificationID("th_1", "a_1") {
		t.Fatalf("feedback row = %+v", r.Needs[0])
	}
	if r.Needs[1].Kind != islandAttention || r.Needs[2].Kind != islandReview {
		t.Fatalf("kinds = %v %v", r.Needs[1].Kind, r.Needs[2].Kind)
	}
	if r.Needs[1].Route != agentNotificationID("inst", "t1") {
		t.Fatalf("route = %q", r.Needs[1].Route)
	}
	if len(r.Working) != 1 || len(r.Idle) != 1 || r.Empty() {
		t.Fatalf("working %d idle %d", len(r.Working), len(r.Idle))
	}
}

func TestRosterCapsEachGroupAndCountsTheRest(t *testing.T) {
	var snap StatusCenterSnapshot
	for i := 0; i < islandWorkingMax+3; i++ {
		snap.Entries = append(snap.Entries, rosterEntry(string(rune('a'+i)), "w", agent.BucketWorking))
	}
	r := buildIslandRoster(snap, nil, nil)
	if len(r.Working) != islandWorkingMax || r.WorkingMore != 3 {
		t.Fatalf("working %d more %d", len(r.Working), r.WorkingMore)
	}
	if !buildIslandRoster(StatusCenterSnapshot{}, nil, nil).Empty() {
		t.Fatal("no agents and no threads is empty")
	}
}

func pendingAsk(id string, qs ...feedback.Question) *feedback.Ask {
	return &feedback.Ask{ID: id, State: feedback.StatePending, Questions: qs}
}

func pendingAskAt(id string, at int64, qs ...feedback.Question) *feedback.Ask {
	a := pendingAsk(id, qs...)
	a.CreatedAt = at
	return a
}

func TestIslandAskOnlyForOneShortSingleChoice(t *testing.T) {
	good := feedback.Question{ID: "q1", Type: "single", Options: []string{"Yes", "No"}}
	cases := []struct {
		name string
		th   feedback.Thread
		ok   bool
	}{
		{"one single", feedback.Thread{ID: "th", Asks: []*feedback.Ask{pendingAsk("a", good)}}, true},
		{"answered ones are ignored", feedback.Thread{ID: "th", Asks: []*feedback.Ask{{ID: "old", State: feedback.StateAnswered}, pendingAsk("a", good)}}, true},
		{"the newest of two pending asks, when it is answerable", feedback.Thread{ID: "th", Asks: []*feedback.Ask{pendingAskAt("old", 1, good, good), pendingAskAt("a", 2, good)}}, true},
		{"the newest of two pending asks, when it is not", feedback.Thread{ID: "th", Asks: []*feedback.Ask{pendingAskAt("old", 1, good), pendingAskAt("new", 2, good, good)}}, false},
		{"two questions", feedback.Thread{ID: "th", Asks: []*feedback.Ask{pendingAsk("a", good, good)}}, false},
		{"multiple choice", feedback.Thread{ID: "th", Asks: []*feedback.Ask{pendingAsk("a", feedback.Question{ID: "q", Type: "multiple", Options: []string{"A", "B"}})}}, false},
		{"too many options", feedback.Thread{ID: "th", Asks: []*feedback.Ask{pendingAsk("a", feedback.Question{ID: "q", Type: "single", Options: []string{"1", "2", "3", "4", "5"}})}}, false},
		{"a sentence is not a chip", feedback.Thread{ID: "th", Asks: []*feedback.Ask{pendingAsk("a", feedback.Question{ID: "q", Type: "single", Options: []string{"Yes", "Apply it to every package in the monorepo"}})}}, false},
		{"nothing pending", feedback.Thread{ID: "th"}, false},
	}
	for _, tc := range cases {
		a, ok := islandAskOf(tc.th)
		if ok != tc.ok {
			t.Errorf("%s: ok = %v", tc.name, ok)
		}
		if ok && (a.Ask != "a" || a.Question != "q1") {
			t.Errorf("%s: ask = %+v", tc.name, a)
		}
	}
}
