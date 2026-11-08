package history

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

/**
 * [INPUT]: 依赖 agy.go 解析器与 LiveDecoder
 * [OUTPUT]: TestParseAgyTranscript*, TestAgyLiveDecoderMatchesBatchParse, TestAgyPathHelpers, TestResolveLiveSourceAgy
 * [POS]: agy 转录形状（真机探针 2026-11-01 与 IDE brain 转录）的回归锁：USER_REQUEST 取出、先进先出结果配对、ERROR/RUNNING、CHECKPOINT、SYSTEM_MESSAGE、合并与 live/整文件一致
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const testAgyJSONL = "" +
	`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-11-01T04:42:30Z","content":"<USER_REQUEST>\nRun 'sleep 3; echo a', then say done.\n</USER_REQUEST>\n<ADDITIONAL_METADATA>\nThe current local time is: 2026-11-01T12:42:30+08:00.\n</ADDITIONAL_METADATA>\n<USER_SETTINGS_CHANGE>\nThe user changed setting ` + "`Model Selection`" + ` from None to Gemini 3.8 Flash (High). No action needed.\n</USER_SETTINGS_CHANGE>"}` + "\n" +
	`{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-11-01T04:42:31Z","input_tokens":1000,"cache_read_tokens":200,"output_tokens":50,"thinking":"Plan the command.","content":"Running it.","tool_calls":[{"name":"run_command","args":{"CommandLine":"sleep 3; echo a","WaitMsBeforeAsync":6000}}]}` + "\n" +
	`{"step_index":2,"source":"MODEL","type":"GENERIC","status":"DONE","created_at":"2026-11-01T04:42:39Z","content":"Command exited with code 0.\n\nCommand output:\na\r\n"}` + "\n" +
	`{"step_index":3,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-11-01T04:42:42Z","input_tokens":1100,"output_tokens":60,"tool_calls":[{"name":"edit_file","args":{"Path":"/work/demo/a.go","Replacement":"x"}}]}` + "\n" +
	`{"step_index":4,"source":"MODEL","type":"GENERIC","status":"ERROR","error":"path is not absolute","created_at":"2026-11-01T04:42:43Z","content":"Encountered error in step execution: path is not absolute"}` + "\n" +
	`{"step_index":5,"source":"SYSTEM","type":"SYSTEM_MESSAGE","status":"DONE","created_at":"2026-11-01T04:42:44Z","content":"The following is a <SYSTEM_MESSAGE> not sent by the user."}` + "\n" +
	`{"step_index":6,"source":"SYSTEM","type":"CHECKPOINT","status":"DONE","created_at":"2026-11-01T04:42:45Z","content":"# Resuming from a compaction"}` + "\n" +
	`{"step_index":7,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-11-01T04:42:57Z","input_tokens":300,"output_tokens":10,"content":"done."}` + "\n"

func parseAgyFixture(t *testing.T) ParsedTranscript {
	t.Helper()
	path := filepath.Join(t.TempDir(), "brain", "conv-1", ".system_generated", "logs", agyTranscriptName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(testAgyJSONL), 0o600); err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseAgyTranscript(SessionFileRef{Agent: AgentAntigravity, NativeID: "conv-1", FilePath: path, MtimeMS: 1000})
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func TestParseAgyTranscript(t *testing.T) {
	parsed := parseAgyFixture(t)
	msgs := parsed.Mainline
	if len(msgs) != 5 {
		t.Fatalf("want 5 rows (user, merged assistant, system note, compaction, assistant), got %d: %+v", len(msgs), msgs)
	}
	if msgs[0].Role != RoleUser || msgs[0].Text != "Run 'sleep 3; echo a', then say done." {
		t.Fatalf("user text must be the USER_REQUEST body only: %q", msgs[0].Text)
	}
	assistant := msgs[1]
	if assistant.Role != RoleAssistant || assistant.Text != "Running it." || assistant.Thinking == nil || *assistant.Thinking != "Plan the command." {
		t.Fatalf("assistant row: %+v", assistant)
	}
	// Two model steps separated by tool results merge into one message.
	if len(assistant.ToolCalls) != 2 {
		t.Fatalf("merged tool calls: %+v", assistant.ToolCalls)
	}
	run := assistant.ToolCalls[0]
	if run.Name != "run_command" || run.InputPreview != "sleep 3; echo a" || run.Output == nil || !strings.Contains(*run.Output, "Command output:\na") || run.IsError {
		t.Fatalf("first call must pair with the next result: %+v", run)
	}
	edit := assistant.ToolCalls[1]
	if edit.InputPreview != "/work/demo/a.go" || !edit.IsError || edit.Output == nil || !strings.Contains(*edit.Output, "not absolute") {
		t.Fatalf("ERROR step must mark the call failed: %+v", edit)
	}
	kinds := []string{}
	for _, part := range assistant.Parts {
		kinds = append(kinds, string(part.Kind))
	}
	if len(kinds) != 4 || kinds[0] != "thinking" || kinds[1] != "text" || kinds[2] != "tool" || kinds[3] != "tool" {
		t.Fatalf("part order must follow the provider's: %v", kinds)
	}
	if msgs[2].Role != RoleSystem || msgs[2].Kind != MessageMeta {
		t.Fatalf("SYSTEM_MESSAGE is meta: %+v", msgs[2])
	}
	if msgs[3].Kind != MessageCompactSummary {
		t.Fatalf("CHECKPOINT is a compaction marker: %+v", msgs[3])
	}
	if msgs[4].Role != RoleAssistant || msgs[4].Text != "done." {
		t.Fatalf("final assistant: %+v", msgs[4])
	}
	if parsed.Meta.Model == nil || *parsed.Meta.Model != "Gemini 3.8 Flash (High)" {
		t.Fatalf("model: %v", parsed.Meta.Model)
	}
	if parsed.Meta.TokensUsed == nil || *parsed.Meta.TokensUsed != 310 {
		t.Fatalf("tokens: %v", parsed.Meta.TokensUsed)
	}
	if parsed.UnknownLineCount != 0 {
		t.Fatalf("unknown lines: %d", parsed.UnknownLineCount)
	}
}

func TestAgyLiveDecoderMatchesBatchParse(t *testing.T) {
	batch := parseAgyFixture(t)
	for name, chunks := range map[string][]string{
		"at once":      {testAgyJSONL},
		"line by line": strings.SplitAfter(testAgyJSONL, "\n"),
	} {
		decoder, err := NewLiveDecoder(AgentAntigravity)
		if err != nil {
			t.Fatal(err)
		}
		for _, chunk := range chunks {
			decoder.Append([]byte(chunk))
		}
		live := decoder.Projection()
		if len(live) != len(batch.Mainline) {
			t.Fatalf("%s: live %d rows, batch %d", name, len(live), len(batch.Mainline))
		}
		for i := range live {
			if !transcriptMessageEqual(live[i], batch.Mainline[i]) {
				t.Fatalf("%s: row %d differs:\nlive  %+v\nbatch %+v", name, i, live[i], batch.Mainline[i])
			}
		}
	}
}

func TestAgyPathHelpers(t *testing.T) {
	path := "/h/.gemini/antigravity-cli/brain/abc-123/.system_generated/logs/transcript_full.jsonl"
	if got := agyNativeID(path); got != "abc-123" {
		t.Fatalf("native id: %q", got)
	}
	if got := agyNativeID("/h/other/transcript_full.jsonl"); got != "" {
		t.Fatalf("foreign path must not yield an id: %q", got)
	}
	if got := agyTranscriptFile("/h/brain", "abc-123"); got != "/h/brain/abc-123/.system_generated/logs/transcript_full.jsonl" {
		t.Fatalf("transcript file: %q", got)
	}
}

func TestResolveLiveSourceAgy(t *testing.T) {
	home := t.TempDir()
	cli := agyTranscriptFile(filepath.Join(home, ".gemini", "antigravity-cli", "brain"), "conv-a")
	ide := agyTranscriptFile(filepath.Join(home, ".gemini", "antigravity", "brain"), "conv-b")
	for _, path := range []string{cli, ide} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for id, want := range map[string]string{"conv-a": cli, "conv-b": ide} {
		got, err := ResolveLiveSource(home, AgentAntigravity, "id", "herdr:antigravity_cli", id)
		if err != nil || got != want {
			t.Fatalf("id %s: %q, %v", id, got, err)
		}
	}
	// A conversation that has not written yet resolves to the CLI location.
	pending, err := ResolveLiveSource(home, AgentAntigravity, "id", "herdr:antigravity_cli", "conv-new")
	if err != nil || pending != agyTranscriptFile(filepath.Join(home, ".gemini", "antigravity-cli", "brain"), "conv-new") {
		t.Fatalf("pending: %q, %v", pending, err)
	}
	// The hook's clipped transcript.jsonl maps to the complete file beside it.
	clipped := filepath.Join(filepath.Dir(cli), "transcript.jsonl")
	got, err := ResolveLiveSource(home, AgentAntigravity, "path", "herdr:antigravity_cli", clipped)
	if err != nil || got != cli {
		t.Fatalf("clipped path: %q, %v", got, err)
	}
}

func TestAgyListRefsUsesSummariesForTitleAndSkipsChildren(t *testing.T) {
	home := t.TempDir()
	brain := filepath.Join(home, ".gemini", "antigravity-cli", "brain")
	for _, id := range []string{"conv-a", "conv-child", "conv-empty"} {
		path := agyTranscriptFile(brain, id)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		body := testAgyJSONL
		if id == "conv-empty" {
			body = ""
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	db := agySummaryDB(brain)
	if err := os.WriteFile(db, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	enc := func(parts ...string) string {
		out := make([]string, len(parts))
		for i, part := range parts {
			out[i] = hex.EncodeToString([]byte(part))
		}
		return strings.Join(out, "|") + "\n"
	}
	previous := agySQLiteRunner
	agySQLiteRunner = func(string, string) ([]byte, error) {
		return []byte(enc("conv-a", "Sleep Commands", `["file:///work/my%20demo"]`, "") + enc("conv-child", "Child", `[]`, "conv-a")), nil
	}
	t.Cleanup(func() { agySQLiteRunner = previous })

	refs, err := agyListRefs(SourceRoot{Agent: AgentAntigravity, Directory: brain})
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].NativeID != "conv-a" {
		t.Fatalf("only the parent conversation with a transcript is listed: %+v", refs)
	}
	parsed, err := parseAgyWithSummary(refs[0])
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Meta.Title != "Sleep Commands" || parsed.Meta.ProjectPath != "/work/my demo" || parsed.Meta.ProjectName != "my demo" {
		t.Fatalf("index title and workspace must reach the row: %+v", parsed.Meta)
	}
}
