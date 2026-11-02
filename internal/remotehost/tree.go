package remotehost

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/thewei/solco/internal/herdr"
	"github.com/thewei/solco/internal/history"
)

/**
 * [INPUT]: 依赖 internal/herdr.Projection
 * [OUTPUT]: 对外提供 Tree/TreeWorkspace/TreeTab/TreePane/TreeAgent 线格式、BuildTree、Tree.hash、Tree.paneForTerminal
 * [POS]: Herdr 投影到 /api/v3/tree 的只读纯函数：全部不透明 ID、保持 Herdr 的顺序、绝不带「聚焦」字段（手机导航是客户端本地的）
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// Tree is the phone's read-only view of one Herdr session.
type Tree struct {
	Instance   string          `json:"instance"`
	Revision   uint64          `json:"revision"`
	Workspaces []TreeWorkspace `json:"workspaces"`
}

type TreeWorkspace struct {
	ID     string    `json:"id"`
	Label  string    `json:"label"`
	Status string    `json:"status"`
	CWD    string    `json:"cwd,omitempty"`
	Tabs   []TreeTab `json:"tabs"`
}

type TreeTab struct {
	ID     string     `json:"id"`
	Label  string     `json:"label"`
	Status string     `json:"status"`
	Panes  []TreePane `json:"panes"`
}

type TreePane struct {
	PaneID     string     `json:"paneId"`
	TerminalID string     `json:"terminalId"`
	Label      string     `json:"label,omitempty"`
	CWD        string     `json:"cwd,omitempty"`
	Agent      *TreeAgent `json:"agent,omitempty"`
}

type TreeAgent struct {
	Provider string `json:"provider"`
	Status   string `json:"status"`
	// Title is what the desktop sidebar calls this agent: its session title,
	// else its name, else the provider's display name. LastActiveMs is the
	// desktop's last-worked time (Unix ms), absent while unknown.
	Title        string `json:"title,omitempty"`
	LastActiveMs int64  `json:"lastActiveMs,omitempty"`
	// Background is what runs beside the agent: the sub-agents it spawned and
	// the service its pane serves, one list (2026-11-02, designs/mobile-projection).
	Background []TreeBackground `json:"background,omitempty"`
}

// TreeBackground is one such item. Kind "subagent": State working | done |
// stopped, ID the sub-agent's id (the Chat drawer's key), Title its type,
// Detail the task. Kind "service": State running, ID "svc:<paneId>", Title the
// process, Detail its ports. UpdatedMs is the last write (sub-agents only).
type TreeBackground struct {
	Kind      string `json:"kind"`
	ID        string `json:"id"`
	Title     string `json:"title"`
	Detail    string `json:"detail,omitempty"`
	State     string `json:"state"`
	UpdatedMs int64  `json:"updatedMs,omitempty"`
}

// AgentFact is the desktop's own resolved facts about one live agent, keyed by
// terminal ID. The Host never re-derives a title: it forwards what the desktop
// already worked out (nil for a Herdr session the desktop is not showing).
type AgentFact struct {
	Title      string
	LastActive time.Time
	Background []TreeBackground
}

// BuildTree maps a Herdr projection to the wire tree, keeping Herdr's order.
// Revision is left 0: the server assigns it when the content changes.
func BuildTree(instance string, p herdr.Projection) Tree { return buildTree(instance, p, nil) }

func buildTree(instance string, p herdr.Projection, facts map[string]AgentFact) Tree {
	tree := Tree{Instance: instance, Workspaces: []TreeWorkspace{}}
	tabsByProject := map[string][]herdr.Tab{}
	for _, t := range p.Tabs {
		tabsByProject[t.ProjectID] = append(tabsByProject[t.ProjectID], t)
	}
	panesByTab := map[string][]herdr.Pane{}
	for _, pane := range p.Panes {
		panesByTab[pane.TabID] = append(panesByTab[pane.TabID], pane)
	}
	for _, project := range p.Projects {
		ws := TreeWorkspace{ID: project.ID, Label: project.Label, Status: project.AgentStatus, CWD: project.CWD, Tabs: []TreeTab{}}
		for _, tab := range tabsByProject[project.ID] {
			tt := TreeTab{ID: tab.ID, Label: tab.Label, Status: tab.AgentStatus, Panes: []TreePane{}}
			for _, pane := range panesByTab[tab.ID] {
				tp := TreePane{PaneID: pane.ID, TerminalID: pane.TerminalID, Label: pane.Label, CWD: pane.CWD}
				if pane.Agent != "" {
					fact := facts[pane.TerminalID]
					tp.Agent = &TreeAgent{Provider: pane.Agent, Status: pane.AgentStatus, Title: agentTitle(fact.Title, pane.Agent)}
					if !fact.LastActive.IsZero() {
						tp.Agent.LastActiveMs = fact.LastActive.UnixMilli()
					}
					tp.Agent.Background = fact.Background
				}
				tt.Panes = append(tt.Panes, tp)
			}
			ws.Tabs = append(ws.Tabs, tt)
		}
		tree.Workspaces = append(tree.Workspaces, ws)
	}
	return tree
}

// agentTitle is the desktop's fallback chain as far as the Host can follow it:
// the desktop's resolved title, else the provider's display name.
func agentTitle(resolved, kind string) string {
	if title := strings.TrimSpace(resolved); title != "" {
		return title
	}
	if id, ok := history.ParseAgentID(kind); ok {
		return id.DisplayName()
	}
	return ""
}

// hash fingerprints the tree content (revision excluded) for change detection.
func (t Tree) hash() string {
	t.Revision = 0
	raw, _ := json.Marshal(t)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// paneForTerminal resolves a terminal ID to its pane ID; ok is false for an ID
// the session does not contain (so unknown strings never reach an argv).
func (t Tree) paneForTerminal(terminalID string) (string, bool) {
	for _, ws := range t.Workspaces {
		for _, tab := range ws.Tabs {
			for _, pane := range tab.Panes {
				if pane.TerminalID == terminalID && terminalID != "" {
					return pane.PaneID, true
				}
			}
		}
	}
	return "", false
}

func (t Tree) paneIDs() []string {
	var ids []string
	for _, ws := range t.Workspaces {
		for _, tab := range ws.Tabs {
			for _, pane := range tab.Panes {
				ids = append(ids, pane.PaneID)
			}
		}
	}
	return ids
}

func (t Tree) hasPane(paneID string) bool {
	for _, id := range t.paneIDs() {
		if id == paneID {
			return true
		}
	}
	return false
}
