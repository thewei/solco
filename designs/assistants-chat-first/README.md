# Assistants · chat-first (single design)

## Decision (2026-11-04)

**Chosen:** the one design in `exploration.html` — an Assistant *is* its conversation.
**Implementation authorised by the user the same day** ("我授权你在设计好之后，直接根据你的设计方案进行开发"); desktop first, phone from the same design after the desktop passes end to end.

| | |
|---|---|
| Status | Decided, implementing (desktop first) |
| Scope | UX1 chat-first page, first-send ensure, no implicit navigation, Needs-You strip, ⋯ menu, More drawer, light polling; phone parity afterwards |
| Deferred | Peer DM / Group, Directory, Mailbox, Control Center, Primary Assistant, Pin, @-mentions across Assistants, per-Assistant avatars |

## Problem

`/assistants` is an admin panel: six tabs, the Coordinator Chat is one tab among them and opens on *Needs You*. Audit of the code (2026-11-04):

- `bindChatToCard` calls `selectPane` → opening an Assistant's Chat moves the Mac user's Workspace selection (violates "navigation is client-local").
- The Chat state machine (`rebindWorkspaceChat`) only knows the *selected* pane, so a quiet bind needs it factored by card.
- A fresh pi/omp/Claude Coordinator has no transcript until its first prompt; the page must therefore reach the "type the first prompt" invitation without a source.
- The page polls a **full** snapshot every 3 s (goals, memory, tasks + one `task show` per task) even when the user only chats.
- The Coordinator must be started by a button; the person's first sentence should start it.
- The start path hard-codes `claude`; this Mac mostly runs pi/omp/Codex.

## Constraints (kept)

- Herdr is the only runtime; the Coordinator is a real Herdr Agent started by the plugin dispatcher. No new Herdr method, no second runtime.
- One shared Chat page (`frontend/src/App.tsx`) for desktop and phone; no ChatView duplicate.
- The plugin core stays the only authority (SQLite single writer, receipts keyed by `requestId`, stable refusal codes, approvals bound to an action hash).
- Logs never carry prompt/brief/memory text.

## Audit of the six plans: keep / adjust / defer

| Item (source) | Verdict | Why / change |
|---|---|---|
| Chat is the page; admin lives in ⋯ and a drawer (redesign §3–4) | **Keep** | Matches the audit; implemented as header + strip + chat + drawer. |
| Needs-You as a strip above the chat (redesign §4) | **Keep** | Reuses the inbox the plugin already computes. |
| `ensure` on first Send, idempotent (redesign §5, spec UX2) | **Keep, adjust** | Done as bridge `EnsureChat(requestId, id, agent)`; the page holds the typed text and sends it exactly once after the bind. Agent comes from a chip (default: first Chat-capable launchable kind in Solco's own MRU order), not a hard-coded `claude`. |
| No implicit Mac navigation (redesign §6) | **Keep** | `rebindChatForCard(card)` extracted from `rebindWorkspaceChat`; no `selectPane`. Leaving `/assistants` re-binds through the existing route-enter sync. |
| Light snapshot + on-demand detail (redesign §7) | **Keep, adjust** | `Snapshot` becomes light (overview + selected Assistant + approvals + inbox + coordinator); `Drawer(id)` loads goals/tasks/runs/memory only while open. Poll 4 s, paused while the page is hidden. |
| Pause semantics (plans silent/ambiguous) | **Adjust** | Pause governs dispatch (tasks, watches, new Coordinator start), not the conversation: a running Coordinator stays readable and chattable with a banner; an unstarted paused Assistant has a disabled composer ("Resume to start"). |
| Conversation vocabulary human_dm / peer_dm / group, Resolver, ChatTransport scoping (spec §3) | **Defer** | One human↔Assistant DM is all the product has; a resolver for one case is abstraction ahead of need. The `AssistantsView`/`BindCoordinator` seam already scopes a Chat to one Assistant by the plugin's own run record. |
| Directory / Mailbox / Control Center / Primary / Pin (collab plan, spec UX3–UX6) | **Defer** | Need multi-Assistant interplay that does not exist; the sidebar section is the directory. Revisit after the DM lands and is used. |
| `@` typed mentions across Assistants | **Defer** | Depends on groups. |
| Host v3 `…/conversation`, `…/ensure` for the phone (redesign §8, mobile plans) | **Keep, phase 2** | Built after the desktop passes; the phone reuses the same Chat page and an equivalent light summary. |
| Test ID sets CX / CONV / NW | **Adjust** | Only the cases that match shipped behaviour become tests (list below); deferred features carry no placeholder tests. |

## Information architecture

```
Sidebar ▸ Assistants ▸ <name>            (state dot = coordinator/needs)
Page
 ├─ Header: name · coordinator state pill · "N need you" pill · More · ⋯
 ├─ Needs-You strip (only when N>0): approvals (Approve/Deny with action hash), review/failed tasks
 ├─ Conversation (the shared Chat; before start: invitation + page composer)
 ├─ ⋯ menu: Watch a PR · New task · Rename · Pause/Resume · Restart Coordinator · New Assistant · Stop Coordinator
 └─ More drawer (360): Needs you · Watching · Tasks · Done · Memory · About
```

## States (all in `exploration.html`)

Not started · Starting (message held) · Live · Needs-you open · ⋯ menu · More drawer · Paused · Start failed (text kept, retry / pick another agent).

## Verification (desktop)

- Go: `rebindChatForCard` binds a sourceless card without changing `selectedPaneID` (red-first); `EnsureChat` idempotent per `requestId`; paused refusal; light `Snapshot` carries no tasks/memory.
- Plugin: `coordinator start` honours `agent`/`model`, default unchanged; prompt allows direct answers.
- Frontend: `bun run typecheck`, `bun run build`, `bun run build:mobile`.
- Real app (installed Solco, real Herdr, scratch Assistant, pi + magpie/glm-5.3-flash): first message starts the Coordinator, the reply streams, the Mac's selected Workspace/pane is unchanged, Needs-You strip approves an approval, Pause/Resume, drawer lists, relaunch keeps the conversation. Evidence in `docs/debug/2026-11-04-assistants-chat-first.md`.
