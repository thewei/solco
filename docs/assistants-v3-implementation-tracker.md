# Assistants 3.1 — 实施台账（Implementation Tracker）

> 主规划：`docs/assistants-messenger-agent-v3-1-plan.md`（MU-01～MU-23）；手机配套：`solco-mobile/docs/assistants-messenger-agent-mobile-plan.md`。
> 规则：状态只走 `TODO → IN_PROGRESS → IMPLEMENTED → VERIFIED`，受阻写 `BLOCKED`；**VERIFIED 必须有本表“证据”列里的命令/实机记录**；没做过的验证写 `NOT VERIFIED`。进度百分比 = VERIFIED 条数 / 总条数，不做主观估算。
> 契约先后：Go（插件 SQLite 核心 → `internal/personalagent` 客户端 → Host v3 golden）→ 桌面 bridge/UI → 手机 Zod/UI。桌面先跑通，手机真机联调在桌面验收之后。

## 0. 审计结论（2026-11-04，对照真实代码，非规划假设）

| 规划里的“待开发” | 当前真实状态 |
| --- | --- |
| Assistant 侧栏头像 | `assistants_section.go` 固定 `Mark: iconAssistant`，无头像。`treeRowSpec.Tile`（方形字母 tile）只给 Workspace 行用。 |
| `+` 两项菜单 | 头部 `+` 是 `Run: openNewAssistant` 单击；`treeRowAction.Menu` 能力已存在（Tab 的 `+` 用它），只需切换。 |
| New Assistant 偶发打不开 | `newForm bool` 在 `Snapshot` 读取时清除；页面 `useEffect([view?.newForm])` 仅在 false→true 触发。**确认的丢事件路径**：(a) 被 4 s poll 的 Snapshot 读走后，响应若在页面未挂载/正在重载时到达即丢；(b) 两个并发 Snapshot，旧响应后到把 true 覆盖成 false 之前的 true 已被另一次消费；(c) Snapshot 失败（CLI 超时）时 flag 已被清掉；(d) 连续点 `+` bool 不变化。 |
| 单聊 | 桌面 `/assistants` 已是 chat-first（`e357a7b`）；手机 `[id].tsx` 仍有 Conversation 卡 + 管理长页。 |
| Group | **完全不存在**：Go 核心无 group/conversation/message 表，无 CLI，无 Host 路由。 |
| Persona | `role_prompt` 单字段，无版本；`CoordinatorPrompt` 固定“you do not do the tasks yourself”。 |
| Provider/Model/Effort per Assistant | 仅 `coordinator start` 带 agent/model，不落在 Assistant 上。 |
| MCP | 仅旧 automations `mcp_config` 字段；Assistant 无 MCP 登记/授权/绑定校验。 |
| GoalPlan / 持续推进 | Goal/Commitment/TaskGraph/Run/Heartbeat/Inbox 存在；无 GoalPlan、里程碑、nextAction、nextCheckAt、ProgressLedger、GoalSupervisor。 |
| Routine | legacy_rules + Heartbeat + PR Watch 存在；无“聊天提 Routine Draft”入口。 |
| Memory | memory_items（propose/approve/correct/forget/share）存在；无独立 Markdown 目录、版本冲突、主动等级/QuietHours 以外的预算。Heartbeat 已有 quiet hours 字段（待核对）。 |

## 1. 任务表

| ID | 批次 | 任务 | 依赖 | 状态 | 证据 / 备注 |
| --- | --- | --- | --- | --- | --- |
| U0-1 | U0 | 通用 `nameAvatar`（方/圆/叠）+ 稳定 ID 着色 | — | VERIFIED | `assistants_avatar.go`；`TestAvatarInitialAndToneAreStable`（含 CJK/emoji/空名、改名不换色）；实机侧栏圆形头像 |
| U0-2 | U0 | 侧栏 Assistant 行圆形首字母头像，与 Workspace 行同布局 | U0-1 | VERIFIED | `treeRowSpec.Avatar`；实机截图（phone-e2e/Ada/B/C 圆形头像，深色主题）。亮色/超长名 NOT VERIFIED |
| U0-3 | U0 | 头部 `+` = 真菜单，仅 New Assistant / New Group | — | VERIFIED | 实机 AX 树：AXMenu 恰有两项；Workspace 路由与 /assistants 路由均可打开 |
| U0-4 | U0 | create intent（单调 id + ACK + 事件推送）取代 `newForm bool` | — | VERIFIED | `assistants_intent_test.go`（多次 Snapshot 不丢、单调、TTL、门关闭拒绝、插件不可读仍可取）；实机：插件二进制被移走时仍能打开 New Assistant 表单 |
| U0-5 | U0 | NewAssistant：稳定 requestId、失败保留输入、成功进入新 Assistant 聊天 | U0-4 | VERIFIED | 实机：插件缺失→点 Create→表单内报错、名称保留；恢复后再点→恰好新增 1 个 C（`assistant list` 核对）并直接进入 C 的聊天 |
| U1-1 | U1 | Go：groups/group_members/group_messages/group_deliveries 迁移 + Store | — | VERIFIED | 迁移 12–14（groups/members/messages/deliveries/group_agents、launch_seq）；`group_test.go` 全绿；插件 443 tests PASS |
| U1-2 | U1 | Go：CLI `assistant group …` + Client + golden | U1-1 | VERIFIED | `assistant group create/list/show/rename/add/remove/post/say`；golden `group-*.json` 双端钉住（`internal/personalagent` TestGoldenFixturesDecodeStrictly PASS） |
| U1-3 | U1 | Go：@ 精确投递（仅 Owner 的 @ 触发；Agent 发言不再触发）+ 预算/去重 | U1-1 | VERIFIED | 仅用户精确 @ 建 delivery；无 @ 不起 Agent；`say` 要求真实在线 Agent（防冒充）；单测 + 实机“无 @ → 无 Agent” |
| U1-4 | U1 | Go：daemon 投递泵 → 目标 Coordinator（`herdr agent prompt`），Agent 经 CLI 回群 | U1-3 | VERIFIED | 投递泵 claim-before-send；启动即“Agent 已启动≠已就绪”：对话框(`host.StartupDialog`)期间不输入、`agent_blocked` 事实；实机 Duo：B 起 Claude→MCP 对话框→不输入→人按 Esc→送达→B 经 `group say` 回复（seq 19） |
| U1-5 | U1 | 桌面：New Group、群聊页（成员、作者、@、Reply、任务状态卡）、侧栏叠头像 | U0, U1-2 | VERIFIED | 桌面群页（成员/作者/@ 自动补全/Reply/任务卡/事实行/Open Terminal）；`TestAssistantsGroupRowOpensTheGroupConversation`；实机截图：用户气泡 + “B is starting…” + “B is waiting for you in its terminal: …” + B 回复气泡（带 Reply 引用）。亮色主题/任务卡实机 NOT VERIFIED（单测覆盖） |
| U1-6 | U1 | Host v3：groups 读/建/发/消息 + golden；手机 Zod | U1-2 | VERIFIED | `personal_agent_group.go`，capability `personalAgentGroups`，goldens 双端钉住；`go test ./internal/remotehost` PASS；手机 Zod 契约 `pnpm test` PASS（`pnpm check:fixtures` 171 文件一致） |
| U1-7 | U1 | 手机：Chats 列表、直达单聊、群聊 | U1-6 | IMPLEMENTED | Chats 列表、点 Assistant 直达共享 ChatView、真群聊页（solco-mobile `4076612`）；`pnpm typecheck/lint/test/export:web` PASS。真机安装 BLOCKED（手机锁屏 `INSTALL_FAILED_ABORTED: User rejected permissions`，未绕过），故不标 VERIFIED |
| U2-1 | U2 | Assistant 初始提示词版本化（instructions revisions）+ 默认模板 | — | VERIFIED | 迁移 15 `assistant_instructions`+`persona_applied`；Instructions 单一真相仍在 `assistants.role_prompt`，历史/恢复生成新 revision，stale 拒绝；默认模板、旧 Assistant 懒物化、启动记录所用 revision。`persona_test.go` 前 4 例 PASS；Host `instructions*` 路由+golden；桌面 Work style 与手机 `assistants/style/[id]`（视觉 NOT VERIFIED，类型/契约测试 PASS） |
| U2-2 | U2 | Assistant 级 Provider/Model/Effort，真实运行 Agent 展示 | — | VERIFIED | `assistant_runtime`(agent/model/effort)；未显式选择的 Coordinator/Group/Task 回落到档案，显式选择优先；effort 仅 claude 传 `--effort`；`runtime` 同时列出 claimed/starting/running/blocked 的真实 Agent。实机（隔离 state、真实 claude）：Coordinator 进程 `claude --effort low …`，pane 显示 `low · /effort` |
| U2-3 | U2 | MCP：Provider 配置发现（只读 Registry）+ per-Assistant grants + 绑定/生效校验 | U2-2 | VERIFIED | Registry 读 `~/.claude.json`/`~/.mcp.json`/`~/.codex/config.toml`（仅名称/传输/程序，不含 args/env/header）；grants per server；Claude 绑定 0600 的 per-Assistant `--strict-mcp-config --mcp-config`；configured/granted/bound/verified 四态分开。实机：12 个已配置、仅授权 `sequential-thinking`，运行中 claude 的 `/mcp` 恰列 1 个 server ✔ connected；probe 报 `sequential-thinking` connected、`figma-desktop` failed。Codex/其它 Agent 明确 `mcp_unsupported` |
| U3-1 | U3 | GoalPlan（成功条件/里程碑/nextAction/nextCheckAt/planRevision）+ ProgressLedger | — | TODO | |
| U3-2 | U3 | 系统事实卡（Accepted/Planned/Working/Blocked/Verifying/Done）≠ Agent 发言 | U1-1, U3-1 | TODO | |
| U3-3 | U3 | GoalSupervisor（确定性推进 + 预算/最大轮数/重规划上限） | U3-1 | TODO | |
| U3-4 | U3 | 核验后汇报 + 重启恢复 | U3-3 | TODO | |
| U4-1 | U4 | Chat 生成 Routine Draft → 批准 → Go Automations 调度；无双触发 | U3 | TODO | |
| U5-1 | U5 | Memory 目录/Markdown、版本冲突、候选/整理 | — | TODO | |
| U5-2 | U5 | 主动等级 / Quiet Hours / 报告频率 / 预算 | U3-3 | TODO | |
| TODO-WF | — | Go Workflow 选型（仅登记：待三个真实复杂场景证明 TaskGraph 不够） | — | DEFERRED | 不引依赖 |

## 2. MU-01～MU-23 验收

| 编号 | 状态 | 证据 |
| --- | --- | --- |
| MU-01 … MU-23 | NOT VERIFIED | 逐条在对应批次验证后回填 |

## 3. 验证记录

（每批追加：命令、PASS/FAIL、实机证据路径。）

### U0（2026-11-04）
- `GOTOOLCHAIN=auto go test ./internal/nativeui/` PASS（140 s）；`go tool mygo vet .` PASS；`frontend && bun run build` PASS；`git diff --check` PASS。
- 实机（已安装 Solco.app，computer-use）：`+` 真菜单；插件失败时表单仍可打开；创建失败表单内显示 `create: …` 且输入保留；恢复后同一 requestId 重试恰好一个 Assistant；成功进入新 Assistant 聊天。
- 根因记录：旧 `newForm bool` 随 Snapshot 读取清除，Snapshot 失败/并发/页面重载时事件丢失；现在意图独立于插件读取，`PendingIntent`+事件+ACK。
- 教训：打开 NSMenu 期间不要重装（主线程被菜单循环占住）；computer-use 点击 Create 需先 hover；窗口被遮挡时页面 4 s 轮询暂停（document.hidden），属预期。
- 进度：VERIFIED 5 / 总 22（不含 DEFERRED）。

### U2（2026-11-04）
- 插件：`cd plugins/solco-automations && GOTOOLCHAIN=auto go test ./...` PASS（含 `persona_test.go` 10 例与 persona goldens）。
- 根/Host：`GOTOOLCHAIN=auto go test ./internal/personalagent ./internal/remotehost ./internal/hostd` PASS；`go test ./internal/nativeui -run "Assistants|Coordinator|Persona"` PASS；`frontend: bun run typecheck && bun run build && bun run build:mobile` PASS。桌面提交 `1d8d738`。
- 手机：`pnpm typecheck`、`pnpm lint`、`pnpm test`（570 PASS）、`pnpm check:fixtures`（171 一致）、`pnpm export:web` 均 PASS；提交 `c7bbe96`。
- 实机（`HERDR_PLUGIN_STATE_DIR` 隔离目录，重建插件二进制并以 `start_new_session` 重启 daemon，真实 HOME 的 MCP 配置与真实 claude）：创建 Assistant 得默认指令 rev 1；授权 1 个 MCP + effort=low；`coordinator start` → 进程参数含 `--effort low --strict-mcp-config --mcp-config <0600 文件>`；pane `/mcp` 只列 `sequential-thinking`（✔）。
- 发现（非 U2 引入，待 U3 前处理）：Coordinator 的工作目录若不在已信任文件夹之下，Claude 启动时会停在“信任此文件夹”对话框（本次隔离目录触发；真实 state 目录此前已信任，未触发）。`blocked` 状态已被投递泵识别，但没有自动放行（符合“不替用户点信任”的边界）。
- NOT VERIFIED：Work style 桌面/手机 UI 的实机视觉；Host `mcp/verify` 经 relay 最长约 45 s 的超时体验；真机手机联测。
- 进度：VERIFIED 14 / 总 22（不含 DEFERRED）= 64%。

