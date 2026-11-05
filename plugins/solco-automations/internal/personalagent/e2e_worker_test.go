//go:build e2e

// [INPUT]: real Herdr server + real OMP TUI in a throw-away HOME, a scripted loopback model
// [OUTPUT]: proof that Dispatcher→HostLauncher→Herdr→OMP produces a run whose result is read back from the pinned transcript
// [POS]: M2 gate for W01/W02/W04 (not a unit test): `SOLCO_E2E=1 go test -tags e2e -run TestE2E ./internal/personalagent -v`
// [PROTOCOL]: never touches the user's Herdr session, OMP home, launchd or accounts
package personalagent

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thewei/solco/plugins/solco-automations/internal/config"
)

type fixtureStep struct {
	tool string         // empty = final text
	args map[string]any // tool arguments
	text string
}

// fixtureModel is an OpenAI-compatible SSE endpoint that follows a script per
// scenario (named in the first user message) and counts tool results to know
// which step it is on. It also serves the controlled test page.
type fixtureModel struct {
	mu       sync.Mutex
	scripts  map[string][]fixtureStep
	requests int
	page     string
	delay    time.Duration // a real model is never instant; herdr needs to see "working"
}

func (f *fixtureModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/page" {
		_, _ = w.Write([]byte(f.page))
		return
	}
	if r.URL.Path != "/v1/chat/completions" {
		http.NotFound(w, r)
		return
	}
	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad", 400)
		return
	}
	f.mu.Lock()
	f.requests++
	f.mu.Unlock()
	scenario, tools := "", 0
	for _, m := range req.Messages {
		raw, _ := json.Marshal(m.Content)
		if m.Role == "user" && scenario == "" {
			for name := range f.scripts {
				if strings.Contains(string(raw), "SCENARIO:"+name) {
					scenario = name
				}
			}
		}
		if m.Role == "tool" {
			tools++
		}
	}
	if scenario == "" {
		scenario = "*"
	}
	script := f.scripts[scenario]
	step := fixtureStep{text: "no scenario matched"}
	if tools < len(script) {
		step = script[tools]
	}
	time.Sleep(f.delay)
	w.Header().Set("Content-Type", "text/event-stream")
	chunk := func(delta map[string]any, finish any) {
		b, _ := json.Marshal(map[string]any{"id": "c", "object": "chat.completion.chunk", "created": 1, "model": "m",
			"choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": finish}}})
		fmt.Fprintf(w, "data: %s\n\n", b)
		w.(http.Flusher).Flush()
	}
	if step.tool != "" {
		args, _ := json.Marshal(step.args)
		chunk(map[string]any{"role": "assistant", "tool_calls": []map[string]any{{"index": 0, "id": fmt.Sprintf("call_%d", tools), "type": "function",
			"function": map[string]any{"name": step.tool, "arguments": string(args)}}}}, nil)
		chunk(map[string]any{}, "tool_calls")
	} else {
		chunk(map[string]any{"role": "assistant", "content": step.text}, nil)
		chunk(map[string]any{}, "stop")
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
}

type sandbox struct {
	root, home, work string
	env              []string
	model            *fixtureModel
	port             int
	herdr            *exec.Cmd
}

func newSandbox(t *testing.T, scripts map[string][]fixtureStep, page string) *sandbox {
	t.Helper()
	if os.Getenv("SOLCO_E2E") != "1" {
		t.Skip("set SOLCO_E2E=1 to run the real Herdr+OMP gate")
	}
	herdr, err := exec.LookPath("herdr")
	if err != nil {
		t.Skip("herdr not installed")
	}
	omp, err := exec.LookPath("omp")
	if err != nil {
		t.Skip("omp not installed")
	}
	root, err := os.MkdirTemp("/tmp", "sw-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	sb := &sandbox{root: root, home: filepath.Join(root, "home"), work: filepath.Join(root, "work")}
	for _, d := range []string{sb.home, sb.work, filepath.Join(sb.home, ".config/herdr"), filepath.Join(sb.home, ".omp/agent")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	sb.model = &fixtureModel{scripts: scripts, page: page}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sb.port = ln.Addr().(*net.TCPAddr).Port
	srv := &http.Server{Handler: sb.model}
	go srv.Serve(ln)
	t.Cleanup(func() { _ = srv.Close() })

	bunDir := filepath.Dir(omp)
	path := strings.Join([]string{bunDir, filepath.Dir(herdr), "/usr/bin", "/bin", "/usr/sbin", "/sbin"}, ":")
	for _, f := range []string{".zshenv", ".zshrc", ".zprofile", ".bash_profile", ".bashrc", ".profile"} {
		_ = os.WriteFile(filepath.Join(sb.home, f), []byte("export PATH=\""+path+":$PATH\"\n"), 0o644)
	}
	cfg := filepath.Join(sb.home, ".config/herdr/config.toml")
	_ = os.WriteFile(cfg, []byte(fmt.Sprintf("data_dir = %q\n", filepath.Join(sb.home, ".herdr-data"))), 0o644)
	models := map[string]any{"providers": map[string]any{"smoke": map[string]any{
		"baseUrl": fmt.Sprintf("http://127.0.0.1:%d/v1", sb.port), "api": "openai-completions", "apiKey": "local",
		"models": []map[string]any{{"id": "smoke", "name": "Smoke", "contextWindow": 32768, "maxTokens": 2048, "input": []string{"text"},
			"cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}}}}}}
	raw, _ := json.Marshal(models)
	_ = os.WriteFile(filepath.Join(sb.home, ".omp/agent/models.json"), raw, 0o644)

	env := []string{"PATH=" + path, "HOME=" + sb.home, "XDG_CONFIG_HOME=" + sb.home + "/.config", "XDG_CACHE_HOME=" + sb.home + "/.cache",
		"XDG_DATA_HOME=" + sb.home + "/.local/share", "HERDR_CONFIG_PATH=" + cfg, "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"NO_PROXY=127.0.0.1,localhost", "OPENAI_API_KEY=local", "TMPDIR=/tmp", "LANG=en_US.UTF-8",
		"HERDR_PLUGIN_STATE_DIR=" + filepath.Join(root, "state"), "HERDR_PLUGIN_CONFIG_DIR=" + filepath.Join(root, "conf"),
		"SOLCO_ASSISTANTS_WORKDIR=" + filepath.Join(root, "assistants-work"), "SOLCO_ASSISTANTS_DB=" + filepath.Join(root, "state", "a.sqlite")}
	sb.env = env
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
	for _, k := range []string{"HERDR_ENV", "HERDR_PANE_ID", "HERDR_SOCKET_PATH", "HERDR_SESSION", "HERDR_WORKSPACE_ID"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	run := func(name string, args ...string) {
		c := exec.Command(name, args...)
		c.Env, c.Dir = env, sb.work
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
	}
	run(omp, "config", "set", "startup.setupWizard", "false")
	// A routine's omp takes no --model flag (Gap R: it uses the user's default), so the sandbox's default is the fixture.
	run(omp, "config", "set", "modelRoles", `{"default":"smoke/smoke"}`)
	run("git", "init", "-q", sb.work)
	sb.herdr = exec.Command(herdr, "server")
	sb.herdr.Env, sb.herdr.Dir = env, sb.work
	logf, _ := os.Create(filepath.Join(root, "herdr.log"))
	sb.herdr.Stdout, sb.herdr.Stderr = logf, logf
	if err := sb.herdr.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if t.Failed() {
			for _, args := range [][]string{{"workspace", "list"}, {"pane", "read", "w1:p1", "--lines", "60"}, {"agent", "list"}} {
				c := exec.Command(herdr, args...)
				c.Env = env
				out, _ := c.CombinedOutput()
				t.Logf("herdr %v:\n%s", args, out)
			}
			_ = filepath.WalkDir(root, func(path string, e os.DirEntry, err error) error {
				if err == nil && e.Type().IsRegular() && strings.HasSuffix(path, ".jsonl") {
					raw, _ := os.ReadFile(path)
					for _, line := range strings.Split(string(raw), "\n") {
						if strings.Contains(line, `"toolResult"`) {
							t.Logf("transcript %s: %s", filepath.Base(path), tailString(line, 700))
						}
					}
				}
				return nil
			})
			log, _ := os.ReadFile(filepath.Join(root, "herdr.log"))
			t.Logf("herdr.log tail:\n%s", tailString(string(log), 1500))
		}
		// The assistant CLI starts its own standby daemon; it must not outlive the sandbox.
		_ = exec.Command("pkill", "-f", root).Run()
		stop := exec.Command(herdr, "server", "stop")
		stop.Env = env
		_ = stop.Run()
		_ = sb.herdr.Process.Kill()
		_, _ = sb.herdr.Process.Wait()
	})
	sock := filepath.Join(sb.home, ".config/herdr/herdr.sock")
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(sock); err == nil {
			return sb
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("sandbox herdr did not start")
	return nil
}

const e2ePage = `<h1>Digest page</h1>
<a href="https://news.example/a1">A1 — 5200 likes — 2026-11-05T01:00Z</a>
<a href="https://news.example/a2">A2 — 4100 likes — 2026-11-05T02:00Z</a>
<a href="https://news.example/a3">A3 — 3900 likes — 2026-11-05T03:00Z</a>
<a href="https://news.example/gone">GONE — 404</a>`

// AC-04 mechanics: a real OMP Worker runs through Herdr, uses a skill and a
// tool, and Solco reads its answer back from the pinned transcript.
func TestE2EWorkerResultIsReadBackFromTheRealTranscript(t *testing.T) {
	skill := "/skill-placeholder"
	sbScripts := map[string][]fixtureStep{}
	var sb *sandbox
	page := e2ePage
	// The scripts need the port, so build them after the sandbox exists.
	sb = newSandbox(t, sbScripts, page)
	skillDir := filepath.Join(sb.home, ".agents/skills/fixture-browser")
	_ = os.MkdirAll(skillDir, 0o755)
	skill = filepath.Join(skillDir, "SKILL.md")
	_ = os.WriteFile(skill, []byte("---\nname: fixture-browser\ndescription: reads the controlled fixture page\n---\nUse curl on the page URL.\n"), 0o644)
	sb.model.mu.Lock()
	sb.model.scripts["digest"] = []fixtureStep{
		{tool: "read", args: map[string]any{"path": "skill://fixture-browser"}},
		{tool: "bash", args: map[string]any{"command": fmt.Sprintf("curl -s http://127.0.0.1:%d/page", sb.port)}},
		{text: "Top 3 posts:\n1. https://news.example/a1 — 5200 likes\n2. https://news.example/a2 — 4100 likes\n3. https://news.example/a3 — 3900 likes\nUnavailable: https://news.example/gone (404)."},
	}
	sb.model.mu.Unlock()

	s, _ := openTest(t)
	a := mustAssistant(t, s, "e2e-a", "Ada")
	task, _, err := s.CreateTask("e2e-t1", CreateTaskInput{AssistantID: a.ID, Kind: KindResearch, Title: "Digest", Model: "smoke/smoke",
		Brief: "SCENARIO:digest read the fixture page with the skill and list the posts with links", RequiredSkills: []string{"fixture-browser"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.GrantSkill(a.ID, "fixture-browser"); err != nil { // a person's yes (the card path is unit-tested)
		t.Fatal(err)
	}
	d := &Dispatcher{Store: s, Launcher: NewHostLauncher("", WorkDirRoot()), Claimant: "e2e", Timeout: 3 * time.Minute}
	run, err := d.RunTask(t.Context(), task.ID)
	if err != nil {
		t.Fatalf("run: %v (run=%+v)", err, run)
	}
	t.Logf("run=%+v", run)
	if run.Status != RunDone {
		t.Fatalf("run status = %s error=%q", run.Status, run.Error)
	}
	res, ok, err := s.RunResult(run.ID)
	if err != nil || !ok {
		t.Fatalf("no stored result: %v %v", ok, err)
	}
	t.Logf("result=%+v", res)
	if !strings.Contains(res.Summary, "https://news.example/a1") || res.MessageID == "" || res.AgentSessionID == "" {
		t.Fatalf("result = %+v", res)
	}
	if len(res.Skills) != 1 || res.Skills[0] != "fixture-browser" || res.Tools["bash"] != 1 {
		t.Fatalf("skills/tools = %+v %+v", res.Skills, res.Tools)
	}
	grounded := 0
	for _, src := range res.Sources {
		if src.Grounded {
			grounded++
		}
	}
	if grounded < 4 {
		t.Fatalf("sources = %+v", res.Sources)
	}
}

func tailString(s string, n int) string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}

// pluginLayout builds <root>/plugin/{bin,extensions} the way an installed
// plugin looks, and points CLI at its binary so the Coordinator extension finds
// both.
func pluginLayout(t *testing.T, sb *sandbox, goBin string, goEnv []string) string {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	moduleRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	plugin := filepath.Join(sb.root, "plugin")
	for _, d := range []string{"bin", "extensions"} {
		if err := os.MkdirAll(filepath.Join(plugin, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	build := exec.Command(goBin, "build", "-o", filepath.Join(plugin, "bin", "solco-automations"), ".")
	build.Dir = moduleRoot
	build.Env = goEnv
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build plugin: %v\n%s", err, out)
	}
	src, err := os.ReadFile(filepath.Join(moduleRoot, "extensions", "solco-coordinator.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plugin, "extensions", "solco-coordinator.ts"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	old := CLI
	CLI = filepath.Join(plugin, "bin", "solco-automations")
	t.Cleanup(func() { CLI = old })
	return plugin
}

// C01/C02/C03: a real OMP Coordinator in a real Herdr pane loads the broker
// extension, is refused a shell, and files work only through solco_assistant,
// owned by its own Assistant.
func TestE2ECoordinatorLoadsTheBrokerAndCannotUseAShell(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go not installed")
	}
	goEnv := append(os.Environ(), "GOTOOLCHAIN=auto") // before the sandbox rewrites HOME/PATH
	sb := newSandbox(t, map[string][]fixtureStep{}, e2ePage)
	pluginLayout(t, sb, goBin, goEnv)
	sb.model.delay = 2 * time.Second
	marker := filepath.Join(sb.root, "shell-ran")
	payload, _ := json.Marshal(map[string]any{"requestId": "e2e-coord-task-1", "kind": "research", "title": "From chat", "brief": "SCENARIO:idle Find the top posts and cite sources."})
	sb.model.mu.Lock()
	sb.model.scripts["idle"] = []fixtureStep{{text: "Nothing to do in this fixture."}} // the worker the CLI's own daemon starts for the filed task
	sb.model.scripts["*"] = []fixtureStep{
		{tool: "bash", args: map[string]any{"command": "touch " + marker}},
		{tool: "solco_assistant", args: map[string]any{"action": "task.create", "payload": string(payload)}},
		{text: "Delegated."},
	}
	sb.model.mu.Unlock()

	s, err := Open(DBPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	a := mustAssistant(t, s, "e2e-coord-a", "Ada")
	c, _, err := s.StartCoordinatorAs("e2e-coord-start", a.ID, "omp", "smoke/smoke")
	if err != nil {
		t.Fatal(err)
	}
	d := &Dispatcher{Store: s, Launcher: NewHostLauncher("", WorkDirRoot()), Claimant: "e2e", Timeout: 3 * time.Minute}
	run, err := d.RunTask(t.Context(), c.TaskID)
	t.Logf("run=%+v err=%v", run, err)
	if err != nil || run.Status != RunDone {
		t.Fatalf("Coordinator launch: status=%s err=%v (a delivered prompt must never read as a failed launch)", run.Status, err)
	}
	if fi, statErr := os.Stat(marker); statErr == nil {
		t.Logf("marker: %v %v", fi.Name(), fi.ModTime())
		t.Fatal("the Coordinator ran a shell command; the tool gate did not hold")
	}
	tasks, err := s.ListTasks(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	var filed []Task
	for _, tk := range tasks {
		if tk.Kind == KindResearch {
			filed = append(filed, tk)
		}
	}
	if len(filed) != 1 || filed[0].Title != "From chat" || filed[0].Agent != AssistantAgentKind {
		t.Fatalf("tasks = %+v", tasks)
	}
}

// C02/C03 negative: OMP keeps running when the broker extension throws on load.
// The Coordinator must then be inert (no shell, no broker), not ungated.
func TestE2ECoordinatorWithABrokenExtensionIsInertNotUngated(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go not installed")
	}
	goEnv := append(os.Environ(), "GOTOOLCHAIN=auto")
	sb := newSandbox(t, map[string][]fixtureStep{}, e2ePage)
	plugin := pluginLayout(t, sb, goBin, goEnv)
	broken := filepath.Join(plugin, "extensions", "solco-coordinator.ts")
	if err := os.WriteFile(broken, []byte("export default function () { throw new Error(\"boom\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sb.model.delay = 2 * time.Second
	marker := filepath.Join(sb.root, "shell-ran")
	payload, _ := json.Marshal(map[string]any{"requestId": "e2e-broken-1", "kind": "research", "title": "Must not exist", "brief": "x"})
	sb.model.mu.Lock()
	sb.model.scripts["*"] = []fixtureStep{
		{tool: "bash", args: map[string]any{"command": "touch " + marker}},
		{tool: "solco_assistant", args: map[string]any{"action": "task.create", "payload": string(payload)}},
		{text: "done"},
	}
	sb.model.mu.Unlock()
	s, err := Open(DBPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	a := mustAssistant(t, s, "e2e-broken-a", "Ada")
	c, _, err := s.StartCoordinatorAs("e2e-broken-start", a.ID, "omp", "smoke/smoke")
	if err != nil {
		t.Fatal(err)
	}
	d := &Dispatcher{Store: s, Launcher: NewHostLauncher("", WorkDirRoot()), Claimant: "e2e", Timeout: 3 * time.Minute}
	run, err := d.RunTask(t.Context(), c.TaskID)
	t.Logf("run=%+v err=%v", run, err)
	pane, _ := exec.Command("herdr", "pane", "read", run.PaneID, "--lines", "30").CombinedOutput()
	t.Logf("pane after a broken broker:\n%s", pane)
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("a Coordinator whose extension failed to load ran a shell command")
	}
	tasks, _ := s.ListTasks(a.ID)
	for _, tk := range tasks {
		if tk.Kind == KindResearch {
			t.Fatalf("a task was filed without the broker: %+v", tk)
		}
	}
}

// AC-02 (controlled page): a real OMP Coordinator turns a request in words into
// a Routine DRAFT with a named skill, nothing runs or schedules before the
// person's yes, then one occurrence becomes one task that a real OMP Worker
// completes with the skill and the evidence read back from its transcript.
// The real X.com account is never touched: the "browser" is a fixture skill
// over a loopback page.
func TestE2EScenarioOneRoutineFromChatToVerifiedDigest(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go not installed")
	}
	goEnv := append(os.Environ(), "GOTOOLCHAIN=auto")
	cronBefore, _ := exec.Command("crontab", "-l").CombinedOutput()
	sb := newSandbox(t, map[string][]fixtureStep{}, e2ePage)
	pluginLayout(t, sb, goBin, goEnv)
	sb.model.delay = 2 * time.Second
	skillDir := filepath.Join(sb.home, ".agents/skills/fixture-browser")
	_ = os.MkdirAll(skillDir, 0o755)
	_ = os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: fixture-browser\ndescription: reads the controlled fixture page\n---\nUse curl on the page URL.\n"), 0o644)
	draft, _ := json.Marshal(map[string]any{
		"requestId": "e2e-routine-1", "name": "x-digest", "schedule": "every weekday at 9:00", "timezone": "Asia/Shanghai",
		"agent": "omp", "requiredSkills": []string{"fixture-browser"},
		"prompt":    "SCENARIO:digest Use the fixture-browser skill to read the page and list the top posts with links.",
		"rationale": "The person asked for a weekday morning digest.",
	})
	sb.model.mu.Lock()
	sb.model.scripts["*"] = []fixtureStep{
		{tool: "solco_assistant", args: map[string]any{"action": "routine.draft", "payload": string(draft)}},
		{text: "Drafted a weekday 9:00 digest; it starts only after you approve the card."},
	}
	sb.model.scripts["digest"] = []fixtureStep{
		{tool: "read", args: map[string]any{"path": "skill://fixture-browser"}},
		{tool: "bash", args: map[string]any{"command": fmt.Sprintf("curl -s http://127.0.0.1:%d/page", sb.port)}},
		{text: "Top posts:\n1. https://news.example/a1 — 5200 likes\n2. https://news.example/a2 — 4100 likes\n3. https://news.example/a3 — 3900 likes\nUnavailable: https://news.example/gone (404)."},
	}
	sb.model.mu.Unlock()

	s, err := Open(DBPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	a := mustAssistant(t, s, "e2e-r-a", "Ada")
	c, _, err := s.StartCoordinatorAs("e2e-r-start", a.ID, "omp", "smoke/smoke")
	if err != nil {
		t.Fatal(err)
	}
	d := &Dispatcher{Store: s, Launcher: NewHostLauncher("", WorkDirRoot()), Claimant: "e2e", Timeout: 3 * time.Minute}
	if run, err := d.RunTask(t.Context(), c.TaskID); err != nil || run.Status != RunDone {
		t.Fatalf("coordinator: %+v %v", run, err)
	}

	// Drafted, not running: one routine, one pending card, no work.
	api := &API{Store: s}
	got, err := call(t, api, `{"assistantId":"`+a.ID+`"}`, "assistant", "routine", "list")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("routines=%v", got)
	cards, _ := s.ListApprovals(a.ID)
	var card Approval
	for _, ap := range cards {
		if ap.SubjectKind == "routine" {
			card = ap
		}
	}
	if card.ID == "" || !strings.Contains(card.Action, "fixture-browser") || !strings.Contains(card.Action, "Asia/Shanghai") {
		t.Fatalf("the routine card must name schedule zone and skill: %+v", cards)
	}
	if tasks, _ := s.ListTasks(a.ID); len(tasks) != 0 {
		t.Fatalf("a draft must not create tasks: %+v", tasks)
	}
	cfg, err := config.Load()
	if err != nil || cfg.Find("x-digest") == nil {
		t.Fatalf("draft not in automations.yaml: %v", err)
	}
	sched, err := config.CronParser.Parse(cfg.Find("x-digest").Cron)
	if err != nil {
		t.Fatal(err)
	}
	fireAt := sched.Next(time.Now()).Add(5 * time.Second) // the next 09:00 Shanghai weekday, five seconds late
	rs := &RuleScheduler{Store: s, Load: config.Load, Now: func() time.Time { return fireAt }}
	if rep, err := rs.Tick(); err != nil || rep.Fired != 0 {
		t.Fatalf("a draft fired before approval: %+v %v", rep, err)
	}

	// The person's yes enables it; the one scheduler fires one occurrence once.
	body := fmt.Sprintf(`{"requestId":"yes-1","actionHash":%q,"approve":true}`, card.ActionHash)
	if _, err := call(t, api, body, "assistant", "approval", "decide", card.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := api.keeper().Tick(); err != nil {
		t.Fatal(err)
	}
	if rep, err := rs.Tick(); err != nil || rep.Fired != 1 {
		t.Fatalf("approved routine should fire exactly once: %+v %v", rep, err)
	}
	if rep, err := rs.Tick(); err != nil || rep.Fired != 0 {
		t.Fatalf("the same occurrence fired twice: %+v %v", rep, err)
	}
	var occ Task
	for _, tk := range mustTasks(t, s, a.ID) {
		if tk.Kind == KindManual {
			occ = tk
		}
	}
	if occ.ID == "" {
		t.Fatal("no occurrence task")
	}

	// No grant yet: the Worker must not start and a skill card is raised.
	d.Rules = func(name string) (config.Automation, bool) {
		cfg, _ := config.Load()
		if x := cfg.Find(name); x != nil {
			return *x, true
		}
		return config.Automation{}, false
	}
	run, _ := d.RunTask(t.Context(), occ.ID)
	if run.Status != RunFailed || !strings.HasPrefix(run.Error, SkillNotGrantedPrefix) {
		t.Fatalf("an ungranted skill must stop the run: %+v", run)
	}
	if err := s.GrantSkill(a.ID, "fixture-browser"); err != nil {
		t.Fatal(err)
	}
	run, err = d.RunTask(t.Context(), occ.ID)
	if err != nil || run.Status != RunDone {
		t.Fatalf("worker run: %+v %v", run, err)
	}
	res, ok, err := s.RunResult(run.ID)
	if err != nil || !ok || !strings.Contains(res.Summary, "https://news.example/a1") {
		t.Fatalf("result = %+v %v %v", res, ok, err)
	}
	if len(res.Skills) != 1 || res.Skills[0] != "fixture-browser" || res.Tools["bash"] != 1 {
		t.Fatalf("skills/tools = %+v %+v", res.Skills, res.Tools)
	}
	if after, _ := exec.Command("crontab", "-l").CombinedOutput(); string(after) != string(cronBefore) {
		t.Fatal("the user's crontab changed: nothing may schedule outside Solco's core")
	}
}

func mustTasks(t *testing.T, s *Store, id string) []Task {
	t.Helper()
	ts, err := s.ListTasks(id)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

// AC-05/Scenario four (delivery half): a fact the Solco core recorded is
// handed ONCE to the original, live OMP Coordinator through Herdr, and the
// reply is proven from OMP's own transcript, not from Herdr's idle status.
func TestE2EFactReachesTheOriginalCoordinatorExactlyOnceAndTheReplyIsProven(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go not installed")
	}
	goEnv := append(os.Environ(), "GOTOOLCHAIN=auto")
	sb := newSandbox(t, map[string][]fixtureStep{}, e2ePage)
	pluginLayout(t, sb, goBin, goEnv)
	sb.model.delay = 2 * time.Second
	sb.model.mu.Lock()
	sb.model.scripts["*"] = []fixtureStep{{text: "Noted. The digest is ready and I will tell you what needs you."}}
	sb.model.mu.Unlock()

	s, err := Open(DBPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	a := mustAssistant(t, s, "e2e-rep-a", "Ada")
	c, _, err := s.StartCoordinatorAs("e2e-rep-start", a.ID, "omp", "smoke/smoke")
	if err != nil {
		t.Fatal(err)
	}
	d := &Dispatcher{Store: s, Launcher: NewHostLauncher("", WorkDirRoot()), Claimant: "e2e", Timeout: 3 * time.Minute}
	if run, err := d.RunTask(t.Context(), c.TaskID); err != nil || run.Status != RunDone {
		t.Fatalf("coordinator: %+v %v", run, err)
	}
	if _, _, err := s.SetProactivity("e2e-rep-pro", ProactivityInput{AssistantID: a.ID, Level: LevelFollowUp, ReportEveryMin: 1, DailyBudget: 5}, 0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute(s, "", "e2e.fact", nil, func(tx *sql.Tx) (bool, error) {
		return addFactTx(tx, Fact{AssistantID: a.ID, Kind: FactRunFinished, Text: "Worker finished the X digest: 3 posts, 1 unavailable."}, "e2e:fact:1", s.nowMS())
	}); err != nil {
		t.Fatal(err)
	}
	home := os.Getenv("HOME")
	pump := &ReportPump{Store: s, Submit: SubmitReport, Ready: ReportReady, Blocked: ReportDialog,
		Verify: func(marker string, at int64) (ReplyEvidence, error) { return FindOMPReportReplySince(home, marker, at) }}
	deadline := time.Now().Add(90 * time.Second)
	var r CoordinatorReport
	for time.Now().Before(deadline) {
		if _, err := pump.Tick(); err != nil {
			t.Fatal(err)
		}
		r, _ = s.GetCoordinatorReport(a.ID)
		if r.ReplyState == ReplyVerified {
			break
		}
		time.Sleep(1500 * time.Millisecond)
	}
	if r.ReplyState != ReplyVerified || r.ReplyMessageID == "" || r.ReplySessionID == "" {
		t.Fatalf("the reply was never proven from OMP's transcript: %+v", r)
	}
	// Idempotence: more ticks must not type the same facts again.
	for i := 0; i < 4; i++ {
		_, _ = pump.Tick()
		time.Sleep(500 * time.Millisecond)
	}
	prompts := 0
	_ = filepath.WalkDir(filepath.Join(home, ".omp/agent/sessions"), func(p string, e os.DirEntry, err error) error {
		if err == nil && strings.HasSuffix(p, ".jsonl") {
			raw, _ := os.ReadFile(p)
			prompts += strings.Count(string(raw), "X digest: 3 posts")
		}
		return nil
	})
	// Once in the user turn; the reply text does not repeat the fact.
	if prompts != 1 {
		t.Fatalf("the fact was typed into the Coordinator %d times, want exactly 1", prompts)
	}
}

// AC-07 / W07 / L: the Worker's workspace disappears mid-run (a person closed
// it, or Herdr lost it). The run must end as failed or uncertain with a reason,
// the task must not read as done, and no result may be invented.
func TestE2EWorkerVanishingMidRunIsNeverReportedAsDone(t *testing.T) {
	sb := newSandbox(t, map[string][]fixtureStep{}, e2ePage)
	sb.model.delay = 2 * time.Second
	sb.model.mu.Lock()
	sb.model.scripts["slowwork"] = []fixtureStep{
		{tool: "bash", args: map[string]any{"command": "sleep 90"}},
		{text: "Finished everything. https://news.example/a1"},
	}
	sb.model.mu.Unlock()
	s, _ := openTest(t)
	a := mustAssistant(t, s, "e2e-van-a", "Ada")
	task, _, err := s.CreateTask("e2e-van-t", CreateTaskInput{AssistantID: a.ID, Kind: KindResearch, Title: "Slow", Model: "smoke/smoke",
		Brief: "SCENARIO:slowwork do the slow thing"})
	if err != nil {
		t.Fatal(err)
	}
	d := &Dispatcher{Store: s, Launcher: NewHostLauncher("", WorkDirRoot()), Claimant: "e2e", Timeout: 3 * time.Minute}
	type out struct {
		run Run
		err error
	}
	done := make(chan out, 1)
	go func() {
		r, err := d.RunTask(t.Context(), task.ID)
		done <- out{r, err}
	}()
	herdr, _ := exec.LookPath("herdr")
	deadline := time.Now().Add(60 * time.Second)
	closed := false
	var seen time.Time
	for time.Now().Before(deadline) && !closed {
		time.Sleep(time.Second)
		c := exec.Command(herdr, "agent", "list")
		c.Env = sb.env
		raw, _ := c.CombinedOutput()
		// Herdr reports "unknown" while a long bash tool runs, so: once the agent
		// exists and has had time to start the tool, pull its workspace away.
		if strings.Contains(string(raw), `"pane_id"`) && seen.IsZero() {
			seen = time.Now()
		}
		if !seen.IsZero() && time.Since(seen) > 12*time.Second {
			c := exec.Command(herdr, "workspace", "close", "w1")
			c.Env = sb.env
			if o, err := c.CombinedOutput(); err != nil {
				t.Fatalf("close: %v %s", err, o)
			}
			closed = true
		}
	}
	if !closed {
		t.Fatal("the worker never appeared")
	}
	var res out
	select {
	case res = <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("RunTask did not notice the vanished worker")
	}
	t.Logf("run=%+v err=%v", res.run, res.err)
	if res.run.Status == RunDone {
		t.Fatalf("a vanished worker was reported done: %+v", res.run)
	}
	if res.run.Status != RunFailed && res.run.Status != RunUncertain && res.run.Status != RunCancelled {
		t.Fatalf("status = %s, want failed, uncertain or cancelled", res.run.Status)
	}
	if strings.TrimSpace(res.run.Error) == "" {
		t.Fatal("a failed run must say why")
	}
	if _, ok, _ := s.RunResult(res.run.ID); ok {
		t.Fatal("a result was invented for a worker that never finished")
	}
	got, _ := s.GetTask(task.ID)
	if got.State == TaskDone {
		t.Fatalf("task state = %s", got.State)
	}
	t.Logf("task state=%s error=%q", got.State, res.run.Error)
}
