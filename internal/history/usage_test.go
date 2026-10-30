package history

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

/**
 * [INPUT]: 手写的 Pi/OMP、Claude Code、Codex JSONL 夹具
 * [OUTPUT]: ReadUsage 的总量、缓存、按模型路由聚合、Claude 同 id 去重、超长行跳过、不支持的 Provider 回归
 * [POS]: usage.go 的回归
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

func writeUsageFile(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadUsagePiRoutesByModelAndLevel(t *testing.T) {
	path := writeUsageFile(t,
		`{"type":"session","id":"x"}`,
		`{"type":"thinking_level_change","thinkingLevel":"high"}`,
		`{"type":"message","message":{"role":"user","content":[]}}`,
		`{"type":"message","message":{"role":"assistant","provider":"zhipu","model":"glm-5.3-flash","usage":{"input":1000,"output":200,"cacheRead":5000,"cacheWrite":0}}}`,
		`{"type":"message","message":{"role":"assistant","provider":"zhipu","model":"glm-5.3-flash","usage":{"input":800,"output":268,"cacheRead":1500,"cacheWrite":10}}}`,
		`{"type":"thinking_level_change","thinkingLevel":"max"}`,
		`{"type":"message","message":{"role":"assistant","provider":"antigravity","model":"gemini","usage":{"input":10,"output":5}}}`,
	)
	u, err := ReadUsage(context.Background(), AgentPi, path)
	if err != nil {
		t.Fatal(err)
	}
	if u.Input != 1810 || u.Output != 473 || u.CacheRead != 6500 || u.CacheWrite != 10 || u.Total() != 2283 || u.Cache() != 6510 {
		t.Fatalf("totals = %+v", u)
	}
	if len(u.Routes) != 2 || u.Routes[0] != (UsageRoute{Provider: "zhipu", Model: "glm-5.3-flash", Level: "high", Calls: 2, Tokens: 2268}) ||
		u.Routes[1] != (UsageRoute{Provider: "antigravity", Model: "gemini", Level: "max", Calls: 1, Tokens: 15}) {
		t.Fatalf("routes = %+v", u.Routes)
	}
}

func TestReadUsageClaudeCountsEachResponseOnce(t *testing.T) {
	block := func(id string, out int) string {
		return `{"type":"assistant","message":{"id":"` + id + `","model":"claude-opus","usage":{"input_tokens":100,"output_tokens":` +
			strconv.Itoa(out) + `,"cache_creation_input_tokens":10,"cache_read_input_tokens":900}}}`
	}
	path := writeUsageFile(t, block("m1", 5), block("m1", 40), block("m2", 7), `{"type":"user","message":{}}`)
	u, err := ReadUsage(context.Background(), AgentClaudeCode, path)
	if err != nil {
		t.Fatal(err)
	}
	if u.Input != 200 || u.Output != 47 || u.CacheRead != 1800 || u.CacheWrite != 20 {
		t.Fatalf("a response logged per block must count once: %+v", u)
	}
	if len(u.Routes) != 1 || u.Routes[0].Calls != 2 || u.Routes[0].Model != "claude-opus" {
		t.Fatalf("routes = %+v", u.Routes)
	}
}

func TestReadUsageCodexUsesTheRunningTotal(t *testing.T) {
	path := writeUsageFile(t,
		`{"type":"turn_context","payload":{"model":"gpt-x","effort":"high"}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1000,"cached_input_tokens":600,"output_tokens":50},"last_token_usage":{"input_tokens":1000,"cached_input_tokens":600,"output_tokens":50}}}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":2500,"cached_input_tokens":1900,"output_tokens":120},"last_token_usage":{"input_tokens":1500,"cached_input_tokens":1300,"output_tokens":70}}}}`,
	)
	u, err := ReadUsage(context.Background(), AgentCodex, path)
	if err != nil {
		t.Fatal(err)
	}
	if u.Input != 600 || u.CacheRead != 1900 || u.Output != 120 {
		t.Fatalf("totals = %+v", u)
	}
	if len(u.Routes) != 1 || u.Routes[0] != (UsageRoute{Model: "gpt-x", Level: "high", Calls: 2, Tokens: 450 + 270}) {
		t.Fatalf("routes = %+v", u.Routes)
	}
}

func TestReadUsageSkipsHugeLinesAndRefusesUnsupported(t *testing.T) {
	huge := `{"type":"message","message":{"role":"assistant","usage":{"input":999999},"pad":"` + strings.Repeat("x", maxUsageLineBytes+10) + `"}}`
	path := writeUsageFile(t, huge,
		`{"type":"message","message":{"role":"assistant","model":"m","usage":{"input":3,"output":4}}}`)
	u, err := ReadUsage(context.Background(), AgentPi, path)
	if err != nil || u.Input != 3 || u.Output != 4 {
		t.Fatalf("an over-long line must be skipped whole: %+v, %v", u, err)
	}
	if _, err := ReadUsage(context.Background(), AgentGemini, path); !errors.Is(err, ErrNoUsage) {
		t.Fatalf("an unsupported provider is ErrNoUsage, got %v", err)
	}
	if _, err := ReadUsage(context.Background(), AgentPi, filepath.Join(t.TempDir(), "missing.jsonl")); err == nil {
		t.Fatal("a missing file is an error")
	}
}
