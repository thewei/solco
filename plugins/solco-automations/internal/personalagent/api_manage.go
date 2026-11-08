package personalagent

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// The Primary Assistant's management actions (2026-11-07). The person talks to
// the Primary; "make an Assistant that watches my inbox", "archive the travel
// one", "ask the research Assistant to look into X" are all said there. The
// Primary's Coordinator reaches the rest through its one broker tool as
// `manage.*`, and the core below is the only judge:
//
//   - only the Primary (primary.go) may call any of it; every other Coordinator
//     gets `forbidden`;
//   - it can list, create, rename, archive and restore Assistants — never
//     delete one forever, never touch the Primary itself, never approve
//     anything on a person's behalf;
//   - `manage.group` opens a Group chat (title + at least two Assistants, the Primary
//     included) — the desktop and phone no longer list Groups as their own entry, so
//     "start a group of A and B" is said to the Primary;
//   - "hand this to Assistant B" is `manage.delegate`: an ordinary Task owned by
//     B (so B's role, memory and limits shape the worker, and a coding task is
//     still a draft a person publishes). It is durable and idempotent, and it
//     works while B's own Coordinator is not running; B's results show up in B's
//     Tasks and inbox, which the Primary reads with `manage.tasks`.
//
// Everything is the same store the desktop and phone use, so nothing here can
// become a second registry.

var primaryScope = map[string]bool{
	"manage list": true, "manage create": true, "manage rename": true,
	"manage archive": true, "manage restore": true,
	"manage delegate": true, "manage tasks": true, "manage group": true,
}

const (
	maxManagedName = 80
	maxManagedDesc = 1000
)

// ManageActions lists the primary-only commands for the extension/core parity test.
func ManageActions() []string {
	out := make([]string, 0, len(primaryScope))
	for k := range primaryScope {
		out = append(out, k)
	}
	return out
}

type managedRow struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Status      string `json:"status"`
	Primary     bool   `json:"primary,omitempty"`
	Coordinator string `json:"coordinator"`
}

// managed runs one `manage <sub>` command for the caller `who`.
func (a *API) managed(who, sub string, body []byte) (any, error) {
	if !a.Store.IsPrimary(who) {
		return nil, forbidden("only the Primary Assistant can manage other Assistants")
	}
	switch sub {
	case "list":
		return a.manageList()
	case "create":
		var in struct {
			common
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		if in.RequestID == "" {
			return nil, coded("invalid", "a stable requestId is required")
		}
		in.Name, in.Description = strings.TrimSpace(in.Name), strings.TrimSpace(in.Description)
		if in.Name == "" || utf8.RuneCountInString(in.Name) > maxManagedName {
			return nil, coded("invalid", "name is required (at most %d characters)", maxManagedName)
		}
		if utf8.RuneCountInString(in.Description) > maxManagedDesc {
			return nil, coded("invalid", "description is at most %d characters", maxManagedDesc)
		}
		x, replayed, err := a.Store.CreateAssistant(in.RequestID, CreateAssistantInput{Name: in.Name, Description: in.Description})
		return ok("assistant", x, "replayed", replayed), err
	case "rename":
		var in struct {
			common
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		if in.RequestID == "" {
			return nil, coded("invalid", "a stable requestId is required")
		}
		if utf8.RuneCountInString(strings.TrimSpace(in.Name)) > maxManagedName {
			return nil, coded("invalid", "name is at most %d characters", maxManagedName)
		}
		cur, err := a.manageTarget(in.ID, false)
		if err != nil {
			return nil, err
		}
		x, replayed, err := a.Store.RenameAssistant(in.RequestID, in.ID, in.Name, cur.Revision)
		return ok("assistant", x, "replayed", replayed), err
	case "archive", "restore":
		var in struct {
			common
			ID string `json:"id"`
		}
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		if in.RequestID == "" {
			return nil, coded("invalid", "a stable requestId is required")
		}
		cur, err := a.Store.GetAssistant(in.ID)
		if err != nil || cur.System {
			return nil, forbidden("this is not an Assistant you can manage")
		}
		if sub == "archive" {
			x, replayed, err := a.Store.ArchiveAssistant(in.RequestID, in.ID, cur.Revision, a.PaneGone)
			return ok("assistant", x, "replayed", replayed), err
		}
		x, replayed, err := a.Store.RestoreAssistant(in.RequestID, in.ID, cur.Revision)
		return ok("assistant", x, "replayed", replayed), err
	case "delegate":
		var in struct {
			common
			ID            string   `json:"id"`
			Kind          string   `json:"kind"`
			Title         string   `json:"title"`
			Brief         string   `json:"brief"`
			RepoPath      string   `json:"repoPath"`
			RequiredSkill []string `json:"requiredSkills"`
		}
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		if in.RequestID == "" {
			return nil, coded("invalid", "a stable requestId is required")
		}
		if _, err := a.manageTarget(in.ID, true); err != nil {
			return nil, err
		}
		switch in.Kind {
		case KindResearch, KindCoding, KindManual:
		default:
			return nil, coded("invalid", "kind must be research, coding or manual")
		}
		m := map[string]any{
			"requestId": in.RequestID, "assistantId": in.ID, "kind": in.Kind,
			"title": in.Title, "brief": in.Brief, "agent": "omp",
			// A coding task is always a draft a person publishes.
			"draft": in.Kind == KindCoding,
		}
		if in.RepoPath != "" {
			m["repoPath"] = in.RepoPath
		}
		if len(in.RequiredSkill) > 0 {
			m["requiredSkills"] = in.RequiredSkill
		}
		raw, _ := json.Marshal(m)
		raw, err := a.guardDuplicateTask(raw)
		if err != nil {
			return nil, err
		}
		return a.handle([]string{"assistant", "task", "create"}, raw)
	case "group":
		var in struct {
			common
			Title   string   `json:"title"`
			Members []string `json:"members"`
		}
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		if in.RequestID == "" {
			return nil, coded("invalid", "a stable requestId is required")
		}
		for _, id := range in.Members {
			if x, err := a.Store.GetAssistant(strings.TrimSpace(id)); err != nil || x.System {
				return nil, forbidden("this is not an Assistant you can put in a group")
			}
		}
		g, replayed, err := a.Store.CreateGroup(in.RequestID, in.Title, in.Members)
		return ok("group", g, "replayed", replayed), err
	case "tasks":
		var in struct {
			ID string `json:"id"`
		}
		if err := decode(body, &in); err != nil {
			return nil, err
		}
		if _, err := a.manageTarget(in.ID, false); err != nil {
			return nil, err
		}
		return a.handle([]string{"assistant", "task", "list", "--assistant", in.ID}, nil)
	}
	return nil, forbidden("manage %s is not available", sub)
}

// manageTarget loads an Assistant the Primary may act on: an ordinary one that
// exists and is not archived (and, when needActive, is not paused either).
func (a *API) manageTarget(id string, needActive bool) (Assistant, error) {
	x, err := a.Store.GetAssistant(id)
	if err != nil || x.System || x.Status == StatusArchived {
		return Assistant{}, forbidden("this is not an Assistant you can manage")
	}
	if needActive && x.Status != StatusActive {
		return Assistant{}, coded("bad_state", "that Assistant is paused; ask the person to resume it first")
	}
	return x, nil
}

func (a *API) manageList() (any, error) {
	live, err := a.Store.ListAssistants()
	if err != nil {
		return nil, err
	}
	archived, err := a.Store.ListArchivedAssistants()
	if err != nil {
		return nil, err
	}
	rows := make([]managedRow, 0, len(live)+len(archived))
	for _, x := range append(live, archived...) {
		if x.System {
			continue
		}
		state := CoordinatorNone
		if c, err := a.Store.GetCoordinator(x.ID); err == nil && c.State != "" {
			state = c.State
		}
		rows = append(rows, managedRow{ID: x.ID, Name: x.Name, Description: clip(x.Description, 200), Status: x.Status, Primary: x.Primary, Coordinator: state})
	}
	return ok("assistants", rows), nil
}
