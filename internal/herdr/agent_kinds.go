package herdr

import (
	"context"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

/**
 * [INPUT]: 依赖 `herdr agent start --help`（Herdr 自己声明的「--kind [possible values: …]」，版本精确）
 * [OUTPUT]: 提供 Manager.AgentKinds / parseAgentKinds
 * [POS]: 可启动 Agent 种类的唯一来源——读 live CLI 而不是自带一份会过期的清单；读不到返回错误，调用方自行降级
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

var possibleValues = regexp.MustCompile(`(?s)--kind\b.*?\[possible values:\s*([^\]]+)\]`)

// AgentKinds lists the agent kinds this Herdr can start (`agent.start` kind).
func (m *Manager) AgentKinds(ctx context.Context) ([]string, error) {
	cli, err := resolveCLI()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, cli, "agent", "start", "--help").Output()
	if err != nil {
		return nil, err
	}
	return parseAgentKinds(string(out)), nil
}

func parseAgentKinds(help string) []string {
	match := possibleValues.FindStringSubmatch(help)
	if match == nil {
		return nil
	}
	var kinds []string
	for _, part := range strings.Split(match[1], ",") {
		if kind := strings.TrimSpace(part); kind != "" {
			kinds = append(kinds, kind)
		}
	}
	return kinds
}
