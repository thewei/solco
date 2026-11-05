# Solco Personal Assistant × Shepherdr 3.1.3：源码差距总审计、Go 对齐路线与真机验收规范

> 日期：2026-11-05 ｜性质：**目标规范 + 静态源码审计 + 待实施清单**（不是完成证明）
>
> **上游冻结**：eliasstravik/shepherdr，main，`dc1b67824924c02fde6765f184bcd2a420743b8a`，提交描述 `SHEP-36: Release Shepherdr 3.1.3 (#628)`，2026-11-05。2026-11-05 通过 `git ls-remote` 与独立浅克隆核对；[上游仓库](https://github.com/eliasstravik/shepherdr/tree/dc1b67824924c02fde6765f184bcd2a420743b8a)。
>
> **本地基线**：`/Users/wilson/Workspaces/wh-studio/solco`，读取时 HEAD `52d55e2`（`main`、超前远端 5 次提交），**存在他人/上轮尚未提交的 Assistant Core/OMP extension 修改**。以下对这些修改一律标“工作树已写/待实机”，绝不把它们算作已安装产品的能力；不得擅自清理、覆盖、提交、推送。移动端为同级目录 `../solco-mobile`，不在本仓库的提交里。
>
> **取证记录**：`docs/debug/2026-11-05-assistant-shepherdr-3-parity.md`（既有第一轮 P0 取证，不在本计划中重写）；历史规划 `docs/personal-agent-go-mvp-plan.md`、`docs/assistants-v3-audit-2026-11-04.md`、`docs/assistants-orchestration-flow-audit-2026-11-04.md`、`docs/assistants-coordinator-report-delivery-2026-11-05.md`。3.1.3 取代原来“v1.2.27 Rust Fork”和“2.1 单纯 Web 架构”的**功能对照基准**，但不取代 Solco 原生 Herdr/MyGo 架构权属。

## 0. 任务最终目标与不可混淆的语义

**目标**：Solco 是一个长期存在、能理解个人目标、主动安排工作并持续跟进的 Personal Assistant。它在用户同意的权限/预算/频率内，能把自然语言转成 Solco **Goal / Task / Routine / Commitment**，借唯一 Herdr Runtime 调度 OMP Worker，可靠读取结果和证据，更新长期状态，主动把需要知道/决定的事项通过**原 Assistant 会话和手机**交付。模型不是调度器、不是状态库，也不靠一次会话永远忙碌保持主动。

```text
用户发给 Assistant 的一条消息（request / chat-turn / assistantId）
    ↓ 受控能力目录 + OMP Coordinator：意图→目标/计划/技能依赖/验收标准
Solco Broker（身份和授权） ──────────┐
    ├─ GoalPlan / TaskGraph / Commitment / Progress / Memory
    ├─ Routine draft → 绑定定义的人工审批 → 唯一 Go Scheduler
    ├─ Heartbeat / Autopilot / Events / Budgets / Quiet Hours
    └─ Worker Request（runId、scope、skill、inputs、expected proof）
         ↓
Herdr Worker Adapter：真实 Herdr Session/Workspace/Pane/Agent API
         ↓ OMP + 可验证 skill 发现（Herdr Skill / ego-browser 等）
Worker 独立执行 → 输出、Agent 状态与来源证据 → Run Result/Artifact
         ↓
Go Supervisor：验收→依赖解锁/阻塞/复盘/下一步（不能自证成功）
         ↓
事实账本 + outbox + 原 Coordinator 受控唤醒 + Host/Android 通知
         ↓
用户看到结果、来源、下一次计划、需审批动作，可追溯到同一 requestId
```

**对齐 Shepherdr 的东西**：Assistant/task/worker 独立职责、自动化时钟与事件、新 Task 持久化、Autopilot 每次有限唤醒、progress 继承、任务 News、状态、Proof、审核、队列/并发/恢复、浏览器、跨设备、隐私、权限和 UI 交互。

**Solco 有意差异（不能照抄上游代码）**：① Go 单一后台调度 + SQLite 事实/收据、Solco Native UI/共享 Chat WebView；② Herdr 0.9.x Session/Pane API 是唯一 Agent 运行时，**不得偷偷引入 shepherdr 的 hc/herdr-core Python 运行时**；③ 一个 Assistant 可跨 repo/非代码生活任务，不强制一个文件夹一个 Assistant；④ Coordinator 和 Worker 先锁定 **OMP**，不提供 Claude/Codex 等假兼容；⑤ 非代码个人任务不必创建 Git repo/worktree；⑥ 任务、定时计划、记忆与需外部动作的审批要有更严格的人类权限；⑦ Agent 对个人网络服务只用**明确授权**的 Skills/账号能力，不假定上游浏览器或 Secret broker 可直接复用；⑧ 统一尊重 Solco 自己 Hook 优先级（不是让 Herdr Hook 接管产品状态）。

状态字段：**P=部分存在但缺端到端闭环**；**M=确认缺能力**；**V=有实现但尚无当前版本实机证明**；**E=有意不同并须实现等效契约**；**B=发布阻断级差距**。静态命中代码、历史测试 PASS、当前工作树改动、当前设备 PASS 四种证据不可互相替代。

## 1. 可复查源代码索引与读法

上游在隔离调研目录 `/tmp/solco-shepherdr-current-20261105`，所有上游 `U:...:行号` 指向上述 commit；可在 GitHub 的 `blob/dc1b67824924c02fde6765f184bcd2a420743b8a/路径#L行号` 精确查看。Solco `S:...:行号` 是审计当日工作树，最终实现改行号时须同步更新并附 commit SHA。推荐逐行打开而不只看 README：

| 领域 | 上游的可运行行为/测试入口 | Solco 可对照入口 |
| --- | --- | --- |
| Assistant/委派协议 | U:`templates/assistant.md:1-164`；`mods/coordinator/coordinator.js:1-25`；`mods/tasks/bin/herdr_task/commands.py:263,722` | S:`plugins/solco-automations/internal/personalagent/coordinator.go:68,106,329`；`plugins/solco-automations/internal/personalagent/dispatch.go:135,281` |
| 队列、Agent 与状态 | U:`mods/tasks/bin/herdr_task/agents.py:57`、`mods/tasks/bin/herdr_task/queues.py:163`、`mods/tasks/bin/herdr_task/tick.py:85`、`mods/tasks/bin/herdr_task/launch.py:277` | S:`plugins/solco-automations/internal/personalagent/task.go:180,452,551`、`plugins/solco-automations/internal/personalagent/dispatch.go:350,437,491`、`plugins/solco-automations/internal/personalagent/launcher.go:26,80,102,107` |
| 定时/事件、调度生命周期 | U:`mods/tasks/bin/herdr_task/automations.py:71,105,165,205,236,298,340,378,420,491` | S:`plugins/solco-automations/internal/personalagent/api_routine.go:112,239`、`plugins/solco-automations/internal/personalagent/routine.go:236`、`plugins/solco-automations/internal/personalagent/rules.go:360`、`plugins/solco-automations/internal/schedule/schedule.go:74` |
| Autopilot / Heartbeat | U:`mods/tasks/bin/herdr_task/autopilot.py:347,449,455,465,491,521,547,569,621,700,717`、`docs/parity/autopilot.md` | S:`plugins/solco-automations/internal/personalagent/heartbeat.go:254,282,390`、`plugins/solco-automations/internal/personalagent/proactivity.go:87,115,141`、`plugins/solco-automations/internal/personalagent/supervisor.go:127,257,385` |
| 完成证据与审阅 | U:`mods/tasks/bin/herdr_task/proof.py:33,88`、`mods/tasks/bin/herdr_task/settings.py:328,390`、`mods/tasks/bin/herdr_task/news.py:39,52,86,99`、`mods/accept-card/` | S:`plugins/solco-automations/internal/personalagent/review.go:124,348,482,519`、`plugins/solco-automations/internal/personalagent/goal_plan.go:408`、`plugins/solco-automations/internal/personalagent/supervisor.go:334`、`plugins/solco-automations/internal/personalagent/task.go:452` |
| 浏览器、其他机器、Secrets | U:`mods/tasks/bin/herdr_task/browsers.py:23,89`、`machines.py`、`home.py`、`service/browser.ts:28`、`service/secrets.ts:40` | S:`plugins/solco-automations/internal/personalagent/mcp.go:1-24`、`internal/remotehost/personal_agent.go`、`internal/secretstore/`；本地 Skills `~/.agents/skills/herdr/SKILL.md`、`ego-browser/SKILL.md` |
| 主动通知与聊天 | U:`mods/tasks/bin/herdr_task/news.py:99,201,227,395,406`、`mods/pane-chat/autopilot.js`、`web/plugins/coordinators/client.tsx` | S:`plugins/solco-automations/internal/personalagent/coordinator_report.go:179`、`plugins/solco-automations/internal/personalagent/report_reply.go:45-64`、`activity.go`、`frontend/src/assistants/Progress.tsx:63,86`、`internal/remotehost/assistant_push.go:312` |
| 回归与测试基准 | U:`core/autopilot.cases.json`、`core/autopilot.test.js`、`test/test_herdr_task.py`、`docs/parity/` | S:`plugins/solco-automations/internal/personalagent/*_test.go`、`internal/remotehost/*_test.go`、`frontend/src/*test*`、`../solco-mobile/src/features/host/*test.ts` |

**特别注意**：Shepherdr 3.1.3 的 `herdr-task` 实现在 Python+hc，Solco Go **对齐行为语义、持久字段、异常恢复与测试用例**，绝不能把其文件数据库或 `hc exec` 当 Solco 自己的新运行时。

## 2. 源码级差距清单（唯一实施 Backlog，逐项关闭）

每条都有：上游证据 → Solco 证据 → 差距/目标。优先级为 P0（发布阻断）、P1（达到好用）、P2（按场景选择），尾部 AC 指验收用例。

### A. Coordinator / 入口 / 可信能力 (C01-C10)

| ID/优先级 | 上游源码证据 | Solco 对照 / 现状 | 明确差距及关闭条件 |
| --- | --- | --- | --- |
| C01 P0 B | U:`templates/assistant.md:1-17,35-58` 用 Assistant 专职协调、`herdr-task` 统一加/启动任务 | S:`plugins/solco-automations/internal/personalagent/coordinator.go:329` 现提示词；`plugins/solco-automations/internal/personalagent/dispatch.go:149,216`；新增工作树 `extensions/solco-coordinator.ts` | **自然语言请求不能退化为普通 OMP 自行执行**；必须实际观察 Prompt→受控 ToolCall→Solco receipt→Task/Goal/Routine ID；拦截外部系统计划、无归属 async/subagent；见 AC-01/02 |
| C02 P0 B | U:`templates/assistant.md:9-17` 只让产品 CLI 改 Task | S:`plugins/solco-automations/internal/personalagent/api.go:288-388`、`plugins/solco-automations/main_assistant.go:16-64` CLI 任何本机账号可调用；`safety.go` 明言无系统沙盒 | OMP extension 的“工具白名单”**不等于可信身份授权**：设计 scoped Broker 服务端校验 AssistantId/动作/Owner/能力/TTL/重放；危险审批不能靠提示词自限；需越权负例 AC-09 |
| C03 P0 V | U:`templates/assistant.md:35-58` 明确工作目录/入口 | S:`plugins/solco-automations/internal/personalagent/coordinator.go:314-329`、`plugins/solco-automations/internal/personalagent/launcher.go:80-102`；未提交的 `extensions/solco-coordinator.ts` 仅 Bun 单测 | **打包/安装/真实 Herdr Agent --extension 是否成功加载与可调用**未证实；安装前和运行时 doctor 必须反馈缺失，不得静默变通，AC-01 |
| C04 P0 P | U:`mods/coordinator/coordinator.js:1-25,185` 一位置一个 Assistant，生命周期绑定真实 Agent | S:`coordinator.go:106-200,238-301`；`assistant_lifecycle.go` | 已有重复启动 fence，但停机后实体 Pane/Agent 未必结束；安全 stop/restart、trust/permission blocked/uncertain 单元与设备证明，AC-07 |
| C05 P0 P | U:`templates/assistant.md:29-33,60-82` 拆分任务、完整 Brief、Worker 新闻 | S:`plugins/solco-automations/internal/personalagent/coordinator.go:333-362`、`plugins/solco-automations/internal/personalagent/task.go:180-260`、`plugins/solco-automations/internal/personalagent/goal_plan.go:111-198` | 强制 schema：objective、scope、requiredSkills、inputs、deliverables、acceptance/evidence、constraints、retry/escalation、parentRequestId；不能只靠自然语言提示词，AC-03 |
| C06 P1 M | U:`templates/assistant.md:60-82,160-169` 状态需恢复自真实任务图 | S:`plugins/solco-automations/internal/personalagent/coordinator.go:339-360`、`context.go`、`plugins/solco-automations/internal/personalagent/supervisor.go:127` | 用户 Chat Turn→requestId→Goal/Task/Routine→Worker→Evidence→Report 需要**可追踪父子链**；同一问题不得凭模型记忆创建重复项目，AC-03/05 |
| C07 P1 P | U:`templates/assistant.md:120-131` 每次短唤醒、保存进度 | S:`plugins/solco-automations/internal/personalagent/coordinator_report.go:179` 有事实驱动报告；`supervisor_report.go` 规则摘要 | 原 Coordinator 状态如何接续“下一步/完成条件”需可读写 durable progress、人工批准、可停止/恢复；不能把 ReportPump 当自主执行循环，AC-05 |
| C08 P1 P | U:`templates/assistant.md:84-103` 用户发话可定位 Task ID 执行、暂停、重试、提问 | S:`plugins/solco-automations/internal/personalagent/api.go:288-388`、`plugins/solco-automations/internal/personalagent/api_b.go:5-99`；Tasks/Goals CRUD 已有 | Coordinator 应能“这项先暂停/把第二项继续/告诉我为何卡住”，做 owner-scoped 状态读/写且必须有收据、语义一致的错误，AC-03/05 |
| C09 P1 P | U:`templates/assistant.md:160-164` 重启自任务事实恢复 | S:`plugins/solco-automations/internal/personalagent/dispatch.go:350-510`、`store.go`、`plugins/solco-automations/internal/personalagent/report_reply.go:45` | Restart/Context compact 后重新建立确切上下文（Active Goal、Task、Routine、Pending Approvals、上次报告游标）；不能无凭据续聊或重复发布，AC-07 |
| C10 P1 E | U:`templates/assistant.md:137-164` 多机/个人 home space 与 project space 一一绑定 | S:`plugins/solco-automations/internal/personalagent/assistant.go:29-55` 支持个人与跨项目 scope；`group.go` | **不复制每个 repo 一个 Assistant**：定义个人 Assistant 可授权哪些 Workspaces/Hosts/Apps/Skills，跨项目只能在显式 scope 内工作，AC-09 |

### B. Herdr Worker / Skill / Result / Proof (W01-W12)

| ID/优先级 | 上游源码证据 | Solco 对照 / 现状 | 明确差距及关闭条件 |
| --- | --- | --- | --- |
| W01 P0 B | U:`templates/assistant.md:35-58` `herdr-task add/start` 生成 Worker 并绑定父 ID | S:`plugins/solco-automations/internal/personalagent/dispatch.go:124-260`、`plugins/solco-automations/internal/personalagent/launcher.go:26-102` Herdr Session/Workspace/Pane | 定义唯一 `HerdrWorkerAdapter`（名称可调）：只通过现有 Herdr API/Skill 协议启动、读状态、等结束、恢复，返回 Herdr 身份与 Solco runId；不得加第二 PTY/后台 Agent 管理器，AC-04 |
| W02 P0 B | U:`templates/assistant.md:60-82` Agent 结束≠任务被验收 | S:`plugins/solco-automations/internal/personalagent/dispatch.go:281-315` 在 `Work()==nil` 时直接 `FinishRun(RunDone,"")`；`plugins/solco-automations/internal/personalagent/task.go:452` 会把空字符串作为结果 | **当前结果回收核心缺口**：必须读到真正的 Worker 输出/最终消息并携来源（agentSessionId、messageId、读到的 byte range/文件 hash/Herdr command receipt），未取得证据→`result_unknown`/待核对，绝不展示已总结，AC-04/06 |
| W03 P0 B | U:`mods/tasks/bin/herdr_task/news.py:86-121` 基于 Agent 状态形成 News | S:`plugins/solco-automations/internal/personalagent/launcher.go:107` `AgentWait`；`internal/herdr/herdr.go:299,341,348,374,447` 有状态与限量 pane read | 完成判定要区分“Agent idle / final turn / blocked / vanished / timed out / prompted but unaccepted / disconnected”；禁止仅把 wait 返回视为实质完成，AC-04/07 |
| W04 P0 P | U:`templates/assistant.md:35-58` Worker 接收完整 Brief + Settings Block | S:`plugins/solco-automations/internal/personalagent/dispatch.go:533-554` `BuildPrompt`；`plugins/solco-automations/internal/personalagent/mcp.go:1-24` | Worker launch 必须显式协商 Skills；已安装的 `~/.agents/skills/herdr/SKILL.md` **要求 HERDR_ENV=1**；不能假定 OMP 会发现它或在非 Herdr 面板可用。验证技能版本/可发现/作用域/缺失时报 blocked，AC-02/04 |
| W05 P0 M | U:`mods/tasks/bin/herdr_task/browsers.py:23-97` 与 `service/browser.ts:28` task-scoped 浏览器 | S:当前无等价托管浏览器；用户已安装 `ego-browser` Skill（非 Solco 授权事实） | X.com Worker 真正由 OMP 加载指定 `ego-browser` 执行，浏览器会话/登录态由用户授权且限任务；证明调用、URL、去重、失败；技能缺失或未授权不得偷换浏览器，AC-02 |
| W06 P0 M | U:`mods/tasks/bin/herdr_task/proof.py:33,88`、`mods/accept-card/` 输出可审查证据 | S:`plugins/solco-automations/internal/personalagent/review.go:348,482,519` 检查/合并动作存在；`plugins/solco-automations/internal/personalagent/goal_plan.go:408` 自动核验有限 | 定义 `WorkerResult/Proof`：摘要、结构化数据、引用/截图/文件、验证方法、失败/不确定项、需要人决策；事实与 Agent 自述分离；无证据就不自证成功，AC-04/08 |
| W07 P0 P | U:`templates/assistant.md:60-82` blocked/stalled/needs-you/ready 区分 | S:`plugins/solco-automations/internal/personalagent/task.go:452`、`plugins/solco-automations/internal/personalagent/dispatch.go:281-318,350`、`plugins/solco-automations/internal/personalagent/coordinator.go:238` | 状态机应具备 queued/start/working/blocked/waiting_owner/awaiting_proof/ready/done/failed/uncertain/cancelled，旧 enum 映射可兼容，前后端一致且真实信任弹窗可恢复，AC-07 |
| W08 P1 P | U:`mods/tasks/bin/herdr_task/queues.py:163`、`mods/tasks/bin/herdr_task/settings.py:441` 任务/机器限制 | S:`plugins/solco-automations/internal/personalagent/dispatch.go:115,437` maxParallel=2；`plugins/solco-automations/internal/personalagent/supervisor.go:385` fanout/budget | 验证 Assistant/Host/全局并发和资源预算，重启不超过配额；优先级、取消、依赖链和非 Git 工作目录需公平调度，AC-05/07 |
| W09 P1 P | U:`mods/tasks/bin/herdr_task/settings.py:328,390` 任务级 Agent/Review/Merge 设置 | S:`persona.go`、`mcp.go`、`review.go` 已有配置但受限 | OMP-first 显式化 Model、thinking、tool grants、skill grants、超时、可更改范围；继承 Assistant Policy 但任务权限只可更窄，AC-08/09 |
| W10 P1 P | U:`mods/tasks/bin/herdr_task/commands.py:722` 启动/再启动识别原 task 状态 | S:`plugins/solco-automations/internal/personalagent/dispatch.go:350-510`、`plugins/solco-automations/internal/personalagent/coordinator.go:106-210` | Herdr launch 不确定要查询 existing Pane/Agent、receipt，单次 submit 不重发；故障恢复演练“旧 Agent 仍在/丢帧/重启”不得双 Worker，AC-07 |
| W11 P1 E | U:`mods/tasks/bin/herdr_task/launch.py:277` Git worktree，非 Git folder 原位 | S:`worktree.go`、`plugins/solco-automations/internal/personalagent/launcher.go:43-77` | 编码隔离分支保持、非代码用 Assistant 私有工作目录；按产品任务 scope 执行、不污染 repo 的工作树；不能把个人浏览器 Routine 强行当 Git 工程，AC-02/08 |
| W12 P2 P | U:`mods/tasks/bin/herdr_task/takeover.py:143-238` Provider 崩溃后的 backup/hand-off | S:`plugins/solco-automations/internal/personalagent/dispatch.go:350-510` uncertain/reconcile；Agent 只允许 OMP | 先保证 OMP 本身恢复，不偷偷 fallback 到别的 provider；以后才在明确授权和同一 taskId 下增加换代理/续跑，AC-07 |

### C. Automations / Routines (R01-R10)

| ID/优先级 | 上游源码证据 | Solco 对照 / 现状 | 明确差距及关闭条件 |
| --- | --- | --- | --- |
| R01 P0 B | U:`templates/assistant.md:105-118`、`mods/tasks/bin/herdr_task/automations.py:236` 用户意图成产品 Auto，不是 OS Job | S:`plugins/solco-automations/internal/personalagent/coordinator.go:329-360`；`plugins/solco-automations/internal/personalagent/api_routine.go:112,239`；上一轮 extension 工作树未实机 | 自然语言“每天/每周/定时/周期”**强制路由至 Solco Routine draft**，系统 launchd/cron/外部 task/匿名后台 loop 数量增量为 0，AC-02 |
| R02 P0 P | U:`mods/tasks/bin/herdr_task/automations.py:71-123` 支持文字时间：once、daily、weekdays、每 N 分钟、区间 | S:`plugins/solco-automations/internal/personalagent/api_routine.go:239` 用 5 字段 cron；`plugins/solco-automations/internal/config/config.go:27-34,220`；`plugins/solco-automations/internal/schedule/schedule.go:74` | Go 内建立单一“文字时间表达式→时区→机器可验规则→下次 3 次预览”解析器；不确定时让 Assistant 问；涵盖 once/DST/sleep/wake，AC-02/10 |
| R03 P0 M | U:`automations.py:165,330-397` 事件触发 added/ready/failed/needs-you/merged/stalled，过滤/去重/防自触发 | S:`plugins/solco-automations/internal/personalagent/rules.go:360` 只解析 cron；`plugins/solco-automations/internal/personalagent/heartbeat.go:254` 事件只供 heartbeat，不能等同 Routine 事件 | Routine 需支持按事件运行/联合时间条件、去重、过滤、循环抑制和 Owner；单个事件不重复执行，AC-11 |
| R04 P0 P | U:`automations.py:298,378-405` 每次创建新的托管 Task | S:`plugins/solco-automations/internal/personalagent/rules.go:360-444`、`plugins/solco-automations/internal/personalagent/routine.go:236`、`routine_intent.go` | Scheduler 是唯一启动权限；occurrenceKey 唯一、跨进程锁、sleep catch-up/错过、overlap skip/queue、水位不重置；关闭页面仍运行，AC-02/07/10 |
| R05 P0 P | U:`templates/assistant.md:105-118` 用户明确说要开始才加自动化 | S:`plugins/solco-automations/internal/personalagent/api_routine.go:229-340`、`routine_approval_audit_test.go` 草案+绑定 defHash 审批 | Solco 的**审批机制比上游更严格，应保留**；草稿/批准/被改失效/撤销/暂停/恢复必须 GUI 和 Android 同源收据，AC-02/10 |
| R06 P0 B | U:`templates/assistant.md:105-118` 可无 Git repo 的普通事务 | S:`plugins/solco-automations/internal/personalagent/api_routine.go:239-305` 旧代码强制 repo；工作树已新增私有目录默认，未部署 | Repo-less 个人情报 Routine 必须从 Chat 创建成功、使用 OMP headless；不能因为路径校验失败而改用 OS scheduler，AC-02 |
| R07 P0 P | U:`mods/tasks/bin/herdr_task/automations.py:205,236,298` Agent 跟随记录，task settings 继承 | S:`plugins/solco-automations/internal/personalagent/api_routine.go:289-305` 原默认 Claude，当前未提交改为 OMP；`plugins/solco-automations/internal/personalagent/heartbeat.go:390` 仍显式 Claude | **不止 Task 和 Routine，Heartbeat/自发 Worker 的 Agent 都必须锁 OMP**；全路径查找隐形 Provider 漂移，AC-09 |
| R08 P1 P | U:`mods/tasks/bin/herdr_task/automations.py:190-203,284` 列出规则、下一次/来源/状态 | S:`plugins/solco-automations/internal/personalagent/api_routine.go:45-111`、`frontend/src/assistants/AssistantsPage.tsx` | 显示触发来源、下次运行时区、上次 Run 结果链接、Owner、停用原因、技能和预算；支持预览/编辑及变更重新审批，AC-02/10 |
| R09 P1 P | U:`mods/tasks/bin/herdr_task/automations.py:420,491` scheduler 独立运行、一次锁 | S:`plugins/solco-automations/main_assistant.go:66-137` ensureDaemon、`plugins/solco-automations/internal/daemon/personal.go:172-255` | 独立于 UI/原 Coordinator 生死，重启/并发请求只能一份调度器；长时设备启动/睡眠/窗口关闭/用户登出应定义真实保证和已知限制，AC-07/10 |
| R10 P1 P | U:`mods/tasks/bin/herdr_task/automations.py:298` 自动生成托管任务后新闻回 Assistant | S:`plugins/solco-automations/internal/personalagent/rules.go:419-445`、`plugins/solco-automations/internal/personalagent/supervisor.go:127`、`plugins/solco-automations/internal/personalagent/coordinator_report.go:179` | 任意一次 Routine 都带 owner/request/trigger/occurrence/worker/result/report 链；日报中的链接不是“Agent 说了就算”，AC-02/04 |

### D. 个人主动性 / Autopilot / 记忆 (A01-A10)

| ID/优先级 | 上游源码证据 | Solco 对照 / 现状 | 明确差距及关闭条件 |
| --- | --- | --- | --- |
| A01 P0 B | U:`templates/assistant.md:120-131`、`mods/tasks/bin/herdr_task/autopilot.py:465-485,569` 事件/日程醒来做有限动作 | S:`plugins/solco-automations/internal/personalagent/heartbeat.go:254-390` 创建 read-only heartbeat Task；`plugins/solco-automations/internal/personalagent/proactivity.go:87-175` | **当前“模型心跳”≠原 Coordinator 主动承担目标**；要唤醒原 Assistant（或可追溯的受控等效回合）执行 inspect→choose→act→evaluate→save→exit，绝不常驻自循环，AC-05 |
| A02 P0 P | U:`autopilot.py:449-465,491-540` pending/inflight/last_wake/wakes/超时状态 | S:`plugins/solco-automations/internal/personalagent/heartbeat.go:254-395`、`plugins/solco-automations/internal/personalagent/coordinator_report.go:179`、`report_reconcile.go` | 唤醒事件去重、pending 合并、忙时排队、未投递过期、出站状态和最终回复收据关联；中断不得无限重放，AC-05/07 |
| A03 P0 M | U:`mods/tasks/bin/herdr_task/autopilot.py:700,717` `progress/complete` 带下一次唤醒的 durable progress、停止证据 | S:`goal_plan.go` 的 Goal progress，非 Assistant-wide Autopilot 自身进度 | 独立的 Assistant Autopilot checkpoint：last observed event seq、进展、下一动作、阻塞、评估、终止条件；完成时必须证明事实，AC-05 |
| A04 P0 P | U:`mods/tasks/bin/herdr_task/autopilot.py:347-417` Space 继承 Everywhere，按 Space 开关 | S:`plugins/solco-automations/internal/personalagent/proactivity.go:87-150` 三等级和 quiet/budget；`frontend/src/assistants/Behavior.tsx` | 个人版继承模型：global defaults → Assistant override → Goal/Routine explicit grants；版本化、权限递减、真实 UI 更改即时生效，AC-05/09 |
| A05 P0 P | U:`mods/tasks/bin/herdr_task/autopilot.py:54-77` 控制唤醒超时/最多创建数量/安静反馈 | S:`heartbeat.go:27-59,135-160` 每日 WakeBudget；`plugins/solco-automations/internal/personalagent/supervisor.go:385` 有 budget | Budget 不能只计 Task：要覆盖唤醒、模型 token/cost、工具频率、并发、安静时段、没有授权时不自发外部动作；限额耗尽可观察而非偷跑，AC-05/09 |
| A06 P1 P | U:`mods/tasks/bin/herdr_task/autopilot.py:547-591` 状态事件唤醒，而非轮询模型找活儿 | S:`plugins/solco-automations/internal/personalagent/watch.go:268` GitHub PR 监控；`plugins/solco-automations/internal/personalagent/inboxwatch.go:13`、`activity.go` | 多 Goal、多 Routine、inbox、worker stuck、时间与事件汇成统一事件路由/水位；只在变化或合理到期时唤醒，保证不刷屏，AC-05/11 |
| A07 P1 P | U:`templates/assistant.md:60-82,120-131` News 后自行决策，必要时发卡 | S:`plugins/solco-automations/internal/personalagent/supervisor.go:257-385` 确定性推进、`plugins/solco-automations/internal/personalagent/coordinator_report.go:179` 自然语言汇报 | 主动跟进不仅“发一张任务事实卡”：按已批准目标选择下一动作、补充研究、判断阻塞、发起审批/请求用户，不能无上限 replan，AC-05 |
| A08 P1 P | U:`mods/tasks/bin/herdr_task/autopilot.py:465-484` 心跳带上次 progress | S:`context.go` 和 `memory_files.go` 提供 approved memory；`persona.go` 版本化 | 长期记忆/commitment 与短期 checkpoint 分层；只写用户授权事实，去重/纠错/来源引用/敏感数据隔离，AC-05/09 |
| A09 P1 M | U:`mods/tasks/bin/herdr_task/autopilot.py:521-540` 投递的 turn 有明确完成/失败 | S:`plugins/solco-automations/internal/personalagent/report_reply.go:45-148` 只能为某批报告在 OMP transcript 证明最终回复 | 计划执行唤醒、结果汇报与聊天消息必须分别关联真实回合；真实回复没找到→unknown，不得冒称已反馈，AC-06 |
| A10 P2 E | U:`mods/tasks/bin/herdr_task/autopilot.py:347-417` 每 Space 配置 | S:`assistant.go` 以个人身份/多个 scope；`group.go` 跨 Assistant | 不照搬一 Space 一 Agent 的 UI；允许跨空间 Personal Assistant 目标，但必须限制跨机器、跨身份和跨技能默认访问，AC-09 |

### E. Review / 可信完成 / 人工决策 (Q01-Q07)

| ID/优先级 | 上游源码证据 | Solco 对照 / 现状 | 明确差距及关闭条件 |
| --- | --- | --- | --- |
| Q01 P0 B | U:`mods/tasks/bin/herdr_task/proof.py:33,88` 证据链接、核验产物 | S:`plugins/solco-automations/internal/personalagent/goal_plan.go:408`、`plugins/solco-automations/internal/personalagent/supervisor.go:334,353` | 不允许把 Agent “已完成”文本、无结果的 RunDone 或原聊天闲置作为成功标准；根据 Task 类型定义可验谓词，AC-04/08 |
| Q02 P0 P | U:`templates/assistant.md:60-103` Worker Ready≠Owner Done | S:`plugins/solco-automations/internal/personalagent/review.go:348,482,519`、`plugins/solco-automations/internal/personalagent/task.go:452` coding review/人工合并 | 编码 Worker 必须留分支/差异/测试/Proof，Owner 选择 Accept/Reject/Resume；审核状态不和普通 research 结果共用无证据的 Done，AC-08 |
| Q03 P1 P | U:`mods/tasks/bin/herdr_task/settings.py:328,390` 每任务 Review、merge、证明需求可配置 | S:`plugins/solco-automations/internal/personalagent/review.go:124,348` + `persona.go` | 任务继承审批/检查政策；仅明确授权才自动执行可逆操作，外部发布/删除/推送/合并默认需要人，AC-08/09 |
| Q04 P1 M | U:`mods/artifacts/`、`service/artifacts.ts:31`、`mods/accept-card/` | S:`frontend/src/assistants/Progress.tsx:86,141` 事实/计划，尚无通用 Assistant Proof 卡 | 研究报告、浏览器快照、日志摘录、Git diff、验证步骤统一 Artifact/SourceRef，前后端能打开并检查；不可只展示一句“完成”，AC-04/08 |
| Q05 P1 P | U:`mods/tasks/bin/herdr_task/news.py:52,86,99` stalled/failure/needs-you 一致提示 | S:`inbox.go`、`approval.go`、`plugins/solco-automations/internal/personalagent/inboxwatch.go:13`；`internal/remotehost/personal_agent.go:329` | 统一 Needs you 卡：trust/permission、Skill 未安装、token 失效、review fail、网络失败；卡动作回到原 Task/Run，点击可操作，AC-07/08 |
| Q06 P1 P | U:`templates/assistant.md:60-103` 两行报告、避免重述已有卡 | S:`plugins/solco-automations/internal/personalagent/coordinator_report.go:179`、`supervisor_report.go`、`frontend/src/assistants/Progress.tsx:86` | “事实更新”与“Omp 生成并证实发送的语言回复”区分，附 source、不要同一事实推送多个内容，AC-06 |
| Q07 P2 E | U:`templates/assistant.md:84-103` PR/merge 默认开发向 | S:`review.go`、`goal.go`、`routine.go` | Personal Agent 生活研究任务用交付/真实性/时效性验收，不能强制 review/merge Git；编码任务保留成熟 PR 审核流，AC-02/08 |

### F. App UI / 设备 / 宿主能力 / 后台 (U01-U11)

| ID/优先级 | 上游源码证据 | Solco 对照 / 现状 | 明确差距及关闭条件 |
| --- | --- | --- | --- |
| U01 P0 P | U:`mods/tasks/ui.js` + `mods/tasks/live.js` 状态/任务抽屉 | S:`frontend/src/assistants/AssistantsPage.tsx`、`frontend/src/assistants/Progress.tsx:63,86,141` | 在 Assistant 原 Chat 就能看到 Goal、Task、Routine、当前进度、阻塞、下次运行、证据；每条有真实 ID 深链，AC-01/03 |
| U02 P0 P | U:`mods/accept-card/` + `mods/pane-chat/autopilot.js` 操作与设置 | S:`frontend/src/assistants/AssistantsPage.tsx`、`internal/nativeui/assistants_page.go:17-54` | 草案审批卡、主动性开关、计划审批、再次执行/暂停/恢复，错误必须就地可见、不重置输入；真机截图与 AX 对照，AC-02/08 |
| U03 P0 P | U:`web/plugins/coordinators/client.tsx` 与本地同一数据 | S:`internal/remotehost/personal_agent.go:20-23`；`../solco-mobile/src/app/assistants/[id].tsx` | Mac/Android 都读相同 Assistant store，不是手机自己起 Agent；同一审批和结果应一致，手机断线重连按游标恢复，AC-06/12 |
| U04 P0 V | U:`mods/tasks/bin/herdr_task/news.py:227,395,406` 保留消息/阻塞与通知 | S:`activity.go`、`internal/remotehost/personal_agent_activity.go`、`internal/remotehost/assistant_push.go:312` | 已有离线推送代码**不等于设备级送达**；需注册 Android 真机 Token、OS 通知权限、Expo ticket/receipt、App 读取 ACK/原状态一致；AC-06/12 |
| U05 P1 P | U:`mods/task-browser/`、`mods/task-files/`、`mods/task-changes/`、`mods/task-pr/` | S:`internal/nativeui/assistants_page.go`，原 Solco Files/Changes/Terminal 可复用 | 任务卡一键打开对应 Herdr Pane/Browser/Files/Diff/Artifact/PR，回到原聊天保持状态，不再建第二套右侧栏，AC-04/08 |
| U06 P1 P | U:`mods/pane-chat/`、`mods/chat/`（任务与 Assistant 对话） | S:`frontend/src/App.tsx` 共享 Composer；`AssistantsPage.tsx` 专属 UI | 输入框、发送/停止、附件、状态、技能提示、模型切换遵循共享 ChatView 交互，尤其启动第一条消息不得只停在草稿，AC-01 |
| U07 P1 P | U:`mods/shepherdr-settings/autopilot-section.js` 配置出处与继承 | S:`frontend/src/assistants/Behavior.tsx:32-114`、`../solco-mobile/src/app/assistants/behavior/[id].tsx` | 用户看得懂“何时醒/能做什么/为什么没跑/下次报告”，并可在手机修改/暂停，显示操作回执和版本冲突，AC-05/10 |
| U08 P1 E | U:`mods/chat-call/`、`mods/chat-react/`、`mods/chat-image/`，语音、表情、图片可拆 addon | S:Solco 自有 Chat/语音/附件路径 | 可选而非阻断：不造第二个 Chat 生态，现有能力复用；无实现的单项标计划外，不假称 Shepherdr 全 parity，AC-12 |
| U09 P1 P | U:`mods/coordinator/coordinator.js:281,457` 与主机/Workspace 的真实身份 | S:`internal/nativeui/assistants_state.go:68-112`、`internal/personalagent/client.go:296-328` | 运行中/堵塞/未启动、真实 Pane、所属 Agent、能力版本与 skill 缺失 UI 区分；不能给“已运行”假状态，AC-01/07 |
| U10 P2 E | U:`mods/tasks/bin/herdr_task/machines.py`、`home.py`、`templates/assistant.md:137-158` | S:`internal/remotehost/machines.go` + Assistant 单机执行 | Multi-Machine 先定义可见性/离线/Owner/Host 选择策略，保留 Herdr 原生多机机制；先不跨主机写任务，直到 API 与授权真实可测，AC-09 |
| U11 P2 P | U:`service/secrets.ts:40`、`mods/chat/secrets.js` 按名字用 Secret、不回显 | S:`internal/secretstore/`、`plugins/solco-automations/internal/personalagent/mcp.go:1-24`，无 Personal Assistant 同等级 runtime broker | 账号令牌与浏览器登录态由授予的本机能力使用，Prompt/CLI/Push/Fact 不存 secret；任务需授权时发批准卡，AC-09 |

### G. 可靠性 / 测试 / 上游延伸 (L01-L08)

| ID/优先级 | 上游源码证据 | Solco 对照 / 现状 | 明确差距及关闭条件 |
| --- | --- | --- | --- |
| L01 P0 B | U:`mods/tasks/bin/herdr_task/automations.py:407-491` scheduler 锁与 cycle | S:`plugins/solco-automations/main_assistant.go:88-137` standby、自举互斥；`routine_intent.go` 崩溃恢复 | 必须验证多进程/重复请求/杀进程断点：一份 Go daemon、一个 occurrence→一个 Task，无双派发，AC-07/10 |
| L02 P0 P | U:`mods/tasks/bin/herdr_task/news.py:52-99`、`mods/tasks/bin/herdr_task/tick.py:85` 真实状态合并 | S:`plugins/solco-automations/internal/personalagent/dispatch.go:350-510`、`plugins/solco-automations/internal/personalagent/coordinator_report.go:179` | Herdr 临时失联/Agent 中断/信任目录都应有可诊断状态、重连和人工修复指引，无法确认时绝不自动重发，AC-07 |
| L03 P0 P | U:`mods/tasks/bin/herdr_task/backups.py:87`、`mods/tasks/bin/herdr_task/store.py:95` 持久任务 + 备份 | S:`store.go` SQLite migrations/receipts，`memory_files.go` | 定义 DB/automations.yaml/私密记忆的一致性备份恢复与迁移/回滚；版本升级不得清空任务/审批/收据，AC-13 |
| L04 P0 B | U:`templates/assistant.md:105-131` 任务和 Autopilot 生命周期独立于 UI | S:`plugins/solco-automations/internal/daemon/personal.go:172-255`、`internal/hostd/serve.go`、`plugins/solco-automations/main_assistant.go:88-137` | 关闭 Solco 窗口后调度仍运行的承诺必须由真实后台服务支撑；登出/机器睡眠期间不得声称会执行；醒来按 catch-up 策略追踪，AC-10 |
| L05 P1 P | U:`core/autopilot.cases.json` + `test/test_herdr_task.py` 上下游共享案例 | S:`plugins/solco-automations/internal/personalagent/*_test.go`，历史 `docs/shepherdr-2.1-test-parity.md` | 建“3.1.3 逐项契约用例”而不是套用 2.1 goldens；每个 Gap 必须有 red-before / green-after / 真实运行记录，AC 全集 |
| L06 P1 V | U:`mods/tasks/bin/herdr_task/cli.py` 命令直接的错误语义 | S:`main_assistant.go`、`internal/personalagent/client.go:71`、`internal/remotehost/personal_agent.go:40` | CLI/Host/手机的 timeout、断连、写入收据、不可重试错误类别统一；防无限后台 goroutine/未完成 HTTP 持有，AC-07/12 |
| L07 P1 P | U:`mods/tasks/bin/herdr_task/browsers.py:89` 清理任务浏览器会话；`archive.py` 清理完成 task | S:`worktree.go`、`assistant_lifecycle.go`、`internal/hostd/` | 任务停止/结束后按 retention 规则清理临时浏览器/文件/Herdr Pane；默认留可检查结果，不能删未提交工作，AC-08/13 |
| L08 P2 E | U:`mods/` 全 addon / `docs/parity/` 独立可开关 | S:MyGo Native UI + WebView 混合，Assistant 为自有产品 | 不追求逐像素复刻 Shepherdr；对 UX/功能对齐，独立审核移动可达性、深浅主题、无障碍、慢网、中文任务名和高频通知，AC-12 |

**高优先级再审计提醒**（补丁未安装不算 PASS）：
- `plugins/solco-automations/internal/personalagent/heartbeat.go:390` 仍将主动唤醒 Worker Agent 写死成 `claude`：这是 OMP-only 目标的**确证反例**；必须在触发路径而非 UI 文案修复并做静态扫描。
- `plugins/solco-automations/internal/personalagent/dispatch.go:292` 的成功路径使用空字符串结束 Run，不能证明已经读取研究结果；这种状态下对用户发送高赞 X.com 摘要将只能猜测。
- Coordinator 的新 OMP extension 只能阻断它收到的若干工具调用，**不是 OS 沙盒**，也尚未证明从 Herdr 启动时确实加载；Provider/其他 extension/同一用户 shell 和裸 CLI 绕路风险必须由 Broker / 受控权限设计明确解决。
- 旧 `shepherdr-2.1-*` fixtures 的历史通过不代表最新 3.1.3 的 Autopilot/Task/Auto parity，更不代表 Android 和真实 OMP 真机通过。
- 上游的 `browsers.py` 是它自己的浏览器，**不是** `ego-browser`；本次必须验证 Solco Worker **实际发现与使用指定 Skill**，而不是把“Prompt 含 skill 名称”算成功。

## 3. 目标实现方案与分阶段施工（依赖顺序，不许跳过）

**Phase 0 — 固定基线与红测（不写生产功能）**：确认上游 commit + 版本、跑现有 Root/Nested Go、frontend、mobile 契约测试；逐项把本表状态固化到追踪表（ID、owner、现状、红测、期望、验收记录链接）。对 C01/C02/W02/R01/A01 建至少五个可复现失败用例。用临时 HOME+独立 Herdr socket+plugin state/config，先记录“错误行为”。不要污染用户 launchd/cron/X.com 登录态。**交付**：基线记录、风险与红测。门禁：能复现并解释而非单凭代码猜测。

**Phase 1 — 受控 Coordinator + 受控能力发现**（C01-04/C08、W04、R01/R07、U09）：让 OMP 在真实 Herdr Pane 加载 Agent 专属 extension/broker；工具可发现，有作用域、动作 allowlist 和 owner 身份，不靠绕行 shell；能力 doctor 明示 Herdr、OMP、ego-browser、MCP、调度后台可用性；统一 OMP-only，清理所有隐形 Claude defaults（尤其 Heartbeat）。裸 CLI 需切分 Owner/Agent 信任边界（令牌/本机隔离/签发 scope 选择之一，威胁模型写明；不可把 Coordinator 的 AssistantId 文本当权限）。**门禁**：AC-01/09，负向阻断和诊断均真机 PASS。

**Phase 2 — Herdr Worker 生命周期与证据回流**（W01-03/W05-11/Q01-05）：实现统一 WorkerRequest/WorkerResult/Artifact 及 Herdr API seam：启动→状态/对话/输出读取→完成事件→证据校验→持久化→Supervisor；优先探查 Herdr 原生结构化输出/agent session，再在严格来源证明下使用 Provider transcript，最后有限屏幕输出只作诊断，**不拿 500 行 PaneRead 冒充完整历史**。Skill 由 Worker 运行环境验证：Herdr Skill 的 HERDR_ENV 门禁、ego-browser 发现、授权和来源链接；Worker 没有 skill 则 blocked + Needs you。**门禁**：AC-04/07/08，真实 OMP Worker 能读出可证明的结果，断线不编造完成。

**Phase 3 — Routine 统一调度**（R01-10/L01/L04）：一个 Assistant Routine 可从 Chat 草拟；自然语言时间表达式支持明确时区、nextRun 预览；Cron/once/事件触发共享一次性 occurrenceKey；审批前绝不执行，批准后每次 fire 恰创建一个属主 Worker；非仓库用 root 工作区；配置修改需重新审批；复用已有 YAML/Go Scheduler、不可另开 OS 定时服务；迁移既有规则只读审计、可回滚。**门禁**：AC-02/10/11（含睡眠/DST/并发/复活）。

**Phase 4 — 事件驱动 Personal Autopilot**（A01-10/C06-07）：逐步实现每 Assistant 的持久 progress、checkpoint、下一步、停止条件；Inbox/Task/Goal/Routine/Heartbeat 事件触发**单次有限** Coordinator wake；它根据活动图和用户长程意图选择 ≤N 条后续动作，再保存 checkpoint 并退出，不等待新任务无限自旋。将用户指定的提醒/汇报节奏、quiet hours、model cost、budget/permission 限流作为 Go 判定，不靠提示词。与 Routine 并列但**不重复**触发。**门禁**：AC-05/06/09，两个 Goal+一个 Routine 同时推进且没有多余 worker。

**Phase 5 — 审核/Chat/手机可用性**（Q01-07/U01-11）：Tasks/Routines/Goal/Commitment/Proof 统一在原 Assistant Chat/侧栏/卡片/详情中展示；同一 WebView Composer，真实状态、深链、可批注产物、暂停/重试/批准；Mac 与 Android 共用 Host 数据及 idempotency receipts；Activity → ReportPump → 原 OMP 最终回复 → Push/ACK 保持三种事实分开；断网补读、冷启动第一条消息、状态变更后台通知闭环。**门禁**：AC-01/02/03/06/08/12。

**Phase 6 — 正式可靠性/回滚/发布**（L01-08）：再审计 race、守护进程、数据库/规则一致性、Secret/Skill ACL、冷启动、留存、Crash 与恢复；3.1.3 差距表复审，所有 P0/P1 逐项关联变更、测试、真机日志。若出现本期无法实现的 P2，书面说明为什么与目标不同、用户可见限制和后续阶段。**门禁**：AC-07/10/12/13 全 PASS；否则不得写“已经达到 Shepherdr 3.1.3 可用/好用级闭环”。

每 Phase 都必须：**独立可检查的变更 → RED 测试 → 最小实现 → GREEN 测试 → 工作树审计 → 真机验收 → 更新矩阵 → 小批 commit（用户在后续执行时明确允许）**。并行 Agent 按文件/责任边界开发；共享状态机、schema、Host API 变更由一个 Owner 串行合并，不得互相覆盖未提交代码。

## 4. 最终真机验收：先模拟、再受控真实外部服务

### 4.1 测试环境/证据契约

1. **不要在用户活环境验收**：独立临时 `HOME`、Herdr socket/session、`HERDR_PLUGIN_STATE_DIR`、`HERDR_PLUGIN_CONFIG_DIR`、`SOLCO_ASSISTANTS_DB`、`SOLCO_ASSISTANTS_WORKDIR`、`SOLCO_ASSISTANTS_MEMORY`、临时 Git repo。生产测试账号/真实 X.com 若用到，必须是用户明确授权且可控的测试配置；默认仅本地假页面/fixture。
2. **主机/设备**：macOS 上真实 Solco.app (MyGo) + 真实 Herdr daemon + 真实 OMP 进程，必要时 ADB 安装到已授权的 Android 物理机；安卓模拟器不能替代最终真机，iOS 无可用设备则明确 NOT TESTED。
3. **观测四份真值**：① SQLite/task/approval/outbox/receipt/occurrence 及 revision；② Herdr workspace/pane/agent/session/状态；③ OMP tool call/skill 发现+结果/引用/确认；④ Solco Desktop Chat 和 Android UI/Push/已读。只截 UI 不够，单测 PASS 也不够。
4. **外部 OS 调度器负向检查**：采集隔离用户的 launchd/crontab/at/其他托管调度清单前后差异；所有 Solco Routine 仅产生一条 YAML/SQLite 受管记录。**只采集**，不删除用户已有真实系统任务。
5. **报告证据路径建议**：`docs/debug/assistant-shepherdr-3.1.3-acceptance/` 下每 case 一个 markdown，记环境/版本/命令/预期/观察/截图/运行时收据/结果/失败原因/修复 commit；敏感 JSONL 不入 Git，可存隔离受限目录，仅存哈希/脱敏摘要和位置指针。测试流程不能访问和粘贴个人账号凭据。

### 4.2 可直接复现的验收案例（每项必须有设备证据）

| AC | 真实用户输入或操作 | 验收期望（硬断言，不接受模型口头描述） | 对应 Gap |
| --- | --- | --- | --- |
| **AC-01 首次 Chat + 能力** | 新建 Ada，第一次发送“帮我检查一下你能用哪些 Solco 能力，再为这件事规划任务” | 仅一个 OMP Coordinator / Pane；真实 extension 已加载；可列出 Product Actions 与 skill 缺失项；同一输入不重复 launch；首条消息不丢，UI 一次发送后有收据 | C01-04/U01/U06 |
| **AC-02 X.com 定时摘要** | “每个工作日早上 9:00，用 ego-browser 查看 X.com，整理过去 24 小时高赞/推荐内容，保留链接、指标、时间和 5 条要点，然后推给我” | 指定本机时区；创建**一个 Solco Routine draft**，返回 id/审批卡/下次触发；系统 Job 0；未批准 0 Worker；批准后一次 occurrence 一个 OMP Worker，明确用 ego-browser，结果可回读并展示链接与指标；只有批准后的后续触发，原会话汇报；若未登录 X.com 清晰 blocked | R01/02/04/05/06/W02/04/05 |
| **AC-03 两项工作并发** | “先研究 A 和 B 两个方案，第三项只在前两项出来后对比结果，最后做建议” | 一个可追溯 Goal/Plan 或三个有依赖任务；A/B 并行不超过预算；C 在 A/B 具备证据前不启动；三个 taskId/parentRequestId 不变；进度可读、需审批动作可见 | C05/06/08/W08/U01 |
| **AC-04 Worker 真实结果** | 创建受控检索 Worker，让其用测试页面读取三条固定文章和一个不可用链接 | Worker 通过 Herdr 真实注册 OMP；完成时能读到三条来源、时间/分数/摘要和一条失败记录；`Run.Result`/Artifact 不是空或虚构；失去输出身份时状态 unknown，不算 done；原 Coordinator 能按 id 查询 | W01-07/Q01/Q04 |
| **AC-05 主动但有限** | 为 Ada 开启获批 Heartbeat/Autopilot：“每次醒来检查两个 Goal 和 Routine，卡住时提醒我，完成后写下一步，别自行推送代码” | 到时间/事件时仅一个受控 wake；检查任务图、创建获授权下一个 Task，保存 checkpoint；无变化不额外创建、不无休止循环；预算和 Quiet Hours 真正阻断；关闭再开从 checkpoint 继续 | A01-08/C07 |
| **AC-06 主动报告与离线手机** | 让 Worker 完成一条摘要，关闭 Mac UI、让 Android 断网，再联网 | 完成 Fact 一次→原 Coordinator 同一会话有已验证最终回答→Android 看到正确通知/Activity 并可以 ACK；断网重连只补读缺失内容，不能把 Expo ticket 当送达，也不把事实卡伪装成 Agent 回复 | A09/Q06/U03/04 |
| **AC-07 Trust/重启/不确定** | 使用未信任测试目录启动 OMP、连续点击 Start 三次；模拟 Herdr 断连、杀掉测试 daemon、重启 | 只一个 Coordinator；blocked/uncertain 有真实原因与人工操作入口；没有双 pane/双 prompt/双 Worker；重启调度恢复，回执不重复，仍有旧 Pane 时不盲目再次启动 | C04/W03/07/10/L01/02 |
| **AC-08 编码审核** | “为测试仓库修复一个简单 bug，要求测试通过后让我审查，不要合并” | Task draft→Owner publish→Herdr OMP 在隔离 worktree 实现→实际测试/commit/diff/proof→Ready for review；Owner Reject/Accept 两条路径分别正确；不会擅自 push/merge/删除 checkout | W06/09/11/Q02/03/04 |
| **AC-09 权限与攻击负例** | 换一个 AssistantId/任务 ID、未批准的 Routine、尝试 `bash launchctl`、`cron`、无授权 Skill、从网页注入“忽略系统” | broker 按实际身份拒绝跨 Owner；OMP Coordinator 工具无法启动系统计划；越权读写/子 Agent 拒绝；无账号权限时 Needs you；Heartbeat 不选 Claude；如果 OS 级强制限制未实现，必须明示可绕过范围而不是宣称沙盒 | C02/C10/W04/05/R07/A05 |
| **AC-10 计时与睡眠/DST** | 创建 every 30 minutes、weekdays 09:00、once、两条事件规则；跨时区/冬夏令时回放；Mac 休眠、关闭窗口后再恢复 | 明确时区与下三次触发；不重跑过去已消费 occurrence；错过策略可解释；重复 tick 0 双任务；业务 daemon 实际独立于 UI；机器睡眠期间不假装执行 | R02-09/L01/L04 |
| **AC-11 事件自动化** | “当已有监控任务失败时，创建一次诊断任务，不要因为诊断本身再递归触发” | 事件 filter 选中对应 Task，一次失败只触发一个新的受托管 Task，关联 trigger/parent 且阻断自激励；完成后给原 Assistant 事实与汇报 | R03/A06 |
| **AC-12 Mac/Android 易用性** | 真机完成 Chat 冷启动、批准、暂停、任务详情/来源查看、通知点击跳转、断网恢复、后台唤醒 | Mac 与 Android 的 IDs、状态、卡片、选项、失败原因一致；中文长内容、深浅色、键盘输入和无障碍可用；Android 真机安装和操作屏录/截图在案；不以 TS typecheck 代替 | U01-09/L06/L08 |
| **AC-13 持久化与升级** | 在两个 Goal、一个运行 Routine、一个待审批卡存在时备份/升级/恢复测试环境 | 不丢 Task、Run、memory、审批、事件水位、occurrence；收到旧 callback 幂等、签名正确；不把不确定状态假装 done；用户已有真实数据完全未受测试影响 | L03/L07 |

### 4.3 测试执行门禁、失败处理、最终退出条件

```bash
# 建议命令：以仓库实际 toolchain 配置为准；每一项记录 exit code、测试总数和日志
cd plugins/solco-automations && GOTOOLCHAIN=auto go test ./...
cd plugins/solco-automations && GOTOOLCHAIN=auto go test -race ./internal/personalagent ./internal/config ./internal/daemon
cd plugins/solco-automations && bun test extensions/solco-coordinator.test.ts
cd <solco-root> && GOTOOLCHAIN=auto go test ./...
cd <solco-root> && GOTOOLCHAIN=auto go tool mygo vet .
cd <solco-root> && GOTOOLCHAIN=auto go tool mygo build
cd <solco-root>/frontend && bun run typecheck && bun run build && bun run build:mobile
cd <solco-mobile> && pnpm typecheck && pnpm lint && pnpm test && pnpm check:fixtures && pnpm check:chat-page
cd <solco-root> && git diff --check
```

以上命令全绿只是**进入实机测试的前提**。最终真机测试必须采集真正的 Coordinator→ToolCall→Routine/Task receipt→Herdr OMP Worker→Result/Proof→Supervisor→原 Chat→Android Push/ACK。若某一案例因外部账号、设备许可或 Herdr API 限制未执行，标 **BLOCKED / NOT VERIFIED**，绝不可标 PASS。

**可用版本的退出条件**：所有 C/W/R/A/Q/U/L 中的 P0/P1 项都有“设计选择、变更 commit、红绿测试、适用的 AC 设备证据与已知限制”；AC-01～AC-13 全部 PASS（外部 X.com 内容可先用真实浏览器访问受控测试站完成技术测试；但若没有用户明确授权的真实 X.com 账号/浏览器会话验证，AC-02 的“真实站点”子项必须标 BLOCKED，不得报整项 PASS），没有原会话掉消息、无归属后台任务、错误完成、重复调度、重复 Coordinator、权限越权或未证实推送。产品声明只能对应已验证范围。

## 5. 建议里程碑与 Agent 执行卡

| 顺序 | Milestone | 可单独交付/验证的功能 | 前置与阻断 |
| --- | --- | --- | --- |
| M0 | 基线/Shepherdr 3.1.3 契约移植 | 本文件逐项源证据、失败复现、隔离 sandbox、追踪表 | 无 |
| M1 | Coordinator Product Broker | OMP 真正具备 Solco Actions，owner/权限防绕行、OMP-only、doctor | M0；AC-01/09 |
| M2 | Herdr Worker Evidence API | Skill发现/启动/输出/证明、失败和未知状态、Report 关联 | M1；AC-04/07 |
| M3 | Assistant Routine & Schedule | 自然语言时间、审批、事件、执行一次、持久水位、无 Repo | M1+M2；AC-02/10/11 |
| M4 | Personal Autopilot | Event/Wake/Checkpoint/Next Action/Budget/Quiet Hours/退出 | M2+M3；AC-05 |
| M5 | Chat/Android Delivery & Proof UI | 统一任务/报告/审批/推送/深链/断网幂等 | M2+M3+M4；AC-03/06/08/12 |
| M6 | 真机结项/稳定性/回滚 | 全量自动回归 + AC-01~13 真机/故障演练 + 版本审计 | 全部 |

每一张工作卡必写：涉及的 Gap ID、参考源码、Solco 模块、禁止改动边界、失败测试、实现结果、验收命令、设备截图/收据和下一步。每批需要跟踪 commit，不允许只有“功能写了”就把状态从 P 改为 PASS。

## 6. 用于执行时的 Goal Prompt

本节是下一轮交给实现 Agent 的完整合同；可直接复制下面内容作为 Goal。

```text
目标：把 Solco 的 Go Personal Assistant 对齐 Shepherdr 3.1.3 的实用编排能力，并增强“个人主动式助理”的持续工作闭环。必须真正可用、好用，最终由 macOS Solco.app + Herdr + OMP + Android 真机验收，不以源码/单测/模拟测试代替。

权威工作区：/Users/wilson/Workspaces/wh-studio/solco
唯一总计划：docs/assistant-shepherdr-3.1.3-alignment-roadmap.md
辅助取证：docs/debug/2026-11-05-assistant-shepherdr-3-parity.md
上游固定源码：eliasstravik/shepherdr commit dc1b67824924c02fde6765f184bcd2a420743b8a（Shepherdr 3.1.3）。每项必须查上游真实源码与测试，不得仅看 README，不得继续拿 1.2/2.1 当最新。
先运行基线、盘点当前 dirty working tree，保留所有他人未提交改动并协调冲突；然后按计划 C/W/R/A/Q/U/L 差距逐项落实，更新状态、源代码行、设计理由、red→green、实机证据。优先解决 P0，P1 要交付可用体验，P2 明确延期和理由。
Solco 是 MyGo Native UI + Go 单一核心 + Herdr 唯一运行时。Assistant/Coordinator 与 Worker 先都锁 OMP。Coordinator 只用授权的 Solco Broker 创建/管理 Goal、Task、Routine、Heartbeat，不开系统 launchd/cron，不在自己会话异步无限做任务，不随意创建裸 Agent。Worker 通过现有 Herdr API/Herdr Skill 所属的运行路径启动，并真实读取状态、结果、证据；特定用户 Skill（如 ego-browser）须可发现、可授权、真调用，失败为 blocked。不要私造 PTY/Herdr/hc runtime，不绕过原应用 Hook 优先级。
核心必须做到：一句话→持久计划/带完整 Brief 的 worker；多个任务依赖/并行/跟进；个人 Routine 的自然语言时间/事件→草案/Owner 审批→独立 Go Scheduler→每触发一个托管 Worker；Agent 结果与引用确实读回→验证→事实→原 Assistant 主动反馈→Android Push/ACK；Autopilot 事件驱动短唤醒、checkpoint、Next Action、Quiet Hours/Budget/权限/停止条件；用户可通过 Mac/Android 一致地审阅、暂停、批准、打开 Task/Proof。
开发前先为每个问题建立可复现的失败用例；按 M0~M6 分批改动，每批完善测试并提交独立 commit，禁止无授权修改生产 Assistant/OS 调度任务/账号数据，任何真实外部账号操作需通过用户授权。运行 root Go、nested plugin Go、OMP 扩展、Frontend、Mobile golden、build、diff check；再用隔离 HOME+真实 Herdr/OMP+真机执行 AC-01~AC-13。
必须给我：① 最新 parity 表每条的 PASS/PARTIAL/BLOCKED 与源码/测试/commit；② 每个 Phase 的提交记录；③ 隔离真实运行的 Herdr/SQLite/OMP/Skill/Push 证据；④ Mac 和 Android 真机的逐案例结果；⑤ 仍未完成与可回滚事项。没有 AC 全部 PASS 不准报告“已完成/已完全对齐”；如遇确实受外部权限阻断，明确 BLOCKED、原因、证据、待用户动作，不臆测成功。
```

## 7. 审计局限与版本更新规程

本文件是 2026-11-05 的**静态源码矩阵**，不是“已经实现全部 Shepherdr 能力”的声明。上游如更新，应另存 upstream SHA、跑语义 diff，并追加变更行；不能在一次开发中悄悄漂移参照版本。Solco 的 UI/Host/Plugin 不等价于上游 hc 或 Rust 1.2，允许适当差异，但每一条差异都必须在矩阵中有“为何存在、如何验证、是否影响用户能力”的答案。

**下一次执行的第一件事**：复验现有 dirty worktree 的未提交补丁（尤其 OMP Coordinator extension、Routine、结果回收）并在隔离运行环境做 failing-before；不要继续通过追加 prompt 约束去掩盖 Broker/Herdr/Result 的结构性缺口。
