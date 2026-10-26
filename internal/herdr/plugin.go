package herdr

import (
	"context"
	"fmt"
	"strings"
)

// PluginListJSON returns `herdr plugin list --json`: Herdr's own, machine-wide
// answer to "which plugins are installed, enabled and at what version". It is
// the single source for plugin-gated client features (internal/workboard).
func PluginListJSON(ctx context.Context) (string, error) {
	return RunCLI(ctx, []string{"plugin", "list", "--json"})
}

// pluginActionArgs is the argv of one plugin action invocation.
func pluginActionArgs(pluginID, actionID string) ([]string, error) {
	pluginID, actionID = strings.TrimSpace(pluginID), strings.TrimSpace(actionID)
	if pluginID == "" || actionID == "" {
		return nil, fmt.Errorf("plugin id and action id are required")
	}
	return []string{"plugin", "action", "invoke", actionID, "--plugin", pluginID}, nil
}

// InvokePluginAction runs one declared plugin action in a local session and
// returns the authoritative projection after it, so the caller can adopt the
// focus the action produced (for example the board Tab a plugin opened). The
// action's effect belongs to the plugin and Herdr; Shardlane starts no process
// of its own. Like every structural mutation it is never retried here.
//
// Plugins are installed on this machine, so a remote machine instance is
// refused: its actions would run against a different plugin set.
func (m *Manager) InvokePluginAction(ctx context.Context, session, pluginID, actionID string) (Projection, error) {
	if err := guardLocalInstance(session, "running a plugin action"); err != nil {
		return Projection{}, err
	}
	args, err := pluginActionArgs(pluginID, actionID)
	if err != nil {
		return Projection{}, err
	}
	socket, err := m.reachableSocket(session)
	if err != nil {
		return Projection{}, err
	}
	if _, err := runCLIEnv(ctx, args, sessionEnv(ParseRef(session).Session)); err != nil {
		return Projection{}, err
	}
	return m.projectionReadyAt(socket)
}

// pluginPaneFocusArgs is the argv of focusing one plugin-owned pane.
func pluginPaneFocusArgs(paneID string) []string {
	return []string{"plugin", "pane", "focus", strings.TrimSpace(paneID)}
}

// pluginPaneToFocus finds the plugin pane the manifest titles `title` in a
// projection and says whether it still needs the focus. Plugin panes carry no
// plugin id in the snapshot, so the plugin's declared pane title is the key.
func pluginPaneToFocus(p Projection, title string) (paneID string, needsFocus bool) {
	for _, pane := range p.Panes {
		if pane.Label == title && pane.ID != "" {
			return pane.ID, pane.ID != p.FocusedPaneID
		}
	}
	return "", false
}

// FocusPluginPane makes sure the plugin pane titled `title` has Herdr's focus
// after a plugin action: an action invoked from outside a Herdr client (as
// Shardlane does) opens its Tab but leaves the focus where it was. The focus
// itself is Herdr's own `plugin pane focus`; `from` is the projection the
// action returned. With no such pane, or when it is already focused, `from`
// is returned unchanged.
func (m *Manager) FocusPluginPane(ctx context.Context, session, title string, from Projection) (Projection, error) {
	if err := guardLocalInstance(session, "focusing a plugin pane"); err != nil {
		return Projection{}, err
	}
	paneID, needs := pluginPaneToFocus(from, title)
	if !needs {
		return from, nil
	}
	socket, err := m.reachableSocket(session)
	if err != nil {
		return Projection{}, err
	}
	if _, err := runCLIEnv(ctx, pluginPaneFocusArgs(paneID), sessionEnv(ParseRef(session).Session)); err != nil {
		return Projection{}, err
	}
	return m.projectionReadyAt(socket)
}
