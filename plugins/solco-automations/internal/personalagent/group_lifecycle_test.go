package personalagent

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func closedPanes(string, string, string) (bool, error) { return true, nil }

// runningGroupAgent gives an Assistant a launched Group Agent with a real pane.
func runningGroupAgent(t *testing.T, s *Store, g Group, a Assistant, pane string) GroupAgent {
	t.Helper()
	ga, _, err := s.StartGroupAgent("ga-"+a.ID, g.ID, a.ID, "", "")
	if err != nil {
		t.Fatalf("start group agent: %v", err)
	}
	task, _ := s.GetTask(ga.TaskID)
	s.syncGroupAgent(task, Run{ID: "r_" + a.ID, Status: RunRunning, HerdrSession: "default", WorkspaceID: "w1", PaneID: pane})
	got, err := s.GetGroupAgent(g.ID, a.ID)
	if err != nil || !got.Live() {
		t.Fatalf("precondition: group agent not live: %+v %v", got, err)
	}
	return got
}

func TestArchiveRestoreGroupHidesAndKeepsTheConversation(t *testing.T) {
	s, _ := openTest(t)
	g, _ := groupOf(t, s, "Ada", "Bo")
	post, _, err := s.PostGroupMessage("p1", PostMessageInput{GroupID: g.ID, AuthorKind: AuthorUser, Body: "hello everyone"})
	if err != nil || len(post.Deliveries) != 2 {
		t.Fatalf("post: %+v %v", post, err)
	}
	if _, _, err := s.ArchiveGroup("ar-stale", g.ID, g.Revision+1, closedPanes); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale revision = %v", err)
	}
	archived, replayed, err := s.ArchiveGroup("ar1", g.ID, g.Revision, closedPanes)
	if err != nil || replayed || archived.Status != StatusArchived {
		t.Fatalf("archive: %+v %v %v", archived, replayed, err)
	}
	if again, rep, err := s.ArchiveGroup("ar1", g.ID, g.Revision, closedPanes); err != nil || !rep || again.Revision != archived.Revision {
		t.Fatalf("replay: %+v %v %v", again, rep, err)
	}
	if list, _ := s.ListGroups(); len(list) != 0 {
		t.Fatalf("archived group leaked into the live list: %+v", list)
	}
	if hidden, _ := s.ListArchivedGroups(); len(hidden) != 1 || hidden[0].ID != g.ID {
		t.Fatalf("archive list: %+v", hidden)
	}
	if _, _, err := s.PostGroupMessage("p2", PostMessageInput{GroupID: g.ID, AuthorKind: AuthorUser, Body: "anyone?"}); !errors.Is(err, ErrBadState) {
		t.Fatalf("an archived group takes no message: %v", err)
	}
	var open int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM group_deliveries WHERE group_id=? AND state IN (?,?)`, g.ID, DeliveryQueued, DeliveryDelivering).Scan(&open)
	if open != 0 {
		t.Fatalf("waiting deliveries must be skipped on archive: %d", open)
	}
	if feed, err := s.GroupFeedSince(g.ID, 0, 0); err != nil || len(feed.Messages) == 0 {
		t.Fatalf("the log must stay readable: %+v %v", feed, err)
	}
	if _, _, err := s.RestoreGroup("rs-stale", g.ID, archived.Revision+9); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale restore = %v", err)
	}
	restored, _, err := s.RestoreGroup("rs1", g.ID, archived.Revision)
	if err != nil || restored.Status != "active" {
		t.Fatalf("restore: %+v %v", restored, err)
	}
	if _, _, err := s.RestoreGroup("rs2", g.ID, restored.Revision); !errors.Is(err, ErrBadState) {
		t.Fatalf("restoring an active group = %v", err)
	}
	if _, _, err := s.PostGroupMessage("p3", PostMessageInput{GroupID: g.ID, AuthorKind: AuthorUser, Body: "back"}); err != nil {
		t.Fatalf("a restored group takes messages again: %v", err)
	}
}

func TestArchiveGroupNeedsPositiveClosureOfGroupAgentPanes(t *testing.T) {
	s, _ := openTest(t)
	g, as := groupOf(t, s, "Ada", "Bo")
	runningGroupAgent(t, s, g, as[0], "w1:p7")
	if _, _, err := s.ArchiveGroup("a-none", g.ID, g.Revision); !errors.Is(err, ErrBadState) {
		t.Fatalf("no verifier must refuse: %v", err)
	}
	if _, _, err := s.ArchiveGroup("a-open", g.ID, g.Revision, func(string, string, string) (bool, error) { return false, nil }); !errors.Is(err, ErrBadState) || !strings.Contains(err.Error(), "close the group agents") {
		t.Fatalf("an open pane must refuse with the instruction: %v", err)
	}
	if _, _, err := s.ArchiveGroup("a-off", g.ID, g.Revision, func(string, string, string) (bool, error) { return false, errors.New("offline") }); !errors.Is(err, ErrBadState) {
		t.Fatalf("an unreadable Herdr must refuse: %v", err)
	}
	if got, _ := s.GetGroup(g.ID); got.Status != "active" {
		t.Fatalf("a refusal must change nothing: %+v", got)
	}
	var target string
	archived, _, err := s.ArchiveGroup("a-ok", g.ID, g.Revision, func(sess, ws, pane string) (bool, error) { target = sess + "/" + ws + "/" + pane; return true, nil })
	if err != nil || archived.Status != StatusArchived || target != "default/w1/w1:p7" {
		t.Fatalf("archive after closure: %+v %v target=%q", archived, err, target)
	}
	if ga, _ := s.GetGroupAgent(g.ID, as[0].ID); ga.Live() {
		t.Fatalf("a confirmed-closed agent must read stopped: %+v", ga)
	}
}

func TestArchiveGroupRefusesWhileAnAgentIsStillStarting(t *testing.T) {
	s, _ := openTest(t)
	g, as := groupOf(t, s, "Ada", "Bo")
	if _, _, err := s.StartGroupAgent("ga1", g.ID, as[0].ID, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ArchiveGroup("a1", g.ID, g.Revision, closedPanes); !errors.Is(err, ErrBadState) || !strings.Contains(err.Error(), "still starting") {
		t.Fatalf("a launching agent has no pane to verify: %v", err)
	}
}

func TestDeleteArchivedGroupRemovesTheConversationAndUnblocksAssistantDeletion(t *testing.T) {
	s, _ := openTest(t)
	g, as := groupOf(t, s, "Ada", "Bo")
	ga := runningGroupAgent(t, s, g, as[0], "w1:p9")
	if _, _, err := s.PostGroupMessage("p1", PostMessageInput{GroupID: g.ID, AuthorKind: AuthorUser, Body: "secret plan"}); err != nil {
		t.Fatal(err)
	}
	other, _ := groupOf2(t, s, as)
	if err := s.DeleteArchivedGroup(g.ID, g.Revision, closedPanes); !errors.Is(err, ErrBadState) {
		t.Fatalf("an active group cannot be deleted: %v", err)
	}
	dir := filepath.Join(WorkDirRoot(), "personal", "groups", g.ID, as[0].ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	archived, _, err := s.ArchiveGroup("a1", g.ID, g.Revision, closedPanes)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteArchivedGroup(g.ID, archived.Revision+1, closedPanes); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale delete = %v", err)
	}
	if err := s.DeleteArchivedGroup(g.ID, archived.Revision, func(string, string, string) (bool, error) { return false, nil }); !errors.Is(err, ErrBadState) {
		t.Fatalf("an open pane must refuse deletion: %v", err)
	}
	if err := s.DeleteArchivedGroup(g.ID, archived.Revision, closedPanes); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GetGroup(g.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the group survived: %v", err)
	}
	for _, q := range []string{
		`SELECT COUNT(*) FROM group_messages WHERE group_id=?`,
		`SELECT COUNT(*) FROM group_members WHERE group_id=?`,
		`SELECT COUNT(*) FROM group_agents WHERE group_id=?`,
		`SELECT COUNT(*) FROM group_deliveries WHERE group_id=?`,
		`SELECT COUNT(*) FROM receipts WHERE instr(result, ?) > 0`,
	} {
		var n int
		if err := s.db.QueryRow(q, g.ID).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s left %d rows (%v)", q, n, err)
		}
	}
	if _, err := os.Stat(filepath.Join(WorkDirRoot(), "personal", "groups", g.ID)); !os.IsNotExist(err) {
		t.Fatalf("the group folder survived: %v", err)
	}
	if live, _ := s.ListGroups(); len(live) != 1 || live[0].ID != other.ID {
		t.Fatalf("another group must be untouched: %+v", live)
	}
	_ = ga
	// Ada is still in `other`, Bo only in the deleted one: Bo can now leave.
	bo, _ := s.GetAssistant(as[1].ID)
	arch, _, err := s.ArchiveAssistant("arch-bo", bo.ID, bo.Revision, closedPanes)
	if err != nil {
		t.Fatalf("archive Bo: %v", err)
	}
	if err := s.DeleteArchivedAssistant(bo.ID, arch.Revision, closedPanes); err != nil {
		t.Fatalf("a deleted group must not keep its members undeletable: %v", err)
	}
}

// groupOf2 makes a second group over the same two Assistants plus a third, so
// deleting the first can be checked against a bystander.
func groupOf2(t *testing.T, s *Store, as []Assistant) (Group, Assistant) {
	t.Helper()
	c := mustAssistant(t, s, "mk-Cy", "Cy")
	g, _, err := s.CreateGroup("g-other", "Other", []string{as[0].ID, c.ID})
	if err != nil {
		t.Fatal(err)
	}
	return g, c
}
