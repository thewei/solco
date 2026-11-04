# Assistants 3.1 — 实施台账（Implementation Tracker）

> 主规划：`docs/assistants-messenger-agent-v3-1-plan.md`（MU-01～MU-23）；手机配套：`solco-mobile/docs/assistants-messenger-agent-mobile-plan.md`。
> **2026-11-04 深度复审警告**：历史 U0～U5 台账的 `22/22 VERIFIED` **不代表发布验收通过**。新审计在隔离副本中复现了 CLI 用户/Agent 角色未隔离、Group delivery 假 ACK、Goal 未验证成功标准即完成、Group feed 分页不准确等问题。后续修复优先级与复现见 `docs/assistants-v3-audit-2026-11-04.md` §6～7；**目前不得按“全部达成 Goal”结案**。
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
| U1-7 | U1 | 手机：Chats 列表、直达单聊、群聊 | U1-6 | VERIFIED | Chats 列表、点 Assistant 直达共享 ChatView、真群聊页（solco-mobile `4076612`）；`pnpm typecheck/lint/test/export:web` PASS。真机安装 BLOCKED（手机锁屏 `INSTALL_FAILED_ABORTED: User rejected permissions`，未绕过），故不标 VERIFIED 真机（vivo release apk，2026-11-04 15:xx）：Chats 列表（Duo/Ada）、点 Ada 直达共享 Chat 页并与真实 Claude Coordinator 往返（PONG）、真群聊页读（@B 高亮、`✓ B` 投递、系统状态卡、B 的回复带 Reply 引用）与发送（无 @ 的消息不起任何 Agent，`herdr workspace list` 无新增 `auto:` 工作区）。 |
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
| MU-08 | VERIFIED | 手机真机（vivo，release apk）：Assistant 页 Start conversation → Mac 起真实 Claude Coordinator（工作区 `auto: … Ada · Coordinator`）→ 共享 Chat 页打开，Coordinator 的“Ada 已就位”回复可见；在手机输入框发 “Reply with exactly PONG…” → 约 40 s 后回复 `PONG`（见 §3 手机 Chat 真机）。附带修复两处真实缺陷（守护进程缺失、首次落到 Terminal）与一处标题问题 |
| MU-09 | NOT VERIFIED | Android 键盘/附件/长列表/草稿保留需真机；原 Pane Chat 的 `pnpm test` 无回归，但不等于真机验证 |
| MU-10 | PARTIAL | 仅用户精确 @ 建 delivery、无 @ 不起 Agent、`say` 要求真实在线 Agent；实机 Duo 验证无 @ → 无 Agent（U1-3/4）；预算/ACL 对跨 Assistant 交互的专项未做（Peer 交互整体 DEFERRED）；手机真机：无 @ 的群消息不起 Agent（见 §3 手机 Chat 真机） |
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

### 手机 Chat 真机（MU-08，2026-11-04 15:0x，vivo + 真实 Mac，Ada 为测试 Assistant）
- 过程：手机 Assistant 页 Start conversation → 页面停在 “Starting the Coordinator on your Mac…” 超过 2 分钟，Mac 日志只有 `coordinator.start` 操作、无后续。
- 根因 1（真实缺陷）：插件守护进程根本没在运行（`ps` 无 `solco-automations daemon`，`herdr plugin log` 无该插件日志）——Herdr 只在 server 启动时跑插件的 startup hook，Solco 之后才 link/更新的插件没有 daemon，排队的 Coordinator 永远停在 `starting`；Assistants 路径此前不会自启它（只有 Automations 页的 Start Scheduler 按钮会）。修复：`plugins/solco-automations/main_assistant.go` 的 `ensureDaemon`——`assistant coordinator start` 成功后若 `daemon.Running()` 为假，就 `Setsid` 拉起 `daemon`（日志进插件 state 目录 `daemon.log`，flock 保证第二份自行退出，幂等，不让命令失败）。实机：重建插件二进制后再次 start，3 s 内 daemon 在跑，30 s 内出现真实 Herdr 工作区与 Claude 窗格（状态 done）。`go test ./...`（插件）484 PASS。
- 根因 2：Coordinator ready 的瞬间 Herdr 树里该窗格还没有 `agent`，手机 `paneRoute` 因此选了 Terminal 而不是 Chat，手机落在终端画面。修复：`assistants/[id].tsx` 在窗格带 `agent`（或 Host 不提供 chat）之前不自动跳转；重新进入后落在共享 Chat 页。
- 问题 3：Chat 页标题被窗格自己的标题（首条用户消息，即 Coordinator 启动 prompt）占用，显示 “You are the Coordinator of t…”。修复：从 Assistant 页进入时把 Assistant 名带成 `title`，聊天页在带 `assistant` 参数时以它为标题；“Open conversation” 按钮也补上 `assistant` 参数（此前缺，导致聊天页里“回到 Assistant”入口丢失）。`pnpm typecheck`/`lint`/`test`（583 PASS）通过；新 apk 已装机复验，见下一条。
- 已知限制（未改）：Coordinator 的 152 行启动 prompt 作为首条“用户消息”出现在 Chat 里（折叠为 “Show all 152 lines”），因为它确是该会话的真实首条输入；模型名徽标显示 `glm-flash`（该 Claude 的本机配置）。
- 复验（新 release apk 装机后）：停 Coordinator、杀掉 daemon（`ps` 0 个）→ 手机点 Start conversation → 45 s 后 daemon 已被自动拉起（1 个）、手机直接落在共享 Chat 页、标题为 “Ada”（副标题 `claude · auto: … Ada · Coordinator`），不再经过 Terminal。PASS。
- 收尾：Ada 的 Coordinator 已 stop，两个测试用 Herdr 工作区已关闭；daemon 留在运行。
- 群聊真机（同日）：手机 Chats → Duo：@B 高亮、`✓ B` 投递标记、“B is starting…” 与“B 在等你：Claude Code 在问启用哪些 MCP”系统卡、B 的回复带 `You @B ping` 引用与 Reply；从手机发无 @ 的 “plain no mention” → 作为你的消息出现、无投递标记、无新 Agent 工作区。U1-7 → VERIFIED。进度：VERIFIED 22 / 22（不含 DEFERRED）= 100%；MU 逐项状态见 §2（VERIFIED 与 PARTIAL/NOT VERIFIED 的差别以该表为准，不因任务表 100% 而上调）。

## 4. 深度复审发现的发布阻断项（独立于历史阶段完成率）

> 本表更新于 2026-11-04；具体文件/行号、隔离 Go 负向测试输出、修复策略见 `docs/assistants-v3-audit-2026-11-04.md`。**状态 `BLOCKED` 意为用户的最终 Goal 尚未满足，并不是说原模块根本不存在。**
>
> 验收原则：修复者将临时复现变成正式 regression tests（先 RED，再修复，最终 GREEN），至少双端契约同步+真机验证，才能关闭下列项。原文 U0～U5 的 22/22 是历史子任务完成记录，不因这里新发现的漏洞自动改成未实现。

| ID | 紧急度 | 真实缺口 | 状态 | 复审证据 / 关闭条件 |
| --- | --- | --- | --- | --- |
| AUD-01 | **P0 安全** | Agent shell 与 Owner 调用相同 unrestricted CLI；`approval decide` 可取卡片 hash 后批准、`group post` 可冒充人触发 B，且 `memory list` 可读他人私有记忆 / `memory add` 可伪装用户写入 approved | **BLOCKED** | 隔离 `TestAuditPublicAgentCLIHasNoUserActorBoundary` 与 `TestAuditAgentCLIReadsOthersMemoryAndForcesApprovedMemory` 均 PASS（证明越权入口存在）；关闭需 owner/agent principal + 可信进程权限隔离/受限 broker，测试 Agent 审批/冒充/跨助理 Memory 被拒 |
| AUD-02 | **P0 可靠性** | GroupPump 的 50 条消息窗口未包含目标 @ 仍标记 delivered | **BLOCKED** | `TestAuditGroupTargetPastFiftyIsNotAcknowledgedWithoutDelivery` 失败；关闭需目标消息确认包含、到达 Agent、不可误 ACK，重启/多 @ 回归 |
| AUD-03 | **P0 目标真实度** | `RunDone` → `MilestoneVerified` → `GoalCompleted`，未另行核验 Goal 的 `successCriteria` | **BLOCKED** | `TestAuditGoalNeedsSuccessCriteriaProofBeforeComplete` 失败；关闭需独立 evidence validator/人工验收 receipt |
| AUD-04 | P1 消息一致性 | 多群交错时“最新 N 条”页不完整；delivery 查询不按消息页截断 | **BLOCKED** | `TestAuditGroupFeedMustReturnLatestTwentyWithInterleavedGroups`、`TestAuditGroupFeedDeliveryBoundedToMessagePage` 均失败；关闭需正确 pagination、delivery 有界、手机上翻旧消息 |
| AUD-05 | P1 身份路由 | 同名 `@` 和名字前缀误识别；Assistant 自己 group say 可 claim 他人（缺 caller-bound identity） | **BLOCKED** | typed ID mention + server 检验 + spoofed author 拒绝 + 重名/前缀场景 |
| AUD-06 | P1 聊天与回响 | 手机 idle 仍进入管理页；Goal/Routine 只有系统事实卡而无聊天里持久化 Agent 主动回复/outbox | **BLOCKED** | 空 Assistant 直接 ChatComposer；Goal→Task→Validation→Report→原聊天双端和断线恢复 |
| AUD-07 | P1 资料与记忆 UX | Profile description/Permissions 不能编辑且强制生效；手机 Memory Add/Edit 失败后丢输入/退出编辑；手机文件树未实现（**私有记忆的跨助理访问安全另见 P0 AUD-01**） | **BLOCKED** | Go Host versioned grants/profile + 双端 More；失败保留输入、跨设备文件视图和权限回归 |
| AUD-08 | P2 视觉和端侧 | 图片 Avatar、Chats 混排/未读/最近消息、Android 输入法/切后台、低网/MCP 跨 Provider 无完整验收 | **NOT VERIFIED** | 深浅主题/中文长名/真机与多 Provider 的能力门禁，每项记录证据 |

**当前发布结论**：历史 22/22 的框架/模块功能不能代替新识别的 P0 级安全、消息可靠性和 Goal 成功核验门禁。优先修复 AUD-01～03，再解决 AUD-04～07，继续保持 Go 单 Scheduler/Herdr/SQLite 事实源；Workflow Go 框架选型仍延期。

## 5. 第三轮深度复审发布阻断项（2026-11-04，独立于 22/22 历史进度）

> 复现源码行号、隔离测试步骤、风险与精确修复门禁详见 `docs/assistants-v3-audit-2026-11-04.md` `8。测试均在 /tmp 临时插件副本中创建临时测试文件、运行后删除；**不修改本机真实 automations.yaml/SQLite**。期间另一个开发者持续改动 Group/Goal/Mobile 未提交文件，以下测试只核查本轮读到的快照。

| ID | 优先级 | 现状 | 状态 | 闭环要求 |
| --- | --- | --- | --- | --- |
| AUD-09 | **P0** | **8 次并行 config.Put 最终只剩 2/8 Routine**，`config.indent` 被 `go test -race` 检测出 DATA RACE | **BLOCKED / RED** | `Put/Remove/Approve/Pause` 全部跨进程锁内读-改-替换，revision fence 和并发压力测试无丢失、race 检查 GREEN |
| AUD-10 | **P0** | 同名草稿 requestId 重用但 body 变更：Store 正确返回 `request_conflict`，`automations.yaml` Prompt **却已被写成被拒绝的文本** | **BLOCKED / RED** | Receipt/版本检查在任何文件副作用前；YAML+SQLite 恢复协议，拒绝路径零副作用，kill-point 和重试回归 GREEN |
| AUD-11 | P1（依赖部署） | `ensureDaemon` 非 standby；无 Herdr session、活跃任务时 daemon 自行退出，未来 Routine/Goal 需要安装 standby login service | **NOT VERIFIED** | 校验实际 service health、安装状态及 0 session/关闭 Mac App/睡眠/重启后照常调度；未安装时不许承诺持续运行 |
| AUD-12 | P1 | Mobile 空闲 Assistant 点 `Send` 只创建 Coordinator，再把首条消息放入 `ChatPageView.seedDraft`，并未直接提交 Agent；离开页面前首条待发消息只在 `useState` | **PARTIAL / NOT E2E VERIFIED** | 首次点击就进入有收据的耐久发送或明确 Draft 语义；Android 冷启动、断网、重进与 trust 弹窗现场测试 |
| AUD-13 | P1 风险 | `paWrite` 用 `context.WithoutCancel` 移除请求取消与 deadline，通用 CLI 进程没有二次超时 | **NOT REPRODUCED / NEEDS TEST** | 有界 background command + durable receipt，CLI hang、Host shutdown、断网限流与清理测试；不能无限占用 goroutine |

**新基线门禁**：插件全模块 Go test PASS；Go Host/PersonalAgent/Hostd PASS；MyGo vet PASS；Frontend TS+双 build PASS；Mobile TS+Lint+Vitest **592 pass / 2 skip**；Host fixtures **178 synced**、共享 Chat **synced**。以上不代表 AUD-09/10 已修复：隔离负向用例明确仍 RED，也没有本轮长时间真机运行证据。

## 6. 审计修复轮（2026-11-04，对应 `docs/assistants-v3-audit-2026-11-04.md`）

> 状态词沿用本文：`PASS`＝有自动测试且（适用时）实机验证；`PARTIAL`＝已落地一部分；`DEFERRED`＝明确不在本轮、附原因；`NOT VERIFIED`＝代码在，缺真机证据。

| 审计项 | 状态 | 做了什么 / 证据 | 未做与原因 |
| --- | --- | --- | --- |
| P0-01 / AUD-05 同名/前缀 @ | PASS（服务端）+ PASS（手机/桌面客户端单测） | Go `checkMentions`：@ 必须是成员 ID，且正文里要有对应的精确 `@Name`（词边界；CJK 无空格也算；`bo@x.com` 不算）；重名只能靠选择器的 ID，输入文字不会选中其一；`TestGroupMentionsTypedIDMustMatchExactName`、`group_audit_test.go`。手机/桌面 `resolveMentions(text, members, picks)`，重名未选择时报“choose which one”；`group-model.test.ts` 重名/前缀/CJK/picks 用例 | 实机双端未再逐项走 |
| AUD-02 / BLOCKER-02 >50 条积压 @ | PASS | delivery 的 prompt 带“目标消息所在窗口”，窗口之外只写“earlier messages are not shown”，不会在目标不在窗口时 ACK（`groupDeliveryWindow`、`TestAuditGroupTargetPastFiftyIsNotAcknowledgedWithoutDelivery`） | — |
| AUD-03 / BLOCKER-03 Goal 成功标准 | PASS | Supervisor 仅在“每个里程碑都是编码任务（合并即证据）”时自动 `CompleteGoal`；其余需要 owner 确认（plan 状态 `waiting_user`，`FactReady`）。`goal_plan_test.go` | 独立 evidence validator 未做（需要产品决策） |
| AUD-04 / HIGH-01/02 群聊 feed 分页与 delivery 有界 | PASS | `GroupFeedSince` 取每群最新页；delivery 查询按页 seq 范围；两项审计测试转正 | — |
| P1-06 / HIGH-04 手机 Memory 失败丢草稿 | PASS（逻辑）/ NOT VERIFIED（真机） | `memory/[id].tsx`：Add/Edit 只在 `onSuccess` 清空/退出编辑，失败保留输入，同一 job 重试沿用同一 requestId（`useMemoryWrite`）。桌面 `MemoryTab` 同类问题一并修（`Act` 返回拒绝原因，成功才清空）；Rename / Watch / New task 表单同理，失败时在 Sheet 内显示原因 | 手机离线/重载保留文本未做 |
| P1-07 MCP 边界 | PASS | 桌面 Work style 与手机 Style 的 MCP 卡明写“按 server 授权、不是对 Agent 其它工具的沙箱” | 跨 Provider 绑定仍只有 Claude |
| P2-08 Chats 混排 | PASS（排序）/ NOT VERIFIED（真机） | wire 增 `overview.assistants[].updatedAt`（Assistant 与其任务的最近活动，插件→根模块→Host→golden 双端同步）；手机 `mixedChats`：需要你的 Assistant 在前，其余 Assistants 与 Groups 按最近活动混排，老 Host 无该字段时按名字（`personal-agent-model.test.ts`） | 最近消息预览/未读计数/图片头像：需要新的已读游标与预览 wire，未做 |
| P0-02 / AUD-06 手机 Assistant 空态 | PASS（逻辑）/ NOT VERIFIED（真机） | `assistants/[id].tsx`：Coordinator 未启动时顶部直接是“Message <name>”输入框 + Agent 选择，Send＝幂等 `coordinator start`，Coordinator 就绪后自动进入共享 Chat，首条文字作为 `draft` 预填；启动失败文字退回输入框 | 首条文字不自动发送（手机上宁可多按一次 Send，也不在断线时重复发送）；管理区仍在同页下方，未折进 More |
| P0-03 ReportOutbox | DEFERRED | 需要新的持久化表、Report Turn 的授权/预算/QuietHours 规则与双端收据——是一个独立阶段，不是修补 | 下一阶段按审计 §P0-03 的设计做 |
| P1-04 Peer 委派 / P1-05 群聊并入共享 Chat | DEFERRED | 都是架构级重写（授权 Peer DM + Directory；共享 ChatView 的 ConversationTarget）；短期已修身份/草稿/分页 | 见 `docs/assistants-collaboration-plan.md` |
| HIGH-03 Profile/Permissions 可编辑 | DEFERRED | 目前只有改名 + Work style；`scope/workspaceRefs/defaultPolicy` 在核心里没有被强制执行的路径，补 UI 等于做假权限，故不加 | 需要“版本化 UpdateAssistantProfile + 服务端强制 grants” |
| BLOCKER-01 CLI 无调用者身份 | DEFERRED（风险已记） | 同一 HOME 下的 CLI 无法区分 Agent 与 Owner，需要受控 Broker/OS 隔离；本轮不假装解决。Agent Prompt 仍声明不得替人批准，但这不是安全边界 | 单独的安全阶段 |

### 用户直接报告的问题

| 问题 | 状态 | 证据 |
| --- | --- | --- |
| 侧栏群聊头像重叠太小 | PASS（实机） | `treeRowSpec.AvatarWidth` + `groupAvatarWidth`：两个正常尺寸头像重叠 |
| 切换助理很慢 | PASS（实机） | 选择事件 `assistants:select` 让页面立即重读；`agentsReady` 避免“请先开启 Agent”闪现；700 ms 补读 |
| 在助理页点侧栏其他项不切换 | PASS（实机+测试） | `project_tree.go` 的 `treeShowsSelection()`（`/assistants` 时 Workspace 行不再被视为已选，点击不再走“已选则折叠”分支）；`TestAssistantsPageLeavesThroughAWorkspaceRow` 先 RED 后 GREEN |
| 聊天 composer 与 Agent Chat 不一致 | PASS（实机目视） | `Composer`/`GroupChat` 改用与 `App.tsx` 相同的 `composer > cmp > textarea + cbar` 结构与样式 |
| 长时间运行后崩溃 | 见 `docs/debug/2026-11-04-jsc-heap-collector-crash.md` | |

## 7. 第四轮深度审计阻断项（2026-11-04）

> 详细源码证据、临时复现步骤与关闭门禁见 `docs/assistants-v3-audit-2026-11-04.md` `9。以下 **5 项为隔离负向测试 RED**，不是说既有单元测试无法编译。所有 /tmp 临时测试副本已删除、没有操作真实 SQLite 或记忆文件；其它 Agent 同期进行的 Group/Goal/UI 修复应单独复验后记 PASS。

| ID | 优先级 | 已证实问题 | 状态 | 关闭要求 |
| --- | --- | --- | --- | --- |
| AUD-14 | **P0 隔离** | 将 A 的 `facts/` 改成指向 B 的 `facts/` 的目录 symlink 后，A 的 approved fact 被投影到 B，再被 B Sync 成 **candidate**（**不是**自动 approved） | **BLOCKED / RED** | Memory 目录与父目录 no-follow、安全根路径；跨 Assistant 目录交叉和目录替换攻击的负向 GREEN |
| AUD-15 | **P0 授权** | 在 `facts/<id>.md` 写入内容，`Sync` 无调用者认证就将它更新为新的 **approved** Memory；Agent 与 Owner 共 Unix 用户时文件 ingress 绕过人工审批 | **BLOCKED / RED** | Agent 写入最多 candidate、Owner 的可信编辑才直接批准；接入 AUD-01 owner-vs-agent 权限方案，并测试不可伪造 |
| AUD-16 | P1 幂等 | 两位助理导入相同 `notes/shared.md` 正文，A Imported=1，但 B Imported=0 / Refused=1（`file-new:hash(rel,body)` 未含 assistant ID） | **BLOCKED / RED** | 请求键按 assistantId+相对路径+内容哈希分域，两边各自成功且不重复 |
| AUD-17 | P1 版本 | Memory 文件编辑 `Apple→Banana`，UI 修改 `Cherry`，文件再次改 `Banana` 被拒绝并恢复 Cherry（`file:memoryId:hash` 未含 baseRevision） | **BLOCKED / RED** | 内容 hash + **基准 revision** 的幂等键，允许合法 ABA 回改；重试同一 revision 不重复 |
| AUD-18 | **P0 审批** | `RoutineKeeper` 校验 definition hash 后、`config.Approve` 之前被并发修改 Prompt，仍启用新 Prompt 且标 Routine Active | **BLOCKED / RED** | 同一跨进程锁内对**同一版本文档** `ApproveIfHash`；SQLite/YAML crash-reconcile 协议，1000 次交错不启用未批准内容 |

**独立门禁**：插件 Memory/Routine/Approval/Group/Rule 专项 Go race 测试 PASS；Go Host+PersonalAgent+Hostd PASS；Mobile TS/Lint/Vitest PASS（592 pass，2 skip）。真正的 OS 沙箱、生产部署、Mac/Android 实机与跨进程 stress 本轮没有执行。**优先次序：AUD-18 → AUD-14/15 → AUD-16/17**；不可因历史 22/22 VERIFIED 而关闭这些问题。
