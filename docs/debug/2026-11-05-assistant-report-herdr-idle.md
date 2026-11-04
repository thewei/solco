# OMP Coordinator 周期汇报：Herdr 忙碌门禁与真实投递验收（2026-11-05）

- 状态：**隔离 Herdr/OMP transport 验收通过；Assistant SQLite → OMP 回复匹配尚待验收**
- 范围：仅 Coordinator 的自动进度汇报；不改变 Worker's Run/Group 消息投递协议。
- 环境：macOS arm64，Herdr 0.9.5 / protocol 22，OMP 18.16.0；`/tmp/sh-*/home` 独立 HOME、独立 Herdr daemon + socket、scratch Git 目录，OMP 使用 `127.0.0.1` 本地 SSE 假模型。未连接外部模型，也未访问用户真实 Herdr Socket / 配置。
- 可重复命令：
  ```sh
  python3 scripts/check-herdr-omp-chat-stream.py
  cd plugins/solco-automations && go test ./internal/herdr ./internal/daemon ./internal/personalagent
  ```

## 失败前事实

1. `CoordinatorReady` 只反映 SQLite 里最后一次成功启动；原 OMP 可以已经在回答用户消息（`agent_status=working`）、停在 trust/setup 弹窗，或者 Herdr 运行时身份已改变。旧 `ReportPump.tickOne` 在生成报告后只看启动弹窗文本，不读取即时 Herdr `agent.get`，无法防止空闲以外的自动插话。
2. 针对需求先增加 `TestReportPumpWaitsForOriginalOMPPaneIdle`，编译在 `unknown field Ready in struct literal of type ReportPump` 失败，记录了缺失的实时门禁。
3. 旧周期汇报复用 Worker 的 `AgentSubmit(... --wait --until working)`。本仓库更早的隔离 Herdr/OMP 证据已显示：假模型回答极快时，即便 OMP 已完整回复，Herdr 的 `--wait` 仍可能报告 `agent_prompt_stalled`（见 `docs/debug/2026-11-05-herdr-omp-chat-stream.md`）。该错误被旧汇报逻辑视为不确定，产生不必要的人工复核。

## 本轮实现

- 生产 daemon 的 `ReportPump.Ready` 通过 `herdr agent get <paneId>` 实时取证，**只**允许 `type=agent_info`、`agent=omp`、`pane_id` 精确匹配、`agent_status=idle` 且 `interactive_ready=true`。没有 `Ready` / `Blocked` 回调的 ReportPump **拒绝投递**；Herdr 超时、未知返回、不同 Agent、working、blocked、未完成首次启动均保持 `queued`，不 claim、不调整 ACK / attempts。
- 自动汇报的屏幕检测额外 fail-closed：`pane read` 失败时按阻塞处理，而不是认为没有 trust/setup 对话框。ReportPump 仍会再次读取当前 Proactivity，再以 SQL CAS 核对 Assistant / Coordinator 身份后 claim。
- 增加 `herdr.Client.AgentPromptAccepted`：用**非等待** `agent.prompt` 并解析 `agent_prompted` 凭据，校验返回 PaneID 与目标一致。其返回成功只意味着 **Herdr 已接受**，不是模型已经输出。Worker/Group 原有 `AgentSubmit` 保持不变。
- 仍采用 `claimed` → 外部 Herdr → `submitted` 的现有 Outbox 协议；任何真实提交结果不确定的异常仍走 `uncertain` 和人工复核。即使 Agent 在 probe 后、调用前转为 working，也不能把潜在输入当成“肯定没发”，仍按未知状态保护。

## 验收事实

```
Herdr OMP Chat stream: PASS (1 Pane/1 Agent, 2 idle guards + 2 pane-fenced prompt receipts; 14 IPC frames; 6 deltas; read tool + TUI answer)
ISOLATED_E2E_EXIT=0
```

两轮 `agent.get` 都由**真实 Herdr CLI** 返回对原 OMP Pane 的实时 `idle + interactive_ready` 事实；两次非等待 `agent.prompt` 都返回同 Pane 的 `agent_prompted`。Companion Extension 连续 14 帧、6 次文本 delta、真实 read 工具调用，原 Pane 有回复，Herdr `agent list` 始终只有 1 个 Agent。沙箱退出后自动停止 Herdr 并清理目录。

对应单测覆盖 `AgentLiveState` 错 Pane/类型/状态/缺字段、`AgentPromptAccepted` 错 Pane/类型/无凭据、daemon 真 CLI 连接和失败屏幕读取、ReportPump 忙碌待发/恢复、缺任一回调 fail-closed。

## 仍未解决

- **没有批次→真实 OMP 回复的双向关联**：`submitted` 只能证明提示词被 Herdr 接收。今后需用同一真实会话的 transcript/extension message ID 关联，随后以模型实际完成标记/evidence 验证。不能用 `agent_status=idle` 伪造模型完成。
- Herdr `agent.get` 和 `agent.prompt` 之间没有可见的原子化 `if_idle` 参数；用户可能在读之后立刻发送消息。这是运行时控制器的协议缺口，不能声称彻底消除并发插话。
- 此次 sandbox 验证的是**真正 Herdr TUI 的收据、同 Pane 流与回复**；尚未通过完整 Assistant 从 SQLite Fact 到自动 Prompt 到自然语言任务总结的业务验收，也未在真实 Solco WebView/Android UI 上做视觉/交互回归。

### 最终加固：Herdr CLI 超时

- 追加有界 deadline：报告专用 `agent.get` 状态查询 **5s**、原 Pane 截图/Recent screen **5s**、非等待 `agent.prompt` 接收命令 **30s**；`PaneReadContext` 经 `exec.CommandContext` 取消，原 Worker/Group 常规 `PaneRead` 未改变。
- 状态查询与截图超时一律 fail-closed，报告保持 `queued`；一旦进入 `claimed`，提示词命令超时的结果不可判定，一律进入原 `uncertain`，人工核对后才允许再发送。
- 测试：`TestPaneReadContextHonorsCancelledReportProbe` + 插件 `herdr` / `daemon` / `personalagent` 定向回归。30s 超时是外部执行保护，不是模型完成判据。
