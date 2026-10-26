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
