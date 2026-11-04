# Solco Assistants — Conversations / Contacts / Groups 产品交互规范

> **状态：2026-11-04，产品设计与实施方案，尚未上线。** 本规范是 Assistant 聊天产品的**用户体验权威**；Go 领域与安全详见 `docs/assistants-collaboration-plan.md`，Chat-first 详见 `docs/assistants-chat-first-redesign.md`，原 Go/Herdr 核心与 Scheduler 不变。手机配套 `solco-mobile/docs/assistants-chat-first-mobile-plan.md`。
>
> **定位**：Solco 是一个长期助理通讯与协作工具：**Assistant 是联系人且是具有初始提示词/记忆/MCP/Goal/Routine 的 Agent，Conversation 是沟通场所，Task/Worker/Automation 是执行能力。** 不必建立群聊，两个 Assistant 就能发现彼此并形成持久一对一对话；Group 只是几位助理需要共同看见讨论和交接时的独立场所。**最新交互修订与实施优先级以 `docs/assistants-messenger-agent-v3-1-plan.md` 为准**。

## 1. 竞品研究（2026-11-04 核对官方文档）

| 产品 | 核实的产品机制 | Solco 应借鉴 |
| --- | --- | --- |
| [Grok Bot](https://docs.x.ai/grok-bot/bots) / [协作](https://docs.x.ai/grok-bot/chat-and-collaboration) | Bot 是有独立职责/上下文/长期会话的联系人；可选 Primary Bot；Pin/Sections/Hide。创建 Bot 后立即聊天；2–6 位 Bot 可以建群，支持 `@`、Reply、Bot↔Bot 异步消息与任务交接；群与私聊并存 | **Primary Assistant、会话列表、联系人的角色、1:1 + 群聊、@ 指派、隐藏但不中断工作** |
| [Slack AI Agents](https://slack.com/help/articles/33076000248851-Work-with-AI-agents-in-Slack) | Agents & tools 用于发现；可在 DM 中直接交流，也可加入频道后 `@` | **通讯录和最近会话是两个不同列表** |
| [Poe Group FAQ](https://help.poe.com/hc/en-us/articles/19944206309524-Poe-FAQs) | 群里 Bot 默认被 `@` 才回应，自动回应须显式配置 | **默认不唤醒所有群成员，避免循环和 token 浪费** |
| [Microsoft Teams Bot](https://learn.microsoft.com/microsoftteams/platform/bots/bot-basics) / [group](https://learn.microsoft.com/microsoftteams/platform/bots/how-to/conversations/channel-and-group-conversations) | 区分 1:1/group/channel，`@` 有真实 mention entity/participant，而不依赖字符串匹配 | **Mention ID、参与者身份、群消息授权** |
| [OpenClaw multi-agent](https://docs.openclaw.ai/concepts/multi-agent/index.html) / [sessions](https://github.com/openclaw/openclaw/blob/main/docs/concepts/session-tool.md) | Agent 身份与 Session 分离，提供 sessions_send / sessions_spawn 以及跨 Agent 可见性约束 | **同级 Assistant 的 DM ≠ 下属 Worker 的执行任务** |

取机制，不复制软件架构：Solco 仍是 macOS Go/MyGo + React Shared Chat + Expo RN、唯一 Herdr Runtime、唯一 Go Automations Scheduler。Grok 的云电脑、团队授权模型和托管环境不属于此项目已具备能力。**产品上比 Grok 更明确设置通讯权限，不仅靠自然语言请求“不要互相聊天”。**

## 2. Assistant 如何定义：明确长期角色，而不是一轮任务

每位 Assistant 有稳定 `assistantId`、头像/显示名/标签、`mission`（它负责什么）、`publicSummary`（可对其他人介绍的职责）、`expertiseTags`、长期 `rolePrompt` 和已批准 Memory、可使用的数据源/Workspace/Skills/工具、运行策略/审批边界、关联 Goal/Rule、真实 Herdr Coordinator、状态。

**什么时候新建 Assistant？** 当新的长期职责具有独立数据源、记忆、审批边界、工作风格或定期任务时；仅是当前项目的一次实现/研究/测试，则给已有 Assistant 建 Worker 子任务。Assistant 可不关联 Git 仓库，不以 Project/Pane/Provider Agent 为唯一身份。

**轻创建**：`+ → New Assistant` 仅需名字，可选头像与一句职责；保存即进它的 Chat（没有真正 Agent session 也保持 Chat 壳）。在第一段聊天里说明工作、权限和偏好，Assistant 提出的长期角色/记忆先是候选，得到 Owner 同意后才存入 approved Memory。后续在 `⋯ → About / Responsibilities / Memory / Skills` 调整；不做冗长的“创建 AI 工作流”向导。

**Primary Assistant（主要助理）**：最多一个，置顶并可主动协调；可以将请求转成联系/委派/自动化的草案，但**不是超级管理员**，不会自动读取其他 Assistant 私有 Memory。Owner 可随时切换 Primary；原 Assistants/Chat/Tasks 不删除。团队 Assistant 通过使命/专长搜索，无需一个 Primary 才能互相通信。

## 3. 独立的联系人与会话：三个聊天类型

| `conversation.kind` | 参与者 | 用户看到什么 | 真正数据源 |
| --- | --- | --- | --- |
| `human_dm` | 当前 Owner + 一个长期 Assistant | 像正常 ChatGPT/微信的持续私人对话；ChatView、草稿、文件、问题卡/工具输出 | 原 Herdr/Provider Chat 事实；Go 仅存会话索引/绑定/未读/活动 |
| `peer_dm` | Assistant A + Assistant B；Owner 可查看/精准插话 | A↔B 的长期一对一协作会话，双头像，异步问答、回报、线程 | 新 Go 协作 Message Store/Mailbox，真实 Agent Run 仅通过 sourceRef 关联 |
| `group` | Owner + 2–6 个指定 Assistant（MVP 限 4） | 多头像群聊：各助理有自己名字、@、Reply、成员与任务/自动化卡 | 新 Go Group Conversation Store + Herdr 权威执行引用 |

**重要：不需要先创建群才能发现/私聊。** Directory 只解决“我能知道谁、联系谁”；Peer DM 解决 A 和 B 的私聊；Group 解决“大家在一个地方共同看到同一主题”。Assistant 拥有一个默认 human_dm，但一个 Assistant 可以参加多个 Peer DM/Group；加入 Group 不把它变成 Worker，退出 Group 也不删除它的身份/私聊。

**每对 Assistant 默认一条长期 peer_dm**，每次委派/问题以 `threadId/correlationId` 隔离主题和结果，避免两个 Assistant 就每个小需求建立一整套新对话。用户可选择「独立主题对话」作为例外。Owner 查看 Peer DM 默认为旁观，用户插话需选择「对 A / 对 B / 对两者」且署名为自己，**不能伪装成另一个 Agent**。

**群上下文不等于所有人记忆合并**：每个成员只能取得当前 group 授权可见的消息/资料引用，其他 Assistant 的私聊、Memory、Workspace、Secrets 不自动共享。群成员入群后默认只见加入后的消息；历史可见范围由 Owner 显式扩展（已经被模型读过的内容不可承诺撤回）。

## 4. 侧栏、手机首页：一个通讯工具应该怎么呈现

### macOS 侧栏（保留现有 Agents / Workspace 全局结构）

~~~text
Agents                         ← 原 Herdr 运行中的 Agent
Assistants                  [+]
  [全部对话] [联系人]
  ★ 我的助理                  · Primary
  ◉ 发布协作群                 · 3 位助理
  ● 研发助理                   · Working
  ● 研发 ↔ 测试               · 2 条新消息
  ● 测试助理                   · Needs You
  [新建群聊] [管理全部]
Workspace                      ← 原 Herdr workspace/tree
~~~

- 「全部对话」是**私聊/Peer DM/Group 混合排序**，置顶→最近活动与未读；每条都有头像、名称、可信最近一条摘要（无来源则只显示状态）、时间、未读/Needs You；双头像代表 Peer，叠头像代表群，单头像代表长期助理。
- 切换「联系人」显示拥有的全部 Assistant（Owner 能看私人/隐藏的联系人），提供搜索/专长过滤、公开/可联系/忙碌标签；可直接点头像私聊、邀请入群或发起咨询。
- 行更多：Pin/Mute/Hide/Unhide/Archive；**Hide 不会停用 Assistant、不取消 Rule/Task**。不要为每个 Worker/自动化创建新侧栏行。
- `+` 是一个仅含 **New Assistant** 和 **New Group** 的真实下拉菜单（桌面和手机一致）；新建单聊/Consult 由选择联系人或聊天内 @ 完成，不在此处增加第三个入口。「管理全部」进入独立 Control Center，而不是替代聊天主页。
- 侧栏 **Workspace 沿用方形字母 Tile，个人 Assistant 默认使用圆形首字母头像**，群聊用叠成员头像；所有头像/名称/状态/未读使用同一 AssistantID/GroupID 投影，允许用户上传头像。首字母缺省、重新命名、头像加载失败必须安全降级为稳定颜色与字形。

### macOS 对话内容

~~~text
┌─ ◉ 发布协作群 · 研发/测试/审查 ────────────── [成员] [⋯] ┐
│  You: @研发 组织发版，@测试 做回归，@审查 检查阻断项。    │
│                                                          │
│  研发助理: 我负责构建，已交给 Worker。                   │
│    [Task #123 · 进行中 · 查看 Herdr Run]                  │
│  测试助理: 我开始验证构建。                             │
│    [协作请求已接受 · 查看研发↔测试私聊]                 │
│                                                          │
├──────────────────────────────────────────────────────────┤
│ [+] Message · 输入 @ 指定助理                    [➤]   │
└──────────────────────────────────────────────────────────┘
~~~

聊天 Header 只留头像、名称、状态/群成员、`⋯`。原 Shared Chat 的 Markdown/Composer/Dock/Thinking/Files/Voice/Queue 资产保持一份，**不复制 ChatView**；但 Peer/Group **按实际 capabilities** 显示可用工具（没有真实 Provider Stop/Question/Voice 语义时不展示无效按钮）。群消息有明确作者+发送时间、Reply 引用卡、工具/任务事实卡；不能把 Go Activity 冒充 Agent 的 Provider 发言。

### Android/iOS（Expo RN）

- `/assistants/index` 改成会话列表，上方 `Chats | Contacts`，Groups 作为过滤，不增加一个重量级底部 Tab。`+` **只列 New Assistant / New Group**；点击任意个人助理直接进单聊，点击群聊直接进多人 Chat。
- `/assistants/[id]` 保留按 assistantId 深链，解析成 owner↔该 Assistant 的 human_dm；新 `/assistants/conversations/[conversationId]`（拟议）用于 Peer/Group 与所有 Conversation 类型统一渲染。详情继续使用 `src/components/chat/chat-page-view.tsx` 共享 ChatView，差别是 scoped transport。
- 长按列表 Pin/Mute/Hide，群聊 Header 点开成员/群信息/共享范围；More Sheet 保持 Memory/Tasks/Context/Automations/Collaboration 等能力。切换/返回不丢 draft/scroll/queue。
- 在手机上观察 A↔B 的 Peer DM 也要能看到双方头像与真实身份；Owner 插话时必须选择发给谁。Mac GUI 退出而 Host 不具备 chat capability 时照实显示离线/只读/草稿保留。

## 5. `@` 和 Reply 的产品级语义：三种场景不同

| 当前会话 | 用户输入 | 实际动作 |
| --- | --- | --- |
| 和 A 的私聊 | 「请 `@测试助理` 检查这次发布」 | 当前 A 联系 B，建立/复用 A↔B Peer DM，展示分享范围与通信状态；**不会把 B 加进 A 的私人聊天** |
| 群聊 | 「`@测试助理` 检查构建」 | **只唤醒被 @ 的 B**；其他成员能看见消息但不默认启动模型 |
| A↔B Peer DM | Owner 选「向 A 说」并输入消息 | 明确 human author、目标 A；不会伪装成 B 的话 |

**@ Picker**：匹配姓名、专长、最近联系人和真实 permission state；选择后插入绑定 `assistantId` 的 **typed mention token**（头像小 chip），不是靠纯文本 `@名字` 路由；服务端以 ID + 当前 Participant/Grant revision 再验证。重名/改名/复制粘贴不会变成跨身份权限。

**私聊 Consult**：在 A 的 Chat 输入 `@B` 后，如果 B 可联络，A 生成一个简洁动作预览：「联系 B，只共享以下请求与选中的资料，不共享整段私聊/私人 Memory」；低风险且 Owner 已批准的咨询可直接派发，其余要确认。首次请求创建稳定 Peer DM；A 的聊天出现「已联系 B · 等待回复」卡 → 「B 已回复 · 查看 A↔B 对话」。B 离线/拒绝/无权限必须如实显示。

**Group 默认只响应被 @ 的 Bot**：没有 `@` 就保存到群但**零新模型调用**；`@A @B` 有两个精准 target，可在发送前提示将唤醒 2 位。`Reply` 引用 `replyToMessageId`，对某 Assistant 的消息回复默认精确投向该 Assistant，避免让群里所有 Agent 再跑。`@所有助理` 和“全员自动响应” MVP 暂不提供；以后开启前必须有次数、费用、轮数、超时限制与显式确认。

**消息类型**：human text、agent-authored reply、system fact/activity、question/approval card、delegation/request、task/run、automation report。每种有真正 `authorKind/authorId/messageId/sourceRef`；外部文本是数据，不能提升成系统指令/工具授权。Group/Peer 协作消息与 Herdr Provider Transcript 分离，不能把 B 的回复伪装成 A 的自然发言。

**防止 Bot↔Bot 自触发**：群中由 Agent 自己发出的 `@B` 默认仅作为可见文本/待确认的协作建议，**不会继续唤醒 B**；只有 Owner 明确发出的 @，或 Go 权限、成本和最大轮数审核通过的 Delegation，才创建下一次唤醒。Peer DM 的连续自动往返同样必须有 `maxHops/maxDepth/maxFanOut/TTL/tokenBudget`、取消入口与醒目的「已暂停自动交流」状态。

## 6. 五个典型使用流程

**A 私聊中 `@B`：** 用户对“研发助理”说「请 @测试助理 确认回归是否完成」。`@` 选择器按 Directory 展示联系人，插入真实 `assistantId` 的 mention token；在发送前预览只共享“这段请求与勾选附件”，不得自动转发完整私聊/私人 Memory。研发助理 A 通过 Go Mailbox 联系 B，首次建立或复用 A↔B 的稳定 Peer DM，A 的主 Chat 出现「已联系测试助理 · 等待回复 [查看对话]」。B 的受控 Coordinator 在安全回合/受控 Worker 里处理后回复，A 再整理给用户。**无需创建群**；B 忙碌/离线/拒绝时有明确状态，不能盲目直接 prompt 其 Pane。

**多助理群聊：** `+ → New Group` → 选择 2–4 个 Assistant（首版上限）、可选群名和共同目标 → 立即进入相同 ChatView。用户「@研发 完成实现，@测试 做回归，@审查 列阻断项」。Go 只向被精确 @ 的成员派发有界工作；每个助理以自己的头像/署名回应、附真实 Task/Delegation/Run 证据。群聊可以 Reply、按人查任务、静音、增删成员、归档；Group 不是一个新的 Agent、一个共享 Herdr Pane 或三份复制的 Provider Transcript。

**旁观 A↔B 的私聊：** 联系人/聊天列表里有双头像「研发 ↔ 测试」，用户 Owner 点开观察，消息真实展示 A/B 双方身份。Composer 默认是观察模式，用户想插话须点「向 A 说 / 向 B 说 / 向两者说」，发送者始终署名 **You**，不会伪造 A/B 说话。可从任意消息生成后续 Task、共享特定引用到 Group，返回原长期 Chat 不丢 draft。

**一个 Assistant 派多个 Worker：** User↔A 里说「同时调研、实现、测试」。Go 原 `parentTaskId/dependsOn` 组织同一 Assistant 的有向图；Herdr Worker 执行。主聊天显示一张「3 个工作项 · 2 运行中 · 1 待审批」事实卡，点开看到准确 RunID/Pane 和 Git Review。**Worker 不是新的长期联系人**；跨 Assistant 委派才需要 B 接受并创建 B-owned Task，不能把 B 的任务写进 A 的本地 TaskGraph。

**长期定期跟进：** User↔A 中说「每日上午检查 CI 并告诉我」，产生现有 Go Automations `draft` 与权限/预算预览，用户批准后交给**同一个 Go Scheduler**。规则持有 ownerAssistantId 和 reportConversationId，实际 Run/失败/结果以有来源的事实卡报告到 A，或经显式 group grant 报告到一个群，避免所有 Assistant 自动回复。

## 7. 权限与上下文控制

**公开只表示被发现**：Assistant → About →「允许其他助理发现我」默认关闭。开启只显示可编辑的 Public Contact Card（名称、头像、专长、职责摘要、可联系状态），不会暴露系统提示词、个人 Memory、Chat 历史、私有文件或原始 Herdr Pane/AgentSessionID。Owner 总能看到自己管理的 Assistant。

独立设置 `receive_message`、`accept_delegation`、`share_activity`、`share_memory`、`allowedPeers`；即使入群，也不会自动获得其他成员的 Memory/Tools/Workspace 权限。Owner 可以明确把 Private Assistant 加入指定群，给予**仅该群可见**的身份/消息授权，不会使它对整个 Assistant Directory 公开。

Group 消息不是长期 Memory，除非当前 Assistant 明确提出 candidate 并按用户权限批准。新增成员历史可见水位线默认为加入时；移除后无新消息投递，过去已被模型消费的内容不可被技术性“撤回”。Group 中外部引用文本一律当不可信数据，不改变 System Prompt/Go Policy/Approval。

当前 macOS `SandboxEnforced=false`：Go ACL 可保护官方协作接口，却不能保证带完整同 UID Shell 的 Agent 无法绕开 CLI/文件访问；设计/文案不能宣称已实现强 OS 隔离。

## 8. 一份 ChatView、三个会话 Transport、两个消息权威域

~~~text
Assistant Chat List (MyGo / Expo React Native)
    ├── human_dm → AssistantConversationResolver → Herdr/Provider Chat
    ├── peer_dm  ┐
    └── group    ┴→ Go Conversation Store → Directory/Mailbox/Grants
                                      → Go Task/Delegation/Automation
                                      → verified Herdr Coordinator/Worker Runs
    （三个路由都由 Shared ChatView 进行一致的渲染）
~~~

**不复制 Provider Transcript**：`human_dm` 的完整对话仍由 Herdr/Provider/HostConversationService 拥有，Go 只存稳定 Conversation 索引、绑定、source refs、活动与未读。`peer_dm` 和 `group` 是新的一等通讯产品，必须把多作者协作消息保存在同一 Go daemon 的 **ConversationStore**；它不是把某位 Agent 的原 Transcript 复制成第二份。三种不同 source 通过同一 Shared ChatView 的 typed ChatTransport 展示。

建议模型：

~~~text
Assistant (existing ID + optional extensions)
  id, name, avatar, label, mission, expertiseTags
  primaryForOwner?, publicProfileVersion, discoverable, grants
  approvedMemoryRefs, defaultPolicy, coordinatorSource

Conversation
  id, kind: human_dm|peer_dm|group, ownerId, title, revision
  participants[], pinned?, muted?, archived?, lastActivitySeq
  sourceBinding? (only human_dm), historyPolicy, replyPolicy

Participant
  conversationId, principalKind: human|assistant, principalId
  role, joinedAt, leftAt?, historyVisibilityFromSeq, scope

ConversationMessage (peer_dm/group only)
  id, conversationId, authorKind, authorId, body, kind
  mentions[{assistantId, displaySnapshot}], replyToId?, threadId?
  correlationId?, sourceRef?, seq, createdAt, idempotencyKey
  deliveryStatus, policyRevision

Task / Worker / Run / Rule / Delegation / Approval / Event
  keep current Go authority, link via conversationId/threadId/sourceRef
~~~

- **身份**：AssistantId 是长期联系人；ConversationId 是长期私聊/群聊；Herdr Pane/Provider Session 是真实可变执行绑定；不能按 Agent 名、标题或 CWD 猜。
- **作者**：每条群消息带 `authorKind/authorId` 和真实 Provider Run sourceRef；系统事实卡由 Go 署名 `system`，不能伪装成 A 或 B 的发言。
- **投递**：message-created、delivered、read、Agent-consumed、responded、Task-done 是不同状态；接收方 busy/blocked 时消息经 Go Mailbox 排队；断线/uncertain 先 readback，不重启第二个 Agent。
- **群唤醒**：从 typed mentions 和授权的部分 Group history 生成有界 ContextSnapshot，使用现有 Go Dispatcher/真实 Herdr Agent；不把整群聊天升级为模型的系统指令。
- **性能**：Sidebar 用 conversation 最近摘要和每人的 unread cursor；Drawer 详情按需加载。旧 Assistant 页面每 3 秒全量 Snapshot 改为事件 invalidation/有界增量。切换 Conversation 必须丢弃其他 Conversation revision 的迟到流且不改变 Mac Workspace/Pane 焦点。

## 9. 拟议 API 与文件位置（不是已上线接口）

统一前缀 `/api/v3/personal-agent`，新增/扩展：`GET /conversations`（分页/筛选/未读）、`POST /conversations`（幂等创建 Peer/Group）、`GET /conversations/{id}`（成员/策略）、`GET /conversations/{id}/messages`（协作消息/事件游标）、`POST /conversations/{id}/messages`（typed mentions/replyTo/requestId/expectedRevision）、`POST /conversations/{id}/participants`（群成员增删）、`GET /directory`（Agent-scoped 与 Owner-scoped 两种权限读模型）、`GET/POST /assistants/{id}/visibility`（Owner 设置）、`GET /control-center`（Owner 汇总）。

`capabilities` 可拆为 `assistantConversations`、`assistantContacts`、`assistantGroups`、`assistantNetworkManage`。旧 Host 仅显示已有能力；手机不通过不受控 Herdr socket 或直接 SQLite 访问。不重配对既有设备，Go Host 先生成 fixtures/goldens，手机端同步验证。

现有代码对接点：

- `plugins/solco-automations/internal/personalagent/assistant.go`：Profile/Primary/Discovery；`coordinator.go`：真实 Herdr Source；`task.go/taskgraph.go/dispatch.go`：Worker；`memory.go/context.go`：Context；`store.go` 新增 versioned Conversation/Participants/Messages，不迁移用户私人 Provider Transcript。
- `internal/personalagent/client.go` 与 `internal/remotehost/personal_agent.go`：受控 CLI/Host Provider；保持唯一 Go daemon Writer、现有 Device Scopes 和审批。
- `frontend/src/assistants/AssistantsPage.tsx` 和 `internal/nativeui/assistants_section.go`：混合聊天列表、Contacts 切换、Primary Pin、Group rows；`frontend/src/App.tsx` 支持 source-specific scoped transport 和 typed @、Reply/作者信息/活动卡，仍只维护一份组件。
- `solco-mobile/src/app/assistants/index.tsx`：Chats/Contacts 列表；`src/app/assistants/[id].tsx` 保留人↔Assistant 深链；拟增 `src/app/assistants/conversations/[conversationId].tsx` 展示三种聊天，继续复用 `ChatPageView`、Chat controller；Host DTO 在 `src/contracts/host-v3.ts` 和 Queries 补齐。

**范围限定**：第一版只处理当前 Solco owner 下同一 Mac/Go 服务的 Assistant Network，不做跨用户、跨 Mac Federation、公开 Internet 发现。实际 GUI 退出后 resident Host 是否能发送 Chat 必须以真正支持的 capabilities 为准，不能因为 Assistant Directory 在线就冒充实时聊天在线。


## 10. 交互优先的分批实施（不要等全量 Go 后端重构）

| 批次 | 产品切片 | 既有技术计划对照 | 阶段验收 |
| --- | --- | --- | --- |
| **UX0 · 原型/合同和失败测试** | 确定 Assistant/Contact/Conversation/Participant/Message 的关系，Sidebar 两模式、三类 Chat、@ 语义、Owner 权限；原 ChatView adapter 取证 | Chat-first P0、Network N0 | 先测出当前不存在 Peer/Group、桌面隐式 selectPane 和手机只能看卡片的失败证据 |
| **UX1 · 联系人与个人私聊** | Sidebar/手机变消息列表、Primary/Pin/Hide、点击 Assistant 即 Shared Chat、稳定 ID/binding | P1、N1 的最小 Profile | 新建 Assistant 直接进 Chat；切换 A/B 草稿/Queue/消息不串、Mac Pane 焦点不变 |
| **UX2 · @ 查找与咨询** | Directory、typed mention picker、A 私聊中 @B 的 share preview 和 Consult/Waiting 卡 | N1/N2 | 未建 Group 能联系 B；权限拒绝正确；没批准不分享私人资料 |
| **UX3 · Agent↔Agent Direct Chat** | A↔B 持久 Peer DM、离线收件/回复、Owner 观察/精确插话、历史/未读/跳转 | N2/N3 | 同对复用 Peer 会话；不同议题线程隔离，断线幂等，消息不冒充 Provider |
| **UX4 · Group Chat** | 选择成员、群名、Group message store、@/Reply、多作者、成员历史 ACL | N2/N3 衍生 | 无 @ 零模型调用、多个 @ 只唤醒被授权目标，成员离群/撤销即时生效 |
| **UX5 · Task/Rule Chat 卡** | 子 Worker/Delegation/Review/Automation 回报卡和从 Chat 跳 Herdr Pane/Run、返回 | N3/N4 + Chat-first P3 | A→B→Worker 可追溯；Task/Goal 状态来自 Go，单 Scheduler 无双派 |
| **UX6 · 全局管理与完善** | Control Center 中 Contacts/Conversations/Groups/Delegations/Rules/Needs You，移动版只做精简二级页面，性能/通知/离线 | N5/N6 + P4 | Owner 可看全局链路而 Chat 保持简洁；Mac/手机一致、回滚不丢历史 |

**强烈建议 UX1 → UX2 → UX3 优先交付**：即使暂时没有群聊，用户已经可以像聊天工具一样管理不同助理、在 A 聊天里 @B 并查看它们单独交谈的过程。群聊是下一步的“多人共享讨论”，不是产品入口的前置。

## 11. 测试场景（新增 CONV-01…30，不改上游冻结测试）

| ID | 可验证的产品行为 |
| --- | --- |
| **CONV-01** | 每位 Assistant 有稳定默认 human_dm，旧 AssistantId/Herdr refs 保留 |
| **CONV-02** | Primary 同时只有一个；换 Primary 不自动得到其他 Assistant 的权限 |
| **CONV-03** | Sidebar/手机区分单头像 human_dm、双头像 peer_dm、叠头像 group，附未读/Pin/Mute/Hide |
| **CONV-04** | Hide 会话不停止已有 Agent/Rule/Task，Unhide 后历史未损坏 |
| **CONV-05** | A 的私聊输入 @B 出现权限过滤的联系人；选中后插入稳定 assistantId token |
| **CONV-06** | A 私聊 @B 生成 Consult，不把 B 加入 A 的 human_dm participants |
| **CONV-07** | A↔B 首次建立 Peer DM，以后同对复用，会话与单次 thread/correlation 分开 |
| **CONV-08** | A↔B Go Message Store 持久，离线/重试/崩溃后不会双送或重复执行 |
| **CONV-09** | Owner 看 Peer DM 的 A/B 消息来源正确，Owner 插话署名人且有明确目标 |
| **CONV-10** | Group 独立于长期 AssistantID，可选择 2–4 Assistant 创建并可重命名 |
| **CONV-11** | Group 无 @ 零 LLM 调用；@B 只启动 B；多 @ 精准且有预算 |
| **CONV-12** | 纯文本伪造 @、改名、重名、复制粘贴不绕过 structured mention / policy |
| **CONV-13** | Group ReplyTo 锁定精确 messageId/author，不误广播给所有成员 |
| **CONV-14** | 邀请 Private Assistant 只产生指定群权限，其他 Agent Directory 仍不可发现 |
| **CONV-15** | 加入后历史水位线和离群后新消息的 ACL 正确，已分享旧数据不可假称撤回 |
| **CONV-16** | Peer/Group 消息不能伪造成 Herdr Provider transcript；真实 Agent reply 有 sourceRef |
| **CONV-17** | 原 Workspace/Panes Chat 与 Assistant human_dm 的 Send/Stop/Answer/Queue/Files/Voice 不退化 |
| **CONV-18** | A/B/Group 快速切换不串 Chat、draft、未答/滚动/Queue，且不隐式移动 Mac Pane |
| **CONV-19** | A 的 Worker 与委派给 B 是两种对象；B 接受才创建 B-owned Task，跨 Assistant DAG 不误归属 |
| **CONV-20** | Worker/Automation 结果只作为有来源的事件卡进入准确目标 Conversation |
| **CONV-21** | 关 discover 阻止其他 Agent 查询，Owner 仍可在 Contacts 查到 Private |
| **CONV-22** | 关 receive/delegate 后新投递被拒绝，不允许 Skill 绕过 Go policy 直发 Prompt |
| **CONV-23** | Group @mention 多手机重播时消息/Agent 工作遵从 requestId/revision 幂等 |
| **CONV-24** | Peer/Group 不可信文本不能通过提示词注入获取额外系统/文件/审批权限 |
| **CONV-25** | 旧 Host 不具 assistantGroups capability 时手机正确降级、不重新配对 |
| **CONV-26** | Resident Host 只有 Assistant/Inbox 服务无 Chat 时仍能管理，但发送 UI 明确不可用 |
| **CONV-27** | 10+ Assistants、多个群、50+ Tasks/长历史下按增量游标更新，无 3s 全量 CLI 卡顿 |
| **CONV-28** | 自动化事件报告到正确 conversationId，暂停/撤销不再向群广播新消息 |
| **CONV-29** | Owner Control Center 可以按 Assistant/Conversation/Delegation 找到原聊天与真实 Run |
| **CONV-30** | 同 UID Herdr CLI/文件可能绕过软件 ACL 的系统级风险不被 UI/测试虚称为强沙箱 |

**验收纪律**：Go 根 module 与独立 `plugins/solco-automations` 各跑 `go test ./...`；Mac `bun run build` / `bun run build:mobile`；Mobile `pnpm sync:chat-page`/`pnpm check:chat-page`、`pnpm sync:fixtures`/`pnpm check:fixtures`、`pnpm typecheck/lint/test/export:web`。Host goldens **先 Mac 生成后同步手机**；真实 Herdr E2E 使用临时 HOME/Session/scratch Git，不触碰实际工作树或已有 Session。CONV 产品门禁与旧 CX-01..15、NW-01..22 以及 Shepherdr 2.1 的固定来源用例并行，绝不以 Go 单测全绿冒充手机真机已经验证。

## 12. 需要锁定的核心决策（作为默认方案）

1. **三类对话**：Human↔Assistant、Assistant↔Assistant、Owner+多 Assistant Group，分开身份与资料/权限。
2. **不以群代替通讯录**：Assistant Directory 解决“有哪些助理、能联系谁”，Peer DM 解决一对一问答；群只是共享议题/协作空间。
3. **@ 不改变当前会话成员**：A 私聊 @B 表示 A 去找 B；Group @B 表示请 B 在本群回应。
4. **Primary 是协调角色**，不是 Supervisor/Owner 的等价权限。
5. **仅 @ 唤醒**：默认群消息不调用 LLM；@everyone、全员自动回应首版关闭。
6. **一个 ChatView UI，多种 Transport**：人↔Assistant 的 Provider Transcript 不复制；Peer/Group 的新消息存 Go ConversationStore 且有真实 author/source。
7. **一套执行体系**：Herdr 做 Agent/Pane/PTY，Go Task/Automation 记录任务/规则与调度；Group 不建立新的 Runtime。
8. **关系层与权限层分开**：公开名片、接收私信、参与群、委派、分享 Memory/资料是不同操作；旧 Assistant 默认 Private。
9. **管理独立**：侧栏点头像进聊天；要全局查看/暂停/审批/调度，才进 Control Center。
10. **先体验后复杂编排**：按 UX1/UX2/UX3 交付主要价值，再 UX4 群聊、UX5 Worker/Automations、UX6 全局优化。

**本方案是 Chat-first + Assistant Network 的产品交互权威；Go ACL/Mailbox/Herdr Skill 的技术实现继续在 `docs/assistants-collaboration-plan.md` 细化。**

