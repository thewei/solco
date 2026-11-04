# Assistants — Chat-first UI 规划与已实现代码审计

> 状态：2026-11-04；产品 UI 与服务层的下一阶段改造计划。此轮不修改 Go/React/React Native 实现。
> 技术权威继续使用 `docs/personal-agent-go-mvp-plan.md` 和 `docs/shepherdr-2.1-test-parity.md`。本方案取代旧计划中以 Needs You / Watching / Tasks / Done / Memory 管理标签为 Assistant 主页的设计；不推翻 Go-only 核心、Herdr 唯一 Runtime、现有 ChatView 单源约束。移动端对应 `solco-mobile/docs/assistants-chat-first-mobile-plan.md`。

## 1. 产品重新定位

**一个 Assistant = 一个长期身份 + 一段可以持续交流的聊天 + 它拥有的记忆/目标/权限/能力。**

- 点击侧栏的 Assistant，立即进入 ChatView；不先出现任务后台、六个管理标签或技术名 Coordinator。
- 用户向 Assistant 说话：简单问题直接回答，复杂工作按用户授权分解任务，长期跟进产生可检查的承诺；不把每句话默认拆成 workboard task。
- Assistant 的聊天是主入口；Memory、Goals、Tasks、Activity、Context、Skills、Automations、Workspace、Files、Git、History 都是**聊天可调用的能力**，从对话卡片或顶部「⋯ 更多」进入。
- 保留现有 `Agents`（正在运行的 Provider Agent）和 `Workspace`（真实 Herdr 工作区）概念；不按 Repo 创建 Assistants，也不把一个 Pane 误认为 Assistant。
- 聊天中的后台事件卡从 Go 收据/Run/Rule/Approval 来，带明确来源，不可伪装为模型真实发言。

## 2. 已有代码审计（可定位的事实）

| 实现位置 | 目前发生的行为 | 缺口 / 改造措施 |
| --- | --- | --- |
| `frontend/src/assistants/AssistantsPage.tsx` | 默认 `useState<Tab>("needs")`，六个 Coordinator/Needs You/Watching/Tasks/Done/Memory tabs；顶部有 Watch PR / New Task / Pause 按钮 | **管理面板思维**。主页改成 Shared Chat；这些操作归入 ⋯ Drawer、Composer + 和聊天事实卡 |
| 同文件 `Coordinator` | 只有 Coordinator ready 且当前窗口存在正确 Pane 才显示现有 `<App/>`；否则显示 Start/Stop Coordinator | **把后台技术步骤当主 UX**。未启动也应呈现聊天壳/空态；首次发言通过受控 ensure/start，再准确投递 |
| `internal/nativeui/assistants_bridge.go` | `BindCoordinator → bindChatToCard` 会调用 `selectPane`、`bindLiveAgent`；助手页定时尝试绑定 | **会话/导航耦合**：可能改变当前 Mac 工作区 Pane 选中、全局 ChatBridge 目标。改为 per-Assistant scoped Chat adapter，不自动导航 |
| `frontend/src/App.tsx`、`frontend/src/mygo.ts` | Shared ChatUI 直接使用当前全局 `chat.*` bound service | 需要注入可复用 `ChatTransport/ConversationTarget`，普通 Chat 保持默认 adapter；Assistant 拥有独立目标，不复制 Chat 组件 |
| `AssistantsPage.tsx` 的 `useAssistants` | 每 3 秒调用完整 Snapshot，后端要读取 Overview、Goals、Memory、Approval、Tasks、Runs 等多份 CLI 状态 | 聊天期间造成不必要重拉。聊天使用原 stream/events；管理数据可见时才拉，revision invalidation + 恢复时快照校正 |
| `solco-mobile/src/app/assistants/index.tsx` | 以数值/状态为中心的 Assistant 卡片列表 | 改成联系人/对话列表：头像、名称、可信最近消息摘要或一行真实状态、未读/Needs You |
| `solco-mobile/src/app/assistants/[id].tsx` | ScrollView 里排列 Needs You、Watching、Tasks、Done、Remembers；只能从某个 Task Run 的 Pane 跳到普通 Chat | **没有 Assistant 自身的 Chat**。此页直接复用现有 `ChatPageView` + ChatOps；More Sheet 才显示任务/记忆 |
| `solco-mobile/src/app/chat/[paneId].tsx` | 已拥有可用 `ChatPageView`、`features/chat-web/controller.ts`、发送/停止/问答/附件/语音/Queue 的组合 | 抽出可共享的 `usePaneChatSession`/Chat controller adapter；Assistant 不应复制另一份聊天引擎 |
| `internal/remotehost/personal_agent.go` | Assistant detail DTO 只有 assistant/goals/tasks/approvals/memory，没有 Coordinator 绑定；没有 Assistant scoped Chat resolve/ensure 接口 | 手机上无从查出准确的真实 Coordinator Herdr Pane/Session，**不能靠标题猜**；增加只读 binding + 幂等 ensure |
| `internal/hostd/serve.go` 与 `internal/nativeui/remote_settings.go` | **Go resident Host 已实现**：端口 lease、同一配对身份，GUI 关闭后还能提供 Assistants/Herdr 基础数据；目前**不提供 GUI-bound Chat/History/Git/Files** | 手机可以看到 Assistant 在线/可管理，却不能凭此宣称可以聊天；P1 按 capability 禁用发送，后续单独把 HostConversationService 去窗口化，且必须真机验收 |
| `plugins/solco-automations/internal/personalagent/coordinator.go` | Coordinator 以真实 Herdr Agent/Task/Run 持久存在；Prompt 强调协调/委派任务 | 保留技术执行机制，调整为「会自然交谈的助理」，简单提问无需建 Task；执行仍由 Go 限权 |
| `plugins/solco-automations/internal/personalagent/{memory,context}.go` | 已有候选/批准/纠正/忘记/分享、可见 Memory/context 构建；电脑只显示 Memory 列表，手机只读 | 把 Memory/Context 做成管理面板，暴露来源、适用范围、已注入内容与可撤销性；不再把长期记忆和当前 token context 混为一谈 |

**保留已有资产**：Go Assistant/Goal/Task/Rule/Run/Approval/Memory SQLite + 单 Scheduler、Herdr Agent/Pane/Session Authority、现有 ChatUI、Host v3、手机 Zod/Queries 与凭据、Automations/Git/Files/History/Terminal 现有路由。此次不是把 Go 功能重新造一遍，更不是重返 herdr-projects 旧数据模型。

## 3. Chat-first 界面蓝图

### 桌面

~~~text
Solco Sidebar               Assistant — Chat (同一 ChatView)
Agents                      ┌──────────────────────────────────────────────┐
Assistants            [+]   │ ✨ Release Scout · Following 2 things  [⋯] │
  ✨ My Assistant            ├──────────────────────────────────────────────┤
  ● Release Scout    [1]    │ You: 帮我留意 PR #42，有变化告诉我。           │
  ✨ Code Maintainer         │                                              │
Workspace                   │ Assistant: 没问题，我会继续跟进。             │
                            │ [👁 Following PR #42 · next check…] [详情]  │
                            │                                              │
                            │ [Needs approval · View Diff · Approve/Deny]   │
                            │                                              │
                            ├──────────────────────────────────────────────┤
                            │ [+] Ask Release Scout…              [Send]   │
                            └──────────────────────────────────────────────┘
~~~

- **打开立即是 Chat**。原 Composer、Thinking/Tools、Question Dock、Queue、Attachment、Voice、Model/Context/Skills 能力全部保留；不用再点 Coordinator tab。
- Header 只显示助手名称、头像、精简真实状态、⋯。重要审批可以显示轻量提示或聊天内卡片，绝不恢复成七八个常驻 KPI。
- 创建 Assistant 后直接进入空 Chat：显示建议语句「帮我规划今天」「关注一个 PR」「记住这个偏好」，但**不会自动发送消息或启动 Agent**。
- 每个 Assistant 都有自己独立的草稿、滚动位置、Queue/Outbox 与未读数。右侧 More Drawer 关闭不应卸载当前 Chat。
- Chat 中的 Watch/Task/Approval/Memory “事件卡片”必须来自真实 Go 业务事件/收据，带 sourceRef、资源 ID、版本和时间；它们是系统状态，不是模型发出的 message。
- 显式「Open in Workspace / Terminal」才导航当前 Mac 的 Herdr Pane；只查看某 Assistant 的 Chat 不改变 Mac 当前工作区焦点。

### 手机

- `/assistants/index` 是**对话列表**：头像/助手名 + 可信最近一条消息摘要（若无源，显示「Following 2 things」「Needs You」，不能编造）+ 未读。创建后直接进入聊天。
- `/assistants/[id]` 是**ChatView**：Back、名字/状态、⋯；Body 继续用 `src/components/chat/chat-page-view.tsx` 加现有 Host Chat Stream；底部保留原有 Composer/语音/附件/问题卡。
- 右上角 ⋯ 弹出 Gorhom Bottom Sheet/Tamagui 现有模式，复杂详情再 push。来回切换不丢 Composer 草稿、scroll、未答卡，不另建 WebView 页面源。
- 全局 Needs You 收件箱保留；点进去优先跳回对应 Assistant 的 Chat，并定位事实卡/审批卡。需要较大 Diff/配置时打开原有功能页。

### 「⋯ 更多」产品菜单

| 名称 | 内容 / 可复用功能 |
| --- | --- |
| **About Assistant** | 头像、名称、职责、风格、重命名、暂停/恢复、通知；Coordinator 技术细节放 Advanced |
| **Memory** | What it remembers / Suggested memories / Sources / Correct / Forget / Share；复用 Go Memory 状态机 |
| **Goals & Follow-ups** | 长期 Watch、目标、下次检查、暂停/恢复；复用 Goal/Commitment 和原 Go Scheduler |
| **Tasks & Activity** | Running / Needs You / Done、Worker、Run 历史、Diff/Review、真正的 Pane/Chat 跳转 |
| **Context & Workspaces** | 当前会话用到的 Memory 版本/来源、关联 Workspace、可使用的文件和 Skills、可获知的 Context 用量 |
| **Capabilities & Tools** | Skills/Agent Catalog、Git、Files、Browser、Terminal、Remote 等现成能力（服务端 capability-gated） |
| **Automations** | 与该 Assistant 关联的规则；高级编辑跳转原 Automations 页面，**不能建第二个 Scheduler** |
| **History & Settings** | 实际 Session History、Resume/Context Transfer、通知/导出与删除确认 |

**P1/P2 优先做前四项**。尚未有可用 Host API 的按钮必须禁用或展示只读能力，不做能点却没有执行的空菜单。


## 4. 最关键的技术架构：Assistant 不是某个 Pane

~~~text
AssistantID (长期身份，用户在列表中看到)
 ├─ AssistantConversationID (稳定的逻辑聊天入口/关联索引)
 │   ├─ current Herdr SessionRef + PaneID + ProviderSessionRef (真正聊天事实)
 │   └─ historical source refs (仅关联历史，不复制 Provider transcript)
 ├─ Goals / Commitments / Rules / Tasks / Runs (Go daemon 权威)
 ├─ Approved Memory + pending candidates (Go)
 └─ ContextSnapshot + Permissions (每次受控构建的来源/版本快照)
~~~

### 4.1 AssistantConversationResolver（唯一解析源）

- 基于当前 `coordinators` 表、真实 Herdr Run/Session/Pane/AgentSessionID 建立 **assistantId → 当前可对话的真实 source** 解析服务。Coordinator 重新启动或换 Pane 时 AssistantID/对话入口保持稳定；逻辑 ConversationID 不是新的 PTY/聊天数据库。
- **不允许**根据 Assistant 名字、Repo 目录、最后选中的 Pane 或 Task title 猜测会话；所有绑定必须验证 assistantId、Herdr instance/session、PaneID、Provider AgentSessionRef 与当前有效性。
- `none / stopped / failed / offline / uncertain` 仍进入 Chat 壳并保留准确的先前信息。历史只从现有 Herdr/Provider 官方读取源、经原 `chatsession`/HostConversationService 投影；没有可靠 transcript 时给真实空态，**绝不凭 Go Run 简报伪造旧聊天内容**。
- 第一次发送：如果没有会话，**该次明确 Send 就是用户的启动意图**。后端持久 `requestId`/payload hash，执行 ensure/start → 查实 Herdr source → 原消息一次性 Send。启动成功而响应丢失必须先按实际身份对账；`uncertain` 时停止自动重试，保留草稿与故障状态，不生成第二个 Agent。
- 修改 CoordinatorPrompt：可以正常聊天/咨询，只有确需行动才创建任务。结构化 Goal Draft、PR Watch、写文件/合并仍由 Go 的权限与审批决定，不能由 Agent 口头授权。
- 必须确认 Assistant 处于暂停时，聊天发送是否允许。建议**暂停只停止后台派发，不自动阻止用户主动发言**；真正 Disabled/权限撤销才禁止新执行。产品语义与 Go 状态机一致后再上线。

### 4.2 ChatTransport / scoped Chat bridge

当前桌面 `bindChatToCard` 调 `selectPane`，将“打开助手对话”与“切换 Mac 当前 Workspace Pane”绑定。**重构必须先解除这个隐式副作用。**

- `frontend/src/App.tsx` 从全局 `chat.*` 抽象出**可注入、同形状的 ChatTransport/ChatContext**；普通 Workspace Chat 默认仍走现有 ChatBridge，Assistant Chat 使用 assistantId-scoped transport，只查询/发送它的真实会话。无另一个 Chat 组件、无第二份转录存储。
- 每个 Assistant 的 controller/订阅以 `assistantId + bindingRevision + sourceRef` 为 scope；A→B 切换时丢弃 A 的晚到帧，停止旧订阅，重置到 B 的权威快照。会话草稿、Queue 的 key 含稳定 Assistant/Conversation key，不能按全局 Pane 重用。
- Desktop 不借 `selectPane` 解决 Chat binding；仅显式 Open in Workspace/Terminal 才触发导航。是否能完全重用现有 ChatBridge，需要先用 failing test 验证其全局 state ownership；必要时为同一 Host Conversation Service 新建 page-scoped binding，但不复制业务 Engine。
- Mobile 不复制 `chat/[paneId].tsx` 的 ChatOps/Controller 实现；提取复用的 `usePaneChatSession`/`ChatSessionView`，以已解析的 Coordinator Session/Pane 作为目标，复用 `ChatPageView` 和原 Send/Stop/Answer/Attachments/Voice/Queue。
- App 的 Activity/Notification 使用独立 event projection，不直接写进 Provider transcript；后台目标事件卡要明确类型为“Assistant activity / 自动跟进”，用户才知道不是 AI 发出的消息。
- 性能：Chat 用既有 event stream，Assistant summary 用 revision invalidate；Goal/Memory/Tasks/Inbox 明细**只有 Drawer 打开才拉**，页面恢复/WS 掉线后补一次 authoritative snapshot。替换现在每 3 秒完整 `assistants.snapshot()` 的方案。

### 4.3 Host v3 最小新增（兼容当前手机配对/旧 Host）

**现状**：`GET /api/v3/personal-agent/assistants/{id}` 返回 assistant/goals/tasks/approvals/memory，手机无 Coordinator 的准确 Pane/Session 绑定。建议新增：

| API 草案（当前均未上线） | 职责与安全边界 | 批次 |
| --- | --- | --- |
| `GET /api/v3/personal-agent/assistants/{id}/conversation` | 返回逻辑 conversationId、`unstarted/starting/live/blocked/offline/uncertain/stopped`、revision、**真实** Herdr instance/Session/Pane/AgentSessionRef（只有核实后才给）和可用能力 | P1 |
| `POST /api/v3/personal-agent/assistants/{id}/conversation/ensure` | 带 requestId 的显式/首次发送启动，已存在则复用同一 binding；事务不确定先 readback，绝不盲目重复 | P1 |
| `POST …/conversation/send`（视需要） | 只有原 `/panes/{id}/chat/send` 无法保证 per-Assistant scope/身份重验时才引入；内部调用**同一个** HostConversationService/Queue，不创第二队列 | P1/P2 |
| `GET /api/v3/personal-agent/assistants/{id}/context` | 当前真实注入的 Memory 版本/来源、可用 Workspace/Tools、预算；不泄漏系统私有 prompt/secret | P2 |
| `GET/POST …/assistants/{id}/memory/…` | 列表/候选/approve/correct/forget/share/unshare，复用已有 Go Memory 和权限/修订 | P2 |
| `GET …/assistants/{id}/activity`（必要时） | 带 ID/sourceRef 的 Goal/Run/Watch/Inbox 事实卡摘要，分页/增量读取，非第二份 Chat transcript | P3 |

- bootstrap 可增加可选 `assistantConversation` / `assistantMemoryManage` capability，旧 Host 不认识时界面如实提示/按已有功能降级，不要求重新配对。**已有 `internal/hostd` 在 GUI 退出后提供 Assistant 管理，但暂不提供 Chat**；即使 Assistant list 在线，也只显示聊天 unavailable/草稿保留。将 Chat 服务真正迁入 resident Host 必须另做独立可验证的 Go HostConversationService 迁移，不能混入 UI 的“已完成”状态。客户端权限来自当前 device scopes；不能 UI 判定能 send 后跳过服务端 Assistant policy。
- ensure/send/answer 都验证 assistantId、设备权限、SessionRef 与 revision；写入继承 `requestId + payload hash`，审批保留 `expectedRevision + actionHash`。设备撤销必须即时生效。
- Go personal DB 只持有逻辑 Conversation/source ref、活动/记忆/策略；完整 Chat turns、Composer Queue、交互选项继续从原 Host/Herdr/Provider authoritative 数据源读取。业务活动与真正 Agent 回复单独渲染/注明来源。

### 4.4 Memory 和 Context 要分开管理

- **Memory**：长期的 candidate/approved/forgotten，用户能查看「记住了什么」「来源是什么」「何时/对谁生效」，并纠正/忘记/授权共享。其他 Assistant 默认不能读取。
- **Context**：本轮 Agent 真正被注入的 approved Memory IDs + revision、Workspace/File/Skill/Tools scope、最近受控任务摘要源、模型上下文占用（仅有真实 Provider 数据时显示）。通过后端 `ContextBuilder` 生成带来源/预算的 ContextSnapshot，禁止臆造 token 数。
- Forget/Correct 的新版本必须影响**之后的受控上下文构建**；过去已送入运行中的模型上下文的事实不可保证被追回。必要时建议明确「用更新后的记忆开始新会话」操作，且不假称当前 Provider 已忘记。
- 把 Memory/Context 的权限检查放在 Go service；UI 只消费投影，不从 transcript 自动把用户闲聊变为已批准长期记忆。

## 5. 分批实施顺序（不依赖完整后端 E 重做）

| 阶段 | 代码切片 | 必须有的实测效果 |
| --- | --- | --- |
| **P0 · 合同与失败测试** | 审核 ChatBridge 全局 binding、Host 缺 assistant conversation seam、双客户端 Chat 单源约束；补 CX 用例 failing-before | 有证据说明目前为何不是 Chat-first，新增 DTO/golden 设计，不动用户数据 |
| **P1 · Chat-first 首版（最高优先）** | Desktop /assistants 默认 Chat、scoped binding；Mobile /assistants/[id] 直接共享 ChatPageView；首次 ensure/start/send；清晰的空态/错误/离线 | 打开 Assistant 直接聊天；A/B 切换不串会话；不影响 Mac 当前 Pane 焦点；旧普通 Chat 功能不退化 |
| **P2 · ⋯ 更多能力中心** | 旧六大管理 tab 拆入 Drawer/Sheet；Memory approve/correct/forget/share；Context read-only；复用现有 Git/Files/Automations 等路由 | Chat 与 More 来回草稿/阅读位置不丢；危险操作经过后端批准；无空菜单 |
| **P3 · 主动 Assistant 信息流** | Go authoritative Activity summary、消息中的 Watch/Task/Needs You 卡、收件箱定位、后台通知 | 用户看到助手主动跟进结果，卡片附有真正 sourceRef/Run/Pane/Diff，不伪装模型发言 |
| **P4 · 打磨与完整交接** | Assistant persona prompt、自然语言意图→草案、搜索/历史/恢复、性能、大量任务、Android/macOS 低网/重启 | 真机与回归矩阵通过，无第二 scheduler/runtime/chat store |

**避免一次性重构全前端**。P1 只改身份解析/Chat 默认体验和少量 bridge；P2 才迁移各管理功能；P3 才引入完整活动时间线。已有 Go Task/Memory/Rule 的 CRUD 不重写。


### 建议按文件实施（现有路径 → 动作）

**桌面**：
- `frontend/src/assistants/AssistantsPage.tsx` → Chat-first 页面壳；现有 Cards/NeedsYou/Watching/Tasks/Done/Memory 提取到 More Drawer 子页；`assistants.css` 只维护 Header/Drawer/事实卡，不再设置完整仪表盘分栏。
- `frontend/src/App.tsx`、`mygo.ts` → 同一 Chat source 支持 transport/context 注入；默认 Pane Chat 无行为差异。
- `internal/nativeui/assistants_bridge.go` → `ConversationBinding/EnsureConversation` 与 page-scoped Chat adapter；彻底移除 `BindCoordinator` 的隐式 `selectPane`；`assistants_page.go`、`assistants_section.go` 保持既有导航/树形 UI。
- `internal/personalagent/client.go`、`plugins/solco-automations/internal/personalagent/{coordinator,context,memory}.go` → 逻辑 Assistant 会话状态/准确 source ref、受控启动/恢复、角色 prompt 与 Memory 上下文投影。
- `internal/remotehost/{personal_agent,server}.go` → Host v3 Assistant Conversation/Memory/Context gateway，先产真实 Go golden，再同步手机。

**手机**：
- `src/app/assistants/index.tsx` → 联系人/会话列表；`src/app/assistants/[id].tsx` → Chat 主体，More Sheet 和次级路由。
- `src/app/chat/[paneId].tsx` → 提取共用 ChatOps/订阅/Controller hooks，给普通 Pane Chat 和 Assistant Chat 复用；原 `src/components/chat/chat-page-view.tsx`、`src/features/chat-web/*` 保持唯一聊天渲染/协议。
- `src/contracts/host-v3.ts`、`src/lib/host-client-v3.ts`、`src/features/host/queries-v3.ts` → 新增可选 capability + Coordinator target schema/typed API + query invalidation；严格按 Mac Host golden 同步。

## 6. 必须写的测试与验收

| ID | 场景 | 期望 |
| --- | --- | --- |
| **CX-01** | macOS 点击任意 Assistant | 主视图直接 ChatView，六个管理 tab 不在主页 |
| **CX-02** | 刚建 Assistant、未启动 Coordinator | 仍有 Chat 空态和 Composer；首条 Send 引导受控 ensure 并只发一次 |
| **CX-03** | Assistant A/B 连续快切 | transcript、draft、Queue、Outbox、Memory/Activity、late WS 回包不串助手 |
| **CX-04** | Mac 在 Workspace Pane X 操作时进入 Assistant | 当前 Herdr Tab/Pane 焦点不发生隐式改变 |
| **CX-05** | Android 选一个 Assistant | 直接共享 ChatPageView，不需要先找 Task Run |
| **CX-06** | Coordinator stopped/offline/uncertain | 仍是 Chat 壳，历史只取真实 source，错误、可发送能力与恢复结果准确 |
| **CX-07** | Mac/Phone 重复同 requestId，创建/发送时网络丢回复 | 只有一个 Coordinator/一份 Send，uncertain 不盲目重试 |
| **CX-08** | Watch/Task/Needs You 卡在 Chat 中出现 | 有 Go event/sourceRef、真实身份/版本；审批必须精确 actionHash 幂等 |
| **CX-09** | More Memory Correct/Forget | 后续 ContextBuilder 不用已删除文本；不能虚称已撤回过去模型 prompt |
| **CX-10** | More Goals/Tasks/Context 往返 | Chat 草稿/阅读位置/未答问题与实际 Provider session 不丢 |
| **CX-11** | 无新 Agent 发言但后台 Watch 完成 | 显示真实 Activity 卡，不伪造成模型聊天回复 |
| **CX-12** | 普通 Workspace Chat / 手机生成快照 | Send/Stop/Answer/Queue/Attachments/Voice/Subagent/Review 保持正常 |
| **CX-13** | 旧 Host / 无 assistantConversation capability / Relay 断线 | 不能瞎绑最近 Pane 或自动创建会话，显示准确不可用/离线 |
| **CX-14** | 两手机/撤销设备/多 Host 或实例 | 不能将 A 的消息送进 B 或另一台 Mac；已撤销设备立即拒绝 |
| **CX-15** | Assistant 有 50+ Tasks/长历史/多个 Worker | Chat 无每 3 秒全量 CLI 重拉，Drawer 按需加载，恢复时快照纠偏 |

- **UI 编译与回归**：根 Go `GOTOOLCHAIN=auto go test ./...`；插件独立 module `cd plugins/solco-automations && GOTOOLCHAIN=auto go test ./...`；Mac `cd frontend && bun run build && bun run build:mobile`；Mobile `pnpm typecheck` / `lint` / `test` / `check:fixtures` / `check:chat-page` / `export:web`。
- **双端同步**：改变 `frontend/src/App.tsx` 或 Chat 共享源码后，手机 `pnpm sync:chat-page`；先 Mac Host 真实 Go Golden，再手机 `pnpm sync:fixtures`，不可手改生成物。
- **实机**：使用隔离 `HOME` + Herdr Session + scratch repo 运行 macOS MyGo WKWebView，Android Development Build 验证 Chat 真实焦点、输入、切换与启动幂等；单元测试通过不等于 UI 真机通过。不能对用户活跃的会话/工作树做破坏性测试。
- **与 2.1 测试的关系**：源 `docs/shepherdr-2.1-test-cases.jsonl` 的 Go 状态机/安全测试继续按 A–E 完整追踪；**CX-01–CX-15 是新的产品体验测试**，不能用后端 TDD green 替代。若需要加入现有自有必测台账，必须显式追加有目标的产品测试条目，不篡改固定上游清单。

## 7. 禁止的捷径与非目标

1. **不能仅把默认 tab 改成 Coordinator**：手机缺专属聊天，电脑存在 `selectPane` 的绑定副作用，不能算完成。
2. **不能把 AssistantID 当成真正 PaneID**，也不能根据名称、Repo Path、最近活动 Pane 猜会话；不能复制 Provider transcript 到新的 Go 消息表。
3. **不能把 Go Run/Rule 事件冒充为 Agent 自己的聊天句子**；事实卡和模型消息要显式区分来源。
4. **不能通过打开聊天悄悄启动任务**；只有明确 Send/Start/已批准 Watch 才可调度。没有实际运行能力就要显示阻断和可重试状态，不假回成功。
5. **不能静默绕过权限**：内置 Helper、Git Merge、Memory Share、设备授权依旧由后端 scope/revision/approval 审核；无 OS 沙盒时不得虚报无限权限隔离。
6. **不能用删除 Memory 暗示过去发给 Provider 的上下文被追回**。仅能保证后续受控注入变化，必要时告知用户新开会话。
7. **不能替换原 Agents、Workspace、Automations、History、Terminal、Chat 或 Remote Desktop** 的独立入口。这些能力在 Assistant 内被调用而非被 Assistant 覆盖。
8. **不能要求为 UI 更名重新配对手机**、修改旧 Assistants/Goals/Run ID、删除用户既有数据库或插件历史。

**建议最先实施：P0 契约与 failing-before → P1 Desktop+Android Assistant Chat-first，并锁死 session/Pane scope。先达到“用户点开就能聊”，再进入 More 管理能力。**

## 8. 2026-11-04 追加：长期助理协作网络与调度中心

**聊天产品关系与 UX 主规范为 [`docs/assistants-conversation-product-spec.md`](assistants-conversation-product-spec.md)**：`Assistant` 是联系人，`Conversation` 有 `human_dm/peer_dm/group` 三种，一对一 A↔B 不要求先建群；Sidebar 混合人↔助理私聊、助理↔助理私聊、群聊；`@` 在 A 私聊表示“请 A 联系 B”，在 Group 才表示“让 B 本群回复”，Group 默认只有被 @ 才唤醒。产品批次 UX0–UX6 以此为准。

**Go Directory/Mailbox/Herdr Skill/Delegation 实施领域规划见 [`docs/assistants-collaboration-plan.md`](assistants-collaboration-plan.md)**。本聊天优先设计不变：**点击任意 Assistant 仍然默认 ChatView**；但是助手可以通过 Solco 受控 Directory、持久 Mailbox 与现有 Herdr Skill/TaskGraph/Automations 发现、联络并协作。对用户开放单一的「允许其他助理发现我」（默认关），高级项独立决定公开资料、接受消息、接受委派、分享记忆/进度。共享 Memory 仍必须是已有 Go MemoryItem 的显式授权，不能因为公开就全量泄漏。

- **新增二级 Control Center**：从 Assistants 标题处打开；Owner 看全部 Assistant、Task DAG、跨助理 Delegations/Threads、Worker Runs、Automations、审批、状态/成本、授权关系，可调度和追踪。**它不是单个 Assistant 的 Chat 首页**。
- **新增聊天中的协作事实卡**：跨助理请求/回复、委派与 Worker 进度、自动化结果会显示在所属 Assistant 的主 Chat；Go 事件与真实 Provider transcript 分开，不假冒 Agent 发言。
- **Skill 角色**：当前 Herdr Skill 具备真实 Agent `get/read/prompt/wait` 等能力，但不具备持久 Assistant Directory/ACL/Mailbox。计划新增 Solco-owned assistant-network Skill 和 Go facade，后者才提供持久授权/消息收据；同 UID macOS shell 未沙箱化的限制必须如实披露。
- **阶段依赖**：Chat-first P1 先实现安全会话与聊天；Network N0–N6 再按 Directory → Mailbox → Delegation/Worker → Rule/Continuity → Control Center → 可靠性测试推进。Mobile 由 `solco-mobile/docs/assistants-chat-first-mobile-plan.md` 的 N1–N6 章节对齐。
- **测试**：原 CX-01..15 全部继续适用；新增 NW-01..22 协作/权限/恢复测试，不修改 Shepherdr 2.1 静态源索引。

