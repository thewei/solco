# docs/
> L2 | Parent: ../CLAUDE.md

Members:
- `README.md` — documentation entry point.
- `client-product-architecture.md` — unique architecture source of truth; defines Solco product ownership and Herdr runtime ownership.
- `chat-view-streaming-plan-2026-11-04.md` — OMP 原生流式 Chat View 的 SC-01–SC-06 设计、同会话/单运行时约束、Socket/overlay 契约和验收证据；SC-01–04 已落地，真实同会话验收待做。
- `personal-agent-plan.md` — historical Rust-first source audit (v1.2.27 / Shepherdr 2.1 / hc); architecture implementation superseded; cite as historical evidence only.
- `personal-agent-go-mvp-plan.md` — **historical 2026-11-03 Go-native A–E implementation roadmap**: Shepherdr 2.1 test alignment, Assistant memory/task/coordinator, one Go daemon/Host. Substantial Go/Desktop/Mobile implementation now exists; consult current code and the newer Chat-first plan rather than its old pending-state text.
- `assistants-chat-first-redesign.md` — **2026-11-04 Chat-first product/UI transition plan**: shared ChatView, scoped Herdr binding and More drawers; portions of the desktop implementation now exist, verify current code.
- `assistants-personal-agent-platform-plan.md` — **V3 feature/architecture baseline (2026-11-04)**: Profile/Permissions, Provider/Model/Effort, Memory Folder, Routine/Automation and AC-01–AC-20; its previous plugin-market plan is superseded by MCP-first product decisions below.
- `assistants-messenger-agent-v3-1-plan.md` — **latest 2026-11-04 user-approved product direction and U0–U5 / MU-01–MU-23 plan**: Workspace-style sidebar avatars with round Assistant initials, reliable + → New Assistant/New Group menus and form intent, mobile private/group Chat-first, Personal Assistant = preset Agent with initial instructions and Memory, plugin=MCP Server, Goal→Task→evidence→reply loop, Routine from conversation, Go Workflow framework deferred. No runtime code changed.
- `assistants-conversation-product-spec.md` — **authoritative 2026-11-04 product/UX spec** verified against Grok Bot, Slack Agents, Poe, Teams, OpenClaw: Assistant contacts, primary assistant, 3 conversation kinds (human_dm/peer_dm/group), @ mention routing, mixed sidebar, mobile chat list, UX0–UX6 and CONV-01..30. Planned; not shipped.
- `assistants-collaboration-plan.md` — **Assistant Network Go domain N0–N6**: opt-in Directory/visibility & grants, Herdr Skill + durable Mailbox/ConversationStore/delegations, Worker task graph, single Scheduler, owner Control Center, NW-01..22. UX and chat relationship decisions belong to `assistants-conversation-product-spec.md`; no runtime code changed.
- `shepherdr-2.1-test-parity.md` — **authoritative staged TDD handoff A0–E4**: every upstream 2.1 test accounted for (168 files, 1193 static declarations), port/adapt/reuse/platform triage, Solco-only Memory/safety gaps, red→green and final closure gates.
- `shepherdr-2.1-test-cases.jsonl` — immutable generated source-located index of all 1193 Shepherdr 2.1 test declarations; generator: `scripts/generate-shepherdr-2.1-test-index.mjs`.
- `shepherdr-2.1-parity-decisions.jsonl` — manual source-case ledger (46 priority cases planned, none verified) and `solco-assistants-required-cases.jsonl` — 31 **mandatory Solco-only** Assistant/Memory/Watch/mobile/migration cases (all planned). Checker: `scripts/check-shepherdr-2.1-parity.mjs`, final gate checks both.
- `github-release-updates.md` — signed GitHub Releases updater publishing runbook and first-install migration limit.
- `terminal-interaction-spec.md` — terminal interaction-quality contract and manual acceptance for the hosted Herdr TUI surface.
- `herdr-pane-history-api-validation-2026-11-05.md` — Herdr v0.9.5 / Protocol 22 实机 API 与源码验证：pane.read 硬限制 1000 屏幕行、pane.selection.read 可只读提取全部保留文本（修订号门禁，视口不变），精确区分 ANSI、Alternate Screen 与长期归档的缺口。
- `performance-engineering.md` — measured performance contract, budgets, methodology, and known lessons.
- `macos-packaging-and-development.md` — macOS `.app` packaging/install, Mobile Web composition, and the cross-repo debug loop.
- `remote-client-architecture.md` — Host Remote API and remote/mobile client architecture.
- `multiplexer-api.md` — Approved Next backend-neutral Multiplexer API: domain catalog, capability model, adapter/registry rules, and the TDD migration/verification strategy.
- `ui-acceptance-testing.md` — UI acceptance harness practice: `SOLCO_BIND_INSTANCE` seam, evpost/vocr tools, isolation discipline, ground-truth assertion patterns, and pitfalls.
- `mygo-native-execution-rules.md` — active MyGo migration execution contract: atomic task sizing, verification tiers, package/file boundaries, Native UI-only Chat/History policy, and final-cutover rules.
- `mygo-native-migration-roadmap.md` — active MyGo migration handoff/task ledger with milestone order, task IDs, dependencies, and focused verification.
- `mygo-native-0.3.0-closure-audit.md` — post-0.3 closure audit covering framework currency, UI system drift, History request concurrency, Agent idempotency, operational status and Hook/Integration health.
- `mygo-native-0.4.0-closure-plan.md` — next closure release: MyGo 0.2.7, Design System v2, shared operational state, Provider Integration/Hook Health, and safe Agent launch.
- `mygo-native-0.5.0-agent-workbench-plan.md` — post-0.4 Agent lifecycle/workbench: one card model, unread/review semantics, Agent directory/overview/switcher, notifications, and History Continue before full Chat.
- `mygo-native-0.6.0-conversation-chat-plan.md` — Native Conversation/Chat: session-exact identity, incremental live semantics, safe prompt/queue transactions, interactions, and timeline (its §19 Live Handoff was removed 2026-10-12).
- `mygo-native-0.7.0-status-center-plan.md` — right-side Status Center + MyGo Tray: shared Agent status snapshot, attention/review/working grouping, and exact quick navigation.
- `reference-magpie-agent-history-usage-audit.md` — external reference audit: menu-bar floating panel, Project-grouped Sessions, safe Trash, Usage/Quota attribution, Gateway concepts to adopt/adapt/reject.
- `mygo-native-0.8.0-history-usage-agent-inspector-plan.md` — grouped History management, capability-gated Trash/Restore/Purge, UsageService, quota and Agent Inspector before the larger workspace-intelligence release.
- `reference-magpie-herdr-gpui-solco-0.9-audit.md` — three-way reference audit covering command/workspace UX, Project tools, Git/PR, desktop affordances, diagnostics, updater and explicit reject/defer boundaries.
- `mygo-native-0.9.0-workspace-intelligence-desktop-experience-plan.md` — large post-0.8 release plan for Command Center, Right Panel tools, Services/Ports, Git/PR, desktop attention, Diagnostics/Logs, updater, density/high contrast and Task Presets.
- `reference-godiff-solco-0.10-audit.md` — Godiff reference decomposition plus post-0.9 reality audit; documents which landed features are package-only versus actually wired and the current Godiff import/license constraints.
- `mygo-native-0.10.0-git-workbench-primary-surface-plan.md` — removes Lazygit and defines one `/workspace` Terminal/Diff/Commit primary surface, Godiff-inspired Sidebar/Header/Changes UI, safe native commit/branch operations, and inherited 0.9 fixes.
- `libghostty-upgrade.md` — vendored libghostty-vt upgrade and ABI verification runbook.
- `mygo-native-unified-ui-ux-plan-2026-10-09.md` — screenshot and source-based MyGo UI/UX audit, page map, contextual panel/Agent popover interaction specification, staged implementation and acceptance ledger.
- `mygo-git-workbench-product-ux-2026-10-09.md` — Fork/lazygit feature parity, native Git toolbar and Commit dialog, safe FF merge/revert/undo, AI commit prompt handoff versus future generation API, acceptance stages.
- `mygo-webview-upstream-evaluation-2026-10-10.md` — v0.3.7 WebView gap and three open PR alternatives, non-adoption rationale and safe reconsideration gates.

Rules:
- Solco is the client/product brand; Herdr is the real backend/runtime and retains its technical name wherever that is the fact being described.
- Architecture conflicts are resolved in `client-product-architecture.md` first, then subordinate docs are updated.
- Documents must distinguish Current / Approved Next / Protocol Gap. Never write plans as implemented facts.
- The active MyGo migration execution rules and roadmap are an explicit repository-owned exception because multiple implementers must share one authoritative task contract. Architecture facts still belong only in `client-product-architecture.md`; incidental audits/progress evidence may remain outside the repository.

[PROTOCOL]: Update this header on change, then check CLAUDE.md.
