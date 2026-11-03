package personalagent

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

func approvedMem(t *testing.T, s *Store, req, assistant, content string) MemoryItem {
	t.Helper()
	m := proposeMem(t, s, req, assistant, content)
	m, _, err := s.ApproveMemory(req+"-ok", m.ID, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// C03: a context holds only what the Assistant may use: its own approved
// items and items another Assistant explicitly shared — never candidates,
// forgotten items or another Assistant's private ones.
func TestContextUsesOnlyPermittedMemory(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	b := mustAssistant(t, s, "a2", "Bob")
	approvedMem(t, s, "m-own", a.ID, "Ada likes short summaries")
	proposeMem(t, s, "m-cand", a.ID, "Ada candidate only")
	approvedMem(t, s, "m-priv", b.ID, "Bob private fact")
	shared := approvedMem(t, s, "m-sh", b.ID, "Bob shared fact")
	if _, _, err := s.ShareMemory("share", shared.ID, a.ID, shared.Revision); err != nil {
		t.Fatal(err)
	}
	c, err := s.BuildContext(a.ID, ContextRequest{Query: "summaries", BudgetTokens: 1000})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Ada likes short summaries", "Bob shared fact", "m_"} {
		if !strings.Contains(c.Text, want) {
			t.Fatalf("context lacks %q:\n%s", want, c.Text)
		}
	}
	for _, bad := range []string{"candidate only", "Bob private fact"} {
		if strings.Contains(c.Text, bad) {
			t.Fatalf("context leaks %q:\n%s", bad, c.Text)
		}
	}
	if len(c.Used) != 2 {
		t.Fatalf("used = %v", c.Used)
	}
	// Revoking the share removes it at once.
	shared, _ = s.GetMemory(shared.ID)
	if _, _, err := s.UnshareMemory("unshare", shared.ID, a.ID, shared.Revision); err != nil {
		t.Fatal(err)
	}
	c, _ = s.BuildContext(a.ID, ContextRequest{BudgetTokens: 1000})
	if strings.Contains(c.Text, "Bob shared fact") {
		t.Fatalf("revoked share still used:\n%s", c.Text)
	}
}

// SOLCO-M03: a correction or a deletion changes the very next context.
func TestContextForgetsCorrectedAndDeletedMemoryImmediately(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	m := approvedMem(t, s, "m1", a.ID, "Prefers tea in the morning")
	build := func() string {
		c, err := s.BuildContext(a.ID, ContextRequest{BudgetTokens: 500})
		if err != nil {
			t.Fatal(err)
		}
		return c.Text
	}
	if !strings.Contains(build(), "tea") {
		t.Fatal("approved memory missing")
	}
	m, _, err := s.CorrectMemory("fix", m.ID, "Prefers coffee in the morning", m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if txt := build(); strings.Contains(txt, "tea") || !strings.Contains(txt, "coffee") {
		t.Fatalf("after correction:\n%s", txt)
	}
	if _, _, err := s.ForgetMemory("del", m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	if txt := build(); strings.Contains(txt, "coffee") || strings.Contains(txt, "tea") || strings.Contains(txt, m.ID) {
		t.Fatalf("after forgetting:\n%s", txt)
	}
}

// SOLCO-M06: the context is bounded by its token budget, the most relevant
// items win the room, and one huge item cannot swallow it.
func TestContextBudgetIsBounded(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	for i := 0; i < 60; i++ {
		approvedMem(t, s, "m"+string(rune('A'+i/26))+string(rune('a'+i%26)), a.ID, strings.Repeat("filler words go here ", 20))
	}
	approvedMem(t, s, "needle", a.ID, "The deploy key rotation happens on Fridays")
	approvedMem(t, s, "huge", a.ID, strings.Repeat("blah ", 5000))

	c, err := s.BuildContext(a.ID, ContextRequest{Query: "when does the deploy rotation happen", BudgetTokens: 400})
	if err != nil {
		t.Fatal(err)
	}
	if c.Tokens > 400 || estimateTokens(c.Text) > 400 {
		t.Fatalf("tokens = %d (estimate %d) over budget 400", c.Tokens, estimateTokens(c.Text))
	}
	if !strings.Contains(c.Text, "rotation happens on Fridays") {
		t.Fatalf("the relevant item lost the room:\n%s", c.Text)
	}
	if c.Dropped == 0 {
		t.Fatal("62 items cannot fit 400 tokens; Dropped should say so")
	}
	// A budget is clamped, never unbounded.
	c, _ = s.BuildContext(a.ID, ContextRequest{BudgetTokens: 10_000_000})
	if c.Tokens > MaxContextTokens {
		t.Fatalf("clamp failed: %d", c.Tokens)
	}
	c, _ = s.BuildContext(a.ID, ContextRequest{})
	if c.Tokens == 0 || c.Tokens > DefaultContextTokens {
		t.Fatalf("default budget: %d", c.Tokens)
	}
}

// SOLCO-M06: credentials never enter a context, and never reach the log.
func TestContextWithholdsSecretsAndLogsNone(t *testing.T) {
	var logs bytes.Buffer
	log.SetOutput(&logs)
	defer log.SetOutput(nil)

	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	secrets := []string{
		"deploy with sk-ant-api03-AAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"token ghp_abcdefghijklmnopqrstuvwxyz0123456789",
		"-----BEGIN OPENSSH PRIVATE KEY-----\nabc",
		"Authorization: Bearer abcdefghijklmnopqrstuvwxyz012345",
		"AKIAABCDEFGHIJKLMNOP is the aws id",
	}
	for i, sec := range secrets {
		if _, _, err := s.ProposeMemory("p"+string(rune('a'+i)), ProposeMemoryInput{AssistantID: a.ID, Content: sec, SourceRef: "run:r_1"}); err == nil {
			t.Fatalf("a credential was accepted as memory: %q", sec)
		}
	}
	// A secret that predates the check (written straight into the table) is
	// still withheld at build time and counted, not logged.
	m := approvedMem(t, s, "ok", a.ID, "plain fact")
	if _, err := s.db.Exec(`UPDATE memory_items SET content=? WHERE id=?`, secrets[1], m.ID); err != nil {
		t.Fatal(err)
	}
	c, err := s.BuildContext(a.ID, ContextRequest{BudgetTokens: 500})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(c.Text, "ghp_") || c.Withheld != 1 {
		t.Fatalf("withheld=%d text=%q", c.Withheld, c.Text)
	}
	if strings.Contains(logs.String(), "ghp_") || strings.Contains(logs.String(), "sk-ant") {
		t.Fatalf("a secret reached the log: %q", logs.String())
	}
}

// C03: lightweight retrieval ranks by words in common, ignoring forgotten and
// foreign items.
func TestSearchMemoryRanksByRelevance(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	b := mustAssistant(t, s, "a2", "Bob")
	approvedMem(t, s, "m1", a.ID, "Standup is at 9:30 every weekday")
	approvedMem(t, s, "m2", a.ID, "The quarterly report is due on the 30th")
	approvedMem(t, s, "m3", b.ID, "Bob's standup is secret")
	got, err := s.SearchMemory(a.ID, "when is the standup", 5)
	if err != nil || len(got) == 0 || !strings.Contains(got[0].Content, "Standup") {
		t.Fatalf("search = %+v %v", got, err)
	}
	for _, m := range got {
		if m.AssistantID != a.ID {
			t.Fatalf("foreign item: %+v", m)
		}
	}
	if none, _ := s.SearchMemory(a.ID, "zebra", 5); len(none) != 0 {
		t.Fatalf("no overlap should be no result: %+v", none)
	}
}

// C03: a launched task's prompt carries the context, under the task's own
// Assistant, with the same budget rules.
func TestLaunchPromptCarriesMemoryContext(t *testing.T) {
	s, _ := openTest(t)
	a := mustAssistant(t, s, "a1", "Ada")
	b := mustAssistant(t, s, "a2", "Bob")
	approvedMem(t, s, "m1", a.ID, "Ada wants reports in bullet points")
	approvedMem(t, s, "m2", b.ID, "Bob wants reports in tables")
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
