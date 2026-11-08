package personalagent

import (
	"bytes"
	"fmt"
	"log"
	"strings"
	"testing"
)

// notesStore opens a store whose memory folders live in a temp dir.
func notesStore(t *testing.T) *Store {
	t.Helper()
	t.Setenv("SOLCO_ASSISTANTS_MEMORY", t.TempDir())
	s, _ := openTest(t)
	return s
}

func writeBoth(t *testing.T, s *Store, id, user, asst string) {
	t.Helper()
	n, err := s.ReadNotes(id)
	if err != nil {
		t.Fatal(err)
	}
	if user != "" {
		if _, err := s.WriteNotes(id, NotesUser, user, n.User.Rev); err != nil {
			t.Fatal(err)
		}
	}
	if asst != "" {
		if _, err := s.WriteNotes(id, NotesAssistant, asst, n.Assistant.Rev); err != nil {
			t.Fatal(err)
		}
	}
}

// A context is the person's file and the Assistant's file merged, under each
// Assistant's own folder — never another Assistant's.
func TestContextMergesBothFilesAndNeverAnotherAssistants(t *testing.T) {
	s := notesStore(t)
	a := mustAssistant(t, s, "a1", "Ada")
	b := mustAssistant(t, s, "a2", "Bob")
	writeBoth(t, s, a.ID, "I like short summaries.", "- Ada keeps the digest on Monday")
	writeBoth(t, s, b.ID, "Bob's secret preference", "- Bob private fact")
	c, err := s.BuildContext(a.ID, ContextRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"I like short summaries.", "Ada keeps the digest on Monday", "USER.md", "ASSISTANT.md"} {
		if !strings.Contains(c.Text, want) {
			t.Fatalf("context lacks %q:\n%s", want, c.Text)
		}
	}
	for _, bad := range []string{"Bob"} {
		if strings.Contains(c.Text, bad) {
			t.Fatalf("context leaks %q:\n%s", bad, c.Text)
		}
	}
	// An edit is in effect for the very next context.
	n, _ := s.ReadNotes(a.ID)
	if _, err := s.WriteNotes(a.ID, NotesUser, "I like long summaries.", n.User.Rev); err != nil {
		t.Fatal(err)
	}
	c, _ = s.BuildContext(a.ID, ContextRequest{})
	if strings.Contains(c.Text, "short") || !strings.Contains(c.Text, "long summaries") {
		t.Fatalf("after the edit:\n%s", c.Text)
	}
	// No memory at all is no section, not a header over nothing.
	e := mustAssistant(t, s, "a3", "Eve")
	if c, err := s.BuildContext(e.ID, ContextRequest{}); err != nil || c.Text != "" {
		t.Fatalf("empty memory: %q %v", c.Text, err)
	}
}

// The context is bounded by its budget: the person's file reads from the top,
// the Assistant's file keeps its newest lines, and the budget is clamped.
func TestContextBudgetIsBounded(t *testing.T) {
	s := notesStore(t)
	a := mustAssistant(t, s, "a1", "Ada")
	var user, asst strings.Builder
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&user, "user line %d %s\n", i, strings.Repeat("filler ", 10))
		fmt.Fprintf(&asst, "- assistant line %d %s\n", i, strings.Repeat("filler ", 10))
	}
	writeBoth(t, s, a.ID, user.String(), asst.String())
	c, err := s.BuildContext(a.ID, ContextRequest{BudgetTokens: 600})
	if err != nil {
		t.Fatal(err)
	}
	if c.Tokens > 600 || !c.Truncated {
		t.Fatalf("tokens = %d truncated=%v over budget 600", c.Tokens, c.Truncated)
	}
	if !strings.Contains(c.Text, "user line 0 ") || strings.Contains(c.Text, "user line 199 ") {
		t.Fatalf("the person's file is read from the top:\n%s", c.Text)
	}
	if !strings.Contains(c.Text, "assistant line 199 ") || strings.Contains(c.Text, "assistant line 0 ") {
		t.Fatalf("the Assistant's file keeps its newest lines:\n%s", c.Text)
	}
	c, _ = s.BuildContext(a.ID, ContextRequest{BudgetTokens: 10_000_000})
	if c.Tokens > MaxContextTokens {
		t.Fatalf("clamp failed: %d", c.Tokens)
	}
	c, _ = s.BuildContext(a.ID, ContextRequest{})
	if c.Tokens == 0 || c.Tokens > DefaultContextTokens {
		t.Fatalf("default budget: %d", c.Tokens)
	}
}

// Credentials are refused on write, withheld from a context if one got into a
// file by hand, and never reach the log.
func TestContextWithholdsSecretsAndLogsNone(t *testing.T) {
	var logs bytes.Buffer
	log.SetOutput(&logs)
	defer log.SetOutput(nil)

	s := notesStore(t)
	a := mustAssistant(t, s, "a1", "Ada")
	secrets := []string{
		"deploy with sk-ant-api03-AAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"token ghp_abcdefghijklmnopqrstuvwxyz0123456789",
		"-----BEGIN OPENSSH PRIVATE KEY-----\nabc",
		"Authorization: Bearer abcdefghijklmnopqrstuvwxyz012345",
		"AKIAABCDEFGHIJKLMNOP is the aws id",
	}
	n, _ := s.ReadNotes(a.ID)
	for _, sec := range secrets {
		if _, err := s.WriteNotes(a.ID, NotesAssistant, sec, n.Assistant.Rev); err == nil {
			t.Fatalf("a credential was accepted as memory: %q", sec)
		}
		if _, err := s.AddAssistantNote(a.ID, sec); err == nil {
			t.Fatalf("a credential was accepted as a note: %q", sec)
		}
	}
	// A file edited by hand to hold one: that line alone is withheld.
	r, _, err := openNotes(a.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeNote(r, NotesAssistantFile, "- plain fact\n- "+secrets[1]+"\n- another fact\n"); err != nil {
		t.Fatal(err)
	}
	r.Close()
	c, err := s.BuildContext(a.ID, ContextRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(c.Text, "ghp_") || !strings.Contains(c.Text, "plain fact") || !strings.Contains(c.Text, "another fact") {
		t.Fatalf("context:\n%s", c.Text)
	}
	if strings.Contains(logs.String(), "ghp_") || strings.Contains(logs.String(), "sk-ant") {
		t.Fatalf("a secret reached the log: %q", logs.String())
	}
}

// A launched task's prompt carries the context, under the task's own Assistant.
func TestLaunchPromptCarriesMemoryContext(t *testing.T) {
	s := notesStore(t)
	a := mustAssistant(t, s, "a1", "Ada")
	b := mustAssistant(t, s, "a2", "Bob")
	writeBoth(t, s, a.ID, "Ada wants reports in bullet points", "")
	writeBoth(t, s, b.ID, "Bob wants reports in tables", "")
	task, _, err := s.CreateTask("t1", CreateTaskInput{AssistantID: a.ID, Title: "Weekly report", Brief: "Write the weekly report.", Kind: KindResearch})
	if err != nil {
		t.Fatal(err)
	}
	l := &fakeLauncher{}
	d := &Dispatcher{Store: s, Launcher: l, Claimant: "daemon"}
	if _, err := d.RunTask(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}
	p := l.specs[0].Prompt
	if !strings.Contains(p, "bullet points") || strings.Contains(p, "tables") || !strings.Contains(p, "Write the weekly report.") {
		t.Fatalf("prompt:\n%s", p)
	}
}
