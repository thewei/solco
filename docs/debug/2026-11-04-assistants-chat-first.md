# 2026-11-04 · Assistants chat-first — desktop e2e evidence

Design: `designs/assistants-chat-first/` (one option, Decision recorded). Scope of this round: the desktop only; the phone joint test waits for it.

## Environment
- Installed `Solco.app` 0.12.2 (`solco-install`, "Solco Local Dev" signature), real Herdr, real `pi` Agent, scratch Assistant `phone-e2e`.
- Plugin daemon kept alive with `launchctl submit` + `PATH` containing `~/.local/bin` (see finding 1).

## Walkthrough (real app, computer-use)
| Step | Result |
|---|---|
| Open the Assistant with no Coordinator | Invitation shell: name, description, composer with Agent chip (MRU first), state pill "Stopped" |
| Pick Pi, type, Send | User bubble held in place, "Starting Pi…", composer disabled, pill "Starting…"; `assistants.ensureChat` filed one coordinator task (idempotent requestId) |
| Coordinator launches | Run `ready`, pane created in the plugin's own Herdr workspace; the Mac's selected Workspace/pane did **not** move |
| Held message | Sent exactly once after the Chat reported `ready`/`queue`; `pi` answered in the page ("你好！我是 phone-e2e 助手的 Coordinator…") |
| Coordinator runs the plugin CLI | `task graph` through the absolute binary path; it reported the earlier task `t_a0b9…` as done — the conversation is the Assistant's real one |
| ⋯ → Pause / Resume | Status `disabled` ⇄ `active`; banner "Paused — new tasks and watches are not started. The conversation keeps working."; chat stayed usable |
| More drawer → Tasks | Draft `e2e-noop` filed through the CLI appeared with **Publish**; the light snapshot polls no lists, the drawer reads them only while open |
| Leave to Workspace and come back | Workspace shows its own pane untouched; the Assistants Chat rebinds to the Coordinator with the transcript intact |
| Restart the app | Conversation intact; the standing brief row is hidden |

## Findings fixed in this round
1. **Coordinator stuck in `starting` forever** when the launch is `uncertain` (daemon without `herdr` on `PATH`). `syncCoordinator` now reads an uncertain launch as `failed`, so the page gives the held text back and the next Send starts a fresh attempt; a run that is re-attached later flips back to `ready`. Test: `TestCoordinatorWithAnUncertainLaunchReadsFailed`.
2. **The Coordinator could not call its own CLI.** The brief said `solco-automations …`, which a pane's shell does not have on `PATH` (the Agent's first command failed). The daemon now sets `personalagent.CLI` to its own absolute executable and the brief names that path. Test: `TestCoordinatorPromptNamesTheConfiguredCLI`.
3. **First message swallowed.** `chat.send` into a pane whose card has no phase yet is refused with "This agent needs the terminal; prompts stay disabled" and returned without error. The page now asks `chat.snapshot` first and sends only when the disposition is `ready` or `queue`, retrying every 0.7 s until the 90 s hold limit gives the text back.
4. **The standing brief showed up as something the person said.** `chatBriefPane` marks a pane the Assistants page bound; user rows opening with the brief prefix are left out of that Chat's turns (wherever they sit — the person's own first message can be echoed ahead of it). Test: `TestAssistantsChatHidesTheCoordinatorBrief`.
5. Status lag after Send: the page calls `pull()` when `ensureChat` settles.

## Gates
`go test ./...` (root + plugin: 430 tests), `go tool mygo vet .`, `bun run typecheck / build / build:mobile`, `git diff --check` — all green.

## Not covered live (covered by unit tests)
Needs-You approve/deny (needs a real approval: a coding run or a PR watch), failed-start give-back (unit: `co.state==="failed"` path and the uncertain case above), Agent fence (`agentAllowed`).

## Left for the phone phase
Host v3 `conversation/ensure` DTO + goldens in both repos, solco-mobile `/assistants` over the shared chat page (`pnpm sync:chat-page`, `pnpm sync:fixtures`), real-phone run.

## 手机端阶段（2026-11-04，未上真机）

- Host v3：`GET …/assistants/{id}` 增加 `coordinator`、`agents`；新增 `POST …/assistants/{id}/coordinator/start`（scope manage，agent 必须在 `Options.AssistantAgents` 提供的列表内，否则 400）。golden：`personal-agent-assistant.json`（改）、`personal-agent-coordinator-start-request.json`、`personal-agent-coordinator-start.json`，两仓一致。
- 手机：`/assistants/[id]` 的 Conversation 卡（idle → 选 Agent「Start conversation」；starting 3 s 轮询；ready → 仅当 Coordinator 的 `herdrSession`+`paneId` 在手机 tree 中才打开其共享 Chat；failed → 重试）。
- 验证：solco `go test ./...`、`go vet`、`git diff --check` 通过；solco-mobile `pnpm typecheck / lint / vitest（552 通过）/ check:fixtures / check:chat-page` 通过。
- 未做（按用户指示「电脑端先跑通，后面再联调手机端」）：vivo 真机 USB 联调。
