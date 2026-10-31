package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func errorsNew(message string) error { return errors.New(message) }

// fakeHerdrServer speaks the verified newline-JSON envelope over a unix
// socket placed where SocketPath resolves, so the launch transport is
// contract-tested against the shapes pinned from `herdr api schema`.
type fakeHerdrServer struct {
	mu       sync.Mutex
	requests []fakeRequest
	handler  func(method string, params map[string]any) (json.RawMessage, error)
	close    func()
}

type fakeRequest struct {
	Method string         `json:"method"`
	Params map[string]any `json:"params"`
}

func startFakeHerdr(t *testing.T, handler func(method string, params map[string]any) (json.RawMessage, error)) *fakeHerdrServer {
	t.Helper()
	// macOS unix socket paths are limited to 104 bytes: keep the socket under
	// a short root while HOME still points at the fake config tree.
	shortRoot, err := os.MkdirTemp("/tmp", "shfake")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(shortRoot) })
	home := filepath.Join(shortRoot, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	socketDir := filepath.Join(home, ".config", "herdr")
	if err := os.MkdirAll(socketDir, 0o755); err != nil {
		t.Fatal(err)
	}
	socketPath := filepath.Join(socketDir, "herdr.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	server := &fakeHerdrServer{handler: handler, close: func() { listener.Close() }}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go server.serve(conn)
		}
	}()
	t.Cleanup(server.close)
	return server
}

func (s *fakeHerdrServer) serve(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return
		}
		var generic map[string]any
		if err := json.Unmarshal(line, &generic); err != nil {
			continue
		}
		method, _ := generic["method"].(string)
		params, _ := generic["params"].(map[string]any)
		s.mu.Lock()
		s.requests = append(s.requests, fakeRequest{Method: method, Params: params})
		s.mu.Unlock()

		if method == "ping" {
			respond(conn, map[string]any{"result": map[string]any{"type": "pong", "protocol": 22, "version": "fake"}})
			continue
		}
		result, handlerErr := s.handler(method, params)
		if handlerErr != nil {
			respond(conn, map[string]any{"error": map[string]string{"code": "runtime", "message": handlerErr.Error()}})
			continue
		}
		respond(conn, map[string]any{"result": result})
	}
}

func respond(conn net.Conn, envelope map[string]any) {
	data, err := json.Marshal(envelope)
	if err != nil {
		return
	}
	conn.Write(append(data, '\n'))
}

func (s *fakeHerdrServer) recorded() []fakeRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]fakeRequest(nil), s.requests...)
}

func TestLaunchTransportCreateTabWithoutFocus(t *testing.T) {
	server := startFakeHerdr(t, func(method string, params map[string]any) (json.RawMessage, error) {
		if method != "tab.create" {
			return nil, errorsUnexpected(method)
		}
		if focus, ok := params["focus"].(bool); !ok || focus {
			return nil, errorsNew("tab.create must never carry focus=true")
		}
		return json.RawMessage(`{"type":"tab_created","tab":{"tab_id":"tab-9","workspace_id":"w1"},"root_pane":{"pane_id":"pane-9"}}`), nil
	})
	manager := NewManager()
	tabID, paneID, err := manager.CreateTabWithoutFocus(context.Background(), "default", "w1", "/work/demo")
	if err != nil {
		t.Fatal(err)
	}
	if tabID != "tab-9" || paneID != "pane-9" {
		t.Fatalf("created = %q/%q", tabID, paneID)
	}
	for _, request := range server.recorded() {
		if request.Method == "tab.create" && request.Params["workspace_id"] != "w1" {
			t.Fatalf("params = %+v", request.Params)
		}
	}
}

func errorsUnexpected(method string) error { return errorsNew("unexpected method " + method) }

// The schema types agent.start args as an array: a launch without extra
// arguments must send [] rather than null.
func TestLaunchTransportStartAgentSendsArgsArray(t *testing.T) {
	server := startFakeHerdr(t, func(method string, params map[string]any) (json.RawMessage, error) {
		return json.RawMessage(`{"type":"agent_started","agent":{"pane_id":"pane-9"},"argv":["pi"]}`), nil
	})
	if _, err := NewManager().StartAgent(context.Background(), "default", AgentStartParams{
		Name: "pi-op1", Kind: "pi", PaneID: "pane-9",
	}); err != nil {
		t.Fatal(err)
	}
	for _, request := range server.recorded() {
		if request.Method != "agent.start" {
			continue
		}
		if args, ok := request.Params["args"].([]any); !ok || len(args) != 0 {
			t.Fatalf("args = %#v, want an empty array", request.Params["args"])
		}
		return
	}
	t.Fatal("no agent.start request recorded")
}

func TestLaunchTransportStartPromptWaitGet(t *testing.T) {
	server := startFakeHerdr(t, func(method string, params map[string]any) (json.RawMessage, error) {
		switch method {
		case "agent.start":
			return json.RawMessage(`{"type":"agent_started","agent":{"pane_id":"pane-9","tab_id":"tab-9","workspace_id":"w1","agent_status":"launch_pending","interactive_ready":false,"revision":3},"argv":["claude","--permission-mode","default"]}`), nil
		case "agent.prompt":
			return json.RawMessage(`{"type":"agent_prompted","agent":{"pane_id":"pane-9","agent_status":"working","interactive_ready":true,"revision":4}}`), nil
		case "agent.wait":
			// The shape a live Herdr 0.9.3 answers a matched agent.wait with.
			return json.RawMessage(`{"type":"agent_info","agent":{"pane_id":"pane-9","agent_status":"idle","interactive_ready":true,"revision":5}}`), nil
		case "agent.get":
			return json.RawMessage(`{"type":"agent_info","agent":{"pane_id":"pane-9","agent_status":"idle","interactive_ready":true,"revision":5,"agent":"claude-code","agent_session":{"agent":"claude-code","kind":"native","source":"provider","value":"sess-1"}}}`), nil
		default:
			return nil, errorsUnexpected(method)
		}
	})
	manager := NewManager()
	ctx := context.Background()

	started, err := manager.StartAgent(ctx, "default", AgentStartParams{
		Name: "claude-code-op1", Kind: "claude-code", PaneID: "pane-9",
		Args: []string{"--permission-mode", "default"}, TimeoutMS: 30000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if started.AgentInfo.PaneID != "pane-9" || started.AgentInfo.AgentStatus != "launch_pending" {
		t.Fatalf("started = %+v", started.AgentInfo)
	}
	if len(started.Argv) != 3 {
		t.Fatalf("argv = %v", started.Argv)
	}

	if err := manager.PromptAgent(ctx, "default", "pane-9", "do the thing"); err != nil {
		t.Fatal(err)
	}
	if err := manager.WaitAgentState(ctx, "default", "pane-9", []string{"idle"}, 60000); err != nil {
		t.Fatal(err)
	}
	agent, err := manager.AgentByPane(ctx, "default", "pane-9")
	if err != nil {
		t.Fatal(err)
	}
	if agent == nil || !agent.InteractiveReady || agent.AgentStatus != "idle" || agent.Revision != 5 {
		t.Fatalf("agent = %+v", agent)
	}
	if agent.AgentSession == nil || agent.AgentSession.Value != "sess-1" {
		t.Fatalf("session = %+v", agent.AgentSession)
	}

	// Every method was exercised through the verified envelopes.
	methods := map[string]bool{}
	for _, request := range server.recorded() {
		methods[request.Method] = true
	}
	for _, want := range []string{"agent.start", "agent.prompt", "agent.wait", "agent.get"} {
		if !methods[want] {
			t.Fatalf("method %s never called", want)
		}
	}
}

func TestLaunchTransportShellReady(t *testing.T) {
	// Observations in order. The idle shell lists itself as its own
	// foreground process — the live protocol-22 shape; a foreground process
	// beyond the shell, or no shell at all, reads as not ready.
	responses := []string{
		`{"type":"pane_process_info","process_info":{"pane_id":"pane-9","shell_pid":401,"foreground_processes":[{"pid":512,"name":"claude"}]}}`,
		`{"type":"pane_process_info","process_info":{"pane_id":"pane-9","shell_pid":401,"foreground_processes":[{"pid":401,"name":"zsh"},{"pid":512,"name":"claude"}]}}`,
		`{"type":"pane_process_info","process_info":{"pane_id":"pane-9","shell_pid":null,"foreground_processes":[]}}`,
		`{"type":"pane_process_info","process_info":{"pane_id":"pane-9","shell_pid":401,"foreground_processes":[{"pid":401,"name":"zsh"}]}}`,
	}
	wantReady := []bool{false, false, false, true}
	observations := 0
	startFakeHerdr(t, func(method string, params map[string]any) (json.RawMessage, error) {
		if method != "pane.process_info" {
			return nil, errorsUnexpected(method)
		}
		response := responses[observations]
		observations++
		return json.RawMessage(response), nil
	})
	manager := NewManager()

	for i, want := range wantReady {
		got, err := manager.ShellReady(context.Background(), "default", "pane-9")
		if err != nil || got != want {
			t.Fatalf("observation %d: ShellReady = (%v, %v), want %v", i, got, err, want)
		}
	}
}

func TestLaunchTransportShellSettled(t *testing.T) {
	idle := `{"type":"pane_process_info","process_info":{"pane_id":"p","shell_pid":401,"foreground_processes":[{"pid":401,"name":"zsh"}]}}`
	busy := `{"type":"pane_process_info","process_info":{"pane_id":"p","shell_pid":401,"foreground_processes":[{"pid":512,"name":"bun"}]}}`
	pane := func(agent string) string {
		if agent == "" {
			return `{"type":"pane_info","pane":{"pane_id":"p","agent_status":"unknown"}}`
		}
		return `{"type":"pane_info","pane":{"pane_id":"p","agent":"` + agent + `","agent_status":"idle"}}`
	}
	read := func(text string) string {
		return `{"type":"pane_read","read":{"text":` + string(mustJSON(text)) + `}}`
	}
	cases := []struct {
		name                string
		process, pane, text string
		want                bool
	}{
		{"fresh pane, no prompt drawn yet", idle, pane(""), "", false},
		{"prompt drawn but a helper is still the foreground", busy, pane(""), "% ", false},
		{"Herdr still detects an agent from the rc files", idle, pane("omp"), "% ", false},
		{"settled", idle, pane(""), "wilson@mac %", true},
	}
	for _, tc := range cases {
		startFakeHerdr(t, func(method string, params map[string]any) (json.RawMessage, error) {
			switch method {
			case "pane.process_info":
				return json.RawMessage(tc.process), nil
			case "pane.get":
				return json.RawMessage(tc.pane), nil
			case "pane.read":
				return json.RawMessage(read(tc.text)), nil
			}
			return nil, errorsUnexpected(method)
		})
		got, err := NewManager().ShellSettled(context.Background(), "default", "p")
		if err != nil || got != tc.want {
			t.Fatalf("%s: ShellSettled = (%v, %v), want %v", tc.name, got, err, tc.want)
		}
	}
}

func mustJSON(v any) []byte {
	data, _ := json.Marshal(v)
	return data
}

func TestLaunchTransportRejectsWrongResultType(t *testing.T) {
	startFakeHerdr(t, func(method string, params map[string]any) (json.RawMessage, error) {
		return json.RawMessage(`{"type":"tab_list","tabs":[]}`), nil
	})
	manager := NewManager()
	if _, _, err := manager.CreateTabWithoutFocus(context.Background(), "default", "w1", "/work/demo"); err == nil {
		t.Fatal("a mismatching result discriminator must fail loudly")
	}
}

// TestReadPaneDecodesTypedResult pins the pane.read contract: the result is
// the discriminated "pane_read" envelope with the payload nested under
// "read". Both the discriminator ("read") and the missing nesting drifted
// once, which silently disabled every hook-layer detection in production —
// the parse unit tests alone could not see it.
func TestReadPaneDecodesTypedResult(t *testing.T) {
	server := startFakeHerdr(t, func(method string, params map[string]any) (json.RawMessage, error) {
		if method != "pane.read" {
			return nil, errorsUnexpected(method)
		}
		if params["source"] != "visible" {
			return nil, errorsNew("pane.read must read the visible source")
		}
		return json.RawMessage(`{"type":"pane_read","read":{"pane_id":"p1","revision":7,"format":"text","source":"visible","text":"Enter to select"}}`), nil
	})
	defer server.close()
	manager := NewManager()
	text, err := manager.ReadPane(context.Background(), "default", "p1", "visible")
	if err != nil {
		t.Fatal(err)
	}
	if text != "Enter to select" {
		t.Fatalf("read text = %q", text)
	}
}

// An omp launched through bun reports `bun.native` as its foreground
// process, so agent.* refuses it by identity; only that refusal falls back
// to the pane-scoped writes, and a different refusal never does.
func TestAgentWritesFallBackToPaneScopeOnlyOnIdentityMismatch(t *testing.T) {
	refusal := "agent pane-1 is not an active named agent"
	server := startFakeHerdr(t, func(method string, params map[string]any) (json.RawMessage, error) {
		switch method {
		case "agent.send_keys", "agent.prompt":
			if params["target"] == "pane-busy" {
				return nil, errorsNew("agent pane-busy is not ready for input")
			}
			return nil, errorsNew(refusal)
		case "pane.send_keys", "pane.send_input":
			return json.RawMessage(`{"type":"ok"}`), nil
		default:
			return nil, errorsUnexpected(method)
		}
	})
	manager := NewManager()
	ctx := context.Background()
	if err := manager.SendAgentKeys(ctx, "default", "pane-1", []string{"down", "enter"}); err != nil {
		t.Fatal(err)
	}
	if err := manager.PromptAgent(ctx, "default", "pane-1", "hello"); err != nil {
		t.Fatal(err)
	}
	if err := manager.SendAgentKeys(ctx, "default", "pane-busy", []string{"enter"}); err == nil {
		t.Fatal("a not-ready agent must not be bypassed")
	}
	got := map[string]map[string]any{}
	for _, request := range server.recorded() {
		got[request.Method] = request.Params
	}
	if got["pane.send_keys"]["pane_id"] != "pane-1" {
		t.Fatalf("pane.send_keys params = %v", got["pane.send_keys"])
	}
	input := got["pane.send_input"]
	if input["pane_id"] != "pane-1" || input["text"] != "hello" {
		t.Fatalf("pane.send_input params = %v", input)
	}
}

func TestSubmitTextClosesATrailingMention(t *testing.T) {
	cases := map[string]string{
		"@/tmp/a.png":                       "@/tmp/a.png ",
		`look at @"/tmp/My Dir/a.png"`:      `look at @"/tmp/My Dir/a.png" `,
		"describe\n@/tmp/a.png":             "describe\n@/tmp/a.png ",
		"what is this? @/tmp/a.png and why": "what is this? @/tmp/a.png and why",
		"mail me at wilson@example.com":     "mail me at wilson@example.com",
		"/skill:tdd fix it":                 "/skill:tdd fix it",
		"already closed @/tmp/a.png ":       "already closed @/tmp/a.png ",
		"":                                  "",
	}
	for in, want := range cases {
		if got := submitText(in); got != want {
			t.Errorf("submitText(%q) = %q, want %q", in, got, want)
		}
	}
}
