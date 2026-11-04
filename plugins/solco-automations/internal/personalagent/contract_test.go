package personalagent

import (
	"context"
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

var idRe = regexp.MustCompile(`\b[adgtrcmpef]_[0-9a-f]{16}\b`)

// stable renames random ids to a_0000000000000001… in order of first sight so
// a fixture is byte-identical between runs.
var hashRe = regexp.MustCompile(`\b[0-9a-f]{64}\b`)

func stable(raw []byte, seen map[string]string) []byte {
	raw = hashRe.ReplaceAll(raw, []byte("0000000000000000000000000000000000000000000000000000000000000000"))
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

	task := run("task-created", fmt.Sprintf(`{"requestId":"g-t","assistantId":%q,"goalId":%q,"kind":"research","title":"Check the PR","brief":"Read it.","agent":"claude"}`, aid, gid), "assistant", "task", "create")
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

	_, err := call(t, api, `{"requestId":"g-x","expectedRevision":1}`, "assistant", "goal", "pause", gid)
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
