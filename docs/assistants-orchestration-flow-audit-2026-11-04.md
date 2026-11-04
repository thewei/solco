# Assistant 端到端编排与统一 Composer 审计（2026-11-04）

状态：**PARTIAL / 持续实施**。这是代码行为与自动化回归审计，不代表真实 OMP + Herdr + App 的实机验收。

## 目标流程

```
人提出一个目标或多个请求
  → Coordinator 在 Chat 中澄清边界/成功条件，说明分析与步骤
  → 将复杂目标持久化为 Goal Draft + 有依赖关系的 Milestone Plan
     或将互不依赖的事项拆为多个 Task
  → 对每个 Worker 生成独立完整 Brief（范围/输出/验收/阻塞上报）
  → Owner 批准 Goal / 发布 Coding Draft（审批不可跳过）
  → Go Supervisor 按依赖/预算/最大 fan-out 分派
  → Dispatcher 通过 Herdr 运行 OMP Worker，不复制运行时
  → Task/Run/Review 事件与证据持久化
  → Supervisor 校验里程碑并记录事实
  → Assistant WebView 显示 WorkPulse + FactStrip；配置了 reportEveryMin 时定期生成事实摘要
  → 人主动问 Coordinator 时，基于 Task Graph / Goal Plan / Facts 做自然语言总结
```

## 审计矩阵

| 阶段 | 核心代码 | 目前结论 | 缺口/后续 |
| --- | --- | --- | --- |
| 首次消息启动 Coordinator | `assistants/AssistantsPage.tsx`、`AssistantsBridge.EnsureChat` | **已实现（需实机）**：保留草稿、幂等请求、Herdr 确认后发送 | 启动超时/传输不确定时应有用户可操作的原会话诊断；不能自动重发可能已交付的消息 |
| 任务分析与计划 | `CoordinatorPrompt`、Goal Plan | **部分**：站立提示词规定分析、明确步骤、验收；Go 校验里程碑/依赖/修订 | **P0** 提示词不是强制编排器；尚无“每个重大请求→计划/执行记录”的可验证映射和审批交互 |
| 多任务创建与并行 | `CreateTask`、`Supervisor.dispatch` | **已实现**：独立任务 + 带依赖的 Goal 计划，fan-out 默认 3，幂等及预算 | **P1** 对每个用户请求的跟踪 ID/关联 Chat Turn 缺失；任务只有非结构化 Brief，缺少可审计的 schema |
| Worker OMP 执行 | `Dispatcher`、`HostLauncher` | **部分**：Herdr 唯一 Agent 运行时；不确定启动不会盲目重复 | **P0** 真实目录信任/权限屏、失联重连、启动成功但 Prompt 未交付的取证矩阵未封闭 |
| 完成/验证 | `GoalPlan`、`Supervisor.step`、`Review` | **已实现**：已做、验收通过、待人工 Review 不混为一谈 | **P1** Worker 证据格式不强制，结果质量需单独复核；Coding 必须 Owner 发布及 review |
| 事件/持续跟踪 | `Supervisor.eventFacts` | **本轮完善**：单独 Task 的创建/完成/失败也进入 Facts，事件水位可重启恢复 | 只有事实投影，暂未由后台 Agent 进行反思/重新规划 |
| 定时摘要 | `Supervisor.periodicReports` | **本轮新增，需真实通知验证**：用户启用 `reportEveryMin` 后依正式状态计数并留可去重事实，尊重 quiet hours；默认 0 不发送周期摘要 | **P0** 还不是聊天消息 outbox，没实现可靠的发送/ACK/重连补发，也没有手机 push；不能声称“关闭 App 也会收到 Coordinator 消息” |
| 对话 UI | `frontend/src/App.tsx`、`composer-surface.tsx`、`assistants/*` | **本轮统一**：普通 Chat、Coordinator 冷启动、Group 都共享 Composer 的 DOM 结构、输入控制与扩容规则；同一个 WebView SPA | **P1** 冷启动不能使用需要实际会话的附件、语音、模型切换、Context 环；后续可把可预启动的功能从 App controller 抽为共享状态机 |
| 任务概况 UI | `WorkPulse` / `FactStrip` | **本轮新增**：显示真实 Worker 数量/待处理，事实明确署名 Solco，未伪装 Agent | **P1** 需支持按 Task/Goal 来源跳转和证据引用、UI 实机截图与无障碍检查 |

## 约束与安全原则

- 任何任务创建都需要固定 Assistant ID；复杂任务创建 Goal Draft、Coding Task Draft 均保持人为放行。
- 任务事实与“协调者自然语言回复”是两个不同数据源，不可伪造成同一 Transcript。
- 进度百分比只有来源数据可证实时展示。当前使用真实任务状态和“已验证的里程碑数”。
- OMP 由 Herdr 唯一运行；不允许引入第二套后台 agent runtime 或直接绕过权限/信任 UI。
- 自动调度尊重 Enabled / Quiet Hours / Budget / MaxFanOut；循环失败应进入“需要人处理”而非无限重试。

## 后续实施优先级

1. **P0-1：请求关联/计划状态**：将用户发起的 Request ID、Chat source、Goal/Task 和计划版本建立 durable trace；复杂任务未规划时 UI 显示“规划中/需补充”，允许审核计划后发布。
2. **P0-2：可靠主动汇报**：设计受控 Activity outbox（sourceRef、sequence、delivery/ack/retry/quiet schedule），对接 desktop/phone 的可信通知及 Assistant conversation 中独立的系统进度卡，避免重复或覆盖 OMP transcript。
3. **P0-3：真实 OMP E2E**：隔离 HOME + Herdr 运行测试，涵盖 first send→plan→审批→两个并行 worker→失败/阻塞→恢复→验收→多次进度更新→睡眠/重启；必须验证 Hero/Herdr 进程与 SQLite 真值。
4. **P1-1：结构化 Worker Brief & 验收**：Go 端验证范围/输出/证据/下一步字段，不仅靠 Coordinator Prompt；人工可以修改、批准或拒绝。
5. **P1-2：跨端统一呈现**：同源 WebView Composer 全功能分级、可跟踪的 Task/Goal 卡和 evidence 深链；移动端宿主支持动态通知。

## 本轮自动验证

- 插件 `go test ./...`：**PASS**（包含 independent Task event ledger、周期报告与原有 GoalPlan/Supervisor suite）。
- 前端 `bun run typecheck`：**PASS**，桌面 `bun run build`：**PASS**（其他验证见开发回合记录）。
- 未进行真实 OMP + Herdr + GUI 端到端验收；不能据此宣称最终目标全部实现。

## 继续优化：Stop → Restart 防重复（2026-11-04）

- **新增安全闸**：Coordinator 的 `stopped / failed` 不再意味着可以直接重新启动。如果旧协调者或其 Run 仍关联 Herdr Pane，`coordinator start` 必须由只读 `HerdrPaneGone` 正面确认旧 Pane 已关闭；Herdr 不可用、Pane 仍在或记录的 Session / Workspace / Pane 不完整，全部拒绝。由于 `StopCoordinator` 目前只变更 SQLite 状态，用户必须先在原 Terminal 结束旧 Agent/关闭 Pane，再启动新 Coordinator。
- **核对 Run**：即使 Coordinator 行没有记录 Pane，依然读取旧 Task 的所有 Runs 验证；原 Run 处于 `claimed/starting/running/blocked/uncertain` 且没有可查 Pane 时直接拒绝，避免未经确认的重复启动。
- **并发和幂等**：Herdr 读在 SQLite 写事务之外完成；写入前比较 Coordinator revision 与所有旧 Runs 的状态和 Pane 身份。验证后才把遗留 `running` Run 收敛成 `cancelled`，并创建下一次 Task。同一 `requestId` 优先返回已有收据，不需要再次触碰 Herdr。
- **测试**：`TestStoppedCoordinatorCannotDuplicateLiveHerdrPane`、`TestAPICoordinatorRestartRefusesUnclosedPane`、`TestCoordinatorStopAndRestart`，插件全量测试 PASS；这不等于真实 OMP 进程停止的实机验证。
- **仍待修复**：Stop 目前不是 Herdr 的真实 kill/close；后续要增加受控的运行时停止控制器，并处理关闭时与运行中 Dispatcher 的竞态，不能把“已停止”标签当作真实进程存活证明。

## 继续优化：Archive → Restore 运行时安全（2026-11-04）

- Archive 在 SQLite 写事务之前经 Herdr 验证该 Assistant 所有已记录 Pane 都已关闭；历史 Run 虽已 `done` 但 Pane 仍在，同样拒绝归档。Herdr 离线、不确定或 Session/Workspace 身份不完整均不可视为关闭。
- 在确认外部 Pane 状态之后，写事务里再次对比所有 Run 的 Pane 身份，防止归档验证期间新增/切换 Pane；后续永久删除也采用同样的事务内核对。
- 对已安全结束的 Coordinator，Archive 将旧协调者状态重置为 `stopped`；恢复 Assistant 后可重新启动，但再次启动仍要重新验证原 Pane 关闭，不能凭历史成功证明盲目创建。
- 自动测试覆盖归档幂等、仍有 Pane 的拒绝、检查期间 Pane 变化的拒绝、归档-恢复-重启；`go test ./...`（plugin）PASS。没有实际 Herdr/OMP 进程级测试时，**不能宣称进程停止功能已实现**。
