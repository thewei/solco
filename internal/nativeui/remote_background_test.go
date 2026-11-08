package nativeui

import (
	"testing"
	"time"

	"github.com/thewei/solco/internal/agent"
	"github.com/thewei/solco/internal/history"
)

/**
 * [INPUT]: Shell.serviceIndex、agent.AgentCardModel（子代理）
 * [OUTPUT]: 手机列表的 Background 清单（子代理状态映射 + 面板服务）与桌面嵌套服务行的可见条件回归
 * [POS]: 2026-11-02 子代理 + 后台服务合并（design: designs/mobile-projection 议题 8/9）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

func TestRemoteBackgroundMergesSubagentsAndService(t *testing.T) {
	now := time.Now()
	s := NewShell()
	s.serviceIndex = sidebarServiceIndex{Panes: map[string]paneServiceActivity{"p1": {Proc: "node", Ports: []uint16{3000, 5173}}}}
	card := agent.AgentCardModel{PaneID: "p1", Subagents: []history.Subagent{
		{ID: "a", Type: "Explore", Description: "find it", UpdatedAt: now},
		{ID: "b", Type: "", UpdatedAt: now.Add(-time.Hour), Ended: true},
		{ID: "c", Type: "Plan", UpdatedAt: now.Add(-time.Hour), Ended: true, Stopped: true},
	}}
	got := s.remoteBackground(card, now)
	if len(got) != 4 {
		t.Fatalf("background = %+v", got)
	}
	want := []struct{ kind, id, state string }{
		{"subagent", "a", "working"}, {"subagent", "b", "done"}, {"subagent", "c", "stopped"}, {"service", "svc:p1", "running"},
	}
	for i, w := range want {
		if got[i].Kind != w.kind || got[i].ID != w.id || got[i].State != w.state {
			t.Fatalf("row %d = %+v, want %+v", i, got[i], w)
		}
	}
	if got[1].Title != "Sub-agent" || got[3].Title != "node" || got[3].Detail != ":3000, :5173" {
		t.Fatalf("titles = %+v", got)
	}
	if none := s.remoteBackground(agent.AgentCardModel{PaneID: "other"}, now); len(none) != 0 {
		t.Fatalf("a card with nothing running must list nothing: %+v", none)
	}
}

func TestCardServiceHidesWhenTheParentIsFolded(t *testing.T) {
	s := NewShell()
	s.serviceIndex = sidebarServiceIndex{Panes: map[string]paneServiceActivity{"p1": {Proc: "bun", Ports: []uint16{3000}}}}
	card := agent.AgentCardModel{PaneID: "p1", Subagents: []history.Subagent{{ID: "a", UpdatedAt: time.Now(), Ended: true}}}
	if _, ok := s.cardService(card); !ok {
		t.Fatal("a service in the agent's pane must show as a nested row")
	}
	s.foldedAgents = map[string]bool{subagentFoldKey(card): true}
	if _, ok := s.cardService(card); ok {
		t.Fatal("folding the parent folds its service too")
	}
}
