package personalagent

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// Audit 2026-11-04 P0-01: the core is the authority on who a message wakes.
func TestMentionsNeedARealNameTokenAndNeverDoubleBookEqualNames(t *testing.T) {
	s, _ := openTest(t)
	g, as := groupOf(t, s, "Bo", "Bobby")
	post := func(body string, ids ...string) (PostResult, error) {
		res, _, err := s.PostGroupMessage("", PostMessageInput{GroupID: g.ID, AuthorKind: AuthorUser, Body: body, Mentions: ids})
		return res, err
	}
	if _, err := post("@Bobby hi", as[0].ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("@Bobby must not wake Bo: %v", err)
	}
	if res, err := post("@Bobby hi", as[1].ID); err != nil || len(res.Deliveries) != 1 || res.Deliveries[0].AssistantID != as[1].ID {
		t.Fatalf("@Bobby wakes Bobby: %+v %v", res, err)
	}
	if res, err := post("@Bo and @Bobby", as[0].ID, as[1].ID); err != nil || len(res.Deliveries) != 2 {
		t.Fatalf("both named: %+v %v", res, err)
	}
	if _, err := post("hello@Bo", as[0].ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an address inside a word is no mention: %v", err)
	}

	// Two members with one name: what was picked decides, and a message cannot wake more of them than it names.
	if _, _, err := s.RenameAssistant("rn", as[1].ID, "Bo", 1); err != nil {
		t.Fatal(err)
	}
	g2, as2 := g, as
	if _, err := post("@Bo look", as2[0].ID, as2[1].ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("one @Bo cannot wake both Bos: %v", err)
	}
	if res, err := post("@Bo look", as2[1].ID); err != nil || len(res.Deliveries) != 1 || res.Deliveries[0].AssistantID != as2[1].ID {
		t.Fatalf("the picked Bo only: %+v %v", res, err)
	}
	if res, err := post("@Bo and @Bo", as2[0].ID, as2[1].ID); err != nil || len(res.Deliveries) != 2 {
		t.Fatalf("two tokens, two Bos: %+v %v", res, err)
	}
	_ = g2
}

func TestMentionsWorkForCJKNamesWithoutSpaces(t *testing.T) {
	s, _ := openTest(t)
	g, as := groupOf(t, s, "张三", "李四")
	res, _, err := s.PostGroupMessage("", PostMessageInput{GroupID: g.ID, AuthorKind: AuthorUser, Body: "请@张三帮忙看看", Mentions: []string{as[0].ID}})
	if err != nil || len(res.Deliveries) != 1 {
		t.Fatalf("cjk mention: %+v %v", res, err)
	}
	if _, _, err := s.PostGroupMessage("", PostMessageInput{GroupID: g.ID, AuthorKind: AuthorUser, Body: "请@张三帮忙看看", Mentions: []string{as[1].ID}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a mention of someone else: %v", err)
	}
}

// appendHistoryOnly builds pagination fixtures without filling the live
// delivery queue, which is independently capped at twenty messages.
func appendHistoryOnly(t *testing.T, s *Store, groupID, body string) {
	t.Helper()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := insertMessage(tx, s.nowMS(), GroupMessage{ID: newID("m"), GroupID: groupID, AuthorKind: AuthorUser, Kind: MsgText, Body: body}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// Audit HIGH-01/02: the newest page of one Group, with only its own deliveries.
func TestGroupFeedNewestPageSurvivesInterleavedGroups(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "i1", "Ada")
	b := mustAssistant(t, s, "i2", "Bo")
	c := mustAssistant(t, s, "i3", "Cy")
	g1, _, _ := s.CreateGroup("ig1", "one", []string{a.ID, b.ID})
	g2, _, _ := s.CreateGroup("ig2", "two", []string{b.ID, c.ID})
	for i := 0; i < 30; i++ {
		for _, g := range []Group{g1, g2} {
			appendHistoryOnly(t, s, g.ID, fmt.Sprintf("%s-%d", g.Title, i))
		}
	}
	feed, err := s.GroupFeedSince(g1.ID, 0, 20)
	if err != nil || len(feed.Messages) != 20 || feed.Messages[19].Body != "one-29" || feed.Messages[0].Body != "one-10" {
		t.Fatalf("newest 20 of group one: %d err=%v", len(feed.Messages), err)
	}
}

func TestGroupFeedDeliveriesStayInsideTheMessagePage(t *testing.T) {
	s, _ := openTest(t)
	g, as := groupOf(t, s, "Ada", "Bo")
	for i := 0; i < 4; i++ {
		if _, _, err := s.PostGroupMessage("", PostMessageInput{GroupID: g.ID, AuthorKind: AuthorUser, Body: "@Ada x", Mentions: []string{as[0].ID}}); err != nil {
			t.Fatal(err)
		}
	}
	feed, err := s.GroupFeedSince(g.ID, 1, 1)
	if err != nil || len(feed.Messages) != 1 || len(feed.Deliveries) != 1 {
		t.Fatalf("one message page carries its own delivery only: msgs=%d deliveries=%d err=%v", len(feed.Messages), len(feed.Deliveries), err)
	}
}

// Audit BLOCKER-02: a delivery is acknowledged only with its @-mention in the prompt.
func TestDeliveryBeyondTheFirstFiftyUnseenStillCarriesTheMention(t *testing.T) {
	s, _ := openTest(t)
	g, as := groupOf(t, s, "Ada", "Bo")
	readyGroupAgent(t, s, g, as[0])
	for i := 0; i < 55; i++ {
		appendHistoryOnly(t, s, g.ID, fmt.Sprintf("ordinary context %d", i))
	}
	if _, _, err := s.PostGroupMessage("", PostMessageInput{GroupID: g.ID, AuthorKind: AuthorUser, Body: "@Ada important action", Mentions: []string{as[0].ID}}); err != nil {
		t.Fatal(err)
	}
	var got []string
	p := &GroupPump{Store: s, Submit: func(sess, pane, text string) error {
		got = append(got, text)
		return nil
	}}
	rep, err := p.Tick()
	if err != nil || rep.Delivered != 1 || len(got) != 1 {
		t.Fatalf("rep=%+v err=%v prompts=%d", rep, err, len(got))
	}
	if !strings.Contains(got[0], ">>> You (the person)") || !strings.Contains(got[0], "@Ada important action") || !strings.Contains(got[0], "earlier messages are not shown") {
		t.Fatalf("the mention is missing from the prompt: %q", got[0])
	}
}
