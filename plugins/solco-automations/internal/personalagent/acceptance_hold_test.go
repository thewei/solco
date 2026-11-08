//go:build e2e

package personalagent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestAcceptanceHold keeps the isolated real-Herdr + real-OMP sandbox (scripted
// fixture model, scratch repo, linked plugin) alive for a Solco.app instance and
// a Computer Use session. It writes the exports to $SOLCO_ACC_ENV and exits when
// $SOLCO_ACC_STOP appears. Never touches the user's own Herdr, HOME or data.
//
//	SOLCO_E2E=1 SOLCO_ACC_ENV=… SOLCO_ACC_STOP=… SOLCO_ACC_PLUGIN=<plugin dir> go test -tags e2e -run TestAcceptanceHold -timeout 4h
func TestAcceptanceHold(t *testing.T) {
	envFile, stop, plugin := os.Getenv("SOLCO_ACC_ENV"), os.Getenv("SOLCO_ACC_STOP"), os.Getenv("SOLCO_ACC_PLUGIN")
	if envFile == "" || stop == "" || plugin == "" {
		t.Skip("acceptance hold needs SOLCO_ACC_ENV, SOLCO_ACC_STOP and SOLCO_ACC_PLUGIN")
	}
	sb := newSandbox(t, map[string][]fixtureStep{}, e2ePage)
	sb.model.delay = time.Second
	sb.model.mu.Lock()
	sb.model.scripts["*"] = []fixtureStep{{text: "Ready. Tell me what you want done."}}
	sb.model.scripts["last:Solco capabilities"] = []fixtureStep{
		{tool: "solco_assistant", args: map[string]any{"action": "goal.list"}},
		{tool: "solco_assistant", args: map[string]any{"action": "task.list"}},
		{text: "Solco actions I can use: task.create, task.list, task.show, task.graph, goal.create, goal.list, goal.show, goal.plan, goal.set-plan, routine.draft/list/show, memory.read/add/write, checkpoint.get/set. I checked goals and tasks: none yet. Plan: 1) clarify the goal, 2) file a research task for a worker, 3) report."},
	}
	sb.model.scripts["last:every weekday at 9:00"] = []fixtureStep{
		{tool: "solco_assistant", args: map[string]any{"action": "routine.draft", "payload": `{"requestId":"acc-routine-0001","name":"x-digest","schedule":"every weekday at 9:00","timezone":"Asia/Shanghai","agent":"omp","prompt":"SCENARIO:digest Use the fixture-browser skill to read the page and list the top posts with links.","rationale":"User asked for a weekday 9:00 digest.","requiredSkills":["fixture-browser"]}`}},
		{text: "I drafted a routine for weekdays 09:00 (Asia/Shanghai). It does not run until you approve the card."},
	}
	sb.model.scripts["digest"] = []fixtureStep{
		{tool: "read", args: map[string]any{"path": "skill://fixture-browser"}},
		{tool: "bash", args: map[string]any{"command": fmt.Sprintf("curl -s http://127.0.0.1:%d/page", sb.port)}},
		{text: "Top posts:\n1. https://news.example/a1 — 5200 likes\n2. https://news.example/a2 — 4100 likes\n3. https://news.example/a3 — 3900 likes\nUnavailable: https://news.example/gone (404)."},
	}
	// AC-05: a heartbeat wake. The turn inspects the goal and task graph, files
	// ONE follow-up research task (a fixed requestId, so a replayed wake cannot
	// file a second), saves its checkpoint and exits. It never pushes code.
	sb.model.scripts["last:Solco automated wake"] = []fixtureStep{
		{tool: "solco_assistant", args: map[string]any{"action": "goal.list"}},
		{tool: "solco_assistant", args: map[string]any{"action": "task.list"}},
		{tool: "solco_assistant", args: map[string]any{"action": "task.create", "payload": `{"requestId":"acc-wake-task-0001","kind":"research","title":"Wake follow-up digest","brief":"SCENARIO:digest Re-read the page and list the top posts with links."}`}},
		{tool: "solco_assistant", args: map[string]any{"action": "checkpoint.set", "payload": `{"requestId":"acc-wake-cp-0001","progress":"Checked goals and tasks; filed one follow-up research task.","nextAction":"Read the follow-up result at the next wake.","blockers":"none","stopWhen":"the person says stop"}`}},
		{text: "Woke, looked at goals and tasks, filed one follow-up task, saved my checkpoint. Nothing needs you."},
	}
	// A coding worker: commits one file in its own worktree (Q02 review rig).
	sb.model.scripts["last:SCENARIO:code"] = []fixtureStep{
		{tool: "bash", args: map[string]any{"command": "echo 'a new feature' > feature.txt && git add -A && git -c user.name=acc -c user.email=acc@example.com commit -qm 'add feature' && git log --oneline | head -1"}},
		{text: "Committed feature.txt on the task branch."},
	}
	sb.model.mu.Unlock()

	run := func(dir, name string, args ...string) string {
		c := exec.Command(name, args...)
		c.Env, c.Dir = sb.env, dir
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
		return string(out)
	}
	demo := filepath.Join(sb.work, "demo")
	run(sb.work, "git", "init", "-q", demo)
	_ = os.WriteFile(filepath.Join(demo, "README.md"), []byte("scratch project\n"), 0o644)
	herdr, _ := exec.LookPath("herdr")
	t.Logf("plugin link: %s", run(sb.work, herdr, "plugin", "link", plugin))
	// A user's OMP carries Herdr's state extension and Solco's chat-stream
	// extension; without them the Chat page has no session identity to follow.
	t.Logf("herdr integration: %s", run(sb.work, herdr, "integration", "install", "omp"))
	if raw, err := os.ReadFile(filepath.Join(realHome(), ".omp/agent/extensions/solco-chat-stream.ts")); err == nil {
		ext := filepath.Join(sb.home, ".omp/agent/extensions")
		_ = os.MkdirAll(ext, 0o755)
		_ = os.WriteFile(filepath.Join(ext, "solco-chat-stream.ts"), raw, 0o644)
	}
	skill := filepath.Join(sb.home, ".agents/skills/fixture-browser")
	_ = os.MkdirAll(skill, 0o755)
	_ = os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: fixture-browser\ndescription: Read the controlled fixture page.\n---\nFetch http://127.0.0.1:"+fmt.Sprint(sb.port)+"/page and list the posts with links.\n"), 0o644)

	var b strings.Builder
	for _, kv := range sb.env {
		k, v, _ := strings.Cut(kv, "=")
		fmt.Fprintf(&b, "export %s=%q\n", k, v)
	}
	fmt.Fprintf(&b, "export SB_ROOT=%q\nexport SB_DEMO=%q\nexport SB_PORT=%d\n", sb.root, demo, sb.port)
	if err := os.WriteFile(envFile, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(stop); err == nil {
			return
		}
		time.Sleep(time.Second)
	}
}

// realHome is the user's own home, read only to copy one extension file.
func realHome() string { return "/Users/wilson" }
