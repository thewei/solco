package remotehost

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/thewei/solco/internal/herdr"
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
}

// BuildTree maps a Herdr projection to the wire tree, keeping Herdr's order.
// Revision is left 0: the server assigns it when the content changes.
func BuildTree(instance string, p herdr.Projection) Tree {
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
					tp.Agent = &TreeAgent{Provider: pane.Agent, Status: pane.AgentStatus}
				}
				tt.Panes = append(tt.Panes, tp)
			}
			ws.Tabs = append(ws.Tabs, tt)
		}
		tree.Workspaces = append(tree.Workspaces, ws)
	}
	return tree
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
