// Package settings owns Solco client preferences for the MyGo rewrite.
// It is UI- and platform-framework-independent.
package settings

import (
	"context"
	"sync"
)

const CurrentSchemaVersion = 1

type Settings struct {
	SchemaVersion int               `json:"schema_version"`
	General       GeneralSettings   `json:"general"`
	Terminal      TerminalSettings  `json:"terminal"`
	Workbench     WorkbenchSettings `json:"workbench"`
	Shortcuts     ShortcutSettings  `json:"shortcuts"`
	Session       SessionSettings   `json:"session,omitempty"`
	// Voice configures dictation against an OpenAI-compatible
	// transcription endpoint (local proxies included). The API key is a
	// secret: never logged, never echoed.
	Voice VoiceSettings `json:"voice,omitempty"`
	// Usage is the Usage page's switches. Tokens are never kept here: they
	// live in Solco's encrypted credential file (internal/secretstore).
	Usage UsageSettings `json:"usage,omitempty"`
	// Remote is the phone-client Host's listener choice (product decision
	// 2026-10-31). The zero value is off: nothing listens until the user
	// picks a mode in Settings → Remote Access.
	Remote RemoteAccessSettings `json:"remote_access,omitempty"`
}

// RemoteAccessSettings is the Remote Access page's choice. Mode is "" / "off",
// "loopback", "lan" or "tailscale" (internal/remotehost.Mode); Address is the
// single interface address picked for lan/tailscale. Pairing codes, device
// tokens and the TLS key never live here (internal/remotehost owns them).
// The relay fields are the opt-in route through the user's own self-hosted
// alma-relay: URL is the bare https:// or wss:// base, RegistrationKey is the
// relay's shared key (a relay credential, not a Solco secret). This Mac's own
// relay connect code lives in <userDataDir>/RemoteHost/relay-secret.
type RemoteAccessSettings struct {
	Mode    string `json:"mode,omitempty"`
	Port    int    `json:"port,omitempty"`
	Address string `json:"address,omitempty"`

	RelayEnabled bool   `json:"relay_enabled,omitempty"`
	RelayURL     string `json:"relay_url,omitempty"`
	RelayKey     string `json:"relay_key,omitempty"`

	// P2P is the WebRTC direct path a phone may open through whichever route
	// already works. It is on by default (zero value) and only does anything
	// while the Host is on; P2PDisabled switches it off. P2PICE are the user's
	// own STUN/TURN URLs (comma separated; empty = Solco's built-in STUN list),
	// P2PTurnUser/P2PTurnPass the credential those turn: URLs share.
	P2PDisabled bool   `json:"p2p_disabled,omitempty"`
	P2PICE      string `json:"p2p_ice,omitempty"`
	P2PTurnUser string `json:"p2p_turn_user,omitempty"`
	P2PTurnPass string `json:"p2p_turn_pass,omitempty"`
}

// UsageSettings are the Usage page preferences (product decision 2026-10-30):
// which providers Solco may contact (each is opt-in, nothing is read before
// the user switches it on), the Zhipu site, and the Used/Left reading.
type UsageSettings struct {
	// Configured is set once the first-run choice was made.
	Configured bool `json:"configured,omitempty"`
	// Enabled maps a provider id (internal/usage.ID) to its opt-in.
	Enabled map[string]bool `json:"enabled,omitempty"`
	// SignInAllowed maps a provider id to the user's yes for reading another
	// app's saved sign-in (Antigravity's OAuth login); cleared with its switch.
	SignInAllowed map[string]bool `json:"sign_in_allowed,omitempty"`
	// ZhipuSite is "global" (api.z.ai, the default) or "china" (open.bigmodel.cn).
	ZhipuSite string `json:"zhipu_site,omitempty"`
	// ShowLeft shows what is left instead of what is used.
	ShowLeft bool `json:"show_left,omitempty"`
}

// VoiceSettings points dictation at an OpenAI-compatible
// /audio/transcriptions endpoint. An empty API key falls back to the
// OPENAI_API_KEY environment variable.
type VoiceSettings struct {
	// Source is "" / "api" (the endpoint below) or "chatgpt" (the sign-in
	// Codex already holds). "chatgpt" only takes effect with ChatGPTConsent.
	Source string `json:"source,omitempty"`
	// ChatGPTConsent records the user's explicit yes to using that sign-in;
	// switching back to the API endpoint clears it.
	ChatGPTConsent bool   `json:"chatgpt_consent,omitempty"`
	EndpointURL    string `json:"endpoint_url,omitempty"`
	APIKey         string `json:"api_key,omitempty"`
	Model          string `json:"model,omitempty"`
}

type GeneralSettings struct {
	Appearance    string `json:"appearance"`
	RestoreWindow bool   `json:"restore_window"`
	// Density configures the sidebar spacing (0.9 P19): "compact", "default", "comfortable".
	Density string `json:"density,omitempty"`
	// HighContrast enables accessible higher-contrast borders and text tones (0.9 P19).
	HighContrast bool `json:"high_contrast,omitempty"`
	// ChatSerif sets the Chat reading text (assistant prose) in a serif face;
	// code, tool rows and the interface stay sans. Default off.
	ChatSerif bool `json:"chat_serif,omitempty"`
	// ChatOutline shows the Chat's conversation outline (the tick rail in the
	// left gutter that jumps between your messages). Default on; always
	// persisted (no omitempty) so an omitted key reads as the default and an
	// explicit off survives, like WebViewChat.
	ChatOutline bool `json:"chat_outline"`
	// WebViewChat renders the /workspace Chat surface in the official MyGo
	// ui.WebView over the presentation-only chat bridge (N19, product
	// decision 2026-10-11). Off keeps the Native chat surface; the native
	// implementation retires only at WebView acceptance parity. Always
	// persisted (no omitempty): the default is true, so an omitted key
	// could never represent an explicit off.
	WebViewChat bool `json:"webview_chat"`
	// WebViewFilesPreview renders the /files preview center in the official
	// MyGo ui.WebView over the read-only filesPreview bridge (product
	// decision 2026-10-13). The native preview renderer is retired; off
	// shows the truthful "preview off" states instead. Always persisted,
	// same reason as WebViewChat.
	WebViewFilesPreview bool `json:"webview_files_preview"`
	// WorkboardView picks how the herdr-projects work board shows up when
	// the plugin is installed: "board" (the default: Project/task rows open
	// the plugin's own board Tab) or "native" (Solco's Project and task
	// pages). User decision 2026-10-26; the native pages stay under trial.
	WorkboardView string `json:"workboard_view,omitempty"`
	// TabsPlacement says where a Workspace's Tabs live: "" or "titlebar" (the
	// default — a browser-style Tab bar takes the breadcrumb's place and the
	// sidebar tree lists only Workspaces) or "sidebar" (the tree lists Tabs
	// and Panes too). Stored as a word so an absent key means the default.
	TabsPlacement string `json:"tabs_placement,omitempty"`
	// AgentView is the view an Agent pane opens in when it has no remembered
	// one of its own: "" or "tui" (the terminal, the default) or "chat" (the
	// Chat page). A pane the user has switched keeps its own choice.
	AgentView string `json:"agent_view,omitempty"`
	// Opacity controls the transparency of the application window shell
	// (0.50 to 1.00). An unset (0) or out-of-range value defaults to 1.00 (opaque).
	Opacity float64 `json:"opacity,omitempty"`
}

// TabsInTitleBar reports Show Tabs in Sidebar left off (the default).
func (g GeneralSettings) TabsInTitleBar() bool { return g.TabsPlacement != "sidebar" }

// AgentChatFirst reports the Chat default; anything but "chat" keeps the TUI.
func (g GeneralSettings) AgentChatFirst() bool { return g.AgentView == "chat" }

// WorkboardNative reports the opt-in native work board pages; anything but
// "native" (including unset) keeps the plugin's own board.
func (g GeneralSettings) WorkboardNative() bool { return g.WorkboardView == "native" }

// EffectiveOpacity returns the window shell opacity, clamped to [0.50, 1.00].
// Unset or invalid values (<= 0 or > 1.00) safely default to 1.00 (fully opaque).
func (g GeneralSettings) EffectiveOpacity() float64 {
	if g.Opacity <= 0 || g.Opacity > 1.0 {
		return 1.0
	}
	if g.Opacity < 0.5 {
		return 0.5
	}
	return g.Opacity
}

type TerminalSettings struct {
	FontFamily  string  `json:"font_family"`
	FontSize    float64 `json:"font_size"`
	LineHeight  float64 `json:"line_height"`
	Scrollback  int     `json:"scrollback_bytes"`
	OptionAsAlt bool    `json:"option_as_alt"`
	// ThemeName pins a Ghostty theme by name (terminal.GhosttyTheme):
	// both window kinds use it, as Ghostty's theme = <name> does. Empty
	// follows the window's appearance with the gorex palettes.
	ThemeName string `json:"theme_name"`
	// Transparent leaves newly attached terminal views' background
	// undrawn (MyGo terminal.Transparent, Ghostty background-opacity
	// equivalent) and paints the pane card translucent, so the window's
	// gorex gradient shows through. Presentation only: it never touches
	// Herdr-owned terminal identity or program output.
	Transparent bool `json:"transparent"`
}

// WorkbenchSettings holds Git Workbench presentation preferences (0.10).
// DefaultCommitPrompt is the editable half of the user's commit-message
// request: style only. The safety rules are fixed in code
// (agent.BuildCommitPrompt), so editing this can't drop them. Plain
// preference text, never a secret/API key.
const DefaultCommitPrompt = `Use Conventional Commits: type(scope): imperative summary, maximum 72 characters.
Choose type from feat, fix, refactor, perf, docs, test, build, ci, chore.
Scope is optional. Prefer a concise English subject without a period.
Add a body only if it explains WHY a change was needed or meaningful tradeoffs; use short bullet points.`

// CommitAPI configures the AI commit-message generator. Endpoint is the
// base URL of an OpenAI-compatible provider; "/chat/completions" is
// appended unless already present. Model is the provider's model name.
type CommitAPI struct {
	Endpoint string `json:"endpoint,omitempty"`
	Model    string `json:"model,omitempty"`
}

// WorkbenchSettings controls presentation and safe AI drafting preferences.
type WorkbenchSettings struct {
	// CommitAPI is the one AI commit-message channel: an OpenAI-compatible
	// chat/completions endpoint. The API key is not stored here — it lives
	// in Solco's encrypted credential file (internal/secretstore, key git.commitapi.token).
	CommitAPI CommitAPI `json:"commit_api,omitempty"`
	// CommitPrompt overrides the default commit convention, never sent
	// automatically. Per-app preferences are separate from Git operations.
	CommitPrompt string `json:"commit_prompt,omitempty"`
	// SplitDiff persists the unified/split diff layout choice (GWB-174).
	SplitDiff bool `json:"split_diff,omitempty"`
	// PinnedPanes maps a Herdr instance name to its pinned Pane ids
	// (2026-10-05 sidebar review). Cosmetics only: Herdr owns panes, the
	// client only remembers which ones the user pinned to the sidebar top.
	PinnedPanes map[string][]string `json:"pinned_panes,omitempty"`
	// GraphHidden maps a repository root to the full ref names the user hid
	// from the Commits graph (the sidebar eye). Cosmetics only: Git decides
	// what hiding removes, the client just remembers the choice.
	GraphHidden map[string][]string `json:"graph_hidden,omitempty"`
	// GraphFolded maps a repository root to the merge commits (full hashes)
	// whose merged-in branch the Commits graph folds away. Cosmetics only.
	GraphFolded map[string][]string `json:"graph_folded,omitempty"`
}

type ShortcutSettings struct {
	Search   string `json:"search"`
	Settings string `json:"settings"`
	Refresh  string `json:"refresh"`
	Back     string `json:"back"`
	Forward  string `json:"forward"`
}

// SessionSettings is the launch-restore snapshot of the shell's navigation
// and panel cosmetics, captured when the window hides or the app quits.
// Herdr owns every runtime fact: instance/project/tab/pane entries are hints
// that the shell re-validates against the live projection at restore time.
type SessionSettings struct {
	// Route is the shell page restored at launch ("" = /workspace).
	Route string `json:"route,omitempty"`
	// Instance/ProjectID/TabID/PaneID are selection hints.
	Instance  string `json:"instance,omitempty"`
	ProjectID string `json:"project_id,omitempty"`
	TabID     string `json:"tab_id,omitempty"`
	PaneID    string `json:"pane_id,omitempty"`
	// PaneSurfaces remembers which surface (terminal/chat) each pane was
	// last used on, so sidebar Agent clicks land where the user left them.
	PaneSurfaces map[string]string `json:"pane_surfaces,omitempty"`
	// Sidebar section visibility and tree expansion cosmetics.
	ProjectsOpen     bool     `json:"projects_open"`
	AgentsOpen       bool     `json:"agents_open"`
	ServicesOpen     bool     `json:"services_open"`
	PinsOpen         bool     `json:"pins_open"`
	ExpandedProjects []string `json:"expanded_projects,omitempty"`
	ExpandedTabs     []string `json:"expanded_tabs,omitempty"`
	TasksClosed      bool     `json:"tasks_closed,omitempty"`  // sidebar Projects fold, inverted: old sessions stay open
	FoldedBoards     []string `json:"folded_boards,omitempty"` // Projects rows the user folded; the rest stay open
	FoldedAgents     []string `json:"folded_agents,omitempty"` // sidebar Agents groups ("group:<workspace>") and parents' sub-agent lists ("sub:<terminal>") the user folded
	// SidebarWidth is the dragged left-sidebar width (0 = the default).
	SidebarWidth int `json:"sidebar_width,omitempty"`
	// Right panel cosmetics (Changes/Files/Services).
	RightPanelOpen    bool   `json:"right_panel_open"`
	RightPanelSurface string `json:"right_panel_surface,omitempty"`
	RightPanelWidth   int    `json:"right_panel_width,omitempty"`
	// RecentFiles are the files last opened in Files (newest first, absolute
	// paths): the start page lists the ones under the current root.
	RecentFiles []string `json:"recent_files,omitempty"`
	// RecentHosts are the ssh aliases last connected from Hosts (newest first).
	RecentHosts []string `json:"recent_hosts,omitempty"`
	// RecentLaunchKinds are the kinds last started from the Tab bar's "+ ▾"
	// (an agent id or "terminal"; newest first): + repeats the first, the
	// menu lists them in this order with Terminal last.
	RecentLaunchKinds []string `json:"recent_launch_kinds,omitempty"`
	// AgentsSort is the sidebar Agents list's grouping mode: "" or "workspace" /
	// "group" (by Workspace, the default), "time" (by time), or "status" (by
	// status). Anything else reads as the default.
	AgentsSort string `json:"agents_sort,omitempty"`
	// AgentsSortByTime toggles whether cards within groups are sorted by update time
	// instead of the default Pane order.
	AgentsSortByTime bool `json:"agents_sort_by_time,omitempty"`
	// AgentsSortAscending is true for oldest-first when sorting by updated time.
	AgentsSortAscending bool `json:"agents_sort_ascending,omitempty"`
	// HookSessions remember, per "<instance>/<pane>", the session identity an
	// Agent's hooks announced: Herdr's own agent list carries none for some
	// providers (Claude Code), and without it Chat cannot rebind after a
	// restart until the next hook event. No conversation content.
	HookSessions map[string]HookSessionSettings `json:"hook_sessions,omitempty"`
	// ScriptPanes remembers which pane each script/command launch ran in
	// ("<instance>/<pane>" → the command's name), so Services attribution
	// and titles survive a restart. Command names only — never command
	// lines, output or paths beyond the pane's own.
	ScriptPanes map[string]string `json:"script_panes,omitempty"`
}

// HookSessionSettings is one remembered hook-reported session identity.
type HookSessionSettings struct {
	Agent      string `json:"agent"`
	Kind       string `json:"kind"`
	Source     string `json:"source,omitempty"`
	Value      string `json:"value"`
	TerminalID string `json:"terminal_id,omitempty"`
}

func Default() Settings {
	return Settings{
		SchemaVersion: CurrentSchemaVersion,
		General: GeneralSettings{
			Appearance:          "system",
			RestoreWindow:       true,
			Density:             "default",
			HighContrast:        false,
			ChatOutline:         true,
			WebViewChat:         true,
			WebViewFilesPreview: true,
			Opacity:             1.0,
		},
		Voice: VoiceSettings{
			EndpointURL: "https://api.openai.com/v1",
			Model:       "whisper-1",
		},
		Terminal: TerminalSettings{
			FontFamily:  "SF Mono",
			FontSize:    13,
			LineHeight:  1.08,
			Scrollback:  10 * 1024 * 1024,
			OptionAsAlt: true,
		},
		Workbench: WorkbenchSettings{},
		Shortcuts: ShortcutSettings{
			Search:   "Cmd+K",
			Settings: "Cmd+,",
			Refresh:  "Cmd+R",
			Back:     "Cmd+[",
			Forward:  "Cmd+]",
		},
	}
}

type Store interface {
	Load(context.Context) (Settings, error)
	Save(context.Context, Settings) error
}

// MemoryStore is useful for app-service and UI tests; production persistence
// uses FileStore.
type MemoryStore struct {
	mu       sync.RWMutex
	settings Settings
}

func NewMemoryStore(initial Settings) *MemoryStore {
	return &MemoryStore{settings: initial}
}

func (s *MemoryStore) Load(ctx context.Context) (Settings, error) {
	if err := ctx.Err(); err != nil {
		return Settings{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings, nil
}

func (s *MemoryStore) Save(ctx context.Context, value Settings) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	s.settings = value
	s.mu.Unlock()
	return nil
}
