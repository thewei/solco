package automations

import (
	"fmt"
	"testing"
)

// entry builds one `herdr plugin list --json` plugin object in the shape the
// live CLI prints (the shape internal/workboard pinned on 2026-10-26).
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

func TestDetectGatesAutomations(t *testing.T) {
	actions := []string{"run-now", "install-cli"}
	panes := []string{"board"}
	cases := []struct {
		name      string
		raw       string
		supported bool
		reason    Reason
		installed bool
	}{
		{"live 0.12.0", envelope(entry(PluginID, "0.12.0", true, actions, panes)), true, ReasonNone, true},
		{"another 0.12 patch", envelope(entry(PluginID, "0.12.7", true, actions, panes)), true, ReasonNone, true},
		{"v prefix and pre-release", envelope(entry(PluginID, "v0.12.3-rc1", true, actions, panes)), true, ReasonNone, true},
		{"beside other plugins", envelope(
			entry("herdr-projects", "1.2.26", true, nil, nil),
			entry(PluginID, "0.12.0", true, actions, panes)), true, ReasonNone, true},
		{"not installed", envelope(entry("herdr-projects", "1.2.26", true, nil, nil)), false, ReasonNotInstalled, false},
		{"no plugins at all", envelope(), false, ReasonNotInstalled, false},
		{"disabled", envelope(entry(PluginID, "0.12.0", false, actions, panes)), false, ReasonDisabled, true},
		{"newer minor is unverified", envelope(entry(PluginID, "0.13.0", true, actions, panes)), false, ReasonVersionUnsupported, true},
		{"older minor", envelope(entry(PluginID, "0.11.9", true, actions, panes)), false, ReasonVersionUnsupported, true},
		{"next major", envelope(entry(PluginID, "1.0.0", true, actions, panes)), false, ReasonVersionUnsupported, true},
		{"no version", envelope(entry(PluginID, "", true, actions, panes)), false, ReasonVersionUnsupported, true},
		{"junk version", envelope(entry(PluginID, "latest", true, actions, panes)), false, ReasonVersionUnsupported, true},
		{"run-now action missing", envelope(entry(PluginID, "0.12.0", true, []string{"install-cli"}, panes)), false, ReasonMissingEntrypoint, true},
		{"board pane missing", envelope(entry(PluginID, "0.12.0", true, actions, nil)), false, ReasonMissingEntrypoint, true},
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

// Supported is the only gate: every other field is diagnostic. The plugin
// folder is what the CLI runs from; it must survive detection so callers
// never guess a path.
func TestDetectReportsThePluginRoot(t *testing.T) {
	got := Detect(envelope(entry(PluginID, "0.12.0", true, []string{"run-now"}, []string{"board"})))
	if got.Root != "/x" {
		t.Fatalf("Root = %q, want the plugin_root from the list", got.Root)
	}
}

func TestUnsupportedNeverReadsAsUsable(t *testing.T) {
	got := Detect(envelope(entry(PluginID, "0.13.0", true, []string{"run-now"}, []string{"board"})))
	if got.Supported {
		t.Fatalf("0.13.0 must stay gated until verified: %+v", got)
	}
	if got.Version != "0.13.0" {
		t.Fatalf("version should still be reported for diagnostics: %+v", got)
	}
}
