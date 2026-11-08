package sshconfig

/**
 * [INPUT]: 系统 ssh 客户端（`ssh -G`，不建连、不做认证）
 * [OUTPUT]: EffectiveConfig（某别名的生效配置展示）、parseEffectiveOutput
 * [POS]: 语义求值永远委托系统 ssh，本包绝不复刻其规则；以 "-" 开头的别名直接拒绝，杜绝参数注入
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// EffectiveTimeout bounds one `ssh -G` round trip. -G only prints the
// resolved configuration; it never authenticates, so the budget is generous
// but finite.
const EffectiveTimeout = 5 * time.Second

// maxEffectiveRows bounds one evaluation for presentation.
const maxEffectiveRows = 400

// EffectiveConfig runs `ssh -G alias` and returns its key/value lines in
// ssh's own output order. The system client is the only compatibility
// authority; a parse here feeds the display, never a connection.
func EffectiveConfig(ctx context.Context, alias string) ([]KV, error) {
	if err := validAlias(alias); err != nil {
		return nil, err
	}
	out, err := exec.CommandContext(ctx, "ssh", "-G", alias).Output()
	if err != nil {
		return nil, fmt.Errorf("ssh -G failed: %w", err)
	}
	return parseEffectiveOutput(out), nil
}

// validAlias rejects empty and option-like aliases before they reach argv.
func validAlias(alias string) error {
	if alias == "" || strings.HasPrefix(alias, "-") {
		return fmt.Errorf("invalid host alias")
	}
	return nil
}

func parseEffectiveOutput(data []byte) []KV {
	var out []KV
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		out = append(out, KV{Key: key, Value: value})
		if len(out) >= maxEffectiveRows {
			break
		}
	}
	return out
}
