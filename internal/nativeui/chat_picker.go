package nativeui

import (
	"context"
	"log/slog"
	"time"

	"github.com/thewei/solco/internal/agentcatalog"
	"github.com/thewei/solco/internal/chatsession"
	"github.com/thewei/solco/internal/conversation"
)

// pickerClose is the card's own row for the picker's Escape.
const pickerClose = chatsession.PickerClose

// detectPicker is chatsession.DetectPicker (shared with the phone Host).
func detectPicker(screen string) (conversation.PendingInteraction, bool) {
	return chatsession.DetectPicker(screen)
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
