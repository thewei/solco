package agenthooks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

/**
 * [INPUT]: 依赖 os/filepath/encoding/json、内嵌 hookScriptTemplate（script.go）与用户 HOME 下的 provider 配置文件
 * [OUTPUT]: 对外提供 Manager、NewManager、NewManagerWithHome、ProviderID/State/Diagnosis、Manager.Diagnose、Manager.Install
 * [POS]: Agent Hook 一键安装与环境诊断（Hook Doctor）的领域包：脚本供给、provider 接线检测与安全幂等写入；UI 在 nativeui，socket 服务在 apphook
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

// ProviderID names one supported coding-agent CLI.
type ProviderID string

const (
	ProviderClaude      ProviderID = "claude"
	ProviderAntigravity ProviderID = "antigravity"
	ProviderCursor      ProviderID = "cursor"
	ProviderCodex       ProviderID = "codex"
	ProviderAider       ProviderID = "aider"
)

// DisplayName is the user-facing provider name.
func (p ProviderID) DisplayName() string {
	switch p {
	case ProviderClaude:
		return "Claude Code"
	case ProviderAntigravity:
		return "Antigravity CLI"
	case ProviderCursor:
		return "Cursor"
	case ProviderCodex:
		return "Codex"
	case ProviderAider:
		return "Aider"
	default:
		return string(p)
	}
}

// State is the presentation-safe hook diagnosis vocabulary.
type State string

const (
	StateOK           State = "ok"
	StateNotInstalled State = "not-installed"
	StateOutdated     State = "outdated"
	StateUnsupported  State = "unsupported"
	// StateNeedsApproval: installed and wired, but the agent runs a hook only
	// after the user trusts it (Codex). Solco cannot grant that and offers no action.
	StateNeedsApproval State = "needs-approval"
	StateError         State = "error"
)

// Diagnosis is one provider's reconciled hook row. Detail is composed
// user-language text; the UI renders it verbatim.
type Diagnosis struct {
	Provider ProviderID `json:"provider"`
	State    State      `json:"state"`
	Detail   string     `json:"detail,omitempty"`
	Path     string     `json:"path,omitempty"`
	Version  int        `json:"version,omitempty"` // installed script version, 0 = none
	Wired    []string   `json:"wired,omitempty"`   // event names with our wiring
	Missing  []string   `json:"missing,omitempty"` // wanted event names without wiring
}

// Actionable reports whether the row exposes the one safe install/update
// action. Actions never run from render; the Providers page owns timing.
func (d Diagnosis) Actionable() bool {
	return d.State == StateNotInstalled || d.State == StateOutdated
}

// ActionLabel names the safe action for the row, if any.
func (d Diagnosis) ActionLabel() string {
	switch d.State {
	case StateNotInstalled:
		return "Install"
	case StateOutdated:
		return "Update"
	default:
		return ""
	}
}

// Manager provisions and diagnoses the Solco direct-connect agent
// hooks. It writes exactly two kinds of files: our own scripts under
// ~/.solco/hooks/ (never overwriting foreign files) and narrow,
// idempotent edits to each provider's hook wiring — everything else in
// the provider configs is preserved byte-for-value with a rolling backup.
type Manager struct {
	home string
}

// NewManager returns a Manager against the real user home.
func NewManager() *Manager {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = "."
	}
	return &Manager{home: home}
}

// NewManagerWithHome injects the home root; it is the test seam.
func NewManagerWithHome(home string) *Manager {
	return &Manager{home: home}
}

// claudeHookEvents is the proven wiring set for Claude Code. The script
// also understands UserPromptSubmit/SessionEnd; they stay unwired until a
// verified need lands (the doctor provisions only what shipped verified).
var claudeHookEvents = []string{"SessionStart", "PreToolUse", "PostToolUse", "PermissionRequest", "Stop"}

// antigravityHookEvents is the proven wiring set for Antigravity CLI,
// grouped under the "solco-hook" owner key of ~/.gemini/config/hooks.json.
var antigravityHookEvents = []string{"SessionStart", "PreInvocation", "PreToolUse", "PostToolUse", "Stop"}

// cursorHookEvents is the wiring set for Cursor Agent (camelCase event names,
// ~/.cursor/hooks.json). Cursor has no hook trust gate; the payload carries
// conversation_id on every event, which the script reports as the session.
var cursorHookEvents = []string{"sessionStart", "beforeSubmitPrompt", "preToolUse", "postToolUse", "stop"}

// codexHookEvents is the wiring set for Codex (~/.codex/hooks.json, the same
// nested shape as Claude Code). Codex runs a hook only once the user trusts
// it, so wiring alone is not "ok" (see codexUntrusted).
var codexHookEvents = []string{"SessionStart", "PreToolUse", "PostToolUse", "PermissionRequest", "Stop"}

// antigravityOwnerKey is the hooks.json group this package owns. Foreign
// groups (herdr, orca-status, …) are never touched.
const antigravityOwnerKey = "solco-hook"

type providerSpec struct {
	id         ProviderID
	scriptName string
	events     []string
}

func (m *Manager) spec(id ProviderID) (providerSpec, bool) {
	switch id {
	case ProviderClaude:
		return providerSpec{id: id, scriptName: "solco-claude-hook.sh", events: claudeHookEvents}, true
	case ProviderAntigravity:
		return providerSpec{id: id, scriptName: "solco-antigravity-hook.sh", events: antigravityHookEvents}, true
	case ProviderCursor:
		return providerSpec{id: id, scriptName: "solco-cursor-hook.sh", events: cursorHookEvents}, true
	case ProviderCodex:
		return providerSpec{id: id, scriptName: "solco-codex-hook.sh", events: codexHookEvents}, true
	default:
		return providerSpec{}, false
	}
}

func (m *Manager) hooksDir() string { return filepath.Join(m.home, ".solco", "hooks") }

func (m *Manager) scriptPath(id ProviderID) string {
	spec, ok := m.spec(id)
	if !ok {
		return ""
	}
	return filepath.Join(m.hooksDir(), spec.scriptName)
}

func (m *Manager) claudeSettingsPath() string {
	return filepath.Join(m.home, ".claude", "settings.json")
}

func (m *Manager) codexHooksPath() string {
	return filepath.Join(m.home, ".codex", "hooks.json")
}

func (m *Manager) codexConfigPath() string {
	return filepath.Join(m.home, ".codex", "config.toml")
}

func (m *Manager) cursorHooksPath() string {
	return filepath.Join(m.home, ".cursor", "hooks.json")
}

func (m *Manager) antigravityHooksPath() string {
	return filepath.Join(m.home, ".gemini", "config", "hooks.json")
}

// socketPath mirrors apphook.DefaultSocketPath for diagnosis detail only.
func (m *Manager) socketPath() string {
	return filepath.Join(m.home, ".solco", "run", "agent-status.sock")
}

// Diagnose reconciles every supported provider's hook state, plus the
// Aider row that explains why it is not provisionable. Pure reads; safe
// on route entry and manual refresh.
func (m *Manager) Diagnose() []Diagnosis {
	rows := []Diagnosis{m.diagnoseProvider(ProviderClaude), m.diagnoseProvider(ProviderAntigravity), m.diagnoseProvider(ProviderCursor), m.diagnoseProvider(ProviderCodex)}
	rows = append(rows, m.diagnoseAider())
	return rows
}

func (m *Manager) diagnoseProvider(id ProviderID) Diagnosis {
	if _, ok := m.spec(id); !ok {
		return Diagnosis{Provider: id, State: StateUnsupported, Detail: "Unsupported provider"}
	}
	path := m.scriptPath(id)
	row := Diagnosis{Provider: id, Path: path}

	owned, version, err := readOwnedScript(path)
	switch {
	case err != nil:
		row.State = StateError
		row.Detail = err.Error()
		return row
	case owned == foreignOwned:
		row.State = StateError
		row.Detail = fmt.Sprintf("%s exists but was not installed by Solco; refusing to touch it", path)
		return row
	case version == 0:
		row.State = StateNotInstalled
	case version < hookScriptVersion:
		row.State = StateOutdated
	default:
		row.State = StateOK
	}
	row.Version = version

	spec, _ := m.spec(id)
	wired, missing := m.wiringState(id, spec.events)
	row.Wired, row.Missing = wired, missing
	if len(missing) > 0 && (row.State == StateOK || row.State == StateOutdated) {
		// One Install action fixes script and wiring together, so any
		// missing wiring presents as the same actionable state.
		row.State = StateNotInstalled
	}
	if id == ProviderCodex && row.State == StateOK {
		if untrusted := m.codexUntrusted(); len(untrusted) > 0 {
			row.State = StateNeedsApproval
			row.Detail = "Wired, but Codex runs a hook only after you trust it: open Codex and review hooks (/hooks); waiting on " + strings.Join(untrusted, ", ")
			return row
		}
	}
	row.Detail = m.describe(row)
	return row
}

// diagnoseAider reports the honest fallback: Aider ships no hook
// mechanism, so pane status comes from Herdr's own screen detection.
func (m *Manager) diagnoseAider() Diagnosis {
	row := Diagnosis{Provider: ProviderAider, State: StateUnsupported}
	config := filepath.Join(m.home, ".aider.conf.yml")
	if _, err := os.Stat(config); err == nil {
		row.Detail = "Aider has no hook mechanism; its pane status comes from Herdr screen detection (no action needed)"
		return row
	}
	row.Detail = "Not detected on this machine; if you use Aider, its pane status comes from Herdr screen detection"
	return row
}

func (m *Manager) describe(row Diagnosis) string {
	switch row.State {
	case StateOK:
		return fmt.Sprintf("Hook script v%d wired to all %d events; reports to %s", row.Version, len(row.Wired), m.socketPath())
	case StateOutdated:
		return fmt.Sprintf("Hook script v%d is outdated (want v%d); updating rewrites the script and keeps wiring", row.Version, hookScriptVersion)
	case StateNotInstalled:
		missing := ""
		if len(row.Missing) > 0 {
			missing = "; missing wiring: " + strings.Join(row.Missing, ", ")
		}
		if row.Version == 0 {
			return "Hook script not installed" + missing
		}
		return fmt.Sprintf("Hook script v%d present but not fully wired", row.Version) + missing
	default:
		return row.Detail
	}
}

// wiringState checks each wanted event's wiring for our script command.
func (m *Manager) wiringState(id ProviderID, events []string) (wired, missing []string) {
	for _, event := range events {
		if m.eventWired(id, event) {
			wired = append(wired, event)
		} else {
			missing = append(missing, event)
		}
	}
	sort.Strings(wired)
	sort.Strings(missing)
	return wired, missing
}

// Install provisions one provider end to end: write the script (never
// overwriting foreign files), then wire the provider config idempotently.
// It returns the fresh diagnosis; call it from a real user action only.
func (m *Manager) Install(id ProviderID) (Diagnosis, error) {
	if _, ok := m.spec(id); !ok {
		return Diagnosis{}, fmt.Errorf("unsupported provider %q", id)
	}
	if err := m.writeScript(id); err != nil {
		return Diagnosis{}, err
	}
	switch id {
	case ProviderClaude:
		if err := m.wireClaude(); err != nil {
			return Diagnosis{}, err
		}
	case ProviderAntigravity:
		if err := m.wireAntigravity(); err != nil {
			return Diagnosis{}, err
		}
	case ProviderCursor:
		if err := m.wireCursor(); err != nil {
			return Diagnosis{}, err
		}
	case ProviderCodex:
		if err := m.wireCodex(); err != nil {
			return Diagnosis{}, err
		}
	}
	slog.Info("agent hooks provisioned", "provider", string(id), "script", m.scriptPath(id))
	return m.diagnoseProvider(id), nil
}

// UpgradeScripts refreshes the hook scripts Solco itself installed earlier
// (owned marker, version below the current one) and nothing else: no provider
// config, hooks.json or Codex trust record is touched, a missing or foreign
// script is left alone, so the user's choices and approvals stay theirs. It
// returns the providers whose script it replaced.
func (m *Manager) UpgradeScripts() []ProviderID {
	var upgraded []ProviderID
	for _, id := range []ProviderID{ProviderClaude, ProviderAntigravity, ProviderCursor, ProviderCodex} {
		if _, ok := m.spec(id); !ok {
			continue
		}
		owned, version, err := readOwnedScript(m.scriptPath(id))
		if err != nil || owned != oursOwned || version == 0 || version >= hookScriptVersion {
			continue
		}
		if err := m.writeScript(id); err != nil {
			slog.Warn("agent hook script upgrade failed", "provider", string(id), "error", err)
			continue
		}
		upgraded = append(upgraded, id)
	}
	return upgraded
}

// writeScript installs the universal hook script at the provider's path.
// Foreign files at the same path are never touched.
func (m *Manager) writeScript(id ProviderID) error {
	path := m.scriptPath(id)
	owned, version, err := readOwnedScript(path)
	if err != nil {
		return err
	}
	if owned == foreignOwned {
		return fmt.Errorf("%s exists but was not installed by Solco; remove or rename it first", path)
	}
	if owned == oursOwned && version == hookScriptVersion {
		return nil
	}
	if err := os.MkdirAll(m.hooksDir(), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", m.hooksDir(), err)
	}
	if err := atomicWrite(path, []byte(hookScriptTemplate), 0o755); err != nil {
		return err
	}
	slog.Info("agent hook script written", "path", path, "version", hookScriptVersion)
	return nil
}

type scriptOwnership int

const (
	scriptMissing scriptOwnership = iota
	oursOwned
	foreignOwned
)

var versionRe = regexp.MustCompile(`(?m)^#\s*SOLCO_HOOK_VERSION=(\d+)\s*$`)

// readOwnedScript classifies the file at path and extracts its marker
// version. owned=foreignOwned means a foreign file occupies our path.
func readOwnedScript(path string) (owned scriptOwnership, version int, err error) {
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		if os.IsNotExist(readErr) {
			return scriptMissing, 0, nil
		}
		return scriptMissing, 0, fmt.Errorf("read %s: %w", path, readErr)
	}
	if !strings.Contains(string(data), hookScriptMarker) {
		return foreignOwned, 0, nil
	}
	if match := versionRe.FindSubmatch(data); match != nil {
		if parsed, parseErr := strconv.Atoi(string(match[1])); parseErr == nil {
			version = parsed
		}
	}
	return oursOwned, version, nil
}

// wireClaude idempotently adds our command under each wanted event of
// ~/.claude/settings.json's hooks map, preserving all other content.
func (m *Manager) wireClaude() error {
	path := m.claudeSettingsPath()
	root, err := readJSONObject(path)
	if err != nil {
		return err
	}
	hooks := objectOf(root["hooks"])
	changed := false
	command := fmt.Sprintf("bash %s claude", shellQuote(m.scriptPath(ProviderClaude)))
	for _, event := range claudeHookEvents {
		entries := arrayOf(hooks[event])
		if wiringContains(entries, filepath.Base(m.scriptPath(ProviderClaude))) {
			continue
		}
		entry := map[string]any{
			"matcher": "*",
			"hooks": []any{map[string]any{
				"type":    "command",
				"command": command,
			}},
		}
		hooks[event] = append(entries, entry)
		changed = true
	}
	root["hooks"] = hooks
	if !changed {
		return nil
	}
	return backupAndWriteJSON(path, root)
}

// wireAntigravity idempotently adds our event commands under the
// "solco-hook" owner key of ~/.gemini/config/hooks.json, preserving
// foreign owner groups (herdr, orca-status, …).
func (m *Manager) wireAntigravity() error {
	path := m.antigravityHooksPath()
	root, err := readJSONObject(path)
	if err != nil {
		return err
	}
	group := objectOf(root[antigravityOwnerKey])
	changed := false
	script := m.scriptPath(ProviderAntigravity)
	for _, event := range antigravityHookEvents {
		entries := arrayOf(group[event])
		if wiringContains(entries, filepath.Base(script)) {
			continue
		}
		if event == "PreToolUse" || event == "PostToolUse" {
			group[event] = append(entries, map[string]any{
				"matcher": "*",
				"hooks": []any{map[string]any{
					"type":    "command",
					"command": fmt.Sprintf("bash %s agy %s", shellQuote(script), event),
					"timeout": json.Number("5"),
				}},
			})
		} else {
			group[event] = append(entries, map[string]any{
				"type":    "command",
				"command": fmt.Sprintf("bash %s agy %s", shellQuote(script), event),
				"timeout": json.Number("5"),
			})
		}
		changed = true
	}
	if enabled, ok := group["enabled"].(bool); !ok || !enabled {
		group["enabled"] = true
		changed = true
	}
	root[antigravityOwnerKey] = group
	if !changed {
		return nil
	}
	return backupAndWriteJSON(path, root)
}

// wireCodex idempotently adds our command under each wanted event of
// ~/.codex/hooks.json (nested {hooks:[{type,command,timeout}]} groups),
// preserving every foreign hook. It never touches config.toml: trusting a
// hook is the user's decision inside Codex.
func (m *Manager) wireCodex() error {
	path := m.codexHooksPath()
	root, err := readJSONObject(path)
	if err != nil {
		return err
	}
	hooks := objectOf(root["hooks"])
	changed := false
	script := m.scriptPath(ProviderCodex)
	command := fmt.Sprintf("bash %s codex", shellQuote(script))
	for _, event := range codexHookEvents {
		entries := arrayOf(hooks[event])
		if wiringContains(entries, filepath.Base(script)) {
			continue
		}
		hooks[event] = append(entries, map[string]any{
			"hooks": []any{map[string]any{
				"type":    "command",
				"command": command,
				"timeout": json.Number("10"),
			}},
		})
		changed = true
	}
	root["hooks"] = hooks
	if !changed {
		return nil
	}
	return backupAndWriteJSON(path, root)
}

// codexUntrusted lists the wired events Codex has no trust record for. Codex
// keeps one `[hooks.state."<hooks.json>:<snake_event>:<group>:<hook>"]` table
// with a trusted_hash per approved hook in config.toml; only its presence is
// read here (the hash itself is Codex's to compute and check).
func (m *Manager) codexUntrusted() []string {
	data, _ := os.ReadFile(m.codexConfigPath())
	config := string(data)
	root, err := readJSONObject(m.codexHooksPath())
	if err != nil {
		return nil
	}
	script := filepath.Base(m.scriptPath(ProviderCodex))
	var untrusted []string
	for _, event := range codexHookEvents {
		for group, entry := range arrayOf(objectOf(root["hooks"])[event]) {
			if !wiringContains([]any{entry}, script) {
				continue
			}
			key := fmt.Sprintf("%s:%s:%d:0", m.codexHooksPath(), snakeCase(event), group)
			if !tomlTableHas(config, `[hooks.state."`+key+`"]`, "trusted_hash") {
				untrusted = append(untrusted, event)
			}
			break
		}
	}
	sort.Strings(untrusted)
	return untrusted
}

// snakeCase turns Codex's PascalCase event names into its state-key form
// (PreToolUse -> pre_tool_use).
func snakeCase(event string) string {
	var b strings.Builder
	for i, r := range event {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			r += 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return b.String()
}

// tomlTableHas reports whether the table headed by header carries key,
// scanning lines until the next table header (no TOML dependency needed for
// one presence check).
func tomlTableHas(config, header, key string) bool {
	in := false
	for _, line := range strings.Split(config, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			in = line == header
			continue
		}
		if in && strings.HasPrefix(line, key) {
			return true
		}
	}
	return false
}

// wireCursor idempotently adds our command under each wanted event of
// ~/.cursor/hooks.json (flat {command} entries, `version` kept or set to 1),
// preserving every foreign hook.
func (m *Manager) wireCursor() error {
	path := m.cursorHooksPath()
	root, err := readJSONObject(path)
	if err != nil {
		return err
	}
	hooks := objectOf(root["hooks"])
	changed := false
	script := m.scriptPath(ProviderCursor)
	command := fmt.Sprintf("bash %s cursor", shellQuote(script))
	for _, event := range cursorHookEvents {
		entries := arrayOf(hooks[event])
		if wiringContains(entries, filepath.Base(script)) {
			continue
		}
		hooks[event] = append(entries, map[string]any{"command": command})
		changed = true
	}
	if _, ok := root["version"]; !ok {
		root["version"] = json.Number("1")
		changed = true
	}
	root["hooks"] = hooks
	if !changed {
		return nil
	}
	return backupAndWriteJSON(path, root)
}

func (m *Manager) eventWired(id ProviderID, event string) bool {
	var data []byte
	var err error
	switch id {
	case ProviderClaude:
		data, err = os.ReadFile(m.claudeSettingsPath())
	case ProviderAntigravity:
		data, err = os.ReadFile(m.antigravityHooksPath())
	case ProviderCursor:
		data, err = os.ReadFile(m.cursorHooksPath())
	case ProviderCodex:
		data, err = os.ReadFile(m.codexHooksPath())
	default:
		return false
	}
	if err != nil {
		return false
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return false
	}
	var entries []any
	if id == ProviderClaude || id == ProviderCursor || id == ProviderCodex {
		entries = arrayOf(objectOf(root["hooks"])[event])
	} else {
		entries = arrayOf(objectOf(root[antigravityOwnerKey])[event])
	}
	return wiringContains(entries, filepath.Base(m.scriptPath(id)))
}

// wiringContains walks hook entry shapes (nested {matcher,hooks:[…]} and
// legacy flat {type,command}) looking for our script filename.
func wiringContains(entries []any, scriptName string) bool {
	for _, entry := range entries {
		obj, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if command, ok := obj["command"].(string); ok && strings.Contains(command, scriptName) {
			return true
		}
		if wiringContains(arrayOf(obj["hooks"]), scriptName) {
			return true
		}
	}
	return false
}

func shellQuote(path string) string {
	return "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
}

// readJSONObject loads a JSON object preserving number fidelity.
func readJSONObject(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var root map[string]any
	if err := decoder.Decode(&root); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return root, nil
}

// backupAndWriteJSON writes root back atomically, keeping one rolling
// backup beside the original (before the first modification of this run).
func backupAndWriteJSON(path string, root map[string]any) error {
	if original, err := os.ReadFile(path); err == nil {
		backup := path + ".solco-hook-backup"
		if err := atomicWrite(backup, original, 0o644); err != nil {
			return fmt.Errorf("backup %s: %w", backup, err)
		}
	}
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(root); err != nil {
		return err
	}
	return atomicWrite(path, buf.Bytes(), 0o644)
}

// atomicWrite writes via a same-directory temp file plus rename, creating
// the parent directory when missing (fresh machines may lack ~/.claude or
// ~/.gemini/config entirely).
func atomicWrite(path string, data []byte, mode os.FileMode) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	tmp := path + ".tmp-solco"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

func objectOf(value any) map[string]any {
	if obj, ok := value.(map[string]any); ok {
		return obj
	}
	return map[string]any{}
}

func arrayOf(value any) []any {
	if list, ok := value.([]any); ok {
		return list
	}
	return nil
}
