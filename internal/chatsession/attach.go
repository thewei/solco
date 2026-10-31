package chatsession

import (
	"time"

	"github.com/thewei/solco/internal/conversation"
)

// AttachSubagents stamps SubagentIDs on the page steps whose tool call spawned
// a sub-agent and returns exactly those sub-agents (page order = first
// appearance). out and raw are the same turns before/after Turns(); a length
// mismatch (a racing projection) attaches nothing.
func AttachSubagents(out []ChatTurn, raw []conversation.TimelineTurn, views []View, now time.Time) []SubagentCard {
	if len(views) == 0 || len(raw) != len(out) {
		return nil
	}
	byID := make(map[string]View, len(views))
	for _, view := range views {
		byID[view.Sub.ID] = view
	}
	var cards []SubagentCard
	seen := map[string]bool{}
	for ti, turn := range raw {
		if len(turn.Steps) != len(out[ti].Steps) {
			continue
		}
		for si, step := range turn.Steps {
			ids, types := Linked(step.Tool, views)
			if len(ids) == 0 {
				continue
			}
			out[ti].Steps[si].SubagentIDs = ids
			for _, id := range ids {
				if seen[id] {
					continue
				}
				seen[id] = true
				cards = append(cards, Card(byID[id], types[id], step.Tool, now))
			}
		}
	}
	return cards
}
