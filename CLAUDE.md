@AGENTS.md

# Solco — native macOS workspace for coding agents (Go / MyGo Native UI)

Current implementation: Go 1.27.3 + MyGo Native UI (pinned v0.8.1, patched fork vendored under `third_party/mygo`) + official MyGo Terminal/Ghostty; Herdr remains the runtime authority. The previous Rust/GPUI implementation lives only on `rewrite/mygo`/`main`.

<directory>
frontend/ - Chat WebView 页唯一源码（桌面内嵌 dist + 手机单文件快照，见 frontend/CLAUDE.md）
internal/ - Go application/domain packages; each package has its own `CLAUDE.md` (member list and the invariants it keeps); the `internal/` index with the per-package notes is `internal/CLAUDE.md`
cmd/ - solco-hostd (the resident Remote Host: a launchd user service that keeps the phone-facing Host v3 reachable while the app is closed, owning the port only through `internal/hostd`'s lease; embedded at Contents/Helpers by `scripts/embed-hostd.sh`; it also keeps the Feedback broker alive and hosts `feedback-mcp`, the stdio MCP server external Agents launch) and development harnesses (rd-devhost: real Remote Desktop stack behind a stub Herdr on its own port/data dir, for phone testing; not shipped)
scripts/ - packaging helpers and read-only OMP Chat stream companion (omp-chat-stream.ts + Bun test; see scripts/CLAUDE.md) (embed-remote-helper.sh: build, embed and sign the Remote Desktop helper into Solco.app; publish-remote-helper.sh + remote-helper.pin: build the universal helper locally, publish it as the `remote-helper-<sha>` prerelease asset that release.yml downloads into resources/darwin → Contents/Resources)
resources/ - MyGo packaging resources (app icon)
third_party/ - vendored patched MyGo framework fork (go.mod replace target; see third_party/mygo/LOCAL-PATCH.md)
site/ - GitHub Pages landing site (static HTML/CSS, no build step); assets/app-icon holds the icon prompt and Apple safe-margin export rule
docs/ - [private: link into ../solco-docs] architecture source of truth, MyGo migration contract/roadmap, interaction/performance/audit documentation, release updater runbook
design/ - the design language's one source: `tokens.json` (colour roles, pointer/touch tiers) + `icons.json` (Tabler registry) + `gen.mjs`, which writes the Go, Chat-page and phone files (`--check` = drift gate; see design/CLAUDE.md)
designs/ - [private: link into ../solco-docs] interactive design explorations (self-contained `exploration.html` + `README.md` Decision record per topic; not built, not shipped; product code waits for the recorded Decision)
.agents/skills/ - [private: link into ../solco-docs] project-local engineering workflow/checklists; the `solco-docs` skill holds the rules for every private link
TODO.md, AUDIT-*.md, dev-only scripts under scripts/ and plugins/solco-automations/scripts/e2e-*.sh - [private: links into ../solco-docs] maintainer notes and dev-only checks (the full list is ../solco-docs/manifest.tsv)
.github/ - CI workflows, issue/PR templates
</directory>

<config>
go.mod + go.sum + mygo.config.ts + go.work - Go toolchain, MyGo pin and its `replace`, the sibling `solco-gitkit` module, packaging policy, signed updater feed, the chat WebView frontend (the `replace` and the five vendored deltas: `third_party/mygo/LOCAL-PATCH.md`)
AGENTS.md - repository engineering rules and required reading; its Product decisions section indexes the decision record
docs/product-decisions.md - the user-approved product decisions, word for word (one `## <date> · <title>` section each; append here, index in AGENTS.md)
README.md - developer setup, run, checks, packaging, and product scope
docs/client-product-architecture.md - unique architecture source of truth
docs/mygo-native-execution-rules.md - active migration execution contract
docs/mygo-native-migration-roadmap.md - active migration roadmap and task order
.github/workflows/pages.yml - GitHub Pages deployment for site/
.github/workflows/release.yml - signed tag release / updater publishing for installed apps
docs/github-release-updates.md - release tagging, draft review, publish, and first updater installation rules
../solco-docs - the private sibling repository behind every [private] path: its link.sh links the files in from manifest.tsv; rules for reading, writing, committing and never leaking them: AGENTS.md "Private assets (solco-docs)"
</config>

Product rule: **Solco is the client and brand. Herdr is the backend/runtime.** Solco must not introduce a second Workspace registry, pane runtime, terminal authority, or process lifecycle owner. Herdr remains authoritative for workspaces, tabs, panes, layouts, agents, scrollback, persistence, terminal sessions, and process lifecycle. Solco owns native macOS presentation, local interaction state/preferences, and the read-only Agent-history browsing/index layer (plus its user-initiated, reversible Sessions Trash).

Implementation rule: do not hand-roll capabilities while mature owners exist. Search in order: Herdr API → MyGo native components/official plugins → a proven compatible implementation → custom code. Missing runtime APIs are fixed at the Herdr boundary rather than bypassed with a parallel terminal/process path.

Private-assets rule: **code, tests and the rules an agent obeys live here; the development record (decisions, architecture, roadmap, designs, evidence, skills, device facts, keys) lives in the private `solco-docs`, linked in at the same paths.** Read through the link; write and commit there (`commit-docs.sh`), never copy it into this repository or its commit messages; every commit is authored as the user's GitHub identity. Details: `AGENTS.md` "Private assets (solco-docs)".


Principles: minimal · stable · navigation-first · version-exact · mature-API-first
