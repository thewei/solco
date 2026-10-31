package nativeui

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/thewei/solco/internal/agentcatalog"
	"github.com/thewei/solco/internal/conversation"
)

// Chat structures the numbered list pickers of Claude Code and Codex
// (/model, /permissions) as a Question card. Only Solco opens them — right
// after the user sent the command from the composer — so the screen read is
// never a guess about an arbitrary agent prompt. Pickers it cannot read
// (pi/omp lists, /resume search boxes) hand over to Terminal instead.

// pickerRow matches a numbered picker row: indent, optional selector mark
// (❯ › > →) or scroll arrow (↓ ↑), number, text.
var pickerRow = regexp.MustCompile(`^(\s*)([❯›>→↓↑]?)\s*(\d+)\.\s+(.*?)\s*$`)

// pickerFooter is the key hint every such picker ends with
// ("enter select · esc back", "Enter to set as default · … · Esc to cancel").
var pickerFooter = regexp.MustCompile(`(?i)\benter\b.*\besc\b`)

var pickerTitle = regexp.MustCompile(`^\s*(Select|Choose|Update|Switch|Set|Pick)\b`)

// pickerSessionOnly spots the footer key that applies a choice to this
// session only instead of saving it as the default ("s to use this session
// only" in Claude Code, "s session" in Codex's effort step).
var pickerSessionOnly = regexp.MustCompile(`(?i)\bs (?:to use this )?session(?: only)?\b`)

var pickerGap = regexp.MustCompile(`\s{2,}`)

// pickerClose is the card's own row for the picker's Escape.
const pickerClose = "Close"

// detectPicker reads a visible screen for a numbered list picker.
func detectPicker(screen string) (conversation.PendingInteraction, bool) {
	lines := strings.Split(strings.ReplaceAll(screen, "\r\n", "\n"), "\n")
	footer := -1
	for index := len(lines) - 1; index >= 0; index-- {
		if pickerFooter.MatchString(lines[index]) {
			footer = index
			break
		}
	}
	// Some picker steps carry no key hint at all (Claude Code's "Switch
	// model?" confirmation is two rows); then the rows must end the screen
	// and one of them must carry the selector mark.
	hinted := footer >= 0
	if !hinted {
		footer = len(lines)
		for footer > 0 && strings.TrimSpace(lines[footer-1]) == "" {
			footer--
		}
	}
	// The last run of rows above the footer; a gap of more than four lines
	// ends it (conversation text above may contain its own "1. foo").
	var rows []int
	for index := footer - 1; index >= 0; index-- {
		if !pickerRow.MatchString(lines[index]) {
			continue
		}
		if len(rows) > 0 && rows[0]-index > 4 {
			break
		}
		rows = append([]int{index}, rows...)
		if len(rows) > 40 {
			break
		}
	}
	if len(rows) < 2 {
		return conversation.PendingInteraction{}, false
	}
	if !hinted {
		marked := false
		for _, index := range rows {
			marked = marked || strings.ContainsAny(pickerRow.FindStringSubmatch(lines[index])[2], "❯›>→")
		}
		if !marked || footer-rows[len(rows)-1] > 3 {
			return conversation.PendingInteraction{}, false
		}
	}
	var interaction conversation.PendingInteraction
	interaction.Kind = conversation.InteractionQuestion
	interaction.Prompt = "Choose an option"
	for back := rows[0] - 1; back >= 0 && back >= rows[0]-12; back-- {
		text := strings.TrimSpace(lines[back])
		if strings.HasPrefix(text, "▔") || strings.HasPrefix(text, "─") {
			break
		}
		if pickerTitle.MatchString(text) {
			interaction.Prompt = text // keep walking: the top-most match is the title
		}
	}
	// A pane shorter than the list clips it; say so instead of passing a
	// partial list off as the whole one.
	partial := pickerRow.FindStringSubmatch(lines[rows[0]])[3] != "1"
	for _, index := range rows {
		partial = partial || strings.ContainsAny(pickerRow.FindStringSubmatch(lines[index])[2], "↓↑")
	}
	for _, line := range lines[rows[len(rows)-1]+1 : footer] {
		partial = partial || strings.HasPrefix(strings.TrimSpace(line), "…")
	}
	if partial {
		interaction.Prompt += " (rows beyond the screen: use Terminal)"
	}
	seen := map[string]int{}
	for position, index := range rows {
		match := pickerRow.FindStringSubmatch(lines[index])
		if strings.ContainsAny(match[2], "❯›>→") && interaction.Start == 0 {
			interaction.Start = position
		}
		label, desc := match[4], ""
		descCol := 0
		if parts := pickerGap.Split(label, 2); len(parts) == 2 {
			label, desc = parts[0], parts[1]
			descCol = utf8.RuneCountInString(lines[index][:strings.LastIndex(lines[index], desc)])
		}
		end := footer
		if position+1 < len(rows) {
			end = rows[position+1]
		}
		for _, line := range lines[index+1 : end] {
			indent := utf8.RuneCountInString(line) - utf8.RuneCountInString(strings.TrimLeft(line, " "))
			text := strings.TrimSpace(line)
			if text == "" || descCol == 0 || indent < descCol-2 {
				continue
			}
			desc = strings.TrimSpace(desc + " " + text)
		}
		seen[label]++
		interaction.Options = append(interaction.Options, label)
		interaction.Descriptions = append(interaction.Descriptions, desc)
	}
	// Duplicate labels would make an answer ambiguous: the description (or
	// the position) tells them apart.
	count := map[string]int{}
	for index, label := range interaction.Options {
		if seen[label] < 2 {
			continue
		}
		count[label]++
		if desc := interaction.Descriptions[index]; desc != "" {
			interaction.Options[index] = label + " · " + desc
		} else {
			interaction.Options[index] = fmt.Sprintf("%s #%d", label, count[label])
		}
	}
	interaction.Options = append(interaction.Options, pickerClose)
	interaction.Descriptions = append(interaction.Descriptions, "")
	interaction.EscapeOption = pickerClose
	interaction.Picker = true
	if pickerSessionOnly.MatchString(lines[footer]) {
		interaction.Commit = "s"
	}
	return interaction, true
}

// pickerWatch bounds: the picker has to appear within pickerAppear; once
// shown the card is re-checked every pickerRecheck until the picker is gone.
const (
	pickerAppear  = 6 * time.Second
	pickerRecheck = 1500 * time.Millisecond
	pickerPoll    = 300 * time.Millisecond
	pickerMaxLife = 10 * time.Minute
)

// watchPicker waits for the picker the agent was just told to open, publishes
// it as a card and retires the card once the picker leaves the screen
// (answered in the page or in the terminal, or cancelled). settle delays the
// first read (after an answer the follow-up screen needs a beat). It reads
// the pane and never answers by itself.
func (s *Shell) watchPicker(paneID string, settle time.Duration) {
	runtime, instance := s.runtime, s.activeInstance
	if runtime == nil || instance == "" || paneID == "" {
		return
	}
	read := func() (conversation.PendingInteraction, bool) {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		screen, err := runtime.ReadPane(ctx, instance, paneID, "visible")
		if err != nil {
			return conversation.PendingInteraction{}, false
		}
		return detectPicker(screen)
	}
	go func() {
		time.Sleep(settle)
		deadline := time.Now().Add(pickerAppear)
		var found conversation.PendingInteraction
		for {
			if picked, ok := read(); ok {
				found = picked
				break
			}
			if time.Now().After(deadline) {
				slog.Info("picker watch found no picker", "pane", paneID)
				return
			}
			time.Sleep(pickerPoll)
		}
		var id string
		s.callOnWindow(func() {
			if len(s.interactionBroker.PendingForPane(paneID)) == 0 {
				s.publishInteraction(paneID, found)
				for _, pending := range s.interactionBroker.PendingForPane(paneID) {
					id = pending.ID
				}
			}
		})
		if id == "" {
			return
		}
		for end := time.Now().Add(pickerMaxLife); time.Now().Before(end); {
			time.Sleep(pickerRecheck)
			var pending bool
			s.callOnWindow(func() {
				for _, interaction := range s.interactionBroker.PendingForPane(paneID) {
					pending = pending || interaction.ID == id
				}
			})
			if !pending {
				return
			}
			if _, ok := read(); !ok {
				s.callOnWindow(func() {
					s.interactionBroker.CancelForPane(paneID)
					s.publishChatBridge()
				})
				return
			}
		}
	}()
}

// chatCommandKind is what Chat does with a sent text on the bound pane's
// agent: agentcatalog.KindPicker, KindTerminal, or "" for an ordinary prompt.
func (s *Shell) chatCommandKind(text string) agentcatalog.Kind {
	card, ok := s.agentCardForPane(s.chatPaneID)
	if !ok {
		return ""
	}
	return agentcatalog.Classify(card.Provider, text)
}

// afterChatSend runs the picker follow-up for a command the composer just
// delivered: picker commands open a card, terminal-only ones switch the
// surface. It reports nothing for ordinary prompts.
func (s *Shell) afterChatSend(text string) {
	card, ok := s.agentCardForPane(s.chatPaneID)
	if !ok {
		return
	}
	switch agentcatalog.Classify(card.Provider, text) {
	case agentcatalog.KindPicker:
		s.watchPicker(s.chatPaneID, 400*time.Millisecond)
	case agentcatalog.KindTerminal:
		s.showSurface(WorkspaceSurfaceTerminal)
	}
}
