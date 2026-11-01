# Changelog

Curated by hand before every release — grouped into New / Fixed / Improved /
Internal from the release's commits (see `.agents/skills/solco-release`). The
current release's section is snapshotted into `internal/changelog/snapshot.md`
at release time and shown in Settings → About.

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
