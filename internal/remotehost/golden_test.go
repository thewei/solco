package remotehost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/thewei/solco/internal/agentcatalog"
	"github.com/thewei/solco/internal/chatsession"
	"github.com/thewei/solco/internal/feedback"
	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/history"
)

// [INPUT]: server_test.go 的假 Backend 与 harness，真实的 handler / WebSocket 帧
// [OUTPUT]: testdata/api-v3/*.json|txt 黄金文件（含 SC-06 Chat t=chat + seq 初始帧、chat_delta 后续补丁）：手机端 Zod 契约逐字同步解析
// [POS]: /api/v3 线格式的 Go 半边钉死：handler 输出与黄金文件逐字一致，漂移 = 两端同批修复；`go test ./internal/remotehost -run Golden -update` 重生成
// [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md

var updateGolden = flag.Bool("update", false, "rewrite testdata/api-v3 from the live handlers")

const goldenDir = "testdata/api-v3"

var (
	goldenDeviceID = regexp.MustCompile(`dev_[0-9a-f]{16}`)
	goldenToken    = regexp.MustCompile(`"token": ?"[^"]+"`)
	goldenCode     = regexp.MustCompile(`c=[A-Z2-7]{26}`)
)

// golden compares one wire payload (JSON bytes) with its file, canonically
// indented so the file reads well and diffs cleanly.
func golden(t *testing.T, name string, raw []byte) {
	t.Helper()
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, bytes.TrimSpace(raw), "", "  "); err != nil {
		t.Fatalf("%s: not JSON: %v\n%s", name, err, raw)
	}
	pretty.WriteByte('\n')
	goldenText(t, name, pretty.Bytes())
}

func goldenText(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join(goldenDir, name)
	if *updateGolden {
		if err := os.MkdirAll(goldenDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s missing (run with -update): %v", path, err)
	}
	if !bytes.Equal(want, got) {
		t.Errorf("%s drifted from the live handler (run with -update, then sync solco-mobile):\n--- want\n%s--- got\n%s", name, want, got)
	}
}

func readRaw(t *testing.T, conn *websocket.Conn) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	typ, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if typ != websocket.MessageText {
		t.Fatalf("want a text frame, got %v", typ)
	}
	return data
}

func bodyOf(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestGoldenAPIv3(t *testing.T) {
	h := newHarness(t)
	h.back.screen = "ls\nfoo bar\n│       │"

	// A listening Host, as pairing needs one (the harness serves the handler directly).
	const fp = "a1cc1d2af9c5a03eefb30bfcc11c5bae5554e2cee2ff007c8cde90e0a096ad57"
	h.srv.mu.Lock()
	h.srv.fingerprint, h.srv.listenHost, h.srv.listenPort = fp, "192.168.1.20", "7788"
	h.srv.mu.Unlock()
	h.srv.opts.HostName = "Test Mac"
	// the desktop's resolved title and last-worked time for the agent pane (the shell pane has none)
	h.srv.opts.AgentFacts = func(string) map[string]AgentFact {
		return map[string]AgentFact{"term_a": {Title: "Fix the parser", LastActive: time.UnixMilli(1785546001000), Background: []TreeBackground{
			{Kind: "subagent", ID: "agent-a1", Title: "Explore", Detail: "Find the lexer entry points", State: "working", UpdatedMs: 1785546000000},
			{Kind: "service", ID: "svc:w1:p1", Title: "node", Detail: ":3000, :5173", State: "running"},
		}}}
	}
	// the moment this Host first saw the agent terminal (agent.firstSeenMs) is pinned: the real one differs every run
	realNow := h.srv.opts.Now
	h.srv.opts.Now = func() time.Time { return time.UnixMilli(1785546002000) }
	if _, err := h.srv.currentTree("default"); err != nil {
		t.Fatal(err)
	}
	h.srv.opts.Now = realNow

	// pairing: the offer URL and the POST /pair answer
	offer, err := h.srv.StartPairing()
	if err != nil {
		t.Fatal(err)
	}
	goldenText(t, "pair-url.txt", []byte(goldenCode.ReplaceAllString(offer.URL, "c=AAAAAAAAAAAAAAAAAAAAAAAAAA")+"\n"))
	resp, err := http.Post(h.http.URL+"/api/v3/pair", "application/json",
		strings.NewReader(`{"code":"`+offer.Code+`","deviceName":"Test Phone"}`))
	if err != nil {
		t.Fatal(err)
	}
	pair := bodyOf(t, resp)
	pair = goldenToken.ReplaceAll(goldenDeviceID.ReplaceAll(pair, []byte("dev_0000000000000000")), []byte(`"token":"tok_0000000000000000000000000000000000000000000"`))
	golden(t, "pair.json", pair)

	// plain HTTP
	for name, path := range map[string]string{
		"bootstrap.json": "/api/v3/bootstrap",
		"tree.json":      "/api/v3/tree",
		"peek.json":      "/api/v3/panes/w1:p1/peek",
	} {
		golden(t, name, bodyOf(t, h.get(path, h.token, nil)))
	}
	golden(t, "error-unauthorized.json", bodyOf(t, h.get("/api/v3/tree", "", nil)))
	// net: the address the Mac serves right now, so a phone's book follows a changed
	// LAN/Tailscale address (bootstrap.json above is loopback — it carries none)
	h.srv.opts.Config = Config{Mode: ModeLAN, Port: 7788, Address: "192.168.1.10"}
	h.srv.mu.Lock()
	h.srv.listenHost, h.srv.listenPort = "192.168.1.10", "7788"
	h.srv.mu.Unlock()
	golden(t, "bootstrap-net.json", bodyOf(t, h.get("/api/v3/bootstrap", h.token, nil)))
	h.srv.opts.Config = Config{Mode: ModeLoopback, Port: 7788}
	h.srv.mu.Lock()
	h.srv.listenHost, h.srv.listenPort = "", ""
	h.srv.mu.Unlock()
	golden(t, "error-unknown-instance.json", bodyOf(t, h.get("/api/v3/tree?instance=ghost", h.token, nil)))

	// events: hello, then tree.changed after a visible change
	events, _, err := h.dial("/api/v3/events", true)
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "events-hello.json", readRaw(t, events))
	src := h.back.source(0)
	if src == nil {
		t.Fatal("no event subscription")
	}
	proj := fixtureProjection()
	proj.Panes[0].AgentStatus = "blocked"
	h.back.set(proj)
	src.events <- herdr.RuntimeEvent{Event: "pane.updated"}
	golden(t, "events-tree-changed.json", readRaw(t, events))
	// automation: one step of a plugin automation, worded by the phone (no prompt, no error text)
	h.srv.NotifyAutomation("failed", "nightly-ci-triage", "run-7")
	golden(t, "events-automation.json", readRaw(t, events))
	// feedback: an Agent's question reached the broker; the frame holds ids, the waiting count and the notification's words
	h.srv.NotifyFeedback(FeedbackNotice{Kind: "ask", ThreadID: "t_1", AskID: "a_1", Waiting: 1, Project: "solco", Client: "Claude Code", Preview: "Which database should the service use?"})
	golden(t, "events-feedback.json", readRaw(t, events))

	// terminal: frame header, ping, bad input, refused input
	term, _, err := h.dial("/api/v3/terminals/term_b/stream?cols=51&rows=19", true)
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "terminal-frame-header.json", readRaw(t, term))
	readBinary(t, term)
	writeClient(t, term, map[string]any{"t": "ping"})
	golden(t, "terminal-pong.json", readRaw(t, term))
	writeClient(t, term, map[string]any{"t": "input", "keys": []string{"enter; rm -rf"}})
	golden(t, "terminal-input-error-bad.json", readRaw(t, term))
	h.back.mu.Lock()
	h.back.inputErr = errors.New("agent is busy")
	h.back.mu.Unlock()
	writeClient(t, term, map[string]any{"t": "input", "text": "ls", "keys": []string{"enter"}})
	golden(t, "terminal-input-error-refused.json", readRaw(t, term))
	// terminal pane grid: the Mac pane's cell size, sent before the first frame once Herdr has a layout
	h.back.set(layoutProjection(120, 40))
	paneTerm, _, err := h.dial("/api/v3/terminals/term_b/stream?cols=51&rows=19", true)
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "terminal-pane.json", readRaw(t, paneTerm))

	// chat (P2, read-only): the shared chatsession projection of a Claude
	// transcript, as a one-shot read and as a stream frame
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(claudeFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	chatProj := fixtureProjection()
	chatProj.Agents = []herdr.Agent{{
		TerminalID: "term_a", PaneID: "w1:p1", Kind: "claude", Status: "working",
		AgentSession: &herdr.AgentSessionIdentity{Agent: "claude", Kind: "path", Value: path},
	}}
	h.back.set(chatProj)
	golden(t, "chat.json", bodyOf(t, h.get("/api/v3/panes/w1:p1/chat", h.token, nil)))
	golden(t, "error-no-conversation.json", bodyOf(t, h.get("/api/v3/panes/w1:p2/chat", h.token, nil)))
	chat, _, err := h.dial("/api/v3/panes/w1:p1/chat/stream", true)
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "chat-frame.json", readRaw(t, chat))
	patch, _ := json.Marshal(chatWireDelta{
		T: "chat_delta", PaneID: "w1:p1", BaseSeq: 1, Seq: 2, Start: 1,
		Turns: []chatsession.ChatTurn{{Narration: "Hello live stream!"}},
	})
	golden(t, "chat-delta.json", patch)

	// chat send/stop (P3): the request body, the ack and a named refusal
	rawReq, _ := json.Marshal(chatSendRequest{Text: "run the tests", Interrupt: true})
	golden(t, "chat-client-send.json", rawReq)
	_, _ = h.post("/api/v3/panes/w1:p1/chat/stop", h.token, "")
	req, _ := http.NewRequest("POST", h.http.URL+"/api/v3/panes/w1:p1/chat/send", strings.NewReader(`{"text":"x"}`))
	req.Header.Set("Authorization", "Bearer "+h.token)
	busy, err := http.DefaultClient.Do(req) // the fixture agent is working → busy
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "error-busy.json", bodyOf(t, busy))
	chatProj.Panes[0].AgentStatus = "idle"
	h.back.set(chatProj)
	req, _ = http.NewRequest("POST", h.http.URL+"/api/v3/panes/w1:p1/chat/send", strings.NewReader(`{"text":"x"}`))
	req.Header.Set("Authorization", "Bearer "+h.token)
	sent, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "chat-send-ok.json", bodyOf(t, sent))

	// chat answer (P3): the card a blocked agent shows (screen prompt), the
	// answer body and the named refusal for a stale card
	chatProj.Panes[0].AgentStatus = "blocked"
	h.back.set(chatProj)
	h.back.mu.Lock()
	h.back.screen = permissionScreen
	h.back.mu.Unlock()
	blocked, _, err := h.dial("/api/v3/panes/w1:p1/chat/stream", true)
	if err != nil {
		t.Fatal(err)
	}
	rawCard := readRaw(t, blocked)
	golden(t, "chat-frame-interaction.json", rawCard)
	var cardFrame struct {
		Interaction struct{ ID string } `json:"interaction"`
	}
	if err := json.Unmarshal(rawCard, &cardFrame); err != nil || cardFrame.Interaction.ID == "" {
		t.Fatalf("no card in the blocked frame: %v %s", err, rawCard)
	}
	rawAnswer, _ := json.Marshal(chatAnswerRequest{InteractionID: cardFrame.Interaction.ID, Option: "No"})
	golden(t, "chat-client-answer.json", rawAnswer)
	req, _ = http.NewRequest("POST", h.http.URL+"/api/v3/panes/w1:p1/chat/answer", strings.NewReader(`{"interactionId":"stale","option":"No"}`))
	req.Header.Set("Authorization", "Bearer "+h.token)
	gone, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "error-interaction-gone.json", bodyOf(t, gone))

	// launch (P4): a Host with a launcher advertises it in bootstrap; the
	// request body, the answer and a named refusal
	h.srv.opts.Launcher = &fakeLauncher{pane: "w1:p2"}
	golden(t, "bootstrap-launch.json", bodyOf(t, h.get("/api/v3/bootstrap", h.token, nil)))
	rawLaunch, _ := json.Marshal(launchRequest{RequestID: "req_0123456789", WorkspaceID: "w1", Kind: "claude-code", Prompt: "fix the failing test"})
	golden(t, "launch-client-request.json", rawLaunch)
	golden(t, "launch-ok.json", bodyOf(t, h.launch("/api/v3/launch", h.token, string(rawLaunch))))
	golden(t, "error-launch-bad-request.json", bodyOf(t, h.launch("/api/v3/launch", h.token, `{"requestId":"x","kind":"terminal"}`)))
	h.srv.opts.Launcher = &fakeLauncher{pane: "w1:p2", pending: true}
	golden(t, "launch-prompt-pending.json", bodyOf(t, h.launch("/api/v3/launch", h.token, string(rawLaunch))))
	// devices: saved machines ride in bootstrap.instances with kind/label/status; connect answers the instance
	h.srv.opts.Machines = sampleMachines()
	golden(t, "bootstrap-devices.json", bodyOf(t, h.get("/api/v3/bootstrap", h.token, nil)))
	golden(t, "device-connect.json", bodyOf(t, h.launch("/api/v3/devices/def/connect", h.token, ``)))
	h.srv.opts.Machines.(*fakeMachines).err = &MachineError{Code: "device_needs_signin", Status: http.StatusConflict}
	golden(t, "error-device-needs-signin.json", bodyOf(t, h.launch("/api/v3/devices/def/connect", h.token, ``)))
	h.srv.opts.Machines = nil
	h.srv.opts.Git = sampleGit()
	golden(t, "bootstrap-git.json", bodyOf(t, h.get("/api/v3/bootstrap", h.token, nil)))
	golden(t, "git-status.json", bodyOf(t, h.get("/api/v3/workspaces/w1/git", h.token, nil)))
	golden(t, "git-diff.json", bodyOf(t, h.get("/api/v3/workspaces/w1/git/diff?path=a.go", h.token, nil)))
	golden(t, "error-git-unknown-file.json", bodyOf(t, h.get("/api/v3/workspaces/w1/git/diff?path=gone.go", h.token, nil)))
	// git write: stage/unstage/commit/fetch/pull/push answer the fresh status; refusals are stable codes
	gw := &fakeGitWriter{}
	h.srv.opts.GitWrite = gw
	golden(t, "bootstrap-git-write.json", bodyOf(t, h.get("/api/v3/bootstrap", h.token, nil)))
	golden(t, "git-paths-client-request.json", []byte(`{"paths":["a.go"]}`))
	golden(t, "git-commit-client-request.json", []byte(`{"subject":"Fix the failing test","body":"The nil check was inverted.","paths":["a.go"],"head":"abc1234"}`))
	golden(t, "git-stage-ok.json", bodyOf(t, h.launch("/api/v3/workspaces/w1/git/stage", h.token, `{"paths":["a.go"]}`)))
	golden(t, "git-commit-ok.json", bodyOf(t, h.launch("/api/v3/workspaces/w1/git/commit", h.token, `{"subject":"Fix the failing test","body":"The nil check was inverted.","paths":["a.go"],"head":"abc1234"}`)))
	golden(t, "git-push-ok.json", bodyOf(t, h.launch("/api/v3/workspaces/w1/git/push", h.token, ``)))
	gw.err = &GitWriteError{Code: "git_rejected", Status: http.StatusConflict}
	golden(t, "error-git-rejected.json", bodyOf(t, h.launch("/api/v3/workspaces/w1/git/pull", h.token, ``)))
	h.srv.opts.GitWrite = nil
	// files (read-only): one Workspace folder browsed a level at a time; a path outside it is refused
	filesRoot := t.TempDir()
	for rel, body := range map[string]string{"README.md": "# Solco\n", "src/main.go": "package main\n", "assets/blob.bin": "a\x00b"} {
		if err := os.MkdirAll(filepath.Join(filesRoot, filepath.Dir(rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(filesRoot, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	h.back.mu.Lock()
	savedCWD := h.back.proj.Projects[0].CWD
	h.back.proj.Projects[0].CWD = filesRoot
	h.back.mu.Unlock()
	h.srv.opts.Files = true
	golden(t, "bootstrap-files.json", bodyOf(t, h.get("/api/v3/bootstrap", h.token, nil)))
	golden(t, "files.json", bodyOf(t, h.get("/api/v3/workspaces/w1/files", h.token, nil)))
	golden(t, "files-dir.json", bodyOf(t, h.get("/api/v3/workspaces/w1/files?dir=src", h.token, nil)))
	golden(t, "file-content.json", bodyOf(t, h.get("/api/v3/workspaces/w1/files/content?path=README.md", h.token, nil)))
	golden(t, "file-content-binary.json", bodyOf(t, h.get("/api/v3/workspaces/w1/files/content?path=assets/blob.bin", h.token, nil)))
	golden(t, "error-files-bad-request.json", bodyOf(t, h.get("/api/v3/workspaces/w1/files?dir=..", h.token, nil)))
	h.srv.opts.Files = false
	h.back.mu.Lock()
	h.back.proj.Projects[0].CWD = savedCWD
	h.back.mu.Unlock()
	// quota (read-only): the desktop's provider quotas behind a capability;
	// the seam answers the whole page, the handler only forwards
	h.srv.opts.Usage = func(context.Context, bool) Quota { return sampleQuota() }
	golden(t, "bootstrap-quota.json", bodyOf(t, h.get("/api/v3/bootstrap", h.token, nil)))
	golden(t, "quota.json", bodyOf(t, h.get("/api/v3/quota", h.token, nil)))
	h.srv.opts.Usage = nil
	golden(t, "error-quota-unavailable.json", bodyOf(t, h.get("/api/v3/quota", h.token, nil)))
	// feedback: the Agent-question inbox; the seam is the broker's client, the handler forwards and strips Mac paths
	fb := sampleFeedback()
	h.srv.opts.Feedback = fb
	golden(t, "bootstrap-feedback.json", bodyOf(t, h.get("/api/v3/bootstrap", h.token, nil)))
	golden(t, "feedback.json", bodyOf(t, h.get("/api/v3/feedback", h.token, nil)))
	golden(t, "feedback-thread.json", bodyOf(t, h.get("/api/v3/feedback?thread=t_1", h.token, nil)))
	golden(t, "feedback-answer-request.json", []byte(`{"answers":{"q1":{"selected":["SQLite"],"custom":"with WAL"},"q2":{"selected":[],"custom":"ship it"}}}`))
	_, fbAck := h.send("POST", "/api/v3/feedback/asks/a_1/answer", h.token, `{"answers":{"q1":{"selected":["SQLite"]}}}`)
	golden(t, "feedback-ack.json", fbAck)
	golden(t, "feedback-delete-request.json", []byte(`{"ids":["t_1","t_2"]}`))
	_, fbDeleted := h.send("POST", "/api/v3/feedback/threads/delete", h.token, `{"ids":["t_1","t_2"]}`)
	golden(t, "feedback-delete-ack.json", fbDeleted)
	golden(t, "feedback-cleanup-request.json", []byte(`{"olderThanDays":30,"dryRun":true}`))
	_, fbCleanup := h.send("POST", "/api/v3/feedback/threads/cleanup", h.token, `{"olderThanDays":30,"dryRun":true}`)
	golden(t, "feedback-cleanup-ack.json", fbCleanup)
	golden(t, "feedback-retention-request.json", []byte(`{"days":90}`))
	fb.err = feedback.ErrFinal
	_, fbFinal := h.send("POST", "/api/v3/feedback/asks/a_0/end", h.token, "")
	golden(t, "error-feedback-final.json", fbFinal)
	fb.err, fb.offline = nil, true
	golden(t, "feedback-offline.json", bodyOf(t, h.get("/api/v3/feedback", h.token, nil)))
	h.srv.opts.Feedback = nil
	golden(t, "error-feedback-unavailable.json", bodyOf(t, h.get("/api/v3/feedback", h.token, nil)))
	// automations (read-only): the desktop's Automations page behind a capability that
	// follows the plugin gate; the seam answers the whole list, the handler only forwards
	h.srv.opts.Automations = func(context.Context) (Automations, bool) { return sampleAutomations(), true }
	golden(t, "bootstrap-automations.json", bodyOf(t, h.get("/api/v3/bootstrap", h.token, nil)))
	golden(t, "automations.json", bodyOf(t, h.get("/api/v3/automations", h.token, nil)))
	// management: detail (prompt, rationale, runs without error text), the write request the
	// phone sends, and a refusal that carries the plugin's own words
	fa := &fakeAdmin{open: true}
	h.srv.opts.AutomationsAdmin = fa
	golden(t, "bootstrap-automations-manage.json", bodyOf(t, h.get("/api/v3/bootstrap", h.token, nil)))
	golden(t, "automation-detail.json", bodyOf(t, h.get("/api/v3/automations/nightly-ci-triage", h.token, nil)))
	writeReq, _ := json.Marshal(automationWrite{Name: "weekly-recap", AutomationFields: sampleAutomationDetail().AutomationFields})
	golden(t, "automation-write-request.json", writeReq)
	fa.fail = &AutomationError{Code: "automation_refused", Status: http.StatusConflict, Detail: "cron: expected 5 fields"}
	_, refusal := h.send("POST", "/api/v3/automations/dependency-sweep/approve", h.token, "")
	golden(t, "error-automation-refused.json", refusal)
	h.srv.opts.AutomationsAdmin = nil
	h.srv.opts.Automations = nil
	golden(t, "error-automations-unavailable.json", bodyOf(t, h.get("/api/v3/automations", h.token, nil)))
	// terminal history (view): the pane's retained text beyond the observe stream's
	// current screen; only a Backend that can read it advertises the capability
	plainBackend := h.srv.opts.Backend
	hist := &historyBackend{fakeBackend: h.back, history: PaneHistory{Text: "$ go test ./...\nok  \tgithub.com/thewei/solco\t0.4s\n完成 ✓\n", Rows: 1042, Revision: 86}}
	h.srv.opts.Backend = hist
	golden(t, "bootstrap-terminal-history.json", bodyOf(t, h.get("/api/v3/bootstrap", h.token, nil)))
	golden(t, "terminal-history.json", bodyOf(t, h.get("/api/v3/terminals/term_a/history", h.token, nil)))
	for i := 0; i < historyAttempts; i++ {
		hist.errs = append(hist.errs, errors.New("Herdr pane content is changing"))
	}
	golden(t, "error-terminal-history-busy.json", bodyOf(t, h.get("/api/v3/terminals/term_b/history", h.token, nil)))
	h.srv.opts.Backend = plainBackend
	// sessions (read-only): the desktop's catalog metadata with the running-pane
	// link; the seam answers search and running, the handler only forwards
	h.srv.opts.Sessions = func(_ context.Context, q SessionsQuery) (Sessions, error) { return sampleSessions(q), nil }
	golden(t, "bootstrap-sessions.json", bodyOf(t, h.get("/api/v3/bootstrap", h.token, nil)))
	golden(t, "sessions.json", bodyOf(t, h.get("/api/v3/sessions", h.token, nil)))
	golden(t, "sessions-search.json", bodyOf(t, h.get("/api/v3/sessions?q=tests&limit=5", h.token, nil)))
	golden(t, "error-sessions-bad-request.json", bodyOf(t, h.get("/api/v3/sessions?limit=0", h.token, nil)))
	// continue (manage): the same engine as the desktop's Continue behind a seam;
	// the answer names the pane the session now runs in
	h.srv.opts.Continuer = &fakeContinuer{result: ContinueResult{PaneID: "w1:p2"}}
	golden(t, "bootstrap-sessions-continue.json", bodyOf(t, h.get("/api/v3/bootstrap", h.token, nil)))
	golden(t, "session-continue.json", bodyOf(t, h.launch("/api/v3/sessions/codex:19bd/continue", h.token, "")))
	h.srv.opts.Continuer = &fakeContinuer{err: &LaunchError{Code: "needs_project", Status: http.StatusConflict, Detail: "This session has no project folder."}}
	golden(t, "error-session-needs-project.json", bodyOf(t, h.launch("/api/v3/sessions/codex:19bd/continue", h.token, "")))
	h.srv.opts.Continuer = nil
	golden(t, "error-session-continue-unavailable.json", bodyOf(t, h.launch("/api/v3/sessions/codex:19bd/continue", h.token, "")))
	// trash (manage): the reversible two steps of the desktop's Sessions Trash; the
	// seam moves files, the handler only forwards (permanent delete is desktop-only)
	h.srv.opts.Trasher = &fakeTrasher{entries: sampleTrash()}
	golden(t, "bootstrap-sessions-trash.json", bodyOf(t, h.get("/api/v3/bootstrap", h.token, nil)))
	golden(t, "session-trash.json", bodyOf(t, h.launch("/api/v3/sessions/codex:19bd/trash", h.token, "")))
	golden(t, "sessions-trash.json", bodyOf(t, h.get("/api/v3/sessions/trash", h.token, nil)))
	golden(t, "session-trash-restore.json", bodyOf(t, h.launch("/api/v3/sessions/trash/1785549600000-0a1b2c3d/restore", h.token, "")))
	h.srv.opts.Trasher = &fakeTrasher{err: &TrashError{Code: "session_in_use", Status: http.StatusConflict, Detail: "An Agent is still using this session."}}
	golden(t, "error-session-in-use.json", bodyOf(t, h.launch("/api/v3/sessions/codex:19bd/trash", h.token, "")))
	h.srv.opts.Trasher = &fakeTrasher{err: &TrashError{Code: "session_exists", Status: http.StatusConflict, Detail: "A session file already exists there."}}
	golden(t, "error-session-exists.json", bodyOf(t, h.launch("/api/v3/sessions/trash/1785549600000-0a1b2c3d/restore", h.token, "")))
	h.srv.opts.Trasher = nil
	// voice (terminal-input): the phone's clip goes to the desktop's own Voice source and the text comes back
	h.srv.opts.Voice = &fakeVoice{ready: true, text: "fix the failing test"}
	golden(t, "bootstrap-voice.json", bodyOf(t, h.get("/api/v3/bootstrap", h.token, nil)))
	golden(t, "voice-client-request.json", []byte(voiceBody([]byte("webm-bytes"), "audio/webm")))
	golden(t, "voice-transcribe.json", bodyOf(t, h.launch("/api/v3/voice/transcribe", h.token, voiceBody([]byte("webm-bytes"), "audio/webm"))))
	golden(t, "error-voice-bad-request.json", bodyOf(t, h.launch("/api/v3/voice/transcribe", h.token, "nope")))
	h.srv.opts.Voice = &fakeVoice{ready: true, err: errors.New("transcription auth failed — check the voice API key")}
	golden(t, "error-voice-transcribe-failed.json", bodyOf(t, h.launch("/api/v3/voice/transcribe", h.token, voiceBody([]byte("x"), "audio/webm"))))
	h.srv.opts.Voice = nil
	golden(t, "error-voice-unavailable.json", bodyOf(t, h.launch("/api/v3/voice/transcribe", h.token, voiceBody([]byte("x"), "audio/webm"))))
	h.srv.opts.Sessions = nil
	golden(t, "error-sessions-unavailable.json", bodyOf(t, h.get("/api/v3/sessions", h.token, nil)))
	h.srv.opts.Catalog = func(context.Context, history.AgentID, string) agentcatalog.Catalog {
		return agentcatalog.Catalog{
			Commands:     []agentcatalog.Entry{{Name: "/compact", Insert: "/compact ", Detail: "Compact the conversation", Kind: agentcatalog.KindText}},
			Skills:       []agentcatalog.Entry{{Name: "/review", Insert: "/review ", Detail: "Review the diff", Kind: agentcatalog.KindSkill}},
			Models:       []agentcatalog.Model{{ID: "anthropic/sonnet", Efforts: []agentcatalog.Effort{{Label: "low", Send: "/effort low"}, {Label: "high", Send: "/effort high"}}}},
			ModelVerb:    "/model ",
			DefaultModel: "sonnet",
			ModelNote:    "Saved as the default model.",
			Efforts:      []agentcatalog.Effort{{Label: "medium", Send: "/effort medium"}},
			EffortNote:   "Saved as the model's default effort.",
		}
	}
	golden(t, "chat-catalog.json", bodyOf(t, h.get("/api/v3/panes/w1:p1/chat/catalog?instance=default", h.token, nil)))
	folder := t.TempDir()
	for _, name := range []string{"src/app.go", "README.md", ".env"} {
		_ = os.MkdirAll(filepath.Join(folder, filepath.Dir(name)), 0o755)
		_ = os.WriteFile(filepath.Join(folder, name), nil, 0o644)
	}
	h.back.mu.Lock()
	for i := range h.back.proj.Panes {
		if h.back.proj.Panes[i].ID == "w1:p1" {
			h.back.proj.Panes[i].CWD = folder
		}
	}
	h.back.mu.Unlock()
	golden(t, "chat-files.json", bodyOf(t, h.get("/api/v3/panes/w1:p1/chat/files?instance=default", h.token, nil)))
	h.srv.opts.Attach = func([]byte, string, string) (string, error) {
		return "/Users/me/Library/Caches/Solco/ChatAttachments/20261031-120000-a1b2c3.png", nil
	}
	golden(t, "bootstrap-attach.json", bodyOf(t, h.get("/api/v3/bootstrap", h.token, nil)))
	rawAttach := []byte(attachBody([]byte("png"), "image/png", "shot.png"))
	golden(t, "attach-client-request.json", rawAttach)
	golden(t, "attach-ok.json", bodyOf(t, h.launch("/api/v3/panes/w1:p1/chat/attachments?instance=default", h.token, string(rawAttach))))
	golden(t, "error-attach-bad-request.json", bodyOf(t, h.launch("/api/v3/panes/w1:p1/chat/attachments?instance=default", h.token, `{"dataBase64":"@@@"}`)))

	// sub-agents: the card in the chat state, the drawer's own transcript and the refusal for an unlisted id
	subSession, subDir := filepath.Join(t.TempDir(), "sub.jsonl"), ""
	subDir = filepath.Join(strings.TrimSuffix(subSession, ".jsonl"), "subagents")
	if err := os.MkdirAll(subDir, 0o700); err != nil {
		t.Fatal(err)
	}
	spawn := claudeFixture + `{"type":"assistant","cwd":"/work/solco","timestamp":"2026-08-01T01:00:02Z","message":{"id":"m2","content":[{"type":"tool_use","id":"call_1","name":"Agent","input":{"description":"Find the parser","subagent_type":"Explore","prompt":"find it"}}]}}` + "\n"
	files := map[string]string{
		subSession: spawn,
		filepath.Join(subDir, "agent-abc123.meta.json"): `{"agentType":"Explore","description":"Find the parser","toolUseId":"call_1"}`,
		filepath.Join(subDir, "agent-abc123.jsonl"):     subagentFixture,
	}
	stamp := time.Date(2026, 8, 1, 1, 0, 20, 0, time.UTC)
	for file, body := range files {
		if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(file, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	h.srv.opts.Now = func() time.Time { return stamp.Add(time.Hour) }
	subProj := fixtureProjection()
	subProj.Agents = []herdr.Agent{{
		TerminalID: "term_a", PaneID: "w1:p1", Kind: "claude", Status: "idle",
		AgentSession: &herdr.AgentSessionIdentity{Agent: "claude", Kind: "path", Value: subSession},
	}}
	h.back.set(subProj)
	golden(t, "chat-with-subagent.json", bodyOf(t, h.get("/api/v3/panes/w1:p1/chat", h.token, nil)))
	golden(t, "chat-subagent.json", bodyOf(t, h.get("/api/v3/panes/w1:p1/chat/subagents/abc123", h.token, nil)))
	golden(t, "error-unknown-subagent.json", bodyOf(t, h.get("/api/v3/panes/w1:p1/chat/subagents/nope", h.token, nil)))

	// follow-up queue: parked behind a running turn, shown in the chat state,
	// blocked behind an uncertain delivery, withdrawn by item and wholesale
	q := &fakeQueue{}
	h.srv.opts.Queue = q
	golden(t, "bootstrap-queue.json", bodyOf(t, h.get("/api/v3/bootstrap", h.token, nil)))
	subProj.Panes[0].AgentStatus = "working"
	h.back.set(subProj)
	rawQueue := []byte(`{"text":"then run the tests","queue":true}`)
	golden(t, "queue-client-request.json", rawQueue)
	golden(t, "queue-ok.json", bodyOf(t, h.launch("/api/v3/panes/w1:p1/chat/send?instance=default", h.token, string(rawQueue))))
	golden(t, "chat-queued.json", bodyOf(t, h.get("/api/v3/panes/w1:p1/chat", h.token, nil)))
	q.err = ErrFollowUpFrozen
	golden(t, "error-queue-blocked.json", bodyOf(t, h.launch("/api/v3/panes/w1:p1/chat/send?instance=default", h.token, string(rawQueue))))
	q.err = nil
	h.launch("/api/v3/panes/w1:p1/chat/send?instance=default", h.token, string(rawQueue)) // a second, live entry for the wholesale cancel
	golden(t, "queue-item-edit-client-request.json", []byte(`{"text":"then run the tests and lint"}`))
	golden(t, "queue-item-move-client-request.json", []byte(`{"to":0}`))
	golden(t, "queue-item-edit-ok.json", bodyOf(t, h.launch("/api/v3/panes/w1:p1/chat/queue/item/req-q1/edit?instance=default", h.token, `{"text":"then run the tests and lint"}`)))
	golden(t, "queue-item-move-ok.json", bodyOf(t, h.launch("/api/v3/panes/w1:p1/chat/queue/item/req-q2/move?instance=default", h.token, `{"to":0}`)))
	golden(t, "queue-item-cancel-ok.json", bodyOf(t, h.launch("/api/v3/panes/w1:p1/chat/queue/item/req-q1/cancel?instance=default", h.token, `{}`)))
	golden(t, "queue-cancel-ok.json", bodyOf(t, h.launch("/api/v3/panes/w1:p1/chat/queue/cancel?instance=default", h.token, `{}`)))
	golden(t, "error-queue-none.json", bodyOf(t, h.launch("/api/v3/panes/w1:p1/chat/queue/cancel?instance=default", h.token, `{}`)))

	// the upstream messages the phone sends (what the Host accepts)
	for name, msg := range map[string]map[string]any{
		"terminal-client-input.json":  {"t": "input", "text": "ls", "keys": []string{"enter"}},
		"terminal-client-resize.json": {"t": "resize", "cols": 60, "rows": 22},
		"terminal-client-ping.json":   {"t": "ping"},
		"terminal-client-scroll.json": {"t": "scroll", "lines": 3},                      // takeover only
		"terminal-client-tap.json":    {"t": "tap", "col": 12, "row": 4},                // takeover only
		"terminal-client-mouse.json":  {"t": "mouse", "a": "drag", "col": 12, "row": 4}, // takeover mouse mode only
	} {
		raw, _ := json.Marshal(msg)
		golden(t, name, raw)
	}
}
