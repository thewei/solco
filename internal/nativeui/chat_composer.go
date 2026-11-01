package nativeui

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/thewei/solco/internal/agentcatalog"
	"github.com/thewei/solco/internal/chatattach"
	"github.com/thewei/solco/internal/history"
)

// ChatCatalogEntry is one `/` or `$` popover row.
type ChatCatalogEntry struct {
	Name   string `json:"name"`
	Insert string `json:"insert"`
	Detail string `json:"detail,omitempty"`
	// Kind is text | picker | terminal | skill | prompt (agentcatalog.Kind).
	Kind string `json:"kind"`
}

// ChatCatalogEffort is one thinking-effort choice: Send is the exact prompt
// text that applies it (the agent's own typed command).
type ChatCatalogEffort struct {
	Label string `json:"label"`
	Send  string `json:"send"`
}

// ChatCatalogModel is one model the chip can switch to. Efforts are the
// model's own levels; Pick, when set, is the exact prompt text that selects
// the row (Antigravity families select their first variant).
type ChatCatalogModel struct {
	ID      string              `json:"id"`
	Efforts []ChatCatalogEffort `json:"efforts,omitempty"`
	Pick    string              `json:"pick,omitempty"`
}

// ChatCatalog is the composer's menu for the bound Agent.
type ChatCatalog struct {
	Commands []ChatCatalogEntry `json:"commands"`
	Skills   []ChatCatalogEntry `json:"skills"`
	Models   []ChatCatalogModel `json:"models"`
	// CanSwitchModel: a typed command or a structured picker can switch it.
	CanSwitchModel bool `json:"canSwitchModel"`
	// ModelPicker is the command ("/model") that opens the picker card, "" if none.
	ModelPicker string `json:"modelPicker,omitempty"`
	// DefaultModel is the model a fresh session starts on (the agent's own
	// config); the chip shows it until a transcript records a model.
	DefaultModel string `json:"defaultModel,omitempty"`
	// ModelNote warns that a switch has a side effect (Antigravity saves it as
	// the default model); the model panel shows it above the list.
	ModelNote string `json:"modelNote,omitempty"`
	// Efforts are the thinking-effort choices that do not depend on the model
	// (Claude Code); a model's own Efforts win for it. EffortNote warns that
	// applying one is saved by the agent.
	Efforts    []ChatCatalogEffort `json:"efforts,omitempty"`
	EffortNote string              `json:"effortNote,omitempty"`
}

// chatProvider reports the bound pane's Agent and the project root.
func (s *Shell) chatProvider() (provider history.AgentID, root string) {
	if s.chatReadOnly {
		return "", ""
	}
	if card, ok := s.agentCardForPane(s.chatPaneID); ok {
		provider = card.Provider
	}
	if s.git != nil {
		root = s.git.root
	}
	return provider, root
}

// Catalog lists the Skills, commands and models the composer can offer. It
// reads the provider's own files (and pi/omp's read-only model listing, cached
// ten minutes) and executes no command.
func (b *ChatBridge) Catalog(ctx context.Context) (ChatCatalog, error) {
	var provider history.AgentID
	var root string
	b.shell.callOnWindow(func() { provider, root = b.shell.chatProvider() })
	out := ChatCatalog{Commands: []ChatCatalogEntry{}, Skills: []ChatCatalogEntry{}, Models: []ChatCatalogModel{}}
	if provider == "" {
		return out, nil
	}
	cat := agentcatalog.Load(ctx, provider, root)
	rows := func(entries []agentcatalog.Entry) []ChatCatalogEntry {
		rows := make([]ChatCatalogEntry, 0, len(entries))
		for _, entry := range entries {
			rows = append(rows, ChatCatalogEntry{Name: entry.Name, Insert: entry.Insert, Detail: entry.Detail, Kind: string(entry.Kind)})
		}
		return rows
	}
	out.Commands, out.Skills = rows(cat.Commands), rows(cat.Skills)
	for _, model := range cat.Models {
		out.Models = append(out.Models, ChatCatalogModel{ID: model.ID, Efforts: chatEfforts(model.Efforts), Pick: model.Pick})
	}
	out.ModelPicker = cat.ModelPicker
	out.DefaultModel = cat.DefaultModel
	out.ModelNote = cat.ModelNote
	out.Efforts, out.EffortNote = chatEfforts(cat.Efforts), cat.EffortNote
	out.CanSwitchModel = (cat.ModelVerb != "" && len(cat.Models) > 0) || cat.ModelPicker != "" || len(out.Efforts) > 0
	return out, nil
}

func chatEfforts(efforts []agentcatalog.Effort) []ChatCatalogEffort {
	var out []ChatCatalogEffort
	for _, effort := range efforts {
		out = append(out, ChatCatalogEffort{Label: effort.Label, Send: effort.Send})
	}
	return out
}

// SaveAttachment stores a pasted or dropped file under the cache dir's ChatAttachments (kept
// 14 days, 200 MB) and returns its absolute path for the `@path` mention.
func (b *ChatBridge) SaveAttachment(ctx context.Context, dataBase64, mime, name string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(dataBase64)
	if err != nil {
		return "", errors.New("malformed attachment")
	}
	var dir string
	b.shell.callOnWindow(func() { dir = b.shell.userDataDir })
	cache, err := chatattach.Dir(dir)
	if err != nil {
		return "", err
	}
	return chatattach.Save(cache, data, mime, name, time.Now())
}

// SwitchModel sends the provider's own typed model command (pi `/model`, omp
// `/switch`: both session-only) through the ordinary prompt path.
func (b *ChatBridge) SwitchModel(ctx context.Context, id string) (ChatBridgeState, error) {
	id = strings.TrimSpace(id)
	if id == "" || strings.ContainsAny(id, "\r\n") {
		return b.shell.chatBridgeStateOnWindow(), errors.New("invalid model")
	}
	var verb string
	b.shell.callOnWindow(func() {
		provider, _ := b.shell.chatProvider()
		verb = agentcatalog.ModelVerb(provider)
	})
	if verb == "" {
		return b.shell.chatBridgeStateOnWindow(), errors.New("this agent switches model through its picker")
	}
	return b.Send(ctx, verb+id)
}
