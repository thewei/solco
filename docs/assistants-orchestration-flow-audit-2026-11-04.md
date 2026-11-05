# Assistant 端到端编排与统一 Composer 审计（2026-11-04）

状态：**PARTIAL / 持续实施**。这是代码行为与自动化回归审计，不代表真实 OMP + Herdr + App 的实机验收。

## 目标流程

```
人提出一个目标或多个请求
  → Coordinator 在 Chat 中澄清边界/成功条件，说明分析与步骤
  → 将复杂目标持久化为 Goal Draft + 有依赖关系的 Milestone Plan
     或将互不依赖的事项拆为多个 Task
  → 对每个 Worker 生成独立完整 Brief（范围/输出/验收/阻塞上报）
  → Owner 批准 Goal / 发布 Coding Draft（审批不可跳过）
  → Go Supervisor 按依赖/预算/最大 fan-out 分派
  → Dispatcher 通过 Herdr 运行 OMP Worker，不复制运行时
  → Task/Run/Review 事件与证据持久化
  → Supervisor 校验里程碑并记录事实
  → Assistant WebView 显示 WorkPulse + FactStrip；配置了 reportEveryMin 时定期生成事实摘要
  → 人主动问 Coordinator 时，基于 Task Graph / Goal Plan / Facts 做自然语言总结
```

## 审计矩阵

| 阶段 | 核心代码 | 目前结论 | 缺口/后续 |
| --- | --- | --- | --- |
| 首次消息启动 Coordinator | `assistants/AssistantsPage.tsx`、`AssistantsBridge.EnsureChat` | **已实现（需实机）**：保留草稿、幂等请求、Herdr 确认后发送 | 启动超时/传输不确定时应有用户可操作的原会话诊断；不能自动重发可能已交付的消息 |
| 任务分析与计划 | `CoordinatorPrompt`、Goal Plan | **部分**：站立提示词规定分析、明确步骤、验收；Go 校验里程碑/依赖/修订 | **P0** 提示词不是强制编排器；尚无“每个重大请求→计划/执行记录”的可验证映射和审批交互 |
| 多任务创建与并行 | `CreateTask`、`Supervisor.dispatch` | **已实现**：独立任务 + 带依赖的 Goal 计划，fan-out 默认 3，幂等及预算 | **P1** 对每个用户请求的跟踪 ID/关联 Chat Turn 缺失；任务只有非结构化 Brief，缺少可审计的 schema |
| Worker OMP 执行 | `Dispatcher`、`HostLauncher` | **部分**：Herdr 唯一 Agent 运行时；不确定启动不会盲目重复 | **P0** 真实目录信任/权限屏、失联重连、启动成功但 Prompt 未交付的取证矩阵未封闭 |
| 完成/验证 | `GoalPlan`、`Supervisor.step`、`Review` | **已实现**：已做、验收通过、待人工 Review 不混为一谈 | **P1** Worker 证据格式不强制，结果质量需单独复核；Coding 必须 Owner 发布及 review |
| 事件/持续跟踪 | `Supervisor.eventFacts` | **本轮完善**：单独 Task 的创建/完成/失败也进入 Facts，事件水位可重启恢复 | 只有事实投影，暂未由后台 Agent 进行反思/重新规划 |
| 定时摘要 | `Supervisor.periodicReports` | **本轮新增，需真实通知验证**：用户启用 `reportEveryMin` 后依正式状态计数并留可去重事实，尊重 quiet hours；默认 0 不发送周期摘要 | **P0** 还不是聊天消息 outbox，没实现可靠的发送/ACK/重连补发，也没有手机 push；不能声称“关闭 App 也会收到 Coordinator 消息” |
| 对话 UI | `frontend/src/App.tsx`、`composer-surface.tsx`、`assistants/*` | **本轮统一**：普通 Chat、Coordinator 冷启动、Group 都共享 Composer 的 DOM 结构、输入控制与扩容规则；同一个 WebView SPA | **P1** 冷启动不能使用需要实际会话的附件、语音、模型切换、Context 环；后续可把可预启动的功能从 App controller 抽为共享状态机 |
| 任务概况 UI | `WorkPulse` / `FactStrip` | **本轮新增**：显示真实 Worker 数量/待处理，事实明确署名 Solco，未伪装 Agent | **P1** 需支持按 Task/Goal 来源跳转和证据引用、UI 实机截图与无障碍检查 |

## 约束与安全原则

- 任何任务创建都需要固定 Assistant ID；复杂任务创建 Goal Draft、Coding Task Draft 均保持人为放行。
- 任务事实与“协调者自然语言回复”是两个不同数据源，不可伪造成同一 Transcript。
- 进度百分比只有来源数据可证实时展示。当前使用真实任务状态和“已验证的里程碑数”。
- OMP 由 Herdr 唯一运行；不允许引入第二套后台 agent runtime 或直接绕过权限/信任 UI。
- 自动调度尊重 Enabled / Quiet Hours / Budget / MaxFanOut；循环失败应进入“需要人处理”而非无限重试。

## 后续实施优先级

1. **P0-1：请求关联/计划状态**：将用户发起的 Request ID、Chat source、Goal/Task 和计划版本建立 durable trace；复杂任务未规划时 UI 显示“规划中/需补充”，允许审核计划后发布。
2. **P0-2：可靠主动汇报**：设计受控 Activity outbox（sourceRef、sequence、delivery/ack/retry/quiet schedule），对接 desktop/phone 的可信通知及 Assistant conversation 中独立的系统进度卡，避免重复或覆盖 OMP transcript。
3. **P0-3：真实 OMP E2E**：隔离 HOME + Herdr 运行测试，涵盖 first send→plan→审批→两个并行 worker→失败/阻塞→恢复→验收→多次进度更新→睡眠/重启；必须验证 Hero/Herdr 进程与 SQLite 真值。
4. **P1-1：结构化 Worker Brief & 验收**：Go 端验证范围/输出/证据/下一步字段，不仅靠 Coordinator Prompt；人工可以修改、批准或拒绝。
5. **P1-2：跨端统一呈现**：同源 WebView Composer 全功能分级、可跟踪的 Task/Goal 卡和 evidence 深链；移动端宿主支持动态通知。

## 本轮自动验证

- 插件 `go test ./...`：**PASS**（包含 independent Task event ledger、周期报告与原有 GoalPlan/Supervisor suite）。
- 前端 `bun run typecheck`：**PASS**，桌面 `bun run build`：**PASS**（其他验证见开发回合记录）。
- 未进行真实 OMP + Herdr + GUI 端到端验收；不能据此宣称最终目标全部实现。

## 继续优化：Stop → Restart 防重复（2026-11-04）

- **新增安全闸**：Coordinator 的 `stopped / failed` 不再意味着可以直接重新启动。如果旧协调者或其 Run 仍关联 Herdr Pane，`coordinator start` 必须由只读 `HerdrPaneGone` 正面确认旧 Pane 已关闭；Herdr 不可用、Pane 仍在或记录的 Session / Workspace / Pane 不完整，全部拒绝。由于 `StopCoordinator` 目前只变更 SQLite 状态，用户必须先在原 Terminal 结束旧 Agent/关闭 Pane，再启动新 Coordinator。
- **核对 Run**：即使 Coordinator 行没有记录 Pane，依然读取旧 Task 的所有 Runs 验证；原 Run 处于 `claimed/starting/running/blocked/uncertain` 且没有可查 Pane 时直接拒绝，避免未经确认的重复启动。
- **并发和幂等**：Herdr 读在 SQLite 写事务之外完成；写入前比较 Coordinator revision 与所有旧 Runs 的状态和 Pane 身份。验证后才把遗留 `running` Run 收敛成 `cancelled`，并创建下一次 Task。同一 `requestId` 优先返回已有收据，不需要再次触碰 Herdr。
- **测试**：`TestStoppedCoordinatorCannotDuplicateLiveHerdrPane`、`TestAPICoordinatorRestartRefusesUnclosedPane`、`TestCoordinatorStopAndRestart`，插件全量测试 PASS；这不等于真实 OMP 进程停止的实机验证。
- **仍待修复**：Stop 目前不是 Herdr 的真实 kill/close；后续要增加受控的运行时停止控制器，并处理关闭时与运行中 Dispatcher 的竞态，不能把“已停止”标签当作真实进程存活证明。

## 继续优化：Archive → Restore 运行时安全（2026-11-04）

- Archive 在 SQLite 写事务之前经 Herdr 验证该 Assistant 所有已记录 Pane 都已关闭；历史 Run 虽已 `done` 但 Pane 仍在，同样拒绝归档。Herdr 离线、不确定或 Session/Workspace 身份不完整均不可视为关闭。
- 在确认外部 Pane 状态之后，写事务里再次对比所有 Run 的 Pane 身份，防止归档验证期间新增/切换 Pane；后续永久删除也采用同样的事务内核对。
- 对已安全结束的 Coordinator，Archive 将旧协调者状态重置为 `stopped`；恢复 Assistant 后可重新启动，但再次启动仍要重新验证原 Pane 关闭，不能凭历史成功证明盲目创建。
- 自动测试覆盖归档幂等、仍有 Pane 的拒绝、检查期间 Pane 变化的拒绝、归档-恢复-重启；`go test ./...`（plugin）PASS。没有实际 Herdr/OMP 进程级测试时，**不能宣称进程停止功能已实现**。

## 继续优化：可靠活动流与手机 ACK（2026-11-04）

- **唯一事实源**：不创建第二套 Assistant 对话运行时或模型。沿用 `goal_facts` 的 Solco-authored sentences；数据库升级版本 20 将原来可能被 SQLite 复用的 `rowid` 迁移为 `seq INTEGER PRIMARY KEY AUTOINCREMENT`（原序号保留），保证永久删除旧行后，新消息不会复用序号。
- **Activity 分页**：插件 `assistant activity list <assistantId> --consumer <key> [--after N] [--limit N]` 按 `seq ASC` 提供 `facts/nextSeq/hasMore/ackSeq`，单页最多 200。读取不修改状态，断线重连从最近一次 ACK 序号继续；条目 ID 去重；多 Assistant 的数据隔离。
- **显式确认**：插件 `assistant activity ack <assistantId>` 按 `requestId/consumer/seq` 更新 `activity_cursors`，只允许确认该 Assistant 真实存在的 seq，单向推进，事务化写入+请求收据回放。归档保留 ACK 游标，永久删除清除它。ACK 表示此消费端处理/看过，不保证手机 OS 的通知或协调者语言生成成功。
- **Host / 手机**：Host `GET /api/v3/personal-agent/assistants/{id}/activity?after=&limit=`（ScopeView）和 `POST .../activity/ack`（ScopeManage）共用原插件 CLI 缝；消费者 ID **完全由已配对的 Device ID 派生**，手机无法伪造；ACK JSON 拒绝未知字段。手机独立 Activity History 页面：显示新到旧逐页传递的消息（画面按旧到新排序）、支持主动标记已读、定期拉取、重连去重、跨 Mac 隔离；最新事实条保留。
- **自动测试**：迁移后序号无复用、分页跨多个消费端、幂等/拒绝非法 ACK、断线重开重放、删除游标、Host 鉴权/门关闭/设备绑定、手机 Zod Goldens/行为单测。
- **仍未实现**：不会自动把 Fact 插入 OMP 的聊天记录；没有可靠 OS Push/后台通知投递、自然语言总结生成与推送 ACK；启用 reportEveryMin 只产生可读的事实，不等于 Coordinator 主动发话。后续将复用 `personal-agent.changed` 事件提示客户端主动补读，而不是把事件帧当内容本体。

## 继续优化：Activity 变化提示与 Android 本地通知（2026-11-05）

- **变更提示不再遗漏“仅有事实变化”**：插件 Overview 增加 `activitySeq`（事实表最大 AUTOINCREMENT seq，无正文）；Mac `assistantsOverviewFingerprint` 将其纳入比对。同样任务数、Needs You 数但新产生 Fact 时，也通过既有 `personal-agent.changed` socket 触发手机重读。此帧依然只有 revision / assistants / needsYou，绝无 Prompt、目标、记忆或进度正文。旧 Host 的 ActivitySeq 字段缺省可兼容。
- **独立通知通道**：Host 新增 `GET/POST /api/v3/personal-agent/assistants/{id}/activity/notifications[ /ack ]`，ScopeView 只读 / ScopeManage ACK；consumer 完全由配对 Device ID + `:notifications` 派生，与 `device:<ID>` 的手工已读游标分离。`--unread` 使插件单次查询即可从通知 ACK 开始，GET 绝不标记已读。仅新 Host 声明 `personalAgentActivityNotices`，旧 Host 不进行后台轮询。
- **Android 通知收敛**：复用 Solco Mobile 根部唯一 events socket、已有保活服务和 `expo-notifications`，Activity 变化提示立即重读，30 秒兜底查漏；同一 Assistant 一批事实汇总为一条通知，标识稳定并点击进入该 Assistant Activity history。锁屏通知不含事实/任务正文、Agent Prompt、助手名字或证据；只有通知调度成功才推进通知消费游标。OS 调度失败、网络失败均可重试；ACK 传输不确定时同一运行期间复用 requestId 并避免重复调度。
- **静默时间**：每个 Assistant 只在 Mac 的 `proactivity.quietNow=false` 时投递本地通知；静默期不推进消费游标，解除静默后补读。此路径不修改用户 Activity History 的显式已读状态，也不拼接/伪造 OMP 对话。
- **边界**：这是在线、Android JS/前台服务仍运行时的本地通知和离线补读，不是服务器级 FCM/APNs push，不保证 Android 系统强杀应用后即时到达；通知被 OS 展示不等于用户看到。iOS 还未接此后台投递；跨重启“OS 已调度、Host 尚未 ACK”的极端窗口可能重复提醒一次（at-least-once）；真实手机、Herdr/OMP 的端到端验收仍待完成。协调者生成带证据的自然语言主动总结未实现。

## 继续优化：真实 OMP Coordinator 自然语言阶段汇报（2026-11-05）

- 新增 `coordinator_reports` SQLite v18 outbox 与 `ReportPump`：用户在 Behavior 中 opt-in `reportEveryMin>0`、非 `respond` 且不在 quiet hours 时，只有原 Herdr OMP Coordinator `ready` 并有完整 session/workspace/pane 才可投递。沿已提交 `goal_facts.seq` 做批次窗口，引用真实任务/Run 状态，限制每批至多 12 条 Fact/12 个 Task，并限制文本长度；不启动新 Agent、不绕开审批、不拼接假回复。
- 使用现有 Herdr `AgentSubmit` 把明确标为“Solco 自动汇报请求、不是用户消息”的输入交给**同一个原 OMP Chat**，由 OMP 生成自然语言总结。Herdr 收到仅记为 `submitted`，不声称 OMP 已回复；FactId/TaskId/GoalId 是其可验证的来源。新事实 + 用户配置的时间间隔到来后才再次投递。
- 先持久化 `claimed` 再触碰 Herdr；明确 pre-send 拒绝可延期重试，超时/崩溃/不明结果 → `uncertain` 永不自动重发，并写明示的 Solco FactBlocked 告警（不进入后续报告源，防止自循环）；人工检查原 Chat 后，通过 CLI `assistant report show/resolve` 幂等地 Skip 或确认风险后 Retry。
- daemon 单独 goroutine + atomic fence，Herdr 阻塞不拖住 Task/Goal dispatcher；Archive/Restore 维持不确定状态，永久删除清理 outbox。完整架构、恢复说明与仍未验收事项见 `docs/assistants-coordinator-report-delivery-2026-11-05.md`。
- **未宣称完成**：尚未对真实 OMP/Herdr 会话验收模型自然语言答复与端到端交互，也未实现 Owner-only 的桌面/移动端人工恢复按钮，以及复杂请求到 Task/Goal 的强制 durable trace。

## 继续实施：原 OMP Coordinator 的主动自然语言汇报（2026-11-05）

- **真实对话而非伪造消息**：`ReportPump` 只向 `CoordinatorReady` 且具备完整 Herdr session/workspace/pane 身份的原协调者发送受控 `agent.prompt`；不创建第二个 Agent、不会把 Solco Fact 插入 OMP 转录。提示词明确声明是自动状态请求、不是人的消息，要求按事实序号/任务/Run/证据说明进度、阻塞和下一步，不执行任务或猜测完成比例。
- **唯一事实源**：摘要请求读取当前 Assistant 的持久化 `goal_facts`（有界最新 12 条，携带 seq/Task/Goal）和真实 Task/最近 Run（有界 12 条）；不能证明 OMP 回复完成，`submitted` 只代表 Herdr 确认接收。默认 `reportEveryMin=0` 不提交；启用后尊重 cadence、level、静默期，原会话未就绪时不投递。
- **crash-safe outbox**：`coordinator_reports`（DB v18）先持久化 `claimed` 再发送 Herdr。明确未输入的拒绝才有限次数退避重试；Herdr 提交结果不确定及崩溃后 claim 一律停为 `uncertain`，并向事实账本写一条需要人确认的警示，不自行重发。用户必须检查原对话，再在 desktop/phone 选择 `skip`（不重发）或 `retry`（可能重复），通过 requestId + throughSeq + confirmed 的 CAS/幂等门禁恢复。
- **跨端可操作**：桌面 Assistants 的对话上方显示独立报告状态条与二次确认 Sheet；MyGo `assistants.ResolveCoordinatorReport` 通过 `internal/personalagent.Client` 仍调用同一插件。手机 Host `GET /assistants/{id}/report`（ScopeView）、`POST .../report/resolve`（ScopeManage）从插件获取/更新状态，手机 Activity history 上显示相同告警并用原生二次确认。`personalAgentNarrativeReports` 需要插件实际 `assistant report probe` 成功，旧版插件不误展示。手机响应和恢复命令通过 Zod+Host 金样验证。
- **变更竞态**：Herdr pane 检查可能耗时，发送前再读个人 Proactivity，并在 SQL claim 中检查 Coordinator 的 revision/Herdr 身份、Assistant Active、reportEveryMin 和 level。若静默规则中途变化，不能仅靠旧时间点状态接受后续投递。提交后任何情况不会宣称 OMP 已输出回复。
- **自动验证**：插件多项 ReportPump 功能/崩溃/幂等/清理/竞态测试；Go NativeUI 桥、Host Scope + 请求体拒绝测试；WebView TypeScript 构建及移动 Zod/端测试。真实 OMP/Herdr/手机 UI 端到端仍待实机验收。
- **未完成**：没有 Chat turn 与 Task ID 的强制请求关联、Agent 自然语言输出的端到端验收/证据质量检查、关闭手机进程后依然投递的系统级推送；模型回复由原 Herdr transcript 单独拥有，不保证每次请求都有成功回复。CLI 的 `confirmed=true` 是操作者显式声明，当前还不是无法被 Agent 伪造的权限身份认证，跨端 Host ScopeManage 仍受真实配对设备授权约束。

## 继续开发：原 OMP 自动汇报的实时忙碌门禁与隔离 Herdr 验收（2026-11-05）

- 旧实现只确认 SQLite CoordinatorReady / Herdr startup dialogue：无法知道 OMP 是否正在处理用户 Chat Turn；Worker 通用 `AgentSubmit --wait --until working` 又会在本地极快模型已经回复时误报 `agent_prompt_stalled`（先前隔离证据）。
- 现在 ReportPump 的 `Ready`（真实 Herdr `agent.get`）、`Blocked`（真实 `pane.read`，读取失败 fail-closed）**缺一不可**：精确匹配 OMP kind、目标 pane、`idle` 与 `interactive_ready`；Busy、Trust、未知/不完整状态都保留 `queued`，不 claim、不触发 Model。状态取证在其他只读工作之后、SQLite claim 之前进行；SQL 仍核对原 Coordinator revision/session/workspace/pane + Owner 现有设置。
- 报告专用 `AgentPromptAccepted` 改为无 `--wait` 的 `agent.prompt` 并强制校验 `type=agent_prompted` + exact pane ID。只推进 Herdr **accepted** 水位，不伪称 OMP completed/replied。Worker/Group 原有 `AgentSubmit` 不变。
- 测试先见失败（新增 Busy 场景不具备 Ready gate 的编译错误），随后单测包含 Hero/Herdr 状态不明/不同 Agent/Pane、Pane 读取失败、缺任一回调、被占用后解阻塞、返回收据不匹配。真实沙箱 `python3 scripts/check-herdr-omp-chat-stream.py` **PASS**：Herdr 0.9.5 + OMP 18.16.0 一个真实 Pane/Agent，两次真实 `agent.get idle/interactive_ready` 和无等待 `agent.prompted` 凭据，OMP TUI 输出与 14 帧/6 delta 一致；全程本地 SSE 模型、隔离 HOME；证据见 `docs/debug/2026-11-05-assistant-report-herdr-idle.md`。
- 仍待：**SQLite Assistant report batch 到模型回复 ID 的强制关联**、完整 Assistant→Herdr+OMP→Chat UI E2E、Herdr API 的原子 `if_idle` 请求前置条件及用户发送竞态、实际 Android 应用验收。以上无法由“已收到 Herdr 凭据”替代。
- 有界 Herdr IO：Report 的实时 `agent.get` (5s)、`pane.read` (5s)、无等待 `agent.prompt` CLI (30s) 均有 deadline；探测失败留在 queued，提交不确定仍按原 Outbox uncertain。其他 Worker/Group 的原 API 不变。

## 补齐闭环：自动汇报与 OMP 真实回复关联（2026-11-05）

- 新增 SQLite v19 报告 nonce、replyState、Provider Session/Message ID 与 verified timestamp。Herdr `submitted` 不能冒充完成；读取 OMP 自有 JSONL 的唯一 nonce user turn 及 `assistant stopReason=stop` 最终文本后才证明完成。阻塞下一批直到验证，或人工审阅中断/超时状态；不能仅据空闲、流式 delta 或一个工具调用就断言完成。老版本已提交收据 untracked 并转入 uncertain 由人核对，不伪称验证。
- 回复验证成功会事务性写入 `report:reply-verified` Activity Fact，供现有桌面/手机事件提示与 Android 本地通知消费，但主动汇报源明确排除这些系统自身事件防循环。PC/Phone 均显示 awaiting/verified，不会对真实 OMP Chat 写假消息。独立沙箱真实 OMP JSONL 路径已验收；发现真实 OMP 第一行是 title 而不是 session，并修复前 32 行元数据搜索。
- 完整通过矩阵、边界、未完成的原子 Herdr if_idle、批次对真实 Session 文件的来源认证限制、结构化多 Worker evidence 质量与全业务 E2E：`docs/debug/2026-11-05-assistant-report-reply-correlation.md`。
- 真实回复核验的最后安全门禁：候选文件按本次 Herdr 接收时间（向前容错 10m）筛选，旧历史不占预算；候选数/读取预算溢出、损坏/无法读取、同 JSONL 重复标记均不宣称唯一验证；核验 callback/marker 不存在超过 30m 后交人工处理。真实隔离 OMP + 实际 Go 核验器已再次 PASS。
