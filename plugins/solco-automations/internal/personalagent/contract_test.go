package personalagent

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite the golden fixtures in testdata/")

var idRe = regexp.MustCompile(`\b[adgtrcmpefh]_[0-9a-f]{16}\b`)
var hookKeyRe = regexp.MustCompile(`\bhk_[0-9a-f]{16}\b`)

// stable renames random ids to a_0000000000000001… in order of first sight so
// a fixture is byte-identical between runs.
var hashRe = regexp.MustCompile(`\b[0-9a-f]{64}\b`)

func stable(raw []byte, seen map[string]string) []byte {
	raw = hashRe.ReplaceAll(raw, []byte("0000000000000000000000000000000000000000000000000000000000000000"))
	raw = hookKeyRe.ReplaceAllFunc(raw, func(m []byte) []byte {
		if v, ok := seen[string(m)]; ok {
			return []byte(v)
		}
		v := fmt.Sprintf("hk_%016d", len(seen)+1)
		seen[string(m)] = v
		return []byte(v)
	})
	return idRe.ReplaceAllFunc(raw, func(m []byte) []byte {
		if v, ok := seen[string(m)]; ok {
			return []byte(v)
		}
		v := fmt.Sprintf("%c_%016d", m[0], len(seen)+1)
		seen[string(m)] = v
		return []byte(v)
	})
}

func pretty(t *testing.T, v any, seen map[string]string) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var back any
	_ = json.Unmarshal(stable(raw, seen), &back)
	out, _ := json.MarshalIndent(back, "", "  ")
	return append(out, '\n')
}

// The wire is pinned twice: these fixtures are written by the CLI's own API and
// decoded strictly by the root module's client (internal/personalagent).
func TestGoldenFixtures(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	mcpHomeFixture(t) // the registry reads the person's own MCP files
	oldProber := MCPProber
	t.Cleanup(func() { MCPProber = oldProber })
	MCPProber = func(context.Context, []string) ([]MCPProbeStatus, error) {
		return []MCPProbeStatus{{Name: "alpha", Status: "connected"}}, nil
	}
	s, _ := openTest(t, WithClock(func() time.Time { return now }))
	api := &API{Store: s}
	seen := map[string]string{}
	fixtures := map[string][]byte{}

	run := func(name, stdin string, args ...string) map[string]any {
		out, err := call(t, api, stdin, args...)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		fixtures[name] = pretty(t, out, seen)
		return out
	}
	asst := run("assistant-created", `{"requestId":"g-a","name":"Ada","description":"Follows my pull requests"}`, "assistant", "create")
	aid := field(asst, "assistant", "id").(string)
	run("assistant-list", ``, "assistant", "list")

	g := run("goal-draft", fmt.Sprintf(`{"requestId":"g-g","assistantId":%q,"title":"Watch PR 12","objective":"Tell me when it changes","commitments":[{"kind":"pr_watch","successCriteria":"merged","exitCondition":"merged or closed","spec":"{\"repo\":\"o/r\",\"number\":12}"}]}`, aid), "assistant", "goal", "create")
	gid := field(g, "goal", "id").(string)
	run("goal-active", `{"requestId":"g-ap","expectedRevision":1}`, "assistant", "goal", "approve", gid)

	task := run("task-created", fmt.Sprintf(`{"requestId":"g-t","assistantId":%q,"goalId":%q,"kind":"research","title":"Check the PR","brief":"Read it.","agent":"omp"}`, aid, gid), "assistant", "task", "create")
	tid := field(task, "task", "id").(string)
	r, _, _ := s.ClaimTask(tid, "daemon")
	_ = s.MarkStarting(r.ID)
	_ = s.RecordLaunch(r.ID, Session{HerdrSession: "default", WorkspaceID: "w1", PaneID: "w1:p1"})
	_ = s.FinishRun(r.ID, RunDone, "")
	run("task-detail", ``, "assistant", "task", "show", tid)

	mem := run("memory-candidate", fmt.Sprintf(`{"requestId":"g-m","assistantId":%q,"content":"Prefers short reports","sourceRef":"run:r_1"}`, aid), "assistant", "memory", "propose")
	mid := field(mem, "memory", "id").(string)
	run("memory-approved", `{"requestId":"g-m2","expectedRevision":1}`, "assistant", "memory", "approve", mid)

	ap := run("approval-pending", fmt.Sprintf(`{"requestId":"g-p","assistantId":%q,"subjectKind":"task","subjectId":%q,"action":"merge work/x","ttlSeconds":3600}`, aid, tid), "assistant", "approval", "request")
	_ = ap
	run("overview", ``, "assistant", "overview")

	run("inbox", ``, "assistant", "inbox", "--assistant", aid)

	started := run("coordinator-starting", `{"requestId":"g-cs"}`, "assistant", "coordinator", "start", aid)
	ctask, _ := s.GetTask(field(started, "coordinator", "taskId").(string))
	cr, _, _ := s.ClaimTask(ctask.ID, "daemon")
	_ = s.MarkStarting(cr.ID)
	_ = s.RecordLaunch(cr.ID, Session{HerdrSession: "default", WorkspaceID: "w2", PaneID: "w2:p1", AgentSessionID: "sess-1"})
	cr, _ = s.GetRun(cr.ID)
	s.syncCoordinator(ctask, cr)
	run("coordinator-ready", ``, "assistant", "coordinator", "show", aid)

	// Groups (Assistants 3.1 U1).
	bo := run("assistant-created-b", `{"requestId":"g-b","name":"Bo"}`, "assistant", "create")
	bid := field(bo, "assistant", "id").(string)
	grp := run("group-created", fmt.Sprintf(`{"requestId":"g-grp","title":"Launch crew","members":[%q,%q]}`, aid, bid), "assistant", "group", "create")
	grpID := field(grp, "group", "id").(string)
	run("group-list", ``, "assistant", "group", "list")
	posted := run("group-posted", fmt.Sprintf(`{"requestId":"g-post","groupId":%q,"body":"@Bo can you check the launch plan?","mentions":[%q]}`, grpID, bid), "assistant", "group", "post")
	postedID := field(posted, "result", "message", "id").(string)
	ga, _, _ := s.StartGroupAgent("g-ga", grpID, bid, "", "")
	gat, _ := s.GetTask(ga.TaskID)
	_, _ = s.groupLaunchPrompt(Assistant{ID: bid, Name: "Bo"}, grpID)
	s.syncGroupAgent(gat, Run{Status: RunRunning, HerdrSession: "default", WorkspaceID: "w3", PaneID: "w3:p1"})
	run("group-said", fmt.Sprintf(`{"requestId":"g-say","groupId":%q,"as":%q,"body":"Looks fine; I filed a task.","replyTo":%q}`, grpID, bid, postedID), "assistant", "group", "say")
	run("group-task-created", fmt.Sprintf(`{"requestId":"g-gt","assistantId":%q,"groupId":%q,"kind":"research","title":"Check the launch plan","brief":"Check."}`, bid, grpID), "assistant", "task", "create")
	run("group-feed", ``, "assistant", "group", "show", grpID)

	// Persona (Assistants 3.1 U2).
	run("persona-instructions", fmt.Sprintf(`{"requestId":"g-pi","body":"Be brief and precise.","expectedRevision":1}`), "assistant", "persona", "set-instructions", bid)
	run("persona-runtime", `{"requestId":"g-pr","agent":"claude","model":"sonnet","effort":"high","expectedRevision":0}`, "assistant", "persona", "set-runtime", bid)
	run("persona-mcp", `{"requestId":"g-pg","serverId":"alpha","granted":true}`, "assistant", "persona", "mcp-grant", bid)
	s.bindMCP(bid, "coordinator", "claude")
	run("persona-mcp-verified", ``, "assistant", "persona", "mcp-verify", bid)

	// Goal plan + ledger (Assistants 3.1 U3).
	run("goal-plan-set", `{"requestId":"g-pl","expectedRevision":0,"reason":"first plan","successCriteria":"The report exists and was read.","nextAction":"Collect the numbers.","milestones":[{"id":"m1","title":"Collect the numbers","task":{"kind":"research","title":"Collect numbers","brief":"Find the Q3 numbers."}},{"id":"m2","title":"You read the report","dependsOn":["m1"]}]}`, "assistant", "goal", "set-plan", gid)
	if _, err := (&Supervisor{Store: s}).Tick(); err != nil {
		t.Fatal(err)
	}
	run("goal-plan", ``, "assistant", "goal", "plan", gid)
	run("goal-facts", ``, "assistant", "goal", "facts", gid)
	run("assistant-facts", ``, "assistant", "facts", aid)

	// Routines (Assistants 3.1 U4): drafted in chat, an approval card, listed with their owner.
	cfgDir := t.TempDir()
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", cfgDir)
	t.Setenv("HERDR_PLUGIN_STATE_DIR", cfgDir)
	oldLocal := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = oldLocal })
	api.Now = func() time.Time { return now }
	run("routine-draft", fmt.Sprintf(`{"requestId":"g-rt","assistantId":%q,"name":"morning-check","cron":"*/30 * * * *","repo":"/tmp","prompt":"Check the repository for risky changes.","rationale":"A recurring check the user asked for in chat."}`, aid), "assistant", "routine", "draft")
	run("routine-list", ``, "assistant", "routine", "list", "--assistant", aid)

	// Memory workspace and proactivity (Assistants 3.1 U5). The clock moves on so item order never rests on random ids.
	now = now.Add(time.Second)
	madd := run("memory-add", fmt.Sprintf(`{"requestId":"g-ma","assistantId":%q,"content":"Prefers short answers","kind":"preference"}`, aid), "assistant", "memory", "add")
	umid := field(madd, "memory", "id").(string)
	run("memory-corrected", `{"requestId":"g-mc","content":"Prefers short answers with one example","expectedRevision":1}`, "assistant", "memory", "correct", umid)
	run("memory-history", ``, "assistant", "memory", "history", umid)
	run("memory-review", ``, "assistant", "memory", "review", aid)
	run("proactivity-default", ``, "assistant", "proactivity", "show", aid)
	run("proactivity-set", `{"requestId":"g-pa","expectedRevision":0,"level":"suggest","quietStart":"22:00","quietEnd":"08:00","tz":"Asia/Shanghai","reportEveryMin":120,"dailyBudget":10}`, "assistant", "proactivity", "set", aid)

	// Autonomy surface (Gaps A05/A06/A09): webhook keys, their receipts and event triggers.
	now = now.Add(time.Second)
	hk := run("hook-key-created", fmt.Sprintf(`{"requestId":"g-hk","assistantId":%q,"sources":["github","ci"]}`, aid), "assistant", "hook", "key", "create")
	hkID, secret := field(hk, "key", "id").(string), field(hk, "secret").(string)
	run("hook-key-list", ``, "assistant", "hook", "key", "list", "--assistant", aid)
	hookBody := fmt.Sprintf(`{"source":"github","event":"check.requested","resource":{"type":"task","id":%q},"reason":"CI build failed"}`, tid)
	ts, hp := fmt.Sprint(now.Unix()), HookPath(aid)
	delivery, _ := json.Marshal(HookDelivery{KeyID: hkID, Method: "POST", Path: hp, Timestamp: ts, Delivery: "gh-1001", Body: hookBody,
		Signature: HookSign(secret, "POST", hp, ts, "gh-1001", []byte(hookBody))})
	if _, err := call(t, api, string(delivery), "assistant", "hook", "ingest"); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	run("hook-events", ``, "assistant", "hook", "events", aid, "--limit", "20")
	trg := run("trigger-draft", fmt.Sprintf(`{"requestId":"g-tg","assistantId":%q,"name":"diagnose","on":"task.failed","taskId":%q,"title":"Diagnose the failure","brief":"Find out why it failed."}`, aid, tid), "assistant", "trigger", "draft")
	trgID := field(trg, "trigger", "id").(string)
	run("trigger-list", ``, "assistant", "trigger", "list", "--assistant", aid)
	run("trigger-withdrawn", `{"requestId":"g-tw"}`, "assistant", "trigger", "cancel", trgID)
	run("hook-key-revoked", `{"requestId":"g-hr"}`, "assistant", "hook", "key", "revoke", hkID)

	// Owner key and grants (Gap A04): the owner's client registers a public key,
	// seals a memory.autosave grant, and an Agent's proposal is then kept at once.
	t.Setenv("SOLCO_OWNER_PUBKEY_FILE", filepath.Join(t.TempDir(), "owner.pub"))
	ownerSeed := make([]byte, ed25519.SeedSize)
	for i := range ownerSeed {
		ownerSeed[i] = 7
	}
	ownerPriv := ed25519.NewKeyFromSeed(ownerSeed)
	ownerPub := base64.StdEncoding.EncodeToString(ownerPriv.Public().(ed25519.PublicKey))
	run("owner-status-none", ``, "assistant", "owner", "status")
	run("owner-registered", fmt.Sprintf(`{"requestId":"g-or","publicKey":%q}`, ownerPub), "assistant", "owner", "register")
	WithGrantVerifier(Ed25519Verifier(ownerPriv.Public().(ed25519.PublicKey)))(s)
	now = now.Add(time.Second)
	grantIn := GrantInput{ID: "gr_00000000000000a1", AssistantID: aid, Capability: CapMemoryAutosave, Effect: "allow",
		Scope: map[string][]string{"kind": {"preference"}}, MaxPerDay: 5, IssuedAt: now.UnixMilli(), ExpiresAt: now.Add(30 * 24 * time.Hour).UnixMilli()}
	grantIn.Seal = base64.StdEncoding.EncodeToString(ed25519.Sign(ownerPriv, GrantPayload(grantIn)))
	grantBody, _ := json.Marshal(struct {
		RequestID string `json:"requestId"`
		GrantInput
	}{"g-gr", grantIn})
	run("grant-created", string(grantBody), "assistant", "grant", "create")
	run("grant-list", ``, "assistant", "grant", "list", "--assistant", aid)
	run("memory-autosaved", fmt.Sprintf(`{"requestId":"g-mauto","assistantId":%q,"kind":"preference","content":"Likes the weekly digest on Monday","sourceRef":"run:r_1"}`, aid), "assistant", "memory", "propose")
	run("grant-revoked", `{"requestId":"g-grv"}`, "assistant", "grant", "revoke", grantIn.ID)

	// The budget ledger (Gap A05): what the Supervisor spent and held today.
	run("budget-show", ``, "assistant", "budget", "show", aid, "--limit", "20")

	// A finished research task's proof (Gap Q04): the answer with where it was
	// read from, its sources and what the run did.
	proofTask, _, err := s.CreateTask("g-proof-task", CreateTaskInput{AssistantID: aid, Title: "Find the pricing page", Brief: "Look it up.", Kind: KindResearch})
	if err != nil {
		t.Fatal(err)
	}
	finishWithResult(t, s, proofTask.ID, WorkerResult{Summary: "Pricing starts at $10.", Evidence: EvidenceOMPTranscript, AgentSessionID: "sess-1", MessageID: "msg-9",
		SHA256: "a1", Tools: map[string]int{"web_fetch": 2}, ToolErrors: 1, Skills: []string{"web-research"},
		Sources: []SourceRef{{URL: "https://example.com/pricing", Grounded: true}, {URL: "https://example.com/guess"}}})
	run("task-proof", ``, "assistant", "task", "proof", proofTask.ID)

	// A review's wire shape (Gap Q02). Its evidence comes from a real git
	// worktree (random hashes), so the fixture pins the shape of one with every
	// field present rather than a recorded run.
	fixtures["review-shape"] = pretty(t, map[string]any{"schema": 1, "review": Review{
		ID: "v_0000000000000001", TaskID: "t_0000000000000001", AssistantID: aid, State: ReviewChangesRequested,
		Branch: "solco/t-1", Head: "1111111", BaseHead: "0000000", TargetBranch: "main", TargetHead: "0000000",
		Files: 1, Insertions: 3, Deletions: 1, Paths: []string{"a.txt"}, DiffHash: "d1",
		Checks: []CheckResult{{Command: "go test ./...", OK: true, DurationMS: 12}},
		Error:  "Changes requested: add a test", Revision: 2, CreatedAt: 1, UpdatedAt: 2}}, seen)

	_, err = call(t, api, `{"requestId":"g-x","expectedRevision":1}`, "assistant", "goal", "pause", gid)
	var ce *CodedError
	if !asCodedErr(err, &ce) {
		t.Fatalf("want a coded error, got %v", err)
	}
	fixtures["error-stale-revision"] = pretty(t, map[string]any{"schema": 1, "error": ce}, seen)

	dir := filepath.Join("testdata")
	for name, want := range fixtures {
		path := filepath.Join(dir, name+".json")
		if *update {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, want, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v (run go test -update)", name, err)
		}
		if string(got) != string(want) {
			t.Fatalf("%s drifted from its golden file; re-run with -update after reviewing:\n--- want\n%s--- got\n%s", name, want, got)
		}
	}
}
