---
name: solco-development
description: Use when developing, debugging, reviewing, packaging, or handing off the Solco macOS client (Go + MyGo Native UI) — the Herdr runtime/protocol boundary, native Terminal attach, pane layout projection, Agent conversation/history semantics, Git Workbench, or release validation. Enforces Solco client identity, Herdr runtime authority, mature-API-first implementation, native-first presentation (one Chat WebView page shared with the phone), visible-only attachments, one canonical shell, bounded logging, and documentation truth alignment.
---

# Solco Development

Use this skill for engineering work in the Solco repository (Go 1.27.1 + MyGo Native UI; Herdr is the runtime authority). The Rust/GPUI implementation is not on this branch.

The skill controls process. Architectural truth lives in `docs/client-product-architecture.md`; do not duplicate or override it here.

## Mandatory first reads

Before changing code:

1. read `AGENTS.md` and `CLAUDE.md`;
2. read `docs/client-product-architecture.md` (its §1 is the canonical Workspace/Project/Herdr domain language);
3. when the work touches the active migration contract, read `docs/mygo-native-execution-rules.md` and `docs/mygo-native-migration-roadmap.md`.

Then inspect:

```sh
git status --short --branch
git diff --stat
git diff --cached --stat
```

Preserve user/staged changes. Do not reset, stage, commit, or push unless the user explicitly asks.

## Product/runtime boundary

Keep the names and ownership distinct:

- **Solco** is the application, product, shell, package, bundle, release artifact, and user-facing brand.
- **Project** is the Solco product concept backed by one Herdr runtime workspace.
- **Herdr** is the actual backend/runtime. Genuine Herdr API, protocol, CLI, socket, `Workspace` runtime type, `workspace_id`, `workspace.*` methods, errors, and integration names stay Herdr.
- Never use a Herdr runtime workspace as though it were a Solco Workspace; map it through the Project projection boundary. Never rename a real Herdr backend concept merely to make a branding search cleaner. Never present the client itself as Herdr.

## Mature-semantics-first ladder

Before hand-writing a capability, search the owner ladder in this order:

1. Herdr API;
2. MyGo Native UI components and official plugins (Terminal/Ghostty included);
3. a proven compatible implementation pattern;
4. custom Solco code.

If custom code is necessary, record the concrete reason: missing API, ownership conflict, or measured performance limitation.

## Ownership router

- Herdr daemon bindings (discovery/bootstrap, socket RPC, events, direct terminal attach) → `internal/herdr`
- MyGo-native desktop presentation (shell, router, pages, theme/icons/components, dialogs) → `internal/nativeui`
- Agent product semantics (launch, conversation, handoff, usage) → `internal/agent`
- application service coordination → `internal/app`; client preferences → `internal/settings`
- read-only coding-agent history domain → `internal/history`
- Git review model (status/diff/commit) → `internal/gitworkbench`
- Right Panel Files tool → `internal/filesview`; local preview → `internal/preview`
- resident service monitoring → `internal/services`; command entry → `internal/commandcenter`; user scripts → `internal/scripts`
- diagnostics & log export → `internal/diagnostics`; bounded structured logging → `internal/applog`
- app entrypoint/window lifecycle → `main.go`

Do not put a capability in `main.go` merely because that is convenient.

## Herdr runtime rule

Herdr remains authoritative for workspaces, tabs, panes, layouts, terminal sessions, Agent runtime state, persistence, scrollback, and process lifecycle.

Do not introduce: a second Herdr runtime, a client-owned PTY/pane-layout model/runtime registry, external-terminal launchers for runtime continuation, or invented socket methods and event shapes. When a required runtime capability is absent, document the protocol gap and solve it at the Herdr boundary.

For Herdr socket/event work, inspect the live protocol schema before changing request shapes. Treat protocol compatibility as an explicit verified range (current baseline: Herdr 0.9.3 / protocol 22), not exact-version equality and not open-ended forward compatibility; a successful ping followed by an unsupported protocol is a compatibility error, not proof the service is unavailable. For nontrivial commands, verify request params, outer success discriminator, and nested result field against the schema, then add a schema-shaped contract test. Validate each subscription's required fields (global vs pane-scoped), synchronously validate the initial `events.subscribe` acknowledgement, and never let a failed event stream leave a startup overlay permanently active.

For nontrivial Herdr-owned runtime capabilities, verify all three applicable surfaces before designing client state: snapshot/query, controller/command, and incremental event. A bounded query result must not be mistaken for the runtime authority when Herdr exposes a controller plus correction event. For structural mutations, apply the authoritative result payload to the smallest local projection and fetch only genuinely missing metadata; reserve full snapshot refreshes for bootstrap, explicit manual refresh, or exceptional recovery.

`session.snapshot.layouts` is the Pane topology authority: native UI maps visible Pane rectangles into same-window MyGo Terminal elements running `herdr terminal attach <terminal_id>`. Automatic attach never uses `--takeover`; hidden Tabs keep no attachment fleet. Navigation never attaches a terminal or reimplements a focus chain as a side effect.

## Native UI rule

Chat UI is the official MyGo `ui.WebView` page over the single bound-service bridge (product decision 2026-10-11; the page talks only to its own bridge from the app content origin). History and every other surface stay Native UI until their own explicit task moves them. Still forbidden: private AppKit embedding, a localhost HTTP backend for desktop presentation, a second desktop presentation backend, and a second Chat page implementation.

`frontend/src` is the one Chat page source for BOTH clients: the phone (solco-mobile) embeds a generated snapshot, it never forks the page and the page gains no per-client branches. A `frontend/src` change is not done until `bun run build:mobile` (solco/frontend) + `pnpm sync:chat-page` (solco-mobile) have refreshed the snapshot; `pnpm check:chat-page` must pass afterwards. `solco-mobile/src/generated/chat-page.ts` is generated output — never hand-edit it, and never patch presentation only on one client.

Use MyGo native widgets before custom drawing; reuse `theme.go`, `icons.go`, and `components.go` before one-off page styling. One canonical shell: do not add alternate Sidebar implementations or duplicate row/navigation builders for the same shell state; improve the canonical path in place. Keep framework/platform code out of `internal/herdr` and ordinary domain packages.

Workspace primary-surface navigation has one owner pair in `internal/nativeui` (2026-11-03): `showPaneSurface(paneID)` for "go to this pane" and `landSelectedPane()` for "back to the workspace home" (header home tab, ⌘1 / Go-menu Workspace, palette Workspace, view-tab exits). They land the pane's remembered surface (`paneSurfaceMemo` via `panePrefersChat`) and NEVER rewrite it — `paneSurfaceMemo` is written only by explicit choices (card-header toggle, Open Terminal actions, `openChat` success/invitation). New navigation entry points must ride these owners; never hand-write a `panePrefersChat ? Chat : showViewTerminal` ternary and never call `showViewTerminal` right after `selectPane` (both shapes persisted a Chat-pinned pane on Terminal — 2026-11-02 flash and 2026-11-03 revert loops, `docs/debug/`).

## Logging and privacy

Application logs are structured and size-bounded (`internal/applog`, MyGo `PathLogs`). Never log terminal output, prompt/conversation bodies, credentials, environment dumps, or provider secrets. Log operation names, stable runtime IDs, errors, state counts, and timing needed for diagnosis.

## Verification

Normal gate:

```sh
GOTOOLCHAIN=auto go test ./...
GOTOOLCHAIN=auto go tool mygo build
git diff --check
git diff --cached --check
```

For packaging changes also: build the `.app`, verify `Solco.app` exists under `build/darwin-arm64/`, verify the executable runs, lint `Info.plist`, and perform available signing/structural verification. Packaging identity lives in `mygo.config.ts` (app + frontend keys, since the N19 chat WebView frontend).

Real-app UI acceptance (only-the-real-app questions: picker contents, multi-Pane input/resize, rendering) drives a real app run through the Computer Use tools with mandatory isolation — temporary `HOME` + dedicated `HERDR_SOCKET_PATH`, never the user's live instance — and asserts Herdr/protocol ground truth over pixels alone, recording PASS/FAIL evidence per scenario. The Rust-era acceptance harness scripts were removed with the Rust implementation; rebuild the harness entrypoints against the Go app before the next acceptance session.

Bug investigations, behavior misidentification, and any fix needing on-device proof run the `.agents/skills/solco-evidence-loop/SKILL.md` loop instead of an ad-hoc path: isolated environment first, failing-before device evidence, hunt-contract root cause, fix, passing-after evidence in the same environment; it maintains `docs/debug/` progress files and self-updating lessons.

## Completion review

Before handoff or commit:

- [ ] intended diff only; no user changes reset/staged accidentally;
- [ ] Solco is the client identity in product surfaces; Herdr remains factually named at the backend boundary;
- [ ] one canonical shell path remains; no second runtime/PTY/layout owner introduced;
- [ ] all static gates pass; relevant runtime smoke done; manual-only acceptance explicitly listed;
- [ ] canonical architecture matches code; required third-party notices preserved;
- [ ] no commit unless explicitly requested.
