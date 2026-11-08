// Package automations is Solco's seam to the com.whstudio.solco.automations
// Herdr plugin (Phase 4, 2026-11-02: a native management list + detail
// drawer for scheduled agent runs; see designs/automations/).
//
// The plugin stays the only authority — it owns the daemon, the schedule,
// the runs and their history; Solco keeps no scheduler, no PTY and no state
// mirror of its own and only presents what the plugin's CLI reports. This
// package owns only the capability gate: whether the plugin is installed,
// enabled and in the verified version range. Every automations surface is
// shown, and every automations command runs, only while Detect reports
// Supported; otherwise the feature leaves no trace in the UI.
package automations

import (
	"encoding/json"
	"strconv"
	"strings"
)

const (
	// PluginID is the plugin's Herdr id (herdr-plugin.toml `id`).
	PluginID = "com.whstudio.solco.automations"
	// RunNowAction is the plugin action behind the page's Run Now.
	RunNowAction = "run-now"
	// BoardPane is the plugin pane entrypoint the manifest declares.
	BoardPane = "board"

	// The verified plugin line is 0.12.x. Like the Herdr protocol range this
	// is an explicit window, not equality and not open-ended forward
	// compatibility: a newer minor must be verified before it is enabled.
	supportedMajor = 0
	supportedMinor = 12
)

// Reason says why a plugin is not Supported. It is a stable code for logs and
// tests, never user-facing text: an unsupported plugin shows no UI at all.
type Reason string

const (
	ReasonNone               Reason = ""
	ReasonNotInstalled       Reason = "not-installed"
	ReasonDisabled           Reason = "disabled"
	ReasonVersionUnsupported Reason = "version-unsupported"
	ReasonMissingEntrypoint  Reason = "missing-entrypoint"
	ReasonUnreadable         Reason = "unreadable"
)

// Availability is the result of one detection.
type Availability struct {
	Installed bool
	Enabled   bool
	Version   string
	// Root is the plugin folder (`plugin_root`) the CLI runs from. It carries
	// an install counter that changes on upgrade, so it is re-read on every
	// detection and never cached beyond one.
	Root string
	// Supported is the only flag UI and logic may gate on.
	Supported bool
	Reason    Reason
}

// Unreadable is the availability when `herdr plugin list` could not be run or
// parsed (no Herdr CLI, an older Herdr without plugin support, bad output).
func Unreadable() Availability { return Availability{Reason: ReasonUnreadable} }

type pluginList struct {
	Result struct {
		Plugins []pluginEntry `json:"plugins"`
	} `json:"result"`
}

type pluginEntry struct {
	PluginID   string `json:"plugin_id"`
	Enabled    bool   `json:"enabled"`
	Version    string `json:"version"`
	PluginRoot string `json:"plugin_root"`
	Actions    []struct {
		ID string `json:"id"`
	} `json:"actions"`
	Panes []struct {
		ID string `json:"id"`
	} `json:"panes"`
}

// Detect classifies the output of `herdr plugin list --json`. It is pure: the
// caller runs the CLI, so the gate is testable without Herdr.
func Detect(listJSON string) Availability {
	var list pluginList
	if err := json.Unmarshal([]byte(listJSON), &list); err != nil {
		return Unreadable()
	}
	for _, plugin := range list.Result.Plugins {
		if plugin.PluginID != PluginID {
			continue
		}
		return classify(plugin)
	}
	return Availability{Reason: ReasonNotInstalled}
}

func classify(plugin pluginEntry) Availability {
	out := Availability{Installed: true, Enabled: plugin.Enabled, Version: plugin.Version, Root: plugin.PluginRoot}
	switch {
	case !plugin.Enabled:
		out.Reason = ReasonDisabled
	case !inVerifiedRange(plugin.Version):
		out.Reason = ReasonVersionUnsupported
	case !hasAction(plugin, RunNowAction) || !hasPane(plugin, BoardPane):
		out.Reason = ReasonMissingEntrypoint
	default:
		out.Supported = true
	}
	return out
}

func hasAction(plugin pluginEntry, id string) bool {
	for _, action := range plugin.Actions {
		if action.ID == id {
			return true
		}
	}
	return false
}

func hasPane(plugin pluginEntry, id string) bool {
	for _, pane := range plugin.Panes {
		if pane.ID == id {
			return true
		}
	}
	return false
}

// inVerifiedRange accepts MAJOR.MINOR.* of the verified line, tolerating a
// leading "v" and a pre-release/build suffix on the patch part.
func inVerifiedRange(version string) bool {
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(version), "v"), ".")
	if len(parts) < 2 {
		return false
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return false
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return false
	}
	return major == supportedMajor && minor == supportedMinor
}
