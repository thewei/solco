# Solco Assistants 3.1 — 消息式个人助理、群聊、MCP 与目标履约规划

> **状态：2026-11-04，用户新决策的最新产品/实施权威；本轮仅审计与编写计划，没有修改产品代码。**
> **优先级与覆盖关系**：本文件覆盖 `docs/assistants-personal-agent-platform-plan.md` 中旧的“三类 Marketplace（Integration/Workflow/Agent Template）、独立 Lua/TS 插件运行时、马上落地通用 Go WorkflowEngine”的决策。该文 Profile/Provider/Memory/Security/原有 AC-01～AC-20 仍有效，但其 `7 Marketplace`、`V3-4/V3-5`、`AC-16～AC-18` 必须按本文的 **MCP-first / Workflow-deferred** 执行。群聊/联系人的数据契约参考 `docs/assistants-conversation-product-spec.md`、`docs/assistants-collaboration-plan.md`；手机同步 `solco-mobile/docs/assistants-messenger-agent-mobile-plan.md`。**禁止把规划的能力说成现已上线。**
> **一句话产品**：侧栏像通讯录，点击个人助理像打开微信单聊，点击群聊像打开微信群；个人助理是有初始指令、独立记忆、长期目标、周期职责和 MCP 工具的真正 Agent，能自主推进、持续解释工作进展、事事有回应。

## 1. 概念最终收敛（这次以用户定义为准）

| 概念 | 产品含义 | Runtime / 事实源 | 是否是常驻 Agent |
| --- | --- | --- | --- |
| **Personal Assistant（个人助理）** | 一位可创建、编辑、聊天的长期 Agent，有稳定 assistantId、头像、名字、描述、初始指令、权限、独立 Memory、Provider/Model/Effort、MCP、Goal/Routine | Go PersonalAgent Assistant Store + Herdr Coordinator；每个助手自己的 conversation binding | **是长期身份**，但实际 Provider Session 可以重建；不要把 AssistantID 当 PaneID |
| **Group（群聊）** | Owner 与多位个人助理交流、分配任务、接收彼此回报；独立 groupId、members、conversationId、头像、权限 | 未来 Go Conversation/Group/Message Store；精确 Assistant participant IDs | **不是**新个人助理、不是共享 Agent/Provider Pane |
| **MCP Service（插件/工具）** | 能被 Agent 使用的 MCP Server，提供 Tools/Resources/Prompts；一位助理可选择和授权多个服务 | 复用 Agent/Provider 可用的 MCP 支持、已有配置与连接器；Solco 只统一管理可见/授权/绑定/状态 | **不是** Assistant，不需要 Lua/TS 新插件架构 |
| **Goal（目标）** | 有结果定义、退出条件、进度证据的长期工作目标，由一位 Assistant 持续负责 | 现有 Goal/Commitment + TaskGraph/Heartbeat/Events；后续 GoalPlan/Progress/Initiative | **不是**一次 Task |
| **Routine（例行职责）** | “每天检查项目”“每周做回顾”这种 Assistant 自己负责的可反复执行工作 | 现有 Go Automations Scheduler + Heartbeat + Assistant-owned Rule/Commitment 的显式绑定 | **不是**第二套 Scheduler |
| **Task / Run** | 为实现 Goal/Routine 发出的一次实际工作及每次尝试，责任明确、可复盘 | 已有 Assistant TaskGraph、Run、Herdr/Provider Agent，原身份精确 refs | Worker/Task **不是**新的长期 Assistant |
| **Workflow（复杂工作流）** | 多步骤执行、条件、人工审批、恢复/补偿等；只做需求/框架调研，不创建新产品类型或引入框架 | 暂用现有 TaskGraph/Herdr 与 Automations；未来调研 Go 工作流库 vs 小型状态机 | **延期决策** |

**纠正旧认知**：
1. “预置智能体就是个人助理”。今后不把 “Agent Template / PR Reviewer” 作为独立的第三类插件：可内置 **Assistant Preset（创建个人助理时选用的默认配置）**，创建后是一个真实 Personal Assistant；被其派发执行的 PR Review 是 Task/Worker role，不是第二个 Assistant 类型。Preset 是创建辅助数据，不是 Marketplace runtime。
2. **插件 = MCP Service**。不设计与 Agent MCP 平行的独立插件 RPC、工作流包安装器或 Lua/TS 入口。可有以后展示 MCP Catalog 的 UI，但物理实现就是既有/兼容的 MCP Server 配置和权限管理；统一 Solco 的助理授权视图，不强迫所有 Provider 共享不兼容的 config 格式。
3. **初始提示词不是记忆**：初始指令定义角色、工作原则、口吻、目标和 MCP 使用准则；Memory 保存个人助理长期积累/用户维护的事实；Routine 保存自动工作规则，三者在 Chat 里形成一致人格但有明确权限与版本优先级。不能把任意记忆晋升成系统最高权限 prompt。

## 2. 真实代码审计：头像、按钮、创建弹窗

| 位置 | 当前实现 | 本次要解决的差异 |
| --- | --- | --- |
| `internal/nativeui/assistants_section.go` | 侧栏 Assistant 行传入固定 `Mark: visualMark{svg: iconAssistant}`，没有自己的头像；头部 `+` 的 `treeRowAction.Run=s.openNewAssistant`；上下文菜单也只有 New Assistant/Open Assistants | 使用与 Workspace 的 `treeRow` 同套视觉/间距/状态位，但个人助理头像默认改为**圆形单字母**，群聊是叠头像；头部 `+` 展开**恰好两个菜单项**：New Assistant、New Group |
| `internal/nativeui/components.go` + `project_tree.go` | 共用 `treeRowSpec.Tile`、Workspace 字母 tile；`git_avatar.go` 已有按名字首字母、稳定色和圆形 `authorAvatar` | 复用相同 token/`treeRow` 字号与节奏，抽取通用 `nameAvatar(name,id,shape,size)`（按稳定 ID 分配颜色，改名不变），**不要跨模块调用 Git “authorAvatar” 语义方法**。按 workspace tile 方形、Assistant 圆形、Group 叠形，状态点和 NeedsYou 单独一列 |
| `internal/nativeui/assistants_state.go` + `assistants_bridge.go` | `openNewAssistant` 设 `assistants.newForm`；`Snapshot` 中 `view.NewForm,s.assistants.newForm = ...,false`，**读取时清除**；`AssistantsPage` 的 `useEffect([view?.newForm])` 只根据 bool 触发，页面每 4s polling，且 NewAssistant 需页面/视图加载成功 | **存在可解释的丢事件/合并风险**：前后快照乱序、首次页面重载/未 ready、连续同种请求 bool 不变化；还有 `showAssistant("")` 先触发导航，`newForm=true` 后设置的时序窗口。**代码静态审计定位风险，不等于已用真机复现唯一根因** |
| `frontend/src/assistants/AssistantsPage.tsx` | 页内 NewAssistant Sheet 依赖 `form===assistant`；提交时先调用异步 action，随后立即 `onClose()`；失败只出顶栏错误 | 新建表单单一受控 open/close、点击确认后 Await mutation 成功再关闭；禁重复提交、持久提交 requestId、失败保留内容、创建成功进入对应 Assistant Chat，不回落至第一位老助手 |
| `src/app/assistants/index.tsx`（手机） | 页面顶端 New Assistant 是 **inline Card 展开/折叠**；列表只有状态点没有头像；创建完成跳转到管理详情 | 顶栏 `+` 先打开 Native Action Sheet/Popover，两个入口；New Assistant 全屏/Bottom Sheet；新建 Group 选人并进入 Group Chat；列表头像、消息摘要、未读，而不是工作列表 |
| `src/app/assistants/[id].tsx`（手机） | `ScrollView` 的 Conversation 卡只转去普通 `/chat/[paneId]`；随后大段任务/审批/记忆卡片 | 进入个人助理即是 **复用一个 ChatPageView 的私聊**；群聊 route 也是统一聊天面板，多 Assistant 署名；管理功能移进 ⋯，不在聊天屏里堆管理卡 |

### 2.1 头像规格

- **个人助理**：Owner 未上传头像时使用圆形色块，中心名称的**第一个有意义字母/数字**（Unicode 汉字保留首字，英文字母大小写正规化）；空名称用 “?”，色彩以 `assistantId` hash 得到稳定 TileTone，而不是姓名 hash（更名不换颜色）。支持用户可选图片/avatarRef，在侧栏、Chat 顶栏、列表、@ Picker/群消息相同身份投影。
- **群聊**：有用户上传的群头像就用它；无则取成员中的前 2–3 个个人助理小头像做**叠头像**（尺寸/位置固定），成员不足时用多用户 glyph。不要把群头像默认为单个 Agent 首字母。
- **Workspace**：继续原有方形首字母 tile，保持旧 UI 视觉规则；代理（真正运行中的 Agent/Provider）已有品牌图标时可继续显示品牌标识，**这里个人助理的默认头像是圆形字母**，与 Workspace 方形 tile 区分。
- 所有行共用 `treeRow` 选中态/hover/间距/密度/裁切：左头像，名称，右边 status dot 和 unread/NeedsYou 计数；**头像上不覆盖重要文字**。长名、省略、中文/Emoji/RTL、图片缺失、不透明色、深浅主题都验收。
- 手机/桌面从 Go 共有稳定 `avatarRef` / `avatarVersion`（不把本机绝对路径给手机）；Mac renderer 优先本地受控资源/缓存，Mobile 经 paired Host 的授权图片引用获取，安全降级为字母。

### 2.2 New Assistant / New Group：唯一的 `+` 菜单

~~~text
Assistants                                     [+]
  [全部聊天] [联系人]
  🟦 W  Wilson 的个人助理                    ● 2
  👥    发布协作群                          ● 1
  🟢 R  研发助理                            ...
  🟣 T  测试助理                            ...

                       点击 [+] 后
                  ┌─────────────────┐
                  │ New Assistant   │
                  │ New Group       │
                  └─────────────────┘
~~~

- **桌面**：复用 `treeRowAction.Menu`，将头部 icon button 变成真实 Menu 的所有者（当前 `components.go` 注释已说明，Menu owner 必须**每帧稳定存在**；不要把 menu handler 放在 hover 才创建的 transient button 里）。上下文菜单也复用同一两个命令。
- **New Assistant**：仅姓名必填；可选初始说明/Avatar/Preset；保存 Go `CreateAssistant` 后返回稳定 assistantId → 进入其 Chat。后续才在 More 设详细 Prompt/Provider/Memory/Goals/MCP；默认不自动授权高风险执行。
- **New Group**：名称可选、选取 2–4 位 Assistant（第一版）、预览参与者及共享边界；写入真实 `groupId/conversationId/participantIds`（均用稳定 ID），创建后进入空群 Chat。**如果 Go group endpoints 未实现，New Group 应有明确未开放状态/实施门禁，绝不能点了生成一个普通 Assistant 冒充群**。首批若要交付菜单与群聊同时上线，必须先实现最小 Group Store+Conversation API。
- 创建异常：未配对/离线/没有 Provider/同时重复点击/连续切换 Assistant/重复请求、慢响应/程序重启，都保留表单或显示明确错误。创建动作支持 requestId 幂等；**不是每次 Retry 都生成新 Assistant/Group**。
- **修复 New Assistant 丢事件**：取代 `bool newForm` 为单调 `createIntentID`/`kind` 的事件队列或 route intent。Go 先登记意图再导航；UI 只在 mounted+ready 时消费，打开 Sheet 成功后 ACK，直到 ACK 前不可在 Snapshot 中清除；同时支持同一个请求被重复送达不重复弹出。对同页面 `+` 菜单直接写本地 UI state，无需经过 Snapshot。明确触发来源（Sidebar、Header、命令面板、手机），通过 intentId 去重而不依赖 bool toggle。
- **竞态测试**：两轮 Snapshot 乱序、首个快照成功而后第二次将 bool 覆盖、第一次进入路由时 WebView 未 ready、4s poll、抽屉开关、连续双击、取消/失败后重试、empty-state、创建成功路由选中等，全部按可复现自动测试+Mac UI 真测验证。

## 3. 手机就是聊天软件，不再有“先点 Conversation”这一层

- `/assistants`：上面 “Chats | Contacts”，一个 `+` 仅两项 “New Assistant / New Group”；联系人行和聊天行统一 Avatar / Name / 可信最后消息摘要、时间/未读/Needs You。Owner/Agent/Group/Peer DM 的行用不同图形身份，不增加一个管理大首页。
- `/assistants/[assistantId]`：直接 **ChatPageView**，顶部 Back + Avatar + Name + Assistant status + ⋯，中间 Provider 历史和真实事件卡，底部可发送的共享 Chat Composer。没有 Agent Session 时仍是空会话壳；首条消息用 idempotent ensure 保证只启动/发送一次。
- `/assistants/conversations/[conversationId]`：统一群聊/后续 Assistant↔Assistant Peer DM；同一个 Shared Chat UI 组件，不复制 Provider Chat implementation。Group 的数据源是 Go Conversation Store / Mailbox（多作者消息），Provider transcript 仍是原真实 Herdr 数据源；Group adapter 按能力呈现，不能假装它支持 Provider-only Stop/Question/Voice。
- **群聊路由行为**：每条消息有真实发送人头像、名字、时间、Reply/mention、Task/Approval/Automation 卡；第一版只精确 @ 才唤醒受邀 Assistant（没有 @ 则纯存消息且零 Agent 模型调用），可在群中 @ A 创建/委派 Task，A 在自己的 Coordinator/Task 轨道执行，结果用源引用回群；不把 A 的私有 Memory 发给所有成员。
- **回复语义**：可以同 A 单聊让它和 B 合作，Go Delegation 按 grant 建立/复用 A↔B 联系；Group Message 不能简单当成所有 Agent 的输入 Prompt。Owner 操作与 Agent 自发行动有不同 author/source，防 Agent 群聊无限自激。
- **离线与远端**：继续使用已经配对的 Host v3、Zod golden、按 Assistant/ConversationID 的 scoped binding。Mac 居留 Host 目前不等于 GUI 退出仍能 Chat：Provider Chat capability 不可用时只读/草稿保留，禁止假的 Send。切换 A/B/群，草稿/scroll/queue 和 WS 事件按稳定 ID 严格隔离；Client 导航不移动 Mac 当前 Pane。

## 4. 个人助理的初始提示词 / 记忆 / Routine 是同一身份，不是三种插件

### 4.1 Assistant Persona（可选文件化）

~~~text
Assistant a_123
  profile: name, avatarRef, description, mission, style
  instructions:
    initialPromptVersion / rolePrompt    # 初始工作方式、专业范围、交互风格
    policyId / policyRevision            # Go 强制审批、安全边界，不由 Prompt 覆盖
  memory:
    approved facts / user notes / candidates / maintenance
  runtime:
    agentKind / provider / model / thinkingEffort
  capabilities:
    mcpServerBindings / allowedToolNames / scope / approval
  goals:
    GoalPlan / TaskGraph / Initiatives
  routines:
    approved triggers / commitments / scheduler bindings / reports
~~~

- 创建时每个 Assistant **自动得到一份默认初始提示词**（内置模板＋用户一句职责），对话欢迎语要基于真实身份，不能给每个助理同一空白工程 Coordinator Prompt。用户在 More → “工作方式”查看、编辑、恢复版本；可选从预设配置如 PR Reviewer/私人助手/项目经理开始，保存后得到真正独立身份。
- **Prompt 编译顺序**：固定不可覆盖的安全/工具调用规则 → Role/初始提示词（assistantId/version）→ 当前 Goal/Routine 相关上下文 → 只读 Approved Memory（有 sourceRefs/版本/预算）→ 当前对话。Memory 不是系统指令、外部资料不授予新的工具权限。
- Go 当前 `CoordinatorPrompt(a)` 固定说明“you do not do the tasks yourself”，且只拼 `RolePrompt`；后续需平衡简单聊天直接回答、复杂任务派发、长期目标主动执行、任务结束主动总结。**不靠手写超长角色 Prompt 代替可执行的 Goal/Routine/Approval 规则。**
- Prompt 更改时：先持久化 revision 并显示“下次会话/下次安全轮次生效”，只有实际 Provider 接受新的上下文版本才能显示 applied；不能静默改变正在进行的 Agent Run，不能同时在 SQLite 角色和文件里建立双权威。
- **记忆**沿用前一 V3 文件夹/SQLite 双向维护计划：用户手动 Add/Edit/Forget；Agent candidate/approved；每周/低噪音整理/冲突建议；Owner 可审阅。初始工作方式可编辑但不能被日常自动“整理记忆”无授权地覆盖。

### 4.2 MCP：使用 Agent 已有的协议，不制造第二个插件系统

**当前明确事实**：旧 `plugins/solco-automations/internal/config/config.go` 有 `mcp_config` 字段，能把某 MCP JSON 配置交给对应 Agent；**这不能证明 Solco 已有统一的 MCP server 管理 UI/Host 客户端，也不证明所有 Provider 都支持同样的 MCP 启动方式**。新增功能首先审计现有 Herdr Agent Kind/Providers 与 MCP config 发现/注入路径。

**第一阶段 MCP 用户体验**：Assistant Chat → ⋯ → “MCP Services”：可从已配置服务选“连接”，查看这个助手是否启用、最近连通状态、实际暴露的工具/权限摘要；按 Assistant 开关和允许 Tool 白名单，新增 MCP 服务器则复用 Provider/Agent 既有配置方式，必要时向 Provider 做配置同步，验证它在实际 Agent Session 内可见；模型/Provider 切换后重新能力校验。

**建议 Go 数据结构**（仅保存注册/授权/投影，绝不造非 MCP runtime）：
~~~text
MCPServerRef: { id, label, source, transport, configRef, authRef, capabilities, status }
AssistantMCPGrant: { assistantId, serverId, allowedTools, allowedResources,
                     allowedPrompts, workspaceScopes, networkScopes,
                     approvalPolicy, revision }
AgentMCPBinding: { assistantId, agentKind, herdrSession, actualEnabledServers,
                   bindingVersion, lastVerifiedAt, unsupportedReason }
~~~

- 服务级 Server Registry 可复用 Provider/Herdr 现有配置；**不要假定** Claude/Codex/Pi 三者支持完全一致的 MCP 格式，必要时只有轻薄 Adapter 将同一个已授权 MCP ServerRef 映射成各自启动/配置形式。
- MCP 原生提供 **Tools / Resources / Prompts**，但 Solco 只向 Agent 暴露由其 Provider 支持且已授权的能力；MCP Server 的 prompt/resource 内容视为外部不可信数据，不能提升 system safety policy。
- **OAuth/HTTP remote MCP / stdio local MCP** 均受同一安全检查：凭据 Mac Keychain/安全服务保管，不能转发到手机或 Prompt；本地 stdio server 运行命令可能拥有完整用户权限，要展示安装命令、来源、目录/网络权限并显式确认。Provider 原生 MCP 可能绕过 Solco 的自建 ACL，所以**能证明 enforcement 才允许称为隔离**，必要时通过受控 Broker/OS sandbox 执行。
- 特别不要把 “MCP 服务连接成功” 与 “这个 Assistant 的当前 Provider Session 已加载且可以调用”混为一谈；分别展示 configured / connected / bound-to-agent / verified-effective。
- MCP 市场如果以后仍保留，应该是 **MCP Server Catalog/Directory**，只分“已连接/可连接”，不再分 “Integration/Workflow/Agent Template” 三大扩展类型。第一版不构建新 TS/Lua plugin runtime，暂不支持社区脚本自动安装。
- 官方规范的 Host/Client/Server 概念与 tools/resources/prompts 可参考 https://modelcontextprotocol.io/docs/learn/architecture；官方 Go SDK https://github.com/modelcontextprotocol/go-sdk/ 仅是**若要在 Go Host 直接充当 MCP Client/Server 的候选依赖**，不意味着一定需要引入（先观察能否复用 Agent 已有 MCP，避免两处重复长连接）。

## 5. 从用户目标到持续自主推进：Agent 应该“追着目标走”

现有 `Goal/Commitment/TaskGraph/Run/Heartbeat/Event/Approval` 是基础；补一层薄的 **GoalPlan + Initiative + ProgressLedger**，由同一个 Go PersonalAgent Store 持久化。**不创建另一套 Scheduler 或无限长 Autonomous Agent Loop**。

### 5.1 每个 Goal 是一个可持续的成果合同

最小字段建议：
- `goalId/assistantId`，用户原话/`objective`，**成功条件 successCriteria**，允许动作和高风险审批边界，目标计划 `planRevision`；
- `milestones[]`（每个阶段可验证的 evidence requirements）、任务 `taskIds/dependencies/owners`、阻塞/nextAction/nextCheckAt；
- `progressEvidence[]`（Git SHA/PR/文件/测试结果/审批收据/Herdr Run ID）、`currentState`（draft/active/working/waiting_user/blocked/verifying/completed/failed/paused）、最后进度解释；
- `budget/permissions/quietHours/deadline/priority`、`reportConversationId`、`lastAcknowledgedAt/lastProgressAt/nextReportDueAt`、`followUpPolicy`。

**助理必须能作出五个动作**（受 Go 权限约束）：规划（plan）→ 拆任务并派发（delegate/execute）→ 核验真实结果（verify）→ 发现阻塞自动修正下一步（replan）→ 和用户同步进度（report）。Plan 不是不可变的：每次重大变化形成 planRevision、保留前后 diff/理由；无授权不能无限新建任务/调用模型。

### 5.2 “事事有回应”：采用可持久化的 Commitment Ledger（承诺台账）

**区分聊天确认、运行事实卡和 Assistant 自己的语言回复，不造假 Provider 消息。**

| 阶段 | 系统/UI 给用户看到 | 什么时候触发 |
| --- | --- | --- |
| 1. Accepted/Received | “已收到 · 正在处理”**系统收据卡**，带对应 messageId/goalId；不是模型伪造的“我收到啦” | Host/Go 持久化接收成功后立即返回；服务端没有 ACK 不假定发送成功 |
| 2. Planned | 助理有自己的自然语言答复：“我会先查 A，再请 B 做审查；涉及创建 PR 会先确认。”附可展开 Goal/Task 卡 | 经真实模型回合后，或因预算/离线显示明确“等待开始/无法启动” |
| 3. Working | 任务/Worker/自动化状态事实卡：谁在处理、哪一步完成、哪些阻塞、下一次检查 | 真实 Task/Run 状态迁移或可信任务进展；**没有新进展时不刷屏** |
| 4. Waiting/Blocked | “需要你批准 PR 创建”或“CI 失败，已暂停下一步”的精准卡/通知；状态能留住直到用户处理 | 阻塞/审批/超时/模型不确定等可验证事件 |
| 5. Verifying | “已提交修改、正在验证测试” + 测试 Run 和文件/PR 证据 | 执行产出以后，核验成功以前 |
| 6. Completed / Failed | 协调者自然语言汇总：做了什么、证据、尚余风险、下一步选项；明确 Done/Failed/Cancelled；如无法起模型至少先给系统事实卡 | 真实终态、来源记录与 Report delivery receipt |
| 7. Follow-up | 对未完成/等待外界变化的 Goal 定时检查，必要时主动报告，不因对话窗口关闭而丢掉目标 | `nextCheckAt`/事件/Heartbeat，按 quietHours/预算/节流 |

**Commitment 不能只在大模型上下文中存在**：用户说 “我会再跟进”“每天检查一下”“下周告诉我” 等，必须有结构化 Goal/Routine 草案与 Owner 确认/授权（只有已有明确预授权的安全动作可自动激活）；否则提示这只是建议、尚未建立自动跟进。关闭 App、重启、断线不应让 Commitments 消失。

### 5.3 主动推进状态机

~~~text
User Chat / Existing Routine / External Event
        │  (durable receipt + idempotency)
        ▼
      Goal Draft ──[approval/policy]──► Goal Active
                                         │
                  plan + bounded budget ▼
                   GoalPlan / Milestones / Task DAG
                             │
              ┌──────────────┼─────────────┐
              ▼              ▼             ▼
         Task A           Task B         MCP fetch
              └──────────────┼─────────────┘
                             ▼
                   Evidence / Review / CI
                          │       │
                 verified ▼       ▼ blocked/failed
                     Report     Replan / Ask / Hold
                          │       │
                          └──► Chat / Inbox
                                │
                             Follow-up
~~~

- `GoalSupervisor` 是**Go Core 的轻量决策入口**：只对待处理/状态发生变化/下次 check 到期的 Goals 做确定性判断；模型负责提出下一个 Plan/复盘，Go 事务负责创建 Task/Rule/Initiative 和强制预算、权限、幂等、最大轮数、最大任务数。Supervisor 不应独自变成运行 Provider 模型的第二核心。
- 现有 `TaskGraph` 保持 same-assistant parent/deps 规则；目标交给 A 时 A owns goal/task，委派给 B 必须用未来的 Go Delegation/Mailbox 显式授权，B 归属自己的 Task，A 等结果后总结；跨 Assistant 不能直接写 parentTaskId 指向另一个助手的 Task。
- 进度不得凭空报百分比。用户可看“3/5 可验证里程碑、当前在测试”和证据；只有任务数/里程碑权重客观可算时展示比例，否则明确“进行中/等待/验证中”。
- **受控主动性**：用户可选 only-respond/suggest/follow-up/approved-autonomy，限制 maxSteps/maxFanOut/maxConcurrentTasks/wakeBudget/cost/token/working hours；重复事件去重、持续同一问题退避、失败 escalates。每天主动问候不是主动性目标，**真正主动是能找到下一步、执行和反馈**。
- **报告节奏**：基于状态变化为主，外加可配置 `progressReportInterval` 与 `staleAfter`（例如工作期间 30–60 分钟无新的证据则告诉用户卡在哪里、下一步什么时候检查；实际配置可改）；没有证据不要捏造进度，不重要的更新合并成 digest；闹钟/静默时间照常遵守。发送失败 outbox 保留、重连回补，不重复制造 Agent 回复。
- **反无限执行**：事件/群聊消息不能无上限触发 Agent 回合；跨助手/自动化共享 `correlationId`/TTL/maxHops/maxFanOut/budget，必要时暂停并问 Owner，明确 Stop/Pause。

## 6. 周期任务可由 Chat 提议并创建，但不偷偷启用

### 6.1 Chat 建 Routine

~~~text
User：每天上午 9 点检查我的 GitHub 仓库，如果代码有风险，组织 A 审查并反馈。
    ↓
Assistant：理解意图，提出 Routine Draft
    ↓
Go：校验 repo/sourceId、timezone、cron、工作授权、MCP 实际可用性
    ↓
Assistant Chat 卡：
  [每日上午 09:00 检查仓库 · Draft]
  负责：研发助理 A
  来源：GitHub MCP（只读）
  下次：2026-11-05 09:00 Asia/Tokyo
  条件：出现新 SHA 或 PR 后才调用 Reviewer Agent/Worker
  结果：回到本聊天，异常需我确认
  [启用] [编辑] [取消]
    ↓ (Owner 批准或命中已有明确预授权政策)
Go Automations 单 Scheduler 负责触发和执行
    ↓
Run/Task/Review → Owner Assistant 自然总结+证据 → Chat
~~~

- 默认**可以自动创建 Draft**（低风险、本地元数据），但是否自动启用看 policy、触发频率、工具/代码写入权限、费用预算；没有授权不能直接周期性跑高权限动作。用户明确说“启用这项每天检查”可在详情确认，响应中返回已批准规则和下一次真实预计触发时间。
- 谁触发必须是强字段：`ownerAssistantId` / `requestedBy` / `triggeredBy`(scheduler/MCP event/user/delegated) / `executedBy`(Herdr Worker) / `reportedBy` / `reportConversationId` + `correlationId`。Task/Run 结果返回 owning Assistant/Group 时不能串线。
- 现有 `pr_watch`、Heartbeat/automations.yaml 保持兼容；无需为了支持 Routine 先上通用 WorkflowEngine。旧 yaml 仅在 Owner 显式关联 Assistant 时才改归属，禁止同一 schedule 在旧/新两套重复触发。
- Chat 中 “例行职责”入口是已启用和 Draft 列表、执行记录、下次运行与权限；复杂执行可用现有 Task DAG、MCP Tool Calls、现有 Herdr workflows（如可用）组合，**不是新市场的 Workflow 交付形态**。

### 6.2 Go Workflow 框架：明确登记为后期 TODO

**当前不选型、不引依赖、不设计独立工作流产品或 DSL。**
调研开始门槛：三个真实重复场景（如 Repo Change→Analyze→Review→PR Draft；Calendar→Prepare→Notify；Cross Assistant Delegation→Merge Report）证明现有 TaskGraph/Automations 的 Step/Retry/Compensation 无法优雅表达。
后续评估需包含：
- 保持现有 Go Automations 为唯一 Cron/Event 触发源、SQLite 为唯一状态权威、Herdr 为真正 Agent Run；候选框架应是**可嵌入 Go** 的任务编排辅助，而非隐式启动另一个 scheduler/control plane。
- 对比“现有 `TaskGraph` + 小型 Step Runner”与 maintained Go 工作流库；查看版本稳定性、持久化/恢复、补偿/审批、动态分支、扇出、幂等与重入、DAG 可视化、Go 内存/资源、Licensing。
- 确定真实 E2E 的失败恢复、MCP Tool 调用授权、Agent Run `uncertain`、DST/睡眠恢复、取消/暂停/人工复核。
- 在有失败场景与可量化优势之前，**deferred**；不可由于市场 UI 想要“工作流”就提前造一个框架。

## 7. 分批落地（变更原 V3-4/V3-5 优先级）

| 批次 | 内容 | 主要代码/领域 | 交付验收 |
| --- | --- | --- | --- |
| **U0 · UI 稳定性** | Assistant 圆形首字母头像、Group 叠头像基础组件；侧栏 + 仅 New Assistant/New Group 菜单；可靠 createIntent 状态机；创建成功直接进 Chat | `internal/nativeui/{assistants_section,assistants_state,assistants_bridge,components,git_avatar}.go`，`frontend/src/assistants/AssistantsPage.tsx`，移动 `assistants/index.tsx` | New Assistant 连续打开/取消/失败重试/快照乱序都有效；头像与 Workspace 尺寸对齐 |
| **U1 · Chat/Messenger** | 手机助手点击即私聊、Group 最小 Store/Group 创建与可聊天、共享 Chat 复用、精确 @/消息作者/会话路由；桌面 sidebar 混合会话 | Go Group/Conversation/Host v3 + `src/app/assistants/[id].tsx`、`conversations/[id]`；现有 `ChatPageView` | 不再多一层 Conversation 按钮；A/B/Group 不串消息或草稿；Group 真可用才显示可创建 |
| **U2 · 人格 + 模型 + MCP** | 个人 Assistant 默认初始提示词/版本、Profile/Provider/Model/Effort、MCP Server Registry read + per-Assistant grants + 实际 Provider binding 校验 | `personalagent/{assistant,coordinator,dispatch}.go`，Host/Bridge/Mobile Contracts，Go ACL | 真正区分 assigned/running Agent，实际 MCP Tool 可见而不跨助手泄漏；Prompt 版本切换可审计 |
| **U3 · Goal 履约与主动回响** | GoalPlan / Commitment Ledger / Initiative / GoalSupervisor / 结果验证/事件卡/定期汇报、Assistant↔Assistant Delegation | 现有 Goal/Task/Run/Inbox/Events/Heartbeat + 新少量领域类型 | 用户提出长期目标后，Agent 可持续分解、执行、核验、解释阻塞、汇报；无证据不假回报 |
| **U4 · Routine 聊天创建与自动化融合** | Chat→Routine Draft→批准/预授权→原 Scheduler→TaskGraph→结果回 Chat，Legacy yaml owner binding | `plugins/solco-automations`、Host v3、Chat 卡片 | Cron/event 去重、sleep/DST、异常/续跑/手动 Stop、消息交付 exactly-once effect 的有界实现 |
| **U5 · Memory 细化与长期行为** | 独立记忆目录/版本、用户编辑、Agent candidates、整理建议与自然表达风格；静默时间/主动频率反馈 | 延用 V3 MemorySyncService 计划与 Heartbeat；手机 Memory 编辑 | 记忆不串、不被无授权覆盖；Routine/Goal 的主动行为可解释且不打扰 |
| **TODO · Go Workflow 选型** | 先收集真实复杂场景、再做小型 Benchmark/PoC | 研究文档，不改 runtime | 无真实必要性则不引框架 |

**对原有 V3 计划的改动**：之前 V3-4 “三类 Marketplace + 内置 Workflow 包”与 V3-5 “Lua/TS 插件执行”本轮暂停；U2 首先做 MCP 服务管理且不造第二个 Tool 体系。之前“Agent Template”仅保留为 **New Assistant Preset**，不是一个可安装 Agent Runtime。原 Memory/Permissions/Provider 配置任务继续执行，只是把会话体验与 Goal 履约提前成为主线。

## 8. 可执行验收/回归（新增 MU-01～MU-23）

| 编号 | 必须验证 |
| --- | --- |
| MU-01 | Mac 侧栏 Workspace 仍方形 Tile，Personal Assistant 为圆形首字，Group 叠头像；字体/宽高/深浅色/状态位同系统 |
| MU-02 | 更名不改变 Avatar 稳定色；中文字、Unicode、Emoji、空值有稳健回退；用户头像图片可信、缺失降级 |
| MU-03 | Header + 点击仅有 New Assistant / New Group；菜单可鼠标/键盘打开、关闭、切换，不影响 row selection |
| MU-04 | New Assistant 在第一次从其他路由进入、WebView loading、连续点击、Snapshot 乱序、4 秒 poll 下 100% 可打开；验证真实 UI，不只纯函数 |
| MU-05 | 新建提交失败保留输入；相同 requestId 重试不产生两个 Assistant；新建成功选择新 ID 并进入 Chat |
| MU-06 | New Group 只能创建真实 Group；旧 Host 不支持时明确 unavailable，绝不创建普通 Assistant 冒充 |
| MU-07 | Group 1 个会话/一组 participant IDs；改名、同名、移除成员不能导致作者/消息串线 |
| MU-08 | 手机点击 Assistant/Group **直接**显示 ChatPageView、Composer、聊天记录，不出现任务管理长页或额外 Open Conversation 按钮 |
| MU-09 | Android 键盘、附件、长列表、返回/Sheet 打开不丢草稿/scroll/queue；原普通 Pane Chat 不回归 |
| MU-10 | 群未 @ 不唤醒模型；typed @ 精确目标；@A 与 @B 不串投递，ACL/预算阻止无权限交互 |
| MU-11 | Peer Delegation 与同一 Assistant Worker Task 分离；跨 Assistant 不共享私有 Memory/Prompt |
| MU-12 | 每个 Assistant 创立即有默认 Prompt/role 版本；Memory 不可覆盖更高优先级 Policy |
| MU-13 | MCP 连接注册、按 Assistant 授权、Provider Session 真正可用分别报告，不假显示连接成功 |
| MU-14 | MCP Tool 未授权/服务器断开/Provider 不支持/credential 撤销无法调用；不向手机返回 secret |
| MU-15 | A/B Agent/Provider/Model/Effort/MCP 版本切换不污染彼此；已运行会话不会被未确认设置暗中重启 |
| MU-16 | Goal 承诺有消息 ACK、Plan、真实 Task/Run/Progress、证据/阻塞/Review、最终报告的完整闭环 |
| MU-17 | 结果事件无 Agent 模型参与时只生成系统事实卡，不能冒充 Agent 文字；网络断线不会丢投递/重复发件 |
| MU-18 | 后台 GoalSupervisor 只推进授权 Goal，maxSteps/wakeBudget/quietHours/多设备撤销生效；反无限循环 |
| MU-19 | 手机 Chat 说“每天检查”只创建 Draft，获得 Owner 审批或已有明确政策才启用 Routine |
| MU-20 | Routine 将 owner/requestedBy/triggeredBy/executedBy/reportedBy/source refs 与 Run 精确关联，结果回正确聊天 |
| MU-21 | 旧 automations.yaml 的 Cron/PR Watch 保持；sleep/DST/duplicate event/launch uncertain 无双跑或盲重试 |
| MU-22 | Memory User edit/Agent propose/weekly maintenance 保持区分，Forget/Revision conflict/跨助手保护有效 |
| MU-23 | Android & macOS 真机 Group / Assistant 快切，离线/Resident Host 无 Chat capability 如实门禁；验证测试夹具与实际运行 |

**基本工程约束**：先失败测试再实现；Go 源模块、桌面 Bun、Mobile TS/Gorhom、Host golden/`pnpm sync:fixtures` 与共享 Chat page `pnpm sync:chat-page` 按本项目现有约定实施。对真实仓库/Agent/凭据进行的 E2E 必须使用隔离 Host/HOME/Test repo。现有 `docs/shepherdr-2.1-test-parity.md` 的账本不可被新测试替换。

## 9. 产品样例：真实的一次“你交代给 A → A 会持续回响”

~~~text
你 → 研发助理 A：
“接下来一周改进代码质量。查最近提交、找高风险地方，安排测试和审查；
重大改动先问我。每天下午告诉我进展。”

Assistant A：
“我会先查最近的改动，整理可验证的问题，再分批安排审查和测试。
涉及创建或合并 PR，我会先让你确认。”

系统事实卡：
[已建立目标：一周代码质量改进 · Active]
[今天 17:00 汇报 · Routine Draft / 等待启用]
[计划：筛查提交 → 审查问题 → 测试 → 总结]

A 经 Git MCP 读取最近 commits，判断哪些需要检查。
A 将一个子任务交给自己的 Worker；另一个需要测试助理 B 协作。
[任务卡：审查最近 8 次提交 · 运行中]
[协作卡：已请求测试助理 B · 等待接受]

后续有真实证据：
A：“已经检查了 8 次提交，发现 2 个需要复核的问题；
自动化测试还在跑，我会在得到结果后更新你。”
[查看 Git SHA / Review / 测试 Run]

测试失败：
A：“第一轮测试没有通过，问题集中在登录流程。我已经暂停 PR 创建，
会先补验证；有一个权限变更需要你确认。”
[查看失败报告] [批准/拒绝]

核验结束：
A：“这轮目标的 4 个里程碑已完成 3 个；剩下的跨平台测试仍未通过，
还不能认定全部完成。我下次会在测试结果变化后继续跟进。”
~~~

**终局标准**：用户不是每天打开 Tasks 面板催问 Agent 进展，而是在和自己的个人助理聊天。助理因为有 Goal、Routine、MCP、真实 Task/Run 和承诺台账，可以主动持续推动事情；在合适的时候带证据回来回复用户。所有长期承诺都必须有真实持久记录、已批准的触发与可观察的交付状态。
