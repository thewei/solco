package personalagent

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The CLI's wire is pinned by golden files the plugin module writes
// (plugins/solco-automations/internal/personalagent/testdata). Decoding them
// strictly here means a field added on one side and forgotten on the other
// fails a test instead of silently dropping data.
const fixtures = "../../plugins/solco-automations/internal/personalagent/testdata"

func load(t *testing.T, name string, into any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(fixtures, name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

func TestGoldenFixturesDecodeStrictly(t *testing.T) {
	var a struct {
		Assistant Assistant `json:"assistant"`
		Schema    int       `json:"schema"`
		Replayed  bool      `json:"replayed"`
	}
	load(t, "assistant-created", &a)
	if a.Schema != 1 || a.Assistant.Name != "Ada" || a.Assistant.Status != "active" {
		t.Fatalf("assistant = %+v", a)
	}

	var list struct {
		Schema     int         `json:"schema"`
		Assistants []Assistant `json:"assistants"`
	}
	load(t, "assistant-list", &list)

	var g struct {
		Schema int  `json:"schema"`
		Goal   Goal `json:"goal"`
		Replay bool `json:"replayed"`
	}
	load(t, "goal-active", &g)
	if g.Goal.State != "active" || len(g.Goal.Commitments) != 1 || g.Goal.Commitments[0].Kind != "pr_watch" {
		t.Fatalf("goal = %+v", g.Goal)
	}
	load(t, "goal-draft", &g)

	var td struct {
		Schema int   `json:"schema"`
		Task   Task  `json:"task"`
		Runs   []Run `json:"runs"`
	}
	load(t, "task-detail", &td)
	if td.Task.State != "done" || len(td.Runs) != 1 || td.Runs[0].PaneID != "w1:p1" {
		t.Fatalf("task = %+v runs=%+v", td.Task, td.Runs)
	}
	var tc struct {
		Schema int  `json:"schema"`
		Task   Task `json:"task"`
		Replay bool `json:"replayed"`
	}
	load(t, "task-created", &tc)

	var m struct {
		Schema int        `json:"schema"`
		Memory MemoryItem `json:"memory"`
		Replay bool       `json:"replayed"`
	}
	load(t, "memory-candidate", &m)
	load(t, "memory-approved", &m)
	if m.Memory.State != "approved" {
		t.Fatalf("memory = %+v", m.Memory)
	}

	var ap struct {
		Schema   int      `json:"schema"`
		Approval Approval `json:"approval"`
		Replay   bool     `json:"replayed"`
	}
	load(t, "approval-pending", &ap)
	if ap.Approval.State != "pending" || ap.Approval.ActionHash == "" {
		t.Fatalf("approval = %+v", ap.Approval)
	}

	var ov Overview
	load(t, "overview", &ov)
	if len(ov.Assistants) != 1 || ov.NeedsYou != 1 {
		t.Fatalf("overview = %+v", ov)
	}

	var ib struct {
		Schema int   `json:"schema"`
		Inbox  Inbox `json:"inbox"`
	}
	load(t, "inbox", &ib)
	if ib.Inbox.NeedsYou != 1 || len(ib.Inbox.Items) != 1 || ib.Inbox.Items[0].Kind != "approval" {
		t.Fatalf("inbox = %+v", ib.Inbox)
	}

	var co struct {
		Schema      int         `json:"schema"`
		Coordinator Coordinator `json:"coordinator"`
		Replayed    bool        `json:"replayed"`
	}
	load(t, "coordinator-starting", &co)
	if co.Coordinator.State != CoordinatorStarting || co.Coordinator.TaskID == "" {
		t.Fatalf("coordinator = %+v", co.Coordinator)
	}
	load(t, "coordinator-ready", &co)
	if co.Coordinator.State != CoordinatorReady || co.Coordinator.PaneID != "w2:p1" || co.Coordinator.AgentSessionID == "" {
		t.Fatalf("coordinator = %+v", co.Coordinator)
	}

	var gc struct {
		Schema   int   `json:"schema"`
		Group    Group `json:"group"`
		Replayed bool  `json:"replayed"`
	}
	load(t, "group-created", &gc)
	if gc.Group.Title != "Launch crew" || len(gc.Group.Members) != 2 || gc.Group.Members[1].Name != "Bo" {
		t.Fatalf("group = %+v", gc.Group)
	}
	var gl struct {
		Schema int     `json:"schema"`
		Groups []Group `json:"groups"`
	}
	load(t, "group-list", &gl)
	var gp struct {
		Schema   int        `json:"schema"`
		Result   PostResult `json:"result"`
		Replayed bool       `json:"replayed"`
	}
	load(t, "group-posted", &gp)
	if len(gp.Result.Deliveries) != 1 || gp.Result.Message.AuthorKind != AuthorUser || len(gp.Result.Message.Mentions) != 1 {
		t.Fatalf("posted = %+v", gp.Result)
	}
	load(t, "group-said", &gp)
	if gp.Result.Message.AuthorKind != AuthorAssistant || len(gp.Result.Deliveries) != 0 || gp.Result.Message.ReplyTo == "" {
		t.Fatalf("said = %+v", gp.Result)
	}
	var gf struct {
		Schema int       `json:"schema"`
		Feed   GroupFeed `json:"feed"`
	}
	load(t, "group-feed", &gf)
	if len(gf.Feed.Messages) != 3 || gf.Feed.Messages[2].Kind != MessageTask || len(gf.Feed.Tasks) != 1 || gf.Feed.Agents[1].State != "starting" {
		t.Fatalf("feed = %+v", gf.Feed)
	}
	load(t, "group-task-created", &tc)

	var pi struct {
		Schema       int          `json:"schema"`
		Instructions Instructions `json:"instructions"`
		Replayed     bool         `json:"replayed"`
	}
	load(t, "persona-instructions", &pi)
	if pi.Instructions.Revision != 2 || pi.Instructions.Body != "Be brief and precise." || len(pi.Instructions.History) != 2 || pi.Instructions.History[1].Source != "default" {
		t.Fatalf("instructions = %+v", pi.Instructions)
	}
	var pr struct {
		Schema   int     `json:"schema"`
		Runtime  Runtime `json:"runtime"`
		Replayed bool    `json:"replayed"`
	}
	load(t, "persona-runtime", &pr)
	if pr.Runtime.Profile.Agent != "claude" || pr.Runtime.Profile.Effort != "high" || !pr.Runtime.Profile.EffortSupported || pr.Runtime.Running == nil {
		t.Fatalf("runtime = %+v", pr.Runtime)
	}
	var pm struct {
		Schema   int     `json:"schema"`
		MCP      MCPView `json:"mcp"`
		Replayed bool    `json:"replayed"`
	}
	load(t, "persona-mcp", &pm)
	if len(pm.MCP.Servers) != 3 || !pm.MCP.Servers[0].Granted || pm.MCP.Servers[0].Verified != "" {
		t.Fatalf("mcp = %+v", pm.MCP)
	}
	load(t, "persona-mcp-verified", &pm)
	if a := pm.MCP.Servers[0]; a.Verified != "connected" || len(a.Bound) != 1 || a.Bound[0] != "coordinator" || len(pm.MCP.Bindings) != 1 || !pm.MCP.Bindings[0].Strict {
		t.Fatalf("verified mcp = %+v", pm.MCP)
	}

	var e struct {
		Schema int    `json:"schema"`
		Error  *Error `json:"error"`
	}
	load(t, "error-stale-revision", &e)
	if e.Error == nil || e.Error.Code != "stale_revision" {
		t.Fatalf("error = %+v", e)
	}
}

func TestGoalPlanAndFactsGolden(t *testing.T) {
	var pl struct {
		Schema   int      `json:"schema"`
		Plan     GoalPlan `json:"plan"`
		Replayed bool     `json:"replayed"`
	}
	load(t, "goal-plan-set", &pl)
	if pl.Plan.PlanRevision != 1 || pl.Plan.Total != 2 || pl.Plan.Milestones[0].Task == nil || pl.Plan.Milestones[1].Task != nil {
		t.Fatalf("set plan = %+v", pl.Plan)
	}
	var got struct {
		Schema int      `json:"schema"`
		Plan   GoalPlan `json:"plan"`
	}
	load(t, "goal-plan", &got)
	if got.Plan.State != "working" || got.Plan.Milestones[0].State != "working" || got.Plan.Milestones[0].TaskID == "" || got.Plan.Milestones[1].State != "pending" {
		t.Fatalf("plan = %+v", got.Plan)
	}
	for _, name := range []string{"goal-facts", "assistant-facts"} {
		var f struct {
			Schema int    `json:"schema"`
			Facts  []Fact `json:"facts"`
		}
		load(t, name, &f)
		kinds := ""
		for _, x := range f.Facts {
			kinds += x.Kind + ","
		}
		if len(f.Facts) < 3 || f.Facts[0].Kind != "delegated" || !strings.Contains(kinds, "planned") || !strings.Contains(kinds, "accepted") {
			t.Fatalf("%s kinds = %s", name, kinds)
		}
	}
}

func TestRoutineGolden(t *testing.T) {
	var d struct {
		Schema   int      `json:"schema"`
		Routine  Routine  `json:"routine"`
		Approval Approval `json:"approval"`
		Next     []int64  `json:"next"`
		Replayed bool     `json:"replayed"`
	}
	load(t, "routine-draft", &d)
	if d.Routine.State != "draft" || d.Routine.RequestedBy != "assistant" || d.Approval.SubjectKind != "routine" || d.Approval.SubjectID != d.Routine.Name || len(d.Next) != 3 {
		t.Fatalf("draft = %+v", d)
	}
	if d.Routine.ApprovalID != d.Approval.ID || d.Routine.ApprovalState != "pending" || !d.Routine.Headless {
		t.Fatalf("routine view = %+v", d.Routine)
	}
	var l struct {
		Schema   int       `json:"schema"`
		Routines []Routine `json:"routines"`
	}
	load(t, "routine-list", &l)
	if len(l.Routines) != 1 || l.Routines[0].Name != "morning-check" {
		t.Fatalf("list = %+v", l)
	}
}

func TestMemoryAndProactivityGolden(t *testing.T) {
	var add struct {
		Schema   int        `json:"schema"`
		Memory   MemoryItem `json:"memory"`
		Replayed bool       `json:"replayed"`
	}
	load(t, "memory-add", &add)
	if add.Memory.State != "approved" || add.Memory.SourceRef != "user" || add.Memory.Revision != 1 {
		t.Fatalf("add = %+v", add)
	}
	var hist struct {
		Schema   int             `json:"schema"`
		Versions []MemoryVersion `json:"versions"`
	}
	load(t, "memory-history", &hist)
	if len(hist.Versions) != 2 || hist.Versions[0].Revision != 2 || hist.Versions[1].Author != "user" || hist.Versions[1].Content != add.Memory.Content {
		t.Fatalf("history = %+v", hist)
	}
	var rev struct {
		Schema int          `json:"schema"`
		Review MemoryReview `json:"review"`
	}
	load(t, "memory-review", &rev)
	if rev.Review.Max != 500 || len(rev.Review.Items) == 0 || rev.Review.Duplicates == nil || rev.Review.Conflicts == nil || rev.Review.StaleCandidates == nil {
		t.Fatalf("review = %+v", rev.Review)
	}
	var def, set struct {
		Schema      int         `json:"schema"`
		Proactivity Proactivity `json:"proactivity"`
		Replayed    bool        `json:"replayed"`
	}
	load(t, "proactivity-default", &def)
	load(t, "proactivity-set", &set)
	if def.Proactivity.Level != "follow_up" || def.Proactivity.Revision != 0 || set.Proactivity.Level != "suggest" || set.Proactivity.Revision != 1 || set.Proactivity.TZ != "Asia/Shanghai" {
		t.Fatalf("proactivity = %+v / %+v", def.Proactivity, set.Proactivity)
	}
}

// A04: the grant wire decodes strictly, and the owner-side sealing here
// reproduces the plugin's golden vector byte for byte.
func TestGrantAndOwnerFixturesAndSealingVector(t *testing.T) {
	var list struct {
		Schema       int      `json:"schema"`
		Grants       []Grant  `json:"grants"`
		Capabilities []string `json:"capabilities"`
	}
	load(t, "grant-list", &list)
	if len(list.Grants) != 1 || !list.Grants[0].Verified || list.Grants[0].Capability != "memory.autosave" || len(list.Capabilities) == 0 {
		t.Fatalf("grants = %+v", list)
	}
	var one struct {
		Schema   int   `json:"schema"`
		Grant    Grant `json:"grant"`
		Replayed bool  `json:"replayed"`
	}
	load(t, "grant-created", &one)
	load(t, "grant-revoked", &one)
	if one.Grant.Status != "revoked" {
		t.Fatalf("revoked = %+v", one.Grant)
	}
	var owner struct {
		Schema int         `json:"schema"`
		Owner  OwnerStatus `json:"owner"`
	}
	load(t, "owner-status-none", &owner)
	load(t, "owner-registered", &owner)
	if !owner.Owner.Configured || owner.Owner.Fingerprint == "" {
		t.Fatalf("owner = %+v", owner)
	}

	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = 7
	}
	priv := ed25519.NewKeyFromSeed(seed)
	in := GrantInput{ID: "gr_00000000000000aa", AssistantID: "a_0000000000000001", Capability: "memory.autosave", Effect: "allow",
		Scope: map[string][]string{"kind": {"preference", "fact"}}, MaxPerDay: 5, IssuedAt: 1800000000000, ExpiresAt: 1800086400000}
	if got := string(GrantPayload(in)); got != "solco-grant-v1\n{\"id\":\"gr_00000000000000aa\",\"assistantId\":\"a_0000000000000001\",\"capability\":\"memory.autosave\",\"effect\":\"allow\",\"scope\":{\"kind\":[\"fact\",\"preference\"]},\"maxPerDay\":5,\"issuedAt\":1800000000000,\"expiresAt\":1800086400000}" {
		t.Fatalf("payload drifted from the plugin's: %q", got)
	}
	if got := SealGrant(priv, in).Seal; got != "j7x5VuKXUtOepb0z2POGe9AH1E2ofJlnU9Sq6mnJ+TYcpmeA8EYzzPThOYYfRcxU3QFWwt7ns8y72T+8gnL7BA==" {
		t.Fatalf("seal drifted from the plugin's: %q", got)
	}
}
