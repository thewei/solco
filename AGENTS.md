# AGENTS.md

## Start here

Before engineering Solco, read `.agents/skills/solco-development/SKILL.md` — the routing entry: which architecture section, reference or sibling skill (`solco-commit`, `solco-install`, `solco-release`, UI audit/polish) a task needs, plus the verification gate and the bug/行为异常 evidence loop（环境先行→实机取证→根因→修复→同环境复验，进度文件在 `docs/debug/`；需要实机 computer-use 验证时必读其 Evidence loop 一节与 `references/sandbox.md`）。It names the architecture section, the execution contract (`docs/mygo-native-execution-rules.md`) and the roadmap (`docs/mygo-native-migration-roadmap.md`) a task needs.

`docs/product-decisions.md` — the user-approved decisions behind the rules below, word for word; the index is the "Product decisions" section at the end of this file. Open only the entries for the area you touch.

Architecture ownership lives only in `docs/client-product-architecture.md`; ad-hoc audits/progress evidence that are not part of the active migration contract may remain outside the repository.

## Private assets (solco-docs)

The development record is not in this repository. `docs/`, `designs/`, `.agents/` (skills and their scripts), `TODO.md`, `AUDIT-*.md` and the dev-only scripts listed in `../solco-docs/manifest.tsv` are symlinks into the **private** sibling repository `../solco-docs`; `.gitignore` keeps them out of every commit here, and everything "Start here" names under those paths is read through the link as before. The `solco-docs` skill (`.agents/skills/solco-docs/SKILL.md`) holds the map and the procedures; these rules apply to every session:

- **Read.** A path above that does not resolve means a missing link or checkout: `bash ../solco-docs/link.sh --check`, then `bash ../solco-docs/link.sh`. No `../solco-docs` at all: stop and tell the user; never recreate, stub or reconstruct those files here, never guess their content.
- **Write.** Decisions, architecture and roadmap text, debug progress files (`docs/debug/`), design explorations with their Decision records (`designs/<topic>/`), skills, dev-only scripts, TODO and audit notes are written through the link (the edit lands in solco-docs) and committed there. A decision keeps the user's words in `docs/product-decisions.md`; this file gets its one index line below, never the paragraph. Never create real files at those paths in this repository.
- **Commit.** Code and its rules are one commit here (`commit-paths.sh`), the matching record another in solco-docs (`commit-docs.sh`); never both in one, never `git add -A` in either. Report both hashes. Private edits do not show in this repository's `git status`: also check `git -C ../solco-docs status --short`.
- **Authorship.** Commit as the user's GitHub identity: both scripts set it, a hand-typed `git commit` passes it (name and address: the `solco-docs` skill). Never a placeholder name, never the personal address from the global git config.
- **No leak back.** Nothing private goes into this repository's files, commit messages or CI: decision text beyond an index title, design contents or evidence, device serials, LAN addresses, home paths, user and machine names, personal mail, tokens, key material. Those facts live in solco-docs only; a fixture uses a neutral value (`/Users/me`, user `deploy`, a placeholder subnet such as `192.168.1.x`), and `commit-paths.sh` and the gate refuse added lines that carry the real ones (`public-hygiene.py`). Tests, `go build` and CI pass on a clone without solco-docs; no code, test or workflow reads those paths at run time.
- **Publish.** Push only when asked; a public repository after the outbound scan and author check (`solco-commit`). solco-docs stays private; no force-push and no tag there.

## Product/runtime boundary

- External Agent conversation history is a separate read-only catalog; it must not become a second runtime. Runtime continuation from history must go through Herdr. The one mutation is the user-initiated, reversible Sessions Trash (2026-10-14), owned only by `internal/sessiontrash`.
- Do not invent Herdr protocol methods or socket shapes. Inspect the live/actual API.

## Build and checks

```sh
GOTOOLCHAIN=auto go tool mygo dev         # run the app
GOTOOLCHAIN=auto go test ./...
GOTOOLCHAIN=auto go tool mygo build   # builds build/darwin-arm64/Solco.app
git diff --check
git diff --cached --check
```

- `bash .agents/skills/solco-development/scripts/gate.sh` runs exactly the checks your changed paths call for (Go vet/test, the nested plugin module, frontend typecheck, phone-side drift checks, shell tests) as parallel lanes and prints one table; `--list` previews it, `--build` adds `mygo build` plus the CI bundle checks. `scripts/sandbox.sh up <slug>` brings up an isolated second Solco for real-app verification (see `references/sandbox.md`).
- `go.mod` pins Go 1.27.3 and MyGo v0.8.1 (checked elements: constructors return `ui.Element` values, persistent identity lives in `ui.Handle`); use `GOTOOLCHAIN=auto` so the repository pin does not require changing the user's global Go installation.
- `go.mod` carries a temporary, user-approved `replace github.com/egoist/mygo => ./third_party/mygo`: do not remove it or add a second framework source. Before touching `go.mod` or `third_party/`, read `third_party/mygo/LOCAL-PATCH.md` (the five vendored deltas, and when the replace may go).
- Upstream v0.5.x additions stay unadopted until a task takes them: the Flyout API backs the quick panel (tray + header Agent popover), while `ui.WebView` (official Native-UI-embeddable WebView) does not by itself unfreeze the WebView presentation freeze — that needs an explicit product decision.- `GOTOOLCHAIN=auto go tool mygo vet .` (ordinary vet + type-aware element/Context checks) belongs in the check set beside `go test`.

## Scope and ownership

- `internal/history` is the read-only history catalog/transcript/live-tailing domain; `internal/sessiontrash` is the only package that moves Provider session files (the Sessions Trash: provider root ⇄ `<userDataDir>/SessionTrash/`, never overwriting, no conversation content in its manifest); `internal/agent` owns Agent product semantics; the `gitworkbench` package of the sibling `solco-gitkit` repository (shared with Solco Git, required at a tagged version; an uncommitted `go.work` links the local checkout, `scripts/sandbox.sh` mirrors it; 2026-11-07 decision) owns the local Git review model and its `filesview` the Files data layer; `internal/applog` owns bounded structured logging.
- Herdr `session.snapshot.layouts` remains the Pane topology authority; native UI only maps visible Pane rectangles to official MyGo Terminal elements running supported `herdr terminal attach <terminal_id>`. No client-owned PTY/layout/runtime may appear.
- Automatic attach must never use `--takeover`; hidden Tabs keep no attachment fleet.
- Shell shape (Lazygit retired; `/workspace` has one primary surface; no right sidebar except the Changes / Files list panel; heavier Git and Files work lives in the Solco Git app): read `docs/client-product-architecture.md` §6 before changing the sidebar, the panel or the Detail card.
- Before changing how a window closes or is titled, read "Window contracts" in `.agents/skills/solco-development/references/nativeui.md`.
- Logs are structured and size-bounded under MyGo `PathLogs` (2 MiB active + 3 backups by default). Never log terminal bytes/output, prompt or conversation bodies, credentials, auth headers, provider secrets, or environment dumps; log operation names, stable runtime IDs, errors, state counts, and timing.
- Still forbidden: a second Solco runtime implementation, a mode switch (no named exception remains for the retired work board), Notes/Bookmarks/Annotation, plugin UI/marketplace (the `com.whstudio.solco.automations` Automations view, which also hosts the Assistants core, is the one named exception), cloud accounts, telemetry.

## Product decisions

The user-approved decisions behind the rules above are kept word for word in `docs/product-decisions.md`, in the order they stood in this file; the index below names each one. Before changing an area, read its entry there (`rg -n '<keyword>' docs/product-decisions.md`): an entry binds until a later one says it amends or retires it, and older references to "AGENTS.md Product decision <date>" now resolve there. Record a new decision by appending a `## <date> · <title>` section to that file and one matching index line here — never a new paragraph in this file (`.agents/skills/solco-development/scripts/skills-lint.py` fails when this file outgrows its size cap or the index and the headings drift apart).

- 2026-10-11 · Chat presentation on the official MyGo WebView
- 2026-10-13 · Files preview: native editor, WebView for Markdown and images
- 2026-10-26 · Work board plugin gate (herdr-projects)
- 2026-10-26 · Work Board view switch (Native | Board)
- 2026-10-29 · shadcn alignment: title bar, tooltips, controls
- 2026-10-30 · Device and session switcher
- 2026-10-30 · Quota page
- 2026-10-31 · Quota credential copies
- 2026-11-01 · Solco's own credentials leave the Keychain
- 2026-11-01 · Quota on the phone
- 2026-11-01 · Phone connection policy
- 2026-11-01 · Phone Home composer
- 2026-11-02 · Phone Git writes
- 2026-11-02 · Phone devices
- 2026-11-02 · One "+" menu for every new Tab
- 2026-11-02 · New pane surface flash fix: launch intent decides the surface
- 2026-11-03 · Launch-held Chat
- 2026-11-02 · Cursor and Codex status hooks
- 2026-10-30 · Agent surfaces: status slot, Header popover, sub-agent nesting, Tab bar
- 2026-10-30 · UX audit
- 2026-10-30 · UX audit round 2
- 2026-10-30 · UX audit round 3
- 2026-10-30 · Chat view: the A+B direction
- 2026-10-30 · Chat states: dock stack above the composer
- 2026-10-31 · Chat composer
- 2026-10-31 · Remote Host for the phone client
- 2026-10-31 · Phone Chat is the desktop Chat page
- 2026-10-31 · Sub-agent views
- 2026-10-31 · Projects WebView
- 2026-11-01 · Queued message stack
- 2026-11-01 · Cursor Agent and Antigravity CLI in Chat
- 2026-11-01 · Thinking effort in the Chat model panel
- 2026-11-01 · Naming unification
- 2026-11-01 · UX audit round 4
- 2026-11-01 · About page and release changelog
- 2026-11-02 · Sessions Trash on the phone
- 2026-11-02 · Automations view
- 2026-11-03 · Automations and folder trust
- 2026-11-03 · Agent launch toggles and first-run guide
- 2026-11-03 · Remote Desktop
- 2026-11-03 · Remote Host follows IP changes
- 2026-11-03 · Git panel aligned to Fork.app
- 2026-11-03 · Assistants (B3 desktop surface)
- 2026-11-03 · Resident Remote Host (E05)
- 2026-11-03 · Assistants replace the work board (E06)
- 2026-11-04 · Assistants chat-first
- 2026-11-04 · Assistants 3.1 U2: work style
- 2026-11-04 · Assistants 3.1 U3: Goal, Plan, Supervisor, verification
- 2026-11-04 · Assistants 3.1 U4: Routines from chat
- 2026-11-04 · Assistants memory workspace and proactivity (U5)
- 2026-11-04 · Long-run crash: JavaScriptCore heap collector
- 2026-11-06 · Group archive and delete
- 2026-11-07 · Primary Assistant and two-file memory
- 2026-11-07 · Assistants: a usable helper rather than a complete one
- 2026-11-07 · Phone routes: the fastest answer serves
- 2026-11-07 · Phone redesign after T3 Code
- 2026-11-07 · Assistants: invisible lifecycle, per-Assistant sleep, result cards
- 2026-11-07 · Assistants: the PR watcher is retired
- 2026-11-07 · Phone terminal takeover
- 2026-11-07 · Phone Files search
- 2026-11-07 · Agent Thread and Provider Switching
- 2026-11-07 · Desktop and phone design language (T3 Code desktop review)
- 2026-11-07 · Desktop sidebar navigation, pane header and Agent glyph (T3 Code review, round 2)
- 2026-11-07 · Header count, menu-bar icon and Assistants glyph (T3 Code review, round 3)
- 2026-11-07 · Solco Git and the right panel (T3 Code review, rounds 4 and 5)
- 2026-11-08 · G5: Solco's Git and Files pages retire
- 2026-11-08 · T3 unification: the design language lands
- 2026-11-08 · The panel is a list; Solco Git workspaces (T3 Code review, round 6)
- 2026-11-08 · Solco Git sidebar: history, the workspace chip in the foot, Control-Tab on menu items (T3 Code review, round 7)
- 2026-11-08 · Terminal pane cards: soft-shadow hierarchy, dark terminal on the surface role
- 2026-11-08 · Terminal cards and Pane debug identifiers
- 2026-11-08 · Solco Git sidebar tabs: a glyph for each view, the name for the chosen one, a wash on the material (T3 Code review, round 8)
- 2026-11-08 · Files preview: native first, a page of a few hundred bytes for AVIF and ICO (T3 Code review, round 9)
- 2026-11-08 · Files preview, round 9b: 2 MiB Markdown, a drawn SVG, find, Quick Look, HTML / math / Mermaid (T3 Code review, round 9b)
- 2026-11-09 · Solco Feedback: the feedback MCP joins Solco
- 2026-11-09 · Feedback phone notification carries the words
- 2026-11-09 · Mobile ChatView windowing, theme sync and zero-CLS skeleton
- 2026-11-09 · Notification channels: a webhook when the phone cannot be told
- 2026-11-09 · Sidebar bell: one waiting number, one panel
- 2026-11-09 · Solco Vault on the phone: approve, browse, show a value (Host v3 wire)
- 2026-11-07 · Phone Assistant window and its follow-ups (A–F)
- 2026-11-12 · The notch island: Agent and Feedback status at the notch (optional, off by default)
- 2026-11-12 · Solco Git on LazyGit's command layer, TanGit's graph, a three-row sidebar (TanGit review)
- 2026-11-13 · Remote Desktop window mode
- 2026-11-13 · Remote Desktop window mode: the app's own text zoom (amends the two fit tiers)
- 2026-11-13 · Remote Desktop window mode: the lost-link grace is 20 seconds (amends the two-minute grace)
- 2026-11-13 · Solco Git edits in place: pencil toggle, ⌘P, LSP, a terminal drawer (editor / LSP / terminal exploration)
- 2026-11-13 · Remote Desktop window mode: the app's text zoom is retired (A−/A+ removed; retires the text zoom entry)
- 2026-11-13 · Remote Desktop: touch mode works like a phone (swipe scrolls, long press selects)
- 2026-11-14 · Solco Git Changes and Commit: LazyGit's small gaps (ignore, compare two commits, move onto, no paused edit, no mergetool)
- 2026-11-14 · Solco Git Changes and Commit, second batch: fixups, discard all, stash modes, co-authors, hide untracked
- 2026-11-14 · Solco Git UX pass: one split handle, remembered pane sizes, token cleanup
- 2026-11-14 · Solco Git: zero statistics stay hidden, one count badge

## Git safety

- Preserve staged/user changes.
- Do not `git add`, reset, commit, push, or otherwise rewrite user Git state unless explicitly asked.
- Several agents may share this working tree: when you are asked to commit, stage and commit only your own files with `git commit -m … -- <paths>` (never `git commit -a`/`git add -A`), so a parallel session's unfinished edits do not ride into your commit (2026-11-02: a `-a` commit swept a half-finished change into history and left one commit that did not build). `git commit -- <paths>` skips untracked files, so use `.agents/skills/solco-commit/scripts/commit-paths.sh -m "…" -- <paths>` (it adds new files intent-to-add, refuses no path or `.`, and reports what stays dirty); the workflow is in `.agents/skills/solco-commit/SKILL.md`.
