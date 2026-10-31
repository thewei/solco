package remotehost

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/thewei/solco/internal/agentcatalog"
	"github.com/thewei/solco/internal/history"
)

/**
 * [INPUT]: 依赖 Backend.Projection（窗格的 agent 种类与 cwd）、internal/agentcatalog（各 Agent 自己的 Skills/命令/模型，只读）、server.go 的 guard/resolveInstance
 * [OUTPUT]: 对外提供 GET /api/v3/panes/{paneID}/chat/catalog——Chat 输入框的 `/` `$` 弹窗与模型菜单的数据（scope view）
 * [POS]: 手机 Chat 与桌面共用同一份目录；与会话是否已有转录无关（新开的窗格也能列），只靠 Herdr 报的 agent 种类与窗格 cwd；不写、不执行任何 Provider 命令；失败只回通用错误，目录内容不进日志
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// catalogEntry is one `/` or `$` row (agentcatalog.Entry on the wire).
type catalogEntry struct {
	Name   string `json:"name"`
	Insert string `json:"insert"`
	Detail string `json:"detail,omitempty"`
	// Kind is text | picker | terminal | skill | prompt (agentcatalog.Kind).
	Kind string `json:"kind"`
}

type catalogModel struct {
	ID string `json:"id"`
}

// catalogBody mirrors the desktop bridge's ChatCatalog plus the typed model
// verb the phone needs to switch a model itself.
type catalogBody struct {
	Commands []catalogEntry `json:"commands"`
	Skills   []catalogEntry `json:"skills"`
	Models   []catalogModel `json:"models"`
	// ModelVerb is the typed command that switches model for this session
	// ("/model ", "/switch "); empty when the agent only has a picker.
	ModelVerb    string `json:"modelVerb,omitempty"`
	ModelPicker  string `json:"modelPicker,omitempty"`
	DefaultModel string `json:"defaultModel,omitempty"`
}

// catalogLoader is agentcatalog.Load; tests swap it so no login shell runs.
type catalogLoader func(ctx context.Context, provider history.AgentID, root string) agentcatalog.Catalog

func (s *Server) handleChatCatalog(w http.ResponseWriter, r *http.Request, _ Device) {
	instance, ok := s.resolveInstance(w, r)
	if !ok {
		return
	}
	paneID := r.PathValue("paneID")
	proj, err := s.opts.Backend.Projection(instance)
	if err != nil {
		slog.Warn("remote host catalog: projection failed", "instance", instance, "error", err)
		writeError(w, http.StatusBadGateway, "backend_unavailable", "")
		return
	}
	var kind, cwd string
	found := false
	for _, p := range proj.Panes {
		if p.ID == paneID {
			kind, cwd, found = p.Agent, p.CWD, true
			break
		}
	}
	if !found {
		writeError(w, http.StatusNotFound, "unknown_pane", "")
		return
	}
	out := catalogBody{Commands: []catalogEntry{}, Skills: []catalogEntry{}, Models: []catalogModel{}}
	provider, known := history.ParseAgentID(kind)
	if !known {
		writeJSON(w, http.StatusOK, out)
		return
	}
	load := s.opts.Catalog
	if load == nil {
		load = agentcatalog.Load
	}
	cat := load(r.Context(), provider, cwd)
	rows := func(entries []agentcatalog.Entry) []catalogEntry {
		rows := make([]catalogEntry, 0, len(entries))
		for _, e := range entries {
			rows = append(rows, catalogEntry{Name: e.Name, Insert: e.Insert, Detail: e.Detail, Kind: string(e.Kind)})
		}
		return rows
	}
	out.Commands, out.Skills = rows(cat.Commands), rows(cat.Skills)
	for _, m := range cat.Models {
		out.Models = append(out.Models, catalogModel{ID: m.ID})
	}
	out.ModelVerb, out.ModelPicker, out.DefaultModel = cat.ModelVerb, cat.ModelPicker, cat.DefaultModel
	writeJSON(w, http.StatusOK, out)
}
