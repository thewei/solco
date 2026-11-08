package nativeui

import (
	"context"
	"fmt"
	"github.com/thewei/solco/internal/chatsession"
	"github.com/thewei/solco/internal/conversation"
	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/history"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ompCoordinatorSession writes an OMP transcript whose working directory is the
// Coordinator's own folder (the one fact the preview keys on).
func ompCoordinatorSession(t *testing.T, root, assistantID, id, said string) {
	t.Helper()
	dir := filepath.Join(root, "-plugin-personal-coordinators-"+assistantID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"type":"session","version":3,"id":%q,"timestamp":"2026-11-06T10:00:00.000Z","cwd":"/plugin/personal/coordinators/%s"}
{"type":"message","timestamp":"2026-11-06T10:00:01.000Z","message":{"role":"user","content":[{"type":"text","text":"You are the Coordinator of the Solco Assistant Ada. Standing brief."}]}}
{"type":"message","timestamp":"2026-11-06T10:00:02.000Z","message":{"role":"user","content":[{"type":"text","text":"remember the launch plan"}]}}
{"type":"message","timestamp":"2026-11-06T10:00:03.000Z","message":{"role":"assistant","content":[{"type":"text","text":%q}]}}
`, id, assistantID, said)
	if err := os.WriteFile(filepath.Join(dir, "2026-11-06T10-00-00-000Z_"+id+".jsonl"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A Coordinator that is not running still shows what it said last, read-only,
// from its own folder's OMP session and nobody else's; the standing brief row
// stays out; and the live Coordinator takes the Chat over without a trace of it.
func TestPreviewCoordinatorShowsThePreviousConversationReadOnly(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".omp", "agent", "sessions")
	ompCoordinatorSession(t, root, "a_1", "omp-sess-mine", "Launch plan noted: ship on Friday.")
	ompCoordinatorSession(t, root, "a_2", "omp-sess-theirs", "Somebody else's secret answer.")

	stubAssistants(t, false)
	assistantsCoordinators["a_1"] = coordinatorJSON("a_1", "stopped", "")
	s := openAssistantsShell(t)
	s.router.Push(routeAssistants) // the page is on screen: the preview may bind
	s.hist.service = history.NewHistoryService(history.NewCatalog(), history.DefaultRoots(home))
	if _, err := s.hist.service.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	b := NewAssistantsBridge(s)
	s.hist.viewFrame = 40 // a window that has been drawing for a while

	got, err := b.PreviewCoordinator(context.Background(), "a_1")
	if err != nil || !got.Previewing {
		t.Fatalf("preview = %+v err=%v", got, err)
	}
	state := s.chatBridgeState()
	text := fmt.Sprintf("%+v", state.Turns)
	if !state.ReadOnly || !strings.Contains(text, "Launch plan noted") || strings.Contains(text, "secret answer") {
		t.Fatalf("the preview must be a_1's own conversation, read-only: ro=%v %s", state.ReadOnly, text)
	}
	for _, turn := range state.Turns {
		if turn.User && strings.Contains(turn.Text, "Standing brief") {
			t.Fatalf("the Coordinator's standing brief is not something the person said: %+v", turn)
		}
	}
	// The next frame must not drop it before the page builds (the real app did).
	s.hist.viewFrame++
	s.releaseIdleHistoryChat()
	if !s.chatReadOnly || !s.assistantPreviewActive() {
		t.Fatal("the next frame released the preview before the page could claim it")
	}
	// Asking again is a no-op, not a reload loop.
	if again, err := b.PreviewCoordinator(context.Background(), "a_1"); err != nil || !again.Previewing {
		t.Fatalf("second preview = %+v %v", again, err)
	}

	// An Assistant with no conversation of its own shows none (and drops the old one).
	assistantsCoordinators["a_3"] = coordinatorJSON("a_3", "none", "")
	if none, err := b.PreviewCoordinator(context.Background(), "a_3"); err != nil || none.Previewing || s.chatReadOnly {
		t.Fatalf("no conversation, no preview: %+v ro=%v %v", none, s.chatReadOnly, err)
	}

	// The live Coordinator replaces the preview.
	got, err = b.PreviewCoordinator(context.Background(), "a_1")
	if err != nil || !got.Previewing {
		t.Fatal(got, err)
	}
	assistantsCoordinators["a_1"] = coordinatorJSON("a_1", "ready", "p1")
	s.projection.Agents = nil
	s.callOnWindow(func() { s.releaseAssistantPreview() })
	if s.chatReadOnly || s.assistantPreviewActive() {
		t.Fatalf("the preview must be gone once released: ro=%v", s.chatReadOnly)
	}
}

// A Coordinator stopped in the same app run has a transcript the catalog has
// not seen yet (OMP's identity reaches Solco through Herdr, which starts no
// scan): the preview asks for one catalog round — throttled, never for an
// Assistant that has not run, and not once the conversation is found.
func TestPreviewCoordinatorAsksForACatalogRoundOnlyWhenItFindsNothing(t *testing.T) {
	home := t.TempDir()
	stubAssistants(t, false)
	assistantsCoordinators["a_1"] = coordinatorJSON("a_1", "stopped", "")
	assistantsCoordinators["a_9"] = `{"schema":1,"coordinator":{"assistantId":"a_9","state":"none","revision":1}}`
	s := openAssistantsShell(t)
	s.router.Push(routeAssistants)
	s.hist.service = history.NewHistoryService(history.NewCatalog(), history.DefaultRoots(home))
	b := NewAssistantsBridge(s)

	if _, err := b.PreviewCoordinator(context.Background(), "a_9"); err != nil || !s.assistants.previewScanAt.IsZero() {
		t.Fatalf("an Assistant that never ran must not trigger a scan: %v %v", s.assistants.previewScanAt, err)
	}
	if got, err := b.PreviewCoordinator(context.Background(), "a_1"); err != nil || got.Previewing {
		t.Fatalf("nothing in the catalog yet: %+v %v", got, err)
	}
	first := s.assistants.previewScanAt
	if first.IsZero() {
		t.Fatal("a Coordinator that ran but is missing from the catalog must ask for a scan")
	}
	if _, err := b.PreviewCoordinator(context.Background(), "a_1"); err != nil || !s.assistants.previewScanAt.Equal(first) {
		t.Fatalf("the page's next ask must not ask for another round inside the gap: %v %v", s.assistants.previewScanAt, err)
	}

	// Once the transcript is in the catalog the preview shows it and asks for nothing.
	ompCoordinatorSession(t, filepath.Join(home, ".omp", "agent", "sessions"), "a_1", "omp-sess-late", "Noted late.")
	if _, err := s.hist.service.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.assistants.previewScanAt = time.Time{}
	s.hist.viewFrame = 40
	if got, err := b.PreviewCoordinator(context.Background(), "a_1"); err != nil || !got.Previewing || !s.assistants.previewScanAt.IsZero() {
		t.Fatalf("found conversation: %+v scanAt=%v %v", got, s.assistants.previewScanAt, err)
	}
}

// The 2026-11-07 storm: the /assistants WebView stays alive hidden after the
// user leaves the route, and its preview poll re-bound the shared Chat away
// from the pane the user was chatting with — every cycle tore the live tail
// down (Loading conversation… cycling forever, the composer swapping stored
// drafts). A page that is not on screen must not move the shared Chat.
func TestPreviewCoordinatorOffRouteCannotHijackTheChat(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".omp", "agent", "sessions")
	ompCoordinatorSession(t, root, "a_1", "omp-sess-mine", "Launch plan noted: ship on Friday.")
	stubAssistants(t, false)
	assistantsCoordinators["a_1"] = coordinatorJSON("a_1", "stopped", "")
	s := openAssistantsShell(t)
	s.hist.service = history.NewHistoryService(history.NewCatalog(), history.DefaultRoots(home))
	if _, err := s.hist.service.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "user-session.jsonl")
	if err := os.WriteFile(path, []byte("{\"type\":\"session\",\"version\":3,\"id\":\"u1\",\"cwd\":\"/tmp\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	identity := herdr.AgentSessionIdentity{Agent: "omp", Kind: "path", Source: "herdr:omp", Value: path}
	s.selectedPaneID = "p-user"
	s.chatPaneID = "p-user"
	s.chatConversationID = "live:omp:term-u"
	s.chatLiveFingerprint = conversation.SessionIdentity{Provider: string(history.AgentOMP), Kind: identity.Kind, Source: identity.Source, Value: identity.Value}.Fingerprint()
	s.chatLiveProvider = history.AgentOMP
	s.chatLivePath = path
	s.chatTurns = []conversation.TimelineTurn{{UserRow: true, Text: "user row"}}
	s.projection = herdr.Projection{Agents: []herdr.Agent{{TerminalID: "term-u", PaneID: "p-user", Status: "idle", AgentSession: &identity}}}

	got, err := NewAssistantsBridge(s).PreviewCoordinator(context.Background(), "a_1")
	if err != nil || got.Previewing {
		t.Fatalf("an off-route preview must not claim the chat: %+v %v", got, err)
	}
	if s.chatPaneID != "p-user" || s.chatConversationID != "live:omp:term-u" || s.chatReadOnly {
		t.Fatalf("the preview hijacked the live chat: pane=%q conv=%q readOnly=%v", s.chatPaneID, s.chatConversationID, s.chatReadOnly)
	}
}

// The bind poller of a hidden conversation page must not move the Chat onto
// the Coordinator's pane either — same storm, the live side.
func TestBindCoordinatorOffRouteCannotMoveTheChat(t *testing.T) {
	stubAssistants(t, false)
	assistantsCoordinators["a_1"] = coordinatorJSON("a_1", "ready", "p1")
	s := openAssistantsShell(t)
	s.projection = herdr.Projection{Agents: []herdr.Agent{{TerminalID: "t1", PaneID: "p1", Status: "idle"}}}

	got, err := NewAssistantsBridge(s).BindCoordinator(context.Background(), "a_1")
	if err != nil || got.Bound {
		t.Fatalf("an off-route bind must not claim the chat: %+v %v", got, err)
	}
	if s.chatPaneID != "" || s.chatConversationID != "" {
		t.Fatalf("the bind moved the chat off-route: pane=%q conv=%q", s.chatPaneID, s.chatConversationID)
	}
}

// The phone's Assistant page finds the same OMP session the desktop preview
// binds (its own folder's, nobody else's), drops the standing brief, and asks
// for a catalog round only for a Coordinator that has run.
func TestHostCoordinatorHistoryFindsTheCoordinatorsOwnTranscript(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".omp", "agent", "sessions")
	ompCoordinatorSession(t, root, "a_1", "omp-sess-mine", "Launch plan noted.")
	ompCoordinatorSession(t, root, "a_2", "omp-sess-theirs", "Somebody else's answer.")
	stubAssistants(t, false)
	s := openAssistantsShell(t)
	s.hist.service = history.NewHistoryService(history.NewCatalog(), history.DefaultRoots(home))
	if _, err := s.hist.service.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	find := s.hostCoordinatorHistory(s.hist.service)

	got, ok := find(context.Background(), "a_1", true)
	if !ok || got.Provider != history.AgentOMP || !strings.Contains(got.Path, "omp-sess-mine") || strings.Contains(got.Path, "theirs") {
		t.Fatalf("transcript = %+v ok=%v", got, ok)
	}
	shown := hostFilterTurns([]chatsession.ChatTurn{
		{User: true, Text: "You are the Coordinator of the Solco Assistant Ada. Standing brief."},
		{Narration: "Noted."},
		{User: true, Text: "remember the launch plan"},
		{Narration: "Remembered."},
	})
	if len(shown) != 2 || !shown[0].User || shown[0].Text != "remember the launch plan" || shown[1].Narration != "Remembered." {
		t.Fatalf("the standing brief and its greeting, and only they, must be hidden: %+v", shown)
	}
	if !s.assistants.previewScanAt.IsZero() {
		t.Fatal("a found conversation must not ask for a scan")
	}

	if _, ok := find(context.Background(), "a_9", false); ok || !s.assistants.previewScanAt.IsZero() {
		t.Fatalf("an Assistant that never ran must not trigger a scan: ok=%v at=%v", ok, s.assistants.previewScanAt)
	}
	if _, ok := find(context.Background(), "a_9", true); ok || s.assistants.previewScanAt.IsZero() {
		t.Fatalf("a Coordinator that ran but is not in the catalog asks for one round: ok=%v at=%v", ok, s.assistants.previewScanAt)
	}
	if s.hostCoordinatorHistory(nil) != nil {
		t.Fatal("without a catalog the seam is absent, so bootstrap does not advertise it")
	}
}
