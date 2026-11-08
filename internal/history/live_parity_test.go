package history

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

/**
 * [INPUT]: 依赖 claude/codex/pi 解析器与 LiveDecoder
 * [OUTPUT]: TestLiveDecoderMatchesBatchParse
 * [POS]: 聊天视图的三条入口（新会话 / 已有 Agent 窗格的 live tail / 历史记录）共用同一份转录：live 增量解码的投影必须与整文件解析的 Mainline 逐字段一致
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

const parityClaudeJSONL = "" +
	"{\"type\":\"user\",\"cwd\":\"/work/demo\",\"timestamp\":\"2026-08-01T01:00:00Z\",\"message\":{\"content\":\"Fix the parser\"}}\n" +
	"{\"type\":\"assistant\",\"cwd\":\"/work/demo\",\"timestamp\":\"2026-08-01T01:00:01Z\",\"message\":{\"id\":\"m1\",\"model\":\"claude-sonnet-4-5\",\"usage\":{\"input_tokens\":10,\"output_tokens\":5},\"content\":[{\"type\":\"thinking\",\"thinking\":\"Inspect first\"},{\"type\":\"tool_use\",\"id\":\"tool-1\",\"name\":\"Read\",\"input\":{\"file_path\":\"src/lib.rs\"}}]}}\n" +
	"{\"type\":\"user\",\"cwd\":\"/work/demo\",\"timestamp\":\"2026-08-01T01:00:02Z\",\"message\":{\"content\":[{\"type\":\"tool_result\",\"tool_use_id\":\"tool-1\",\"content\":\"source text\"}]}}\n" +
	"{\"type\":\"assistant\",\"cwd\":\"/work/demo\",\"timestamp\":\"2026-08-01T01:00:03Z\",\"message\":{\"id\":\"m2\",\"model\":\"claude-sonnet-4-5\",\"content\":[{\"type\":\"text\",\"text\":\"I found it.\"},{\"type\":\"tool_use\",\"id\":\"tool-2\",\"name\":\"Edit\",\"input\":{\"file_path\":\"src/lib.rs\"}}]}}\n" +
	"{\"type\":\"user\",\"cwd\":\"/work/demo\",\"timestamp\":\"2026-08-01T01:00:04Z\",\"message\":{\"content\":[{\"type\":\"tool_result\",\"tool_use_id\":\"tool-2\",\"content\":\"done\"}]}}\n" +
	"{\"type\":\"assistant\",\"cwd\":\"/work/demo\",\"timestamp\":\"2026-08-01T01:00:05Z\",\"message\":{\"id\":\"m3\",\"model\":\"claude-sonnet-4-5\",\"content\":[{\"type\":\"text\",\"text\":\"Fixed.\"}]}}\n" +
	"{\"type\":\"user\",\"cwd\":\"/work/demo\",\"timestamp\":\"2026-08-01T01:01:00Z\",\"message\":{\"content\":\"Thanks\"}}\n"

// TestLiveDecoderMatchesBatchParse pins the one-transcript rule: a pane
// followed live (incremental decoder), a pane hydrated at once and a session
// opened from the Sessions list all show the same rows, because the live
// projection equals the whole-file parse however the bytes arrive.
func TestLiveDecoderMatchesBatchParse(t *testing.T) {
	cases := []struct {
		name  string
		agent AgentID
		jsonl string
		parse func(SessionFileRef) (ParsedTranscript, error)
	}{
		{"claude", AgentClaudeCode, parityClaudeJSONL, ParseClaudeTranscript},
		{"codex", AgentCodex, codexFixtureRollout, ParseCodexTranscript},
		{"pi", AgentPi, testPiJSONL, ParsePiTranscript},
		{"pi interleaved", AgentPi, testPiInterleavedJSONL, ParsePiTranscript},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "2026-10-06T10-00-00-000Z_parity-session.jsonl")
			if err := os.WriteFile(path, []byte(tc.jsonl), 0o600); err != nil {
				t.Fatal(err)
			}
			info, _ := os.Stat(path)
			batch, err := tc.parse(SessionFileRef{Agent: tc.agent, NativeID: "parity-session", FilePath: path, MtimeMS: 1000, SizeBytes: info.Size()})
			if err != nil {
				t.Fatal(err)
			}

			feeds := map[string][]string{
				"hydrate at once": {tc.jsonl},
				"line by line":    strings.SplitAfter(tc.jsonl, "\n"),
			}
			for feed, chunks := range feeds {
				decoder, err := NewLiveDecoder(tc.agent)
				if err != nil {
					t.Fatal(err)
				}
				for _, chunk := range chunks {
					decoder.Append([]byte(chunk))
				}
				decoder.Finalize()
				got := decoder.Projection()
				if !reflect.DeepEqual(got, batch.Mainline) {
					t.Errorf("%s: live projection differs from the batch parse\nlive : %+v\nbatch: %+v", feed, got, batch.Mainline)
				}
			}
		})
	}
}
