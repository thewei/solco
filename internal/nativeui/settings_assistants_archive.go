package nativeui

/**
 * [INPUT]: assistantsState cached archive rows, personalagent CLI and Settings row/button controls
 * [OUTPUT]: Settings → Archived Assistants / Archived Groups lists, restore, guarded Delete Forever, async refresh
 * [POS]: Archive management is a UI projection only; the plugin SQLite is authoritative.
 * [PROTOCOL]: Update this header and internal/nativeui/CLAUDE.md when behavior changes.
 */
import (
	"context"
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/thewei/solco/internal/personalagent"
)

func (s *Shell) loadArchivedAssistants(force bool) {
	st := &s.assistants
	if !s.assistantsAvailable() || st.archiveLoading || (!force && st.archiveLoaded) {
		return
	}
	c := s.assistantsClientFor()
	if c == nil {
		return
	}
	st.archiveLoading = true
	assistantsGo(func() {
		ctx, cancel := context.WithTimeout(context.Background(), assistantsCallTimeout)
		defer cancel()
		list, err := c.ArchivedAssistants(ctx)
		var groups []personalagent.Group
		if err == nil {
			groups, err = c.ArchivedGroups(ctx)
		}
		s.applyOnWindow(func() {
			st := &s.assistants
			st.archiveLoading = false
			if !s.assistantsAvailable() {
				return
			}
			if err != nil {
				st.archiveError = err.Error()
				return
			}
			st.archiveError = ""
			st.archived = list
			st.archivedGroups = groups
			st.archiveLoaded = true
		})
	})
}

func (s *Shell) runArchiveAction(action, id string) {
	if !s.assistantsAvailable() || s.assistants.archiveLoading {
		return
	}
	var found *personalagent.Assistant
	for i := range s.assistants.archived {
		if s.assistants.archived[i].ID == id {
			a := s.assistants.archived[i]
			found = &a
			break
		}
	}
	if found == nil {
		s.pendingToast = "Archived Assistant changed. Refresh the list before acting."
		return
	}
	client := s.assistantsClientFor()
	if client == nil {
		return
	}
	s.assistants.archiveLoading = true
	a := *found
	assistantsGo(func() {
		ctx, cancel := context.WithTimeout(context.Background(), assistantsCallTimeout)
		defer cancel()
		var err error
		switch action {
		case "restore":
			_, err = client.RestoreAssistant(ctx, fmt.Sprintf("restore-%s-%d", a.ID, a.Revision), a.ID, a.Revision)
		case "delete":
			err = client.DeleteAssistantForever(ctx, fmt.Sprintf("delete-%s-%d", a.ID, a.Revision), a.ID, a.Revision)
		default:
			err = fmt.Errorf("invalid archived Assistant action")
		}
		s.applyOnWindow(func() {
			s.assistants.archiveLoading = false
			s.assistants.archiveLoaded = false
			if err != nil {
				s.pendingToast = err.Error()
			} else {
				if action == "delete" {
					s.pendingToast = "Assistant permanently deleted"
				}
				if action == "restore" {
					s.pendingToast = "Assistant restored"
				}
			}
			s.loadArchivedAssistants(true)
			s.refreshAssistants(true)
		})
	})
}

// runGroupArchiveAction restores or permanently deletes one archived Group.
func (s *Shell) runGroupArchiveAction(action, id string) {
	if !s.assistantsAvailable() || s.assistants.archiveLoading {
		return
	}
	var found *personalagent.Group
	for i := range s.assistants.archivedGroups {
		if s.assistants.archivedGroups[i].ID == id {
			g := s.assistants.archivedGroups[i]
			found = &g
			break
		}
	}
	if found == nil {
		s.pendingToast = "Archived group changed. Refresh the list before acting."
		return
	}
	client := s.assistantsClientFor()
	if client == nil {
		return
	}
	s.assistants.archiveLoading = true
	g := *found
	assistantsGo(func() {
		ctx, cancel := context.WithTimeout(context.Background(), assistantsCallTimeout)
		defer cancel()
		var err error
		switch action {
		case "restore":
			_, err = client.RestoreGroup(ctx, fmt.Sprintf("group-restore-%s-%d", g.ID, g.Revision), g.ID, g.Revision)
		case "delete":
			err = client.DeleteGroupForever(ctx, fmt.Sprintf("group-delete-%s-%d", g.ID, g.Revision), g.ID, g.Revision)
		default:
			err = fmt.Errorf("invalid archived group action")
		}
		s.applyOnWindow(func() {
			s.assistants.archiveLoading = false
			s.assistants.archiveLoaded = false
			switch {
			case err != nil:
				s.pendingToast = explainAssistantsFailure(err).Error()
			case action == "delete":
				s.pendingToast = "Group permanently deleted"
			default:
				s.pendingToast = "Group restored"
			}
			s.loadArchivedAssistants(true)
			s.refreshAssistants(true)
		})
	})
}

// settingsArchivedAssistants never performs storage IO from the render lane.
func (s *Shell) settingsArchivedAssistants(c *ui.Context) {
	if s.assistantsAvailable() {
		s.loadArchivedAssistants(false)
	}
	st := &s.assistants
	note := "Archived Assistants keep their data and can be restored. Permanent deletion is irreversible and refuses while shared or running resources remain."
	if !s.assistantsAvailable() {
		note = "Assistants plugin is unavailable on this device."
	}
	rows := []settingsRow{
		row("Archived Assistants", note, func() {
			if compactButton(c, "Refresh").Clicked() {
				s.assistants.archiveLoaded = false
				s.loadArchivedAssistants(true)
			}
		}),
	}
	if st.archiveError != "" {
		rows = append(rows, row("Could not load archive", st.archiveError, func() {}))
	} else if st.archiveLoading && !st.archiveLoaded {
		rows = append(rows, row("Loading archive", "Reading archived Assistants…", func() {}))
	} else if st.archiveLoaded && len(st.archived) == 0 {
		rows = append(rows, row("No archived Assistants", "Archive an Assistant from its ⋯ menu to see it here.", func() {}))
	} else {
		for _, a := range st.archived {
			a := a
			rows = append(rows, row(a.Name, "All goals, memory, tasks and history are retained.", func() {
				ui.Row(c).Gap(Spacing().M).Children(func() {
					if compactButton(c, "Restore").Clicked() {
						s.runArchiveAction("restore", a.ID)
					}
					if compactButton(c, "Delete Forever").Clicked() {
						s.openConfirm("assistant-delete", a.ID, "Delete “"+a.Name+"”?",
							"This permanently removes the Assistant's private data. You must first settle Herdr sessions and remove any shared Groups, routines, and Git worktrees. This cannot be undone.")
					}
				})
			}))
		}
	}
	settingsGroup(c, "Archived Assistants", rows...)
	if st.archiveLoaded && len(st.archivedGroups) > 0 {
		groupRows := []settingsRow{}
		for _, g := range st.archivedGroups {
			g := g
			names := make([]string, 0, len(g.Members))
			for _, m := range g.Members {
				names = append(names, m.Name)
			}
			groupRows = append(groupRows, row(g.Title, "With "+strings.Join(names, ", ")+". The conversation is kept.", func() {
				ui.Row(c).Gap(Spacing().M).Children(func() {
					if compactButton(c, "Restore").Clicked() {
						s.runGroupArchiveAction("restore", g.ID)
					}
					if compactButton(c, "Delete Forever").Clicked() {
						s.openConfirm("group-delete", g.ID, "Delete “"+g.Title+"”?",
							"This permanently removes the group's messages and its agents' records. Close the group agents' Herdr panes first. The Assistants themselves are not touched. This cannot be undone.")
					}
				})
			}))
		}
		settingsGroup(c, "Archived Groups", groupRows...)
	}
}
