package personalagent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewAssistantGetsDefaultInstructions(t *testing.T) {
	s, _ := openTest(t)
	a, _, err := s.CreateAssistant("c1", CreateAssistantInput{Name: "Ada", Description: "Follows my pull requests."})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(a.RolePrompt, "Ada") || !strings.Contains(a.RolePrompt, "Follows my pull requests.") || strings.Contains(a.RolePrompt, "..") {
		t.Fatalf("default role = %q", a.RolePrompt)
	}
	ins, err := s.GetInstructions(a.ID)
	if err != nil || ins.Revision != 1 || len(ins.History) != 1 || ins.History[0].Source != SourceDefault || ins.Body != a.RolePrompt {
		t.Fatalf("instructions = %+v err=%v", ins, err)
	}
	// An explicit role is kept as the person's own text.
	b, _, _ := s.CreateAssistant("c2", CreateAssistantInput{Name: "Bo", RolePrompt: "Be terse."})
	if bi, _ := s.GetInstructions(b.ID); bi.Body != "Be terse." || bi.History[0].Source != SourceUser {
		t.Fatalf("explicit = %+v", bi)
	}
}

func TestInstructionsRevisionsStaleAndRestore(t *testing.T) {
	s, _ := openTest(t)
	a, _, _ := s.CreateAssistant("c1", CreateAssistantInput{Name: "Ada"})
	first, _ := s.GetInstructions(a.ID)

	got, replayed, err := s.SetInstructions("i1", a.ID, "  Be formal.  ", 1)
	if err != nil || replayed || got.Revision != 2 || got.Body != "Be formal." {
		t.Fatalf("set = %+v replayed=%v err=%v", got, replayed, err)
	}
	// The active text is the one authority launches read.
	cur, _ := s.GetAssistant(a.ID)
	if cur.RolePrompt != "Be formal." || cur.Revision != a.Revision+1 {
		t.Fatalf("assistant after set = %+v", cur)
	}
	// The same request replays; a stale editor is refused.
	if again, rep, err := s.SetInstructions("i1", a.ID, "  Be formal.  ", 1); err != nil || !rep || again.Revision != 2 {
		t.Fatalf("replay = %+v %v %v", again, rep, err)
	}
	if _, _, err := s.SetInstructions("i2", a.ID, "Other", 1); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale err = %v", err)
	}
	for _, bad := range []string{"", "   ", strings.Repeat("x", MaxInstructionsBytes+1), "a\x00b"} {
		if _, _, err := s.SetInstructions("", a.ID, bad, 2); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%q accepted: %v", bad[:min(len(bad), 8)], err)
		}
	}
	// Restore is a new revision carrying the old text: history only grows.
	rest, _, err := s.RestoreInstructions("i3", a.ID, 1, 2)
	if err != nil || rest.Revision != 3 || rest.Body != first.Body || len(rest.History) != 3 || rest.History[0].Source != SourceRestore {
		t.Fatalf("restore = %+v err=%v", rest, err)
	}
	if _, _, err := s.RestoreInstructions("i4", a.ID, 9, 3); !errors.Is(err, ErrNotFound) {
		t.Fatalf("restore missing = %v", err)
	}
}

func TestAssistantFromBeforeU2MaterialisesItsInstructions(t *testing.T) {
	s, _ := openTest(t)
	_, err := s.db.Exec(`INSERT INTO assistants(` + assistantCols + `) VALUES('a_legacy','Old','', 'Legacy role','personal','[]','','review','active',1,1,1)`)
	if err != nil {
		t.Fatal(err)
	}
	ins, err := s.GetInstructions("a_legacy")
	if err != nil || ins.Revision != 1 || ins.Body != "Legacy role" || ins.History[0].Source != SourceUser {
		t.Fatalf("legacy = %+v err=%v", ins, err)
	}
	_, _ = s.db.Exec(`INSERT INTO assistants(` + assistantCols + `) VALUES('a_blank','Blank','Does X','','personal','[]','','review','active',1,1,1)`)
	blank, _ := s.GetInstructions("a_blank")
	if !strings.Contains(blank.Body, "Does X") || blank.History[0].Source != SourceDefault {
		t.Fatalf("blank = %+v", blank)
	}
	if got, _ := s.GetAssistant("a_blank"); got.RolePrompt != blank.Body {
		t.Fatalf("role text not stored: %q", got.RolePrompt)
	}
}

func TestLaunchRecordsTheInstructionsRevisionItStartedWith(t *testing.T) {
	s, _ := openTest(t)
	a, _, _ := s.CreateAssistant("c1", CreateAssistantInput{Name: "Ada"})
	co, _, _ := s.StartCoordinator("co1", a.ID)
	l := &fakeLauncher{}
	d := &Dispatcher{Store: s, Launcher: l, Claimant: "t"}
	if _, err := d.RunTask(t.Context(), co.TaskID); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(l.specs[0].Prompt, a.RolePrompt) {
		t.Fatalf("the active instructions are not in the brief: %q", l.specs[0].Prompt)
	}
	ins, _ := s.GetInstructions(a.ID)
	if len(ins.Applied) != 1 || ins.Applied[0].Surface != "coordinator" || ins.Applied[0].Revision != 1 {
		t.Fatalf("applied = %+v", ins.Applied)
	}
	// An edit afterwards is "next session": the applied revision stays 1.
	_, _, _ = s.SetInstructions("e1", a.ID, "New style", 1)
	ins, _ = s.GetInstructions(a.ID)
	if ins.Revision != 2 || ins.Applied[0].Revision != 1 {
		t.Fatalf("after edit: rev=%d applied=%+v", ins.Revision, ins.Applied)
	}
}

func TestRuntimeProfileDefaultsAndExplicitWins(t *testing.T) {
	s, _ := openTest(t)
	a, _, _ := s.CreateAssistant("c1", CreateAssistantInput{Name: "Ada"})
	rt, err := s.GetRuntime(a.ID)
	if err != nil || rt.Profile.Agent != "" || rt.Profile.Revision != 0 || rt.Profile.EffortSupported || len(rt.Running) != 0 {
		t.Fatalf("fresh = %+v err=%v", rt, err)
	}
	if _, _, err := s.SetRuntime("r0", a.ID, "codex", "gpt-x", "high", 5); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("stale = %v", err)
	}
	for _, bad := range [][3]string{{"Not An Agent", "", ""}, {"codex", "a\nb", ""}, {"codex", "", "very high"}} {
		if _, _, err := s.SetRuntime("", a.ID, bad[0], bad[1], bad[2], 0); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%v accepted: %v", bad, err)
		}
	}
	rt, _, err = s.SetRuntime("r1", a.ID, "codex", "gpt-x", "high", 0)
	if err != nil || rt.Profile.Agent != "codex" || rt.Profile.Revision != 1 || rt.Profile.EffortSupported {
		t.Fatalf("set = %+v err=%v", rt, err)
	}
	if rt, _, _ = s.SetRuntime("r2", a.ID, "claude", "sonnet", "max", 1); rt.Profile.Revision != 2 || !rt.Profile.EffortSupported {
		t.Fatalf("second set = %+v", rt)
	}
	rt, _, _ = s.SetRuntime("r3", a.ID, "codex", "gpt-x", "", 2)

	// Unnamed Agent = the profile (with its model); a named Agent wins and does
	// not inherit the profile's model.
	co, _, _ := s.StartCoordinator("co1", a.ID)
	if tk, _ := s.GetTask(co.TaskID); tk.Agent != "omp" || tk.Model != "" {
		t.Fatalf("coordinator task = %s/%s", tk.Agent, tk.Model)
	}
	tk, _, err := s.CreateTask("t1", CreateTaskInput{AssistantID: a.ID, Title: "T", Brief: "B", Kind: KindResearch})
	if err != nil || tk.Agent != "omp" || tk.Model != "" {
		t.Fatalf("task = %s/%s %v", tk.Agent, tk.Model, err)
	}
	tk2, _, _ := s.CreateTask("t2", CreateTaskInput{AssistantID: a.ID, Title: "T", Brief: "B", Kind: KindResearch, Agent: "claude"})
	if tk2.Agent != "claude" || tk2.Model != "" {
		t.Fatalf("explicit task = %s/%s", tk2.Agent, tk2.Model)
	}
	// The running Agent is what really runs, apart from the profile.
	_, _, _ = s.SetRuntime("r4", a.ID, "claude", "", "", 3)
	rt, _ = s.GetRuntime(a.ID)
	if rt.Profile.Agent != "claude" {
		t.Fatalf("profile = %+v", rt.Profile)
	}
}

func TestRuntimeShowsTheRealRunningAgent(t *testing.T) {
	s, _ := openTest(t)
	a, _, _ := s.CreateAssistant("c1", CreateAssistantInput{Name: "Ada"})
	co, _, _ := s.StartCoordinatorAs("co1", a.ID, "omp", "gpt-9")
	run, _, err := s.ClaimTask(co.TaskID, "t")
	if err != nil {
		t.Fatal(err)
	}
	_ = s.MarkStarting(run.ID)
	_ = s.RecordLaunch(run.ID, Session{HerdrSession: "default", WorkspaceID: "w1", PaneID: "w1:p1"})
	_, _, _ = s.SetRuntime("r1", a.ID, "claude", "", "", 0)
	rt, _ := s.GetRuntime(a.ID)
	if len(rt.Running) != 1 || rt.Running[0].Surface != "coordinator" || rt.Running[0].Agent != "omp" || rt.Running[0].Model != "gpt-9" || rt.Running[0].PaneID != "w1:p1" {
		t.Fatalf("running = %+v (profile %+v)", rt.Running, rt.Profile)
	}
}

func TestLaunchPassesEffortOnlyWhereTheAgentTakesIt(t *testing.T) {
	s, _ := openTest(t)
	t.Setenv("HOME", t.TempDir())
	a, _, _ := s.CreateAssistant("c1", CreateAssistantInput{Name: "Ada"})
	_, _, _ = s.SetRuntime("r1", a.ID, "claude", "", "high", 0)
	// OMP Coordinator is locked; verify legacy Claude effort on a worker.
	co, _, _ := s.CreateTask("effort-task", CreateTaskInput{AssistantID: a.ID, Title: "Check", Brief: "brief", Kind: KindResearch, Agent: "claude"})
	l := &fakeLauncher{}
	d := &Dispatcher{Store: s, Launcher: l, Claimant: "t"}
	if _, err := d.RunTask(t.Context(), co.ID); err != nil {
		t.Fatal(err)
	}
	args := strings.Join(l.specs[0].AgentArgs, " ")
	if !strings.Contains(args, "--effort high") || !strings.Contains(args, "--strict-mcp-config") {
		t.Fatalf("args = %q", args)
	}

	_, _, _ = s.SetRuntime("r2", a.ID, "codex", "", "high", 1)
	tk, _, _ := s.CreateTask("t1", CreateTaskInput{AssistantID: a.ID, Title: "T", Brief: "B", Kind: KindResearch})
	if _, err := d.RunTask(t.Context(), tk.ID); err != nil {
		t.Fatal(err)
	}
	if got := l.specs[1].AgentArgs; len(got) != 0 {
		t.Fatalf("codex got args %v", got)
	}
}

// --- MCP ---------------------------------------------------------------------

func mcpHomeFixture(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	write := func(rel, body string) {
		p := filepath.Join(home, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(".claude.json", `{"projects":{},"mcpServers":{"alpha":{"command":"/usr/local/bin/alpha-mcp","args":["--token","ARGSECRET"],"env":{"KEY":"ENVSECRET"}},"bad name!":{"command":"x"}}}`)
	write(".mcp.json", `{"mcpServers":{"beta":{"type":"http","url":"https://beta.example.com/mcp?key=URLSECRET","headers":{"Authorization":"HEADERSECRET"}}}}`)
	write(".codex/config.toml", "model = \"x\"\n\n[mcp_servers.gamma]\ncommand = \"npx\"\nargs = [\"-y\", \"TOMLSECRET\"]\n\n[mcp_servers.gamma.env]\nTOKEN = \"TOMLENV\"\n")
	return home
}

func TestMCPRegistryShowsNamesAndTransportNeverSecrets(t *testing.T) {
	mcpHomeFixture(t)
	servers, raw := discoverMCP(mcpHome())
	blob, _ := json.Marshal(servers)
	for _, secret := range []string{"ARGSECRET", "ENVSECRET", "URLSECRET", "HEADERSECRET", "TOMLSECRET", "TOMLENV", "bad name"} {
		if strings.Contains(string(blob), secret) {
			t.Fatalf("registry leaks %q: %s", secret, blob)
		}
	}
	by := map[string]MCPServer{}
	for _, sv := range servers {
		by[sv.ID] = sv
	}
	if by["alpha"].Transport != "stdio" || by["alpha"].Summary != "alpha-mcp" || by["alpha"].Agents[0] != "claude" {
		t.Fatalf("alpha = %+v", by["alpha"])
	}
	if by["beta"].Transport != "http" || by["beta"].Summary != "beta.example.com" {
		t.Fatalf("beta = %+v", by["beta"])
	}
	if by["gamma"].Agents[0] != "codex" || by["gamma"].Summary != "npx" {
		t.Fatalf("gamma = %+v", by["gamma"])
	}
	if _, ok := raw["gamma"]; ok {
		t.Fatal("a Codex server must not be bindable into a Claude config")
	}
}

func TestMCPGrantsBindStrictlyAndHonestly(t *testing.T) {
	mcpHomeFixture(t)
	s, path := openTest(t)
	a, _, _ := s.CreateAssistant("c1", CreateAssistantInput{Name: "Ada"})
	b, _, _ := s.CreateAssistant("c2", CreateAssistantInput{Name: "Bo"})
	_, _, _ = s.SetRuntime("a-claude", a.ID, "claude", "", "", 0)
	_, _, _ = s.SetRuntime("b-claude", b.ID, "claude", "", "", 0)

	v, err := s.GetMCP(a.ID)
	if err != nil || !v.Supported || len(v.Servers) != 3 {
		t.Fatalf("view = %+v err=%v", v, err)
	}
	for _, sv := range v.Servers {
		if sv.Granted || len(sv.Bound) != 0 || sv.Verified != "" || !sv.Configured {
			t.Fatalf("nothing is granted yet: %+v", sv)
		}
	}
	if _, _, err := s.SetMCPGrant("", a.ID, "bad id!", true); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad id = %v", err)
	}
	v, _, err = s.SetMCPGrant("g1", a.ID, "alpha", true)
	if err != nil {
		t.Fatal(err)
	}
	// Granted is not bound: nothing launched yet.
	if sv := serverState(v, "alpha"); !sv.Granted || len(sv.Bound) != 0 {
		t.Fatalf("alpha = %+v", sv)
	}
	// A grant for a server that is gone stays visible as not configured.
	v, _, _ = s.SetMCPGrant("g2", a.ID, "vanished", true)
	if sv := serverState(v, "vanished"); sv.Configured || !sv.Granted {
		t.Fatalf("vanished = %+v", sv)
	}

	args := s.bindMCP(a.ID, "coordinator", "claude")
	cfg := filepath.Join(filepath.Dir(path), "assistants-mcp", a.ID+".json")
	if len(args) != 3 || args[0] != "--strict-mcp-config" || args[1] != "--mcp-config" || args[2] != cfg {
		t.Fatalf("args = %v", args)
	}
	st, err := os.Stat(cfg)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("config mode: %v %v", st, err)
	}
	body, _ := os.ReadFile(cfg)
	if !strings.Contains(string(body), `"alpha"`) || strings.Contains(string(body), "beta") || strings.Contains(string(body), "vanished") {
		t.Fatalf("config = %s", body)
	}
	// Bo has no grants: strict and empty, so no user MCP server leaks in.
	bargs := s.bindMCP(b.ID, "coordinator", "claude")
	bbody, _ := os.ReadFile(bargs[2])
	if len(bargs) != 3 || strings.TrimSpace(string(bbody)) != `{"mcpServers":{}}` {
		t.Fatalf("empty config = %q args=%v", bbody, bargs)
	}

	v, _ = s.GetMCP(a.ID)
	if sv := serverState(v, "alpha"); len(sv.Bound) != 1 || sv.Bound[0] != "coordinator" || sv.Verified != "" {
		t.Fatalf("after bind: %+v", sv)
	}
	if sv := serverState(v, "vanished"); len(sv.Bound) != 0 {
		t.Fatalf("a missing server cannot be bound: %+v", sv)
	}
	if len(v.Bindings) != 1 || !v.Bindings[0].Strict || v.Bindings[0].Servers[0] != "alpha" {
		t.Fatalf("bindings = %+v", v.Bindings)
	}

	// Another Agent kind: nothing can be enforced and the binding says so.
	if cargs := s.bindMCP(a.ID, "group:g1", "codex"); len(cargs) != 0 {
		t.Fatalf("codex args = %v", cargs)
	}
	v, _ = s.GetMCP(a.ID)
	var codexBinding MCPBinding
	for _, bd := range v.Bindings {
		if bd.Surface == "group:g1" {
			codexBinding = bd
		}
	}
	if codexBinding.Unsupported == "" || codexBinding.Strict || len(codexBinding.Servers) != 0 {
		t.Fatalf("codex binding = %+v", codexBinding)
	}

	// Withdrawing a grant is honoured by the next bind.
	_, _, _ = s.SetMCPGrant("g3", a.ID, "alpha", false)
	args = s.bindMCP(a.ID, "coordinator", "claude")
	body, _ = os.ReadFile(args[2])
	if strings.TrimSpace(string(body)) != `{"mcpServers":{}}` {
		t.Fatalf("after withdraw: %s", body)
	}
}

func serverState(v MCPView, id string) MCPServerState {
	for _, sv := range v.Servers {
		if sv.ID == id {
			return sv
		}
	}
	return MCPServerState{}
}

func TestMCPVerifyRecordsTheAgentsOwnStatus(t *testing.T) {
	mcpHomeFixture(t)
	s, _ := openTest(t)
	a, _, _ := s.CreateAssistant("c1", CreateAssistantInput{Name: "Ada"})
	_, _, _ = s.SetRuntime("claude-profile", a.ID, "claude", "", "", 0)
	_, _, _ = s.SetMCPGrant("g1", a.ID, "alpha", true)
	_, _, _ = s.SetMCPGrant("g2", a.ID, "beta", true)

	var got []string
	old := MCPProber
	t.Cleanup(func() { MCPProber = old })
	MCPProber = func(ctx context.Context, args []string) ([]MCPProbeStatus, error) {
		got = args
		return []MCPProbeStatus{{Name: "alpha", Status: "connected"}, {Name: "beta", Status: "failed"}}, nil
	}
	v, err := s.VerifyMCP(t.Context(), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != "--strict-mcp-config" {
		t.Fatalf("probe args = %v", got)
	}
	if sv := serverState(v, "alpha"); sv.Verified != "connected" || sv.VerifiedAt == 0 {
		t.Fatalf("alpha = %+v", sv)
	}
	if sv := serverState(v, "beta"); sv.Verified != "failed" {
		t.Fatalf("beta = %+v", sv)
	}
	// A probe is not a launch: no surface is "bound" by it.
	if len(v.Bindings) != 0 || len(serverState(v, "alpha").Bound) != 0 {
		t.Fatalf("probe leaked into bindings: %+v", v.Bindings)
	}
	// Ungranted servers show no verdict.
	if sv := serverState(v, "gamma"); sv.Verified != "" {
		t.Fatalf("gamma = %+v", sv)
	}

	MCPProber = func(ctx context.Context, args []string) ([]MCPProbeStatus, error) {
		return nil, errors.New("claude is not installed")
	}
	if _, err := s.VerifyMCP(t.Context(), a.ID); err == nil || codeFor(err).Code != "mcp_probe_failed" {
		t.Fatalf("probe failure = %v", err)
	}
	_, _, _ = s.SetRuntime("r1", a.ID, "codex", "", "", 1)
	if _, err := s.VerifyMCP(t.Context(), a.ID); err == nil || codeFor(err).Code != "mcp_unsupported" {
		t.Fatalf("unsupported = %v", err)
	}
	if v, _ := s.GetMCP(a.ID); v.Supported || v.Unsupported == "" {
		t.Fatalf("view for codex = %+v", v)
	}
}

func TestPersonaCLIRoundTrip(t *testing.T) {
	mcpHomeFixture(t)
	s, _ := openTest(t)
	api := &API{Store: s}
	out, err := call(t, api, `{"requestId":"p1","name":"Ada","description":"Helps"}`, "assistant", "create")
	if err != nil {
		t.Fatal(err)
	}
	id := field(out, "assistant", "id").(string)
	if _, err := call(t, api, `{"requestId":"p2","body":"Be kind.","expectedRevision":1}`, "assistant", "persona", "set-instructions", id); err != nil {
		t.Fatal(err)
	}
	out, _ = call(t, api, ``, "assistant", "persona", "instructions", id)
	if field(out, "instructions", "body") != "Be kind." {
		t.Fatalf("instructions = %v", out)
	}
	if _, err := call(t, api, `{"requestId":"p3","agent":"claude","model":"opus","effort":"high","expectedRevision":0}`, "assistant", "persona", "set-runtime", id); err != nil {
		t.Fatal(err)
	}
	if _, err := call(t, api, `{"requestId":"p4","serverId":"alpha","granted":true}`, "assistant", "persona", "mcp-grant", id); err != nil {
		t.Fatal(err)
	}
	out, _ = call(t, api, ``, "assistant", "persona", "mcp", id)
	if field(out, "mcp", "agent") != "claude" {
		t.Fatalf("mcp = %v", out)
	}
	if _, err := call(t, api, `{"requestId":"p5","typo":1}`, "assistant", "persona", "set-runtime", id); err == nil {
		t.Fatal("unknown fields must be refused")
	}
}
