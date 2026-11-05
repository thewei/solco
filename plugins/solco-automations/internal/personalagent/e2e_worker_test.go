//go:build e2e

// [INPUT]: real Herdr server + real OMP TUI in a throw-away HOME, a scripted loopback model
// [OUTPUT]: proof that Dispatcher→HostLauncher→Herdr→OMP produces a run whose result is read back from the pinned transcript
// [POS]: M2 gate for W01/W02/W04 (not a unit test): `SOLCO_E2E=1 go test -tags e2e -run TestE2E ./internal/personalagent -v`
// [PROTOCOL]: never touches the user's Herdr session, OMP home, launchd or accounts
package personalagent

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
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
	script := f.scripts[scenario]
	step := fixtureStep{text: "no scenario matched"}
	if tools < len(script) {
		step = script[tools]
	}
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
			log, _ := os.ReadFile(filepath.Join(root, "herdr.log"))
			t.Logf("herdr.log tail:\n%s", tailString(string(log), 1500))
		}
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
		{tool: "read", args: map[string]any{"path": skill}},
		{tool: "bash", args: map[string]any{"command": fmt.Sprintf("curl -s http://127.0.0.1:%d/page", sb.port)}},
		{text: "Top 3 posts:\n1. https://news.example/a1 — 5200 likes\n2. https://news.example/a2 — 4100 likes\n3. https://news.example/a3 — 3900 likes\nUnavailable: https://news.example/gone (404)."},
	}
	sb.model.mu.Unlock()

	s, _ := openTest(t)
	a := mustAssistant(t, s, "e2e-a", "Ada")
	task, _, err := s.CreateTask("e2e-t1", CreateTaskInput{AssistantID: a.ID, Kind: KindResearch, Title: "Digest", Model: "smoke/smoke",
		Brief: "SCENARIO:digest read the fixture page with the skill and list the posts with links"})
	if err != nil {
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
