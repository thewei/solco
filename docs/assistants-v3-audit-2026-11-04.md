# Solco Assistants 3.1 — 双端代码目标符合度审计（2026-11-04）

> **审计性质**：截至本次读取的桌面 `main@4b0ce58`、手机 `master@f1cbd28` 工作树的**独立静态代码审计 + 非破坏性测试**。未新建真实 Assistant/Group、未修改生产 SQLite、未部署客户端；此前真机验收使用 `docs/assistants-v3-implementation-tracker.md` 的记录，**不作为本轮重新实机复验**。
> **产品目标**：`docs/assistants-messenger-agent-v3-1-plan.md`（U0～U5 / MU-01～MU-23），手机 `solco-mobile/docs/assistants-messenger-agent-mobile-plan.md`。此报告按**功能存在 / 自动化测试 / 用户端到端体验**分别评分，不把台账的开发任务完成率当作全部产品验收。
> **用户代码安全**：未更改运行时代码、没有暂存/提交/推送/重置用户工作树，本文件为文档交付。

## 1. 一页结论

- 实施台账：`22/22` 个非延期任务标记 VERIFIED，另 `TODO-WF` DEFERRED；这描述开发阶段，不是最终产品可用性。
- **MU 产品验收：5 VERIFIED / 15 PARTIAL / 3 NOT VERIFIED**，即只有 **5/23 完全完成验收**（台账 `2` 的真实状态；这个比例不能解读为功能只写了 22%）。
- 本轮运行：独立插件 `go test ./...` PASS；Root `go test ./internal/personalagent ./internal/remotehost ./internal/hostd` PASS；Native UI 专项 `-run Test(Assistants|Avatar|Assistant|Group|Coordinator|Persona|MCP|Memory|Behavior) -count=1` PASS；Desktop `bun run typecheck`、`bun run build`、`bun run build:mobile` PASS；Mobile `pnpm typecheck`、`pnpm lint`、`pnpm test`（583 passed, 2 skipped）、`pnpm check:fixtures`（178 一致）、`pnpm check:chat-page`、`pnpm export:web` PASS。
- **未运行**完整 `go test ./internal/nativeui`（耗时）、本轮 Mac App/Android 原生安装、真机 / 容错长跑；不能将以上数列的 PASS 延伸为未执行的门禁。
- **判断**：U0 基础能力真实落地；U1 真实 Group 和已启动 Assistant Chat 可用，但首次聊天和身份 @ 仍有缺陷；U2 单 Provider MCP 方案有实践证据但非泛 Provider/Tool ACL；U3–U4 真实 Go 核心与事实账本存在，但**消息式主动结果回报/跨 Assistant 任务协调未形成完整用户体验**；U5 记忆 DB+文件与策略存在，手机没有文件树、整理尚不完整。

## 2. U0～U5 静态核查矩阵

| 阶段 | 独立核实的已实现部分 | 本次仍未达到的目标 / 验收风险 |
| --- | --- | --- |
| **U0 UI** | `internal/nativeui/assistants_avatar.go`、`assistants_section.go` 的圆形 Assistant 头像、Group 叠头像、Workspace TreeRow 对齐和 + 的两个菜单项；`assistants_intent.go` 的可靠 intent+ACK；React `NewAssistant` await / 持久 requestId；手机 `assistants/index.tsx` 的两个菜单和创建 Sheet | 图片头像、浅色/长名称等未做完整视觉验收；新表单的智能体 Preset 与初始指令选择 UI 不全；手机首次打开仍可能落管理长页 |
| **U1 Chat/Group** | Go `group.go` SQLite Store、`group_pump.go` 精确 ID delivery、Group Agent 回报、Host `personal_agent_group.go`、桌面 Group Chat、手机 `group/[id].tsx`；已有实际 Coordinator 时手机自动跳 Chat，真机记录验证与 Claude 对话 | **重名 @ 误投递风险**；Groups 使用另一套 RN FlatList/Input 而非单源 Shared ChatView；Assistant idle/starting 先出现管理页与 Start 按钮；没有长期 Peer DM/Assistant↔Assistant Delegation |
| **U2 Persona/MCP** | `persona.go` 默认 rolePrompt + revisions，`mcp.go` 从 Claude/Codex 配置发现 Server，per Assistant server grant、Claude strict config + connected probe；`style/[id].tsx` 支持 Prompt/Agent/Model/Effort/MCP | MCP 只有服务器级授权，不做真实 Tool 白名单；运行绑定仅支持 Claude，Codex/其他 Provider 明确 unsupported；不是完整跨 Provider MCP 市场/连接器；不能以 Go 业务 ACL 伪称 OS/原生 Provider 工具强沙箱 |
| **U3 Goal** | `goal_plan.go`、`supervisor.go` GoalPlan、里程碑、受控 TaskGraph、事实账本、预算/退避、核验后 `CompleteGoal`；桌面 `FactStrip/PlanCard` 和手机 `FactsStrip/GoalPlanBlock` | 事实卡**不等于 Coordinator 向聊天发起有来源的主动自然语言总结**；系统事件放在 Assistant 页面管理层，没有通用 scoped Conversation outbox/replay、跨设备已读/对话报告；手机真实“目标指令→Agent 完成里程碑→报告”端到端未验证；Peer Delegation 未实现 |
| **U4 Routine** | `routine.go` Draft/Approval hash/启用由单 Go Automations Scheduler、事件回归为 owner 的 `routine_done/failed` 事实；CoordinatorPrompt `routine draft` CLI 指导；手机 Routine 只读列表 | “用户自然语言提出周期任务→实际 Agent 创建 Draft→人批准→执行→Assistant 聊天回复”全链路未实测；报告不在聊天作为 Agent message，owner/requestedBy/source 有但完整 triggeredBy/executedBy/reportedBy 不齐；手机不能直接管理 Routine |
| **U5 Memory/Proactivity** | `memory_files.go` facts/inbox/notes/INDEX/README 投影、冲突/修订；`proactivity.go` level、QuietHours、报告间隔、dailyBudget；手机 `memory/[id]` 可增改忘记及查历史，`behavior/[id]` 可调行为等级 | 手机 Memory **没有虚拟文件夹浏览/文件编辑**（源码明确 “there is no file tree here”）；每周模型辅助整理未实现；主动消息仍以 facts/report UI 为主，非 Chat Outbox/实际主动消息通知；手机添加记忆提交失败有丢输入风险 |

## 3. 必须修复的可定位问题（不要只修文案）

### P0-01 — 群聊重名/前缀 @ 路由可能误唤醒助手（ID 信任缺口）

- 证据：手机 `src/features/host/group-model.ts` 的 `mentionsIn` 是 **遍历成员 Name 正则**提取所有匹配 ID；`applyMention` 仅插入 `@Name` 文本。两个成员重名时，发 `@Ada` 将提取两个 ID，可能双投递。即使通过选择器点击了其中一人，输入里也没有稳定的 ID token。
- Go `plugins/solco-automations/internal/personalagent/group.go:470–483` 验证 `strings.Contains(in.Body, "@"+m.Name)`；对 `@Bobby` 请求附带 `Bo` 的 ID，该包含检查也能通过。虽然手机目前匹配 `@Bobby` 时有前缀保护，**服务端才是权威边界，不能依赖手机始终正确传 ID**。
- 优先修复：Composer 产生绑定 `assistantId` 的 typed mention entity；Go 收 `mentions[{id, span/tokenId}]` 并校验该实体在当前 group、版本与消息本体位置；同名必须按用户选择的 ID 区分，纯文本粘贴不可默默得到另一个 ID；增加 Mobile + Go 混淆用例（重名、`@Bo`/`@Bobby`、更名后、跨群、复制粘贴）。

### P0-02 — 手机个人助理首次聊天仍先呈现管理页

- 证据：`solco-mobile/src/app/assistants/[id].tsx:146–159` 仅在 `convo.kind === "open"` 且 Pane Agent 已识别时 `router.replace(chat)`；`:181–339` 仍是大段 `ScrollView`（Conversation Card/Start conversation/Needs You/Watching/Tasks/Done/Memory）。
- 结果：空闲/启动中/远端不可绑定状态**不是可输入的 Assistant Chat 壳**，用户还需点击 Start conversation，管理页与聊天主入口倒置；如果 Provider Chat 不可用，提示正确但仍应保留空会话、草稿、启动与 retry 状态。
- 修复：`/assistants/[id]` 的默认渲染直接是复用 `ChatPageView` 的 assistant-scoped ChatShell；对输入创建 idempotent ensure/send；More Sheet 承载所有管理内容。`manage=1` 可保留深链但必须是明确的管理入口。

### P0-03 — Goal/Routine “事事有回响”还不是真正的聊天闭环

- 证据：`supervisor.go:11–13,254–266` 只写 `goal_facts`，`routine.go:294–306` 只写 Routine Fact；桌面 `AssistantsPage.tsx:278` 在 Chat 外渲染 `FactStrip`，手机 `assistants/[id].tsx:271` 在管理长页渲染 `FactsStrip`；缺少与原 Assistant human_dm / Group 可回放的受控报告消息交付，`reportConversationId` + delivery ack/retry 机制未闭环。
- 风险：系统可以宣告 “Done: … verified”，但**用户不会在聊天时间线中收到 Coordinator 的真正阶段性解释/最终回复**；手机关页时事实还在数据库，却不等价于按时主动告知。既要保留真实 system fact，又要让模型在授权的 Report Turn 中审视证据做总结，输出 `author=Assistant` 的真实消息，失败出系统卡与可重试 outbox。
- 修复：durable `ReportOutbox`（assistantId, goalId/routineId, eventId/correlationId, conversationId, sourceRefs, attempts, state）；真正的 Coordinator Report Turn 与原对话绑定，允许人工审批/预算与 QuietHours；无人处理时也必须给 Owner 可达的系统通知与收据。覆盖 offline/restart、多客户端、重复事件、自然语言模型输出与事实分开。

### P1-04 — Assistant↔Assistant 私聊委派未实现

- 证据：台账 MU-11 NOT VERIFIED，Go 有 Group Delivery 和同 Assistant TaskGraph，但没有长期 Peer DM + Directory/Grant + 受限跨 Assistant delegation/results。群聊 @ 是用户直接叫成员，不等于“助理 A 自己委派给助理 B，再汇总回原会话”。
- 修复：按 `assistants-collaboration-plan.md` 引入授权 Peer DM / Mailbox / Delegation，清楚地区分 Assistant Identity、Worker Run 与 Group。

### P1-05 — Mobile/desktop Group 使用第二套 Chat 呈现

- 证据：手机 `assistants/group/[id].tsx` 自己实现 FlatList + Input + 消息 Row；单聊 `chat/[paneId].tsx` 使用 `ChatPageView`。前者确实能聊，是有效 MVP，但**违反“单源聊天 UI + scoped transport”的长期架构目标**，分叉后会丢语音/附件/Slash/Queue/统一草稿行为。
- 修复：抽出共享 Chat 渲染/Composer 接口 + `ConversationTarget={kind:assistant|group|pane, id}`，按 Host capability 展示能真实支持的功能。短期先修身份/首次聊天/草稿恢复，避免为了统一 UI 贸然重写可靠的 Group Store。

### P1-06 — Mobile Memory 新增失败可能丢草稿

- 证据：`solco-mobile/src/app/assistants/memory/[id].tsx:80–99`：点击 Remember 后调用 `write({kind:'add',content:text.trim()})`，紧接着无条件 `setText('')`；若断网/服务器拒绝/超时，文字不在编辑框，不能按目标保留用户输入。
- 修复：只有 mutation onSuccess 才清空；失败保留草稿和同一 requestId 的安全重试，添加页面级 failure + reconnect 用例。

### P1-07 — MCP 授权模型应明确能力边界，不声称已做到 Tool 级强隔离

- 证据：`mcp.go:20–27` 源码明确整个 Server 授权，Tool allowlist 无法由当前 Provider 启动旗标强制执行；`style/[id].tsx` 也仅允许/撤销 Server。Claude `--strict-mcp-config` 的确做 Server 隔离，但其他 Provider 当前不支持该路径；用户的“插件统一 MCP”核心方向已落实到 Claude-only 版本。
- 修复：在 UI/文档标记 scope 是 Server-level；对于要求不同权限的两个 Assistant，只有真正能够 enforcement 的 Provider/Broker 才展示 Tool-specific policy，不能只在 Go 配置表里填个字段说已阻断。

### P2-08 — 产品体验与验证缺口

- 用户可上传头像、手机 Memory 虚拟文件树、每周模型整理、群聊未读与草稿恢复、Android 键盘/附件/切后台、Desk light/long-name 实测、Group 设备撤销/Participant ACL、sleep/DST/uncertain launch 长时间重放均未完整验收。
- 手机 Chats 目前将 Groups 全排在 Assistants 前，再各自排序（`assistants/index.tsx:47–48,103–131`），**还不是真正置顶/最近活动混合排序**；行缺有依据的最近消息预览与未读统计。
- Desktop Coordinator 的启动 Prompt 本身会成为 Provider Transcript 的首条长用户消息（台账明确现象），需要未来处理 scoped Chat 的 welcome/system setup 以免显示 152 行系统工作说明。

## 4. 下一轮真正的“目标达成”验收顺序

1. **P0 先行**：重名/前缀 typed mention 与服务端一致性测试；手机 Assistant 空态就能直接 Chat/首次 Send 自启动；Goal/Routine ReportOutbox→真实 Assistant Chat→Phone + Mac 的持久交付。完成前不宣布 Messenger/Proactivity 主目标全部达成。
2. **P1**：Peer Delegation/受限协作、群聊与共享 Chat 统一策略、Memory 失败保留草稿、MCP Server 授权边界及 Provider status；补足 Android 真机断网/后台/草稿/输入法。
3. **P2**：助手头像图片、混合最近会话/真正未读、手机文件树、模型辅助 Memory maintenance、浅色/长名/低内存/低网络质量验收。
4. 对每个 MU 项新增 `RED→FIX→GREEN` 测试并在同一个 tracker 明确记 `PASS`、`PARTIAL`、`NOT VERIFIED` 和真实操作证据；可复现的 UI 行为需真机/Computer Use 测试，单测不能替代。
5. 保持唯一 Go Scheduler / Herdr Runtime / SQLite 数据权威 / Shared Chat 架构原则；Workflow Go 框架仍为 TODO，不阻塞这些产品缺口。

## 5. 本轮工具输出与状态

**Pass**：
- `solco/plugins/solco-automations`：`GOTOOLCHAIN=auto go test ./...`
- `solco`：`GOTOOLCHAIN=auto go test ./internal/personalagent ./internal/remotehost ./internal/hostd`
- `solco`：`GOTOOLCHAIN=auto go test ./internal/nativeui -run 'Test(Assistants|Avatar|Assistant|Group|Coordinator|Persona|MCP|Memory|Behavior)' -count=1`
- `solco/frontend`：`bun run typecheck && bun run build && bun run build:mobile`
- `solco-mobile`：`pnpm typecheck && pnpm lint && pnpm test` — 583 pass, 2 skip；另 `pnpm check:fixtures`（178 files）、`pnpm check:chat-page`、`pnpm export:web`
- 编辑校验：文档记录仅引用本轮实际读取代码的行号，或明确标记仅来自已有 tracker 的历史实机证据。

**Not independently verified this turn**：整个 Native UI 全套 `go test ./internal/nativeui`、`go tool mygo vet .`、本轮安装桌面 App / Android Release APK、真实 Agent 重跑、跨端事件恢复与 OS 沙箱对抗测试。

**最终结论**：代码实现进度很好，但目前只能宣布 **U0～U5 的核心领域能力大部分落地，尚未满足“一打开就聊天、目标主动推进、每个承诺都在原聊天有真实回复”的用户级完成条件**。首要修复不是新框架和新市场，而是身份准确路由、默认聊天行为与耐久报告交付链。

## 6. 第二轮深度审计：权限、消息不丢失、Goal 真假完成（2026-11-04）

> **安全与可靠性升级结论**：除上文静态产品差距外，本次通过 **独立临时复制的 Go 插件 + 新增只在临时副本存在的回归测试**，复现了 4 个独立的回归失败（消息投递/群会话分页×2/Goal 成功标准），且以两个运行成功的隔离测试确认本机 CLI 角色鉴权缺失（群消息/人工审批与跨 Assistant 私有记忆读取/伪造用户记忆）。**所有临时副本经 shell EXIT trap 清理，无运行时代码变更，没有访问用户生产数据库。**
>
> **独立检查**：`go test -race ./internal/personalagent -run 'Test(CreateGroup|Mentions|Pump|Group|Supervisor|Routine)' -count=1` PASS；`go tool mygo vet .` 执行完且未报出 Go Vet 诊断；但后续 `git diff --cached --check` 因原本已暂存的 3 个旧文档 EOF 多余空行而返回 2（不是本轮报告引入）。全套 Mac UI/Android 真机未再次操作。

### 6.1 BLOCKER-01：同一本机 CLI 可同时扮演“Agent”与“用户”，人工审批无法强制隔离

**根因 / 影响**
- `plugins/solco-automations/main_assistant.go:21–34`：每次 `solco-automations assistant ...` 直接用当前 HOME 的 `DBPath()` 打开 Store，并把参数传给同一 `API.Handle`；无调用者 principal、token 或 Agent/Owner 角色区分。
- `plugins/solco-automations/internal/personalagent/api_group.go:94–119`：调用 `group post` 即在 `PostMessageInput` 里使用 `AuthorUser`，还能够通过 `mentions` 唤醒另一个 Assistant；`group say` 的 `as` 是用户可填字段。`group.go:457–468` 只检查被声称的 Agent 是否确实 Live，**不校验当前调用进程是否属于那个 Agent**。
- `api_b.go:196–240`：`approval list` 返回带 `actionHash` 的完整审批卡，而 `approval decide` 只要 ID、hash、approve 就能通过；`approval.go:163–209` 验证内容/修订/过期，**不能验证这项决定来自真正的人**。`assistant goal approve`、`assistant memory approve/forget/share` 等其他 owner-only 指令也走同一路径。
- `memory.go:15–19,130–145` **明确注明 CLI 无法证明输入者是谁**：`assistant memory list <assistantId>` 能按任意 ID 读另一助理的私有内容；`assistant memory add` 通过相同 API 直接写入 `state=approved, sourceRef=user` 的记忆，跳过 Agent candidate→Owner 审核。`BuildContext(assistantId)` 的逻辑过滤**不能当成此 CLI 的访问隔离**；任何能够访问相同 HOME/数据库的 Agent 进程都需要可信 OS 隔离或受控 Broker 才能保持个人记忆边界。
- `group_agent.go:188–194` 和 `coordinator.go:198–207` 明确教 Agent 在自己可运行命令的 shell 使用该 CLI。因此不能把“模型一般会听 Prompt 的规则”当作用户审批授权。**Host 的 HTTP 设备 ScopeManage 不覆盖 Agent 本机 CLI**。
- **实际隔离测试 `TestAuditPublicAgentCLIHasNoUserActorBoundary` PASS**，确认 API 允许一条伪装成人的群消息排入 B 的 delivery，并允许使用公开审批卡的 hash 将本应人决定的卡标为 approved。
- **新增实际隔离测试 `TestAuditAgentCLIReadsOthersMemoryAndForcesApprovedMemory` PASS**：相同本机 CLI API 成功读出 Ada 的私有 Memory，并为 Bo 写入一条直接 `approved`、标为 `user` 来源的记忆；**无身份凭证也没有跨 Assistant 授权检查**。这两个测试模拟了本机任意 CLI 调用者，不声称已经观察到真实 Agent 主动恶意操作。

**必须实施的安全修复**：把 owner-only `approve/decide/publish/merge/memory grant/group human post` 从任何 Agent 可调用的通用 CLI 中分离。Owner 操作只能走真实用户交互拥有的授权通道；Agent 的能力通过受限 Broker 按 `assistantId/runId/taskId` 和短期 allowlisted action 授权。**仅藏 CLI 命令、不在 Prompt 里提命令、或把密钥存放在同一 UID 的可读文件中，都不能提供可靠的权限边界**。如果 Agent 仍可自由运行本机任意 shell 且与 Owner 处于同一 Unix 用户/文件权限域，就必须先用实测的 OS/进程隔离或仅允许无副作用操作，不要宣称已有强沙箱。对所有用户批准的 Routine、合并、**跨 Assistant Memory 读取/已批准写入**和投递分别补反向权限测试。

### 6.2 BLOCKER-02：群聊积压 50+ 条后，目标 @ 消息可能未送达却被 ACK（实测失败）

- `group_agent.go:284–324` 中 `groupDeliveryPrompt` 只读 `groupMessagesAfter(lastSeq, 50)`，生成的 prompt 即使没包含 `messageID` 所指目标仍然返回无错误的 `lastSeq`。
- `group_pump.go:256–295` 用该内容调用 Submit 成功后，立刻推进 `lastSeq` 并将原 delivery 设为 `delivered`，从未校验目标消息在这批 50 条里。**仅推进了上下文，不代表目标 @ 收到回答请求**。
- **独立临时回归 `TestAuditGroupTargetPastFiftyIsNotAcknowledgedWithoutDelivery` 预期失败**：先写入 55 条普通消息，再 `@Ada important action`；真实发送的 prompt 到 `ordinary context 49` 结束，无 `@Ada`，但 delivery 已 `delivered`。
- 修复：首先保证 delivery 提交的消息确实在当前批次；分页扫至 `target seq` 或单独加载目标并按上下文窗口截断。只有成功包含目标并被 Herdr 确认接收才 ACK；需要对同一 Assistant 的多个目标消息按 seq 顺序做完整处理和 bounded budget，异常状态使用 `uncertain` 而不是盲重试或假 delivered。新增 `>50`、`多 @`、`多群并发`、崩溃恢复回归。

### 6.3 BLOCKER-03：Goal successCriteria 未经独立核验就宣布 Completed（实测失败）

- `task.go:451–480`：research/manual Task 的 Agent `RunDone` 直接让 Task `Done`，Coding 需 review。
- `goal_plan.go:317–343`：`TaskDone → MilestoneVerified`，证据只是字符串 `task "..." done (taskId)`；`SuccessCriteria` 只作为显示/存储字段，不进入该验证逻辑。
- `supervisor.go:244–267`：只要每个里程碑来自任务状态的 `verified`、`allDelegated`，立即 `CompleteGoal`，并发出 “Done” 系统事实卡，**不要求成果级 Evidence Review 或用户确认 successCriteria**。
- **独立临时回归 `TestAuditGoalNeedsSuccessCriteriaProofBeforeComplete` 预期失败**：Goal 的 `successCriteria` 明确为“Owner 核对事实并签署发布检查单”；只有研究任务 `RunDone`，Supervisor 即宣告 `GoalCompleted`，证据全文只包含任务完成状态，未得到 Owner 确认。
- 修复：拆分 `ExecutionFinished` / `EvidenceCollected` / `CriteriaVerified` / `GoalCompleted`，每个里程碑明确 `validationPolicy`（可自动核对的文件/测试/PR/结果 schema 或人工验收），不可验证的先等待 Owner。Go 只承认独立可引用的 validator receipt 或 Owner 审批，不让模型单纯宣称成功触发最终完成；已有的 Draft/预算/最大重规划约束继续保留。

### 6.4 HIGH-01：群聊最近消息分页把全局 Seq 误当群内连续 Seq（实测失败）

- `group.go:549–564` 的消息 `seq` 是全局数据库序号；`GroupFeedSince:567–588` 计算 `since = g.LastSeq - limit` 再按当前群查询，这个公式**只有每个群的消息 seq 连续时才正确**。
- **临时 `TestAuditGroupFeedMustReturnLatestTwentyWithInterleavedGroups` 预期失败**：A/B 两个群各写 30 条消息且交错，取 A 最新 20 条，只返回 10 条（`lastSeq=59`）。
- 修复：`since=0` 用 `WHERE group_id=? ORDER BY seq DESC LIMIT ?` 取最近 N 条，客户端再正序呈现；历史分页使用 cursor 前后锚点而不是简单减 seq。Mac/Android 多群切换必须覆盖这个案例。

### 6.5 HIGH-02：请求 1 条群消息可读出不限于该页的 delivery（实测失败）

- `group.go:585–602`：`feed.Messages` 按 `limit` 加载，`feed.Deliveries` 查询只限制 `m.seq > since`，却**没有限制该页最大 seq / message IDs**，因此翻页在较长群中可能读取无关的大量 delivery，造成 JSON/CPU 放大。
- **临时 `TestAuditGroupFeedDeliveryBoundedToMessagePage` 预期失败**：`since=1&limit=1`，返回 1 条消息但 4 个 delivery records。
- 修复：`feed.Deliveries` 按 `feed.Messages` 的消息 ID 精确查询，或把 `lower/upper cursor` 同时传到 WHERE；任务卡/可视化事实也必须属于相同消息页。手机端 `group/[id].tsx` 当前固定每两秒重新获取 tail，无历史向上翻页，另应补真正的 older/backfill 流程和增量缓存。

### 6.6 HIGH-03：Assistant Profile/Permissions 仍不符合用户可编辑需求

- `assistant.go:24–46` 拥有 `Description`、`Scope`、`WorkspaceRefs`、`DefaultPolicy` 字段，但公开 Store 更新仅 `RenameAssistant`、`SetAssistantStatus`、`SetCoordinatorSession`（`assistant.go:109–141`）。
- 现有 Host `personal_agent.go` 暴露 create/rename/pause/resume，不暴露完整的 description/scope/workspace refs 版本化管理；Desktop More 的 About 只读 description，Mobile Work Style 编辑的是初始指令和模型/MCP，不是 Profile/权限。**用户提出的“More 可编辑姓名、描述、访问权限”只完成改名和部分 Agent 配置**。
- 修复：单一版本化 `UpdateAssistantProfile` 与服务端强制 Policy grants、Mac+Mobile capability/golden；不要把写字段和真正执行时的文件/工具授权混为一谈。

### 6.7 HIGH-04：Mobile Memory 失败清除草稿、编辑界面提前结束

- `solco-mobile/src/app/assistants/memory/[id].tsx:89–95` 调用异步 `write(add)` 后立即 `setText("")`，无等待 `onSuccess`；断网/Host 409/超时，输入直接从编辑框消失。
- `:138–146` 保存编辑 `write(edit)` 后立即 `setEditing(false)`，即使请求失败也退出编辑态，产生用户以为已保存的错觉；Server 409 stale 会刷新，但原修改未必可恢复。
- 修复：`mutateAsync` 并只在确认真实 receipt 后清空/关闭，失败保留草稿或保存 locally per assistant item，offline/stale/onSuccess/typing while saving 测试，以及 Android 返回/重载后保留用户文本。

### 6.8 能力边界再确认

- **群聊精确 @** 仍是 `Name → id`，而不是用户选择时绑定不可混淆的 id entity；`group.go:470–483` 的 `strings.Contains` 只是一道名称包含检查。重名/前缀服务端伪造 mention 的问题见前文 P0-01。
- **手机 Assistant idle** 时仍是 `assistants/[id].tsx` 的管理长页和 `Start conversation`，未达到空会话即可输入消息。
- **MCP** 是真实 Claude strict binding + provider probe，但 `mcp.go:20–27` 承认只有 Server-level grants，Tool-level ACL 和其他 Provider 不能声称已实现；由于 Agent 进程的 shell 权限问题，目前也不能把“只绑定一个 MCP”理解成它无法调用其他本机工具。
- **Goal/Routine 主动回响**：`FactsStrip` 在管理区域，`ReportOutbox` 和 Assistant-authored report delivery 仍未形成跨设备对话闭环；消息收据与真实模型答复必须分开。
- **存量优点**：Plugin single Scheduler、SQLite receipts、重启恢复的 `uncertain` 状态、Host ScopeManage/ScopeView、Claude strict MCP、Memory 版本冲突、Routine stale definition hash 与睡眠 DST 测试均应保留；本轮查出的安全问题不应误导为这些机制没有实现。

## 7. 最新修复排序 / 测试门禁

| 顺序 | 负责人域 | 必须先闭环的验收 |
| --- | --- | --- |
| **Security P0** | Go Plugin + Host/Herdr/OS | Agent Shell 不能调用 Owner-only approve/merge/share 或冒充人发群消息；有单独的 actor provenance + 实测隔离，不只是 Prompt 禁令 |
| **Reliability P0** | Go GroupPump + Desktop/Mobile Chat | 50+ 积压 @ 不能丢/假 ACK，exact ID mention + 多个 @，异步断线/重启后的 durable delivery |
| **Goal Integrity P0** | Go GoalPlan/Supervisor/Review | 成功标准与证据核验双阶段；不能研究任务退出就宣称整目标完成；人工验收要留在等待状态 |
| **Messenger P1** | Go Group Store + Host v3 + RN | 群消息最新页准确、delivery 有界、向上翻页及深度历史，A/B 同名 @ 正确；单聊 idle 直接 Chat |
| **Reporting P1** | Go Core/Host + Shared Chat | Goal/Routine 状态变更有可靠 Fact + 独立真实 Coordinator Report，断网/双机/重启不丢不重、未看提醒可达 |
| **UX P1** | Go Profile/Policy + Desktop + Mobile | 描述/权限可编辑并真实生效；MCP 能力可解释；Memory 失败不丢输入；真实设备验证 |
| **Deferred** | 架构 | Go Workflow 框架只作为研究项，不能分散这轮 P0 资源 |

**复现测试说明**：以上四个“预期失败”均为针对现有代码写的**目标行为回归**，失败本身证明旧代码违反验收；它们未进入项目，也不能在审计结束后把当前代码说成已修复。修复者应先将这些临时测试重建为正式回归，确认 RED→修复→GREEN，并在隔离环境运行双端 E2E。

**更新结论**：本轮比初稿更明确：产品未达标不只是 UX 和报告展示。**Agent 本机 CLI 的真实角色边界、群聊消息不丢失、Goal 证据标准** 都属于自主个人助理上线前必须解决的核心正确性问题。

## 8. 第三轮深度审计：跨文件事务、并发、守护进程寿命和手机首次发送（2026-11-04）

**审计条件**：桌面基线 `main@4b0ce58`、移动 `master@f1cbd28`，都含用户/其他 Agent 的未提交改动。本轮期间其他执行者正在改 `group.go`/`group_agent.go`/`goal_plan.go`/`supervisor.go` 和移动首聊/mention/Memory：**不以第三轮开始前的老代码断言这些缺陷现在仍未修复**；相关最新补丁仅通过构建/单测，仍待正式反向用例与用户端验收。本轮重点是**此前未覆盖且独立新复现的缺陷**。

### 8.1 P0 / AUD-09：automations.yaml 同时编辑发生真实丢更新和 data race（隔离实测）

**代码位置**：`plugins/solco-automations/internal/config/edit.go:27–68` 的 `Put` 走 `readDoc → mutate yaml.Node → encode → write`，而 `write:245–263` 用临时文件加 `os.Rename` 只保证**替换原子性**，没有跨请求/进程锁或 compare-and-swap。`edit.go:220–242` 的包级 `var indent` 每次 `readDoc` 都赋值、`encode` 读取，也没有同步机制。

**隔离测试**：`TestAuditConcurrentDistinctConfigPutsDoNotLoseEachOther`，在 `t.TempDir` 指定 `HERDR_PLUGIN_CONFIG_DIR`，用屏障同时启动 **8 次不同名字的 Put**；`GOTOOLCHAIN=auto go test -race ./internal/config -run TestAuditConcurrentDistinctConfigPutsDoNotLoseEachOther -count=1 -v` **RED**：
- 8 个 Put 调用均返回、最终 YAML **只剩 2/8 个不同 Routine**；这是实测，不是推断。
- Race Detector 同时指出 `config.readDoc() edit.go:145` 的包级 indent 写入互相竞争，并与 `encode() edit.go:238` 的读取竞争。
- 真正跨进程（桌面、手机 Host、Agent shell 同时起 CLI）即使每个 Go 进程不共享 `indent`，**仍存在丢更新**，因为文件读改写的整个周期没锁。
- **关闭条件**：所有 `Put/Remove/Approve/SetPaused` 使用同一锁文件的**跨进程排他锁**，锁内重新读取最新文件、修改及替换；以 revision/hash 判定过时更新并返回冲突；不要把 `flock` 直接加在被 rename 替换的 YAML inode 上。取消全局 indent：每次 encode 使用局部格式。至少 2 进程×多线程并发更名/改名/审批/暂停压力测试，确认 0 丢更新且 `go test -race` 无 race。

### 8.2 P0 / AUD-10：Routine Draft 被拒绝，但 YAML 已经被改写（隔离实测）

**代码位置**：`plugins/solco-automations/internal/personalagent/api_routine.go:248–320`。对已有未启用 Routine 的重草稿，`cfgPut` **先发生并写入 YAML**（`:280`），然后 `Store.UpsertRoutine(requestID+":r",...)` 才检查 SQLite Receipt（`:309`）。原有 Routine 的 `created=false`，`undo` 不恢复旧 YAML（`:287–292`），导致拒绝操作仍有文件副作用。

**隔离测试**：`TestAuditRejectedRoutineRedraftMustNotChangeYAML`。在同一 `routineEnv`：
1. 第一次 `assistant routine draft` 请求 ID `same-id` 创建原始 prompt “Check the repository for risky changes and summarise.”；
2. 第二次重用 `same-id`，保持 Routine 名称不变但将 prompt 改为 “CHANGED REJECTED routine content!”；
3. SQLite 正确返回 `request_conflict`；而 `config.Load().Find("morning-check").Prompt` 已变成被拒绝的内容；测试 **RED**，确认拒绝路径有副作用。

**重要限定**：已启用 Routine 的 `DefinitionHash` / Keeper 可能阻止该新定义直接运行，不能擅称“审批被绕过”；**已证实的是配置实际内容与拒绝收据不一致、用户旧草稿丢失**。

**关闭条件**：先核查 requestId receipt 与 expectedRevision，再执行 YAML 读改写；对两种权威存储使用 staged intent/版本化方案，保证每个 crash point 可恢复/可辨识 uncertain，禁止出现 `request_conflict` 却持久修改 YAML。重复 ID 同体重放、不同体拒绝、同名草稿更新失败、Keeper 审批交错、后台崩溃注入全覆盖。

### 8.3 P1 / AUD-11：非 standby daemon 在无 Herdr 会话时退出，长期 Goal/Routine 存在服务依赖（源代码路径确认，条件性）

- `plugins/solco-automations/main_assistant.go:43–66` 的 `ensureDaemon` 在 Coordinator 启动后只执行 `exec.Command(exe,"daemon")`，**没有 `--standby`**。
- `internal/daemon/daemon.go:101–109`：每 30 秒，当 `!standby && backend==herdr && !runs.Busy() && !assistants.busy() && noSessionLeft(...)`，daemon **正常退出**。`assistants.busy()` → `personal.go:264` 的 `Dispatcher.Busy()`，仅统计活跃 run，**不会因“未来还有已批准 Routine、Active Goal”而保持存活**。
- 但系统另有 `solco-automations service install` 安装的 `--standby` launchd/systemd 登录服务（`internal/service/service.go:99–131`），可让计划持续运行。**尚未审计用户本机是否确实安装/启用该服务；不把此问题写成每台机器必现**。
- **关闭条件**：产品使用中给出明确 `backgroundAutomationReady`/daemon health/lastTick/nextRun 服务状态。没有 standby 时主动提示并安全配置持久服务或禁用“将自动运行”的承诺；测试 Mac 客户端关闭、Herdr 0 会话、睡眠唤醒、重启后在绝对时间窗口内调度已批准 Routine，不能仅验证“Start Coordinator 后 daemon 一度存在”。

### 8.4 P1 / AUD-12：手机 Assistant 首次“Send”实际上只启动 Coordinator、原文停留草稿（当前 WIP 静态审计）

- 新增的 `solco-mobile/src/app/assistants/[id].tsx:124–126,229–237` 在 idle 时展示输入框并用 `Send` 按钮发出 **`kind:"coordinator"` 启动请求**，不是消息提交。成功后 `setHeld(text)` / `setFirst("")`。
- `:152–160` 等新 Pane 就绪后把 `held` 放在路由参数 `draft`。`src/app/chat/[paneId].tsx:119–125,465–467` 将它作为 `ChatPageView.seedDraft`，**只填入 Composer**，没有启动完成后的独立 sendChat 操作，因此用户需要**第二次点击 Send**才能让 Agent 收到首条实际指令。
- `held`/`first` 都只是页面 `useState`：在 Coordinator 等待启动时退出页面或进程被回收，首条消息未进入可靠待发队列；页面导航的 effect 依赖 `directPane,instance,chatServed`，未涵盖 `held` 的更新时序，存在启动刚就绪时取旧值的竞态**风险**。后者只做静态审计，尚未实机复现。
- **关闭条件**：聊天壳对 idle/starting/ready 使用同一个 Composer、发送回执与 client-scoped conversationId；第一次真实 Send 必须等 Agent ready 后经唯一队列送达或明确标成“已存草稿、未发送”，不得用 Send 文案暗示已经发出。Android 离开/重进、Home 杀进程、10–60s 冷启动、Provider trust dialog、聊天路由 effect 竞态必须实测。

### 8.5 P1 / AUD-13：Host 写请求断线后执行可能不受任何期限约束（静态风险）

`solco/internal/remotehost/personal_agent.go:353–367` 的 `paWrite` 用 `context.WithoutCancel(r.Context())` 保证手机连接断开时 CLI 继续持久执行，这个方向是为了配合幂等 `requestId`，**动机正确**；但 `WithoutCancel` 同时去掉了上游 deadline，`internal/personalagent/client.go:37–46` 的 CLI `exec.CommandContext(ctx,...)` 没有再加 timeout。因此如果某个 CLI 操作阻塞/失去响应，会长期占据 Host goroutine/进程，用户断线也不会释放。部分子操作（例如 MCP probe）内部自己有限时 45s，但不能覆盖所有 Host write。

**状态：静态高风险、NOT REPRODUCED**（未构造被恶意挂住的 Host 集成压测，不声称已发生生产故障）。**关闭条件**：从有期限的独立后台 command context 处理持久操作（例如 bounded queue + receipt，必要时执行到 30/60s 的明确预算上限），超时返回 `uncertain` 并允许稳定 requestId 重放/查询，不得错误重跑已启动副作用。模拟 CLI hang、手机断链、Host shutdown、多请求并发上限和资源释放测试。

### 8.6 第三轮基线 PASS 与未通过的区分

以下**当前动态工作树的现有门禁通过**：
- `solco/plugins/solco-automations`：`GOTOOLCHAIN=auto go test ./...` PASS（含其他执行者正在提交的 Group/Goal 测试）。
- `solco`：`GOTOOLCHAIN=auto go test ./internal/personalagent ./internal/remotehost ./internal/hostd` PASS，`GOTOOLCHAIN=auto go tool mygo vet .` PASS，`git diff --check` PASS。
- `solco/frontend`：`bun run typecheck && bun run build && bun run build:mobile` PASS（大 chunk 的 Vite warning 不影响结果）。
- `solco-mobile`：`pnpm typecheck && pnpm lint && pnpm test` PASS：**592 passed / 2 skipped**；`pnpm check:fixtures` PASS（178 fixtures），`pnpm check:chat-page` PASS（hash 17b7b659bdef）；`git diff --check` PASS。
- **隔离负向测试**：`TestAuditConcurrentDistinctConfigPutsDoNotLoseEachOther` 失败（8→2 + DATA RACE），`TestAuditRejectedRoutineRedraftMustNotChangeYAML` 失败（request_conflict 后 YAML prompt 变更）。这些是**新发现的行为不符合目标的证明，不是现有仓库测试失败**。
- **未验证**：当前未提交补丁的完整真机行为、standby 服务实际配置、Host CLI hang 的系统级压力测试、Mac/Android 安装重跑、Mac 合盖与长时间定时唤醒。

**本轮修复优先级**：AUD-09（跨进程读改写锁＋竞态）→ AUD-10（SQLite+YAML 拒绝/崩溃一致性）→ AUD-11（长期服务保证）→ AUD-12（手机首条 Send 真实提交与离线持久性）→ AUD-13（Host 耐久写入有界）。前两项必须做正式 RED→GREEN 回归，原阶段任务完成率不因这次构建 PASS 而被当作产品终验。

## 9. 第四轮深度审计：Memory 文件权限、跨助理隔离与 Routine 启用竞态（2026-11-04）

> **审计依据**：当前桌面 `main@4b0ce58` + 手机 `master@f1cbd28`，工作树包含其他 Agent 持续进行的未提交修复。第四轮针对既有 MemoryFiles/Config/RoutineKeeper **没有修改的实现**，把 5 个新场景写成隔离临时 Go 负向测试（测试失败 RED = 证明旧行为不满足目标）。每轮用 `cp -R plugins/solco-automations/. "$d"`，在 `/tmp/solco-audit-*` 中增加 `audit_only_test.go`，测试以 `t.TempDir` 和独立 SQLite / config 环境执行，`trap ... EXIT` 清理，**不访问真实 MemoryRoot、automations.yaml、生产 SQLite**。
>
> **注意区分**：一个记忆“出现在 B 的 candidate / Review 列表”，**不等于**它已经进入 B 的 `VisibleMemory` 或模型上下文；文件写入后自动变 `approved` 则另属于来源授权漏洞。两者是不同事件。

### 9.1 P0 / AUD-14：Memory 子目录 symlink 可将 A 私有事实投影到 B，并被 B 当成候选导入（隔离复现）

- `plugins/solco-automations/internal/personalagent/memory_files.go:163–185`：`Sync` 调用 `MkdirAll(dir/facts, inbox, notes)`，**没有 `Lstat` / `EvalSymlinks` 验证每一个子目录确实为原生目录**。对已经指向另一个目录的 symlink，`MkdirAll` 不构成拒绝。`readSmall:119–130` 只检查叶子文件不是 symlink，并未防止父目录被替换。
- `memory_files.go:310–334` 的 `writeFileAtomic(path, data)` 直接沿路径写入，不检查父目录是否偏离指定 Assistant 的根。`:253–276` 把 B 目录里的任何新 `facts/*.md` 都读作 B 的候选。
- 隔离 `TestAuditSymlinkedFactsDirectoryMustNotCrossAssistantBoundary`：分别建 Ada/Bo 空 Memory 文件夹；将 Ada 的 `facts/` 换成指向 Bo `facts/` 的 symlink；对 Ada 写一个新 `approved` Fact 并 `Sync(A)`，再 `Sync(B)`。**RED：Bo 的 `memory_items` 出现含 Ada 私有文字的 `candidate`，`sourceRef=file:facts/<A-memory-id>.md`。**它没有自动成为 B `VisibleMemory` 的 approved fact，但已经越过私有记忆目录与管理 UI 边界。
- **修复验收**：所有目录级 symlink 和别名路径拒绝/隔离，使用固定根 dir fd + no-follow / 安全路径遍历模式，跨助手的内外目录不能交叉读写；不能仅检查叶子文件。新增父目录 symlink、符号链接切换、文件/目录替换竞态、跨 Assistant 投影和导入负向测试。即使相同 Unix UID 的全 shell 权限无法提供强进程隔离，文件同步器自身也不应扩大错误路径的影响。

### 9.2 P0 / AUD-15：编辑 facts/*.md 可直接改写已批准记忆，未识别“人编辑”与“Agent 写文件”（隔离复现，和 AUD-01 同属授权根因）

- `memory_files.go:207–249`：已跟踪 `facts/<id>.md` 内容变化、revision 一致时直接 `Store.correctAs(MemByFiles,...)`；`memory.go:188–209` 的 `CorrectMemory` **保持已有 `approved` 状态**，记录一条文件来源版本但不经过 candidate/Owner approval。
- `TestAuditFileEditIsApprovedWithoutKnowingWriter`：在临时目录改写一个 approved fact Markdown，再调用 `Sync(A)`；**RED：事实直接成为 revision 2 的 `approved` 版本，没提供任何用户身份、审批卡或 Agent Run scope**。
- **设计辨析**：“用户在本机用编辑器修改文件直接保存”可能确实是产品期望，不应盲目取消。真正缺口是 **Agent shell 与 Owner 同 UID、能编辑同一目录，Sync 无法认证是谁写的**，这会绕过 “Agent 提议→Owner 审核”。
- **修复验收**：仅 Owner 可信 UI/独立权限进程的写入能够直接批准；Agent 可写目录只产生 candidate，不能因为 `MemByFiles` 便立即生效。若不可区分同 UID shell，先禁用从任意文件修改自动批准并提供显式“Approve imported edit”通道；反向测试证明 Agent 直接编辑 facts、篡改 front matter、修改 notes 后都不能自动上升为 approved。关联前两轮 AUD-01 的 Owner/Agent 真实身份隔离。

### 9.3 P1 / AUD-16：不同助理导入同路径同内容文件时，幂等 ID 跨 Assistant 冲突（隔离复现）

- `memory_files.go:270–272`：新文件采用 `"file-new:"+sha256(rel+"\x00"+body)[:24]`，**没包含 assistantId**。`proposeAs` 会以 `ProposeMemoryInput{AssistantID,...}` 进入 `execute` 的全局 receipts 表（`store.go:161–167,635–659`），所以来自另一个 Assistant 的**同路径/同正文**会以同一个 requestId 搭配不同 payload，得到 `ErrRequestConflict`。
- `TestAuditSameNoteInTwoAssistantsMustImportTwice`：A、B 同时拥有 `notes/shared.md` 且正文一致，各自独立文件夹；A `Sync`: `Imported=1`，B `Sync`: `Imported=0, Refused=1`，B 的 `ListMemory` 为 0。**RED；重复尝试还会撞同一个请求键**，并非资料已归属 B。
- **修复验收**：构造请求 ID 时使用 Assistant ID、操作域、文件路径、内容 hash；旧 receipts 的迁移兼容要考虑，避免复用历史错误 ID；A/B 相同相对路径正文及跨进程同时扫描时两边各有且只有一条 candidate。

### 9.4 P1 / AUD-17：重复使用过的旧记忆文本无法通过文件再次编辑回来（隔离复现）

- `memory_files.go:231–245` 对现有文件使用 `"file:"+memoryID+":"+hash(fileRaw)[:16]`，**未包含当前 `rec.revision` 或编辑基准**。`correctAs` 的 request payload 含 `expectedRevision`；旧文本的 ID 再次出现时命中以前的 receipt，但 revision 已变化，返回 `ErrRequestConflict`，`Sync` 将它计为 Refused 并把磁盘内容恢复为新版本。
- `TestAuditRepeatPriorFileEditShouldBeAnAllowedLaterRevision`：先 `Apple→Banana`（文件编辑，成功）→ `Cherry`（手机/DB API 编辑，成功）→ 文件再次改回 `Banana`；**RED：`Edited=0, Refused=1`，最终值仍 `Cherry`**。这是合法用户编辑被错误拒绝，而非预期并发冲突。
- **修复验收**：同一 memoryId、**当前 base revision**、内容 hash 构造幂等键；相同 revision+同内容重试是单次变更，新 revision 后返回旧文本必须允许作为新的合法编辑；覆盖 ABA 文本往返、异常恢复、跨端 revision fence。

### 9.5 P0 / AUD-18：RoutineKeeper 先验证审批 hash，再启用配置时有 TOCTOU 窗口（隔离复现）

- `routine.go:226–286`：`k.Load()` 后检查 `DefinitionHash(*a) == r.DefHash`（`:261`），但随后执行 `k.Approve(r.Name)`（`:274`），默认 `config.Approve` 重新读取 YAML 并清除 `draft: true`（`config/edit.go:92–111`）。**没有锁跨越“验证所见”与“启用所改”。**
- `TestAuditRoutineKeeperMustRecheckDefinitionAtActivation`：人工 approval receipt 已记录；测试在 Keeper `Approve` seam 的开始，用真正的 `config.Put` 替换 Prompt 为未经批准的新文字，再调用 `config.Approve`。**RED：Keeper `Enabled=1`、Routine 状态 `active`、YAML `draft=false`，而 Prompt 已不是 Owner 批准的内容。**
- **修复验收**：跨进程文件锁内进行**校验预期定义 hash → 对同一 YAML 文档解除 Draft → 原子落盘**的单一条件操作（例如 `ApproveIfHash(name, hash)`），并将审批 receipt 的批准对象与 YAML 版本绑定。配置持久化成功、SQLite active 失败时必须有可恢复的 durable transition / reconcile，不可不明状态放行。并发改 prompt/cron/model/agent/tool grant 与 Keeper Tick 交错 1000 次应始终拒绝未经批准的内容。该漏洞与 AUD-09/10 共享根因，但不能被“单纯添加写锁”自动证明修好。

### 9.6 基线与证据等级

- **隔离负向测试（新增）**：`TestAuditSameNoteInTwoAssistantsMustImportTwice` **RED**（A import=1，B refused=1）；`TestAuditSymlinkedFactsDirectoryMustNotCrossAssistantBoundary` **RED**（B candidate 含 A 内容）；`TestAuditFileEditIsApprovedWithoutKnowingWriter` **RED**（非认证文件写入成为 approved）；`TestAuditRepeatPriorFileEditShouldBeAnAllowedLaterRevision` **RED**（旧文本无法再编辑回来）；`TestAuditRoutineKeeperMustRecheckDefinitionAtActivation` **RED**（未经批准 prompt 被启用）。所有测试文件只存在于隔离临时副本，不进入真实仓库。
- **现有门禁**：`GOTOOLCHAIN=auto go test -race ./internal/personalagent ./internal/config -run 'Test(Memory|Routine|Approval|Group|Rule)' -count=1` **PASS**；根 `go test ./internal/personalagent ./internal/remotehost ./internal/hostd` **PASS**；手机 `pnpm typecheck && pnpm lint && pnpm test` **PASS**，`592 tests passed / 2 skipped`；`git diff --check` **PASS**。
- **未验证**：实际恶意 Agent 自主访问 MemoryRoot、macOS 沙箱与额外 OS 账户、模拟器/Android Live、长期 Keeper 真实多进程压力、各个 crash point。P0 评估来自真实可到达的代码/文件系统语义及隔离回归，不代表已观察到外部入侵。
- **注意其他并行修复**：用户/另一 Agent 已更新 Group、Goal、桌面/手机 UI 工作树，台账后追加“审计修复轮”；**保留这些改动**，不用历史审计结论抹掉已转绿的用例。

**第四轮结论**：优先关停 **AUD-18（审批内容原子性）** 与 **AUD-14/15（跨助手目录安全与文件写入授权）**，随后修 **AUD-16/17（Memory requestId 域与修订号）**。这组问题不能通过仅增加 Go 正常路径单测或更新 UI 声称完成；必须保存针对负向场景的正式 RED→GREEN 测试。

## 10. 修复批次与再验收（2026-11-04；原第 9 节是修复前 RED 证据）

本节的状态以 **具体不变量** 为粒度，不能把 “部分转 GREEN” 误写成整个 Assistant 产品验收通过。未经说明的原有审计报告保留复现时的事实，修复后的新状态由本节覆盖。

| Issue | 状态 | 实现与实测 | 剩余边界 |
| --- | --- | --- | --- |
| **AUD-14（P0）** | **PARTIAL / hardening GREEN** | `d6f0df6`：Memory Sync 检查根/Assistant/facts/inbox/notes 是真实目录，拒绝目录 symlink 和 symlink 锁文件；`TestMemoryFilesRejectCrossAssistantDirectorySymlink` GREEN | `Lstat` 后到写文件之间仍可能被同 UID 进程交换目录，未做 dirfd + `openat(O_NOFOLLOW)` 的端到端防替换；同 UID Agent 直接读 SQLite/文件的 OS 隔离未完成，**P0 边界不能关闭** |
| **AUD-15（P0）** | **MITIGATED / principal OPEN** | `d6f0df6`：修改 approved `facts/*.md` 不再即时改写 approved fact，只创建 `MemoryCandidate(kind=correction, sourceRef=edit:id@rev)` 供 Owner 审核；原 approved 与版本保留，正式反向测试 GREEN | 独立可信 Owner 与 Agent 的 OS 身份未分离（AUD-01）；Agent 仍可能使用同 Unix UID 下未授权 CLI 提交/批准候选；不得称“全链路不可伪造” |
| **AUD-16（P1）** | **PASS（核心）** | `d6f0df6`：`file-new` receipt digest 包含 assistantId+rel+body，两位助理相同笔记分别导入成功；正式测试 GREEN | Android UI/多进程目录切换未做实机 |
| **AUD-17（P1）** | **PASS（核心）** | `d6f0df6`：`file:<memoryId>:<baseRevision>:<fileHash>`；ABA 合法回改（Apple→Banana→Cherry→Banana）正式测试 GREEN | 同 UID 外部原始文件替换仍属 AUD-14 边界 |
| **AUD-09（P0）** | **PASS（插件写入并发）/ 外部编辑待检** | `9657565`：`Put/Remove/Approve/SetPaused/Save` 的整个读改写统一使用稳定 sibling `.automations.lock` 跨进程 `flock`；YAML 缩进变为局部值；`Save` 增 sourceDigest CAS，过期 UI/向导快照拒绝覆盖。12 goroutine + 6 CLI 子进程并发以及 `go test -race` GREEN | 任意不合作的外部编辑器不会拿这个 advisory lock；极端崩溃点文件 `fsync`/持久化另需单独保证 |
| **AUD-18（P0）** | **PASS（定义审批原子检查）/ crash recovery OPEN** | `9e877b5`：`ApproveIf(name,match)` 在同一个 `flock` 内读取、验证归一化 definition hash、解除 draft；Keeper 遇变化标 `RoutineStale`，正式插入竞态测试 GREEN | 审批 YAML 生效与 SQLite `active` 的跨存储崩溃恢复尚未有 durable intent/reconciliation；不能对完整跨存储状态宣称原子 |
| **AUD-10（P0）** | **PASS（拒绝重放不改 YAML）/ crash recovery OPEN** | `9e877b5`：提前 `peekReceipt` 拒绝已提交但不同载荷的 requestId，匹配重放不重写 YAML；`1ab92c1`：draft/cancel/keeper 使用独立的 `.assistant-routines.lock`，避免两个 CLI 在写 YAML 与写 SQLite 收据之间交错。顺序 + 并发竞争测试 GREEN | 进程在第一次写 YAML 后、提交 SQLite 前崩溃，仍可能留下未纳管的 YAML draft；需两阶段操作意图和重启 reconciliation |
| **AUD-13（P1）** | **PASS（Host 有界调用）** | `b87ded8`：`context.WithoutCancel(r.Context())` 外包独立 `context.WithTimeout(..., 2*time.Minute)`；CLI `CommandContext` 到时杀掉；504 `personal_agent_uncertain` 提醒使用**原 requestId** 重试，正式测试和 Host 全量测试 GREEN | 执行到达 SQLite 与客户端回复之间仍有常规“结果不确定”窗口；幂等收据提供重试语义 |
| **AUD-12（P1）** | **PARTIAL / first Send GREEN（逻辑）** | 手机仓库 `536579d`：Coordinator 已启动并有确切可访问的 Chat Pane 后，首条 `Send` 通过 Host `sendChat` **真正发出**，收到成功再进入 Chat；断线后不盲目重发而交还 Chat 草稿。TS/Lint/Vitest 592 PASS | Expo/Android 实机未复验；App 被强杀/卸载时未实现持久化的首条待发消息 outbox。不能声称 exactly-once 送达 |
| **AUD-11（P1）** | **OPEN / conditional** | 已有 launchd `daemon --standby` 服务机制，但单纯由 Coordinator CLI `ensureDaemon` 启动的 daemon 仍是非 standby，`noSessionLeft` 可使它在空 Herdr Session 时退出 | 需要明确可靠的安装/启动服务策略、退出与重启恢复 E2E；本批未更改守护进程生命周期 |
| **AUD-01（P0）** | **OPEN** | 仍必须为同 UID Agent 与 Owner 提供真实 Broker / 进程权限隔离；提示词、前端隐藏权限不是安全边界 | 未做 OS sandbox、独立用户或可信 Broker；不能标“完全兼容安全规范” |

### 10.1 提交边界（全部保留其他开发者工作树）

- Desktop `d6f0df6` — Memory 文件安全、审批与 requestId
- Desktop `9657565` — Automations 并发文件锁、Save 版本 CAS
- Desktop `9e877b5` — Routine 定义审批原子校验与被拒请求预查
- Desktop `b87ded8` — RemoteHost durable write 有界超时
- Desktop `1ab92c1` — Routine draft/cancel/keeper 跨 CLI 序列化
- Mobile `536579d` — Assistant 首条 Send 真实提交
- 独立已有 Desktop `eb9b5bc` 是 MyGo JSC Bridge 消息传输修复（**不是**本批 Assistant commit）。OSC 7501/Hook 则由其他工作流的 `dea65e9` 独立处理。

### 10.2 门禁与稳定性

- **PASS**：Desktop `GOTOOLCHAIN=auto go test ./...`（全部 47 个 root module 包），`GOTOOLCHAIN=auto go tool mygo vet .`，`GOTOOLCHAIN=auto go tool mygo build` → `build/darwin-arm64/Solco.app` + arm64 DMG，`plutil -lint` PASS。
- **PASS**：Plugin `go test ./...`，`go test -race ./internal/config ./internal/personalagent ./internal/daemon -count=1`（包括跨进程、并发批准、Memory 边界、Routine rejected replay）。
- **PASS**：Mobile `pnpm check:fixtures`（178），`pnpm check:chat-page`（hash `17b7b659bdef`），`pnpm typecheck`，`pnpm lint`，`pnpm test`（592 pass、2 skip）。
- **JSC 崩溃**：`docs/debug/2026-11-04-jsc-heap-collector-crash.md` 提供同频约 80 msg/s 旧链路 ≤12 min 崩溃，新 scheme 传输 25 min/约 12 万条消息无崩溃的 A/B 证据。用户新提交的 `2026-11-04 16:59:58` 诊断报告发生在 `17:52` 安装修复版**之前**，不能证明修复失效。截至 `18:35 +0800`，已安装 App 进程从 `17:52:29` 持续运行 42 分钟且 **无新的 Solco.ips**。这不等于多日运行通过。
- **未执行**：Android 实机首次发送/断线、macOS 多日保活、进程强杀 crash-point 故障注入、OS 级 Assistant Owner 隔离；生产环境没有做隐式数据迁移和破坏性测试。

**后续发布阻断优先级**：AUD-01/15 可信 Agent 进程隔离 + AUD-14 dirfd/no-follow → AUD-10/18 SQLite/YAML durable intent/reconcile → AUD-11 持久服务/重启恢复 → AUD-12 原始消息持久化与 Android E2E。当前已修复的局部不变量有正式 GREEN 证据，**Assistants 3.1 全面安全兼容与可无人值守运行尚未达到最终发布门槛**。

## 11. 追加修复批次：Routine dispatch / Scheduler 常驻 / Memory 句柄隔离（2026-11-04）

本节覆盖第 10 节当时的未修复/未验证表述，原始 RED 取证仍保留以便追溯。本批只修改插件，没有移动端接口变更或真实用户库迁移。

| Commit | 证据 | 安全结论 |
| --- | --- | --- |
| `a425db0` Routine dispatch 最终闸门 | `RuleScheduler.Tick` 在同一 SQLite 事务中检查 YAML 与 `RoutineActive && Approved && DefinitionHash==DefHash`；普通未绑定的 Legacy Automation 不受影响。`TestRuleSchedulerRefusesRoutineUntilApprovedAndActive`、`TestRuleSchedulerRefusesEnabledRoutineWithChangedDefinition` 原本均 **RED**：非批准 YAML 也会 `Fired=1`；改后 **GREEN**。`TestApprovedRoutineFiresOnceUnderItsOwnerAndReportsBack` 保持 GREEN | **AUD-18 的跨库崩溃窗口 fail-closed**：YAML 已启用但 DB 未 Active 时不调度，已批准的 Keeper 在下一 Tick 可恢复 Active。**不代表**已经实现 `routineDraft` 第一次 YAML 写入前后的 durable intent 日志；AUD-10 的 orphan Draft 恢复仍 OPEN |
| `bce7ba0` Assistant 长驻 scheduler | `ensureDaemon` 补启动由 `daemon` 改成 `daemon --standby`；hook-started daemon 在 SQLite 里已注册 Assistant 时不因 Herdr Sessions 为零而退出。`TestAssistantBootstrapDaemonIsResident`、`TestPersistentAssistantsSurviveSessionlessScheduler` GREEN（覆盖无会话、旧模式、standby、运行中等） | **AUD-11 代码路径 PASS**；真实进程强杀、launchd 未安装时系统重启自动拉起、长期离线/无会话 E2E 尚未验证，不宣称 complete |
| `f930bce` Memory OS 目录根句柄 | `os.Root` pin Assistant / memory / facts / inbox / notes；`Lstat` + `SameFile` 验证打开后同一 inode，leaf `O_NOFOLLOW/O_NONBLOCK`，所有 read/write/rename/remove/readDir 走 Root 相对句柄。测试 `TestMemoryTreePinsFactsAcrossDirectorySwap`、`TestMemoryTreePinsParentAcrossDirectorySwap`、`TestMemoryTreeReadRejectsLeafSymlink` GREEN，原有 Memory 全量 GREEN | **AUD-14 路径替换风险已实质加固**，替换目录不会将 Sync IO 导入 B 的内存文件。仍无 OS Principal/同 Unix UID Agent 隔离（AUD-01/15）：同 UID Agent 可直接打开绝对路径/SQLite，不能将此技术防线视作权限边界 |
| `f1e41a8` 调度归属读取失败时 fail-closed | `personal.ownedRules` 读取 SQLite 失败现在返回错误，`evaluateWith` 在改变任何水位/启动 Legacy runner 前终止；`Run` 无法打开 Assistant Store 不再运行 Legacy fallback。`TestLegacySchedulerFailsClosedWhenAssistantLedgerIsUnavailable` GREEN | 宕库期间所有规则暂停是故意的安全默认；恢复服务后重新进入日程计算，需实机故障注入和观察 |

**新验收门禁（本批）**：插件 `GOTOOLCHAIN=auto go test ./...` **PASS**，`go test -race ./internal/personalagent ./internal/daemon ./internal/config -count=1` **PASS**。没有碰其它 Agent 的 OSC 7501/Hook、Onboarding、旧文档暂存文件。

**仍未关闭的 P0**：AUD-01/15 高权限 CLI 没有身份 Broker 或 OS 沙箱；AUD-10 在 `routineDraft` YAML 写入和 SQLite 写入之间遭遇 `SIGKILL` 后，尚缺 durable intent / orphan Draft reconciliation；AUD-18 的审批原子性已经被额外的 dispatch gate 防护，但仍缺完整跨库 crash-point 恢复协议。此处仅对**负向测试覆盖的安全不变量**宣称 PASS，不宣称全部实现与完全无人值守。

### 11.1 全仓库测试套件的独立阻断与修补

- 本批 Plugin **完整 `go test ./...` + Race 检查 PASS**；Root 的 `go test ./internal/personalagent ./internal/remotehost ./internal/hostd ./internal/automations` PASS。不能用插件 PASS 替代整个 GUI 测试。
- Root `go test ./...` **NOT PASS / 全量 UI 套件超时**。第一次卡在 `TestPushOutlivesTheWorkingTreeBudget`：原测试桩把所有非 `rev-parse` 命令都延迟 31s，与“只测 push >30s”的目标不符。`733520a` 修改**仅测试桩**为只有 `git push` 延迟 31s；该测试单独 `go test ./internal/nativeui -run '^TestPushOutlivesTheWorkingTreeBudget$' -count=1 -timeout=55s` **PASS，31.51s**，真实 Push 生产代码未改。
- 修补后重新运行 Root `go test ./... -timeout=120s`，`internal/nativeui` 仍超过整个包 120s 的超时预算；超时发生时正在执行 `TestEveryInnerPageLeavesByTheTerminalTab`，**不能仅凭堆栈断言该测试本身死锁**，因为同一个包此前的测试已消耗大量总时间。本批不缩短生产代码、不删用例，也不伪称全量 GREEN。其他根模块包在该次输出中均 PASS。
- 同期其他开发者的 `internal/nativeui/page_onboarding.go` / `page_onboarding_test.go` WIP 未修改或提交；三个 Onboarding 专项测试独立运行均 PASS。

**结论**：新增四个插件安全/可靠性 commit 具备完整插件全量与 Race 证据；额外 Git 测试修补 commit 具备独立 GREEN 证据；全量 GUI 测试超时需下一批做可重复基准、测试运行时间预算/隔离，不应将其归因于本批插件行为。
