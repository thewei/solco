
### New
- Mobile: phone-native bottom tabs for Agents, Changes, Files, Sessions and Quota with adaptive capability gating
- Mobile: full Git review, file staging/unstaging, commit composition and sync actions straight from the phone
- Mobile: bridged device switching between your Mac and remote Herdr machines over the secure desktop SSH bridge
- Mobile: Sessions Trash with swipe or batch-select to move, instantaneous Undo, and Put Back view
- Mobile: read-only file browser with inline previews (text, markdown, images) and Attach to Chat draft hand-off
- Mobile: live agent status notification bar and background service/sub-agent folding under active agent rows
- Desktop: tab strip with equal-width shrinking, drag-and-drop reordering, and an All Tabs overflow menu
- Desktop: new-agent tabs open as instant Chat drafts without registering empty sessions in Herdr
- Desktop: first-class Pi provider integration with interactive question dialogs and compaction markers
- Chat: render Mermaid blocks as interactive diagrams with an inline source code view toggle
- Sub-agents: automatic tree folding after 10 minutes idle, keyboard fold-all shortcuts, and running cues
- Agents: sort and group sidebar agents by workspace, status, or time with orthogonal secondary sorting
- Security: credentials migrate from macOS Keychain to an AES-256-GCM encrypted local store keyed to platform UUID

### Fixed
- Chat: refuse queueing follow-ups cleanly until the session conversation link is established
- Chat: clear stale model selections on session rebind and fix Antigravity default model resolution
- Remote: keep relay tunnels alive when the local network listen address changes or vanishes
- Remote: log WebRTC candidate types and session lifespans safely without leaking private network addresses
- Remote: answer WebRTC P2P offers promptly once the first reflexive candidate receives a short grace period
- Mobile: stack blocked connection cards properly and keep tool rollups on a single line
- Native UI: Sessions row context menus reliably deliver selections after the pointer leaves the row
- Native UI: system notification clicks activate the main Solco window and restore preferred view states
- Git: apply network timeout budgets for fetch, pull, and push operations with progress indicators

### Improved
- Mobile: fluid cross-fade transition and zoom motion from the tapped composer into full Chat
- Mobile: collapsible composer rises smoothly with keyboard motion without jumping or lag
- Mobile: race direct LAN and relay paths to pick the fastest connection with smart fallback
- Desktop UI: theme-faced native tooltips and multi-fact hover cards aligned with the design token system
- Desktop About: Settings → About page presents the release changelog and version details
- Desktop Hooks: outdated shell hook scripts for agent providers are automatically and silently updated at launch
- Desktop Quota: header popover draws the unified quota visualization card matching the full /quota page
- Desktop Webview: disable pinch-zoom in embedded chat views to preserve native layout density

### Internal
- Wire contracts: bidirectional synchronization between host test fixtures and mobile Zod schemas
- History: support Pi sub-agent parent-child relationship tracking and linked task text
- Documentation: update GEB fractal documentation L1/L2/L3 contracts and release verification protocols



