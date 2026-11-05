# Assistant / Shepherdr 3.x 行为取证与修复门禁（2026-11-05）

- 状态：**局部红绿回归通过，完整 Solco / Herdr / OMP 实机闭环未完成**
- 循环：第 1 轮
- 用户现象：对原 Solco Assistant 说“定时用 ego-browser 查看 X.com，提取高赞/高推荐内容并总结”，它创建了 macOS 系统定时任务、另开自己的异步任务，绕开 Solco Routine 和 Coordinator。
- 本次边界：用户既有 Assistant、Herdr Pane、系统定时服务、X.com 登录态均**未读写或重启**。只修改 Solco 插件源码与测试。

## 0. 参考源码的版本事实

2026-11-05 从原始 Git 仓库只读取得：

- URL：`https://github.com/eliasstravik/shepherdr`
- 分支：`main`
- commit：`dc1b67824924c02fde6765f184bcd2a420743b8a`
- README 当前标记：**Shepherdr 3.1.3，herdr-app 0.1.12+**
- 本机隔离调研目录：`/tmp/solco-shepherdr-current-20261105`（不参与运行/打包）
- 本项目旧 `docs/personal-agent-plan.md` 提到的 `herdr-projects v1.2.27` 是历史设计，不是本轮对照基准。3.x 基于 herdr-core，**不能照搬其 `hc` 接口进仍由 Herdr 0.9.5/协议 22 承载的 Solco**；对照的是产品语义、任务状态与自动化交付协议。

直接参考的上游实现：

| 最新 Shepherdr | 语义真值 | Solco Go 对应 |
| --- | --- | --- |
| `templates/assistant.md` | Assistant 只协调；`herdr-task` 是受控任务入口；worker/news 与原 Assistant 关联 | `personalagent/coordinator.go`、`dispatch.go` |
| `mods/tasks/bin/herdr_task/automations.py` | 时间/事件触发→创建新的托管 Task；在后台 scheduler 驱动，不在 Assistant Chat 常驻循环 | `api_routine.go` → `routine.go` → `rules.go` |
| `mods/tasks/bin/herdr_task/autopilot.py`、`service/autopilot.ts` | 每 Space 唤醒、heartbeat、事件与进度持久化；无需要时退出 | `heartbeat.go`、`proactivity.go`、`supervisor.go` |
| `mods/tasks/bin/herdr_task/tick.py` | 状态变化、队列、并发、去重、恢复 | `dispatch.go`、`rules.go`、`watch.go` |
| `mods/coordinator/coordinator.js` | 单个 Assistant/Coordinator 与持久任务归属 | `coordinator.go`、`coordinator_report.go` |

## 1. 根因与复现证据

1. **启动缺失产品能力约束**：`CoordinatorPrompt` 虽写了 Solco CLI 示例，`Dispatcher.launch` 却将它作为普通 OMP 用户消息提交；没有明确工具入口/deny-by-default gate。所以上下文、其他 Skills 或 OMP 自身工具可以诱导它写 launchd/cron 或派生无归属的异步工作。此前隔离测试**未覆盖**这种工具路由行为。
2. **仓库假设错误**：`api_routine.go` 强制任何 Routine 带绝对 `repo`。纯个人 X.com 研究没有仓库，正确的 Solco API 曾直接拒绝。新增 `TestRoutineDraftForPersonalResearchUsesOwnedFolderAndOMP` 在补丁前确切失败：`invalid: repo must be an absolute path`。
3. **Agent 选择漂移**：Assistant Task 已锁定 OMP，而 Routine 原默认 `claude`；本轮统一默认和显式选择的 OMP 限制。
4. **Worker 边界缺口**：Assistant Routine 经过 `LegacyRuleID` 复用旧规则执行后，`Dispatcher` 原样转发 YAML 的 Prompt，没有 Solco Worker 的编排边界；仅为 Assistant-owned Routine 增加运行时包装，不改原批准定义或旧 Automations。
5. **参考版本错位**：旧规划选的是 v1.2.27 Rust Fork；用户明确要求的是**最新 Shepherdr 3.x 的语义转 Go**。上游 3.x 已有完整的 Assistant template、托管工具、Auto、Autopilot、worker/news 规则；Solco 不能只复述早期的“一个 Coordinator + 简单 Cron”。

## 2. 这轮已改的最小闭环

- `api_routine.go`：无 `repo` 的 Assistant Routine 使用 `SOLCO_ASSISTANTS_WORKDIR/personal/routines/<assistantId>`，目录权限 0700，`workspace=root`；外部仓库原有 worktree 行为不变。Routine 必须是 OMP、默认 headless、仍一律 draft 并经过 Owner 审批。
- `coordinator.go`：明确单个定时意图应调用 `assistant routine draft`，不给系统 launchd/cron、内存循环、普通一次性 Task 或新异步子代理；把 `ego-browser` 等技能要求写入**Worker 的完整 brief**。原 CLI 实例仍为协议例子。
- `extensions/solco-coordinator.ts` + `dispatch.go`：仅 Coordinator 通过 `--extension` 加载 OMP 专用 `solco_assistant` 工具。此工具以当前 Coordinator Herdr workspace 确定 Assistant ID，以固定动作白名单调用插件 CLI；明确禁止普通 bash、write、subagent 和系统 scheduler 工具路径；Goal/Task ID 访问前核对 owner；Coding Task 必须 draft，Routine 始终 OMP/headless/draft。Worker 不载入该扩展。
- `launcher.go`：真实 Herdr Provision 之前校验本地扩展存在；没有产品工具不能悄悄拉起一个可自由调用外部工具的 Coordinator。
- `dispatch.go`：仅已批准的 Assistant-owned Routine 发给 Worker 时包装 Solco Task 边界，不允许 Worker 自己另建系统计划/平行任务；旧 legacy Automation 继续使用原始 Prompt。
- 注意：工具拦截**不是 macOS 沙盒或安全边界**。其他已加载扩展代码、Provider 本身以及本机账号权限不会因此被剥夺。扩展需要真实 OMP 启动与工具拦截证据才能判定“运行上已阻断绕路”。

## 3. 目标能力与最新参考的差距（仍需实现）

| 行为 | 当前 Solco | 剩余门禁 |
| --- | --- | --- |
| 自然语言一次请求→受控 Solco Task/Goal | CLI、Supervisor 已有；本轮加 Coordinator 工具路由 | 需真实 OMP 的意图→ToolCall→正确 ID 收据，检验多任务依赖、幂等重放 |
| 自然语言定时→Solco Routine | 仅支持已翻译的 5 段 Cron；有草案/审批/定时 tick | **P0**：未提供上游式 `every 30 minutes` / `weekdays 9:00` / 一次性与事件触发转换、带时区的运行预览；不能伪称全部支持 |
| 定时托管 worker | Cron → RuleScheduler → Task → Herdr OMP；一名 Assistant 归属 | **P0**：需要隔离环境下验证 1 次触发只生 1 个 Task、同一 Routine 不重叠、重启不重复 |
| 结果/完成证据→协调者主动反馈 | Supervisor Fact、ReportPump、原 Pane OMP 回复关联已存在 | **P0**：需要 DB Fact→Report accepted→OMP 真实回复→Chat/手机推送的整链验收；“accepted” 不等于“answered” |
| Autopilot/Heartbeat 自主推进 | Heartbeat、事件持有、WakeBudget、Proactivity、原 Coordinator 汇报 | **P1**：仍缺上游 3.x 每 Space 任务驱动的完整 Autopilot 连续动作协议与持久 progress/next-step 对照验收；不能靠一个后台模型永远工作 |
| Skill 显式绑定 | Worker Prompt 携带 skill 名；OMP 全局发现可用技能 | **P0**：检查真实 Worker 是否能发现、加载并执行 `ego-browser` Skill；无技能时需明确 blocked 而不是改用其他浏览器 |
| 任务/审批/结果统一 UI | `/assistants` + Automations 管理、审批；WebView / 原 Chat | **P1**：跨桌面/Android 的归属、审批卡、Routine 链接、状态与结果证据一致性 |
| 3.x 多机/浏览器 per-task | 本轮不照搬 hc-core 私有 API | **P2**：在 Herdr 0.9.5 边界单独评估；不得再造第二个 Agent Runtime |

## 4. 端到端验收（必须完成才能宣称行为已修复）

只使用隔离 HOME、Herdr daemon/socket、临时数据库/config、fake SSE OMP 模型；**不动真实 X.com、不创建真实 OS 定时器**。

1. 创建 Assistant 并成功启动 1 个 OMP Coordinator；查 Herdr agent identity / Pane；验证启动参数包含本次扩展且 OMP 实际注册了 `solco_assistant`。
2. 输入“每个工作日上午 9:00 用 ego-browser 看 X.com 高互动消息，附链接和时间，发摘要给我”，其受控 ToolCall 必须创建 1 个属于该 Assistant 的 Routine draft；绝无新的 `launchd`、`crontab`、`at`、`tasks` 外部记录，亦无未托管的子 Agent。
3. Coordinator 返回 Route/Approval ID、时间解释、下一次运行；审批之前，即使时钟越过触发点仍无 Worker。
4. 在模拟时钟审批后执行 RuleScheduler；一个 occurrence 恰好创建 1 个 Assistant 归属的 Task，Worker Agent=omp、prompt 含 `ego-browser`，且成功按该 Skill 读取模拟 X.com fixture。
5. Worker 返回包含来源链接、互动指标、时间和不确定项的结果；Supervisor 记录真实 Run/Fact；原 Coordinator 获得一次通知，在**同一原 Chat**中报告；远程 Host 推送只发送已证实状态。
6. 重启 Scheduler / 模拟 Herdr 暂时不可用 / 权限弹窗 / 回放相同 requestId；不能生成双 Coordinator、双 Routine 或双 occurrence；不确定投递保持待核对。
7. 对两个同时进行的 Goal、一个 Routine 和一个受阻任务，Coordinator 能根据任务图及事实主动跟踪，闲置后不再占模型或生成匿名系统任务。
8. 在 UI 的审批页撤回 Routine，检查下一次没有触发且既有历史可追溯；在无 `ego-browser` 环境中必须显示 blocked 而不是悄悄替换工具。

### 本轮验证记录

- RED：`go test ./internal/personalagent -run TestRoutineDraftForPersonalResearchUsesOwnedFolderAndOMP` → `invalid: repo must be an absolute path`
- RED：`go test ./internal/personalagent -run TestCoordinatorLaunchInjectsScopedOMPBroker` → Coordinator `AgentArgs = []`
- GREEN：两个上述 Go 聚焦测试，以及 Routine draft/refusal 测试通过。
- GREEN：`bun test extensions/solco-coordinator.test.ts` 5 tests / 36 assertions；`bun build --target=bun` 成功。
- PLUGIN FULL：第一次 `go test ./...` 仅在两处旧 Golden fixture 失败（`claude` → `omp`），已审核并刷新这两个 fixture；待最终全量复验。
- NOT DONE：真实 Herdr Agent 通过 `--extension` 装载、OMP ToolCall 的实际拦截、真实用户 Assistant UI、手机推送与 E2E X.com 阅读。

## 5. 下一阶段计划

- **A / 运行门禁**：在隔离 Herdr/OMP fixture 验证扩展实际加载、ToolCall 拦截、CLI 收据与重复请求；补部署链路的扩展存在性检查；已运行旧 Coordinator 仅在经原 Pane 身份核对并安全停止后重新启动，不偷偷替换。
- **B / 自然语言时间规则**：Go 中做明确的时间语义解析（含时区/下一次执行的 preview），不确定则让 Coordinator 提问；对同一 Routine 补事件触发，迁移时与旧 cron watermark 去重。
- **C / 主动闭环**：对照上游 Autopilot 完善 heartbeat→goal graph→worker→evidence→progress→原 Coordinator follow-up，一次唤醒有预算、有边界、有退出条件。
- **D / 全平台验收**：真正 OMP 使用 `ego-browser` 的隔离演练、跨桌面/Android Task/Routine 审批、系统推送、睡眠恢复、停机重启。

**终止条件**：上面的 8 条中任何一条失败，都不能说“已完全达到最新 Shepherdr 的主动助理能力”。此文为取证/实施记录，不重新定义 `docs/client-product-architecture.md` 的所有权。
