package agenthooks

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func newTestHome(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

func findRow(t *testing.T, rows []Diagnosis, id ProviderID) Diagnosis {
	t.Helper()
	for _, row := range rows {
		if row.Provider == id {
			return row
		}
	}
	t.Fatalf("no diagnosis row for %s", id)
	return Diagnosis{}
}

func diagnose(t *testing.T, home string) []Diagnosis {
	t.Helper()
	rows := NewManagerWithHome(home).Diagnose()
	if len(rows) == 0 {
		t.Fatal("Diagnose returned no rows")
	}
	return rows
}

func TestDiagnoseFreshHome(t *testing.T) {
	rows := diagnose(t, newTestHome(t))

	claude := findRow(t, rows, ProviderClaude)
	if claude.State != StateNotInstalled {
		t.Fatalf("claude state = %q, want not-installed", claude.State)
	}
	if len(claude.Missing) != len(claudeHookEvents) {
		t.Fatalf("claude missing = %v, want all %d events", claude.Missing, len(claudeHookEvents))
	}
	if !claude.Actionable() || claude.ActionLabel() != "Install" {
		t.Fatalf("claude action = %q, want Install", claude.ActionLabel())
	}

	antigravity := findRow(t, rows, ProviderAntigravity)
	if antigravity.State != StateNotInstalled || len(antigravity.Missing) != len(antigravityHookEvents) {
		t.Fatalf("antigravity = %+v, want not-installed with all events missing", antigravity)
	}

	aider := findRow(t, rows, ProviderAider)
	if aider.State != StateUnsupported || aider.Actionable() {
		t.Fatalf("aider = %+v, want non-actionable unsupported", aider)
	}
}

func TestInstallClaudeEndToEnd(t *testing.T) {
	home := newTestHome(t)
	settingsDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	seed := `{
  "model": "opus",
  "runDepth": 3,
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {"type": "command", "command": "npx block-no-verify@1.1.2"}
        ]
      }
    ],
    "Stop": [
      {"hooks": [{"type": "command", "command": "\"/Applications/Other.app/MacOS/other\" agent-hook claude stop"}]}
    ]
  },
  "nested": {"path": "/tmp/x", "enabled": true, "runDepth": 0}
}`
	if err := os.WriteFile(filepath.Join(settingsDir, "settings.json"), []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewManagerWithHome(home)
	if _, err := m.Install(ProviderClaude); err != nil {
		t.Fatalf("Install claude: %v", err)
	}

	// Script landed executable with the current version marker.
	script := filepath.Join(home, ".solco", "hooks", "solco-claude-hook.sh")
	data, err := os.ReadFile(script)
	if err != nil {
		t.Fatalf("script: %v", err)
	}
	if string(data) != hookScriptTemplate {
		t.Fatal("installed script differs from template")
	}
	info, err := os.Stat(script)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		t.Fatal("script is not executable")
	}

	// Wiring covers exactly the proven event set, appended beside the
	// foreign entries.
	var settings map[string]any
	decode(t, settingsDir+"/settings.json", &settings)
	hooks := settings["hooks"].(map[string]any)
	for _, event := range claudeHookEvents {
		entries := arrayOf(hooks[event])
		if !wiringContains(entries, "solco-claude-hook.sh") {
			t.Errorf("event %s not wired", event)
		}
	}
	// Foreign content survives: the seeded Bash matcher entry and the Stop
	// foreign command are still present exactly once.
	pre := arrayOf(hooks["PreToolUse"])
	if len(pre) != 2 {
		t.Fatalf("PreToolUse entries = %d, want 2 (foreign + ours)", len(pre))
	}
	if !wiringContains(pre, "block-no-verify") {
		t.Fatal("foreign PreToolUse entry lost")
	}
	stop := arrayOf(hooks["Stop"])
	if len(stop) != 2 || !wiringContains(stop, "Other.app") {
		t.Fatalf("Stop entries = %v, want foreign preserved + ours added", stop)
	}
	// Non-hook keys and numeric fidelity survive.
	if settings["model"] != "opus" {
		t.Fatal("top-level key lost")
	}
	nested := settings["nested"].(map[string]any)
	if nested["path"] != "/tmp/x" || nested["enabled"] != true {
		t.Fatalf("nested = %v, want path+enabled preserved", nested)
	}
	if got := numberInt(t, nested["runDepth"]); got != 0 {
		t.Fatalf("nested.runDepth = %v, want integer 0 preserved", got)
	}
	if got := numberInt(t, settings["runDepth"]); got != 3 {
		t.Fatalf("runDepth = %v, want integer 3 preserved", got)
	}

	// Rolling backup exists beside the original.
	if _, err := os.Stat(filepath.Join(settingsDir, "settings.json.solco-hook-backup")); err != nil {
		t.Fatalf("backup: %v", err)
	}

	// Idempotent: a second install changes nothing.
	before := snapshot(t, settingsDir+"/settings.json")
	if _, err := m.Install(ProviderClaude); err != nil {
		t.Fatalf("second Install: %v", err)
	}
	if after := snapshot(t, settingsDir+"/settings.json"); after != before {
		t.Fatal("second Install rewrote wiring")
	}

	row := findRow(t, diagnose(t, home), ProviderClaude)
	if row.State != StateOK || row.Version != hookScriptVersion || len(row.Missing) != 0 {
		t.Fatalf("post-install row = %+v, want ok v%d", row, hookScriptVersion)
	}
}

func TestInstallAntigravityPreservesForeignGroups(t *testing.T) {
	home := newTestHome(t)
	geminiDir := filepath.Join(home, ".gemini", "config")
	if err := os.MkdirAll(geminiDir, 0o755); err != nil {
		t.Fatal(err)
	}
	seed := `{
  "herdr": {
    "PreInvocation": [
      {"command": "bash '/x/herdr-agent-state.sh' session", "timeout": 10, "type": "command"}
    ]
  },
  "orca-status": {
    "PostToolUse": [
      {"matcher": "*", "hooks": [{"command": "/x/orca.sh", "type": "command"}]}
    ]
  }
}`
	path := filepath.Join(geminiDir, "hooks.json")
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewManagerWithHome(home)
	if _, err := m.Install(ProviderAntigravity); err != nil {
		t.Fatalf("Install antigravity: %v", err)
	}

	var config map[string]any
	decode(t, path, &config)
	group := config["solco-hook"].(map[string]any)
	if group["enabled"] != true {
		t.Fatal("solco-hook group not enabled")
	}
	for _, event := range antigravityHookEvents {
		if !wiringContains(arrayOf(group[event]), "solco-antigravity-hook.sh") {
			t.Errorf("event %s not wired", event)
		}
	}
	// Foreign groups survive untouched.
	herdr := config["herdr"].(map[string]any)
	if !wiringContains(arrayOf(herdr["PreInvocation"]), "herdr-agent-state.sh") {
		t.Fatal("herdr group lost")
	}
	if _, ok := config["orca-status"]; !ok {
		t.Fatal("orca-status group lost")
	}

	// Idempotent.
	before := snapshot(t, path)
	if _, err := m.Install(ProviderAntigravity); err != nil {
		t.Fatalf("second Install: %v", err)
	}
	if after := snapshot(t, path); after != before {
		t.Fatal("second Install rewired antigravity hooks")
	}

	row := findRow(t, diagnose(t, home), ProviderAntigravity)
	if row.State != StateOK || len(row.Missing) != 0 {
		t.Fatalf("post-install row = %+v, want ok", row)
	}
}

func TestInstallCreatesMissingProviderConfigs(t *testing.T) {
	home := newTestHome(t)
	m := NewManagerWithHome(home)
	for _, id := range []ProviderID{ProviderClaude, ProviderAntigravity} {
		if _, err := m.Install(id); err != nil {
			t.Fatalf("Install %s: %v", id, err)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); err != nil {
		t.Fatalf("claude settings not created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".gemini", "config", "hooks.json")); err != nil {
		t.Fatalf("antigravity hooks.json not created: %v", err)
	}
}

func TestInstallRefusesForeignScript(t *testing.T) {
	home := newTestHome(t)
	hooksDir := filepath.Join(home, ".solco", "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	foreign := "#!/bin/sh\n# mine, not solco\nexit 0\n"
	script := filepath.Join(hooksDir, "solco-claude-hook.sh")
	if err := os.WriteFile(script, []byte(foreign), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := NewManagerWithHome(home).Install(ProviderClaude); err == nil {
		t.Fatal("Install overwrote a foreign script without error")
	}
	if data, _ := os.ReadFile(script); string(data) != foreign {
		t.Fatal("foreign script content changed")
	}
}

func TestStaleScriptDiagnosedAndUpdated(t *testing.T) {
	home := newTestHome(t)
	m := NewManagerWithHome(home)
	// Start from a fully provisioned state, then downgrade only the script
	// to the previous version: isolates staleness from missing wiring.
	if _, err := m.Install(ProviderClaude); err != nil {
		t.Fatalf("initial Install: %v", err)
	}
	script := filepath.Join(home, ".solco", "hooks", "solco-claude-hook.sh")
	stale := strings.Replace(hookScriptTemplate,
		"# SOLCO_HOOK_VERSION="+itoa(hookScriptVersion),
		"# SOLCO_HOOK_VERSION="+itoa(hookScriptVersion-1), 1)
	if err := os.WriteFile(script, []byte(stale), 0o755); err != nil {
		t.Fatal(err)
	}

	row := findRow(t, diagnose(t, home), ProviderClaude)
	if row.State != StateOutdated || row.ActionLabel() != "Update" || row.Version != hookScriptVersion-1 {
		t.Fatalf("row = %+v, want outdated v%d with Update action", row, hookScriptVersion-1)
	}

	if _, err := m.Install(ProviderClaude); err != nil {
		t.Fatalf("Install: %v", err)
	}
	data, _ := os.ReadFile(script)
	if string(data) != hookScriptTemplate {
		t.Fatal("stale script not updated to template")
	}
}

// TestHookScriptRelayParityWithHerdr pins the session-identity takeover:
// the embedded script relays pane.report_agent_session to Herdr's socket
// with the same optional fields herdr's own scripts send, so herdr's
// official hook group can stay a pure fallback lane.
func TestHookScriptRelayParityWithHerdr(t *testing.T) {
	for _, fragment := range []string{
		"pane.report_agent_session",
		"agent_session_path",
		"transcript_path",
		"transcriptPath",
		"session_start_source",
		"# SOLCO_HOOK_VERSION=" + itoa(hookScriptVersion),
	} {
		if !strings.Contains(hookScriptTemplate, fragment) {
			t.Fatalf("hook script template missing %q", fragment)
		}
	}
}

func TestWiringDetectionAcrossShapes(t *testing.T) {
	nested := []any{map[string]any{
		"matcher": "*",
		"hooks":   []any{map[string]any{"type": "command", "command": "bash '/h/solco-claude-hook.sh' claude"}},
	}}
	flat := []any{map[string]any{"type": "command", "command": "bash '/h/solco-antigravity-hook.sh' agy Stop"}}
	if !wiringContains(nested, "solco-claude-hook.sh") {
		t.Fatal("nested shape not detected")
	}
	if !wiringContains(flat, "solco-antigravity-hook.sh") {
		t.Fatal("flat shape not detected")
	}
	if wiringContains(flat, "solco-claude-hook.sh") {
		t.Fatal("wrong script matched")
	}
}

func decode(t *testing.T, path string, into *map[string]any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.UseNumber()
	if err := dec.Decode(into); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
}

func snapshot(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("snapshot %s: %v", path, err)
	}
	return string(data)
}

func numberInt(t *testing.T, value any) int64 {
	t.Helper()
	n, ok := value.(json.Number)
	if !ok {
		t.Fatalf("value %v (%T) is not a json.Number", value, value)
	}
	out, err := n.Int64()
	if err != nil {
		t.Fatalf("number %s: %v", n, err)
	}
	return out
}

func itoa(v int) string {
	return strconv.Itoa(v)
}

func TestInstallCursorPreservesForeignHooksAndKeepsVersion(t *testing.T) {
	home := newTestHome(t)
	dir := filepath.Join(home, ".cursor")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	seed := `{
  "version": 1,
  "hooks": {
    "preToolUse": [{"command": "rtk hook cursor", "matcher": "Shell"}],
    "sessionStart": [{"command": "bash '/x/herdr-agent-state.sh' session"}]
  }
}`
	path := filepath.Join(dir, "hooks.json")
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewManagerWithHome(home)
	if row := findRow(t, m.Diagnose(), ProviderCursor); row.State != StateNotInstalled || len(row.Missing) != len(cursorHookEvents) {
		t.Fatalf("fresh cursor row = %+v, want not-installed with every event missing", row)
	}
	if _, err := m.Install(ProviderCursor); err != nil {
		t.Fatalf("Install cursor: %v", err)
	}

	var config map[string]any
	decode(t, path, &config)
	if numberInt(t, config["version"]) != 1 {
		t.Fatalf("version = %v, want 1 kept", config["version"])
	}
	hooks := config["hooks"].(map[string]any)
	for _, event := range cursorHookEvents {
		if !wiringContains(arrayOf(hooks[event]), ".sh") {
			t.Errorf("event %s not wired", event)
		}
	}
	if pre := arrayOf(hooks["preToolUse"]); len(pre) != 2 || !wiringContains(pre, "rtk hook cursor") {
		t.Fatalf("preToolUse = %v, want the foreign hook kept beside ours", pre)
	}
	if start := arrayOf(hooks["sessionStart"]); len(start) != 2 || !wiringContains(start, "herdr-agent-state.sh") {
		t.Fatalf("sessionStart = %v, want Herdr's hook kept beside ours", start)
	}

	before := snapshot(t, path)
	if _, err := m.Install(ProviderCursor); err != nil {
		t.Fatalf("second Install: %v", err)
	}
	if snapshot(t, path) != before {
		t.Fatal("second Install rewrote the wiring")
	}
	if row := findRow(t, m.Diagnose(), ProviderCursor); row.State != StateOK || len(row.Missing) != 0 {
		t.Fatalf("post-install row = %+v, want ok", row)
	}
}

// Cursor's camelCase events and conversation_id reach the apphook socket as a
// status plus a session id (the script is what the doctor installs, run here
// against a stand-in socket).
func TestCursorHookScriptReportsStatusAndSession(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	dir, err := os.MkdirTemp("/tmp", "solco-hook")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s.sock")
	listener, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	script := filepath.Join(dir, "hook.sh")
	if err := os.WriteFile(script, []byte(hookScriptTemplate), 0o755); err != nil {
		t.Fatal(err)
	}

	cases := []struct{ event, status string }{
		{"sessionStart", "idle"}, {"beforeSubmitPrompt", "working"}, {"preToolUse", "working"},
		{"postToolUse", "working"}, {"stop", "idle"}, {"sessionEnd", "released"},
	}
	for _, tc := range cases {
		got := make(chan map[string]any, 1)
		go func() {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
			var payload map[string]any
			_ = json.NewDecoder(conn).Decode(&payload)
			got <- payload
		}()
		cmd := exec.Command("sh", script, "cursor")
		cmd.Stdin = strings.NewReader(`{"hook_event_name":"` + tc.event + `","conversation_id":"chat-1"}`)
		cmd.Env = append(os.Environ(), "HERDR_PANE_ID=p1", "SOLCO_SOCKET_PATH="+sock, "HERDR_ENV=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", tc.event, err, out)
		}
		select {
		case payload := <-got:
			if payload["agent"] != "cursor" || payload["status"] != tc.status || payload["session_id"] != "chat-1" {
				t.Fatalf("%s: payload = %v, want cursor/%s/chat-1", tc.event, payload, tc.status)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: nothing reached the socket", tc.event)
		}
	}
}

func TestScriptAntigravityEventsSocketReport(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	dir, err := os.MkdirTemp("/tmp", "solco-hook")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s.sock")
	listener, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	script := filepath.Join(dir, "hook.sh")
	if err := os.WriteFile(script, []byte(hookScriptTemplate), 0o755); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		event      string
		payload    string
		wantStatus string
		wantStdout string
	}{
		{
			event:      "SessionStart",
			payload:    `{"conversationId":"agy-1"}`,
			wantStatus: "idle",
			wantStdout: "{}\n",
		},
		{
			event:      "PreInvocation",
			payload:    `{"conversationId":"agy-1","lastUserInput":"Fix the sidebar"}`,
			wantStatus: "working",
			wantStdout: "{}\n",
		},
		{
			event:      "PreToolUse",
			payload:    `{"conversationId":"agy-1","toolCall":{"name":"run_command"}}`,
			wantStatus: "working",
			wantStdout: "{\"decision\":\"allow\"}\n",
		},
		{
			event:      "PostToolUse",
			payload:    `{"conversationId":"agy-1","toolCall":{"name":"run_command"}}`,
			wantStatus: "working",
			wantStdout: "{}\n",
		},
		{
			event:      "Stop",
			payload:    `{"conversationId":"agy-1","fullyIdle":true}`,
			wantStatus: "idle",
			wantStdout: "{}\n",
		},
	}

	for _, tc := range cases {
		got := make(chan map[string]any, 1)
		go func() {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
			var p map[string]any
			_ = json.NewDecoder(conn).Decode(&p)
			got <- p
		}()
		cmd := exec.Command("sh", script, "agy", tc.event)
		cmd.Stdin = strings.NewReader(tc.payload)
		cmd.Env = append(os.Environ(), "HERDR_PANE_ID=p1", "SOLCO_SOCKET_PATH="+sock, "HERDR_ENV=0")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", tc.event, err, out)
		}
		if string(out) != tc.wantStdout {
			t.Fatalf("%s: stdout = %q, want %q", tc.event, string(out), tc.wantStdout)
		}
		select {
		case payload := <-got:
			if payload["agent"] != "agy" || payload["status"] != tc.wantStatus || payload["session_id"] != "agy-1" {
				t.Fatalf("%s: payload = %v, want agy/%s/agy-1", tc.event, payload, tc.wantStatus)
			}
			if tc.event == "PreInvocation" && payload["text"] != "Fix the sidebar" {
				t.Fatalf("%s: text = %v, want 'Fix the sidebar'", tc.event, payload["text"])
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: nothing reached the socket", tc.event)
		}
	}
}

func TestInstallCodexNeedsApprovalUntilCodexTrustsEachHook(t *testing.T) {
	home := newTestHome(t)
	dir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	seed := `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"command":"rtk hook codex","type":"command"}]}],` +
		`"SessionStart":[{"hooks":[{"command":"bash '/x/herdr-agent-state.sh' session","timeout":10,"type":"command"}]}]}}`
	path := filepath.Join(dir, "hooks.json")
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(configPath, []byte("[features]\nhooks = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewManagerWithHome(home)
	if _, err := m.Install(ProviderCodex); err != nil {
		t.Fatalf("Install codex: %v", err)
	}
	var config map[string]any
	decode(t, path, &config)
	hooks := config["hooks"].(map[string]any)
	for _, event := range codexHookEvents {
		if !wiringContains(arrayOf(hooks[event]), "solco-codex-hook.sh") {
			t.Errorf("event %s not wired", event)
		}
	}
	if pre := arrayOf(hooks["PreToolUse"]); len(pre) != 2 || !wiringContains(pre, "rtk hook codex") {
		t.Fatalf("PreToolUse = %v, want the foreign hook kept beside ours", pre)
	}
	// Our entry sits after the foreign one in PreToolUse (group 1) and after
	// Herdr's in SessionStart (group 1); the other events start at group 0.
	row := findRow(t, m.Diagnose(), ProviderCodex)
	if row.State != StateNeedsApproval || row.Actionable() {
		t.Fatalf("untrusted row = %+v, want needs-approval with no Solco action", row)
	}

	// Codex's own trust records, written when the user approves in /hooks.
	trust := ""
	for _, key := range []string{
		path + ":session_start:1:0", path + ":pre_tool_use:1:0", path + ":post_tool_use:0:0",
		path + ":permission_request:0:0", path + ":stop:0:0",
	} {
		trust += "[hooks.state.\"" + key + "\"]\ntrusted_hash = \"sha256:x\"\n\n"
	}
	// One hook still missing its record keeps the row waiting.
	partial := strings.Replace(trust, "[hooks.state.\""+path+":stop:0:0\"]\ntrusted_hash = \"sha256:x\"\n\n", "", 1)
	if err := os.WriteFile(configPath, []byte("[features]\nhooks = true\n\n"+partial), 0o644); err != nil {
		t.Fatal(err)
	}
	if row := findRow(t, m.Diagnose(), ProviderCodex); row.State != StateNeedsApproval || !strings.Contains(row.Detail, "Stop") {
		t.Fatalf("partial trust row = %+v, want needs-approval naming Stop", row)
	}
	if err := os.WriteFile(configPath, []byte("[features]\nhooks = true\n\n"+trust), 0o644); err != nil {
		t.Fatal(err)
	}
	if row := findRow(t, m.Diagnose(), ProviderCodex); row.State != StateOK {
		t.Fatalf("trusted row = %+v, want ok", row)
	}
	// config.toml is Codex's: installing never rewrote it.
	if got, _ := os.ReadFile(configPath); !strings.HasSuffix(string(got), trust) {
		t.Fatal("config.toml changed")
	}
}

func TestCodexHookScriptIgnoresAnInheritedParentThread(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	dir, err := os.MkdirTemp("/tmp", "solco-hook")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s.sock")
	listener, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	script := filepath.Join(dir, "hook.sh")
	if err := os.WriteFile(script, []byte(hookScriptTemplate), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(thread string) bool {
		got := make(chan struct{}, 1)
		go func() {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
			got <- struct{}{}
		}()
		cmd := exec.Command("sh", script, "codex")
		cmd.Stdin = strings.NewReader(`{"hook_event_name":"PreToolUse","session_id":"child"}`)
		cmd.Env = append(os.Environ(), "HERDR_PANE_ID=p1", "SOLCO_SOCKET_PATH="+sock, "HERDR_ENV=0", "CODEX_THREAD_ID="+thread)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		select {
		case <-got:
			return true
		case <-time.After(1500 * time.Millisecond):
			// unblock the pending Accept for the next run
			if c, err := net.Dial("unix", sock); err == nil {
				c.Close()
			}
			return false
		}
	}
	if run("parent") {
		t.Fatal("an event from a child codex inheriting its parent's thread id must not report")
	}
	if !run("child") {
		t.Fatal("an event whose session is the pane's own thread must report")
	}
}

// Cursor also runs ~/.claude/settings.json hooks with its own payload; the
// Claude copy must stay silent there (Cursor's own copy reports instead).
func TestClaudeHookScriptIgnoresCursorPayloads(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	dir, err := os.MkdirTemp("/tmp", "solco-hook")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s.sock")
	listener, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	script := filepath.Join(dir, "hook.sh")
	if err := os.WriteFile(script, []byte(hookScriptTemplate), 0o755); err != nil {
		t.Fatal(err)
	}
	reports := func(payload string) bool {
		got := make(chan struct{}, 1)
		go func() {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
			got <- struct{}{}
		}()
		cmd := exec.Command("sh", script, "claude")
		cmd.Stdin = strings.NewReader(payload)
		cmd.Env = append(os.Environ(), "HERDR_PANE_ID=p1", "SOLCO_SOCKET_PATH="+sock, "HERDR_ENV=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		select {
		case <-got:
			return true
		case <-time.After(1500 * time.Millisecond):
			if c, err := net.Dial("unix", sock); err == nil {
				c.Close()
			}
			return false
		}
	}
	if reports(`{"hook_event_name":"stop","session_id":"s","cursor_version":"2026.10.22"}`) {
		t.Fatal("Claude's hook reported for a Cursor payload")
	}
	if !reports(`{"hook_event_name":"Stop","session_id":"s"}`) {
		t.Fatal("Claude's hook stayed silent for a real Claude payload")
	}
}

func TestApplyLiveAntigravityHook(t *testing.T) {
	if os.Getenv("APPLY_LIVE_HOOK") != "1" {
		t.Skip("skipping live hook application; set APPLY_LIVE_HOOK=1 to apply")
	}
	m := NewManager()
	if _, err := m.Install(ProviderAntigravity); err != nil {
		t.Fatalf("Install Antigravity failed: %v", err)
	}
}

// TestUpgradeScriptsReplacesOnlyOlderScriptsOfOurs pins the silent upgrade:
// an older script of ours is rewritten in place, while a current one, a
// missing one and a foreign file are left exactly as they are, and the
// provider's own config is never touched.
func TestUpgradeScriptsReplacesOnlyOlderScriptsOfOurs(t *testing.T) {
	home := newTestHome(t)
	m := NewManagerWithHome(home)
	if _, err := m.Install(ProviderClaude); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Install(ProviderCursor); err != nil {
		t.Fatal(err)
	}
	old := strings.Replace(hookScriptTemplate, "SOLCO_HOOK_VERSION="+strconv.Itoa(hookScriptVersion), "SOLCO_HOOK_VERSION=1", 1)
	if old == hookScriptTemplate {
		t.Fatal("template carries no version marker to age")
	}
	if err := os.WriteFile(m.scriptPath(ProviderClaude), []byte(old), 0o755); err != nil {
		t.Fatal(err)
	}
	foreign := []byte("#!/bin/sh\necho mine\n")
	if err := os.WriteFile(m.scriptPath(ProviderAntigravity), foreign, 0o755); err != nil {
		t.Fatal(err)
	}
	cursorConfig, err := os.ReadFile(m.cursorHooksPath())
	if err != nil {
		t.Fatal(err)
	}

	got := m.UpgradeScripts()
	if len(got) != 1 || got[0] != ProviderClaude {
		t.Fatalf("upgraded = %v, want only claude", got)
	}
	if _, version, _ := readOwnedScript(m.scriptPath(ProviderClaude)); version != hookScriptVersion {
		t.Fatalf("claude script version = %d, want %d", version, hookScriptVersion)
	}
	if after, _ := os.ReadFile(m.scriptPath(ProviderAntigravity)); string(after) != string(foreign) {
		t.Fatal("a foreign script was rewritten")
	}
	if after, _ := os.ReadFile(m.cursorHooksPath()); string(after) != string(cursorConfig) {
		t.Fatal("the provider's own config changed")
	}
	if again := m.UpgradeScripts(); len(again) != 0 {
		t.Fatalf("second pass upgraded %v", again)
	}
}

func TestTrustWorktreesAddsOneKeyAndKeepsTheRestByteForByte(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".claude.json")
	orig := "{\n  \"numStartups\": 3,\n  \"projects\": {\n    \"/a\": {\"hasTrustDialogAccepted\": true}\n  },\n  \"tail\": \"é&<>\"\n}\n"
	if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewManagerWithHome(home)
	changed, err := m.TrustWorktrees()
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	got, _ := os.ReadFile(path)
	var parsed struct {
		Projects map[string]struct {
			Trusted bool `json:"hasTrustDialogAccepted"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(got, &parsed); err != nil {
		t.Fatalf("result is not valid JSON: %v\n%s", err, got)
	}
	if !parsed.Projects[filepath.Join(home, ".herdr", "worktrees")].Trusted || !parsed.Projects["/a"].Trusted {
		t.Errorf("want the new record and the old one, got %+v", parsed.Projects)
	}
	// Everything but the spliced entry is the original text.
	if !strings.Contains(string(got), "\"tail\": \"é&<>\"\n}\n") || !strings.HasPrefix(string(got), "{\n  \"numStartups\": 3,\n  \"projects\": {") {
		t.Errorf("the rest of the file moved:\n%s", got)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("mode changed to %v", info.Mode().Perm())
	}
	if again, err := m.TrustWorktrees(); err != nil || again {
		t.Errorf("a second call must be a no-op, changed=%v err=%v", again, err)
	}
}

func TestTrustWorktreesNeverOverridesAnExistingRecord(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".claude.json")
	dir := filepath.Join(home, ".herdr", "worktrees")
	orig := fmt.Sprintf("{\"projects\":{%q:{\"hasTrustDialogAccepted\":false}}}", dir)
	_ = os.WriteFile(path, []byte(orig), 0o600)
	if changed, err := NewManagerWithHome(home).TrustWorktrees(); err != nil || changed {
		t.Fatalf("an explicit no stays: changed=%v err=%v", changed, err)
	}
	if got, _ := os.ReadFile(path); string(got) != orig {
		t.Errorf("file rewritten: %s", got)
	}
}

func TestTrustWorktreesHandlesEmptyProjectsAndAbsentFile(t *testing.T) {
	home := t.TempDir()
	m := NewManagerWithHome(home)
	if changed, err := m.TrustWorktrees(); err != nil || changed {
		t.Fatalf("no file: changed=%v err=%v", changed, err)
	}
	path := filepath.Join(home, ".claude.json")
	_ = os.WriteFile(path, []byte(`{"projects":{}}`), 0o600)
	if changed, err := m.TrustWorktrees(); err != nil || !changed {
		t.Fatalf("empty projects: changed=%v err=%v", changed, err)
	}
	got, _ := os.ReadFile(path)
	if !json.Valid(got) {
		t.Fatalf("invalid JSON: %s", got)
	}
}
