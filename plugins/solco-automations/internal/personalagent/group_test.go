package personalagent

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func groupOf(t *testing.T, s *Store, names ...string) (Group, []Assistant) {
	t.Helper()
	var as []Assistant
	var ids []string
	for _, n := range names {
		a := mustAssistant(t, s, "mk-"+n, n)
		as = append(as, a)
		ids = append(ids, a.ID)
	}
	g, _, err := s.CreateGroup("g-"+strings.Join(names, ""), "", ids)
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	return g, as
}

func TestCreateGroupNeedsTwoRealAssistantsAndIsIdempotent(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	if _, _, err := s.CreateGroup("r0", "x", []string{a.ID}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("one member: %v", err)
	}
	if _, _, err := s.CreateGroup("r1", "x", []string{a.ID, a.ID}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate counts once: %v", err)
	}
	if _, _, err := s.CreateGroup("r2", "x", []string{a.ID, "a_nope"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown member: %v", err)
	}
	b := mustAssistant(t, s, "a2", "Bo")
	g, replayed, err := s.CreateGroup("r3", "", []string{a.ID, b.ID})
	if err != nil || replayed || g.Title != "Ada, Bo" || len(g.Members) != 2 {
		t.Fatalf("group = %+v replayed=%v err=%v", g, replayed, err)
	}
	g2, replayed, err := s.CreateGroup("r3", "", []string{a.ID, b.ID})
	if err != nil || !replayed || g2.ID != g.ID {
		t.Fatalf("replay = %+v %v %v", g2, replayed, err)
	}
	list, _ := s.ListGroups()
	if len(list) != 1 {
		t.Fatalf("one request, one group: %d", len(list))
	}
}

func TestPostWithoutMentionCallsNobody(t *testing.T) {
	s, _ := openTest(t)
	g, _ := groupOf(t, s, "Ada", "Bo")
	res, _, err := s.PostGroupMessage("p1", PostMessageInput{GroupID: g.ID, AuthorKind: AuthorUser, Body: "hello everyone"})
	if err != nil || len(res.Deliveries) != 0 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	rep, err := (&GroupPump{Store: s}).Tick()
	if err != nil || rep != (GroupPumpReport{}) {
		t.Fatalf("pump did something: %+v %v", rep, err)
	}
	ls, _ := s.ListTasks("")
	if len(ls) != 0 {
		t.Fatalf("an unmentioned message started work: %v", ls)
	}
}

func TestMentionsAreExactAndMembersOnly(t *testing.T) {
	s, _ := openTest(t)
	g, as := groupOf(t, s, "Ada", "Bo")
	outsider := mustAssistant(t, s, "out", "Cy")
	cases := []struct {
		name string
		in   PostMessageInput
	}{
		{"non-member", PostMessageInput{GroupID: g.ID, AuthorKind: AuthorUser, Body: "@Cy hi", Mentions: []string{outsider.ID}}},
		{"name not in text", PostMessageInput{GroupID: g.ID, AuthorKind: AuthorUser, Body: "hi Ada", Mentions: []string{as[0].ID}}},
		{"reply elsewhere", PostMessageInput{GroupID: g.ID, AuthorKind: AuthorUser, Body: "x", ReplyTo: "m_nope"}},
		{"empty", PostMessageInput{GroupID: g.ID, AuthorKind: AuthorUser, Body: "  "}},
	}
	for _, c := range cases {
		if _, _, err := s.PostGroupMessage("", c.in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want invalid", c.name, err)
		}
	}
	res, _, err := s.PostGroupMessage("ok", PostMessageInput{GroupID: g.ID, AuthorKind: AuthorUser, Body: "@Bo please look; @Bo again", Mentions: []string{as[1].ID, as[1].ID}})
	if err != nil || len(res.Deliveries) != 1 || res.Deliveries[0].AssistantID != as[1].ID {
		t.Fatalf("exactly Bo is delivered: %+v %v", res, err)
	}
	// A reply is stored with its target.
	r2, _, err := s.PostGroupMessage("", PostMessageInput{GroupID: g.ID, AuthorKind: AuthorUser, Body: "thanks", ReplyTo: res.Message.ID})
	if err != nil || r2.Message.ReplyTo != res.Message.ID {
		t.Fatalf("reply = %+v %v", r2, err)
	}
	// Replaying a request never doubles the message or its delivery.
	again, replayed, err := s.PostGroupMessage("ok", PostMessageInput{GroupID: g.ID, AuthorKind: AuthorUser, Body: "@Bo please look; @Bo again", Mentions: []string{as[1].ID, as[1].ID}})
	if err != nil || !replayed || again.Message.ID != res.Message.ID {
		t.Fatalf("replay = %+v %v %v", again, replayed, err)
	}
	feed, _ := s.GroupFeedSince(g.ID, 0, 0)
	if len(feed.Messages) != 2 || len(feed.Deliveries) != 1 {
		t.Fatalf("feed = %d msgs, %d deliveries", len(feed.Messages), len(feed.Deliveries))
	}
}

func TestAnAssistantCannotSpeakWithoutARealAgentAndNeverTriggersDeliveries(t *testing.T) {
	s, _ := openTest(t)
	g, as := groupOf(t, s, "Ada", "Bo")
	if _, _, err := s.PostGroupMessage("", PostMessageInput{GroupID: g.ID, AuthorKind: AuthorAssistant, AuthorID: as[0].ID, Body: "I am Ada"}); !errors.Is(err, ErrBadState) {
		t.Fatalf("fake Agent speech must be refused, got %v", err)
	}
	ga := readyGroupAgent(t, s, g, as[0])
	if ga.State != GroupAgentReady {
		t.Fatalf("agent = %+v", ga)
	}
	res, _, err := s.PostGroupMessage("", PostMessageInput{GroupID: g.ID, AuthorKind: AuthorAssistant, AuthorID: as[0].ID, Body: "@Bo your turn", Mentions: []string{as[1].ID}})
	if err != nil {
		t.Fatalf("say: %v", err)
	}
	if len(res.Deliveries) != 0 || len(res.Message.Mentions) != 1 {
		t.Fatalf("an Agent's @ is recorded but never delivers: %+v", res)
	}
}

// readyGroupAgent brings an Assistant's Group Agent to `ready` the way the
// Dispatcher would, without a Herdr.
func readyGroupAgent(t *testing.T, s *Store, g Group, a Assistant) GroupAgent {
	t.Helper()
	ga, _, err := s.StartGroupAgent("start-"+a.ID, g.ID, a.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	task, _ := s.GetTask(ga.TaskID)
	if _, err := s.groupLaunchPrompt(a, g.ID); err != nil {
		t.Fatal(err)
	}
	run := Run{ID: "r_" + a.ID, Status: RunRunning, HerdrSession: "default", WorkspaceID: "w9", PaneID: "w9:p1"}
	s.syncGroupAgent(task, run)
	run.Status = RunDone // the launch turn finished
	s.syncGroupAgent(task, run)
	got, _ := s.GetGroupAgent(g.ID, a.ID)
	return got
}

func TestPumpStartsOnlyTheMentionedAssistantsAgentAndCreditsTheLaunchPrompt(t *testing.T) {
	s, _ := openTest(t)
	g, as := groupOf(t, s, "Ada", "Bo", "Cy")
	if _, _, err := s.PostGroupMessage("m1", PostMessageInput{GroupID: g.ID, AuthorKind: AuthorUser, Body: "@Bo what do you think?", Mentions: []string{as[1].ID}}); err != nil {
		t.Fatal(err)
	}
	submits := 0
	clock := time.Now()
	p := &GroupPump{Store: s, Now: func() time.Time { return clock }, Submit: func(_, _, _ string) error { submits++; return nil }}
	rep, err := p.Tick()
	if err != nil || rep.Started != 1 {
		t.Fatalf("rep=%+v err=%v", rep, err)
	}
	for _, a := range []Assistant{as[0], as[2]} {
		if ga, _ := s.GetGroupAgent(g.ID, a.ID); ga.State != GroupAgentNone {
			t.Fatalf("%s must not start: %+v", a.Name, ga)
		}
	}
	// The Dispatcher launches Bo's Agent: the brief carries the conversation.
	l := &fakeLauncher{}
	d := &Dispatcher{Store: s, Launcher: l, Claimant: "daemon"}
	if n := d.Pump(t.Context()); n != 1 {
		t.Fatalf("dispatcher started %d", n)
	}
	d.Wait()
	spec := l.specs[0]
	if spec.Kind != KindGroupAgent || spec.GroupID != g.ID || !strings.Contains(spec.Prompt, "@Bo what do you think?") || !strings.Contains(spec.Prompt, "group say") {
		t.Fatalf("spec = %+v", spec)
	}
	ga, _ := s.GetGroupAgent(g.ID, as[1].ID)
	if ga.State != GroupAgentReady || ga.PaneID != "w1:p1" {
		t.Fatalf("agent = %+v", ga)
	}
	// Plumbing never shows as the Assistant's own tasks.
	if ls, _ := s.ListTasks(as[1].ID); len(ls) != 0 {
		t.Fatalf("group agent leaked into the task list: %v", ls)
	}
	clock = clock.Add(5 * time.Second)
	rep, err = p.Tick()
	if err != nil || rep.Delivered != 1 || submits != 0 {
		t.Fatalf("the launch prompt already carried it: rep=%+v submits=%d err=%v", rep, submits, err)
	}
	feed, _ := s.GroupFeedSince(g.ID, 0, 0)
	if feed.Deliveries[0].State != DeliveryDelivered {
		t.Fatalf("delivery = %+v", feed.Deliveries[0])
	}
	// Receipts are system facts, not anybody's speech.
	sawStart, sawDelivered := false, false
	for _, m := range feed.Messages {
		if m.Kind == MsgFact {
			if m.AuthorKind != AuthorSystem {
				t.Fatalf("a receipt is system: %+v", m)
			}
			sawStart = sawStart || strings.Contains(string(m.Fact), "agent_starting")
			sawDelivered = sawDelivered || strings.Contains(string(m.Fact), `"delivered"`)
		}
	}
	if !sawStart || !sawDelivered {
		t.Fatalf("receipts missing: %+v", feed.Messages)
	}
}

func TestPumpDeliversOnlyWhatTheRunningAgentHasNotSeen(t *testing.T) {
	s, _ := openTest(t)
	g, as := groupOf(t, s, "Ada", "Bo")
	readyGroupAgent(t, s, g, as[0])
	if _, _, err := s.PostGroupMessage("", PostMessageInput{GroupID: g.ID, AuthorKind: AuthorUser, Body: "context for later"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.PostGroupMessage("", PostMessageInput{GroupID: g.ID, AuthorKind: AuthorAssistant, AuthorID: as[0].ID, Body: "my own earlier words"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.PostGroupMessage("", PostMessageInput{GroupID: g.ID, AuthorKind: AuthorUser, Body: "@Ada now answer", Mentions: []string{as[0].ID}}); err != nil {
		t.Fatal(err)
	}
	var got []string
	p := &GroupPump{Store: s, Submit: func(sess, pane, text string) error {
		got = append(got, sess+"|"+pane+"|"+text)
		return nil
	}}
	rep, err := p.Tick()
	if err != nil || rep.Delivered != 1 || len(got) != 1 {
		t.Fatalf("rep=%+v err=%v got=%d", rep, err, len(got))
	}
	text := got[0]
	if !strings.HasPrefix(text, "default|w9:p1|") || !strings.Contains(text, "context for later") || !strings.Contains(text, ">>> You (the person) [") ||
		!strings.Contains(text, "@Ada now answer") || strings.Contains(text, "my own earlier words") {
		t.Fatalf("prompt = %q", text)
	}
	// A second pass has nothing left to do: no double delivery.
	rep, _ = p.Tick()
	if rep != (GroupPumpReport{}) || len(got) != 1 {
		t.Fatalf("redelivered: %+v %d", rep, len(got))
	}
}

func TestPumpRetriesARefusalAndNeverResendsAnUnknownOutcome(t *testing.T) {
	s, _ := openTest(t)
	g, as := groupOf(t, s, "Ada", "Bo")
	readyGroupAgent(t, s, g, as[0])
	clock := time.Now()
	post := func(body string) {
		if _, _, err := s.PostGroupMessage("", PostMessageInput{GroupID: g.ID, AuthorKind: AuthorUser, Body: body, Mentions: []string{as[0].ID}}); err != nil {
			t.Fatal(err)
		}
	}
	post("@Ada first")
	calls := 0
	var failWith error = ErrSubmitRefused
	p := &GroupPump{Store: s, Now: func() time.Time { return clock }, Submit: func(_, _, _ string) error {
		calls++
		return failWith
	}}
	if rep, _ := p.Tick(); rep.Delivered != 0 || calls != 1 {
		t.Fatalf("refused: rep=%+v calls=%d", rep, calls)
	}
	// Backoff: an immediate second pass does not hammer the Agent.
	_, _ = p.Tick()
	if calls != 1 {
		t.Fatalf("no backoff: %d", calls)
	}
	clock = clock.Add(time.Minute)
	failWith = nil
	if rep, _ := p.Tick(); rep.Delivered != 1 || calls != 2 {
		t.Fatalf("retry: rep=%+v calls=%d", rep, calls)
	}
	// An error that may have typed the text is never retried.
	post("@Ada second")
	failWith = errors.New("stalled")
	_, _ = p.Tick()
	feed, _ := s.GroupFeedSince(g.ID, 0, 0)
	last := feed.Deliveries[len(feed.Deliveries)-1]
	if last.State != DeliveryUncertain {
		t.Fatalf("delivery = %+v", last)
	}
	failWith = nil
	before := calls
	clock = clock.Add(time.Hour)
	_, _ = p.Tick()
	if calls != before {
		t.Fatalf("an uncertain delivery was resent")
	}
}

func TestRecoverMarksInterruptedDeliveriesUncertain(t *testing.T) {
	s, _ := openTest(t)
	g, as := groupOf(t, s, "Ada", "Bo")
	readyGroupAgent(t, s, g, as[0])
	res, _, _ := s.PostGroupMessage("", PostMessageInput{GroupID: g.ID, AuthorKind: AuthorUser, Body: "@Ada hi", Mentions: []string{as[0].ID}})
	if ok, err := s.claimDelivery(res.Deliveries[0].ID); err != nil || !ok {
		t.Fatal("claim failed")
	}
	n, err := s.RecoverGroupDeliveries()
	if err != nil || n != 1 {
		t.Fatalf("recover = %d %v", n, err)
	}
	feed, _ := s.GroupFeedSince(g.ID, 0, 0)
	if feed.Deliveries[0].State != DeliveryUncertain {
		t.Fatalf("delivery = %+v", feed.Deliveries[0])
	}
}

func TestTaskFiledFromAGroupBecomesALiveCard(t *testing.T) {
	s, _ := openTest(t)
	g, as := groupOf(t, s, "Ada", "Bo")
	in := CreateTaskInput{AssistantID: as[0].ID, GroupID: g.ID, Kind: KindResearch, Title: "Compare vendors", Brief: "Compare."}
	if _, _, err := s.CreateTask("", in); !errors.Is(err, ErrBadState) {
		t.Fatalf("a task needs a running Group Agent: %v", err)
	}
	readyGroupAgent(t, s, g, as[0])
	task, _, err := s.CreateTask("tk", in)
	if err != nil {
		t.Fatal(err)
	}
	feed, _ := s.GroupFeedSince(g.ID, 0, 0)
	var card *GroupMessage
	for i := range feed.Messages {
		if feed.Messages[i].Kind == MsgTask {
			card = &feed.Messages[i]
		}
	}
	if card == nil || card.TaskID != task.ID || card.AuthorID != as[0].ID || len(feed.Tasks) != 1 || feed.Tasks[0].State != TaskQueued {
		t.Fatalf("card = %+v tasks=%+v", card, feed.Tasks)
	}
	// The card follows the real task.
	if _, err := s.db.Exec(`UPDATE tasks SET state=? WHERE id=?`, TaskDone, task.ID); err != nil {
		t.Fatal(err)
	}
	feed, _ = s.GroupFeedSince(g.ID, 0, 0)
	if feed.Tasks[0].State != TaskDone {
		t.Fatalf("card is not live: %+v", feed.Tasks)
	}
	// A task of a non-member cannot be filed into the group.
	out := mustAssistant(t, s, "o", "Cy")
	if _, _, err := s.CreateTask("", CreateTaskInput{AssistantID: out.ID, GroupID: g.ID, Kind: KindResearch, Title: "x", Brief: "x"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("outsider: %v", err)
	}
}

func TestGroupMembersKeepAtLeastTwoAndRemovalSkipsPendingDeliveries(t *testing.T) {
	s, _ := openTest(t)
	g, as := groupOf(t, s, "Ada", "Bo", "Cy")
	res, _, _ := s.PostGroupMessage("", PostMessageInput{GroupID: g.ID, AuthorKind: AuthorUser, Body: "@Cy hi", Mentions: []string{as[2].ID}})
	g2, _, err := s.RemoveGroupMember("rm", g.ID, as[2].ID)
	if err != nil || len(g2.Members) != 2 {
		t.Fatalf("remove: %+v %v", g2, err)
	}
	if _, _, err := s.RemoveGroupMember("rm2", g.ID, as[1].ID); !errors.Is(err, ErrBadState) {
		t.Fatalf("below two must be refused: %v", err)
	}
	feed, _ := s.GroupFeedSince(g.ID, 0, 0)
	for _, d := range feed.Deliveries {
		if d.ID == res.Deliveries[0].ID && d.State != DeliverySkipped {
			t.Fatalf("pending delivery of a removed member = %+v", d)
		}
	}
	if _, _, err := s.AddGroupMember("add", g.ID, as[2].ID); err != nil {
		t.Fatal(err)
	}
}

func TestGroupAPIPostVersusSay(t *testing.T) {
	s, _ := openTest(t)
	api := &API{Store: s}
	g, as := groupOf(t, s, "Ada", "Bo")
	if _, err := api.Handle([]string{"assistant", "group", "post"}, []byte(`{"groupId":"`+g.ID+`","as":"`+as[0].ID+`","body":"x"}`)); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("post must not accept an author: %v", err)
	}
	if _, err := api.Handle([]string{"assistant", "group", "say"}, []byte(`{"groupId":"`+g.ID+`","body":"x"}`)); err == nil {
		t.Fatalf("say needs as")
	}
	out, err := api.Handle([]string{"assistant", "group", "post"}, []byte(`{"requestId":"r","groupId":"`+g.ID+`","body":"@Ada go","mentions":["`+as[0].ID+`"]}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if out.(envelope)["schema"] != wireSchema {
		t.Fatalf("out = %v", out)
	}
	if _, err := api.Handle([]string{"assistant", "group", "show", g.ID, "--since", "0"}, nil); err != nil {
		t.Fatalf("show: %v", err)
	}
}

// A launched Agent is not a briefed Agent: while its first turn runs (or a
// dialog blocks it) the launch prompt is not credited, the Agent can already
// speak because its pane is real, and a blocked Agent says so in the Group.
func TestLaunchIsNotReadyUntilTheFirstTurnFinishes(t *testing.T) {
	s, _ := openTest(t)
	g, as := groupOf(t, s, "Ada", "Bo")
	ga, _, err := s.StartGroupAgent("go", g.ID, as[1].ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	task, _ := s.GetTask(ga.TaskID)
	if _, _, err := s.PostGroupMessage("", PostMessageInput{GroupID: g.ID, AuthorKind: AuthorUser, Body: "@Bo hello", Mentions: []string{as[1].ID}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.groupLaunchPrompt(as[1], g.ID); err != nil {
		t.Fatal(err)
	}
	run := Run{ID: "r_1", Status: RunRunning, HerdrSession: "default", WorkspaceID: "w9", PaneID: "w9:p1"}
	s.syncGroupAgent(task, run)
	got, _ := s.GetGroupAgent(g.ID, as[1].ID)
	if got.State != GroupAgentStarting || got.LastSeq != 0 || !got.Live() {
		t.Fatalf("a running launch turn is starting, unbriefed and live: %+v", got)
	}
	if _, _, err := s.PostGroupMessage("", PostMessageInput{GroupID: g.ID, AuthorKind: AuthorAssistant, AuthorID: as[1].ID, Body: "hello back"}); err != nil {
		t.Fatalf("an Agent with a real pane may speak during its first turn: %v", err)
	}
	// A dialog blocks it: no credit, and the Group is told why.
	run.Status, run.Error = RunBlocked, "Claude Code is asking which project MCP servers to enable"
	s.syncGroupAgent(task, run)
	got, _ = s.GetGroupAgent(g.ID, as[1].ID)
	if got.State != GroupAgentBlocked || got.LastSeq != 0 {
		t.Fatalf("blocked = %+v", got)
	}
	feed, _ := s.GroupFeedSince(g.ID, 0, 0)
	saw := false
	for _, m := range feed.Messages {
		saw = saw || (m.Kind == MsgFact && strings.Contains(string(m.Fact), "agent_blocked") && strings.Contains(string(m.Fact), "MCP servers"))
	}
	if !saw {
		t.Fatalf("the Group is not told the Agent is blocked: %+v", feed.Messages)
	}
	// Nothing was acknowledged by the Agent, so the pump really submits (with the
	// standing brief), and a successful submit brings the Agent back to ready.
	var sent string
	p := &GroupPump{Store: s, Submit: func(_, _, text string) error { sent = text; return nil }}
	rep, err := p.Tick()
	if err != nil || rep.Delivered != 1 || sent == "" {
		t.Fatalf("rep=%+v err=%v sent=%q", rep, err, sent)
	}
	if !strings.Contains(sent, "group say") || !strings.Contains(sent, "@Bo hello") {
		t.Fatalf("an unbriefed Agent gets the brief with the message: %q", sent)
	}
	if got, _ = s.GetGroupAgent(g.ID, as[1].ID); got.State != GroupAgentReady {
		t.Fatalf("answered dialog → ready, got %+v", got)
	}
}

// Text is never typed into a dialog: while a startup question is on screen the
// delivery waits (no attempt spent, nothing submitted), and goes through once
// the person has answered it.
func TestPumpWaitsOutADialogInsteadOfTypingIntoIt(t *testing.T) {
	s, _ := openTest(t)
	g, as := groupOf(t, s, "Ada", "Bo")
	readyGroupAgent(t, s, g, as[0])
	if _, _, err := s.PostGroupMessage("", PostMessageInput{GroupID: g.ID, AuthorKind: AuthorUser, Body: "@Ada hi", Mentions: []string{as[0].ID}}); err != nil {
		t.Fatal(err)
	}
	clock := time.Now()
	dialog, submits := true, 0
	p := &GroupPump{Store: s, Now: func() time.Time { return clock },
		Blocked: func(_, _ string) (string, bool) {
			return "Claude Code is asking which project MCP servers to enable", dialog
		},
		Submit: func(_, _, _ string) error { submits++; return nil }}
	for i := 0; i < 3; i++ {
		clock = clock.Add(3 * time.Second)
		if rep, _ := p.Tick(); rep.Delivered != 0 || submits != 0 {
			t.Fatalf("typed into a dialog: %+v submits=%d", rep, submits)
		}
	}
	feed, _ := s.GroupFeedSince(g.ID, 0, 0)
	blocked := 0
	for _, m := range feed.Messages {
		if m.Kind == MsgFact && strings.Contains(string(m.Fact), "agent_blocked") {
			blocked++
		}
	}
	if blocked != 1 || feed.Deliveries[0].State != DeliveryQueued || feed.Deliveries[0].Attempts != 0 {
		t.Fatalf("one receipt, delivery waiting, no attempt spent: blocked=%d %+v", blocked, feed.Deliveries[0])
	}
	if ga, _ := s.GetGroupAgent(g.ID, as[0].ID); ga.State != GroupAgentBlocked {
		t.Fatalf("agent = %+v", ga)
	}
	dialog = false
	clock = clock.Add(3 * time.Second)
	if rep, _ := p.Tick(); rep.Delivered != 1 || submits != 1 {
		t.Fatalf("after the answer: %+v submits=%d", rep, submits)
	}
	if ga, _ := s.GetGroupAgent(g.ID, as[0].ID); ga.State != GroupAgentReady {
		t.Fatalf("agent = %+v", ga)
	}
	// A dialog that never goes away ends in a visible failure, not a silent queue.
	if _, _, err := s.PostGroupMessage("", PostMessageInput{GroupID: g.ID, AuthorKind: AuthorUser, Body: "@Ada again", Mentions: []string{as[0].ID}}); err != nil {
		t.Fatal(err)
	}
	dialog = true
	p.StartWait = time.Minute
	for i := 0; i < 30; i++ {
		clock = clock.Add(5 * time.Second)
		_, _ = p.Tick()
	}
	feed, _ = s.GroupFeedSince(g.ID, 0, 0)
	if last := feed.Deliveries[len(feed.Deliveries)-1]; last.State != DeliveryFailed {
		t.Fatalf("a never-answered dialog must fail visibly: %+v", last)
	}
}

// MU-07: a message belongs to the participant id that wrote it, never to a name —
// renames, equal names and removed members cannot move a message or a delivery.
func TestGroupAuthorshipFollowsIdsNotNames(t *testing.T) {
	s, _ := openTest(t)
	g, as := groupOf(t, s, "Ada", "Bo")
	// Bo is renamed to the other member's name: two members now read "Ada".
	if _, _, err := s.RenameAssistant("rn", as[1].ID, "Ada", 1); err != nil {
		t.Fatal(err)
	}
	res, _, err := s.PostGroupMessage("p1", PostMessageInput{GroupID: g.ID, AuthorKind: AuthorUser, Body: "@Ada first", Mentions: []string{as[1].ID}})
	if err != nil || len(res.Deliveries) != 1 || res.Deliveries[0].AssistantID != as[1].ID {
		t.Fatalf("an equal name must still address exactly the id asked: %+v %v", res, err)
	}
	// The member that was renamed keeps its id on the stored message and delivery.
	feed, _ := s.GroupFeedSince(g.ID, 0, 0)
	if len(feed.Messages) != 1 || feed.Messages[0].Mentions[0] != as[1].ID || feed.Deliveries[0].AssistantID != as[1].ID {
		t.Fatalf("feed follows ids: %+v", feed)
	}
	// Renaming the group changes only its title.
	g2, _, err := s.RenameGroup("rg", g.ID, "Renamed", g.Revision)
	if err != nil || g2.Title != "Renamed" || len(g2.Members) != 2 {
		t.Fatalf("rename group = %+v %v", g2, err)
	}
	after, _ := s.GroupFeedSince(g.ID, 0, 0)
	if len(after.Messages) != 1 || after.Messages[0].ID != res.Message.ID || after.Messages[0].GroupID != g.ID {
		t.Fatalf("a rename moved a message: %+v", after.Messages)
	}
	// Another group keeps a separate conversation even with the same members' names.
	g3, _, err := s.CreateGroup("g3", "Other", []string{as[0].ID, as[1].ID})
	if err != nil {
		t.Fatal(err)
	}
	other, _ := s.GroupFeedSince(g3.ID, 0, 0)
	if g3.ID == g.ID || len(other.Messages) != 0 {
		t.Fatalf("groups share a conversation: %+v", other)
	}
}
