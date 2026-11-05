# Coordinator 报告回复关联：真实 OMP JSONL 取证（2026-11-05）

状态：**隔离真实 OMP 读取校验通过；自动报告 Outbox 单测闭环通过；完整 Solco UI/手机实机验收待完成**。

## 原因与约束

- 过去 `ReportSubmitted` 只表示 Herdr 接受 `agent.prompt`，并不能证明 OMP 输出。若允许其后新的汇报继续进入聊天，便可能打断上一轮用户对话或造成报告堆积。
- Herdr 0.9.5 的 `agent.get` 无法提供可使用的 OMP 转录文件路径；同一 OMP 的 `chat.stream` 扩展事件本来就是非可靠提示，不能把 delta 或 `idle` 视为最终输出。Solco 不新增 OMP RPC/PTY/进程，不写 OMP JSONL。
- 本轮使用本地随机 128-bit `report_marker` 与已经验证接受的原 Herdr Pane prompt 进行**相关性**匹配。严格限制扫描 `$HOME/.omp/agent/sessions`：最多 3,000 目录条目、最新 24 个候选 JSONL、每文件最多尾部 8 MiB、总计 64 MiB；独立只读读取。确认唯一出现该 marker 的 Provider user turn，并在**同一 turn、下一条 user 消息之前**找到 `stopReason=stop`、非空文本、真实 ID 的 assistant row 才算 verified。中间 `toolUse`、流式 partial、子代理 `parentSession`、中断、错误、缺失、不唯一全部拒绝冒充完成。
- 本轮单独验证了真实 OMP 18.16.0 的一个真实格式差异：JSONL **可能先写 `title` 再写 `session`**。初版测试通过而隔离真机验证失败；修复为从前 32 条、最多 1 MiB metadata 里读取 Provider session ID，才解析有界尾部。这是实际失败→定位→同环境复验，不是模拟通过后猜测兼容。

## 持久化状态与跨端

- SQLite 升级到 v19：`report_marker`、`reply_state`、`reply_session_id`、`reply_message_id`、`reply_verified_at`、`reply_checked_at`；已有 Herdr 接收记录保留 `untracked` 并进入 `uncertain`，需人工核对（不冒充回复已验证）。随机 marker 永远不出现在手机/桌面的 status JSON 中。
- `ReportPump` 每 10 秒至多查一次待证回复。经 OMP 原文核对完成后以同一事务写入验证元数据和 Solco 的 `FactPulse`（只含已验证状态及 Provider session/message ID），使已有 Activity/Android 本地通知逻辑能补读到此结果。该系统 Fact 被自动报告数据源过滤，不会递归形成“为汇报回复再汇报”。
- `awaiting` 不创建下一批；匹配到 OMP 最终回复才释放下一批（仍受频率与空闲/静默门禁控制）。有真实 Provider `aborted/error` 或 30 分钟仍无法核对时，进入原 `uncertain` + FactBlocked，必须由人检查原 Chat 再通过请求 ID + throughSeq 明确 skip/retry；不确定状态从不自动重发。
- 手机与桌面展示 `Herdr 已接收 / 等待 OMP 回复` 和 `OMP 回复已验证` 两个独立事实。手机从 Host 读真实 ReplyState；旧 Host 没有这些字段时兼容为无验证状态，不绘制成功勋章。提供 `replySessionId / replyMessageId / replyVerifiedAt` 作为证据链接身份，不复制模型正文。

## 实际证据

可重跑：

```sh
cd /Users/wilson/Workspaces/wh-studio/solco
python3 scripts/check-herdr-omp-chat-stream.py
cd plugins/solco-automations && go test ./internal/personalagent -run 'Test(OMPReportReply|ReportPump)'
```

使用独立 `/tmp/sh-*/home`、真实 Herdr 0.9.5 + OMP 18.16.0 仅一个 Agent/Pane、本地 loopback SSE 假模型。第一条 `agent.prompt` 携带 batch marker，OMP 实际调用 read 工具，最终写下 `stopReason=stop` 的 Assistant row；Go `TestRealOMPReportScannerFixture` 在沙箱还存活时读取真实 OMP JSONL 并确认源 Session ID / final Message ID。第二条同 Pane 的 prompt 及连续 14 帧 / 6 delta 仍通过。无付费模型、无用户真实 Herdr 接入。

独立 plugin 测试涵盖：工具中间态 vs 最终回复、assistant 中断/error、空输出、插入下一用户 turn、跨文件重复 nonce、子代理和 symlink、超大 session 尾部、重启补证、上一批未完成阻止第二批、超时人工复核、显式 skip 后恢复、Activity 仅一条验证通知且不触发新报告。

## 仍需明确的界限

- Marker 匹配是接受过的 prompt 和用户本地 OMP 文件之间的唯一随机标识**相关性证明**，不是 Herdr 提供的不可伪造原子 SessionID→文件路径证明；若同一 marker 在多个文件出现，拒绝自动验证。
- 自动汇报仍是 prompt 层“请只总结”的约束，不能在 Go 层确保模型完全不调用有写入能力的 tools；真正工具权限隔离和质量/evidence 验收未实现。
- 当前只是这一报告 Outbox 与 OMP 原始文件的实际校验，完整 Assistant 建议计划→审批→多 Worker→汇总→Chat UI 的 E2E、跨设备视觉/交互验收、Herdr 原子 `if_idle` 接口依然是后续门槛。

### 有界候选扫描再加固

- Production `FindOMPReportReplySince(home, marker, lastSubmittedAt)` 以真实 Herdr 接收时间为基准，容忍最多 10 分钟的文件写入/时钟偏差；旧历史会话不占用近期报告的候选预算。完整回溯/本地沙箱仍可使用 `FindOMPReportReply`。
- 当前 **最多 24 个近期候选文件**、**总计 64 MiB** 的预算仍保持。**超过预算或数量则不宣布唯一成功**，不会“只看前 24 个”就误判剩余文件没有相同 marker。对于扫描范围内打不开的文件同样 fail-closed；报告进入正常等待/超时核对流程。
- 检查完整单文件尾部而非匹配第一条最终回复就提前返回：同一个 Provider JSONL 若重复出现相同 marker 的 user turn，即使之前有完整 assistant stop，也视为歧义，不自动验证。无关的后续普通用户对话不影响已完成报告的证据。
- 如果插件未接入核验 callback 或持久化 marker 不可用，30 分钟后仍会生成需要人确认的 `uncertain` 状态，不再无限停留在 `awaiting`。
- 对应回归：`TestOMPReportReplyRecentScopeAndIncompleteCandidateSet`、`TestOMPReportReplyRequiresExactProviderCompletedTurn` 的同 session 重复 nonce 测试、`TestReportPumpMissingVerifierBecomesOwnerReviewAfterDeadline`；真实 Herdr/OMP 隔离脚本再跑 PASS。
