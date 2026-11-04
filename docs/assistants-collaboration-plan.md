# Solco Assistant Network — 长期助理、协作发现、跨助理通讯与统一调度规划

> **状态：2026-11-04 提案 / 代码审计已完成，本轮仅补规划与验收用例，尚未实现 Assistant Network。**
> **产品交互权威**：`docs/assistants-conversation-product-spec.md`（联系人、human_dm、peer_dm、group、@ Picker、Sidebar 与 UX0–UX6；不需要先建群才能 Peer DM）；**最新用户指示以 `docs/assistants-messenger-agent-v3-1-plan.md` 为准**（个人 Assistant 即 Agent；插件即 MCP；Group 只做聊天；Workflow 框架延期）。本文负责 **Go Directory/Mailbox/ACL/Herdr Skill/Delegation** 的实现领域规范。其他基线：`docs/assistants-chat-first-redesign.md`、`docs/personal-agent-go-mvp-plan.md`、`docs/shepherdr-2.1-test-parity.md`。手机：`solco-mobile/docs/assistants-chat-first-mobile-plan.md`。
> **核心承诺**：同一个长期 Assistant 与人持续对话，能发现愿意暴露的其他 Assistant，主动联络、协作、分派 Worker/自动化任务、追踪结果并向人汇报；管理面板完整可观测和人工调度。**“Assistant 名片公开”不等于“公开记忆/消息/权限”。**

## 1. 审计当前实现与 Herdr Skill 的真实能力

| 现有事实 | 真实源 | 对新增能力的影响 |
| --- | --- | --- |
| Assistant 已是稳定身份 `Assistant{ID,Name,Description,RolePrompt,Scope,WorkspaceRefs,CoordinatorSession,DefaultPolicy,Status,Revision}` | `plugins/solco-automations/internal/personalagent/assistant.go` | 可复用 AssistantID；**目前没有 discoverable/presence/peer policy 等字段**，需做 versioned migration |
| Coordinator 是真正 Herdr Agent，`StartCoordinator` 持久幂等、`coordinators` 保留真实 Session/Workspace/Pane/AgentSessionID | `plugins/solco-automations/internal/personalagent/coordinator.go` | 每位长期助理由它作为会话/协调执行体，不创建第二个 Agent Runtime |
| Task 具有 `assistantId/parentTaskId/dependsOn`，`taskgraph.go` 验证**同一 Assistant** 的父子与依赖、禁止环；真实 Run 关联 Pane 与会话 | `task.go`、`taskgraph.go` | **同一助手拆子任务已存在**；“委派给另一 Assistant”是**新协作请求+受控任务归属**，不允许伪装成现有 same-assistant parentTask |
| Go Memory 已支持 candidate/approved/correct/forget/share/unshare；`VisibleMemory` 只包含所属和**显式共享给本 Assistant** 的 approved Memory | `memory.go`、`context.go` | 不要把目录公开等同于自动共享记忆；已有 item-level share 必须继续遵守 |
| Assistant CLI 提供 `assistant task create/list/show/deps/graph`、`assistant memory context/search`、`assistant event list`、`assistant inbox` | `api.go`、`api_b.go`、`api_c.go` | 可在官方受控 Skill 里包装现有命令，减少重复的运行状态处理 |
| 当前安装的 `~/.agents/skills/herdr/SKILL.md` 能 `herdr agent list/get/read`，`herdr agent start/prompt/wait`，以及 `pane list/read/split`；要求 `HERDR_ENV=1`，用户指定 agent name 或精确 PaneID，并建议 `--no-focus` | 本机 Herdr Skill（已读） | **Herdr Skill 擅长实时 Agent/Pane 调度，不提供长期 Assistant Directory、权限 ACL、离线邮箱或跨重启消息持久化。** 这些必须由 Solco Go daemon 提供；绝不臆造 `herdr assistant message` 原生命令 |
| 当前 ChatView 有稳定共享渲染页面，但 Assistant 的专属聊天绑定仍要按 Chat-first 计划改 | `frontend/src/App.tsx`、`assistants_bridge.go`、手机 `ChatPageView` | 跨助手消息不能直接串入用户↔Assistant 的 Provider transcript；以有明确来源的协作/活动卡投影 |

**安全现状必须写明**：Herdr Skill 本身可以从同一 macOS 用户权限下调用 CLI 读别的 Pane 或 Agent。`plugins/solco-automations/internal/personalagent/safety.go` 明确 `SandboxEnforced=false`，所以由 Go ACL 保护的**官方协作 API**只能保证“走这些 API 的访问受控”，**不能保证任意同 UID Agent shell 无法通过原始 Herdr CLI/本地文件访问其他进程的数据**。真正的强隔离需独立 OS 用户/沙箱/最小化进程权限等另一项安全工程；界面不可宣称已隔离。

## 2. 关系模型：长期助理 ≠ Coordinator ≠ Worker ≠ Automation

~~~text
USER
  │ 长期聊天: My Assistant (AssistantID=a_...)
  ▼
Assistant Conversation (稳定逻辑入口，可在 Pane 更换后恢复)
  │
  ├── Coordinator Agent (真实 Herdr Session/Pane/Provider session)
  │       │
  │       ├── 内部子任务 Task A → Herdr Worker Run A
  │       ├── 内部子任务 Task B → Herdr Worker Run B
  │       ├── 协作请求 → Assistant Directory → Assistant B Inbox
  │       │                                 └── B 可接受/拒绝/提问/报告结果
  │       └── Goal/Rule → 单一 solco-automations Go Scheduler → Run / evidence
  │
  ├── Memory (只用 approved 且属于自己/被明确 share 的条目)
  ├── Context Snapshot (按来源/版本/预算的当前注入)
  └── Activities/Inbox/Approvals/Run evidence (Go authoritative facts)
~~~

- **主聊天永远是用户与该 Assistant 的对话**，不随着子任务数量拆成多个聊天。Coordinator 可以委派 Worker、请别的 Assistant 帮忙、制定/维护自动化规则，结果归档并在主聊天呈现有来源的概要/可操作卡。
- Worker 是有开始/结束的具体任务执行者，不自动拥有长期 AssistantID、目录可见性或读其他 Assistant Memory 的权利；它通过 `taskId + assistantId + runId + 精确 herdr source` 证明是哪个 Assistant 分派的。
- 别的长期 Assistant 是**对等协作者**：不是“子 Agent”，可以拒绝委派、询问澄清、转成其**自己**可授权的 Task；请求方保留 request/correlation，接收方保留自己所有的执行记录。不能偷偷把 B 的任务改成 A 的任务。
- 自动化是同一个 Go 核心的 Goal/Rule/Trigger/Run，不因为“由某 Assistant 提出”就另建 Scheduler。一个 Rule 可关联 ownerAssistantId/goalId/triggerRef，并将结果报告到所属 Assistant。
- Assistant 的会话**长期存在于身份与可恢复来源**，不意味着模型永远不退出/一直占 CPU/保持无限上下文。可休眠/唤醒，源会话如果已无法恢复要明确进行新 session/handoff；长期承诺靠 Go DB/事件/记忆，不能仅靠模型上下文窗口。

## 3. Assistant Directory（助理发现与可见性）

### 3.1 默认关闭公开；主开关 + 高级共享策略

在 Assistant → ⋯ → About / Visibility 中提供：

**「允许其他助理发现我」**：开/关，**默认关**。关掉后普通 Assistant 的目录查询中没有这位助理，既有协作 channel 禁止新送达。Owner 管理员总能在自己的管理面板看见全部。

开启后高级项单独设置：

| 能力层级 | 默认 | 谁可见 / 做什么 |
| --- | --- | --- |
| `discover` 发现名片 | off | 对其他 Assistant 暴露 AssistantID、可编辑名称、职责摘要、能力标签、可联系状态（**不含 Session/Pane 路径、prompt、Memory 全文或密钥**） |
| `read_profile` 读取共享资料 | 仅本人；发现后按安全摘要开放 | 读取 owner 明确允许的公开介绍/能力、负责领域/联系入口；**不等于全部 Chat History/Memory** |
| `receive_message` 接收消息 | 可发现后还需独立允许 | 允许其他 Assistant 发问题/消息；默认建议是需授权的联系人或首次请求审核 |
| `accept_delegation` 接收协作委派 | off | 对方可以请求协作；B 选择接受、拒绝或需要用户批准；**未授权不自动执行任务** |
| `share_memory` 共享记忆条目 | off | 仍按现有 `MemoryItem.sharedWith` **逐条**审批，或另设明确限定范围；即使可发现也不自动共享 |
| `share_activity` 共享任务/进度摘要 | off | 仅开放 owner 明确选择的 Goal/Task 状态及有限摘要，不暴露敏感 Run 日志、文件路径、完整 transcript |
| `allow_direct_contact` 允许对特定 Assistant 的通讯关系 | 默认需授权 | 可以维护 allowlist 和 blocklist，按 owner/target/operation 单独授权；revoke 即时影响后续读取/投递 |

**单个主开关是最简单的产品入口**。高级权限默认保守、可选择 “仅公开名片 / 可接受消息 / 可参与协作” 三个 preset，再展开细项。UI 能在每个 Assistant 的 About 和全局 Network 管理面板统一调整。

**隐私语义**：Private Assistant 不在其他助理的搜索结果中；如果 A 知道 B 的 ID，也不能绕过 B 的 owner policy 直接读取；历史曾分享过的信息无法从其他模型已消费的上下文中追回。Owner 管理面板不隐藏 Private Assistant。

### 3.2 Directory API（拟议 Go CLI/Host，不是现成 Herdr 命令）

- 查询 `assistant directory list [--query]`：**按调用者身份/策略过滤**，返回 stable ID/displayName/roleSummary/capabilities/availability/visibilityVersion。
- 查询 `assistant directory show <assistantId>`：同样经过 policy；不可见与不存在尽量给同一对外错误，避免名字/ID 探测。
- 设置 `assistant visibility set <id>` 与 `assistant grants ...`：owner 或明确管理权限才能写，requestId/expectedRevision、持久 receipt，变动要向已授权协作方发布“目录变更”失效事件。
- 初期目录只在**同一个 Solco Go daemon 管理的 Assistants** 内可见。跨 Mac / 跨账户 federation 不是第一阶段范围；不能把网络发现放到公网、Relay 或 mDNS 暴露私人 Assistant。
- **发现列表由 daemon 权威产生**；Agent 不得直接使用未过滤的 `assistant list` 作为别人的通讯录。现有 CLI 面向 owner 的管理命令与新“以 Assistant 身份调用”的服务 API 必须分开授权。

## 4. 用 Herdr Skill 协作，但必须补上 Go 邮箱与权限

### 4.1 两层通讯模型

**第一层：Go Assistant Mailbox（持久身份与可审计的业务通讯）**

- 每条消息持久记录 `messageId, threadId, fromAssistantId, toAssistantId, kind, subject, boundedBody, createdAt, expiresAt, status, correlationTaskId, relatedGoalId, replyTo, idempotencyKey, policyRevision, attempts`；状态如 `queued/delivered/read/responded/rejected/expired/failed`。
- 类型：`ask`（咨询）、`inform`（告知）、`delegate_request`（委派请求）、`delegate_reply`（接受/拒绝/追问）、`progress`、`result`、`cancel_request`。允许 thread 回复与附件 **引用 ID**，默认不附完整 Chat/Files/Memory。
- 发送由 Go 验证：**调用者确实属于发起 Assistant 当前受信 Coordinator/Worker 身份**、目标可发现或已显式建立授权关系、接收权限、payload 大小、TTL、速率/成本、循环深度。单靠一个 CLI argv 里的 `fromAssistantId` 绝不能算授权。
- 入站消息进入 target Assistant Inbox；离线时持久排队，直到目标恢复且策略允许时才可投递。**创建消息、被 Agent 消费、对方给出回复是三种不同事实**；没有确认不能标 `read` 或 `responded`。
- 回信使用相同 threadId + replyTo，避免自动互相 ping-pong；支持人工看到完整传递链、撤销尚未投递的消息、取消未开始的委派，历史按明确的保留策略清理。

**第二层：Herdr Skill 实时控制（只负责真实 live Agent）**

- 在 `HERDR_ENV=1` 且 Caller 经 Go 精确 Herdr Instance/Session/Pane/AgentSession 绑定验证后，Skill 通过**Solco 授权过的 facade**读取目标允许的 live 状态并请求协作，后端再按真实 `herdr agent get/read/prompt/wait` 或 Go Dispatcher 来操作。**不要传裸目标 Pane 给未授权 Agent 随意阅读**。
- **投递模式需区分**：默认把 peer 消息投到 B 的 Go Inbox，由 B 的已授权 Coordinator 在下一次安全轮次消费；需要立即处理时，由现有 Scheduler/Dispatcher 创建一条带源身份的受控 Peer Work Run。只有确认 Provider/Chat 能正确把该 prompt 标记为“其他 Assistant 请求”而非“用户发送”的情况下，才允许向 B 的常驻 Coordinator 调用 `herdr agent prompt`；否则不能把 peer 消息塞进其用户主聊天、伪装成人类指令或打断当前任务。对已经 working/blocked 的目标不盲目插话，持久排队且遵循配额。
- Skill 的标准工作流是 `directory.list → peer.request/send → receipt → target inbox/delivery → reply/status → source result card`，而不是“看见任何 Agent 就 herdr agent prompt 它”。
- 对目标 live 状态可用 Herdr 的 `agent get/read`，但 **agent read 是屏幕/滚动快照，不等于完整历史 transcript**。历史使用 Solco 已有 Provider/Herdr history 精确 source；被允许时只返回**受控摘要/引用**，不是直接泄漏另一助理所有屏幕输出。
- 当前全局 `~/.agents/skills/herdr/SKILL.md` 只定义 Herdr CLI 用法和 `HERDR_ENV=1` 前提，不含 Assistant Directory、ACL、消息收据、offline delivery；因此应新建并安装 **Solco 专用 `solco-assistant-network` Skill**（拟议路径 `plugins/solco-automations/skills/assistant-network/SKILL.md`），在其说明里调用 Herdr 原 Skill 获得真实生命周期、调用 Go CLI 获得目录/授权消息。**Skill 只是操作手册，不是 ACL/沙箱**；强授权在 daemon 里执行。
- 如果 Herdr Agent 没有可验证的 Solco Assistant caller token/binding，就拒绝跨 Assistant 访问、投递和私有工作读取；不能因 `HERDR_ENV=1` 就假定“当前 Pane=某 Assistant”。令牌短期/绑定 source、撤销后失效，不写入 prompt/日志/持久命令行。

### 4.2 交互示例（目标产品行为，并非现有 CLI 已支持）

1. 用户对“研发助理”说：「请找测试助理一起检查这次发布。」
2. 研发助理通过受控 Directory 查询可发现的“测试助理”及公开能力；不存在或无权限时如实说明。
3. 研发助理发起 `delegate_request`，附任务摘要、范围、预算、sourceRef、审批需求（**不发送整段对话与私有记忆**）。
4. 目标助理收到 Inbox 卡，可以接受、拒绝或发问；接受后在自己的 owner 范围内创建 Task/Run，必要时先走用户审批。
5. 它借真实 Herdr Agent 工作并返回 `result` 附 Run/Artifact 引用；Go 转给发起 Assistant 并更新关联的 Delegation/Activity。
6. 发起 Assistant 在它的**长期主聊天**里总结结果和未决事项，人可以点开委派链、真实 Run、Diff 或重新指派。

未公开的目标/关闭消息接收/拒绝委派必须终止于 `refused`，不回退到无审核的 `herdr agent prompt <pane>` 绕过策略。

## 5. 子任务、跨助理委派、自动化执行的统一模型

### 5.1 子任务（属于同一个 Assistant）

- 现有 `task.go` 有 `parentTaskId` / `dependsOn`，`taskgraph.go` 有归属和 DAG 环检测；不重造 Job scheduler。Assistant 可通过受控 Skill 查询自己创建的 TaskGraph、`task show/deps/graph`、Run status、Herdr `agent get/read` 及 Provider History（只对精确归属的 source）。
- 主 Coordinator 可将一个 Goal 拆为 Research、Coding、Review、Manual 等 Task；独立 Herdr Worker 由已有 Dispatcher 启动。**Worker 需要明确的任务 brief、上下文精简快照、允许的文件/目标、预算、完成定义和回报地址**，不是整个父 Assistant 的聊天历史/全部 Memory。
- 子任务在单一 Dashboard 可以树状展开：Plan → Task A/B → Run / Worker / commit or diff evidence，显示 `queued / running / blocked / review_required / done / failed / uncertain`，保留任务时序和每次尝试的 receipt。
- 如果 Worker 正在运行且授权成立，Coordinator 可以通过 Herdr Skill 查看 Agent 状态/有限输出；如果不在，读取 durable Task/Run 证据；**不能把“Agent 当前是 done”当成任务完成**，仍以 Go Task/Review 状态为真。
- Worker 的任务回报只回到自己 owner Assistant 的 Go Inbox / Task event，然后由 Coordinator 归纳到**同一长期用户对话**；不把 Worker 的原始终端全部流式注入主聊天。

### 5.2 跨 Assistant 委派（对等，不破坏现有 taskgraph）

拟议新的 `Delegation` 对象：`delegationId, parentAssistantId, targetAssistantId, sourceTaskId, targetTaskId?, requestMessageId, state, scope, budget, policySnapshot, deadline, resultRef, createdAt, updatedAt`。状态 `proposed → offered → accepted|declined → queued → running → review → completed|failed|cancelled`。

- A 向 B 提出委派时生成 Delegation + Mailbox request；B 接受后**在 B 自己的 AssistantID 下**创建目标 Task；A 的原 Task 用 `delegationId` 等待一个受控 completion event，避免把 B 的 Task ID 强行放进当前 `taskgraph.go` 的 same-assistant DAG。
- A 可以读取**本次 Delegation 范围允许的状态/结果**，不因此获得 B 其他任务和私有 Memory。B 对本次任务的具体源码/仓库访问仍按 B 的实际授权与用户批准。
- 对方接受 ≠ 自动获得执行敏感命令权限。源策略与目标策略取交集，超范围要提请用户批准，尤其 coding、merge、push、文件写、消息或自动化规则修改。
- A/B 可以对同一 Delegation 往返澄清；每个 message 有明确 correlation 与最大轮数，不能无限递归派活。
- 拒绝、暂停、取消、失联时对原 Task 明确置 `blocked/review_required/failed`，由人工选择重分派、保留/撤销。取消已启动 Herdr Run 不是简单清掉 Go 记录；遵循原有 live process lifecycle。

### 5.3 自动化长期跟进

- Assistant 可以提出：Watch PR、定时整理报告、每日监控构建、每周质量巡检、任务状态定时问询。**永远通过现有 `solco-automations` 的 Go Scheduler/Rules**，不允许 Skill 独立启动无记录 cron/外部常驻轮询。
- 对话里一句「每天下午查看 CI」→ **结构化草案卡**（触发条件/频率、仓库范围、模型/预算、持续期限、成功标准、谁会收到报告）→ 用户批准后才持久启用。
- 实现 `Rule.ownerAssistantId`/`GoalID`/`sourceConversationID`（或权威关联表），保证定时 Run 触发时知道该向**哪个长期 Assistant 的活动流**投递结果，且不会触发无意义的模型汇报。
- Deterministic Watch 可以先不调用模型，只有变化/有效事件才启动 Agent；每次 Run 的开始、完成、失败与审批都进入 owner Assistant Activity/Inbox；用户可在该 Assistant 的主 Chat 查看摘要，也可在 Network 控制中心看全局进度。
- 如果规则向另一个 Assistant 发消息，必须通过相同 Mailbox/ACL/幂等/配额门禁；Scheduler 不因为自己是系统服务就绕过目标 Assistant 的接收策略。

### 5.4 长期会话的连续性与身份

- `AssistantID` 稳定，`ConversationID` 稳定入口，Coordinator 的真实 Herdr Pane/Provider Session 允许改变。每次恢复先精确核对已有 SessionRef，未确认就报 unknown/uncertain，不能猜最近的 Pane。
- **会话连续 ≠ 无限模型上下文**：Assistant 的承诺、目标、记忆、协作线程、未完成任务、最近交接摘要由 Go DB 持久化；Chat transcript 仍由 Provider/Herdr 原权威提供。新 session/handoff 只注入**被授权、可追溯、限预算**的当前上下文摘要，不把所有别的 Assistant 聊天拼进去。
- Coordinator 应定期在安全受控边界生成 `handoff/checkpoint`（AssistantID、目标状态摘要、open task refs、peer threads、approved memory refs、最后活动 ID、source version），上下文过长/进程退出后可以恢复。Checkpoint 是**系统事实记录**，不可伪装成模型说过的文本。
- 长期自主跟进依赖 Go 规则/事件/Heartbeat，而不是不断给模型发“还在吗”来保活。空闲时不烧 tokens，不保持所有助理常驻 Agent 进程。
- 需要有人介入时发通知和 Inbox item；没有用户回复时可以在策略允许范围内独立推进确定性只读事项，但不能自动越权提交外部副作用。

## 6. 调度与防失控规则

| 风险 | 必须的后端规则 |
| --- | --- |
| 助理互相无限聊天或互相委派 | 每条 thread 最大 hop/回复轮数、关联 DAG 防环、`maxFanOut`、`maxDepth`、每日调用/Token/时间预算；超限 Blocked + Needs You |
| 无界群发/消息风暴 | per sender/target rate limit、mailbox quota、TTL、backpressure、idempotencyKey + unique receipt；状态不可假装送达 |
| 断线重试重复任务 | Message/Delegation/Task/Run/Prompt 分层 receipts；至少一次 transport + 幂等效果；`uncertain` 必须精确 readback/reconcile 才能再次派发 |
| 私人资料外泄 | Private 默认不可发现；区分公开简介、共享 Memory item、授权 Task progress、受限引用，owner 可 revoke。系统日志只记 ID/hash/状态，不存敏感正文 |
| Prompt injection 通过他人消息注入指令 | Peer message/Run output 全部当作**不可信数据**，不能覆盖 Coordinator role、Go policy、工具权限；必须过结构化 action approval |
| Target Assistant 已暂停/删除/隐藏/禁联络 | 新消息/委派按当前策略拒绝；Pending 依规则取消或仅保留 owner 可见历史；撤销后不再暴露新信息 |
| 同 UID shell 绕过 | 明确 `SandboxEnforced=false`：不能宣称 ACL 阻止持完整 shell 的任意 Agent 直接访问本地文件/Herdr；高敏感 Agent 需要独立 OS 隔离方可达到硬安全 |
| 运行成本不可控 | Ask/consult 可以低成本直接回答；后台规则先 deterministic checks；模型 Run 必须有额度、时间、频率限制及中止机制 |
| 别人的 Task/Memory 作为事实 | B 接受才创建 B Task；A 只读本次协作 grant 的结果；Memory 必须逐项 share 并有 rev；禁止通过公开名片直接套用别人的 Context |

**默认推荐**：普通 Assistant 默认 Private；Owner 明确开启「允许发现」后只公开名片。开启「可联系」后允许接受受控问询，**跨 Assistant 委派默认需要目标接受**。目标 Assistant 可以依照 Owner 事先批准的 grant 自动回复自己被允许分享的资料；**Agent 自己说“我愿意共享”不能代替 Owner 对私人 Memory、文件和聊天记录的授权**。可给信任关系授予免逐次确认的狭义 grant（例如“研发助理 ↔ 测试助理：可互相咨询公开 CI 进度”），并设置次数、资源范围、费用预算和过期时间；不能给“所有其他助理”一条永久无限授权。

## 7. UI：聊天优先 + 协作调度中心并存

### 7.1 单位 Assistant Chat 与 ⋯ 更多

- Assistant Chat 主页仍用共享 `frontend/src/App.tsx` / 手机 `ChatPageView`。顶部头像、名字、轻量状态、⋯，不变回六标签管理面板。
- 聊天中显示「联系测试助理」「对方已接受委派」「3 个 Worker 正在执行」「自动化下次运行…」「待你审批」等**有出处的系统卡片**；点卡片进入该 thread/delegation/task/run，返回仍在原 Chat/草稿位置。
- ⋯ More 增加 **Collaboration / Visibility**：查看谁能发现我、我能联系谁、能接收什么、和谁分享了哪些 Memory、某次被委派的 thread。上下文页只展示这次确实使用的信息。
- 所属 Assistant 的任务抽屉支持 Task graph → Worker → Herdr live status/Run evidence → Open Chat/Terminal（必须有准确 source）；可直接「请另一位助手帮忙」但不能在 UI 偷偷授权跨域写入。
- 初次创建可以选「个人助理」「代码审查」「测试跟进」等职责模板，但模板只初始化公开说明/私有偏好，不自动赋予广泛通讯和文件权限。

### 7.2 全局 Assistants Control Center（管理面板保留并加强）

**管理中心是调度工作台，不是点击 Assistant 时的默认主页**。Desktop 在 Assistants 标题右侧提供 `管理全部` / `Control Center` 入口；手机提供独立 `Manage Assistants` 次级页面。

~~~text
Assistants Control Center
┌────────────────────────────────────────────────────────────────────┐
│ [All 5]  [Working 2]  [Needs you 1]  [Collaboration 3]  [Rules 4] │
├─────────────────────┬─────────────────────────┬────────────────────┤
│ Assistants / status │ Task Graph & Delegation │ Activity / Inbox   │
│ Release Scout  ●    │ Goal: Release v2        │ B asked A …        │
│ QA Partner     ●    │  ├─ Research worker     │ PR watch changed   │
│ Personal      private│ ├─ QA delegation → QA  │ Waiting approval   │
│                     │  └─ Weekly automation  │                    │
└─────────────────────┴─────────────────────────┴────────────────────┘
~~~

建议模块：

1. **Assistants Directory / Presence**：全部（Owner 视角）、可发现、Private、已暂停、可接收请求；每个 Assistant 的公开开关、read/message/delegate grant 和它当前 Coordinator/待办状态。用户可以从这里开始和任一 Assistant 聊天。
2. **Task Graph / Delegations**：按 Assistant/Goal 过滤的 parent/dependsOn DAG 和跨 Assistant 协作边；Worker/Agent/Run/实际 Herdr Pane、持有锁/等待谁/失败原因、可重新指派/取消/人工审阅。
3. **Automations**：归属 Assistant 的规则、触发器、下次执行、近期 Run、费用、异常、暂停/恢复；深度修改复用现有 Automations 页。
4. **Messages & Requests**：所有 Assistant 间的消息线程、未投递、待接受、已拒绝/过期、消息重试；Owner 调度优先、能按 correlationId 追溯，内容按实际权限脱敏/展示。
5. **Memory / Context / Permissions**：每个 Assistant 独立查看，公开资料、显式共享 Memory、当前 ContextSnapshot、Skills/Workspaces/文件与网络能力；支持 grant revoke 和操作历史。
6. **Global Activity / Needs You**：集中人工审批、成本、卡住/不确定的 Run、Host/Herdr 离线；按 Assistant 跳对应 Chat/Run/Rule 的原始来源。
7. **Scheduler Controls**：Owner 可暂停一位 Assistant 的未来任务、批量审阅草案、调整优先级/并发、重试有限失败任务；**修改既有运行中 Herdr Agent 需明确的独立动作，不把关闭 UI 等同于停工**。

**UI 视觉规则**：管理中心可以使用表格/可折叠 DAG/过滤栏，Chat 必须继续保持对话阅读密度和输入框。不要把 Control Center 常驻到每位 Assistant 的 Chat 上；也不要把所有 Worker transcript 直接刷进主聊天。

## 8. Go 数据层与 API 设计（建议增量）

### 新增/扩展存储（同一个 Go daemon、同一个 SQLite 写者）

- `assistants` 新增 `discovery_enabled` 与 `public_profile_version`（默认 false；历史迁移不能突然公开旧助理）。更细策略在独立表中持久：`assistant_grants`（owner/source/target/op/scope/expires_at/limit/rev）和 `assistant_public_profiles`（允许的介绍/标签/能力快照）。
- `assistant_messages` + `assistant_threads`（真实投递/已读/回信/关联）、`assistant_delegations`（双边协作状态 + source/target task refs）、`assistant_checkpoints`（受控恢复摘要）、`assistant_activity`（可投影给 Chat 和全局面板的 Go 事实记录）。保留现有 `memory_items/tasks/task_deps/runs/rules/events/receipts`；避免复制原 Provider Chat。
- 所有新增表使用单写事务、WAL、schema version migration、唯一幂等键与跨重启收据；索引按 ownerAssistantId/targetAssistantId/threadId/state/updatedAt。暴露变更事件时只带受控 ID/版本/计数，不推送私有内容。
- Owner 总览与 Agent-scoped 目录查询是**两个不同读模型和鉴权入口**。Agent 的协作授权不能因为手机配对获得 manage 级别 owner 权限，手机则仍需现有 Host device scopes。

### 推荐 CLI（拟议，不是 Herdr 自带）

~~~text
solco-automations assistant directory list [--query ...]
solco-automations assistant directory show <assistantId>
solco-automations assistant visibility set <assistantId> <config-json-from-stdin>
solco-automations assistant grants list|allow|revoke ...
solco-automations assistant peer send|inbox|reply|ack|thread ...
solco-automations assistant delegate offer|accept|decline|status|cancel ...
solco-automations assistant checkpoint show|create ...
solco-automations assistant task graph <assistantId>        # already exists
solco-automations assistant task show <taskId>              # already exists
~~~

- CLI 语法须按 `api.go` 的现有 stdout JSON/stdin JSON 规范细化。**不要只凭 `--from <id>` 授权**；必须将 Caller 精确绑定到 Herdr Agent Session/Pane 或由 Owner 受信 UI 端执行，并带短期的 caller capability。
- Skill 通过上述官方 facade 获取过滤后的资料/消息；真正运行 Worker 仍由 Go Dispatcher/Herdr authoritative Runtime 负责，避免 Skill 自己调度导致两个 scheduler。
- Host v3 提议按当前 `/api/v3/personal-agent` 下的 `/directory`、`/assistants/{id}/visibility`、`/assistants/{id}/grants`、`/assistants/{id}/messages`、`/delegations`、`/control-center` 设计；Desktop Go golden 先行，手机 Zod/Queries 后同步。**既有 Assistant DTO/API 向后兼容**；功能由 `assistantNetwork`、`assistantNetworkManage` 等可选 capabilities 宣告。
- 对普通 Agent 的 Host Chat stream 继续使用已有接口；peer thread 数据是 Go Mailbox 与其 Activity projection，不替换 Session/Chat provider transcript；Worker 详情只使用真实 Run/Pane refs。


## 9. 分阶段、可合并、可回滚的实施计划

这个新增工作流**不阻塞已规划的 Chat-first P1**。优先让每个 Assistant 能被用户直接聊天，随后落地 Directory/Peer Inbox、Herdr Skill、委派与 Control Center，最后优化全面协作调度。每批遵循 **先失败测试 → Go 最小实现 → Host golden → 两端 UI → scratch Herdr/移动端 E2E**。

| 批次 | 建议实现 | 必须通过的独立验收 |
| --- | --- | --- |
| **N0 · 协作合同与安全审计** | 固定 caller/target 身份、visibility 默认值、公开 Profile schema、消息 envelope、Grant、生命周期和成本边界；对现有 Herdr Skill 能力做只读实测/版本兼容矩阵 | 证实 Herdr Skill ≠ Assistant directory/mailbox/ACL；不打开任何用户 Assistant |
| **N1 · Directory / Visibility** | Go schema migration + Agent-scoped directory.list/show、Owner visibility/grants；新的 `solco-assistant-network` Skill 初版只读 discovery | 默认所有旧 Assistant private；A 看不到未暴露的 B；B 主动公开后 A 只能看到允许的资料；隐藏即时生效 |
| **N2 · Peer Mailbox** | Go thread/message/receipt/inbox/ack、对等 ask/reply；Herdr live delivery adapter，离线队列、接收拒绝、限流、防循环 | A→B 问询、B 回复，A 的 Chat 显示带来源状态卡；B 停机后恢复只投递一次；禁止给不可联系 B 发送 |
| **N3 · Subtask / Delegation** | 继续用现有 task graph、Herdr Agent get/read/wait；新增 cross-assistant Delegation/accept/decline、目标自己的 Task/Run、结果关联 | A 拆两个 Worker 任务，并把 QA 部分委派 B；A 只能看它被授权的 B 结果；Task DAG 不跨 Assistant 伪造 parent |
| **N4 · Automations / Continuity** | Assistant-owned Goal/Rule、定时/事件触发归属、持久 checkpoints/交接、成本上限/heartbeat | 用户允许后自动化在后台执行、汇报回正确长期 Assistant；Mac/Coordinator 重启后没有重复派发；空闲不启动模型轮询 |
| **N5 · 管理中心/手机** | Desktop Control Center：目录、全局 TaskGraph、delegation/threads、Rules、Inbox、审批、Memory visibility；Mobile：助手 Chat 的 Collaboration/Visibility/Tasks/Automations Sheet + 独立 Manage | Chat 是默认入口，管理页是次级视图；Mac/Android/多设备修改可见性后权限生效，操作可追溯 |
| **N6 · E2E / 可靠性 / 安全闭环** | 故障注入（断线/崩溃/重播/过期/权限变更/多级委派），跨端 golden、长期会话恢复、性能与数据迁移演练 | 没有双启动、泄露、无限回环、伪完成、错发给其他 Assistant、错误恢复；运行中 GUI 退出能力按真实 Host capability 降级 |

**合并顺序**：可以在 Chat-first 的 P1 之后开始 N0/N1；N2/N3 分步推进，N4 可与 P3 活动卡结合；N5 与 Chat-first P2/管理抽屉合并。不要在尚未解决 Assistant-scoped Chat 绑定时启用自动跨 Assistant prompt 投递。

### 文件级交付建议

- Go 数据与业务：`plugins/solco-automations/internal/personalagent/{assistant,store,api,coordinator,task,taskgraph,memory,context,events,rules}.go` 扩展小文件 `directory.go`、`grants.go`、`mailbox.go`、`delegation.go`、`checkpoint.go`、`activity.go` 和各自 `_test.go`，避免巨型 `api.go`。
- Skill：`plugins/solco-automations/skills/assistant-network/SKILL.md`（**拟议创建**）与现有 `~/.agents/skills/herdr/SKILL.md` 的运行时能力配合；安装/升级由 Solco 自有 skill wiring 管理，**绝不能在用户 home 中就地改动 Herdr 官方/用户原有 Skill 文件**。
- Root Go bridge：`internal/personalagent/client.go` 加受控 CLI 请求与 DTO；`internal/remotehost/personal_agent.go` / `server.go` 加 Owner-scoped Host API、guard、capabilities、events/golden；既有 `internal/hostd` 的最小 Agent Directory/Inbox 只在经审计确认安全读写后暴露。
- Desktop Chat + 管理：`frontend/src/assistants/AssistantsPage.tsx` 与 Chat-first scoped transport 对接，在 Chat 中增协作事实卡/More 的 Collaboration、Visibility；新增独立 `AssistantsControlCenter` 页面或 Drawer；`internal/nativeui/assistants_section.go` 头部增加「管理全部」。
- 手机：`solco-mobile/src/app/assistants/{index,[id]}.tsx` 保持 Chat-first，增加 Collaboration/Visibility、TaskGraph、控制中心二级路由；`src/contracts/host-v3.ts` 与 `src/lib/host-client-v3.ts`、`src/features/host/queries-v3.ts` 仅按 Mac Host golden 添 DTO/query。

## 10. 产品/安全/跨端验收清单

以下是**新增 Network 场景**，不属于 Shepherdr 上游固定的 1,193 条测试，也不应挤掉现有 `CX-01..CX-15` Chat-first 验收。N0 创建单独追踪台账，实施时并入 Solco 自有最终 gate。

| ID | 用例及可核实结果 |
| --- | --- |
| **NW-01** | 新创建和旧迁移 Assistant 的 `discover` 默认为 false；Owner 管理中心仍能看全部，其他 Assistant 无法列出 private |
| **NW-02** | B 只打开“允许发现”：A 只能看 B 公开的名字/职责/能力，不能看 B Chat、私有 Memory、内部 Run 或精确 PaneID |
| **NW-03** | B 授权 A 查看指定公开进度/分享某条 Memory 后，只能读被授权范围；撤销后下一次读取立刻拒绝，已消费历史有准确提示 |
| **NW-04** | A 知道 private B 的 ID 也不能通过 Agent-scoped directory.show 绕过；未授权与不存在对 Agent 返回一致拒绝 |
| **NW-05** | 用伪造的 `fromAssistantId`、其他 Herdr PaneID、过期 caller token 或同名 Agent 假冒 A 发送消息被拒绝 |
| **NW-06** | `HERDR_ENV=1` 的真实 A Coordinator 经 scoped facade 发现、询问 B；出 Herdr 环境的普通进程不能借同一 Skill 声称 A |
| **NW-07** | B live/offline/disabled 三状态下的收件行为与 delivery/read/response receipt 一致，离线时不会假称已读或已回答 |
| **NW-08** | 断线/丢回包/daemon 重启后重复 messageId/requestId 不会导致 B 重复执行相同委派或接收多份实质任务 |
| **NW-09** | A 与 B 互相调用时的环路、maxDepth/maxFanOut、TTL、budget 限制能终止风暴并向 Owner 告警 |
| **NW-10** | B 拒绝或仅提问，A 的 Task/Delegation 停在可理解状态，不把拒绝当已完成 |
| **NW-11** | A 向 B 的委派只能在 B 接受后产生属于 B 的 Task/Run；A 不能改 B 的 Task 或拿到无授权的 Provider transcript |
| **NW-12** | A Coordinator 拆子 Task，能通过已有 task graph、真实 Herdr Agent get/read 和精确 Run 查询进度；没有 Pane 时只读 durable evidence |
| **NW-13** | Worker 只获得其 brief/授权 Context，不因 parent Assistant 的公开或协作而拥有所有 Memory/Workspace/目录写权限 |
| **NW-14** | 自动化 draft 必须得到原有 Go 审批才启用；后台 Run 的结果关联到正确 Assistant/Goal/Run，空闲时不进行 LLM poll |
| **NW-15** | Coordinator 被关闭/重启/换 Pane，Assistant 稳定 Chat 入口、Task/Memory/Peer threads/checkpoint 保持一致，不创第二份 transcript |
| **NW-16** | B 取消公开或撤回接收委派：已授权的未来送达立即拒绝，Owner 的审计历史不被静默擦除 |
| **NW-17** | 假的 peer message、恶意 Run output、工具文本不能改写 Prompt 规则或绕过 Go Approval；日志不存 Memory、Token、Message 正文 |
| **NW-18** | Control Center 展示 Directory/TaskGraph/Delegations/Rules/Inbox 的 owner 真值；可从卡跳回 A/B Chat/精确 Run，返回保留草稿和视图 |
| **NW-19** | Mac/Android/双手机同时对 visibility/grant/approval 作冲突写：expectedRevision/actionHash 生效，失效权限不再可写 |
| **NW-20** | GUI 退出但 resident Host 仍在线时，手机能查其真实支持的 Directory/Inbox 能力；没有 Chat capability 则不能假装远程实时聊天 |
| **NW-21** | 长会话的 approved Memory、checkpoint、pending tasks、Peer threads 与真实 Provider source 在上下文压缩/重启后可恢复，无串到别的 Assistant |
| **NW-22** | Agent 拥有无隔离本地 shell 时，产品明确不宣称 OS 级权限隔离；不得以 Go facade 的“拒绝”测试冒充完整同 UID 进程隔离 |

**跨仓回归**：Go root 与插件子 module 分开 `go test ./...`，必要时 race/fault tests；Desktop `bun run build` + `bun run build:mobile`，Mobile `pnpm typecheck/lint/test/check:fixtures/check:chat-page/export:web`。**只有 Mac Host 生成 golden，手机镜像同步**；真机 Herdr E2E 必须隔离 HOME/测试 Session/scratch repo，避免触碰真实工作区和已有 Assistants。

## 11. 明确的非目标

- 第一阶段不做云端公开 Assistant 商店/陌生人发现、跨用户/跨 Mac Federation 或任意 Internet Agent 寻址。
- 不允许未经授权的自动私聊/递归群聊；Assistant 公开开关不自动允许其他 Assistant 操作 Git 或读取聊天。
- 不新增 Herdr 自身没有的“assistant send”伪命令，也不要求修改 Herdr 核心协议；如果未来选择扩展 Herdr，必须先确认官方 API 与版本兼容测试。
- 不新增第二份**同一 Provider transcript**。但新产品规范确定 **Peer DM 和 Group Chat 是用户可打开的一级会话**，因此必须有独立且唯一的 Go ConversationStore 持久化多作者的真实协作消息/参与者/未读/收据；它是新的通讯数据域，**不是**复制原 Herdr 个人 Chat。Go Mailbox/Delegation/Activity 与此 ConversationStore 关联，不得反向篡改 Provider transcript。
- 不承诺每个 Assistant 的模型永久驻留；长期身份/记忆/承诺/任务才是连续性来源。

**实施关键路径**：产品视图与关系以 `docs/assistants-conversation-product-spec.md` 的 **UX0–UX6** 为主——先 UX1 人↔Assistant 聊天、UX2 @Directory、UX3 Peer DM，再 UX4 Group、UX5 Task/Rule 卡、UX6 Control Center；Go 执行用本文件 **N0–N6** 同步。Chat-first P1 的安全 Session binding 是基础，不把 Group 当 Agent Runtime。

