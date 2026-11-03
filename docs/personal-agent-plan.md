# Solco Personal Agent — 基于 herdr-projects v1.2.27 的实施规划

> **状态：历史源码审计 / 实施路线已取代。** 2026-11-03。**当前唯一实施规划是 docs/personal-agent-go-mvp-plan.md：以 Shepherdr 2.1.0 为 Go 功能/测试主要基线，不再 Fork/部署 Rust v1.2.27；新产品侧栏 `Assistants` 替换 workboard `Projects`，每个 Assistant 有持久身份/记忆/目标；A/B 交付 Go 原生 MVP，C/D 统一自动化，E 完成 Go 核心与常驻 Host。** 本文 Rust 独占数据、旧 Rust TaskBackend 的路线不再执行；源码证据、Herdr 边界、安全验收与迁移风险仅作历史参考。尚未部署、安装或迁移。  
> **权威约束**：docs/client-product-architecture.md、AGENTS.md；任何冲突先通过新的用户批准与 canonical 架构决议解决。  
> **配套手机计划**：solco-mobile/docs/personal-agent-mobile-plan.md。  
> **术语**：Solco = 产品；Herdr = 现有 runtime；Personal Agent = 个人目标、长期承诺、记忆与执行协调的产品能力，非单独 Python 运行时。

## 1. 结论与已核实事实

### 1.1 选择

**Fork 最后一个仍直接运行在 Herdr 上的 herdr-projects v1.2.27（Rust）→ Solco Personal Agent Plugin。** 复用其任务协调、Coordinator、Watcher、Automations、Autopilot、worktree、回顾/审批/报告和 Herdr Agent 运行机制。Solco Go 层只作 UI / Host API 适配与安全边界；React Native 手机只作控制/审阅端。

不采用 Shepherdr 2.1 整套独立服务作为本期执行内核，也不并行引入 hc 作为另一个终端 runtime；可以从其新源码移植经过验证的机制。

### 1.2 直接审阅的代码证据（2026-11-03）

| 项目 | 已核实状态 |
| --- | --- |
| 本机 Herdr | herdr 0.9.5；herdr session list 返回 default 正在运行，原生会话 Socket 路径在 ~/.config/herdr/ |
| 旧版 upstream | https://github.com/eliasstravik/shepherdr/tree/v1.2.27；Git tag v1.2.27，SHA 4a351c7b8a012baed16987de0ef0ca776c01d009，发布日期 2026-10-28；legacy-herdr-projects 分支 |
| 兼容声明 | v1.2.27 herdr-plugin.toml 的 id = herdr-projects、min_herdr_version = 0.9.3；这只证明声明的版本下限，仍须隔离会话下实际测试 0.9.5 |
| 旧版源码 | Rust Workspace：crates/core（data/herdr）、crates/flow（自动化、协调、任务、Watcher）、crates/board（TUI）、crates/web、src（CLI） |
| 旧版许可 | MIT；实际 Fork 时保留 LICENSE、原版权归属、源码出处与改动记录 |
| 旧版现有能力 | docs/automations.md：Cron、相对周期、任务事件 added/approval/review-failed/failed/needs-you/merged、条件模式、去重/防循环、每小时最多 10 个任务、同一自动化不重叠；docs/autopilot.md：基于目标与节流的协调者唤醒 |
| 旧版不足 | 无任意外部 Webhook 触发；电脑离线错过的旧版 scheduled tick 默认跳过；原生模型以仓库任务为中心，而非完整的个人长期目标/跨应用记忆体系 |
| 新版 upstream | https://github.com/eliasstravik/shepherdr；v2.1.0、SHA 8df9402c63ebd97ae05ed309b0444e2d755cacc6；现为 herdr-core 独立客户端，插件 API 2，旧 Herdr 插件 API 不兼容 |
| herdr-core | https://github.com/herdrdev/herdr-core；核查 SHA b63ecbd1e6b6a7d63fb7bcf40f268930417723ba，独立 hc CLI、HC_SESSION、socket 与状态目录 |
| 原生 Solco | internal/workboard 有 herdr-projects 1.2.x CLI 适配；internal/automations 有 com.whstudio.solco.automations 0.12.x 适配；internal/app/session_launch.go 有共享 Agent SessionStarter；internal/remotehost 已有移动 v3 接口与配对/权限 |
| 手机端 | Expo 54、RN 0.81、现有 automations 页面、host-v3 Zod、HostClient、TanStack Query 与 events WS |

参考代码在本机的**临时研究目录**：/tmp/solco-herdr-projects-research、/tmp/solco-shepherdr-research.gPyMey/shepherdr、/tmp/solco-herdr-core-research。临时目录不是生产依赖、不保证持久，正式 Fork 必须使用上述固定 tag/SHA 单独引入。

### 1.3 herdr Session 与 herdr-core Session 能否复用？

**对当前 Solco + herdr 0.9.5：不能直接复用。** Herdr 使用自身 Session/Socket/API/Pane；hc 使用 HC_SESSION 指定独立 core/终端/存储，hc attach 不能认领现有 herdr 的 PTY 或 Herdr Pane ID。即使进程同为 macOS 用户、CLI 指向同一目录，也不共享运行时。新版 Herdr App 正在围绕 hc 演进，但那是**整个运行时及客户端适配层的迁移**，不是给当前 Herdr 安装一个插件就完成的兼容。

未来若要用 hc，必须先完成：Herdr 新版的正式 core 适配合同 → Solco Herdr adapter 新协议/会话映射 → 旧 Session、历史与 live pane 恢复 → 终端渲染、Agent input、hooks、移动 v3 → 升级/回滚。**未证明旧 Pane/PTY 能保活迁移之前禁止双写、禁止生产切换。** 目前 hc 在 Mac 上没有安装的证据（仅发现 herdr 命令），不以此路线阻塞 Personal Agent。

### 1.4 两种方向比较

| 路线 | 现有 Session 复用 | 可复用的成熟能力 | 主要代价 | 本期 |
| --- | --- | --- | --- | --- |
| Fork v1.2.27 插件 | 是，沿现有 Herdr API 启动真正 Agent（需实机验收） | Coordinator、Watcher、Autopilot、自动化、任务流、Review | 维护 Rust Fork、扩展个人场景 | **选用** |
| Fork Shepherdr 2.1 + hc | 否，与当前 Herdr 0.9.5 隔离 | 较新的 UI、Cards、复合事件、数据层 | 双运行时或全量迁移 Herdr/Solco/手机 | 参考源码，不直接运行 |
| 单纯扩展 Go solco-automations | 是 | Cron/一次性执行、草案和运行历史 | 要自己重造 Coordinator/任务/记忆/跟进 | 不作为长期主线 |

**最脆弱前提**：v1.2.27 与本机 Herdr 0.9.5 的实际插件事件、agent start/wait、Pane 关联可在隔离环境正常工作。若不满足，先做薄兼容修补并跑插件契约，不得为绕过问题转向 hc 第二 runtime。

## 2. 产品与运行时所有权

- **Herdr（唯一运行时）**：Workspace/Tab/Pane、Agent Session、PTY、进程生命周期、Agent 状态与事件。插件不另起托管独立 PTY。
- **Personal Agent Rust Fork（唯一个人任务权威）**：Goal、Commitment、个人记忆与来源、Coordinator、外部 Monitor、自动化定义/调度、Execution/Result/Inbox、审计回执。避免「Rust 任务一份 + Go 任务一份」。
- **Solco Go**：用插件 CLI 与 Herdr 已有接口取快照、发命令、映射 UI / Remote Host；保留现有 SessionStarter、Chat、workboard 适配可用性；不在 Go 重造任务表/调度状态机。
- **Solco MyGo**：Sidebar 中顶层 Personal Agent 入口、Inbox/Goals/Watching/Run Details/Memory Review；Herdr Workspace 树、Chat/Terminal/Git 保留。
- **Solco Mobile**：只从已配对 Mac Host 的 /api/v3 查询和命令；本地缓存不是写入权威；所有实际任务发生在 Mac。
- **现有 Automations 插件**：迁移期维持旧功能，**同一自动化仅一侧启用**；通过验收后可卸载旧插件。保留可恢复的配置/运行快照，绝不默默丢历史。

### 2.1 统一数据模型（由 Rust Fork 持有）

| 实体 | 字段/不变量 |
| --- | --- |
| Goal | id、title、objective、scope（home/repo）、state、version、owner；目标≠一个随意的 AI prompt |
| Commitment | id、goalId、title、successCriteria、exitCondition、priority、status、nextReviewAt、policyRef、version |
| Automation | id、commitmentId、schedule 或 eventSpec、precheck、enabled、maxPerDay、overlapPolicy、lastWatermark、lastFiredAt |
| TriggerEvent | id、provider、resourceKey、sourceRevision、observedAt、evidenceRef；事件重复需有同一键 |
| Execution | id、commitmentId、triggerEventId、idempotencyKey、backendTaskId、herdrSession、herdrWorkspaceId、paneId、agentSessionId、status、startedAt、endedAt |
| Result | executionId、structuredSummary、evidenceRefs、actualCompletionEvidence、nextAction、reviewState；不靠「Agent 说完成了」直接成功 |
| MemoryItem | id、scope、textRef、sourceRef、kind（profile/project/episode）、confidence、updatedAt、version、ttl、consent |
| Approval | id、executionId、action、actionHash、expectedRevision、expiresAt、state；同一次请求只允许接受一次 |
| Delivery | eventId、deviceId、channel、status、seenAt；发送通知不等于对方已看到 |

目标/承诺和插件原有「repo task」不要混淆：Goal 负责为什么长期做，Commitment 负责什么时候重新检查/何时结束，Task 是每次委派给 Agent 的执行实例。一个 Commitment 可有多次 Execution；一个 Execution 只能关联一个真正的 Herdr Agent/任务身份。所有 id 对外均为不透明字符串。

### 2.2 存储隔离

v1.2.27 的 crates/core/src/store/files.rs **确实支持 HERDR_PROJECTS_HOME** 覆盖默认 ~/.herdr-projects。Fork 先把存储根独立设为 Solco 的 Personal Agent 数据目录，并确保由 CLI、hook、keeper、coordinator 子进程一致继承；开发/测试只在临时 HOME/Herdr Session 内运行。新插件 id 建议 **com.whstudio.solco.personal-agent**、CLI 命令建议 **solco-personal-agent**（这两个是拟议名称，不是现成上游命令）。

- Fork 初始版本 0.1.0，保留 upstream attribution，禁止以 v1.2.27 伪装成未改的旧插件。
- 保留现有 herdr-projects 数据目录原样，不触发 upstream 的自动「老安装迁移」逻辑；必须审查 crates/core/src/store/move_home.rs、old_installs.rs、take_over.rs。
- 存储仅插件写入；Go/手机端通过它的 CLI / Host seam 阅读和编辑。迁移和回滚写操作必须有 backup + 可恢复日志。
- 真正创建独立 Fork 时在 plugins/solco-personal-agent/ 存放完整源码+LICENSE+UPSTREAM.md；不要把临时 /tmp clone 当成生产组件。

## 3. 完整执行闭环

~~~text
用户（桌面或手机）
    -> Coordinator（Herdr 中真正的 Agent Session）
    -> Intent Draft（Goal + Commitment + Trigger + Policy）
    -> 用户确认 / 已授权规则检查
    -> Fork 持久化 Goal / Commitment / Trigger
    -> Trigger Engine（优先确定性条件检查，不变化不调用模型）
    -> Rust Dispatcher（幂等执行认领 + 并发/预算限制）
    -> herdr-projects Task（编码）或 Research/Chat-only Task（个人）
    -> 真正的 Herdr Agent（现有 Herdr Session/Pane）
    -> Hook/Watcher/Review + 原生 Agent Conversation
    -> Execution/Result/Evidence 更新
    -> Inbox/桌面/Remote Host 事件 -> 手机通知/审批
    -> 结束 / 再检查 / 进入下个 Commitment
~~~

### 3.1 Agent 收到什么

**Context Envelope 是插件生成的执行 brief，不是新增的 Herdr RPC。** 对普通任务，沿用插件原有的 task add/delegate 和真实 Herdr agent start/prompt；不能自己模拟终端文字输入。必须携带：

~~~json
{
  "schemaVersion": 1,
  "goalId": "g_123",
  "commitmentId": "c_123",
  "executionId": "e_123",
  "objective": "PR 合并后评估升级影响",
  "trigger": {"type": "resource.changed", "resource": "repo#123", "revision": "sha256"},
  "contextRefs": [{"kind": "project_memory", "id": "m_23", "version": 4}],
  "tools": {"allowed": ["github.read", "files.read", "git.diff"], "requiresApproval": ["git.commit", "git.push"]},
  "budget": {"maxAgentMinutes": 15, "maxRunsPerDay": 3},
  "expectedResult": ["outcome", "evidence", "nextActions"]
}
~~~

- 仅注入与目标直接相关的、用户授权的 Memory 引用/摘要；敏感资料、凭据、完整历史对话不能自动装载。
- 外部 PR/邮件网页文本是「不可信数据」，不得覆盖用户策略或被提权为系统指令。
- 插件必须持久化真实 Task ID、Herdr Session/Workspace/Pane/Agent Session 的实际可用字段。不存在时保持空，不从 title 猜。
- Result 取自插件 task detail/review/artifact + Herdr 权威 Agent 状态；运行不确定时为 uncertain，不能自动重放有副作用的动作。

### 3.2 三类任务

1. **编码任务**：继续使用 v1.2.27 worktree / review / merge（强制明确审批政策）；个人目标关联 Task ID。
2. **Research / Chat / 非 Git 个人任务**：用 v1.2.27 已有 research-only/chat-only 能力和普通目录任务支持；Home Goal 使用插件隔离的受控工作目录，不强行创建 Git worktree，禁止由通用任务自动提交/合并。结束时生成结构化报告。
3. **长期监测**：触发器只产生事实事件；Rust Coordinator 在变化后分析并建议/派发 Task。默认条件预检确定性，无变化零模型调用。

### 3.3 触发器规范

- 第一层复用 v1.2.27 现有 cron/interval、board-task event（added/approval/review-failed/failed/needs-you/merged）、if 条件、每小时安全阈值与循环保护。
- 第二层扩展 **external watch** 接口（GitHub PR/Issue、Web page/HTTP ETag、文件变更、Git remote）；仅存 resource、observedRevision、lastChecked、lastError、nextCheckAt、eventId，变化后事件化；可接 Webhook，但 Webhook 只能进经过鉴权的 Mac Host/插件受限入口。
- 外部 API 限流/错过事件时延迟并重试；睡眠后醒来先做单次状态对账，不补发一周旧 tick 的洪水。
- 执行队列应区分 cron overdue、provider unavailable、agent started but result unknown；重复事件/启动失败重试一定要先读取实存执行回执。
- 「自动跟进」不等于不间断唤醒模型。Autopilot 的定期 check-in 保留预算/安静时段/idle backoff，优先事件变化唤醒。

## 4. 个人记忆与 Agent 控制

### 4.1 Memory

优先复用 Rust Fork 内原有 Coordinator 持久化的记忆、规则与报告传递，**扩展独立结构化 Memory 索引**（先文件/JSONL 与可审阅 Markdown，是否 SQLite 由基准测试决定，不要让 Go/手机各存一份）。四种范围：

- Profile：用户明确确认的偏好；可以查看/修改/删除。
- Project：引用仓库里的 canonical AGENTS.md/CLAUDE.md 决定；不复制后漂移。
- Episode：已完成运行的事实摘要+source/evidence ID；不复制全部 Agent transcript。
- Commitment：结构化目标/承诺，是任务事实，不得被记忆整理删除。

ContextBuilder 按目标、权限、来源版本和 token 预算选片段。记忆写入由候选提取→去重/冲突检查→用户确认/特定类型预授权→持久化。每条记忆附 sourceRef、scope、版本、可删除权限；默认不持久化密码/令牌/终端原文。

### 4.2 Coordinator 控制原则

- 个人入口只有一个 Home Coordinator（真实 Herdr Agent Session），可再按仓库委派 Project Coordinator。
- 人类聊天输入由现有 Conversation/Chat（同一真实 session）送达，绝不制造第二份「模拟 Coordinator」对话。
- Coordinator 可以提出 Commitment/Automation **草案**，高风险写操作必须经硬权限 Policy 后才落盘。
- 原 v1.2.27 有些 worker/coordinator 默认可用高权限跳过提示；Fork **必须把安全默认改为受控权限**，不可直接继承危险的 full-access 默认。每个动作最后仍由平台/Provider 权限决定；文档提示不是安全沙盒。
- 远程桌面、剪贴板、解锁属于另一个明确权限域；允许看 Agent 不等于允许 Agent 控制桌面。

### 4.3 命令/适配面（拟新增）

旧的 workboard adapter 接口已实现 List/Details/Add/Delegate/Approve/Coordinator/Automations。新增 Fork 应**保留已用的 CLI JSON 形状**，新增如下拟议子命令（真实实现前需 schema 契约测试）：

~~~text
solco-personal-agent overview --json
solco-personal-agent goals list|show|draft|approve|pause|resume|cancel --json
solco-personal-agent commitments list|show|draft|approve|pause|resume|cancel --json
solco-personal-agent watches list|add|remove|check --json
solco-personal-agent automations list|draft|approve|on|off|run|show --json
solco-personal-agent runs list|show --json
solco-personal-agent approvals list|resolve --json
solco-personal-agent memory list|propose|approve|revoke --json
~~~

参数必须有 scope、opaque id、requestId 和 expectedRevision（变更型）。命令接受 JSON payload 经 stdin，避免 prompts/memories 在进程 argv 和系统日志中泄露。调用方剥掉 HERDR_PANE_ID 等不应继承的伪用户身份变量，但插件执行者身份在 Rust 层单独校验。

## 5. 自动化迁移到同一插件

**目标是最终只有一个 Automation Authority**，不是在过渡期间并排永久运行两个 Scheduler。此步骤必须在 Fork 的实现/测试对等后再执行，不能按名称相似直接迁。

### 5.1 功能差异

| 能力 | v1.2.27 herdr-projects | Solco 现有 0.12.x | Fork 目标 |
| --- | --- | --- | --- |
| Cron/interval | 已有 | 已有 | 保留 |
| board task 事件 | 已有 | 不同事件模型 | 保留 |
| 外部 GitHub PR 事件 | 无任意 Webhook；可定时查询 | 原插件未声明任意外部事件 | 增加 Watch Provider |
| 草案→人工批准 | 上游命令可直接添加/启用 | add --draft + approve | 默认草案、审批 |
| 执行重叠 | 同自动化忙时 skip | skip/queue 可配置 | 至少 skip/queue |
| 错过运行 | >5 分钟默认 skip | catch_up_minutes 可配置 | 可配置限量 catch-up（默认不洪水） |
| 清理与历史 | 每自动化最近 200 firings | 30 天聚合 + cleanup_ttl_hours | 保留历史/保留期限与数据清理契约 |
| Agent 启动与结果 | 插件 Task/Herdr Session | Herdr worktree Agent + run history | 统一 Execution/Result |
| 循环保护 | 自有祖先链 + 上限 10 tasks/hour | 自有 overlap 等保护 | 保留并增加 per-goal budget |

### 5.2 迁移与切换（必须可回滚）

1. **清点，不改变用户状态**：读取现有 internal/automations.Client.List、History；记录每条名称、cron/once、repo、prompt、rationale、overlap、catch-up、TTL、启用状态、last run。导出到用户明确授权的受限本地备份，不进入 git 或日志。
2. **Fork 完整性**：两类自动化用同一个 Runner + CLI 实现，具备草案、批准、暂停、恢复、手动执行、历史、失误恢复，且在隔离 Herdr Session 下通过契约测试。
3. **干跑导入**：导入草案（disabled），产生字段映射与语义差异报告；不能推断外部凭据，不能强制勾选自动执行。
4. **用户确认切换**：暂停旧插件全部触发并锁定创建入口；等待已开始的旧 Run 进入安全结束/可人工检查状态；冻结旧 run cursor。
5. **建立去重水位线**：记录 migrationId、sourceAutomationId、lastFiredAt、targetAutomationId 和特定事件 cursor。新自动化在上线前不得对同一事件重复派发；错过的时间 tick 遵用户原策略。
6. **按条启用**：每条开启新 Fork Automation，先选只读测试任务证明 exactly-once intent + 可追踪 Herdr Agent Session；全部核验后隐藏旧 UI，保留只读旧历史入口。
7. **清理**：验证一段明确的运行窗口（至少覆盖所迁移规则的一次预计触发）后，按用户批准停用/卸载旧插件；不删除其目录与备份。
8. **回滚**：停新触发器与新守护，恢复旧规则启用状态；以水位线跳过已经执行的外部副作用；保留新历史、旧历史可对照。外部不可逆操作不作「自动回滚」承诺。

**禁止**在无停机窗口和去重水位线的情况下让两个插件同时接管同一条自动化。迁移不是一次性“复制 YAML”。

## 6. Sidebar、Desktop UI 与可用性

目标取代的是 **herdr-projects Workboard 的 Projects 任务导航区**，**不是** Herdr 真实 Workspace/Project/Tab/Pane 树。当前 AGENTS.md（2026-10-26/10-31）已有 Workboard 的 Sidebar 与 /projects 页面决策；批准后先更改 canonical 文档，再移除旧导航入口。

拟交互：
- Sidebar 顶层 Personal Agent：一条入口，角标 = Needs You（不重复算同一 Agent 的提醒）；下层快捷切换 Active / Watching / Inbox；Herdr Workspace 树完全保留。
- 主页面：Today/Needs You、Goals、Watching、Activity、Memory；新任务可用自然语言发给 Coordinator，并在 Task Draft 卡片上确认。
- Goal detail：说明目标、退出条件、运行预算、关联 Commitments、最新结果与下一次检查。
- Commitment detail：触发配置、历史检查、最近一次真实 Agent Run、审批/暂停/删除。
- Run detail：报告/引用/失败原因、真实 Workspace+Pane 跳转、在同一个 Chat 会话继续、Agent 输出链接，不复制第二份 Chat 数据。
- Memory Review：展示来源、版本、删除、冲突，用户可纠正；不建静默「全知」记忆。
- Automations：整合为 Personal Agent 的一个筛选视图/详情区，迁移期间旧 /automations 保留，切换完成后再做导航合并。
- Presenter：MyGo native-first；若复用当前 frontend /projects WebView，仍是展示层，只调用 Go bridge；不能直接浏览插件私有文件或打开第二个 localhost 管理服务。

## 7. 手机与 Host v3 边界

详细见配套 docs/personal-agent-mobile-plan.md。Mac 保持 Host 的身份认证、配对、Scope 和连接方式，手机不直接连 herdr、Rust CLI 或 hc。

拟加 capability personalAgent / personalAgentManage / personalAgentApprove。明确与 remoteDesktop scope 分离；没有能力时不显示对应入口。

拟新增路由：

~~~text
GET    /api/v3/personal-agent/overview
GET    /api/v3/personal-agent/goals
POST   /api/v3/personal-agent/goals/draft
POST   /api/v3/personal-agent/goals/{id}/approve
GET    /api/v3/personal-agent/commitments/{id}
POST   /api/v3/personal-agent/commitments/{id}/pause|resume|cancel
GET    /api/v3/personal-agent/executions/{id}
GET    /api/v3/personal-agent/approvals
POST   /api/v3/personal-agent/approvals/{id}/decision
GET    /api/v3/personal-agent/memories
POST   /api/v3/personal-agent/memories/{id}/review
~~~

这些是**提案接口，现有代码没有实现**。Go 仅转发 Rust Plugin 客户端的权威结果，提供显式前置条件检查。Request 需 requestId + expectedRevision；API 使用已有 HTTP/WS 鉴权，Host 不提供任意 shell 或执行通道。/api/v3/events 增加 personal-agent.changed（id/revision/counts，不含正文），手机收通知后重新 GET；断联期间在重新连接后拉快照补偿。

## 8. 分阶段、独立可合并的实施安排

### P1 — Fork 最小插件、保证 Herdr 同一 Session

拟位置：plugins/solco-personal-agent（Rust fork v1.2.27）；internal/personalagent（Go 单一 adapter/capability gate）；少量 internal/nativeui、remotehost 只读路径。

- 从固定 SHA 引入 v1.2.27，改独立插件 ID、binary 和隔离存储目录；保留 MIT + UPSTREAM.md。
- 在临时 HOME + 单独 Herdr session 安装 Fork，启动 Coordinator、delegate research-only 与 Git worktree task、读取状态/结果、关闭/重启检查恢复。
- 复用 workboard CLI JSON，并新增 overview 读接口，Solco 显示只读 Personal Agent 入口。真实 Herdr pane 关联可跳转。
- **成功标准**：同一 Herdr Session 可从 Solco Chat/Terminal 访问插件启动的 Agent；没有 hc daemon/额外 PTY、没有写入 ~/.herdr-projects；退出 Solco 窗口后 Herdr 原生 Agent 生命周期不变。
- **失败处理**：保留旧 Workboard/Workspace UI，Fork 不启用，测试数据保留在隔离目录。

### P2 — Goal / Commitment / Memory / Run 闭环

- Rust Fork 加 Goal/Commitment、Consent、Memory、Execution/Result、审批回执、事件归集；保留原 Coordinator/Task runner，不增加第二协调引擎。
- 用户自然语言→草案→确认→真实 Herdr Agent→报告→再次跟进，至少覆盖 research-only 与 repo task。
- **成功标准**：重启后能恢复 watching/queued/uncertain；未完成任务不会被模型「报告完成」覆盖；同一 requestId 不创建双份 Task。
- Go 与 MyGo 只实现 UI/Adapter，不持有第二份任务 DB。

### P3 — 主动监测与完整 Automations 替换资格

- 复用上游定时/board-event 逻辑，加 conditional precheck/watch providers、预算、安静时段、一次性 & recurring、draft-approve、queue、catch-up、历史 TTL。
- 与现有 solco-automations 做同一组行为契约对照；不达标不迁移。
- **成功标准**：无变化监测零 LLM 调用、PR 状态变化恰一次派发意图、休眠唤醒不重复运行、Cron DST 规则可预测、无批准不执行高权限行为。

### P4 — Mac Remote Host + 手机

- 先在 Go Host 新增 capability-gated 只读 GET + golden fixtures，再手机 Zod/HostClient/Queries/Overview/Details；第二个小变更增加写/审批及版本校验；第三个做系统通知。
- **成功标准**：手机断线重连只做 refetch；不能越过本人设备 Scope；复用同一会话继续 Chat/查看 Run；不会新开第二 Agent。

### P5 — 自动化迁移 + Sidebar 正式切换

- 实施 §5 的数据清点/干跑/批准/对账/单所有者切换/回滚；P4 未完成不阻碍 Mac-only 迁移，但真正影响用户可用入口的动作需单独产品决定。
- 更新 docs/client-product-architecture.md、AGENTS.md、两端导航决议与 docs/CLAUDE.md。
- **成功标准**：新插件是唯一写入的 Automation/Personal Task Owner；现有旧规则/历史有可访问的归档；无第二套同名定时任务；Workspace 真正的 Herdr 树毫发无损。

### P6 — 深化新版本思路（选择性）

- 从 Shepherdr 2.1 参考插件 API2 的扩展性、Cards/Needs You、mailbox identity/replay、remote reconnect、artifact；只移植可通过 P1–P5 合同的片段，保留引用与版权。
- hc 迁移作为独立系统级研究，不成为本期依赖。

## 9. 验证清单与测试门禁

**仓库内最小命令：**
- 桌面：GOTOOLCHAIN=auto go test ./...；GOTOOLCHAIN=auto go tool mygo vet .；GOTOOLCHAIN=auto go tool mygo build；git diff --check。
- Fork：cargo test --workspace --locked（只在独立 fork 落地后；依据其 Cargo workspace）；插件 CLI 契约和 Herdr 0.9.5 隔离运行验收。
- 手机：pnpm typecheck；pnpm lint；pnpm test；pnpm check:fixtures；pnpm export:web。涉及原生功能时另跑 Android Development Build。
- 两端线契约：Mac internal/remotehost/testdata/api-v3 生成黄金文件 → mobile pnpm sync:fixtures → 通过手机 host-v3.contract.test.ts；不可手改 fixtures。

**必须覆盖的非快乐路径：**
- 一条外部事件三次重复送达，只创建一个执行；另一真实 revision 可以再次执行。
- Coordinator 正在回答权限问题时关闭/重启 UI，审批仍只对应活跃请求；过期卡返回 conflict。
- 被撤销手机或只有 view 权限的手机无法暂停/批准/调用写操作。
- Agent 处于 blocked、Provider 额度耗尽、worktree 冲突、Host 断联、电脑休眠后唤醒、插件 keeper 崩溃后恢复。
- 两个原生 Herdr Session 对同一插件 home 的锁与归属（不得跨 Session 偷用 Pane/身份）；正式支持多 Herdr Session 时需定义 owner session 选择和切换。
- 非 Git personal task 不会触发 Git auto-merge；外部资料中的 prompt injection 无法改变工具授权。
- Source data 缺失、无法迁移自动化、双调度误启用时立即停止新触发并保留旧状态。

## 10. 实施前的决定与风险

1. **优先级**：同一 Herdr Session + 一个成熟插件核心 > 引入 hc 的新技术特性；这是当前推荐路线。
2. **产品变更审批**：P1/P2 是旁路新增功能；真正替换 Workboard Projects sidebar、弃用独立 Automations 是新增产品决议，正式落地前先记录到 canonical 架构。
3. **安全默认**：Fork 上游 Worker 高权限策略不能无审查照搬；所有自动写文件、合并、发送外部消息都受硬性规则限制。
4. **MCP/工具能力**：优先使用 Herdr 现有 Agent 的工具和项目环境；要暴露独立 Mac GUI、浏览器、远程桌面时提供明示能力接口与审批，不能把 remoteDesktop 访问权限视为 Agent 授权。
5. **第三方服务**：GitHub 监测复用已授权 gh 或专属只读 token；其他邮件/日历等必须用户主动连接并限制 OAuth scope。P1–P2 不要求外部账号。
6. **成本与响应**：事件优先、模型按需唤醒；同一目标预算与 Agent 并行上限由 Fork 强制，不靠提示词承诺。
7. **范围**：本计划不修改生产 Herdr 数据，不发布插件，不默默卸载旧插件，不自动把所有对话变成任务，不自动迁移已有 Session。

## 11. 交接与后续执行顺序

- 第一份执行 PR：P1 的 Fork 引入与隔离 Herdr 0.9.5 契约测试，只读入口。验收证据记录插件 SHA、真实 pane/session ID、测试数据隔离与无第二 runtime。
- 第二份：P2 Goal/Commitment/Memory 与模型下发上下文/结果关联。
- 第三份：P3 监测和自动化对齐（在此之前**绝不切换旧自动化**）。
- 第四份：P4 Host v3 + 手机联动，独立合并两端契约。
- 第五份：P5 正式导航和规则迁移、用户确认、可回滚切换。

**当前只完成源码调研与文档。** 尚未执行这些 PR、构建 Fork、迁移任何旧数据、安装 hc 或替换工作区功能。
