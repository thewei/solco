/**
 * [INPUT]: 依赖 history/pi.go, models.go, live.go
 * [OUTPUT]: TestPiParseTranscript, TestPiSkillPromptCustomMessageIsTheUserTurn, TestPiLiveDecoderIncremental, TestParseAgentIDAliases
 * [POS]: history 包中 Pi / Oh My Pi 解析与 live 增量解码测试
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package history

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testPiJSONL = `{"type":"session","version":3,"id":"pi-sess-123","timestamp":"2026-10-06T10:00:00.000Z","cwd":"/work/herdr-client"}
{"type":"message","timestamp":"2026-10-06T10:00:01.000Z","message":{"role":"user","content":[{"type":"text","text":"Check the git status"}]}}
{"type":"message","timestamp":"2026-10-06T10:00:02.000Z","message":{"role":"assistant","model":"glm-5.3-flash","content":[{"type":"thinking","thinking":"Let me inspect git status."},{"type":"toolCall","id":"call_1","name":"sh","arguments":{"command":"git status"}}],"usage":{"totalTokens":150}}}
{"type":"message","timestamp":"2026-10-06T10:00:03.000Z","message":{"role":"toolResult","toolCallId":"call_1","toolName":"sh","content":[{"type":"text","text":"On branch main\nnothing to commit"}],"isError":false}}
{"type":"message","timestamp":"2026-10-06T10:00:04.000Z","message":{"role":"assistant","model":"glm-5.3-flash","content":[{"type":"text","text":"Working tree is clean."}]}}
`

const testPiSkillPromptJSONL = `{"type":"session","version":3,"id":"pi-skill-1","timestamp":"2026-11-01T09:52:08.647Z","cwd":"/work/solco"}
{"type":"custom_message","customType":"skill-prompt","content":"[IMPORTANT: User invoked the \"check\" skill; follow its instructions. Full skill below.]\n\n# Check: Review Before You Ship","display":true,"details":{"name":"check","path":"/home/u/.agents/skills/check/SKILL.md","args":"修复首条消息","prompt":"/skill:check 修复首条消息","lineCount":295},"attribution":"user","id":"2848442c","timestamp":"2026-11-01T09:53:19.399Z"}
{"type":"custom_message","customType":"launch-completion","content":"Supervised process herdr-chatbug failed with exit code 1.","display":true,"details":{"daemons":[]},"attribution":"agent","id":"51334226","timestamp":"2026-11-01T10:44:26.652Z"}
{"type":"message","id":"a1","timestamp":"2026-11-01T09:53:40.000Z","message":{"role":"assistant","content":[{"type":"text","text":"🥷 开始排查。"}]}}
`

// The 2026-11-01 report: a conversation opened with a skill invocation shows
// no first message in Chat — everything after is the assistant's work. omp
// records that prompt as a skill-prompt custom_message (attribution user,
// typed text in details.prompt), the transcript's ONLY user row: the parser
// must surface it as the user turn, and agent-attributed notices must not.
func TestPiSkillPromptCustomMessageIsTheUserTurn(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "2026-11-01T09-52-08-647Z_pi-skill-1.jsonl")
	if err := os.WriteFile(filePath, []byte(testPiSkillPromptJSONL), 0o644); err != nil {
		t.Fatal(err)
	}
	transcript, err := ParsePiTranscript(SessionFileRef{Agent: AgentOMP, NativeID: "pi-skill-1", FilePath: filePath})
	if err != nil {
		t.Fatalf("ParsePiTranscript failed: %v", err)
	}
	if len(transcript.Mainline) != 2 {
		t.Fatalf("got %d rows, want 2 (user skill turn + assistant): %+v", len(transcript.Mainline), transcript.Mainline)
	}
	user := transcript.Mainline[0]
	if user.Role != RoleUser || user.Text != "/skill:check 修复首条消息" {
		t.Fatalf("first row = %s %.60q, want user /skill:check 修复首条消息", user.Role, user.Text)
	}
	if user.Timestamp == nil || *user.Timestamp != 1793526799399 {
		t.Fatalf("user timestamp = %v", user.Timestamp)
	}

	// The live decoder shares the parser: the bound Chat tail must agree.
	decoder, err := NewLiveDecoder(AgentOMP)
	if err != nil {
		t.Fatalf("decoder: %v", err)
	}
	sync := decoder.Append([]byte(testPiSkillPromptJSONL))
	if len(sync.Messages) != 2 || sync.Messages[0].Role != RoleUser || sync.Messages[0].Text != user.Text {
		t.Fatalf("live = %+v, want matching user row first", sync.Messages)
	}
}

func TestPiParseTranscript(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "2026-10-06T10-00-00-000Z_pi-sess-123.jsonl")
	if err := os.WriteFile(filePath, []byte(testPiJSONL), 0644); err != nil {
		t.Fatal(err)
	}

	ref := SessionFileRef{
		Agent:     AgentPi,
		NativeID:  "pi-sess-123",
		FilePath:  filePath,
		MtimeMS:   1000,
		SizeBytes: int64(len(testPiJSONL)),
	}

	transcript, err := ParsePiTranscript(ref)
	if err != nil {
		t.Fatalf("ParsePiTranscript failed: %v", err)
	}

	if transcript.Meta.ID != "pi-sess-123" {
		t.Errorf("got ID %q, want %q", transcript.Meta.ID, "pi-sess-123")
	}
	if transcript.Meta.ProjectPath != "/work/herdr-client" {
		t.Errorf("got ProjectPath %q, want %q", transcript.Meta.ProjectPath, "/work/herdr-client")
	}
	if transcript.Meta.Title != "Check the git status" {
		t.Errorf("got Title %q, want %q", transcript.Meta.Title, "Check the git status")
	}
	if transcript.Meta.TokensUsed == nil || *transcript.Meta.TokensUsed != 150 {
		t.Errorf("got TokensUsed %v, want 150", transcript.Meta.TokensUsed)
	}

	// Mainline messages: 1 User, 1 merged Assistant (containing both toolCall + final text)
	if len(transcript.Mainline) != 2 {
		t.Fatalf("got %d messages, want 2 (user + merged assistant)", len(transcript.Mainline))
	}

	user := transcript.Mainline[0]
	if user.Role != RoleUser || user.Text != "Check the git status" {
		t.Errorf("user message mismatch: %+v", user)
	}

	assistant := transcript.Mainline[1]
	if assistant.Role != RoleAssistant {
		t.Errorf("assistant role mismatch: %v", assistant.Role)
	}
	if assistant.Thinking == nil || *assistant.Thinking != "Let me inspect git status." {
		t.Errorf("assistant thinking mismatch: %v", assistant.Thinking)
	}
	if assistant.Text != "Working tree is clean." {
		t.Errorf("assistant text mismatch: %q", assistant.Text)
	}
	if len(assistant.ToolCalls) != 1 {
		t.Fatalf("assistant tool calls length %d, want 1", len(assistant.ToolCalls))
	}
	tc := assistant.ToolCalls[0]
	if tc.ID != "call_1" || tc.Name != "sh" {
		t.Errorf("tool call mismatch: %+v", tc)
	}
	if tc.Output == nil || *tc.Output != "On branch main\nnothing to commit" {
		t.Errorf("tool call output mismatch: %v", tc.Output)
	}
	if tc.IsError {
		t.Errorf("tool call isError should be false")
	}
}

// TestPiTitleRowWinsOverFirstMessage pins the agent-authored session
// title: the `title` row at head and later `title_change` rows (source
// auto) are the session's real name — the last one wins and the first
// message heuristic only covers sessions without any title row; a blank
// title row never clears a previous one.
func TestPiTitleRowWinsOverFirstMessage(t *testing.T) {
	const jsonl = `{"type":"title","v":1,"title":"🔍 SEARCH: First take","source":"auto","updatedAt":"2026-10-06T10:00:00.000Z"}
{"type":"session","version":3,"id":"pi-sess-t","timestamp":"2026-10-06T10:00:00.000Z","cwd":"/work/x"}
{"type":"message","timestamp":"2026-10-06T10:00:01.000Z","message":{"role":"user","content":[{"type":"text","text":"Check the git status"}]}}
{"type":"title_change","title":"🔍 SEARCH: Reorganize title bar","updatedAt":"2026-10-06T10:05:00.000Z"}
{"type":"title_change","title":"","updatedAt":"2026-10-06T10:06:00.000Z"}
{"type":"title_change","title":"🔍 SEARCH: Reorganize title bar search buttons","updatedAt":"2026-10-06T10:07:00.000Z"}
`
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "2026-10-06T10-00-00-000Z_pi-sess-t.jsonl")
	if err := os.WriteFile(filePath, []byte(jsonl), 0644); err != nil {
		t.Fatal(err)
	}
	ref := SessionFileRef{Agent: AgentOMP, NativeID: "pi-sess-t", FilePath: filePath, MtimeMS: 1000, SizeBytes: int64(len(jsonl))}

	transcript, err := ParsePiTranscript(ref)
	if err != nil {
		t.Fatalf("ParsePiTranscript failed: %v", err)
	}
	if transcript.Meta.Title != "🔍 SEARCH: Reorganize title bar search buttons" {
		t.Errorf("got Title %q, want the agent's last title_change value", transcript.Meta.Title)
	}
	if transcript.UnknownLineCount != 0 {
		t.Errorf("title rows counted as unknown lines: %d", transcript.UnknownLineCount)
	}
}

func TestPiLiveDecoderIncremental(t *testing.T) {
	decoder, err := NewLiveDecoder(AgentPi)
	if err != nil {
		t.Fatalf("NewLiveDecoder(AgentPi) failed: %v", err)
	}

	// 1. Line 1: session header
	sync := decoder.Append([]byte("{\"type\":\"session\",\"id\":\"s1\",\"cwd\":\"/app\"}\n"))
	if len(decoder.Projection()) != 0 {
		t.Fatalf("expected 0 messages from session header, got %d", len(decoder.Projection()))
	}

	// 2. Line 2: user message
	sync = decoder.Append([]byte("{\"type\":\"message\",\"timestamp\":\"2026-10-06T10:00:00Z\",\"message\":{\"role\":\"user\",\"content\":[{\"type\":\"text\",\"text\":\"hello\"}]}}\n"))
	if len(sync.Appended) != 1 {
		t.Fatalf("expected 1 appended message, got %d", len(sync.Appended))
	}
	if decoder.Projection()[0].Text != "hello" {
		t.Errorf("expected text hello, got %q", decoder.Projection()[0].Text)
	}

	// 3. Line 3: assistant tool call
	sync = decoder.Append([]byte("{\"type\":\"message\",\"timestamp\":\"2026-10-06T10:00:01Z\",\"message\":{\"role\":\"assistant\",\"content\":[{\"type\":\"toolCall\",\"id\":\"c1\",\"name\":\"ls\",\"arguments\":{}}]}}\n"))
	if len(sync.Appended) != 1 {
		t.Fatalf("expected 1 appended assistant message, got %d", len(sync.Appended))
	}
	if len(decoder.Projection()[1].ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(decoder.Projection()[1].ToolCalls))
	}

	// 4. Line 4: tool result (mutates earlier assistant message)
	sync = decoder.Append([]byte("{\"type\":\"message\",\"timestamp\":\"2026-10-06T10:00:02Z\",\"message\":{\"role\":\"toolResult\",\"toolCallId\":\"c1\",\"content\":[{\"type\":\"text\",\"text\":\"file.txt\"}]}}\n"))
	if len(sync.Changed) != 1 {
		t.Fatalf("expected 1 changed message for tool result, got %d", len(sync.Changed))
	}
	tc := decoder.Projection()[1].ToolCalls[0]
	if tc.Output == nil || *tc.Output != "file.txt" {
		t.Errorf("expected output file.txt, got %v", tc.Output)
	}

	// 5. Line 5: consecutive assistant message merges into existing assistant turn
	sync = decoder.Append([]byte("{\"type\":\"message\",\"timestamp\":\"2026-10-06T10:00:03Z\",\"message\":{\"role\":\"assistant\",\"content\":[{\"type\":\"text\",\"text\":\"Done listing.\"}]}}\n"))
	if len(decoder.Projection()) != 2 {
		t.Fatalf("expected projection length 2 (merged), got %d", len(decoder.Projection()))
	}
	if len(sync.Changed) != 1 {
		t.Fatalf("expected 1 changed message on merged assistant turn, got %d", len(sync.Changed))
	}
	if decoder.Projection()[1].Text != "Done listing." {
		t.Errorf("expected merged text Done listing., got %q", decoder.Projection()[1].Text)
	}

	// 6. Idempotent poll without new data
	status := decoder.Status()
	if status.HasUpdates() {
		t.Errorf("duplicate wake must report HasUpdates = false")
	}
}

func TestParseAgentIDAliases(t *testing.T) {
	cases := []struct {
		input string
		want  AgentID
		ok    bool
	}{
		{"pi", AgentPi, true},
		{"pi-coding-agent", AgentPi, true},
		{"omp", AgentOMP, true},
		{"oh-my-pi", AgentOMP, true},
		{"claude", AgentClaudeCode, true},
		{"claude-code", AgentClaudeCode, true},
		{"claude_code", AgentClaudeCode, true},
		{"codex", AgentCodex, true},
		{"codex-cli", AgentCodex, true},
		{"antigravity", AgentAntigravity, true},
		{"agy", AgentAntigravity, true},
		{"antigravity-cli", AgentAntigravity, true},
		{"command-code", AgentCommandCode, true},
		{"commandcode", AgentCommandCode, true},
		{"unknown-agent", "", false},
	}

	for _, c := range cases {
		got, ok := ParseAgentID(c.input)
		if ok != c.ok || got != c.want {
			t.Errorf("ParseAgentID(%q) = (%q, %v), want (%q, %v)", c.input, got, ok, c.want, c.ok)
		}
	}
}

const testPiInterleavedJSONL = `{"type":"session","version":3,"id":"pi-order","timestamp":"2026-10-06T10:00:00.000Z","cwd":"/work/x"}
{"type":"message","timestamp":"2026-10-06T10:00:01.000Z","message":{"role":"user","content":[{"type":"text","text":"go"}]}}
{"type":"message","timestamp":"2026-10-06T10:00:02.000Z","message":{"role":"assistant","content":[{"type":"text","text":"First look."},{"type":"toolCall","id":"c1","name":"read","arguments":{"path":"a"}}]}}
{"type":"message","timestamp":"2026-10-06T10:00:03.000Z","message":{"role":"toolResult","toolCallId":"c1","toolName":"read","content":[],"isError":false}}
{"type":"message","timestamp":"2026-10-06T10:00:09.000Z","message":{"role":"assistant","content":[{"type":"text","text":"Now edit."},{"type":"toolCall","id":"c2","name":"edit","arguments":{"path":"a"}}]}}
`

// A merged Pi message keeps its blocks in written order (text → tool → text →
// tool), each with its own row timestamp, and an empty tool result still marks
// the call finished (Output non-nil) while an unanswered call stays nil.
func TestPiMergedMessageKeepsBlockOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "2026-10-06T10-00-00-000Z_pi-order.jsonl")
	if err := os.WriteFile(path, []byte(testPiInterleavedJSONL), 0644); err != nil {
		t.Fatal(err)
	}
	transcript, err := ParsePiTranscript(SessionFileRef{Agent: AgentPi, NativeID: "pi-order", FilePath: path, SizeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(transcript.Mainline) != 2 {
		t.Fatalf("messages = %d, want user + merged assistant", len(transcript.Mainline))
	}
	msg := transcript.Mainline[1]
	want := []struct {
		kind MessagePartKind
		tool int
	}{{PartText, 0}, {PartTool, 0}, {PartText, 0}, {PartTool, 1}}
	if len(msg.Parts) != len(want) {
		t.Fatalf("parts = %+v", msg.Parts)
	}
	for i, w := range want {
		if msg.Parts[i].Kind != w.kind || (w.kind == PartTool && msg.Parts[i].Tool != w.tool) {
			t.Fatalf("part %d = %+v, want %+v", i, msg.Parts[i], w)
		}
	}
	if msg.Parts[0].Timestamp == nil || msg.Parts[2].Timestamp == nil ||
		*msg.Parts[2].Timestamp-*msg.Parts[0].Timestamp != 7000 {
		t.Fatalf("part timestamps = %v / %v, want 7s apart", msg.Parts[0].Timestamp, msg.Parts[2].Timestamp)
	}
	if len(msg.ToolCalls) != 2 {
		t.Fatalf("tool calls = %d", len(msg.ToolCalls))
	}
	if msg.ToolCalls[0].Output == nil {
		t.Fatal("empty tool result must still set Output (finished)")
	}
	if msg.ToolCalls[1].Output != nil {
		t.Fatal("unanswered call must keep Output nil (running)")
	}
}

func TestPiAssistantCarriesTheContextItsRequestFilled(t *testing.T) {
	jsonl := `{"type":"session","id":"pi-ctx","cwd":"/work"}
{"type":"message","timestamp":"2026-10-06T10:00:01.000Z","message":{"role":"user","content":[{"type":"text","text":"hi"}]}}
{"type":"message","timestamp":"2026-10-06T10:00:02.000Z","message":{"role":"assistant","model":"m","content":[{"type":"text","text":"a"}],"usage":{"input":10,"cacheRead":90,"totalTokens":100}}}
{"type":"message","timestamp":"2026-10-06T10:00:03.000Z","message":{"role":"assistant","model":"m","content":[{"type":"text","text":"b"}],"usage":{"input":10,"cacheRead":190,"totalTokens":200}}}
`
	path := filepath.Join(t.TempDir(), "2026-10-06T10-00-00-000Z_pi-ctx.jsonl")
	if err := os.WriteFile(path, []byte(jsonl), 0644); err != nil {
		t.Fatal(err)
	}
	transcript, err := ParsePiTranscript(SessionFileRef{Agent: AgentPi, NativeID: "pi-ctx", FilePath: path, SizeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	last := transcript.Mainline[len(transcript.Mainline)-1]
	if last.Role != RoleAssistant || last.ContextTokens == nil || *last.ContextTokens != 200 {
		t.Fatalf("merged assistant must carry the latest request's 200, got %+v", last.ContextTokens)
	}
	if transcript.Mainline[0].ContextTokens != nil {
		t.Fatal("a user message has no context report")
	}
}

// Pi's todo extension prints only the increment; the whole list is the
// result's details.tasks, which the parser keeps for the Tasks tray. Other
// tools and refused calls carry none.
func TestPiTodoResultKeepsTheWholeListAsDetails(t *testing.T) {
	const jsonl = `{"type":"session","version":3,"id":"pi-todo","timestamp":"2026-11-02T02:00:00.000Z","cwd":"/w"}
{"type":"message","timestamp":"2026-11-02T02:00:01.000Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"c1","name":"todo","arguments":{"action":"update","id":1,"status":"in_progress"}},{"type":"toolCall","id":"c2","name":"bash","arguments":{"command":"ls"}}]}}
{"type":"message","timestamp":"2026-11-02T02:00:02.000Z","message":{"role":"toolResult","toolCallId":"c1","toolName":"todo","content":[{"type":"text","text":"Updated #1 (pending → in_progress)"}],"details":{"action":"update","tasks":[{"id":1,"subject":"A","status":"in_progress"}]}}}
{"type":"message","timestamp":"2026-11-02T02:00:03.000Z","message":{"role":"toolResult","toolCallId":"c2","toolName":"bash","content":[{"type":"text","text":"x"}],"details":{"tasks":[{"id":9}]}}}
`
	decoder, err := NewLiveDecoder(AgentPi)
	if err != nil {
		t.Fatal(err)
	}
	sync := decoder.Append([]byte(jsonl))
	var todo, bash *ToolCall
	for i := range sync.Messages {
		for j := range sync.Messages[i].ToolCalls {
			call := &sync.Messages[i].ToolCalls[j]
			switch call.Name {
			case "todo":
				todo = call
			case "bash":
				bash = call
			}
		}
	}
	if todo == nil || todo.Details == nil || *todo.Details != `{"tasks":[{"id":1,"status":"in_progress","subject":"A"}]}` {
		t.Fatalf("todo details = %+v", todo)
	}
	if bash == nil || bash.Details != nil {
		t.Fatalf("a non-todo tool must carry no details: %+v", bash)
	}
}

// A turn Pi/omp ended early is an assistant row with empty content: it must
// still mark the turn Stopped (an error says why), and a compaction row must
// leave one marker (never the summary body) for Chat's divider.
func TestPiStoppedTurnsAndCompactionMarker(t *testing.T) {
	const jsonl = `{"type":"session","version":3,"id":"pi-stop","timestamp":"2026-11-02T02:00:00.000Z","cwd":"/w"}
{"type":"message","timestamp":"2026-11-02T02:00:01.000Z","message":{"role":"user","content":[{"type":"text","text":"go"}]}}
{"type":"message","timestamp":"2026-11-02T02:00:02.000Z","message":{"role":"assistant","content":[],"stopReason":"aborted","errorMessage":"Operation aborted"}}
{"type":"message","timestamp":"2026-11-02T02:00:03.000Z","message":{"role":"user","content":[{"type":"text","text":"again"}]}}
{"type":"message","timestamp":"2026-11-02T02:00:04.000Z","message":{"role":"assistant","content":[],"stopReason":"error","errorMessage":"Request timed out."}}
{"type":"compaction","timestamp":"2026-11-02T02:00:05.000Z","summary":"SECRET BODY","tokensBefore":1000}
{"type":"message","timestamp":"2026-11-02T02:00:06.000Z","message":{"role":"user","content":[{"type":"text","text":"later"}]}}
`
	decoder, err := NewLiveDecoder(AgentPi)
	if err != nil {
		t.Fatal(err)
	}
	messages := decoder.Append([]byte(jsonl)).Messages
	if len(messages) != 6 {
		t.Fatalf("got %d messages: %+v", len(messages), messages)
	}
	if a := messages[1]; a.Role != RoleAssistant || !a.Stopped || a.Text != "" {
		t.Fatalf("aborted row = %+v", a)
	}
	if e := messages[3]; !e.Stopped || e.Text != "Error: Request timed out." {
		t.Fatalf("error row = %+v", e)
	}
	if c := messages[4]; c.Kind != MessageCompactSummary || strings.Contains(c.Text, "SECRET") {
		t.Fatalf("compaction row = %+v", c)
	}
}
