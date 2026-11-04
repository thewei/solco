# Solco Assistants 3.0 — Personal Agent Platform 专项审计与实施规划

> **状态：2026-11-04 原 V3 能力审计与实施基线；最新用户决策以 `docs/assistants-messenger-agent-v3-1-plan.md` 为准（MCP-first、Personal Assistant 即智能体、Workflow 框架待办）。本轮仅完善规划，不改运行时。**
> **产品结论**：一位 Assistant = 可长期交流的身份 + 可编辑资料/权限 + 指定的运行 Agent/模型策略 + 独立可管理的记忆 + 例行职责与任务 + 按 Assistant 授权的 MCP 服务。主界面仍为 **Chat-first**；所有设置放在 **⋯ 更多**，不把 Chat 恢复成工作面板。
> **前置文档**：`docs/assistants-chat-first-redesign.md`（当前桌面聊天页面）、`docs/assistants-conversation-product-spec.md`（联系人/私聊/群聊提案）、`docs/assistants-collaboration-plan.md`（未来协作设计）、`docs/personal-agent-go-mvp-plan.md` 与 `docs/shepherdr-2.1-test-parity.md`（Go 运行时与测试基线）。本文件是其上的 **V3 能力层计划**，不重建 Go daemon、SQLite/Herdr、Scheduler、Mobile ChatView 或 AssistantID。手机专项实施见 `solco-mobile/docs/assistants-platform-mobile-plan.md`。

## 1. As-built 审计：已有 / 部分具备 / 缺口

| 用户目标 | 已核实的真实代码 | 缺口与风险 |
| --- | --- | --- |
| 姓名、描述、角色、访问权限 | `plugins/solco-automations/internal/personalagent/assistant.go` 有 Name/Description/RolePrompt/Scope/WorkspaceRefs/DefaultPolicy/Revision，已有 Create/Rename/Enable；`frontend/src/assistants/AssistantsPage.tsx` 的 More → About 仅展示资料及 Coordinator | **缺完整 UpdateProfile/Policy 服务端事务及权限编辑界面**；不能让 DefaultPolicy 的字符串假装 OS 层隔离。 |
| Agent、Provider、Model、Thinking | `coordinator.go` 的 `StartCoordinatorAs(requestID,assistantId,agentKind,model)` 已支持**启动时**指定 kind/model，Task 有 Agent/Model；桌面已有 Agent model/effort catalog；手机 `assistants/[id].tsx` 已列可选 Agent 启动 Coordinator | **设置尚未随 Assistant 持久化**、无可管理的 reasoning effort、缺“当前真实运行配置 vs 下次会话默认值”；模型/effort 的能力差异需来自 Provider，不可硬编码跨 Provider 统一可用。 |
| 每位助理的记忆文件夹与双向维护 | `personalagent/memory.go` 已有 candidate/approved/correct/forget/share/unshare、`context.go` 已有词匹配/预算来源/secret-pattern 防护；桌面 More > Memory 仍是列表，手机仅显示只读 Memory | **没有文件夹投影、Finder 打开、外部 .md 编辑回写、修订冲突/备份/合并机制**；SQLite 与文件不能成为两份平行主数据。 |
| 例行职责 → 自动化 → 任务 → 汇报 | `heartbeat.go` 有 Cron/Event rule、最短间隔、每日 wake budget、审批和防叠加；`taskgraph.go` 有依赖 DAG；`watch.go` 直接实现 pr_watch；原 `plugins/solco-automations/internal/config/config.go` 还有 YAML Automation/Cron/Model/Workflow/Overlap | **两个配置领域关联不完整**：助理 Goal/Heartbeat 与旧 YAML Automation 之间缺稳定的 Routine/AutomationRun 归属、触发人、交付去向与结果汇总链；PR Watch 仍是固定内建类型。 |
| MCP 服务（最新决策） | 旧 Automations 已有 mcp_config 字段，Provider/Herdr 有可复用 MCP 能力 | 建立每位 Assistant 的配置、授权与当前 Session 验证；不引入新 Lua/TS 插件协议、Go Workflow 框架先不选型 |
| 主动性/人性化 | Heartbeat 只在明确批准 wakeBudget 后唤醒模型，且单事务记账；`CoordinatorPrompt` 已强调对话而非全做 Task | 还缺“什么重要/何时主动/是否打扰/完成后回复/跨运行跟进”闭环、定期记忆整理、持续角色风格和按用户反馈自适应；不应通过高频 heartbeat 或无限自主执行制造主动性。 |

**重要已完成改进**：当前桌面 `AssistantsPage.tsx` 已将 Chat 放在主页、More 已是抽屉，属于上一轮 Chat-first 方案的部分落地；手机目前的 `assistants/[id].tsx` 仍以 Conversation 卡片链接和长管理页为主，并非真正嵌入 Assistant ChatView。本轮不能把前者说成尚未实现，也不能把后者误标为完成。**旧规划的状态备注可能过期，以运行时代码为准。**

## 2. 新的信息架构：聊天不变，⋯ 更多有清晰的能力目录

~~~text
Assistant Chat（始终是主页）
 ├─ Header: Avatar / Name / 一句实时状态 / ⋯
 ├─ 对话 + 真实 Task/Automation/Memory/Approval 卡片
 └─ Composer（现有 ChatView 单源）
      ⋯ 更多
       ├─ Profile & Permissions     [编辑姓名/描述/职责/范围/预算/权限]
       ├─ Agent & Model             [Provider/Model/Reasoning/运行中的 Agent]
       ├─ Memory                    [记忆文件夹/文件与条目/新增/修订/整理]
       ├─ Routines & Automations    [例行职责/计划触发/运行记录/结果报告]
       ├─ MCP Services              [已配置 Server / Tools / Resources / Prompts / 权限]
       ├─ Context & Skills          [当前注入资料/工作区/工具边界]
       └─ Activity & Audit          [主动事件/授权/成本/失败/恢复]
~~~

不应在 Assistant Chat Header 暴露高级控制面板；常用权限审批、工作结果和新记忆候选可以放入**有来源的**聊天事实卡。复杂设置进入 Mac Drawer / Mobile 底部 Sheet + 次级路由，关闭后恢复草稿/Scroll/Queue。

## 3. Profile & Permissions：助理资料可编辑，权限是服务端事实

### 3.1 持久数据建议

在现有 `Assistant` 上**增量添加、可回滚 schema migration**（勿重建 ID）：
- `avatarRef` / `displayName` (已有 Name) / `description` / `rolePrompt` / `stylePreferences`（回答语言、详细程度、称呼、主动报告风格）；`publicSummary` 与私有 RolePrompt 分离，以兼容未来 Directory/Peer DM 设计。
- `ownerId`（先为当前 Mac Owner，预留多设备角色而不引入云多租户）；`accessPolicyId` / `policyRevision`（单独权限对象，不用自由文本 DefaultPolicy 冒充 enforcement）。
- `workspaceGrants`（特定 Repo/目录/操作的授权引用）、`peerGrants`（允许谁发现、联系、委派、读某类资料）、`toolGrants`（read/write/network/exec/git/remote/plugin capability），`approvalMode`（ask/review/deny），`budgetPolicy`（tokens/calls/日、周期通知上限）、`quietHours`。
- 所有权限变化带 `requestId + expectedRevision`，审计 old/new 权限摘要（**不记录敏感字段原文**），权限缩小立即影响后续调用；对已运行 Worker 的撤销需要显式取消/中断和不可撤回副作用说明。

**最低限度的 UI**：About 里可修改头像、姓名、描述、职责；Permissions 独立编辑卡包含「可访问哪些 Workspace」「哪些工具能用」「哪些操作需我批准」「允许其他 Assistant 联系/访问哪些内容」「通知/预算」。**默认最小权限，无法验证的隔离必须标明限制。**

**安全现实**：目前 Provider/Herdr Agent 可能用和用户相同的 OS 账号运行，Go API 的 ACL 只能约束官方接口，不能保证一个具备完整 Shell 权限的模型/插件读不到其他本机文件。高权限执行必须继续用明确工作目录、受控工具、隔离 Runner/OS sandbox（另阶段实现），不能在 UI 宣称已完成沙盒。

## 4. Agent & Model：每个助理有稳定配置，但真实运行的 Agent 可更换

### 4.1 建议新增 `AssistantAgentConfig`

~~~text
assistantId
primary: { agentKind, providerId, modelId, effortId, mode, fallbackPolicy }
workerDefaults: { research, coding, review, summarization, each optional overrides }
runtime: { maxConcurrentWorkers, maxSteps, maxTokensPerDay, cooldown }
revision / updatedAt
~~~

- `agentKind` 是 Herdr 能启动的 Claude/Codex/Pi 等 Agent Kind；`providerId` 是可用的 AI 账号/服务来源。**同一个 Agent 类型不总是有任意 Provider+Model 组合**，因此先选 Kind/Provider 后再通过已有 ChatCatalog/Agent capability 列出兼容模型。
- `effortId` 是由 Provider adapter 声明的 enum（例如 default/low/medium/high 或 provider-defined）；无法提供思考强度的 Provider 显示「使用 Provider 默认 / 不支持」，**不要虚构强度或把 Chat 思维内容当作可访问链路**。
- **设置的默认值 ≠ 运行中实际值**：页面同时显示“Current: Claude / model X / effort Y（读取自真实 Session/Provider）”与“Next run default: Codex / model Z / effort High”。修改设置默认作用于**下次 Agent/Worker 启动**；重启当前 Agent 必须提示会话上下文/任务中断风险并要求明确确认；如 Provider 可安全即时切换，必须由其 API 证实后显示成功。
- **正在运行的 Agent**：Primary Coordinator + Worker task runs 按 `assistantId + RunID + Herdr Session/Pane` 准确关联；可以看状态、模型（能确认才显示）、最近动作、打开原 Chat/Terminal/Review、暂停新调度/显式 Stop（旧 Run 不伪称已经停），绝不根据 title 猜。
- 多个 Worker 可以用不同 Provider/Model，主 Agent 专职交流、规划、委派与结果审核；Review 任务可选预置 PR Reviewer Template。无可用 Provider/权限时给明确阻断/修复提示，不自动降级到陌生高权限 Provider。

### 4.2 技术落点

`plugins/solco-automations/internal/personalagent/assistant.go` 增配置实体及修订事务，`coordinator.go`/`dispatch.go` 用**启动快照**选择 AgentKind/Model/Effort，不在任务中途读取新默认值影响正在执行的 Run；`internal/personalagent/client.go`、`internal/nativeui/assistants_bridge.go`、`internal/remotehost/personal_agent.go` 暴露可读/可写配置与运行快照。手机沿用 Host v3 capability-gated Zod，不私存 Credentials/Provider Token。

## 5. Memory Workspace：每位助理一座能打开的记忆文件夹

**推荐单一权威：Go SQLite MemoryItem + 文件夹可编辑投影。** 文件不能和 DB 同时被当作独立的“最终事实”。现有 SQLite requestId/revision/Memory state 是唯一提交点，记忆文件是可读写的本地工作区，所有文件更改经一个 `MemorySyncService` 校验并提交到 Go Core。

### 5.1 物理目录（用户数据目录下，而不是项目 Git Repo）

~~~text
{SolcoUserData}/Assistants/{assistantId}/memory/
  README.md                # 如何维护、本地敏感信息提示
  INDEX.md                 # 当前记忆摘要/引用索引（自动生成）
  facts/                   # 一条 approved Memory 一个 .md（稳定 ID）
    m_abc123.md
  notes/                   # 用户个人长笔记/参考资料，可由 Owner 创建
  inbox/                   # Agent 提议的候选记忆（未批准）
  archive/                 # 过期/版本索引，历史版本以数据库审计为准
~~~

- UI **Memory > Open Folder**：Mac 在 Finder 打开本助理目录；**Mobile 不是打开 Mac 本地 Finder**，而是显示同一远端目录的受控树状列表/Markdown 编辑器/下载或共享动作（仅 Host 授权、仅此 Assistant 子树）。
- `facts/m_id.md` 有稳定 frontmatter：`id` / `assistantId` / `revision` / `kind` / `sourceRef` / `state` / `updatedAt`，正文是 Memory 内容。**一条记忆一个 ID，不通过文件名/文本相似度猜归属**。
- 用户在 UI 中 Add/Edit 可**直接提交 user-authored approved Memory**（由 Owner 明确操作），但写入依然需要服务端版本校验与审计；Agent 只能 propose candidate，除非该 Assistant 明确获得低风险自动整理授权。
- Finder/外部编辑器直接改 .md：文件 watcher 检测到变动 → 校验解析/大小/格式/路径/归属/底层 revision → 生成 diff；**对于外部新增、跨文件批量修改或来源无法确认的改动先进入待确认草案**，不直接信任磁盘事件。Owner 确认后 DB 提交，文件由投影服务原子替换。已授权单项外部文件修改可通过本地确认/规则精确控制。
- **DB / 文件不同事务的崩溃窗口**：DB 事务先提交记录 desiredRevision/outbox；文件 projector 通过安全临时文件+fsync+atomic rename 更新；失败重试，启动 reconcile 扫描；用户在 DB 变更后、文件投影前的编辑有 revision 冲突时**保留双方版本并提示合并**，禁止静默覆盖。watcher 忽略自身投影写，要求幂等。
- 路径权限 0700/文件 0600、拒绝符号链接逃逸/`..`、文件数量/大小限制、secret-pattern 过滤、内容不进入系统日志、备份/可选加密。普通 Markdown 是**明文**，必须告知用户；不能把 API Token 当记忆内容，凭据仍放原 Credential Store。
- Forget 产生 tombstone/撤销显式共享与投影删除，不让下一轮 ContextBuilder 继续看到旧内容；历史运行中的 Provider 已读取过的 prompt **无法撤回**。

### 5.2 主动记忆整理（Memory Maintenance Routine）

不要让 Assistant 每隔五分钟自由编辑记忆，推荐默认：
1. **每天空闲时进行轻量确定性整理**：查变更、重复 ID、候选到期、sourceRef 可读性、存储预算；无模型唤醒时也可以完成。
2. **每周一次（用户可调）语义整理建议**：用指定 summarization/low-cost Model 对本 Assistant 允许可见的 Memory 做去重、合并、过时检测、冲突提示和归档**提案**，标来源与预计影响；**只在审批后的预算/时间窗下运行**。
3. **阈值事件触发**：超过 N 条候选、连续 M 次相同事实、发生用户更正/任务结果或某主题长期未使用时提出一次整理；消抖、去重，跨睡眠补跑遵守 catch-up window。
4. **用户审阅操作**：Approve candidate/merge/forget/keep both；批量变更带 source refs 和快照 revision，删除不可静默。用户可开启“仅低风险重复项自动整理”，但不默认自动删除长期记忆。
5. **Context 检索增强**：先复用当前词匹配与 budget，后续引入关键词+来源/时效/可信度的混合检索；向量索引是可重建的辅助索引，不是记忆真相；持续记录 usedMemoryIds/contextVersion 供用户查看“为什么这样回答”。
6. 本例行整理由**同一个 Go Scheduler/Heartbeat 投递**，不会为 Memory 再开一个 daemon；对 Maintenance 做预算/去重和反自触发（整理结果不能无限诱发下一次整理）。

## 6. Routine：不是一次 Task，而是长期例行职责

**建议产品名称：例行职责（Routine）**。它是用户给 Assistant 的长期使命；**计划任务（Schedule/Trigger）**是“何时触发”，**工作流（Workflow）**是“如何执行”，**Task/Run** 是“一次实际执行”，**Goal/Commitment** 是“为什么做、什么时候算完成”。这样把用户不知道该如何命名的总任务解释清楚了。

### 6.1 最小模型

~~~text
Assistant
 └─ Routine（例行职责/长期总目标）
     ├─ Goal / successCriteria / exitCondition
     ├─ Trigger(s): cron | event | webhook | manual | dependency
     ├─ MCPServerRefs / existing automation/Herdr workflow if supported
     ├─ Policy: scopes, approval, maxRuns, overlap, quietHours, budget
     ├─ AutomationBinding: existing Go scheduler rule/job
     └─ RoutineExecution (每次触发)
         ├─ Initiation: ownerAssistantId, requestedBy, triggeredBy, triggerEventId
         ├─ existing AutomationRun / TaskRuns
         ├─ TaskGraph / WorkerRun (真实 Herdr refs)
         ├─ Result(s) + evidenceRefs + Review/Approval
         └─ Coordinator Summary -> reportConversationId (Assistant Chat)
~~~

**必须明确“由谁触发”**，不要只存一个 `assistantId`：
- `ownerAssistantId`：长期负责这项工作的 Assistant；
- `requestedBy`：最初谁要求创建这个例行职责（Owner、另一个获授权的 Assistant）；
- `triggeredBy`：这次实际是 Scheduler / webhook / Git connector / user / delegated Agent；
- `executedBy`：某次 Worker/Agent 的 Herdr Run；
- `reportedBy / reportConversationId`：由谁总结、向哪段聊天交付；
- `correlationId/idempotencyKey`：一条事件引发的同一套执行只能建立一次。

### 6.2 示例：每日上午追踪仓库、必要时创建 PR

~~~text
Owner: 「每天 9 点检查主分支变化，整理高风险内容，必要时提出 PR。」
 → 产生 Routine Draft（职责、作用域、频率、输出、费用/风险上限）
 → Owner 确认 GitHub MCP/Repo 权限后批准 Routine
 → 原 Go automations scheduler 每天 09:00 触发（时区/DST/睡眠补跑策略固定）
 → Step 1: 通过已有 Git/授权 MCP 增量读取 commits（无新变化 => no-op）
 → Step 2: 如有需要，交给具有 PR Reviewer 职责的个人助理或受控 Worker 生成报告
 → Step 3: 对确有问题的分支生成代码修改 Task（isolated worktree）
 → Step 4: 生成 PR Draft / create-PR 操作需批准（按用户策略）
 → Step 5: 检查结构化结果与证据，Coordinator 总结下一步
 → [Assistant Chat] 今天检查了 4 个 commit；1 个风险，请你审阅 [View Diff]
~~~

- 不应每次都起模型：事件采集、去重、状态比对由确定性的连接器完成，只有改变或需要判断才调用 Agent。
- 一个 Routine 可以关联现有 Task DAG、并行 Worker 与任务依赖；输出返回**同一个** owning Assistant 的 Inbox/Activity/Chat，能继续对话和调整下次行动。
- 异步失败/超时：保留事实和 `uncertain`，状态机与事件收据先入库，外部副作用使用稳定幂等键；外部目标（GitHub PR 创建、评论、合并）必须 readback 确认，不能网络断了再盲目执行。
- Schedule 需要 timezone、DST、catch-up、overlap skip/queue、pause/resume/取消、最大并发、API rate limit、预算/quiet hours、异常告警。**不新增 scheduler**：由原 `solco-automations` 产生 occurrence，Go Personal Agent Core 记录 RoutineExecution/TaskGraph。
- **旧规则迁移**：旧 `automations.yaml` 中无归属的规则先标 `owner=global/legacy`，原样继续执行；用户显式选择「关联到 Assistant」才建立 Binding，不能根据名称或 Repo 猜 Assistant。保持原有频率/运行身份/历史/预算；双调度严格避免，新旧 UI 同一个读写者。
- 目前 Go `watch.go` 的 `pr_watch` 是**硬编码**，优先保持可用性；后续研究如何统一被 MCP 触发的规则与现有 TaskGraph，**现在不开发可安装 Workflow 包、Go WorkflowEngine 或独立 marketplace**。

## 7. MCP-first：统一使用现有 Agent 的工具协议（已调整决策）

- **插件 = MCP Server**；优先复用已有 Agent/Herdr/Provider 所支持的 Tools/Resources/Prompts，只做服务列表、每 Assistant 的 grants、Provider adapter 与真实可用性验证，不造新的 Lua/TS Plugin Runtime。
- **个人助理就是预配置的智能体**，从 New Assistant Preset 可生成初始 Prompt、MCP 默认建议与任务角色；PR Reviewer 等可是一位真实的长期 Personal Assistant，或被其调度的 Worker，不再是 Marketplace 中第三种安装包。
- **复杂 Workflow 的 Go 框架选型延期**；现有 Go Automations 仍是唯一 Scheduler，原 TaskGraph/Herdr 继续执行。明确作为后续技术调研待办，证明确有补偿/持久化 DAG 的缺口以后再做。
- 现有 Automations 配置中的 `mcp_config` 只证明可传 Agent MCP 配置，并不意味着已有统一 MCP Catalog；要精确区分 configured/connected/bound/verified、per-Assistant authorization、Provider incompatibility。
- **最新的 UI、Goal 履约与实现顺序**：`docs/assistants-messenger-agent-v3-1-plan.md` 第 4.2、5～7 节；本节取代旧 Marketplace/Go WorkflowEngine/Lua-TS 设计。

## 8. Proactivity：从 Heartbeat 变成有闭环的主动助理

### 8.1 由三层组成，而不是高频问模型「有什么可做」

1. **感知层（Deterministic Sources）**：既有 PR Watch、Git History/CI、Calendar、邮件/Google（用户授权后）、Task/Run/Approval/Heartbeat、记忆待整理、用户消息。先经低成本规则过滤噪声，保存 event/sourceRef/水位。
2. **判断层（Initiative Engine）**：依 Assistant 使命、Routine、用户偏好、时间窗和预算评估 `relevance / urgency / expectedValue / confidence / cost / interruption`；根据策略选择 `ignore / hold / digest / suggest / draft / execute-within-approved-scope / ask-user`。允许模型提出建议，但**Go Policy 决定能否行动**，并记录“为何主动”摘要供用户查看。
3. **跟进层（Follow-through）**：一个 Initiative 对应 Goal/Routine/Task/Rule/Run 的明确 ID、期限、下次检查、证据、负责的 Agent；结束后由 Coordinator 汇总并投递到 Assistant Chat，失败/blocked 则提醒或询问，完成才标完成。没有证据不假汇报完成。

### 8.2 主动等级与打扰控制

| 级别 | 行为 | 默认 |
| --- | --- | --- |
| 0 · 仅响应 | 用户不问就不主动 | 可选 |
| **1 · 温和提醒** | 后台确定性检查，重要事件/卡片或按日简报，未经同意不启动新高成本工作 | **推荐默认** |
| 2 · 提议协助 | 发现机会 → 出草案/估算成本 → 用户确认后执行 | 用户开启 |
| 3 · 有界自主 | 在已批准 Routine、数据范围、预算、最大并发、风险等级内自动执行，超界必须请求批准 | 逐项授权 |

- 设置 quietHours/timezone/工作日/通知渠道、触发来源白名单、预算/日、月度支出、单项运行上限、频率和 fail-safe cooldown；同一 issue/PR 内容变动去重，避免每次模型 wake 重复打扰。
- **Heartbeat 继续兜底**检查 stuck/needs-you，但不是万能业务 Planner；Routine/Workflow 的具体事件先由各 Integration 低成本检测。任何 Assistant-to-Assistant 消息/插件触发要有 `maxHops/maxFanOut/maxDepth` 和防自触发，兼容未来 Peer DM/Group。
- 把 proactive report 作为**可追溯的 Assistant 活动/真实 Agent 回答**写入 Chat；系统事实卡与 Provider transcript 严格区分，不伪造“助手说过的话”。
- 可解释性：用户点「为什么提醒我？」看到关联的 Routine、触发事件、权限、成本、行动证据与下一步；提供「少提醒」「这种情况自动处理」「不要再做此类事情」即时反馈，反向更新偏好（敏感的长期 Memory 仍需明确授权）。

## 9. 人性化体验：像可靠的同事，不是假装真人

- **持续风格**：每个 Assistant 可设「简洁/自然/详细」「语言」「专业度」「主动程度」「汇报频率」「称呼偏好」。从当前 Conversation + approved Memory 恢复个性，但用户明确指令总是优先于记忆中的旧偏好。
- **自然表达**：优先回应用户的真实问题，再交代进展；少讲技术术语、少贴内部 Task ID，重要事实附来源。短问题短答，大任务先概述方案/不确定性，不必每句都创建 Goal/Worker。
- **时间/主动关注**：记得未完成的承诺、截止时间、重复失败、用户说过“稍后提醒”，在合适时机带上下文问一句，而不是仅说“任务成功”；不在深夜或用户设定静默时打扰。
- **协作式思考**：给出权衡、假设与下一步建议；遇到不确定的高风险动作明确询问；可以展示简短决策摘要/依据，但不承诺暴露模型私有推理链或宣称真正有人类的思想/情感。
- **及时反馈与守信**：用户能看到 Assistant 当前正在执行什么，没结果不编造已完成，不能做的说清楚原因和可选方案。所有“我会持续关注”必须有一个真实 approved Routine/Rule/Goal，否则只能表述为“我可以帮你设置关注”。
- **温度与边界**：友好但不谄媚、不拟人化欺骗，不主动制造情感依赖，也不以“我是你的伙伴”暗示它就是有真实情感或长期在线意识的真人。
- **建议衡量指标**：有用的主动提醒占比、误打扰/取消率、事实错误与虚假承诺率、Routine 完成率、审批后执行成功率、用时与成本、记忆修订/冲突次数、回访会话续接成功率；数据先留在本地，用户同意后才能上报遥测。

## 10. 分批交付与每批验收（先交可见价值，再公开插件生态）

| 阶段 | 内容 / 主要模块 | 验收门槛 |
| --- | --- | --- |
| **V3-0 · 基线/架构/迁移测试** | 确认当前 Chat-first/Desktop/phone 缝、SQLite migration、Host capability、Core/legacy Automation owner；加专项用例 AC-01..AC-20 | 不改线上数据；可重复迁移/rollback；旧 Assistants/automations/history 完整 |
| **V3-1 · Profile + Agent 设置** | Go Profile/Policy/AgentConfig，Desktop More + Mobile 设置，Provider/Model/Effort 来源于真实 catalog，Primary/Worker 运行列表 | 姓名/描述/权限/模型可存可读；running 与 configured 区分；A/B 与设备跨端一致；旧 Agent 无副作用 |
| **V3-2 · Memory Workspace** | 每助手文件夹、Finder/open、UI/手机文件编辑、双向导入/冲突/恢复、Memory maintenance draft | 手工新增/编辑/Agent propose/weekly review 闭环；崩溃恢复无丢失/串线；Forget 生效 |
| **V3-3 · Routine + Automation 统一** | 例行职责实体、触发/工作流绑定、reportConversationId、旧 YAML attribution/migration、安全日志、运行结果总结 | 计划任务触发一次只跑一次；任务输出回正确 Assistant Chat；旧 automations 没有双跑 |
| **V3-4 · MCP Services（新决策）** | 复用 Provider/Herdr MCP；统一 ServerRef/catalog、每 Assistant grants、实际 Session 加载状态；New Assistant Preset 是个人助理默认资料 | Tools/Resources/Prompts 的权限生效，当前 Agent 能用的服务才说可用，不造另一 Plugin Runtime |
| **V3-5 · Workflow 研究待办（延期）** | 记录复杂多步骤案例、对比现有 TaskGraph+轻量 Step Runner 与嵌入式 Go 工作流框架的持久化/恢复/补偿 | 先完成明确的场景研究和 PoC 决策，当前不新增工作流服务或独立调度器 |
| **V3-6 · 主动性/自然对话闭环** | Initiative Engine + Digest + QuietHours + Planner/Reporter + Persona + feedback，Mac/Android 真机回归 | 有用主动发现→执行/请示→证据总结→聊天报告闭环；频控/费用/安全有效；普通 Chat 不退化 |
| **V3-7 · 发布门禁** | 全套兼容、故障注入、性能、旧规则迁移、设备撤销/并发、Doc/UX 回归 | 原 Shepherdr 2.1 追踪门禁仍有效；V3 新用例独立 green；没有虚假的市场能力/安全声明 |

### 新增必测（独立于冻结的 Shepherdr 2.1 测试索引）

| ID | 要求 |
| --- | --- |
| **AC-01** | A/B Assistant 资料、私有 RolePrompt/Permissions 修改互不污染，expectedRevision 冲突拒绝 |
| **AC-02** | 设备权限不允许编辑时 Host/daemon 同步拒绝，UI 隐藏不算授权 |
| **AC-03** | Assistant policy 缩紧立即阻止新 Worker/插件调用；已在途工作有明确取消/风险说明 |
| **AC-04** | Agent Provider/Model/Effort 选择只显示真实兼容选项，不支持的 effort 明确 fallback/拒绝 |
| **AC-05** | 配置变更只影响后续 Coordinator/Worker，运行中的实际配置不被假更新 |
| **AC-06** | Agent 列表与 Run 精确绑定 Herdr refs，不根据名字/Repo 猜身份 |
| **AC-07** | A/B Memory Folder 分离、非法路径/软链接/大文件被拒；Finder 打开的确是当前 Assistant |
| **AC-08** | UI 直接新增 approved item，Agent propose 保持 candidate、批准才能用 |
| **AC-09** | Finder 外部编辑/DB 并发修改不会静默覆盖，revision conflict 有保存双方版本的方案 |
| **AC-10** | DB 提交之后/文件原子投影之前进程崩溃可恢复，不能重复创建记忆 |
| **AC-11** | Forget/Unshare/Correct 下一轮 ContextBuilder 按新版本注入，不读取别的 Assistant 私有事实 |
| **AC-12** | Memory Maintenance 有预算、周期、幂等、防循环，自动不会静默合并/删除用户事实 |
| **AC-13** | Routine→Automation→Run→Summary 归属闭环，所有记录分清 requestedBy/triggeredBy/executedBy |
| **AC-14** | Cron/DST/系统休眠/overlap/catch-up/网络丢响应不重复副作用，unknown launch 保持 uncertain |
| **AC-15** | 旧 YAML automations 关联到 Assistant 是显式操作，迁移不会造成两套 scheduler 重复触发 |
| **AC-16** | MCP Server 注册/授权、Provider Session 实际载入、权限变更审计与撤销有效 |
| **AC-17** | MCP 对未授权 Tool/Resource/跨 Assistant Memory/凭据读取的阻断或明确安全边界，不能只隐藏 UI |
| **AC-18** | MCP 服务断连/超时/恶意工具结果/Provider 不兼容能安全降级、预算与错误可观测 |
| **AC-19** | 高风险 GitHub PR 创建/合并/评论经过准确审批，外部文本 prompt injection 不可提升权限 |
| **AC-20** | 主动消息有真实 Routine/事件/来源、安静时段和反骚扰防重复，用户可以回答“为什么提醒我” |

**回归约束**：独立 Go plugin module 与 root module 都跑 Go tests；桌面 `bun run build` / `bun run build:mobile`，手机共享 Chat/Host golden 经 `pnpm sync:chat-page` / `pnpm sync:fixtures` 真实双端锁定；`pnpm typecheck/lint/test/export:web`、MyGo/macOS WKWebView 与 Android Development Build 通过才可声明功能完成。测试使用隔离 HOME/Herdr/scratch repo，禁止动真实用户工作树；所有预计有副作用的例行职责和高权限 MCP 操作默认需审批/授权。

## 11. 方案决策与不做的事

**最终建议（最新用户决策）**：优先修复创建弹窗、头像/双项新建菜单、手机直达个人/群聊；再做 Assistant 初始 Prompt、MCP、Goal 的主动推进与承诺回响、Routine 聊天创建。原 Marketplace/Lua/TS 与立即引入 Go WorkflowEngine 的路线已取消/延期。权威实施阶段 U0～U5 见 `docs/assistants-messenger-agent-v3-1-plan.md`。

**明确不做**：第二个 Scheduler、第二个 Herdr/Provider runtime、双主数据 Memory DB+Folder、通用不受控 Shell 插件、无权限提示的自动 PR Merge、以 Prompt 声称已实现安全沙盒、为后台事件伪造 Chat 里 Assistant 的回复、自动把每条聊天写进长期记忆、未授权跨 Assistant 共享 Memory。

**实施状态**：本文件是下一批开发/审计合同，不表示 UI、Memory 文件夹、MCP 授权管理或 Routine 已上线。
