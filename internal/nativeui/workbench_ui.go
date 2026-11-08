package nativeui

// pluralS returns "s" for counts other than exactly one.
func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// The /agents workbench page and its standard card were removed on
// 2026-10-07 together with the in-window Status Center: agent activity is
// presented only by the floating panel (quick_panel.go) over the shared
// agentCardRow builder (agent_card.go).
