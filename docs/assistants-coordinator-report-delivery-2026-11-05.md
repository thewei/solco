# Coordinator 主动阶段汇报：事实驱动与安全投递（2026-11-05）

状态：**核心已实现 / 自动化测试通过；真实 OMP + Herdr + UI 的端到端消息生成仍待实机验收**。本文不把 Herdr 接收 prompt 等同于 OMP 生成答复，也不宣称推送到已退出的手机应用。

## 预期行为

人在 Solco Assistant 的 Behavior 中启用 **定期汇报**（`reportEveryMin > 0`），且 Proactivity level 不是 `respond`，当前不处于 quiet hours。必须已经存在一个经 Herdr 正式启动、状态为 `ready`、持有完整 Herdr Session / Workspace / Pane 的原 OMP Coordinator。缺任何一项则不创建模型任务、不启动第二个 Agent、不提交消息。

1. Supervisor 先提交 Task/Goal/Run 事实（`goal_facts.seq`）。
2. ReportPump 根据设定频率比较 `coordinator_reports.submitted_seq` 和 Fact 最新 seq；没有新事实则不打扰 OMP。一次报告包含最近至多 12 条 Fact、至多 12 个真实 Worker Task + 最新 Run status；超出范围明确称为有界摘录。
3. 原子持久化 `queued`，确认原 Coordinator identity/revision 后标记 `claimed`，然后才调用原 Herdr `agent.prompt`。真实 OMP 可在同一 Chat 中生成自然语言进度总结；输入显式标为 **Solco automated status request — NOT a message from the human**。来源按 Fact # / Task ID / Goal ID 引用，正文禁止新建任务、修改状态、发起子代理或自行确认完成。
4. Herdr 明确接受文本才标记 `submitted`、推进源 seq 水位并记录本次提交时间。此状态**只证明 Herdr 接受，不能证明 OMP 已完成自然语言回复**；新事实/下一间隔到来才会再次汇报。
5. Herdr 确切拒绝且没有写入（busy/not-ready/agent gone）时延期有限重试，最多 8 次；新 OMP/目录信任弹窗未完成时不输入。网络失败、超时、进程崩溃或 Herdr 提交是否成功不确定时停在 `uncertain`，**绝不自动重试**，只记录 Solco `FactBlocked` 告诉人在原 Chat 检查。告警 Fact 不会成为下次汇报的输入，避免自我触发。
6. `coordinator_reports` 与 Assistant 的其他数据库资料一同在“永久删除”时清理；Archive/Restore 不擅自消除不确定的投递。
7. ReportPump 与原 daemon scheduler 同一 SQLite 权威，但放在独立、单实例 goroutine 中调用 Herdr，长网络请求不能阻塞 Task Supervisor、Worker Dispatcher 和 Routine 调度。

## 运行约束

- **唯一 Agent 运行时**：只调用插件原有的 Herdr `AgentSubmit`，不触碰 OMP transcript 文件、不伪造助理回答、不新建 Console、PTY 或 Agent。这条自动产生的请求会成为真实 Agent Chat 的输入，文本本身清楚标识不是人的话；输出来自 OMP 才可被呈现为 OMP 回复。
- **单进程不等于天然 exactly-once**：SQLite claim 在外部 Herdr 操作前落地；外部调用成功而确认失败的窗口不可原子化，因此宁可 `uncertain`、要求用户审阅。不存在自动跨重启重复发送。
- **文本安全**：Fact 文本/证据从本 Assistant 的库读取、有长度/数量边界，作为不可信数据展示；仅 OMP 回答内容源自模型，不使用数据库假造模型输出。系统日志只记提交/不确定数量，不记录 Fact/Task 正文或 Prompt。
- **频率约束**：`reportEveryMin = 0`（默认）绝对不主动发送；`respond` 模式禁用；quiet hours 禁止提交（留待之后）；还未启动/已停止/blocked/uncertain 的协调者不会收到自动报告。
- **个人审批**：不因为更新进度而自动发布 coding draft、approve goal、merge 或调整 proactivity。
- **桌面和手机**：两端继续读同一个真实 Herdr Agent 的聊天输出；既有 Activity Feed 和 Android 本地通知仍独立、可断线补读。

## 人工恢复 `uncertain`

通过插件 CLI 读当前状态，先在原 OMP Chat 中确认是否已经收到、回复了该自动汇报：

```bash
solco-automations assistant report show a_<assistant-id>
```

如果已经收到了汇报，或打算忽略本次投递，显式消耗该批事实（后续新事实仍可以汇报）：

```bash
printf '%s' '{"requestId":"unique-human-action-id","throughSeq":42,"action":"skip","confirmed":true}' \
  | solco-automations assistant report resolve a_<assistant-id>
```

如果确认需要重新尝试并接受可能重复汇报的风险，将 `action` 改成 `"retry"`。**用户动作不可由 daemon 自行推断**；用户应核对 `throughSeq` 与 `show` 返回的值，发生状态变更则拒绝。CLI 的确认字段只能证明发起命令者主动确认，当前尚未实现身份认证层面的 Owner-only 按钮。需要继续补桌面/移动端受控恢复交互，避免要求用户开终端。

## 自动化测试与剩余项

- ReportPump：未就绪 Coordinator 不发；只对原 Herdr pane 发送；跨 Assistant 不泄漏；只取真实 Fact / Task / Run；不重复相同水位；新事实到下个间隔才再发；quiet hours 拦截；Stop 不会重启 Coordinator。
- 拒绝/故障：弹窗阻塞不输入；Herdr 确认零写入可延迟重试；网络结果不确定阻断自动补发；手工 Skip/Retry 的 requestId 幂等与 seq fencing；崩溃后 claimed→uncertain；“投递失败”自己产生的 Fact 不会导致汇报循环；永久删除清除记录。
- 仍需：真正 OMP + Herdr + Chat 的隔离实机测试（含忙碌、compaction、App 重启）；模型回复被展示的确认/证据，在线传输异常的监控；Owner-only 的恢复 UI；人发起的每个复杂目标与 Chat turn/Goal/Task 的 durable request trace；结构化 Worker Brief 验收。这些不能由“Go 测试通过”替代。

### Prompt 约束的技术局限

同一 OMP Coordinator 仍能调用其既有 Assistant CLI；“这次自动汇报只允许总结、不创建新任务”的约束写在 Coordinator standing prompt 与自动汇报输入中，**目前不是 Go 侧可独立强制的工具权限隔离**。真实 OMP 可能没有遵从文本说明，不能仅凭提示词认定它绝无副作用。若产品需要强制只读，可后续为自动汇报设计 OMP 可验证的只读工具/权限上下文，且不能绕开 Herdr 的信任与权限流程。
