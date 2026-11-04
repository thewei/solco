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
| U3-1 | U3 | GoalPlan（成功条件/里程碑/nextAction/nextCheckAt/planRevision）+ ProgressLedger | — | VERIFIED | 迁移 16 `goal_plans`/`goal_plan_revisions`/`goal_facts`；`goal_plan_test.go`（版本冲突、重规划上限 5、任务进行中的里程碑不可改）；CLI `goal plan/set-plan/facts`；Host `GET …/goals/{id}/plan`、`…/assistants/{id}/facts`（`personalAgentPlans`，golden 两端）；§3 U3 |
| U3-2 | U3 | 系统事实卡（Accepted/Planned/Working/Blocked/Verifying/Done）≠ Agent 发言 | U1-1, U3-1 | VERIFIED | 事实只由核心写、去重键唯一；桌面抽屉/Header 与手机 `FactsStrip` 标注“Solco record”；`progress-model.test.ts`、合同测试；视觉 NOT VERIFIED |
| U3-3 | U3 | GoalSupervisor（确定性推进 + 预算/最大轮数/重规划上限） | U3-1 | VERIFIED | `Supervisor.Tick` 接入 daemon；9 个单测（委派→核验→才完成、失败不编造进度、任务预算 held 一次、扇出上限、人工里程碑、陈旧退避、暂停/取消不动、coding 等发布者）；实机隔离 daemon：批准计划后 12 s 内出现 1 个草稿任务 + `delegated` 事实 |
| U3-4 | U3 | 核验后汇报 + 重启恢复 | U3-3 | VERIFIED | Goal 仅在全部里程碑 verified 后才 `CompleteGoal`（证据 `sup-done-<goal>-<rev>`）；汇报为确定性的系统事实卡（不经 Agent 转述）；实机重启 daemon 15 s 后仍 1 个任务、事实不重复 |
| U4-1 | U4 | Chat 生成 Routine Draft → 批准 → Go Automations 调度；无双触发 | U3 | VERIFIED | 迁移 17 `routines`（owner/requestedBy/source/correlationId/def_hash/approved）；`routine draft|list|show|cancel|pause|resume`；批准卡 `subjectKind=routine` 绑定定义哈希，定义被改→`stale` 不启用；`RoutineKeeper` 启用；`RuleScheduler.fire` 任务归 owner Assistant；`routine_done/failed` 事实；`routine_test.go` 5 例 + goldens；实机见 §3 U4 |
| U5-1 | U5 | Memory 目录/Markdown、版本冲突、候选/整理 | — | VERIFIED | 迁移 18 `memory_versions`/`memory_files`；`MemoryFiles.Sync` 投影 `facts|inbox|notes|INDEX|README`（0700/0600/原子）；人改文件→新 revision，陈旧→`conflict` 候选并存、凭据/空/超长拒绝并还原、notes 投放→候选、符号链接不读、删文件不遗忘；`add|history|review|sync|folder` CLI + Host 路由（`personalAgentMemory`，golden 双端）+ 桌面 Memory 标签 + 手机 `assistants/memory/[id]`；`memory_files_test.go` + 实机见 §3 U5。手机/桌面视觉 NOT VERIFIED；每周模型整理未做 |
| U5-2 | U5 | 主动等级 / Quiet Hours / 报告频率 / 预算 | U3-3 | VERIFIED | `proactivity` 表；`respond`/`suggest`/`follow_up`；Quiet Hours（可跨午夜、IANA 时区）延后派发/提醒/心跳唤醒且自行恢复；日预算 1..100 超额写一次 Held 事实；报告间隔限频；`proactivity_test.go` 8 例；CLI `proactivity show|set`、Host `personalAgentProactivity`、桌面 Behavior 标签、手机 `assistants/behavior/[id]`；实机见 §3 U5。“批准后自主”等级未做（刻意） |
| TODO-WF | — | Go Workflow 选型（仅登记：待三个真实复杂场景证明 TaskGraph 不够） | — | DEFERRED | 不引依赖 |

## 2. MU-01～MU-23 验收

| 编号 | 状态 | 证据 |
| --- | --- | --- |
| MU-01 | PARTIAL | 圆形首字头像（深色主题）实机截图 + Workspace 方形 Tile 未动（U0-1/2）；Group 叠头像有单测/实机截图（U1-5）；亮色主题、超长名、状态位逐项对比 NOT VERIFIED |
| MU-02 | PARTIAL | `TestAvatarInitialAndToneAreStable`：改名不换色、CJK/emoji/空值回退；用户头像图片未实现，NOT VERIFIED |
| MU-03 | PARTIAL | 实机 AX 树：`+` 菜单恰两项，Workspace 与 /assistants 路由均可开（U0-3）；键盘开合/切换、与 row selection 互不影响 NOT VERIFIED |
| MU-04 | VERIFIED | `assistants_intent_test.go`：多次 Snapshot 不丢、单调、TTL、门关闭、插件不可读仍可取；实机从其他路由首次进入、插件缺失时仍打开表单（U0-4） |
| MU-05 | VERIFIED | 实机：缺插件→Create 报错、名称保留；恢复后同输入恰新增 1 个并进入其聊天（`assistant list` 核对）；requestId 幂等见核心 receipts 测试（U0-5） |
| MU-06 | PARTIAL | Host `groups` 路由与 capability `personalAgentGroups`，golden 双端；旧 Host 在手机/桌面的“unavailable、不冒充普通 Assistant”无专项实机/测试，NOT VERIFIED |
| MU-07 | VERIFIED | `group_test.go`：成员/消息/delivery 以 participant id 记录；`TestGroupAuthorshipFollowsIdsNotNames`（两个成员改成同名后 @ 仍只投给被指定的 id、改群名不移动消息、同成员的另一个群是独立会话）；`TestGroupMembersKeepAtLeastTwoAndRemovalSkipsPendingDeliveries`（移除成员其待投递被跳过、不可低于 2 人） |
| MU-08 | NOT VERIFIED | 手机 `[id].tsx` 已直达共享 ChatView（U1-7 IMPLEMENTED）；真机验证被手机锁屏阻塞（见 §3 手机真机） |
| MU-09 | NOT VERIFIED | Android 键盘/附件/长列表/草稿保留需真机；原 Pane Chat 的 `pnpm test` 无回归，但不等于真机验证 |
| MU-10 | PARTIAL | 仅用户精确 @ 建 delivery、无 @ 不起 Agent、`say` 要求真实在线 Agent；实机 Duo 验证无 @ → 无 Agent（U1-3/4）；预算/ACL 对跨 Assistant 交互的专项未做（Peer 交互整体 DEFERRED） |
| MU-11 | NOT VERIFIED | Peer Delegation 未实现（审计表：Peer DM/Group、跨 Assistant @ 延后）；Worker Task 与 Assistant 分离是既有模型，Memory 默认不共享（share 需显式） |
| MU-12 | PARTIAL | 每个 Assistant 创建即有默认 Prompt 与版本（U2-1）；Coordinator prompt 规定 Memory 只能提议、不改设置（U5）；“Memory 不能覆盖更高优先级 Policy”仅在 prompt 层，无对抗性测试，NOT VERIFIED |
| MU-13 | VERIFIED | configured/granted/bound/verified 四态分开；实机：12 个已配置、仅授权 1 个，运行中 `/mcp` 恰 1 个 connected；probe 报 connected 与 failed 各一（U2-3） |
| MU-14 | PARTIAL | Registry 只读名称/传输/程序，不含 args/env/header，Host 不下发 secret；Codex/其它 Agent 明确 `mcp_unsupported`；撤销授权后运行中会话的行为、服务器断开实机 NOT VERIFIED |
| MU-15 | PARTIAL | Assistant 级 Agent/Model/Effort 与 MCP 绑定互不污染（单测 + 实机 `--effort low`）；已运行 Agent 不被重启（设计 + `runtime` 展示 behind 标记）；A/B 并发切换的实机交叉验证 NOT VERIFIED |
| MU-16 | PARTIAL | 接受→计划→委派→核验→才完成→汇报事实卡的闭环单测齐全；实机：批准计划后 12 s 内出现任务 + `delegated` 事实，重启不重复；真实 Agent 跑完一个里程碑并回证据的端到端 NOT VERIFIED（为省预算只走 Draft 路径） |
| MU-17 | PARTIAL | 结果只由核心写成系统事实卡（“Solco record”），不经 Agent 转述（U3-2/3-4）；requestId 幂等保证重试不重复；手机断网中途的实机 NOT VERIFIED |
| MU-18 | PARTIAL | `Supervisor` 只推进已批准计划；任务预算 held 一次、扇出上限、重规划上限 5、陈旧退避（U3）；Quiet Hours/日预算/等级/汇报间隔单测（U5）；多设备撤销与长时间无限循环观测 NOT VERIFIED |
| MU-19 | PARTIAL | `routine draft` 只写草稿（`draft: true`，70 s 0 任务），批准卡绑定定义哈希后才启用（U4 实机）；Coordinator 在真实对话里自己调用 `routine draft` NOT VERIFIED |
| MU-20 | PARTIAL | Routine 记录 owner/requestedBy/source/correlationId，任务归 owner、`routine_done` 回 owner 账本；triggeredBy/executedBy/reportedBy 未作为独立字段，NOT VERIFIED |
| MU-21 | PARTIAL | 旧 `automations.yaml` 保持；实机整分触发恰 1 次、无双跑，暂停后不再触发；sleep/DST/重复事件/launch uncertain 沿用 schedule 包既有测试，本轮未新增 |
| MU-22 | PARTIAL | User add/edit、Agent propose、files 编辑作者分开记录；Forget 清空所有版本；陈旧 revision → conflict 候选并存、拒绝清单（实机见 §3 U5）；跨助手保护沿用 share ACL；每周模型整理未做 |
| MU-23 | NOT VERIFIED | Android/macOS 真机快切需手机；常驻 Host 不宣告桌面绑定能力的门禁有 E05 单测，真机 NOT VERIFIED |

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

### U3（2026-11-04）
- 插件：`cd plugins/solco-automations && GOTOOLCHAIN=auto go test ./...` PASS（含 `goal_plan_test.go` 10 例与 plan/facts goldens）。
- 根/Host/桌面：`GOTOOLCHAIN=auto go test ./internal/personalagent ./internal/remotehost ./internal/hostd ./internal/nativeui` PASS（nativeui 142 s，含轻量 Snapshot 不调用 `goal plan`、抽屉读 plans/facts 的断言）；`go tool mygo vet .` PASS；`frontend: bun run build && bun run build:mobile` PASS。
- 手机：`pnpm typecheck`、`lint`（0 error，1 个既有 tamagui.config 告警）、`test`（573 PASS）、`check:fixtures`（173 一致）、`check:chat-page`、`export:web` PASS；提交 `64390b7`。
- 实机（隔离 `HERDR_PLUGIN_STATE_DIR` + 临时 git 仓库，真实 daemon）：CLI 创建 Assistant/Goal → `set-plan`（coding 里程碑 + 依赖里程碑）→ approve → 12 s 内 Supervisor 建出 1 个 Draft 任务，事实 `accepted/planned/delegated`，里程碑状态 `needs_you`（等批准）、计划 `waiting_user`；重启 daemon 后任务数仍为 1。
- 边界：汇报是确定性系统事实卡，不唤醒 Agent 转述（有意，避免“Agent 自述完成”）；“核验后完成”的完整闭环（任务 done → verified → completed）由 `TestSupervisorDelegatesVerifiesAndOnlyThenCompletes` 在 Store 层覆盖，没有用真实 Agent 跑完整任务（避免费用/副作用）。
- NOT VERIFIED：桌面抽屉 Progress 区与手机 GoalPlanBlock/FactsStrip 的实机视觉；真实 Coordinator 自己写出 plan 的行为；真机手机联测（手机锁屏，BLOCKED）。
- 进度：VERIFIED 18 / 总 22（不含 DEFERRED）= 82%。

### U4（2026-11-04）
- 插件：`cd plugins/solco-automations && GOTOOLCHAIN=auto go test ./...` PASS（`routine_test.go`：草稿不触发且等人、拒绝清单、批准后恰好触发一次且任务属 owner、完成后 owner 账本 `routine_done`、批准后改定义→stale 不启用且旧卡作废、取消/暂停/拒绝）。
- 根/Host/桌面：`go test ./internal/personalagent ./internal/remotehost ./internal/hostd` 与 `go test ./internal/nativeui -run "Assistants|Coordinator|Persona"` PASS（`TestPersonalAgentRoutineRoutes`、`TestRoutineGolden`、抽屉 Routines 断言）；`frontend: bun run typecheck && bun run build` PASS。`internal/nativeui` 全量、`go tool mygo vet .` PASS。`TestAddressWatcherQuietWhileHealthy` 依赖本机网卡，早先一次运行失败、全量重跑通过，`watch*.go` 本轮无 diff（环境相关，非本改动）。
- 手机：`pnpm typecheck`、`lint`（0 error）、`test`（575 PASS）、`check:fixtures`（174）PASS。
- 实机（隔离 state + 隔离 `HERDR_PLUGIN_CONFIG_DIR` 的真实 daemon、临时 git 仓库、真实 headless claude `-p` 一句 OK）：`routine draft` 每分钟 cron → 草稿写入 yaml（`draft: true`、`headless: true`），70 s 内 0 个任务（草稿不触发）；`approval decide`（actionHash 来自卡）→ draft 标记消失、状态 active；下一个整分 1 个任务（归 owner、done、15 s），facts 为 `routine_done / routine enabled / routine drafted`；旧调度器历史日志同一时间只有 1 条（无双触发）；`routine pause` 后 65 s 无新任务。收尾：删除该 automation，关闭测试期间创建的 Herdr 工作区，删除临时 worktree。
- 教训：隔离 state 目录不隔离 config 目录——daemon 会读到真实的 `automations.yaml`；做会写 yaml 的实机验证必须同时设置 `HERDR_PLUGIN_CONFIG_DIR`（本轮已隔离；此前 U2/U3 的隔离 daemon 读的是真实 yaml，只读，未写入）。
- NOT VERIFIED：桌面 Routines 抽屉页与手机 RoutinesList 的实机视觉；Coordinator 自己在聊天里调用 `routine draft` 的真实对话；DST/睡眠恢复（沿用 schedule 包既有测试，未新增）；Agent 非 claude（headless 能力随 `KindRunsHeadless`）。
- 进度：VERIFIED 19 / 总 22（不含 DEFERRED）= 86%。

### U5（2026-11-04）
- 插件：`cd plugins/solco-automations && GOTOOLCHAIN=auto go test ./...` PASS（`memory_files_test.go`：投影/编辑 revision/陈旧冲突并存/拒绝清单/导入与隔离/整理/上限；`proactivity_test.go`：默认与校验、Quiet 跨午夜、三个等级、延后并恢复、日预算、报告间隔、心跳随等级）。golden `memory-*`/`proactivity-*` 的条目顺序原本依赖随机 id（固定时钟下 created_at 相同）——契约测试里让时钟前进 1 s，连跑 5 次稳定。
- 根/Host/桌面：`go test ./internal/personalagent ./internal/remotehost ./internal/hostd ./internal/nativeui` PASS，`go tool mygo vet .` PASS；`frontend: bun run typecheck && bun run build && bun run build:mobile` PASS。
- 手机：`pnpm typecheck`、`lint`、`test`（583 PASS，含 `memory-model.test.ts` 与 Memory/Proactivity golden）、`check:fixtures`（178）、`check:chat-page`、`export:web`（含 `/assistants/memory/[id]`、`/assistants/behavior/[id]`）PASS。
- 实机（隔离 state+config 的真实 daemon，`SOLCO_ASSISTANTS_MEMORY` 指向临时目录，无 Agent 启动）：`memory add` 5 s 内出现 `facts/<id>.md` 与 INDEX；手改正文→revision 2（history：`files` 作者）；`notes/idea.md` → 1 个 candidate 且原文件被移除；再改文件（核心未动）→ 正常新 revision；改文件的同时经 CLI 以 expectedRevision 改核心 → 核心文本为准，文件被还原，另有一条 `conflict:<id>@4` 候选保留了文件版本；写入 `sk-ant-…` 的编辑被还原（文件中 0 处）。`proactivity show` 默认 follow_up/20/revision 0；`set` 后 revision 1；`25:00` 被拒（`invalid`）。收尾：daemon 已停、临时目录与环境变量已清。
- NOT VERIFIED：桌面 Memory/Behavior 标签与手机两屏的实机视觉；手机真机；Quiet Hours 真实跨午夜在 daemon 里的墙钟行为（单测用注入时钟）；每周模型整理（需要模型预算，未做）；“批准后自主”等级（未做）。
- 已知限制：作者标签不是证明；重复项的“保留两者”即不处理；Routines 不受主动等级影响；手机展示条目而非文件树。
- 进度：VERIFIED 21 / 总 22（不含 DEFERRED）= 95%（U1-7 仍因手机锁屏无法真机安装停在 IMPLEMENTED）。

### 手机真机（2026-11-04 14:2x）
- `scripts/android-release.sh`（首次因 gradle 客户端随 shell 返回被杀，改用 `start_new_session` 分离启动后 BUILD SUCCESSFUL）：arm64-v8a release apk 72 MB；手机亮屏后按 vivo 流程勾选“已了解风险检测结果”+“继续安装”，`adb install -r` → `Success`，`pm grant POST_NOTIFICATIONS`，电池白名单已加，`pidof` 有进程，截图显示 Agents 列表且连上 Mac（Relay → 桌面 `Solco.app` 0.12.2 重装后 P2P `192.168.5.68`）。
- 桌面：`solco-install` 流程（vet → build → 退出旧实例 → 替换 → 签名 → 启动）完成，`assistants available` 日志出现。
- NOT VERIFIED：手机上 Assistants 入口 → Memory/Behavior 两屏的真机视觉与读写。截图序列中途发现手机前台已是微信（用户在使用手机），而 adb 截图疑似停在旧帧，随即停止所有 adb 输入，避免误触用户的前台应用；其间有一次落点 (1175,1366) 的点击发生在此判断之前，对象不明，需用户自查微信是否被误触。Mac 端重装后手机顶栏暂未再显示 Assistants 机器人图标，尚未定位（Host `capabilities.personalAgent` 由 `assistantsRemote` 动态给出，日志里桌面端 `assistants available` 已出现；需要在手机前台稳定、用户在场时重新核对，不能用 NOT VERIFIED 以外的状态记录）。

### U5 桌面实机视觉（2026-11-04 14:3x，重装后的 `Solco.app` 0.12.2，Computer Use 后台截图）
- Assistants ▸ Ada ▸ More 的标签条含 Memory / Behavior / Work style；Memory 标签显示 `0 of 500`、Open folder、添加框与空态文案；Behavior 标签显示三档等级（当前 Follow up）、Quiet hours 开关、提醒间隔 0、日预算 20、`0 of 20 started today`、Save、说明“批准的 routine 与心跳唤醒不受影响”。数据来自真实核心（默认值，revision 0），证明已安装的插件二进制与 bridge 读路径联通。
- 仍 NOT VERIFIED：桌面 UI 里的写入（Computer Use 往 WebView 文本框输入未生效，读回为空，未强行）；写路径由 `TestAssistantsMemoryAndBehaviorBridge`、Host 路由测试与隔离 daemon 实机覆盖。
- 手机顶栏 Assistants 入口消失一事：Host 的 `personalAgent` 能力由 `syncRemoteAssistants` 在首次 overview 到达（日志 14:25:55）后才置位，手机若在此前取了 bootstrap 且之后没刷新就会缺该入口；也可能是 adb 截图停在旧帧（状态栏时钟从未走动）。未在手机上复核（用户正在用手机）。

### 手机 Assistants 入口（2026-11-04 15:xx，更正上一条“入口缺失”）
- 结论：入口没有消失。临时诊断日志 `remote host bootstrap personal agent served=true`（临时、已移除；只记布尔能力）确认 Host 已发 `personalAgent:true`；`uiautomator dump` 在手机首页看到 “Assistants” 按钮。此前判断依据的 adb 截图在该手机上滞后，不可作为瞬时状态证据（本机用 `uiautomator dump` 取实时 UI）。上一条“未结问题”作废。

### U5 手机真机验证（2026-11-04 15:xx，vivo，release apk，经 `uiautomator dump` 取 UI、`adb input` 操作；对象为测试 Assistant “Ada”，无 Agent 启动）
- Assistants → Open Ada：详情页含 Work style / Memory / Behavior 按钮（来自 Host 能力 `personalAgentMemory`/`personalAgentProactivity`）。
- Memory 屏：`Ada · Memory`、`REMEMBERED · 0/500`、空态；添加 “U5 phone check note” → `1/500`，`you · rev 1`；Edit → Save → `rev 2`；History 展开出 `rev 2 · You · approved` 与 `rev 1`；Forget → Forget for good → `0/500`（已清理，无遗留）。
- Behavior 屏：三档等级（当前 Follow up）、Quiet hours Off、间隔 0、日预算 20；选 Suggest → Save → 退出重进仍为 Suggest（落盘）；恢复 Follow up → Save → 重进确认已还原。
- 仍 NOT VERIFIED：手机上 Quiet Hours 开启与时区选择、预算为 0 的行为；MU-08/09/23 的手机聊天路径尚未逐项驱动。
