# Shepherdr 3.1.3 对齐追踪表（68 项 Gap）

> 事实账本。每项状态只能是 OPEN / PARTIAL / DONE(软件) / PASS(真机) / BLOCKED。**DONE 只表示代码+自动测试 GREEN；只有 AC 真机证据齐全才可写 PASS。**
> 规划：`assistant-shepherdr-3.1.3-alignment-roadmap.md`。上游固定 `dc1b67824924c02fde6765f184bcd2a420743b8a`。

## 进度日志

- 2026-11-05 M0：基线（plugin 全量 GREEN 308+、root GREEN、race 见下）；修复预存红测 `TestRoutineSIGKILLAfterYAMLRecoversOnNewProcess`（子进程仍用 claude，被 OMP 锁拒绝）。
- 2026-11-05 M2.1：W02/W03/W06/Q01 结果回收核心（见各行）。

| ID | 优先级 | 标记 | 状态 | 源码位置 | 测试 / RED→GREEN | commit | 闭环条件摘要 |
|---|---|---|---|---|---|---|---|
| C01 | P0 | B | PARTIAL | 同 C03 | e2e 证实 Prompt→受控 ToolCall→Task(owner)；外部 shell 被拒 | (本批) | **自然语言请求不能退化为普通 OMP 自行执行**；必须实际观察 Prompt→受控 ToolCall→Solco receipt→Task/Goal/Routine ID；拦截；缺：真实自然语言(非脚本模型)与 Routine/Goal 路径的真机观察|
| C02 | P0 | B | PARTIAL | `assistant --as` 核心侧作用域（allowlist + 属主校验 + 修订自动填充，`api_scope.go`）；TS 仅整形 | TestScopedCaller* ×3、TestExtensionActionsMatchTheCoreScope；越权一律 `forbidden`（不暴露他人 id 是否存在）；真实 Herdr+OMP e2e：TestE2ECoordinatorScopeIsEnforcedByTheCoreThroughTheRealExtension（自己的 goal 被暂停，他人 goal 不变）| (本批) | OMP extension 的“工具白名单”**不等于可信身份授权**：设计 scoped Broker 服务端校验 AssistantId/动作/Owner/能力/TTL/重放；；仍缺：服务端校验的 scoped token/裸 CLI 的 Owner 边界|
| C03 | P0 | V | PARTIAL | `extensions/solco-coordinator.ts` `coordinator.go CoordinatorTools` `host/agent.go explainStart` | 真实 Herdr+OMP e2e：扩展被加载、bash 被拦截、solco_assistant 建出属主 Task；坏扩展→OMP 拒绝启动(inert)并带原因；`bun test` 7；Go 白名单一致性测试 | (本批) | **打包/安装/真实 Herdr Agent --extension 是否成功加载与可调用**未证实；安装前和运行时 doctor 必须反馈缺失，不得静默变通，AC-01；真机发现：①OMP 扩展加载失败仍继续运行(未设闸) ②execute 签名为 5 参(ctx 第 5 位)，Bun 单测未暴露 ③import.meta.dir 在 OMP 加载器中不可靠。安装包/doctor 与 Mac.app 实机待做|
| C04 | P0 | P | PARTIAL | `coordinator stop` 关闭记录的 Herdr Pane（`HerdrPaneClose`：只关记录在册且仍列在该 workspace 下的 pane，并复核已消失）；答复 `pane: closed|close_failed|not_attempted`；重启仍须 Herdr 证明 pane 已消失 | TestStoppingACoordinatorClosesItsRecordedPaneOnce；既有 TestCoordinatorStopAndRestart/API 重启护栏；真机停止/重启三连点待验 | | 已有重复启动 fence，但停机后实体 Pane/Agent 未必结束；安全 stop/restart、trust/permission blocked/uncertain 单元与 |
| C05 | P0 | P | PARTIAL | `CreateTaskInput.RequiredSkills` | TestGapW04RequiredSkillsAreValidatedAndStored | (本批) | 强制 schema：objective、scope、requiredSkills、inputs、deliverables、acceptance/evidence、constrain；其余 schema 字段(deliverables/acceptance…)未强制|
| C06 | P1 | M | PARTIAL | `trace show`（Goal→里程碑→Task→Runs→事实）；Coordinator 创建任务遇未完成的同名同目标任务→`duplicate_task`（回放不受影响，`allowDuplicate` 可显式放行） | TestScopedTaskCreateRefusesAnOpenDuplicate、TestTraceFollowsGoalToTaskToRuns；用户 Chat Turn 级 requestId 尚无；真机待验 | | 用户 Chat Turn→requestId→Goal/Task/Routine→Worker→Evidence→Report 需要**可追踪父子链**；同一问题不得凭模型记忆创建 |
| C07 | P1 | P | PARTIAL | durable progress=checkpoint(A03)；停止/恢复=goal pause/resume(C06,Supervisor 不碰 paused)；proactivity respond/Quiet Hours 阻止自主 wake/汇报；人工批准=审批卡/routine 批准 | TestCoordinator*Report*、Proactivity、api_scope 测试；AC-05 GUI 实机待验 | | 原 Coordinator 状态如何接续“下一步/完成条件”需可读写 durable progress、人工批准、可停止/恢复；不能把 ReportPump 当自主执行循环，AC- |
| C08 | P1 | P | PARTIAL | `task why`（固定语句 + 稳定码）、Coordinator 可 `goal.pause/resume`、`task.retry`；人决定的动作（approve/complete/publish/enable）被核心拒绝 | TestTaskWhyExplainsWhyItIsNotMoving、TestScopedCallerOnlyReachesTheCoordinatorsActions；Mac.app 实机待验；真实 Herdr+OMP e2e：TestE2ECoordinatorScopeIsEnforcedByTheCoreThroughTheRealExtension（自己的 goal 被暂停，他人 goal 不变）| | Coordinator 应能“这项先暂停/把第二项继续/告诉我为何卡住”，做 owner-scoped 状态读/写且必须有收据、语义一致的错误，AC-03/05 |
| C09 | P1 | P | PARTIAL | `RecoveryDigest` 在 Coordinator 启动时并入 brief；`recovery show` 供压缩后重读 | TestRecoveryDigestRestatesTheRecordedState、TestCoordinatorLaunchPromptCarriesTheDigest；重启后实机待验 | | Restart/Context compact 后重新建立确切上下文（Active Goal、Task、Routine、Pending Approvals、上次报告游标）；不能无凭 |
| C10 | P1 | E | PARTIAL | `skill_grants` + 审批卡 subjectKind=skill | TestGapC10UngrantedSkillAsksOnceAndApprovalRequeues | (本批) | **不复制每个 repo 一个 Assistant**：定义个人 Assistant 可授权哪些 Workspaces/Hosts/Apps/Skills，跨项目只能在显式 sco；Skill 授权已做；Workspaces/Hosts/Apps 范围未做|
| W01 | P0 | B | PARTIAL | `Launcher` 接口 + `HostLauncher` 是唯一 Herdr worker 缝(start/wait/resume/close)，调度器与 Coordinator 都只经它 | fakeLauncher 全部调度测试 + e2e_worker_test(真实 Herdr+OMP)；独立文档化待补 | | 定义唯一 `HerdrWorkerAdapter`（名称可调）：只通过现有 Herdr API/Skill 协议启动、读状态、等结束、恢复，返回 Herdr 身份与 Solco r |
| W02 | P0 | B | PARTIAL | `result.go` `ompresult.go` `dispatch.go finishWithResult/Recollect` `task.go FinishRunWithResult` `store.go v20` | `result_test.go` TestGapW02* RED(done+空 Result)→GREEN；真机待；隔离真实 Herdr+OMP e2e PASS(skills=[fixture-browser], bash=1, 4 grounded sources)，Android/Mac.app 待 | 17cdd98 | **当前结果回收核心缺口**：必须读到真正的 Worker 输出/最终消息并携来源（agentSessionId、messageId、读到的 byte range/文件 hash/ |
| W03 | P0 | B | PARTIAL | 同 W02：结束≠完成，读不到答案→uncertain `result_unknown` | `result_test.go`；blocked/vanished 细分待 M2.2 | 17cdd98 | 完成判定要区分“Agent idle / final turn / blocked / vanished / timed out / prompted but unaccepted |
| W04 | P0 | P | PARTIAL | `skills.go` `dispatch.go skillGate/unusedSkills` schema v21 `task_skills` | `skills_test.go` TestGapW04*: 缺失→failed skill_missing 且 0 pane；未授权→审批卡→批准后自动重排；用了才算完成；真实 OMP 经 `skill://fixture-browser` 读取并被 transcript 证实 | (本批) | Worker launch 必须显式协商 Skills；已安装的 `~/.agents/skills/herdr/SKILL.md` **要求 HERDR_ENV=1**；不能假定；缺：能力 doctor 列出可用 skill、Routine 路径(规则 run)同样检查、真实 ego-browser/登录态|
| W05 | P0 | M | PARTIAL | `skills.go` 授权链 + `live_x_test.go`(tag live) | 真实 X.com 只读实机：`docs/debug/assistant-shepherdr-3.1.3-acceptance/2026-11-05-live-x-timeline.md`（ego-browser 被 transcript 证明加载，27 条 grounded 来源，190→27 去重） | (本批) | X.com Worker 真正由 OMP 加载指定 `ego-browser` 执行，浏览器会话/登录态由用户授权且限任务；证明调用、URL、去重、失败；技能缺失或未授权不得偷换浏 |
| W06 | P0 | M | PARTIAL | `WorkerResult` 含来源/工具/技能/grounded URL | `result_test.go`；Artifact/Proof 卡待 M5 | 17cdd98 | 定义 `WorkerResult/Proof`：摘要、结构化数据、引用/截图/文件、验证方法、失败/不确定项、需要人决策；事实与 Agent 自述分离；无证据就不自证成功，AC-0 |
| W07 | P0 | P | PARTIAL | `host/agent.go await`/`launcher.go Await`: `agent_not_found`(herdr 0.9.5 关闭工作区后的真实回码)按 gone 处理；cancelled 带原因 | `TestAwaitGivesUpWhenHerdrAnswersAgentNotFoundForAClosedPane`；e2e `TestE2EWorkerVanishingMidRunIsNeverReportedAsDone`（修复前 150s 仍悬挂，修复后 ~13s 结束且不伪造 done/result） | (本批) | 状态机应具备 queued/start/working/blocked/waiting_owner/awaiting_proof/ready/done/failed/uncerta |
| W08 | P1 | P | PARTIAL | `QueuedTasks` 按 Assistant 轮转（每个 Assistant 先各出一个最老任务）；重启 Resume 的 run 计入并行配额 | TestQueuedTasksInterleaveAssistantsInsteadOfFirstComeFirstServed、TestReattachedRunsCountAgainstTheParallelLimitAfterARestart（RED→GREEN）；优先级/每 Assistant 上限/非 Git 目录公平仍待 | | 验证 Assistant/Host/全局并发和资源预算，重启不超过配额；优先级、取消、依赖链和非 Git 工作目录需公平调度，AC-05/07 |
| W09 | P1 | P | PARTIAL | 任务可带更短的 `timeoutMin`(1–45，不可超过调度上限)，launch/resume 均取 min(任务, 调度) | TestTaskTimeoutOnlyNarrowsTheDispatcherLimit(RED→GREEN)；model/thinking/tool grants 显式化仍待 | | OMP-first 显式化 Model、thinking、tool grants、skill grants、超时、可更改范围；继承 Assistant Policy 但任务权限只可 |
| W10 | P1 | P | PARTIAL | `host/receipt.go`(Worker=pinned dir; Coordinator=OMP 会话目录+run marker) | Coordinator e2e 未再重发 | (本批) | Herdr launch 不确定要查询 existing Pane/Agent、receipt，单次 submit 不重发；故障恢复演练“旧 Agent 仍在/丢帧/重启”不得双|
| W11 | P1 | E | PARTIAL | 无仓库任务改用 Assistant 私有目录 `personal/tasks/<assistantId>`(不再共用一个 personal/)，删除 Assistant 时清理；有仓库的任务保持其仓库；编码任务仍走隔离 worktree | TestRepoLessTasksWorkInTheirAssistantsPrivateFolder；非 Git 目录的 Routine/浏览器场景 GUI 实机待验 | | 编码隔离分支保持、非代码用 Assistant 私有工作目录；按产品任务 scope 执行、不污染 repo 的工作树；不能把个人浏览器 Routine 强行当 Git 工程，AC |
| W12 | P2 | P | PARTIAL | 失败重试保持同 taskId/同 agent=omp/同 model，无任何静默换 provider 路径 | TestRetryNeverSwitchesAgentOrModel(特征测试)；显式授权的换代理续跑仍未做(P2) | | 先保证 OMP 本身恢复，不偷偷 fallback 到别的 provider；以后才在明确授权和同一 taskId 下增加换代理/续跑，AC-07 |
| R01 | P0 | B | PARTIAL | Coordinator 工具白名单(无 bash) + `solco_assistant routine.draft` | 同上 e2e：crontab 无变化、草稿不触发、批准后仅触发一次；launchd 增量未量化；Coordinator 自然语言路由未实测 | c82a8632 | 自然语言“每天/每周/定时/周期”**强制路由至 Solco Routine draft**，系统 launchd/cron/外部 task/匿名后台 loop 数量增量为 0，A |
| R02 | P0 | P | PARTIAL | `internal/schedule/text.go` `ParseText`; `api_routine.go` schedule/timezone | `text_test.go`, `routine_schedule_test.go` (DST/周/中英文/一次性→unsupported/歧义→needs_clarification) | c82a8632 | Go 内建立单一“文字时间表达式→时区→机器可验规则→下次 3 次预览”解析器；不确定时让 Assistant 问；涵盖 once/DST/sleep/wake，AC-02/10 |
| R03 | P0 | M | PARTIAL | 核心 `event_triggers`：Assistant 自有 task 事件(failed/blocked/uncertain/completed)→一个新任务；必须带 taskId/titleContains 过滤；草稿+审批卡才生效且只向前看；每事件至多一次(PK)；触发所产任务不再触发(防自激)；每日 5 次上限(顺延不丢)；`parent_task_id` 关联原因；Coordinator 只能 draft/list，pause/cancel 属于人 | TestTriggerStartsOneLinkedTaskPerEventAndNeverRecurses/FilterOwnerAndRefusals/PauseWithdrawAndDailyCap/DeniedOrWithdrawnDraftNeverFires、TestScopedCallerDraftsATriggerForItselfOnly；Host/桌面/手机 UI 与 webhook/PR 事件未做；AC-11 GUI 实机待验 | | Routine 需支持按事件运行/联合时间条件、去重、过滤、循环抑制和 Owner；单个事件不重复执行，AC-11 |
| R04 | P0 | P | PARTIAL | 规则调度器：(rule,occurrence) 事件唯一键、单事务推进水位、flock 单 daemon、catch-up/missed、overlap skip/queue、draft/paused 不触发；新增两进程竞争测试 | TestTwoSchedulersRacingFireAnOccurrenceOnce(-race ×3)、TestCatchUpAndMissedMatchTheOldScheduler、TestOverlapSkipAndQueueMatchTheOldScheduler；真实睡眠唤醒与关页面实机待验 | | Scheduler 是唯一启动权限；occurrenceKey 唯一、跨进程锁、sleep catch-up/错过、overlap skip/queue、水位不重置；关闭页面仍运行 |
| R05 | P0 | P | PARTIAL | Routine 审批严格保留：草稿不触发、批准只在定义哈希未变时生效、被改失效再审、撤销/暂停/恢复、SIGKILL 恢复；GUI/手机走同一核心 requestId 收据 | TestRoutineChangedAfterApprovalIsNeverEnabled、TestEditedActiveRoutineAsksAgainInsteadOfGoingQuiet、TestRoutineCancelPauseAndDenial、routine_approval_audit_test；Android 同源收据实机待验 | | Solco 的**审批机制比上游更严格，应保留**；草稿/批准/被改失效/撤销/暂停/恢复必须 GUI 和 Android 同源收据，AC-02/10 |
| R06 | P0 | B | PARTIAL | `api_routine.go` 个人 Routine 默认 OMP headless + 私有目录 | `TestE2EScenarioOneRoutineFromChatToVerifiedDigest`（真实 Herdr+OMP，受控页） | c82a8632 | Repo-less 个人情报 Routine 必须从 Chat 创建成功、使用 OMP headless；不能因为路径校验失败而改用 OS scheduler，AC-02 |
| R07 | P0 | P | DONE(软件) | `heartbeat.go`/`watch.go`/`supervisor.go` 锁 `AssistantAgentKind` | `gap_agent_lock_test.go` RED(claude)→GREEN + 静态扫描 | 17cdd98 | **不止 Task 和 Routine，Heartbeat/自发 Worker 的 Agent 都必须锁 OMP**；全路径查找隐形 Provider 漂移，AC-09 |
| R08 | P1 | P | OPEN | | | | 显示触发来源、下次运行时区、上次 Run 结果链接、Owner、停用原因、技能和预算；支持预览/编辑及变更重新审批，AC-02/10 |
| R09 | P1 | P | OPEN | | | | 独立于 UI/原 Coordinator 生死，重启/并发请求只能一份调度器；长时设备启动/睡眠/窗口关闭/用户登出应定义真实保证和已知限制，AC-07/10 |
| R10 | P1 | P | OPEN | | | | 任意一次 Routine 都带 owner/request/trigger/occurrence/worker/result/report 链；日报中的链接不是“Agent 说了就 |
| A01 | P0 | B | PARTIAL | `wake.go` queueCoordinatorWakeTx（Heartbeat→coordinator_reports kind=wake，投递到原 Coordinator pane/revision；预算/静默/Proactivity 由核心裁决） | `TestGapA01WakeGoesToTheOriginalCoordinatorWithItsCheckpoint`；e2e `TestE2EWakeTurnSavesACheckpointThroughTheRealExtension`（真实 Herdr+OMP：唤醒回合经扩展调用 checkpoint.set，回复由 transcript 证明） | (本批) | 仍缺：真实场景下 inspect→act 的多步行动证据（AC-05/07）、Mac.app 实机 |
| A02 | P0 | P | PARTIAL | `report_herdr.go` ReportReady/Dialog/SubmitReport 由 daemon 与 e2e 共用 | e2e `TestE2EFactReachesTheOriginalCoordinatorExactlyOnceAndTheReplyIsProven`（真实 OMP Coordinator：事实只投递 1 次，回复由 OMP transcript 证明） | (本批) | 已做：pending 合并（wake_pending）、忙时排队、6h 未投递过期并留 fact、Quiet/Respond/Suggest 不唤醒；仍缺：、出站状态和最终回复收据关联；中断不得无限重放，AC-05/07 |
| A03 | P0 | M | PARTIAL | `checkpoint.go`（v23 assistant_checkpoints，revision 栅栏、核心盖章 LastSeq）、API `assistant checkpoint get/set`、扩展 `checkpoint.get/set`，唤醒提示带上一份 checkpoint | `TestGapA03CheckpointIsDurableFencedAndCoreStampsTheObservedSeq`；扩展测试；e2e wake 回合 | (本批) | 仍缺：完成目标时的 checkpoint 证据校验、桌面/手机展示 |
| A04 | P0 | P | PARTIAL | `grants.go`/`api_grant.go`(v27 capability_grants + authorization_receipts)：Owner 封印(Ed25519)、不可变、显式 scope、maxPerDay、TTL≤90d、deny 优先、子范围取交集、fail-closed(无公钥=不放行)、Owner 专属能力不可授予；威胁模型 `assistant-owner-agent-identity-threat-model.md` | `TestGrantsFailClosedWithoutOwnerKey`/`ForgedGrantRowsAreInert`(变异验证)/`UnreadableGrantRowIsInert`/`OwnerOnlyAndHighRiskRules`/`DenyWins…`/`ChildScope…`/`CoordinatorCannotTouchGrants…`；残余风险 tripwire `TestKnownResidual…`；隔离实例实测 E0–E5 | (21009b90, b638aeae, 本批) | 已接线 memory.autosave（Agent 提议经 authorizeTx 自动保存，真二进制 e2e `TestRealPluginBinaryOwnerGrantAutosave`）+ Owner 端签名入口（`assistant owner status/register/rotate` + 桌面 More ▸ Autonomy）；信任锚按用户决定留在 L1；缺：global→Assistant→Goal 继承与版本化、桌面/手机展示 |
| A05 | P0 | P | PARTIAL | 系统自发起的入口已统一受 proactivity 约束：Supervisor 派任务、heartbeat 模型唤醒(wakeBudget)、新增 trigger 起任务(Respond/Suggest 不启动并放弃该事件；Quiet Hours 与每日预算顺延不丢；扣同一个 dispatch 预算)；Webhook 经 heartbeat 同样受限 | TestTriggerObeysProactivityLevelQuietHoursAndBudget(RED→GREEN)、TestHookEventsAreJudgedByTheHeartbeat；预算账本 v28 `budget_events`（Supervisor/trigger/heartbeat wake 的 spent/held+原因，同日同因只记一行，保留 14 天，只存 id 与原因）+ `assistant budget show` + 桌面 Behavior ▸ Today's ledger（耗尽可观察）；TestBudgetLedger*；token/成本**不可测**（Agent 在 Herdr pane 里跑，核心看不到用量，界面如实标 unmeasured，需单独产品决策才接各 provider 会话文件）；并发由 Dispatcher MaxParallel=2 + 轮转 + 每目标 fan-out 管；Group/人批准的 Routine 按产品决策不计入；AC-05/09 GUI 待验 | | Budget 不能只计 Task：要覆盖唤醒、模型 token/cost、工具频率、并发、安静时段、没有授权时不自发外部动作；限额耗尽可观察而非偷跑，AC-05/09 |
| A06 | P1 | P | PARTIAL | 事件路由：任务事件(含 R03 trigger)与新的外部 `hook.check` 事件进同一 events 水位，由 heartbeat 统一裁决(审批/预算/Quiet Hours/合并)；Webhook 入站 `POST /api/v3/hooks/assistants/{id}/wake`(逐 Assistant 可撤销 HMAC 密钥、5 分钟窗、delivery 去重、每密钥 60/h 限流、体积 16KiB、资源归属) | TestHookNamesOneTaskOfOneAssistant/ReplayConcurrencyAndConflict/AttacksAreRefusedAndRecordNothing/ReasonIsDataNotInstruction/StormIsRateLimited/EventsAreJudgedByTheHeartbeat(-race)、TestWebhookAgainstTheRealCore(Host→CLI→SQLite)；密钥/收据/触发器管理面已有 Host API(personalAgentAutonomy)+桌面 More ▸ Autonomy(2026-11-05；TestPersonalAgentAutonomyRoutes、TestAutonomyManagementAgainstTheRealCore、TestAssistantsAutonomyBridgeForwardsThroughTheCore，GUI 待实机验)，手机仅 Zod 契约无页面；跨 Goal/Routine 统一 inbox 与 PR 事件未并入；HOOK-05/06/07 与 Mac 真休眠未验 | | 多 Goal、多 Routine、inbox、worker stuck、时间与事件汇成统一事件路由/水位；只在变化或合理到期时唤醒，保证不刷屏，AC-05/11 |
| A07 | P1 | P | OPEN | 已有基础（未算完成）：Supervisor 确定性派发就绪里程碑、1×/2×/4× 退避最多 3 次提醒、replan ≤5、blocked 里程碑进 inbox；缺：补充研究/发起审批的“下一动作选择”与有上限的自动 replan | | | 主动跟进不仅“发一张任务事实卡”：按已批准目标选择下一动作、补充研究、判断阻塞、发起审批/请求用户，不能无上限 replan，AC-05 |
| A08 | P1 | P | PARTIAL | 分层现状：长期=`memory_items`(候选→批准，来源引用必填，版本/纠错/忘记，凭据拒收，Grant 低风险自动批准)；短期=`assistant_checkpoints`(A03，不写记忆)；新增：Agent 提议与已保留记忆同词(忽略大小写/空白)时返回既有条目，不产生第二个待批候选/回执(`memory.go existingMemoryTx`) | `TestAgentProposalOfAnExistingMemoryReturnsTheExistingItem`(RED→GREEN)，`TestContextBudgetIsBounded` 改为互异内容 | (本批) | 仍缺：commitment 与记忆的显式分层展示、敏感类别(健康/财务)隔离策略、checkpoint→记忆的晋升规则；仅 Agent 作者去重，文件/人工路径各自裁决，AC-05/09 |
| A09 | P1 | M | PARTIAL | Webhook 回执只表示 accepted(已持久记录)，不冒称已检查；routing 诚实标 heartbeat/held/recorded；唤醒仍走 wake outbox+真实回复核验(既有) | 同上；真实回合关联/unknown 语义沿用 coordinator_report 既有测试；GUI 实机待验 | | 计划执行唤醒、结果汇报与聊天消息必须分别关联真实回合；真实回复没找到→unknown，不得冒称已反馈，AC-06 |
| A10 | P2 | E | OPEN | | | | 不照搬一 Space 一 Agent 的 UI；允许跨空间 Personal Assistant 目标，但必须限制跨机器、跨身份和跨技能默认访问，AC-09 |
| Q01 | P0 | B | PARTIAL | 无证据不 done（Worker 类 run） | `result_test.go`；Task 类型谓词待 | 17cdd98 | 不允许把 Agent “已完成”文本、无结果的 RunDone 或原聊天闲置作为成功标准；根据 Task 类型定义可验谓词，AC-04/08 |
| Q02 | P0 | P | PARTIAL | `review_owner.go`：Owner 三种回答——Accept(E03 request-merge→批准→merge)、**Resume**(同 worktree/分支、attempt+1、Owner 反馈并入 brief、撤回待决合并卡、审核→`changes_requested`)、**Reject**(任务取消、合并卡 denied、工作保留可检查、仍可手动 Retry)；均不合并、不置 Done；CLI `assistant review resume|reject`，Coordinator `--as` 一律 forbidden | `review_owner_test.go`(6 条，含突变：去掉撤卡即红)、`TestAPIReviewResumeAndRejectThroughTheCLI`、`api_scope_test` forbidden | (本批) | 仍缺：桌面/手机 UI（根端目前没有任何 review/merge 的 bridge 或页面——E03 只有 CLI）、research 与 coding 的 Done 证据在 UI 上的分区展示；真实 Herdr+OMP 的 Resume 回合未跑，AC-08 |
| Q03 | P1 | P | OPEN | | | | 任务继承审批/检查政策；仅明确授权才自动执行可逆操作，外部发布/删除/推送/合并默认需要人，AC-08/09 |
| Q04 | P1 | M | OPEN | | | | 研究报告、浏览器快照、日志摘录、Git diff、验证步骤统一 Artifact/SourceRef，前后端能打开并检查；不可只展示一句“完成”，AC-04/08 |
| Q05 | P1 | P | OPEN | | | | 统一 Needs you 卡：trust/permission、Skill 未安装、token 失效、review fail、网络失败；卡动作回到原 Task/Run，点击可操作， |
| Q06 | P1 | P | OPEN | | | | “事实更新”与“Omp 生成并证实发送的语言回复”区分，附 source、不要同一事实推送多个内容，AC-06 |
| Q07 | P2 | E | OPEN | | | | Personal Agent 生活研究任务用交付/真实性/时效性验收，不能强制 review/merge Git；编码任务保留成熟 PR 审核流，AC-02/08 |
| U01 | P0 | P | OPEN | | | | 在 Assistant 原 Chat 就能看到 Goal、Task、Routine、当前进度、阻塞、下次运行、证据；每条有真实 ID 深链，AC-01/03 |
| U02 | P0 | P | OPEN | | | | 草案审批卡、主动性开关、计划审批、再次执行/暂停/恢复，错误必须就地可见、不重置输入；真机截图与 AX 对照，AC-02/08 |
| U03 | P0 | P | OPEN | | | | Mac/Android 都读相同 Assistant store，不是手机自己起 Agent；同一审批和结果应一致，手机断线重连按游标恢复，AC-06/12 |
| U04 | P0 | V | OPEN | | | | 已有离线推送代码**不等于设备级送达**；需注册 Android 真机 Token、OS 通知权限、Expo ticket/receipt、App 读取 ACK/原状态一致；AC- |
| U05 | P1 | P | OPEN | | | | 任务卡一键打开对应 Herdr Pane/Browser/Files/Diff/Artifact/PR，回到原聊天保持状态，不再建第二套右侧栏，AC-04/08 |
| U06 | P1 | P | OPEN | | | | 输入框、发送/停止、附件、状态、技能提示、模型切换遵循共享 ChatView 交互，尤其启动第一条消息不得只停在草稿，AC-01 |
| U07 | P1 | P | OPEN | | | | 用户看得懂“何时醒/能做什么/为什么没跑/下次报告”，并可在手机修改/暂停，显示操作回执和版本冲突，AC-05/10 |
| U08 | P1 | E | OPEN | | | | 可选而非阻断：不造第二个 Chat 生态，现有能力复用；无实现的单项标计划外，不假称 Shepherdr 全 parity，AC-12 |
| U09 | P1 | P | OPEN | | | | 运行中/堵塞/未启动、真实 Pane、所属 Agent、能力版本与 skill 缺失 UI 区分；不能给“已运行”假状态，AC-01/07 |
| U10 | P2 | E | OPEN | | | | Multi-Machine 先定义可见性/离线/Owner/Host 选择策略，保留 Herdr 原生多机机制；先不跨主机写任务，直到 API 与授权真实可测，AC-09 |
| U11 | P2 | P | OPEN | | | | 账号令牌与浏览器登录态由授予的本机能力使用，Prompt/CLI/Push/Fact 不存 secret；任务需授权时发批准卡，AC-09 |
| L01 | P0 | B | PARTIAL | `dispatch.go work`: 守护进程停止时 Agent 恰好完成，不用已取消 ctx 读结果（不再误 park 为 uncertain），留 running 由下一 daemon Resume | e2e `TestE2EDaemonRestartMidRunReattachesAndRecoversTheResult`（真实 Herdr+OMP：重启后重连、读回同一结果、仅 1 次 run；修复前 uncertain）| (本批) | 必须验证多进程/重复请求/杀进程断点：一份 Go daemon、一个 occurrence→一个 Task，无双派发，AC-07/10 |
| L02 | P0 | P | OPEN | | | | Herdr 临时失联/Agent 中断/信任目录都应有可诊断状态、重连和人工修复指引，无法确认时绝不自动重发，AC-07 |
| L03 | P0 | P | OPEN | | | | 定义 DB/automations.yaml/私密记忆的一致性备份恢复与迁移/回滚；版本升级不得清空任务/审批/收据，AC-13 |
| L04 | P0 | B | OPEN | | | | 关闭 Solco 窗口后调度仍运行的承诺必须由真实后台服务支撑；登出/机器睡眠期间不得声称会执行；醒来按 catch-up 策略追踪，AC-10 |
| L05 | P1 | P | OPEN | | | | 建“3.1.3 逐项契约用例”而不是套用 2.1 goldens；每个 Gap 必须有 red-before / green-after / 真实运行记录，AC 全集 |
| L06 | P1 | V | OPEN | | | | CLI/Host/手机的 timeout、断连、写入收据、不可重试错误类别统一；防无限后台 goroutine/未完成 HTTP 持有，AC-07/12 |
| L07 | P1 | P | OPEN | | | | 任务停止/结束后按 retention 规则清理临时浏览器/文件/Herdr Pane；默认留可检查结果，不能删未提交工作，AC-08/13 |
| L08 | P2 | E | OPEN | | | | 不追求逐像素复刻 Shepherdr；对 UX/功能对齐，独立审核移动可达性、深浅主题、无障碍、慢网、中文任务名和高频通知，AC-12 |

## 2026-11-05 Mac.app 隔离实例 AC-01 批次

证据：`docs/debug/assistant-shepherdr-3.1.3-acceptance/2026-11-05-mac-app-ac01.md`。

- U06 / AC-01 Mac 段：创建 Assistant、首条消息只送达一次、恰好一个 Coordinator、Chat 页渲染对话 — **PASS**（沙箱补装 Herdr omp 集成与 chat-stream 之后；先前"Working…"判为沙箱缺口）。
- U01 / U06 新发现：Coordinator 启动简报以用户消息出现在对话里（132 行），待产品决定；U09/C02：身份缺失时 Chat 只显示 Working、没有诊断，待 capability doctor。
- AC-01 整体仍 **未通过**：能力清单的真实工具调用、回执可见性、ID 深链、Android 同一对话均未在 GUI 验到。


## 2026-11-05 Mac.app 隔离实例 AC-02 / AC-03 批次

- AC-02（定时 Routine）Mac 段 **PASS**：草案→审批卡→批准→到点触发（任务归属 Ada、系统助手无任务）→skill 关卡→批准后自动重启→`routine_done`；报告 `docs/debug/assistant-shepherdr-3.1.3-acceptance/2026-11-05-mac-app-ac02.md`。
- 修复：`routine_failed` 带稳定原因码；手改已批准 Routine 后暂停+事实+新审批卡（R05/R08）；系统 Automations 助手无聊天框且 Coordinator 启动被拒；自动状态请求/唤醒折叠为 System instructions 芯片；Routine 结果进入 Coordinator 报告（实机验证）。
- AC-03（两项并发 + 第三项依赖）Mac 段 **PASS**（A/B 同毫秒启动，C 在二者完成后 1.7 s 启动，D 等人确认）；修复 worker 标题被 `Solco-Run-Id` 占据；报告 `2026-11-05-mac-app-ac03.md`。
- 开放：F4（有计划的草稿 Goal 不进 Needs you）；Coordinator 用自然语言自写计划未用真模型验证；Android 全部未做。
- 重建实例复验：F1（手改 Routine → 暂停 + 事实 + 新审批卡）、F2（系统助手无聊天框）、自动状态请求折叠为 “System instructions” 芯片均在实机通过；隔离实例已清理。

- F4：有计划的草稿 Goal 现在进 Needs you（`InboxItem.GoalID`，Open → Watching）；系统 Automations 助手头部不再显示 Coordinator 状态。

- 2026-11-05 e2e：`--as` 路径经真实 OMP 扩展通过（checkpoint.set、goal.pause 自有/越权）；Coordinator 启动、坏扩展惰性、事实回报各 e2e 仍通过。
