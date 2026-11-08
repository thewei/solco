package history

import (
	"strings"
	"testing"
)

// TestCleanTitleCandidateStripsSkillBlocks pins F166: a session whose first
// message opens with a skill invocation must not surface the injected XML
// (or its attribute-carrying open tag, which overruns the generic tag scan)
// as the user-visible title.
func TestCleanTitleCandidateStripsSkillBlocks(t *testing.T) {
	raw := `<skill name="think" location="/Users/me/.agents/skills/think/SKILL.md">
References are relative to /Users/me/.agents/skills/think.
# Think: Design and Validate Before You Build…
</skill>
帮我把面板可以拖宽`
	if got, want := cleanTitleCandidate(raw), "帮我把面板可以拖宽"; got != want {
		t.Fatalf("cleanTitleCandidate = %q, want %q", got, want)
	}
	// A message that is only a skill block has no title candidate at all —
	// the adapter falls back to its next candidate.
	if got := cleanTitleCandidate("<skill name=\"x\">only</skill>"); got != "" {
		t.Fatalf("skill-only message produced %q, want empty", got)
	}
	// Ordinary prose with a short inline tag keeps the existing behavior.
	if got, want := cleanTitleCandidate("fix the <b>bold</b> path"), "fix the bold path"; got != want {
		t.Fatalf("plain text = %q, want %q", got, want)
	}
}

// TestAgentsInstructionsInjectionIsMetaNotTitle pins the 2026-10-30 UX audit
// bug: Codex injects the repository AGENTS.md as a user message, which must
// become metadata so the real first request titles the session.
func TestAgentsInstructionsInjectionIsMetaNotTitle(t *testing.T) {
	injected := "# AGENTS.md instructions for /Users/me/Workspaces/solco\n\n<INSTRUCTIONS>…</INSTRUCTIONS>"
	if userKind(injected) != MessageMeta {
		t.Fatalf("AGENTS.md injection kind = %v, want MessageMeta", userKind(injected))
	}
	// Codex 0.164 writes the heading without the " for <dir>" tail.
	if bare := "# AGENTS.md instructions\n\n<INSTRUCTIONS>…</INSTRUCTIONS>"; userKind(bare) != MessageMeta {
		t.Fatalf("bare AGENTS.md injection kind = %v, want MessageMeta", userKind(bare))
	}
	appPage := `<external_codex_apps_open_page>{"page_id":null}</external_codex_apps_open_page>`
	if userKind(appPage) != MessageMeta {
		t.Fatalf("Codex app page marker kind = %v, want MessageMeta", userKind(appPage))
	}
	messages := []TranscriptMessage{
		{Role: RoleUser, Kind: userKind(injected), Text: injected},
		{Role: RoleUser, Kind: userKind(appPage), Text: appPage},
		{Role: RoleUser, Kind: userKind("Review the sidebar"), Text: "Review the sidebar"},
	}
	if got, want := titleFromMessages(messages), "Review the sidebar"; got != want {
		t.Fatalf("title = %q, want %q", got, want)
	}
}

func TestTaskPreambleIsSkippedInTitle(t *testing.T) {
	raw := "Complete assignment thoroughly:\n\n# Target\n仓库 /Users/me/code/solco（Go 1.27 + MyGo）。目标：跑起带 Relay 的开发实例。\n\n# Change\n1. x"
	if got := cleanTitleCandidate(raw); !strings.HasPrefix(got, "目标：跑起带 Relay 的开发实例") {
		t.Fatalf("title = %q", got)
	}
	plain := "Complete assignment thoroughly later"
	if got := cleanTitleCandidate(plain); got != plain {
		t.Fatalf("non-preamble text changed: %q", got)
	}
}

// Real-device AC-03: every Assistants worker's first prompt opens with the
// daemon's `Solco-Run-Id:` marker line, which headed all of their titles alike.
func TestRunMarkerLineIsSkippedInTitle(t *testing.T) {
	if got := cleanTitleCandidate("Solco-Run-Id: r_5df4119927120060\n\nResearch option A. Read the page."); got != "Research option A. Read the page." {
		t.Fatalf("title = %q", got)
	}
	if got := cleanTitleCandidate("Solco-Run-Id: r_1"); got != "Solco-Run-Id: r_1" {
		t.Fatalf("a marker alone has nothing to show instead: %q", got)
	}
}

func TestTitleDropsHeadingMarks(t *testing.T) {
	if got := cleanTitleCandidate("fix C# parser\n\n# Change\n## 1. x"); got != "fix C# parser Change 1. x" {
		t.Fatalf("title = %q", got)
	}
}
