package personalagent

import (
	"fmt"
	"testing"
)

// anchorPrimary makes the Primary first, for tests that archive or delete an
// Assistant: the first one ever created is protected, so such a test works on
// the second.
func anchorPrimary(t *testing.T, s *Store) Assistant {
	t.Helper()
	return mustAssistant(t, s, "anchor-primary", "Lead")
}

func TestFirstAssistantIsThePrimaryAndLeadsEveryList(t *testing.T) {
	s, _ := openTest(t)
	api := &API{Store: s}
	// The system "Automations" assistant is older than any Assistant the person
	// makes, and must never be named Primary.
	sys, err := s.AutomationsAssistant()
	if err != nil {
		t.Fatal(err)
	}
	if s.PrimaryAssistantID() != "" {
		t.Fatal("no ordinary Assistant yet: no Primary")
	}
	ada := mustAssistant(t, s, "a-ada-0001", "Ada")
	bob := mustAssistant(t, s, "a-bob-0001", "Bob")
	if !ada.Primary || bob.Primary || s.PrimaryAssistantID() != ada.ID {
		t.Fatalf("the first Assistant is the Primary: ada=%v bob=%v id=%s", ada.Primary, bob.Primary, s.PrimaryAssistantID())
	}
	list, _ := s.ListAssistants()
	if list[0].ID != ada.ID || !list[0].Primary {
		t.Fatalf("the Primary leads the list: %+v", list)
	}
	if sys.Primary {
		t.Fatal("the system assistant is never the Primary")
	}
	ov, err := call(t, api, "", "assistant", "overview")
	if err != nil {
		t.Fatal(err)
	}
	rows := ov["assistants"].([]any)
	if first := rows[0].(map[string]any); first["id"] != ada.ID || first["primary"] != true {
		t.Fatalf("overview leads with the Primary: %v", rows[0])
	}
}

func TestPrimaryCannotBeArchivedOrDeletedButOthersCan(t *testing.T) {
	s, _ := openTest(t)
	ada := mustAssistant(t, s, "a-ada-0001", "Ada")
	bob := mustAssistant(t, s, "a-bob-0001", "Bob")
	if _, _, err := s.ArchiveAssistant("arch-ada-01", ada.ID, ada.Revision); codeOf(asCoded(err)) != "bad_state" {
		t.Fatalf("archiving the Primary must be refused with bad_state: %v", err)
	}
	if err := s.DeleteArchivedAssistant(ada.ID, ada.Revision); err == nil {
		t.Fatal("deleting the Primary must be refused")
	}
	if cur, _ := s.GetAssistant(ada.ID); cur.Status != StatusActive {
		t.Fatalf("the Primary stays active: %s", cur.Status)
	}
	if _, _, err := s.ArchiveAssistant("arch-bob-01", bob.ID, bob.Revision); err != nil {
		t.Fatalf("an ordinary Assistant still archives: %v", err)
	}
}

func TestEnsurePrimaryHealsAMissingOrStaleMarker(t *testing.T) {
	s, _ := openTest(t)
	if _, err := s.AutomationsAssistant(); err != nil {
		t.Fatal(err)
	}
	ada := mustAssistant(t, s, "a-ada-0001", "Ada")
	mustAssistant(t, s, "a-bob-0001", "Bob")
	// A database from before the marker.
	if _, err := s.db.Exec(`DELETE FROM meta WHERE key=?`, metaPrimary); err != nil {
		t.Fatal(err)
	}
	if s.PrimaryAssistantID() != "" {
		t.Fatal("marker removed")
	}
	if id, err := s.EnsurePrimary(); err != nil || id != ada.ID {
		t.Fatalf("EnsurePrimary picks the oldest ordinary Assistant: %q %v", id, err)
	}
	// A live marker is never moved.
	if id, _ := s.EnsurePrimary(); id != ada.ID {
		t.Fatalf("marker moved to %q", id)
	}
	// A marker whose Assistant vanished falls to the next ordinary one.
	if _, err := s.db.Exec(`UPDATE assistants SET status='archived' WHERE id=?`, ada.ID); err != nil {
		t.Fatal(err)
	}
	if s.PrimaryAssistantID() != "" {
		t.Fatal("an archived Assistant is not a Primary")
	}
	if id, _ := s.EnsurePrimary(); id == "" || id == ada.ID {
		t.Fatalf("a new Primary is chosen: %q", id)
	}
}

func manage(t *testing.T, api *API, who, sub, body string) (map[string]any, error) {
	t.Helper()
	return call(t, api, body, "assistant", "--as", who, "manage", sub)
}

func TestOnlyThePrimaryMayManageAssistants(t *testing.T) {
	s, _ := openTest(t)
	api := &API{Store: s}
	ada := mustAssistant(t, s, "a-ada-0001", "Ada")
	bob := mustAssistant(t, s, "a-bob-0001", "Bob")
	for _, sub := range ManageActions() {
		sub = sub[len("manage "):]
		if _, err := manage(t, api, bob.ID, sub, fmt.Sprintf(`{"requestId":"r-12345678","id":%q}`, ada.ID)); codeOf(err) != "forbidden" {
			t.Errorf("manage %s as a non-primary: code %q, want forbidden", sub, codeOf(err))
		}
	}
}

func TestPrimaryManagesAssistantsThroughTheBroker(t *testing.T) {
	s, _ := openTest(t)
	api := &API{Store: s}
	sys, _ := s.AutomationsAssistant()
	ada := mustAssistant(t, s, "a-ada-0001", "Ada")

	// create → listed with a coordinator state, never the system assistant.
	out, err := manage(t, api, ada.ID, "create", `{"requestId":"m-create-01","name":"Travel","description":"plans trips"}`)
	if err != nil {
		t.Fatal(err)
	}
	travel := field(out, "assistant", "id").(string)
	if field(out, "assistant", "primary") != nil {
		t.Fatal("a created Assistant is not the Primary")
	}
	if again, _ := manage(t, api, ada.ID, "create", `{"requestId":"m-create-01","name":"Travel","description":"plans trips"}`); again["replayed"] != true {
		t.Fatalf("a repeat of the requestId replays, never duplicates: %v", again)
	}
	listed, err := manage(t, api, ada.ID, "list", "")
	if err != nil {
		t.Fatal(err)
	}
	rows := listed["assistants"].([]any)
	if len(rows) != 2 {
		t.Fatalf("Ada + Travel, no system assistant: %v", rows)
	}
	for _, r := range rows {
		if r.(map[string]any)["id"] == sys.ID {
			t.Fatal("the system assistant is not manageable")
		}
	}
	if rows[0].(map[string]any)["primary"] != true || rows[1].(map[string]any)["coordinator"] != CoordinatorNone {
		t.Fatalf("Primary first, coordinator state present: %v", rows)
	}

	// rename
	if out, err = manage(t, api, ada.ID, "rename", fmt.Sprintf(`{"requestId":"m-rename-01","id":%q,"name":"Trips"}`, travel)); err != nil || field(out, "assistant", "name") != "Trips" {
		t.Fatalf("rename: %v %v", out, err)
	}

	// delegate: an ordinary task owned by the target; coding stays a draft.
	del, err := manage(t, api, ada.ID, "delegate", fmt.Sprintf(`{"requestId":"m-deleg-001","id":%q,"kind":"research","title":"Find flights","brief":"Find three options"}`, travel))
	if err != nil {
		t.Fatal(err)
	}
	if field(del, "task", "assistantId") != travel {
		t.Fatalf("the task belongs to the target Assistant: %v", del)
	}
	if _, err := manage(t, api, ada.ID, "delegate", fmt.Sprintf(`{"requestId":"m-deleg-001","id":%q,"kind":"research","title":"Find flights","brief":"Find three options"}`, travel)); err != nil {
		t.Fatalf("replaying the same requestId passes: %v", err)
	}
	code, err := manage(t, api, ada.ID, "delegate", fmt.Sprintf(`{"requestId":"m-deleg-002","id":%q,"kind":"coding","title":"Build","brief":"Build it","repoPath":"%s"}`, travel, t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	if field(code, "task", "state") != TaskDraft {
		t.Fatalf("a delegated coding task is a draft a person publishes: %v", field(code, "task", "state"))
	}
	if _, err := manage(t, api, ada.ID, "delegate", fmt.Sprintf(`{"requestId":"m-deleg-003","id":%q,"kind":"research","title":"Find flights","brief":"again"}`, travel)); codeOf(err) != "duplicate_task" {
		t.Fatalf("an unfinished duplicate is refused: %q", codeOf(err))
	}
	tasks, err := manage(t, api, ada.ID, "tasks", fmt.Sprintf(`{"id":%q}`, travel))
	if err != nil || len(tasks["tasks"].([]any)) != 2 {
		t.Fatalf("the Primary reads the target's tasks: %v %v", tasks, err)
	}

	// the Primary itself and the system assistant are protected
	if _, err := manage(t, api, ada.ID, "archive", fmt.Sprintf(`{"requestId":"m-arch-self","id":%q}`, ada.ID)); codeOf(err) != "bad_state" {
		t.Fatalf("the Primary cannot archive itself: %q", codeOf(err))
	}
	for sub, body := range map[string]string{
		"rename":   `{"requestId":"m-sys-0001","id":%q,"name":"x"}`,
		"archive":  `{"requestId":"m-sys-0002","id":%q}`,
		"delegate": `{"requestId":"m-sys-0003","id":%q,"kind":"research","title":"t","brief":"b"}`,
		"tasks":    `{"id":%q}`,
	} {
		if _, err := manage(t, api, ada.ID, sub, fmt.Sprintf(body, sys.ID)); codeOf(err) != "forbidden" {
			t.Errorf("manage %s on the system assistant: %q", sub, codeOf(err))
		}
	}

	// archive refuses while the target has unfinished work, then works once settled.
	if _, err := manage(t, api, ada.ID, "archive", fmt.Sprintf(`{"requestId":"m-arch-0001","id":%q}`, travel)); codeOf(err) != "bad_state" {
		t.Fatalf("unfinished tasks block the archive: %q", codeOf(err))
	}
	quiet, _ := manage(t, api, ada.ID, "create", `{"requestId":"m-create-02","name":"Quiet"}`)
	qid := field(quiet, "assistant", "id").(string)
	if out, err = manage(t, api, ada.ID, "archive", fmt.Sprintf(`{"requestId":"m-arch-0002","id":%q}`, qid)); err != nil || field(out, "assistant", "status") != StatusArchived {
		t.Fatalf("archive: %v %v", out, err)
	}
	if _, err := manage(t, api, ada.ID, "delegate", fmt.Sprintf(`{"requestId":"m-deleg-004","id":%q,"kind":"research","title":"x","brief":"y"}`, qid)); codeOf(err) != "forbidden" {
		t.Fatalf("an archived Assistant takes no work: %q", codeOf(err))
	}
	if out, err = manage(t, api, ada.ID, "restore", fmt.Sprintf(`{"requestId":"m-rest-0001","id":%q}`, qid)); err != nil || field(out, "assistant", "status") != StatusActive {
		t.Fatalf("restore: %v %v", out, err)
	}

	// a paused Assistant is not handed new work
	cur, _ := s.GetAssistant(travel)
	if _, _, err := s.SetAssistantStatus("pause-travel", travel, StatusDisabled, cur.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := manage(t, api, ada.ID, "delegate", fmt.Sprintf(`{"requestId":"m-deleg-005","id":%q,"kind":"research","title":"later","brief":"later"}`, travel)); codeOf(err) != "bad_state" {
		t.Fatalf("a paused Assistant takes no work: %q", codeOf(err))
	}
}

func TestPrimaryStartsAGroupThroughTheBroker(t *testing.T) {
	s, _ := openTest(t)
	api := &API{Store: s}
	sys, _ := s.AutomationsAssistant()
	ada := mustAssistant(t, s, "a-ada-0001", "Ada")
	bob := mustAssistant(t, s, "a-bob-0001", "Bob")
	body := fmt.Sprintf(`{"requestId":"m-group-01","title":"Trip","members":[%q,%q]}`, ada.ID, bob.ID)
	out, err := manage(t, api, ada.ID, "group", body)
	if err != nil || field(out, "group", "title") != "Trip" {
		t.Fatalf("the Primary opens a group: %v %v", out, err)
	}
	if again, _ := manage(t, api, ada.ID, "group", body); again["replayed"] != true {
		t.Fatalf("a retried group is replayed, not duplicated: %v", again)
	}
	if groups, _ := s.ListGroups(); len(groups) != 1 {
		t.Fatalf("exactly one group exists: %d", len(groups))
	}
	if _, err := manage(t, api, ada.ID, "group", fmt.Sprintf(`{"requestId":"m-group-02","members":[%q,%q]}`, ada.ID, sys.ID)); codeOf(err) != "forbidden" {
		t.Fatalf("the system assistant cannot be put in a group: %q", codeOf(err))
	}
	if _, err := manage(t, api, ada.ID, "group", fmt.Sprintf(`{"requestId":"m-group-03","members":[%q]}`, ada.ID)); codeOf(err) != "invalid" {
		t.Fatalf("a group needs two Assistants: %q", codeOf(err))
	}
	if _, err := manage(t, api, ada.ID, "group", fmt.Sprintf(`{"members":[%q,%q]}`, ada.ID, bob.ID)); codeOf(err) != "invalid" {
		t.Fatalf("a requestId is required: %q", codeOf(err))
	}
}
