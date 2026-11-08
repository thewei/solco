package herdr

import "context"

// PluginListJSON returns `herdr plugin list --json`: Herdr's own, machine-wide
// answer to "which plugins are installed, enabled and at what version". It is
// the single source for plugin-gated client features (internal/automations).
func PluginListJSON(ctx context.Context) (string, error) {
	return RunCLI(ctx, []string{"plugin", "list", "--json"})
}
