package remotehost

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/thewei/solco/internal/personalagent"
)

/**
 * [INPUT]: server_test.go 的 harness、真实的 plugins/solco-automations 二进制（测试内 go build）与一次性 SQLite
 * [OUTPUT]: 对外提供 /api/v3/personal-agent 的真核心回归：建助手→草拟 Goal→批准→暂停→过期修订 409→概览/助手页/任务；SOLCO_PA_DUMP=<dir> 时把真实响应落盘供手机 Zod 逐字解析
 * [POS]: personal_agent_test.go 的 fakeCore 只证明 Host 的转发；本文件证明整条线（Host→CLI→SQLite）与 golden 描述的是同一个形状
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */
func TestPersonalAgentAgainstTheRealCore(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the plugin")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-o", filepath.Join(root, "bin", "solco-automations"), ".")
	build.Dir = "../../plugins/solco-automations"
	build.Env = append(os.Environ(), "GOTOOLCHAIN=auto")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	client := personalagent.NewClient(root, []string{"SOLCO_ASSISTANTS_DB=" + filepath.Join(t.TempDir(), "a.sqlite")})
	h := newHarness(t)
	h.srv.opts.PersonalAgent = func(context.Context) (*personalagent.Client, bool) { return client, true }
	dump := os.Getenv("SOLCO_PA_DUMP")
	save := func(name string, raw []byte) {
		if dump == "" {
			return
		}
		if err := os.MkdirAll(dump, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dump, name), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	post := func(path, body string, want int) []byte {
		t.Helper()
		status, raw := h.send("POST", "/api/v3/personal-agent"+path, h.token, body)
		if status != want {
			t.Fatalf("POST %s: status %d, want %d: %s", path, status, want, raw)
		}
		return raw
	}
	get := func(path string) []byte {
		t.Helper()
		resp := h.get("/api/v3/personal-agent"+path, h.token, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: status %d", path, resp.StatusCode)
		}
		return bodyOf(t, resp)
	}
	var created struct {
		Assistant personalagent.Assistant `json:"assistant"`
	}
	raw := post("/assistants", `{"requestId":"real-1","name":"Ada","description":"Follows PRs"}`, 200)
	save("assistant-created.json", raw)
	if err := json.Unmarshal(raw, &created); err != nil || created.Assistant.ID == "" {
		t.Fatalf("created %v %s", err, raw)
	}
	// the same requestId replays the first receipt, a different body under it is a conflict
	if replay := post("/assistants", `{"requestId":"real-1","name":"Ada","description":"Follows PRs"}`, 200); len(replay) == 0 {
		t.Fatal("replay answered nothing")
	}
	post("/assistants", `{"requestId":"real-1","name":"Someone else"}`, http.StatusConflict)

	var goal struct {
		Goal personalagent.Goal `json:"goal"`
	}
	raw = post("/goals/draft", `{"requestId":"real-2","assistantId":"`+created.Assistant.ID+`","title":"Watch o/r#12","objective":"Tell me when it changes","commitments":[{"kind":"pr_watch","spec":"{\"repo\":\"o/r\",\"number\":12}","exitCondition":"merged or closed"}]}`, 200)
	save("goal-draft.json", raw)
	if err := json.Unmarshal(raw, &goal); err != nil || goal.Goal.State != "draft" {
		t.Fatalf("draft %v %s", err, raw)
	}
	post("/goals/"+goal.Goal.ID+"/pause", `{"requestId":"real-3","expectedRevision":`+itoa(goal.Goal.Revision)+`}`, http.StatusConflict) // a draft cannot be paused
	raw = post("/goals/"+goal.Goal.ID+"/approve", `{"requestId":"real-4","expectedRevision":`+itoa(goal.Goal.Revision)+`}`, 200)
	if err := json.Unmarshal(raw, &goal); err != nil || goal.Goal.State != "active" {
		t.Fatalf("approve %v %s", err, raw)
	}
	post("/goals/"+goal.Goal.ID+"/pause", `{"requestId":"real-5","expectedRevision":1}`, http.StatusConflict) // stale
	raw = post("/assistants/"+created.Assistant.ID+"/rename", `{"requestId":"real-6","name":"Grace","expectedRevision":`+itoa(created.Assistant.Revision)+`}`, 200)
	save("assistant-renamed.json", raw)

	save("overview.json", get("/overview"))
	save("assistants.json", get("/assistants"))
	save("assistant.json", get("/assistants/"+created.Assistant.ID))
	save("goals.json", get("/goals?assistant="+created.Assistant.ID))
	save("approvals.json", get("/approvals"))

	var page PersonalAssistantDetail
	if err := json.Unmarshal(get("/assistants/"+created.Assistant.ID), &page); err != nil {
		t.Fatal(err)
	}
	if page.Assistant.Name != "Grace" || len(page.Goals) != 1 || page.Goals[0].State != "active" || page.Tasks == nil || page.Approvals == nil || page.Memory == nil {
		t.Fatalf("page %+v", page)
	}
	if resp := h.get("/api/v3/personal-agent/assistants/a_ffffffffffffffff", h.token, nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown assistant: %d", resp.StatusCode)
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
