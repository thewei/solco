# Solco — native macOS workspace for coding agents (Go / MyGo Native UI)

Current implementation: Go 1.27.3 + MyGo Native UI (pinned v0.6.4, patched fork vendored under `third_party/mygo`) + official MyGo Terminal/Ghostty; Herdr remains the runtime authority. The previous Rust/GPUI implementation lives only on `rewrite/mygo`/`main`.

<directory>
frontend/ - Chat WebView 页唯一源码（桌面内嵌 dist + 手机单文件快照，见 frontend/CLAUDE.md）
internal/ - Go application/domain packages (chatstream bounded OMP token-event overlay, hostd resident Remote Host lease/loop/launchd, nativeui presentation, apphook in-app IPC engine, agenthooks Hook Doctor provisioning, agentstatus OSC 7501 status arbitration, herdr adapter, agent/history/sessiontrash/gitworkbench domains, remotedesktop (phone Remote Desktop: Service + Pion transport + session rules + Swift-helper supervisor; helper lives in the sibling `solco-remote-helper` repo), settings, secretstore AES-GCM credential file, applog, ...)
cmd/ - solco-hostd (the resident Remote Host: a launchd user service that keeps the phone-facing Host v3 reachable while the app is closed, owning the port only through `internal/hostd`'s lease; embedded at Contents/Helpers by `scripts/embed-hostd.sh`) and development harnesses (rd-devhost: real Remote Desktop stack behind a stub Herdr on its own port/data dir, for phone testing; not shipped)
scripts/ - packaging helpers and read-only OMP Chat stream companion (omp-chat-stream.ts + Bun test; see scripts/CLAUDE.md) (embed-remote-helper.sh: build, embed and sign the Remote Desktop helper into Solco.app; publish-remote-helper.sh + remote-helper.pin: build the universal helper locally, publish it as the `remote-helper-<sha>` prerelease asset that release.yml downloads into resources/darwin → Contents/Resources)
resources/ - MyGo packaging resources (app icon)
third_party/ - vendored patched MyGo framework fork (go.mod replace target; see third_party/mygo/LOCAL-PATCH.md)
site/ - GitHub Pages landing site (static HTML/CSS, no build step); assets/app-icon holds the icon prompt and Apple safe-margin export rule
docs/ - architecture source of truth, MyGo migration contract/roadmap, interaction/performance/audit documentation
designs/ - interactive design explorations (self-contained `exploration.html` + `README.md` Decision record per topic; not built, not shipped; product code waits for the recorded Decision)
.agents/skills/ - project-local engineering workflow/checklists
.github/ - CI workflows, issue/PR templates
docs/ - architecture source of truth, MyGo migration contract/roadmap, interaction/performance/audit documentation, release updater runbook
</directory>

<config>
go.mod + go.sum + mygo.config.ts - Go toolchain (1.27.3), MyGo pin (v0.6.4 + replace ./third_party/mygo), packaging policy, signed updater feed (thewei/solco), and the chat WebView frontend (N19)
AGENTS.md - repository engineering rules and required reading
README.md - developer setup, run, checks, packaging, and product scope
docs/client-product-architecture.md - unique architecture source of truth
docs/mygo-native-execution-rules.md - active migration execution contract
docs/mygo-native-migration-roadmap.md - active migration roadmap and task order
.github/workflows/pages.yml - GitHub Pages deployment for site/
.github/workflows/release.yml - signed tag release / updater publishing for installed apps
docs/github-release-updates.md - release tagging, draft review, publish, and first updater installation rules
</config>

Product rule: **Solco is the client and brand. Herdr is the backend/runtime.** Solco must not introduce a second Workspace registry, pane runtime, terminal authority, or process lifecycle owner. Herdr remains authoritative for workspaces, tabs, panes, layouts, agents, scrollback, persistence, terminal sessions, and process lifecycle. Solco owns native macOS presentation, local interaction state/preferences, and the read-only Agent-history browsing/index layer (plus its user-initiated, reversible Sessions Trash).

Implementation rule: do not hand-roll capabilities while mature owners exist. Search in order: Herdr API → MyGo native components/official plugins → a proven compatible implementation → custom code. Missing runtime APIs are fixed at the Herdr boundary rather than bypassed with a parallel terminal/process path.

CI note: `.github/workflows/checks.yml` runs the Go gates (test + mygo build + bundle lint) on macOS runners; the vendored `third_party/mygo` replace keeps CI self-contained.

Principles: minimal · stable · navigation-first · version-exact · mature-API-first
