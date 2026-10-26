# Solco

Solco is a native macOS workspace for coding agents, built with Go and the MyGo Native UI framework (official MyGo Terminal/Ghostty surface). The previous Rust/GPUI implementation lives only on the `rewrite/mygo` and `main` branches.

Solco is the **client/product**. **Herdr remains the backend runtime** and owns workspaces, tabs, panes, terminal sessions, agents, persistence, layout state, and process lifecycle. Solco projects that runtime into a native macOS interface; it does not replace or duplicate Herdr.

## Renamed from Shardlane

Solco was called Shardlane until 2026-10-26. On first launch it moves what the old name left on the machine — `~/Library/Application Support/Shardlane` and `~/Library/Logs/Shardlane` into their `Solco` counterparts, and `~/.shardlane` into `~/.solco` (leaving `~/.shardlane` as a link so installed hooks keep working). The Hook Doctor (Settings → Agent integrations) replaces the old `shardlane-*-hook.sh` wiring in Claude Code and Antigravity when you press Install. `SHARDLANE_SOCKET_PATH`, `SHARDLANE_LOG_LEVEL` are still read after their `SOLCO_` names. The Herdr hook plugin is now `com.whstudio.solco.hooks` (relink: `herdr plugin link --path plugins/solco-hooks`; remove the old `com.whstudio.shardlane.hooks`). The bundle identifier is now `com.whstudio.solco`, so macOS asks for microphone/accessibility permission again.

## Requirements

- macOS 13+
- Go 1.27.3 — the module pins it; `GOTOOLCHAIN=auto` lets Go fetch that toolchain without changing your global install
- no frontend toolchain is needed: the MyGo framework (v0.6.4 plus the F146/F147 `Options.LocalDragSelect`, WIX-021 field-focus, multi-display tray-anchor and OSC 7501 program-status patches, see `third_party/mygo/LOCAL-PATCH.md`) is vendored in-repo via a `replace` directive until upstream ships all four fixes
- `herdr` — if it is missing, Solco offers `wax install herdr`

## Development

```sh
GOTOOLCHAIN=auto go tool mygo dev
```

## Checks

```sh
GOTOOLCHAIN=auto go test ./...
GOTOOLCHAIN=auto go tool mygo build
git diff --check
```

## Packaging

`go tool mygo build` produces the macOS app and DMG with no frontend build toolchain:

```text
build/darwin-arm64/Solco.app
build/darwin-arm64/Solco <version>.dmg
```

Packaging identity and the chat WebView frontend are configured once in `mygo.config.ts` (app name, bundle identifier, icon, minimum macOS version, and the N19 `frontend/` keys). `go tool mygo build` installs frontend dependencies with Bun and embeds `frontend/dist` behind `mygo://localhost`; `go tool mygo dev` serves the page from the Vite dev server, and `go tool mygo generate` writes the typed bridge client.

## Native design system

Presentation is componentized rather than page-styled ad hoc: `internal/nativeui` provides `theme.go` (macOS/shadcn-like neutral tokens), `icons.go` (reusable outline SVG icon set), and `components.go` (navButton/panelCard/treeRow), `gorex_style.go` (gorexIconButton/gorexToggleFace) plus the titlebar/sidebar/project-tree/router/page files. On macOS the window uses MyGo vibrancy behind the full-height sidebar (traffic lights included) while the title bar and content surfaces stay opaque for terminal and text clarity. Reuse these before adding one-off page styling.

## Diagnostics and bounded logs

Structured JSON logs are written to MyGo `PathLogs` (macOS: `~/Library/Logs/Solco/solco.log`). The active file is capped at 2 MiB with three backups. `SOLCO_LOG_LEVEL=debug` enables diagnostic detail. Terminal output, prompt/conversation bodies, credentials and provider secrets are intentionally not logged.

Unpackaged runs (`go run`, and the temporary binary `mygo build` / `mygo dev` start to write the TypeScript client) take the fixed name `Solco-dev`, so they keep one data directory (`~/Library/Application Support/Solco-dev`) and one log directory instead of a pair per run, and never touch the installed app's. The app `mygo dev` launches is MyGo's separate `Solco Dev` bundle.

## Architecture

Ownership is intentionally narrow (full contract: [`docs/client-product-architecture.md`](docs/client-product-architecture.md)):

1. **Herdr** — runtime/backend authority; `session.snapshot.layouts` is the Pane topology authority.
2. **MyGo Native UI** — presentation framework and the official Terminal/Ghostty surface.
3. **Go application/domain packages** — Solco product semantics: navigation, projection, read-only history, Git workbench, settings, logging.

Terminal attachments are visibility-scoped Herdr `terminal attach <terminal_id>` clients mapped into same-window MyGo Terminal elements. Automatic attach never uses `--takeover`; hidden tabs keep no attachment fleet.

```text
main.go              — MyGo Native UI application/window entrypoint
internal/
├─ nativeui/         — MyGo-native desktop presentation (shell, router, pages, theme/icons/components)
├─ herdr/            — Herdr daemon protocol bindings: discovery, socket RPC, events, direct terminal attach
├─ agent/            — UI-independent Agent product semantics
├─ app/              — application service coordination
├─ settings/         — client preferences
├─ history/          — read-only coding-agent history (catalog, transcripts, live tailing)
├─ gitworkbench/     — local Git review model (status, diff review, commit)
├─ conversation/     — Conversation/Chat domain
├─ filesview/        — standalone Files workbench directory + bounded read-only preview
├─ preview/          — local web preview
├─ services/         — resident service monitoring
├─ commandcenter/    — fast command entry point
├─ scripts/          — user-defined project scripts
├─ codehl/           — syntax coloring for the diff review through MyGo editor tokenizers
├─ diagnostics/      — diagnostics & log export
├─ applog/           — bounded structured logging
└─ platform/         — platform integration
```

## Scope

- native macOS client (MyGo Native UI; WebView presentation is frozen until MyGo ships an officially embeddable WebView capability and a product decision unfreezes it)
- Herdr socket/runtime integration
- native Terminal rendering through MyGo's official Terminal/Ghostty
- workspace/tab/pane navigation, New Task / Search / History / Chat / Settings
- Git Workbench (Diff Review / Commit) scoped to the focused Tab cwd
- read-only coding-agent history browsing
- no plugin marketplace · no cloud account layer · no telemetry

## Contributing

Development setup, the verification gates every change must pass, and review expectations live in [CONTRIBUTING.md](CONTRIBUTING.md). Bug reports and feature requests use the issue templates.

## Security

Report vulnerabilities privately through GitHub's vulnerability reporting — see [SECURITY.md](SECURITY.md). Do not open public issues for anything exploitable.

## License

Solco is free software licensed under the [GNU General Public License v3.0](LICENSE): you may use, study, modify, and redistribute it; derivative works must likewise be licensed under GPLv3. Embedded third-party material keeps its own license — see [`THIRD_PARTY_NOTICES.md`](THIRD_PARTY_NOTICES.md).
