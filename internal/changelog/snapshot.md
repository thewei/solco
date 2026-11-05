# Changelog

Curated by hand before every release — grouped into New / Fixed / Improved /
Internal from the release's commits (see `.agents/skills/solco-release`). The
current release's section is snapshotted into `internal/changelog/snapshot.md`
at release time and shown in Settings → About.

## v0.13.0 — 2026-11-05

### New

- Assistants: each Assistant is now one ongoing chat with its Coordinator, plus a Needs-you inbox on Mac and phone
- Assistants replace the old Projects work board; its data stays reachable through a read-only export
- Assistants: real group chats bring several Assistants into one conversation, on desktop and phone
- Assistants: per-Assistant work style — versioned instructions, a chosen Agent/Model/effort and MCP grants
- Goals: the Coordinator plans milestones, Solco runs and verifies the tasks, and the goal closes only on evidence
- Task runs: isolated worktrees, required checks, reviewable proof — nothing merges until you Accept it
- Routines: proposed by an Assistant from chat, fired only after your approval; a hand edit pauses until re-approved
- Memory: each Assistant keeps an editable Markdown memory folder with versions, conflict safety and review
- Proactivity: choose respond / suggest / follow-up per Assistant, with quiet hours and a daily start budget
- Autonomy: webhook and event triggers with per-Assistant keys and receipts; a ledger of system-started work
- Skills: an Assistant can use named skills — you authorise each one and the report shows which were used
- PR watch: an Assistant can watch a GitHub pull request and surface deduped events in the inbox
- Reports: a run's report is delivered back into the original omp conversation and marked accepted or verified
- Activity: a replayable Activity history of each Assistant's durable work, on desktop and phone
- Automations: the plugin's binary and Go module are now Solco's own (`solco-automations`); rebuild the plugin after updating (MIT credit to the upstream project stays)
- Automations: run headless so no trust dialog can stop an unattended run
- Automations: unread failed/finished runs count in the Header and show in a new Automations tab of its popover
- Automations: a Mac notification and a phone notification for each step — a draft waiting for approval, a run starting, finishing or failing
- Phone: an Automations screen (what runs when, last result, drafts waiting), opened from Home or by tapping an automation notification
- Phone: manage automations like on the Mac — approve drafts, run now, pause/resume, edit, delete, start the scheduler, see a run's history and jump to its pane, create one, or ask an Agent to draft it
- Remote Access: "Keep Running When Solco Quits" — a background service keeps phones working while the app is closed
- Phone: each Assistant's conversation, inbox and Coordinator start — the full Assistants experience on Android
- Phone: optional Android system notifications for Assistant and automation steps, with tap-to-open routing
- Phone: Assistant replies stream live into Chat and missed frames recover after a drop

### Fixed

- Stability: fixed a crash after the app ran for hours — bridge messages no longer enter the WebView's JavaScript heap
- Assistants: streaming survives drops and transcript shifts; uncertain reports are reconciled, not guessed
- Assistants: a retried run never repeats a prompt the Agent transcript already holds
- Assistants: a review merge never touches files in the main checkout
- Assistants: a run that cannot start says so instead of hanging
- Assistants: stopping a Coordinator closes its pane; a run finishing while the daemon stops stays resumable
- Assistants: a closed workspace ends the run as cancelled, with the reason in the inbox
- Chat: the stale Stop button retires when the Agent finishes and the first Send goes through
- Automations: an empty page no longer flashes a "Loading…" spinner every few seconds — only the first read shows one
- Automations: the scheduler's login service no longer shares its name with the upstream plugin's
- Automations: turning on Claude Code in Settings → Agents also records Claude's trust for the worktree folder, so runs no longer stop on "trust this folder"; a run that still meets that question (Claude or Codex) answers it for its own worktree or repository only — project MCP approvals are still yours to give
- Automations: an Agent picked in the form is saved under the kind Herdr accepts
- Automations: a Routine edited by hand pauses and asks for a fresh approval instead of firing the old definition
- Automations: a failed routine names the stable gate reason
- Automations: the scheduler keeps Assistants alive without Herdr sessions and wakes for queued work without duplicates
- Dialogs: the remote, New Task and New Automation forms label every field above them
- Sessions: an Assistant run no longer leaves its marker in the session title
- Terminal: apps reporting program status (OSC 7501) update the tab again; Solco's own agent hooks win
- Tray: the popover follows the tray icon's space and display, fixing flicker on fullscreen displays
- Phone Chat: reconnects are fenced and WebView updates publish after render, ending ghost updates
- Config: settings writes are serialised and fsynced — stale full-file saves can no longer clobber newer ones

### Improved

- Onboarding: the first-run guide is centred with more breathing room
- Assistants: typed @ mentions, bounded group delivery and chat-first polish from the 2026-11-04 audit
- Phone: Home lists agents as calm borderless rows with per-row actions under ⋯

### Internal

- Wire contracts: Host golden fixtures and the mobile Zod schemas stay in sync (automated check)
- Assistants: closed the 3.1 parity ledger (1,193 cases) and wired the checker to named tests; Mac.app and phone acceptance rigs
- Automations: one dispatcher owns scheduling, with a legacy-rule ledger and history bridge

## v0.12.2 — 2026-11-03

### New

- Remote Desktop: watch and fully control this Mac from the phone (off by default; Settings → Remote Desktop, approval dialog on the Mac)
- Remote Desktop: remembered phones with Forget, privacy blackout, lock-screen unlock, and a menu-bar indicator while a session is live
- Remote Desktop: peer-to-peer video with adaptive bitrate; reconnects in about 8 s after the phone changes network
- Git: panel redesigned after Fork — commit list with ref chips, review strip, status tiles, pull/squash/reflog/tag/stash/submodule actions
- Git: auto-fetch for the open repository, persistent sidebar folds, double-click to check out
- Automations: a native view over the new Solco automations plugin (drafts need approval before they fire)
- Services: the sidebar shows each project's scripts and services, tinted by whether their port answers
- Agents: toggles in Settings choose which Agents the New Tab menu offers, and a first-run guide walks new installs through setup
- Chat: `/` and `$` skills autocomplete anywhere in the draft; a New Pane Agent launch shows Chat starting at once
- Settings: red dot on updates and a multi-release changelog summary in About
- Phone: the Host follows the Mac's IP changes, so paired phones keep finding it

### Fixed

- Chat: a launched Agent pane now opens on Chat reliably instead of flashing Terminal
- Chat: the workspace home opens on each pane's remembered surface
- Git: selected file rows use the neutral selection tint, and the sidebar keeps its vibrancy with a changes list
- Phone Git: pull and push follow the branch's upstream
- Remote Desktop: a capture stream that stops on its own is restarted instead of failing the session


## v0.12.0 — 2026-11-02

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



## v0.11.0 — 2026-11-01

### New
- Chat: a new chat view — markdown turns, attachments, voice dictation, find, and smooth jumps in long conversations
- Chat: follow-up messages queue into an ordered stack — Enter queues while busy; drag or ⌥↑/⌥↓ to reorder, edit in place, send now, or cancel
- Sub-agents: Claude Code, Codex and omp sub-agents appear as chat cards with a resizable read-only drawer
- Sub-agents: nested in the sidebar, header popover and a Tasks tray section (starts folded)
- Chat: Cursor Agent and Antigravity CLI join the agent picker, with thinking-effort choices and terminal hand-off
- Sessions: Cursor Agent and Antigravity CLI sessions stream in live
- Chat: a context-window card shows used vs. window tokens, cache hit rate and totals, with a Compact button
- Chat: the composer offers a catalog of slash commands, skills and models
- Remote: a new Remote Access settings page — Off, This Mac only, Local network or Tailscale, QR phone pairing and Revoke
- Remote: paired phones get chat read/write, question cards, Files, Git, quota and terminal observation
- Remote: opt-in relay and WebRTC peer-to-peer routes reach your Mac beyond the local network
- Remote: agents on the phone carry the desktop's title and last-worked time; paired phones show an Online marker
- Menu bar: the tray is redesigned around one status snapshot of your workspace
- Tab bar: browser-style tabs move to the title bar by default; Settings > General can restore the sidebar tree
- Changes: a clean working tree becomes a status page with Push/Pull and recent commits
- Files and Hosts open on friendly start pages when nothing is open
- Projects: a new Projects page embedding chat and project tasks in one place
- Git: AI commit messages use a configurable OpenAI-compatible endpoint, with a Test request in Settings > Git
- Hooks: a launch-time notice flags outdated status hooks and links to Settings > Agents to update
- Updates: the app installs signed updates straight from GitHub releases
- Settings: a search box finds options; Voice and chat appearance get dedicated pages
- Shortcuts: view and tab shortcuts join the menus, documented in a Keyboard Shortcuts sheet
- Windows: Keep Window on Top now lives in the Window menu

### Fixed
- Chat: a conversation reads the same from every entry — ordered history, no duplicate invitation, correct scroll
- Chat: conversations starting with a skill invocation no longer open with the first message missing
- Chat: a brand-new agent opens on its invitation instead of loading forever while the session file is written
- Chat: an undeliverable queued message retries up to the next turn boundary
- Chat: wide tables scroll sideways instead of collapsing their columns
- Chat: your short messages stay right-aligned
- Chat: the outline rail appears at any usable window width
- Chat: composer panels open one at a time, stay within the window, and search stays pinned
- Chat: panes waiting without a prompt no longer re-read the transcript on every update
- Terminal: mouse reporting stops cleanly when a session exits, ending stray input echo
- Agents: launches wait for the shell to settle, so agents no longer start into a busy shell and vanish
- Panes: untitled panes take their tab's name instead of the launch command
- Hosts: virtual network interfaces no longer appear in the LAN address list
- Projects: task sheets close safely, the embedded chat binds to the right agents, project keys no longer collide
- Windows: views open on their own background instead of flashing white
- Sessions: Codex AGENTS.md and app-page injections no longer become session titles
- Tab bar: the selected chip stays in view after you pick it, and the Workspace row stays lit in tab-bar mode

### Improved
- Chat and Files previews feel native — text selection, system context menu, Quick Look, and links stay in-app
- Chat: long conversations render in a virtual list, keeping scrolling smooth at any length
- Chat: file-name mentions, folded long prompts and code, SVG preview, image lightbox, persistent drafts
- Chat: edit any sent message and resend; copy replies as Markdown or plain text
- Chat: a quieter composer — hints on demand, dated footer, short model chip, one Open Terminal
- Chat: the model chip shows the agent's default before the first prompt; the outline rail has a Settings switch
- Chat: replies stopped mid-turn carry a clear stopped marker; Codex shows a hooks-verified mark
- Sessions: rows open on click with hover actions, group by day, and animate in as filters change
- Header popover: agents first with Needs you / Working / Idle chips, plus Quota, Services and Tasks segments
- Sidebar: agents grouped by workspace with shared git facts and last-worked times
- Panes: each pane reopens on the surface you left it on
- Quota: the Usage page is renamed Quota; Needs-a-look highlights the tightest window and bars ease in on load
- Quota: borrowed Claude Code and Cursor sign-ins are cached locally, so opening Quota no longer touches those apps
- Command Center: shows menu shortcuts, gains a Quota command, and filters with scope chips
- Files: the launcher centers and fits its content, names keep their extensions, sizes load on demand
- Hosts: name-first rows with user@host on hover; the sidebar aligns under the tab strip
- Remote: /launch runs a first Terminal command; the composer keeps your draft while you navigate
- Motion: one shared timing — tabs grow and fold, rows and cards fade in, quota bars and folders animate

### Internal
- Site: dark redesign, platform-based download recommendations, dynamic latest-release links
- Updater: portable signed applog builds for release artifacts
- Chat: the projection layer extracted into a shared chatsession package; Question parsing shared with the phone Host
- Chat: ordered message parts and pane-scoped delivery plumbing across history and herdr
- Usage: shared HTTP JSON reader tweaks; the Antigravity OAuth client comes from the environment
- Housekeeping: legacy compatibility layer and old-name references removed; skills renamed under the Solco name
- Install skill signs with a stable local identity so macOS permissions persist across reinstalls
- Frontend: virtual-list dependency and host feature flags for the one-file phone build
- Docs: design records for chat, queue, quota, remote host and the UX audit rounds; audit regression tests
