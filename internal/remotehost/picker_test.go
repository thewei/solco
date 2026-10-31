package remotehost

import (
	"strings"
	"testing"
	"time"
)

const claudeModelPickerScreen = `   Select model
   Switch between Claude models.

     1.  Default (recommended)            Use the default model
   ❯ 2.  Opus                             Most capable
     3.  Haiku                            Fastest

   Enter to set as default · s to use this session only · Esc to cancel
`

func (h *harness) setScreen(screen string) {
	h.back.mu.Lock()
	h.back.screen = screen
	h.back.mu.Unlock()
}

func TestChatShowsAPickerCardAfterAPickerCommandAndRetiresItWhenItLeaves(t *testing.T) {
	h, _ := chatHarness(t)
	h.setBlockedStatus("idle")
	h.setScreen(claudeModelPickerScreen)
	if h.card() != nil {
		t.Fatal("a picker on screen must not become a card before the Host sent the command")
	}
	if code, got := h.post(sendPath, h.token, `{"text":"/model"}`); code != 200 {
		t.Fatalf("send = %d %s", code, got)
	}
	c := h.card()
	if c == nil || !strings.HasPrefix(c.Prompt, "Select model") || strings.Join(c.Options, "|") != "Default (recommended)|Opus|Haiku|Close" {
		t.Fatalf("card = %+v", c)
	}
	if code, got := h.post(answerPath, h.token, `{"interactionId":"`+c.ID+`","option":"Haiku"}`); code != 200 {
		t.Fatalf("answer = %d %s", code, got)
	}
	_, keys := h.sent()
	if len(keys) != 1 || strings.Join(keys[0].keys, ",") != "down,s" {
		t.Fatalf("keys = %v (from the highlighted Opus one down, then the commit key `s`: this session only)", keys)
	}
	h.setScreen("● model set\n")
	time.Sleep(screenReadEvery + 50*time.Millisecond)
	if h.card() != nil {
		t.Fatal("the card must retire once the picker is off the screen")
	}
	// An answer re-arms the window for a follow-up list; it lapses on its own.
	cur := h.srv.askFor("default", "w1:p1")
	cur.mu.Lock()
	cur.pickerUntil = time.Now().Add(-time.Second)
	cur.mu.Unlock()
	if h.srv.pickerWatching("default", "w1:p1") {
		t.Fatal("watch should be over after the follow-up window lapsed")
	}
}

func TestChatPickerWatchEndsWhenNoPickerAppears(t *testing.T) {
	h, _ := chatHarness(t)
	h.setBlockedStatus("idle")
	h.setScreen("plain output\n")
	if code, _ := h.post(sendPath, h.token, `{"text":"/model"}`); code != 200 {
		t.Fatal("send failed")
	}
	if !h.srv.pickerWatching("default", "w1:p1") {
		t.Fatal("the command should open a watch")
	}
	cur := h.srv.askFor("default", "w1:p1")
	cur.mu.Lock()
	cur.pickerUntil = time.Now().Add(-time.Second)
	cur.mu.Unlock()
	if h.srv.pickerWatching("default", "w1:p1") || h.card() != nil {
		t.Fatal("the watch must lapse after the appear window")
	}
}

func TestChatPlainTextNeverOpensAPickerWatch(t *testing.T) {
	h, _ := chatHarness(t)
	h.setBlockedStatus("idle")
	h.setScreen(claudeModelPickerScreen)
	if code, _ := h.post(sendPath, h.token, `{"text":"explain /model"}`); code != 200 {
		t.Fatal("send failed")
	}
	if h.srv.pickerWatching("default", "w1:p1") || h.card() != nil {
		t.Fatal("only an exact picker command arms the watch")
	}
}
