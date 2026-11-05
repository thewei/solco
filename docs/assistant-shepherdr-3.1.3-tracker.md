# Shepherdr 3.1.3 对齐追踪表（68 项 Gap）

> 事实账本。每项状态只能是 OPEN / PARTIAL / DONE(软件) / PASS(真机) / BLOCKED。**DONE 只表示代码+自动测试 GREEN；只有 AC 真机证据齐全才可写 PASS。**
> 规划：`assistant-shepherdr-3.1.3-alignment-roadmap.md`。上游固定 `dc1b67824924c02fde6765f184bcd2a420743b8a`。

## 进度日志

- 2026-11-05 M0：基线（plugin 全量 GREEN 308+、root GREEN、race 见下）；修复预存红测 `TestRoutineSIGKILLAfterYAMLRecoversOnNewProcess`（子进程仍用 claude，被 OMP 锁拒绝）。
- 2026-11-05 M2.1：W02/W03/W06/Q01 结果回收核心（见各行）。

| ID | 优先级 | 标记 | 状态 | 源码位置 | 测试 / RED→GREEN | commit | 闭环条件摘要 |
|---|---|---|---|---|---|---|---|
| C01 | P0 | B | PARTIAL | 同 C03 | e2e 证实 Prompt→受控 ToolCall→Task(owner)；外部 shell 被拒 | (本批) | **自然语言请求不能退化为普通 OMP 自行执行**；必须实际观察 Prompt→受控 ToolCall→Solco receipt→Task/Goal/Routine ID；拦截；缺：真实自然语言(非脚本模型)与 Routine/Goal 路径的真机观察|
| C02 | P0 | B | PARTIAL | `--tools` 原生白名单 + tool_call 闸 + 5 参 ctx 取 cwd 身份 | e2e：坏扩展→不可用而非无闸 | (本批) | OMP extension 的“工具白名单”**不等于可信身份授权**：设计 scoped Broker 服务端校验 AssistantId/动作/Owner/能力/TTL/重放；；仍缺：服务端校验的 scoped token/裸 CLI 的 Owner 边界|
| C03 | P0 | V | PARTIAL | `extensions/solco-coordinator.ts` `coordinator.go CoordinatorTools` `host/agent.go explainStart` | 真实 Herdr+OMP e2e：扩展被加载、bash 被拦截、solco_assistant 建出属主 Task；坏扩展→OMP 拒绝启动(inert)并带原因；`bun test` 7；Go 白名单一致性测试 | (本批) | **打包/安装/真实 Herdr Agent --extension 是否成功加载与可调用**未证实；安装前和运行时 doctor 必须反馈缺失，不得静默变通，AC-01；真机发现：①OMP 扩展加载失败仍继续运行(未设闸) ②execute 签名为 5 参(ctx 第 5 位)，Bun 单测未暴露 ③import.meta.dir 在 OMP 加载器中不可靠。安装包/doctor 与 Mac.app 实机待做|
| C04 | P0 | P | OPEN | | | | 已有重复启动 fence，但停机后实体 Pane/Agent 未必结束；安全 stop/restart、trust/permission blocked/uncertain 单元与 |
| C05 | P0 | P | PARTIAL | `CreateTaskInput.RequiredSkills` | TestGapW04RequiredSkillsAreValidatedAndStored | (本批) | 强制 schema：objective、scope、requiredSkills、inputs、deliverables、acceptance/evidence、constrain；其余 schema 字段(deliverables/acceptance…)未强制|
| C06 | P1 | M | OPEN | | | | 用户 Chat Turn→requestId→Goal/Task/Routine→Worker→Evidence→Report 需要**可追踪父子链**；同一问题不得凭模型记忆创建 |
| C07 | P1 | P | OPEN | | | | 原 Coordinator 状态如何接续“下一步/完成条件”需可读写 durable progress、人工批准、可停止/恢复；不能把 ReportPump 当自主执行循环，AC- |
| C08 | P1 | P | OPEN | | | | Coordinator 应能“这项先暂停/把第二项继续/告诉我为何卡住”，做 owner-scoped 状态读/写且必须有收据、语义一致的错误，AC-03/05 |
| C09 | P1 | P | OPEN | | | | Restart/Context compact 后重新建立确切上下文（Active Goal、Task、Routine、Pending Approvals、上次报告游标）；不能无凭 |
| C10 | P1 | E | PARTIAL | `skill_grants` + 审批卡 subjectKind=skill | TestGapC10UngrantedSkillAsksOnceAndApprovalRequeues | (本批) | **不复制每个 repo 一个 Assistant**：定义个人 Assistant 可授权哪些 Workspaces/Hosts/Apps/Skills，跨项目只能在显式 sco；Skill 授权已做；Workspaces/Hosts/Apps 范围未做|
| W01 | P0 | B | OPEN | | | | 定义唯一 `HerdrWorkerAdapter`（名称可调）：只通过现有 Herdr API/Skill 协议启动、读状态、等结束、恢复，返回 Herdr 身份与 Solco r |
| W02 | P0 | B | PARTIAL | `result.go` `ompresult.go` `dispatch.go finishWithResult/Recollect` `task.go FinishRunWithResult` `store.go v20` | `result_test.go` TestGapW02* RED(done+空 Result)→GREEN；真机待；隔离真实 Herdr+OMP e2e PASS(skills=[fixture-browser], bash=1, 4 grounded sources)，Android/Mac.app 待 | 17cdd98 | **当前结果回收核心缺口**：必须读到真正的 Worker 输出/最终消息并携来源（agentSessionId、messageId、读到的 byte range/文件 hash/ |
| W03 | P0 | B | PARTIAL | 同 W02：结束≠完成，读不到答案→uncertain `result_unknown` | `result_test.go`；blocked/vanished 细分待 M2.2 | 17cdd98 | 完成判定要区分“Agent idle / final turn / blocked / vanished / timed out / prompted but unaccepted |
| W04 | P0 | P | PARTIAL | `skills.go` `dispatch.go skillGate/unusedSkills` schema v21 `task_skills` | `skills_test.go` TestGapW04*: 缺失→failed skill_missing 且 0 pane；未授权→审批卡→批准后自动重排；用了才算完成；真实 OMP 经 `skill://fixture-browser` 读取并被 transcript 证实 | (本批) | Worker launch 必须显式协商 Skills；已安装的 `~/.agents/skills/herdr/SKILL.md` **要求 HERDR_ENV=1**；不能假定；缺：能力 doctor 列出可用 skill、Routine 路径(规则 run)同样检查、真实 ego-browser/登录态|
| W05 | P0 | M | OPEN | | | | X.com Worker 真正由 OMP 加载指定 `ego-browser` 执行，浏览器会话/登录态由用户授权且限任务；证明调用、URL、去重、失败；技能缺失或未授权不得偷换浏 |
| W06 | P0 | M | PARTIAL | `WorkerResult` 含来源/工具/技能/grounded URL | `result_test.go`；Artifact/Proof 卡待 M5 | 17cdd98 | 定义 `WorkerResult/Proof`：摘要、结构化数据、引用/截图/文件、验证方法、失败/不确定项、需要人决策；事实与 Agent 自述分离；无证据就不自证成功，AC-0 |
| W07 | P0 | P | PARTIAL | `host/agent.go await`/`launcher.go Await`: `agent_not_found`(herdr 0.9.5 关闭工作区后的真实回码)按 gone 处理；cancelled 带原因 | `TestAwaitGivesUpWhenHerdrAnswersAgentNotFoundForAClosedPane`；e2e `TestE2EWorkerVanishingMidRunIsNeverReportedAsDone`（修复前 150s 仍悬挂，修复后 ~13s 结束且不伪造 done/result） | (本批) | 状态机应具备 queued/start/working/blocked/waiting_owner/awaiting_proof/ready/done/failed/uncerta |
| W08 | P1 | P | OPEN | | | | 验证 Assistant/Host/全局并发和资源预算，重启不超过配额；优先级、取消、依赖链和非 Git 工作目录需公平调度，AC-05/07 |
| W09 | P1 | P | OPEN | | | | OMP-first 显式化 Model、thinking、tool grants、skill grants、超时、可更改范围；继承 Assistant Policy 但任务权限只可 |
| W10 | P1 | P | PARTIAL | `host/receipt.go`(Worker=pinned dir; Coordinator=OMP 会话目录+run marker) | Coordinator e2e 未再重发 | (本批) | Herdr launch 不确定要查询 existing Pane/Agent、receipt，单次 submit 不重发；故障恢复演练“旧 Agent 仍在/丢帧/重启”不得双|
| W11 | P1 | E | OPEN | | | | 编码隔离分支保持、非代码用 Assistant 私有工作目录；按产品任务 scope 执行、不污染 repo 的工作树；不能把个人浏览器 Routine 强行当 Git 工程，AC |
| W12 | P2 | P | OPEN | | | | 先保证 OMP 本身恢复，不偷偷 fallback 到别的 provider；以后才在明确授权和同一 taskId 下增加换代理/续跑，AC-07 |
| R01 | P0 | B | PARTIAL | Coordinator 工具白名单(无 bash) + `solco_assistant routine.draft` | 同上 e2e：crontab 无变化、草稿不触发、批准后仅触发一次；launchd 增量未量化；Coordinator 自然语言路由未实测 | c82a8632 | 自然语言“每天/每周/定时/周期”**强制路由至 Solco Routine draft**，系统 launchd/cron/外部 task/匿名后台 loop 数量增量为 0，A |
| R02 | P0 | P | PARTIAL | `internal/schedule/text.go` `ParseText`; `api_routine.go` schedule/timezone | `text_test.go`, `routine_schedule_test.go` (DST/周/中英文/一次性→unsupported/歧义→needs_clarification) | c82a8632 | Go 内建立单一“文字时间表达式→时区→机器可验规则→下次 3 次预览”解析器；不确定时让 Assistant 问；涵盖 once/DST/sleep/wake，AC-02/10 |
| R03 | P0 | M | OPEN | | | | Routine 需支持按事件运行/联合时间条件、去重、过滤、循环抑制和 Owner；单个事件不重复执行，AC-11 |
| R04 | P0 | P | OPEN | | | | Scheduler 是唯一启动权限；occurrenceKey 唯一、跨进程锁、sleep catch-up/错过、overlap skip/queue、水位不重置；关闭页面仍运行 |
| R05 | P0 | P | OPEN | | | | Solco 的**审批机制比上游更严格，应保留**；草稿/批准/被改失效/撤销/暂停/恢复必须 GUI 和 Android 同源收据，AC-02/10 |
| R06 | P0 | B | PARTIAL | `api_routine.go` 个人 Routine 默认 OMP headless + 私有目录 | `TestE2EScenarioOneRoutineFromChatToVerifiedDigest`（真实 Herdr+OMP，受控页） | c82a8632 | Repo-less 个人情报 Routine 必须从 Chat 创建成功、使用 OMP headless；不能因为路径校验失败而改用 OS scheduler，AC-02 |
| R07 | P0 | P | DONE(软件) | `heartbeat.go`/`watch.go`/`supervisor.go` 锁 `AssistantAgentKind` | `gap_agent_lock_test.go` RED(claude)→GREEN + 静态扫描 | 17cdd98 | **不止 Task 和 Routine，Heartbeat/自发 Worker 的 Agent 都必须锁 OMP**；全路径查找隐形 Provider 漂移，AC-09 |
| R08 | P1 | P | OPEN | | | | 显示触发来源、下次运行时区、上次 Run 结果链接、Owner、停用原因、技能和预算；支持预览/编辑及变更重新审批，AC-02/10 |
| R09 | P1 | P | OPEN | | | | 独立于 UI/原 Coordinator 生死，重启/并发请求只能一份调度器；长时设备启动/睡眠/窗口关闭/用户登出应定义真实保证和已知限制，AC-07/10 |
| R10 | P1 | P | OPEN | | | | 任意一次 Routine 都带 owner/request/trigger/occurrence/worker/result/report 链；日报中的链接不是“Agent 说了就 |
| A01 | P0 | B | OPEN | | | | **当前“模型心跳”≠原 Coordinator 主动承担目标**；要唤醒原 Assistant（或可追溯的受控等效回合）执行 inspect→choose→act→evaluat |
| A02 | P0 | P | PARTIAL | `report_herdr.go` ReportReady/Dialog/SubmitReport 由 daemon 与 e2e 共用 | e2e `TestE2EFactReachesTheOriginalCoordinatorExactlyOnceAndTheReplyIsProven`（真实 OMP Coordinator：事实只投递 1 次，回复由 OMP transcript 证明） | (本批) | 唤醒事件去重、pending 合并、忙时排队、未投递过期、出站状态和最终回复收据关联；中断不得无限重放，AC-05/07 |
| A03 | P0 | M | OPEN | | | | 独立的 Assistant Autopilot checkpoint：last observed event seq、进展、下一动作、阻塞、评估、终止条件；完成时必须证明事实，AC |
| A04 | P0 | P | OPEN | | | | 个人版继承模型：global defaults → Assistant override → Goal/Routine explicit grants；版本化、权限递减、真实 UI |
| A05 | P0 | P | OPEN | | | | Budget 不能只计 Task：要覆盖唤醒、模型 token/cost、工具频率、并发、安静时段、没有授权时不自发外部动作；限额耗尽可观察而非偷跑，AC-05/09 |
| A06 | P1 | P | OPEN | | | | 多 Goal、多 Routine、inbox、worker stuck、时间与事件汇成统一事件路由/水位；只在变化或合理到期时唤醒，保证不刷屏，AC-05/11 |
| A07 | P1 | P | OPEN | | | | 主动跟进不仅“发一张任务事实卡”：按已批准目标选择下一动作、补充研究、判断阻塞、发起审批/请求用户，不能无上限 replan，AC-05 |
| A08 | P1 | P | OPEN | | | | 长期记忆/commitment 与短期 checkpoint 分层；只写用户授权事实，去重/纠错/来源引用/敏感数据隔离，AC-05/09 |
| A09 | P1 | M | OPEN | | | | 计划执行唤醒、结果汇报与聊天消息必须分别关联真实回合；真实回复没找到→unknown，不得冒称已反馈，AC-06 |
| A10 | P2 | E | OPEN | | | | 不照搬一 Space 一 Agent 的 UI；允许跨空间 Personal Assistant 目标，但必须限制跨机器、跨身份和跨技能默认访问，AC-09 |
| Q01 | P0 | B | PARTIAL | 无证据不 done（Worker 类 run） | `result_test.go`；Task 类型谓词待 | 17cdd98 | 不允许把 Agent “已完成”文本、无结果的 RunDone 或原聊天闲置作为成功标准；根据 Task 类型定义可验谓词，AC-04/08 |
| Q02 | P0 | P | OPEN | | | | 编码 Worker 必须留分支/差异/测试/Proof，Owner 选择 Accept/Reject/Resume；审核状态不和普通 research 结果共用无证据的 Done， |
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
