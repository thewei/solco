package personalagent

import (
	"errors"
	"strconv"
	"testing"
)

func TestActivityReplaysAcrossConsumersAndAcksIndependently(t *testing.T) {
	s, path := openTest(t)
	a := mustAssistant(t, s, "activity-owner", "Ada")
	b := mustAssistant(t, s, "activity-other", "Bo")
	for _, sText := range []string{"queued", "running", "blocked"} {
		if _, err := s.newFact(Fact{AssistantID: a.ID, Kind: FactTaskAdded, Text: sText}, "activity:"+sText); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.newFact(Fact{AssistantID: b.ID, Kind: FactTaskAdded, Text: "private"}, "activity:private"); err != nil {
		t.Fatal(err)
	}
	p, err := s.Activity(a.ID, "device:phone", 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Facts) != 2 || !p.HasMore || p.AckSeq != 0 || p.Facts[0].Text != "queued" || p.Facts[1].Text != "running" || p.NextSeq != p.Facts[1].Seq {
		t.Fatalf("page: %+v", p)
	}
	ack, replay, err := s.AckActivity("ack-1", a.ID, "device:phone", p.NextSeq)
	if err != nil || replay || ack != p.NextSeq {
		t.Fatalf("ack: %d %t %v", ack, replay, err)
	}
	if v, replay, err := s.AckActivity("ack-1", a.ID, "device:phone", p.NextSeq); err != nil || !replay || v != ack {
		t.Fatalf("replay: %d %t %v", v, replay, err)
	}
	if _, _, err := s.AckActivity("ack-1", a.ID, "device:phone", p.Facts[0].Seq); !errors.Is(err, ErrRequestConflict) {
		t.Fatalf("replay conflict: %v", err)
	}
	// A transport connection loss cannot mark a read as delivered.
	next, err := s.Activity(a.ID, "device:phone", ack, 2)
	if err != nil || len(next.Facts) != 1 || next.AckSeq != ack || next.HasMore || next.Facts[0].Text != "blocked" {
		t.Fatalf("remaining: %+v %v", next, err)
	}
	other, err := s.Activity(a.ID, "device:tablet", 0, 5)
	if err != nil || other.AckSeq != 0 || len(other.Facts) != 3 {
		t.Fatalf("tablet: %+v %v", other, err)
	}
	if _, _, err := s.AckActivity("invalid", a.ID, "device:phone", 4_000_000); !errors.Is(err, ErrInvalid) {
		t.Fatalf("future watermark: %v", err)
	}
	if _, _, err := s.AckActivity("cross", a.ID, "device:phone", other.Facts[0].Seq); err != nil {
		t.Fatalf("lower ack: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopen, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopen.Close()
	recovered, err := reopen.Activity(a.ID, "device:phone", ack, 2)
	if err != nil || recovered.AckSeq != ack || len(recovered.Facts) != 1 {
		t.Fatalf("recovered: %+v %v", recovered, err)
	}
}

func TestActivitySequenceNeverReusedAfterPurge(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "seq", "Ada")
	if _, err := s.newFact(Fact{AssistantID: a.ID, Kind: FactTaskAdded, Text: "first"}, "first"); err != nil {
		t.Fatal(err)
	}
	p, err := s.Activity(a.ID, "desktop:main", 0, 1)
	if err != nil || len(p.Facts) != 1 {
		t.Fatalf("first: %+v %v", p, err)
	}
	first := p.NextSeq
	if _, err := s.db.Exec("DELETE FROM goal_facts WHERE assistant_id=?", a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.newFact(Fact{AssistantID: a.ID, Kind: FactTaskAdded, Text: "second"}, "second"); err != nil {
		t.Fatal(err)
	}
	p, err = s.Activity(a.ID, "desktop:main", first, 5)
	if err != nil || len(p.Facts) != 1 || p.Facts[0].Seq <= first || p.Facts[0].Text != "second" {
		t.Fatalf("sequence reused or vanished: %+v %v", p, err)
	}
}

func TestActivityInvalidInputsAndOpaqueConsumers(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "invalid-activity", "Ada")
	for _, tc := range []struct {
		consumer string
		after    int64
		limit    int
	}{
		{"", 0, 10}, {"../../secrets", 0, 10}, {"device:a", -1, 10}, {"device:a", 0, 201},
	} {
		if _, err := s.Activity(a.ID, tc.consumer, tc.after, tc.limit); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%+v: %v", tc, err)
		}
	}
	if _, err := s.Activity("a_notpresent", "device:a", 0, 10); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing Assistant: %v", err)
	}
	if _, _, err := s.AckActivity("x", a.ID, "device:a", -1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("negative ACK: %v", err)
	}
	// No fact exists to ACK: an arbitrary cursor is not accepted.
	if _, _, err := s.AckActivity("y", a.ID, "device:a", 1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("phantom ACK: %v", err)
	}
}

func TestActivityCLIListAndAck(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "cli-act", "Ada")
	if _, err := s.newFact(Fact{AssistantID: a.ID, Kind: FactAccepted, Text: "Objective accepted"}, "accepted"); err != nil {
		t.Fatal(err)
	}
	api := &API{Store: s}
	got, err := call(t, api, "", "assistant", "activity", "list", a.ID, "--consumer", "device:phone", "--after", "0", "--limit", "1")
	if err != nil {
		t.Fatal(err)
	}
	page := got["activity"].(map[string]any)
	fs := page["facts"].([]any)
	if len(fs) != 1 || page["ackSeq"] != float64(0) {
		t.Fatalf("CLI page: %+v", page)
	}
	seq := fs[0].(map[string]any)["seq"].(float64)
	body := "{\"requestId\":\"ack1\",\"consumer\":\"device:phone\",\"seq\":" + strconv.FormatInt(int64(seq), 10) + "}"
	ack, err := call(t, api, body, "assistant", "activity", "ack", a.ID)
	if err != nil || ack["ackSeq"] != seq {
		t.Fatalf("CLI ACK: %+v %v", ack, err)
	}
	if _, err := call(t, api, "", "assistant", "activity", "list", a.ID, "--consumer", "device:phone", "--after", "-1"); codeOf(err) != "invalid" {
		t.Fatalf("invalid CLI: %v", err)
	}
}

// Removing an archived Assistant must also erase per-device read state.
// Otherwise the FK would block deletion or private activity markers survive.
func TestDeletingArchivedAssistantClearsActivityCursors(t *testing.T) {
	s, _ := openTest(t)
	anchorPrimary(t, s)
	a := mustAssistant(t, s, "delete-activity-owner", "Ada")
	if _, err := s.newFact(Fact{AssistantID: a.ID, Kind: FactPlanned, Text: "Plan"}, "plan"); err != nil {
		t.Fatal(err)
	}
	p, err := s.Activity(a.ID, "device:phone", 0, 10)
	if err != nil || len(p.Facts) != 1 {
		t.Fatalf("initial page: %+v %v", p, err)
	}
	if _, _, err := s.AckActivity("ack", a.ID, "device:phone", p.NextSeq); err != nil {
		t.Fatal(err)
	}
	archived, _, err := s.ArchiveAssistant("archive", a.ID, a.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteArchivedAssistant(a.ID, archived.Revision); err != nil {
		t.Fatalf("delete with ACK state: %v", err)
	}
	var n int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM activity_cursors WHERE assistant_id=?", a.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("leftover Activity cursor rows: %d %v", n, err)
	}
	if err := s.db.QueryRow("SELECT COUNT(*) FROM receipts WHERE request_id='ack'").Scan(&n); err != nil || n != 0 {
		t.Fatalf("leftover Activity ACK receipt: %d %v", n, err)
	}
}

func TestUnreadActivityIsSeparateFromExplicitSeenCursor(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "notice-owner", "Ada")
	for _, v := range []string{"created", "working", "done"} {
		if _, err := s.newFact(Fact{AssistantID: a.ID, Kind: FactPulse, Text: v}, "notice:"+v); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.UnreadActivity(a.ID, "device:phone:notifications", 2)
	if err != nil || len(page.Facts) != 2 || !page.HasMore || page.AckSeq != 0 {
		t.Fatalf("notifications: %+v %v", page, err)
	}
	if _, _, err := s.AckActivity("delivered", a.ID, "device:phone:notifications", page.NextSeq); err != nil {
		t.Fatal(err)
	}
	unread, err := s.UnreadActivity(a.ID, "device:phone:notifications", 2)
	if err != nil || len(unread.Facts) != 1 || unread.Facts[0].Text != "done" || unread.HasMore {
		t.Fatalf("pending notification: %+v %v", unread, err)
	}
	seen, err := s.UnreadActivity(a.ID, "device:phone", 5)
	if err != nil || len(seen.Facts) != 3 || seen.AckSeq != 0 {
		t.Fatalf("notification delivery changed seen state: %+v %v", seen, err)
	}
	got, err := call(t, &API{Store: s}, ``, "assistant", "activity", "list", a.ID, "--consumer", "device:phone:notifications", "--unread", "--limit", "2")
	if err != nil {
		t.Fatal(err)
	}
	if len(got["activity"].(map[string]any)["facts"].([]any)) != 1 {
		t.Fatalf("CLI unread mismatch: %v", got)
	}
	if _, err := call(t, &API{Store: s}, ``, "assistant", "activity", "list", a.ID, "--consumer", "device:phone", "--after", "0", "--unread"); codeOf(err) != "invalid" {
		t.Fatalf("ambiguous cursor accepted: %v", err)
	}
}
