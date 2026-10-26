package workboard

import (
	"fmt"
	"testing"
)

// entry builds one `herdr plugin list --json` plugin object in the shape the
// live CLI printed on 2026-10-26 (Herdr 0.9.5, herdr-projects 1.2.26).
func entry(id, version string, enabled bool, actions, panes []string) string {
	list := func(ids []string) string {
		out := "["
		for i, name := range ids {
			if i > 0 {
				out += ","
			}
			out += fmt.Sprintf(`{"id":%q,"title":"x"}`, name)
		}
		return out + "]"
	}
	return fmt.Sprintf(`{"plugin_id":%q,"enabled":%t,"version":%q,"actions":%s,"panes":%s,"plugin_root":"/x"}`,
		id, enabled, version, list(actions), list(panes))
}

func envelope(entries ...string) string {
	body := ""
	for i, e := range entries {
		if i > 0 {
			body += ","
		}
		body += e
	}
	return `{"id":"cli:plugin","result":{"plugins":[` + body + `],"type":"plugin_list"}}`
}

func TestDetectGatesTheWorkboard(t *testing.T) {
	full := []string{"approve-current", "coordinator", "open"}
	board := []string{"board"}
	cases := []struct {
		name      string
		raw       string
		supported bool
		reason    Reason
		installed bool
	}{
		{"live 1.2.26", envelope(entry("herdr-projects", "1.2.26", true, full, board)), true, ReasonNone, true},
		{"another 1.2 patch", envelope(entry("herdr-projects", "1.2.0", true, full, board)), true, ReasonNone, true},
		{"v prefix and pre-release", envelope(entry("herdr-projects", "v1.2.3-rc1", true, full, board)), true, ReasonNone, true},
		{"beside other plugins", envelope(
			entry("agent-handoff", "0.1.0", true, nil, nil),
			entry("herdr-projects", "1.2.26", true, full, board)), true, ReasonNone, true},
		{"not installed", envelope(entry("agent-handoff", "0.1.0", true, nil, nil)), false, ReasonNotInstalled, false},
		{"no plugins at all", envelope(), false, ReasonNotInstalled, false},
		{"disabled", envelope(entry("herdr-projects", "1.2.26", false, full, board)), false, ReasonDisabled, true},
		{"newer minor is unverified", envelope(entry("herdr-projects", "1.3.0", true, full, board)), false, ReasonVersionUnsupported, true},
		{"older minor", envelope(entry("herdr-projects", "1.1.9", true, full, board)), false, ReasonVersionUnsupported, true},
		{"next major", envelope(entry("herdr-projects", "2.0.0", true, full, board)), false, ReasonVersionUnsupported, true},
		{"no version", envelope(entry("herdr-projects", "", true, full, board)), false, ReasonVersionUnsupported, true},
		{"junk version", envelope(entry("herdr-projects", "latest", true, full, board)), false, ReasonVersionUnsupported, true},
		{"open action missing", envelope(entry("herdr-projects", "1.2.26", true, []string{"coordinator"}, board)), false, ReasonMissingEntrypoint, true},
		{"board pane missing", envelope(entry("herdr-projects", "1.2.26", true, full, nil)), false, ReasonMissingEntrypoint, true},
		{"not json", "herdr: unknown command plugin", false, ReasonUnreadable, false},
		{"empty output", "", false, ReasonUnreadable, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Detect(tc.raw)
			if got.Supported != tc.supported || got.Reason != tc.reason || got.Installed != tc.installed {
				t.Fatalf("Detect = %+v, want supported=%t reason=%q installed=%t", got, tc.supported, tc.reason, tc.installed)
			}
		})
	}
}

// Supported is the only gate: every other field is diagnostic. A plugin that
// is installed but unsupported must never read as usable.
// The plugin folder is what the CLI runs from; it must survive detection so
// callers never guess a path.
func TestDetectReportsThePluginRoot(t *testing.T) {
	got := Detect(envelope(entry("herdr-projects", "1.2.26", true, []string{"open"}, []string{"board"})))
	if got.Root != "/x" {
		t.Fatalf("Root = %q, want the plugin_root from the list", got.Root)
	}
}

func TestUnsupportedNeverReadsAsUsable(t *testing.T) {
	got := Detect(envelope(entry("herdr-projects", "1.3.0", true, []string{"open"}, []string{"board"})))
	if got.Supported {
		t.Fatalf("1.3.0 must stay gated until verified: %+v", got)
	}
	if got.Version != "1.3.0" {
		t.Fatalf("version should still be reported for diagnostics: %+v", got)
	}
}
