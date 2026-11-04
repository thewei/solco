# Solco Chat View 原生流式事件实施计划（2026-11-04）

状态：**SC-01–SC-04 已落地并做失帧/窗口滑动加固；SC-05 OMP 18.16.0 隔离加载通过，真实 Herdr 同会话验收仍待做**

目标：在**同一个 Herdr Pane / OMP TUI / Agent 会话**内，将模型增量文本和工具生命周期实时投影到现有 Chat View；不引入第二个 Agent 进程，不改变 Prompt/交互写路径。

## 0. 代码与外部事实

- Solco 已有 `history.LiveDecoder + LivePump` → `chatsession.Fold` → `ChatBridgeState` → `chat:updated`，发布节流为 120ms；LivePump 使用事件唤醒 + 2 秒后备轮询。
- `history/live.go` 的已移植增量读取者为 Claude Code、Codex、Cursor、Antigravity CLI、Pi、OMP；其中 OMP/Pi 共享 Pi JSONL decoder，但 OMP 转录写入不等于逐 Token 写入。OpenCode/其他 Providers 未全部移植。
- Tern Work 元素提供 `message/trace/tool/agent/checklist` 组件及语义节点更新；**不等于 Solco 已具备 TSP 解码能力**。参考：https://docs.stencil.so/tern/elements/work.html
- OMP Extension 支持 `message_start/message_update/message_end`、`tool_execution_start/update/end`，`message_update.assistantMessageEvent` 包含 `text_delta/thinking_delta`。参考：https://github.com/can1357/oh-my-pi/blob/main/docs/extensions.md
- OMP RPC 可产生原生 delta，但 `omp --mode rpc` 是**另一个进程/模式**；禁止将其用作当前 Herdr TUI 同会话读取器。参考：https://github.com/can1357/oh-my-pi/blob/main/docs/rpc.md
- 现有 `~/.omp/agent/extensions/herdr-omp-agent-state.ts` 由 Herdr 管理，**禁止直接修改**。新增 Solco Extension 必须独立、仅观察、不接管 Herdr 生命周期/Prompt。

## 1. 架构与真值

```text
Herdr-owned Pane → 同一 OMP TUI / Extension
                           │ 非阻塞 event observer
                           ▼
Solco existing apphook Unix socket (user-only 0600)
                           │ typed, bounded chat stream events
                           ▼
nativeui ChatStream overlay (Pane + Provider + Session Path + Epoch + Seq fence)
                           │ provisional projection only
                           ▼
existing ChatBridgeState / chat:updated（全量） + chat:delta（尾部 3 回合）
                           ▼
                 WebView Chat / mobile shared page
                           ▲
Provider native JSONL → LiveDecoder/LivePump → canonical reconciliation
```

权属：Herdr 继续负责进程、PTY、Pane、Agent session identity 和生命周期；Solco 的事件缓冲仅为**可丢弃的 UI 读模型**。不在客户端保存新的对话历史，不让扩展写聊天转录，不经扩展回复审批。

## 2. 事件契约（v1）

通过现有 AppHook socket 的 JSON-RPC notification `chat.stream` 发送单行 JSON：

```json
{"method":"chat.stream","params":{"agent":"omp","pane_id":"p","session_path":"/absolute/session.jsonl","epoch":"one-process-id","seq":1,"kind":"text_delta","message_id":"m1","delta":"hello"}}
```

- `agent` 固定为 `omp`；`pane_id` 取 `HERDR_PANE_ID`；`session_path` 取 OMP `ctx.sessionManager.getSessionFile()`，必须是绝对路径。
- `epoch` 是扩展进程唯一串，`seq` 是该进程严格递增序号，`message_id` 限定当前消息。第一批支持 `message_start`、`text_delta`、`thinking_delta`、`tool_start`、`tool_end`、`message_end`。
- 每帧/字段限长；单条事件错乱不得污染别的 Pane/Session。重连允许重复帧，必须幂等；不同 SessionPath 不能替代当前绑定。扩展不存在时，旧 JSONL Chat 流继续正常工作。
- Extension 对模型事件**不 await socket 写入**；丢帧后只影响临时视图，原始转录仍可恢复。严禁写模型内容到 app log。

## 3. 任务清单

| ID | 目标与 owner | 验收标准 | 状态 |
|---|---|---|---|
| SC-01 | `internal/apphook`：严谨解析、大小限制、独立 `OnChatStream` 回调 | Socket 接入、参数限制、超大帧拒绝、独立分流均有单测；序号幂等在 chatstream | 代码完成 / 实机待验收 |
| SC-02 | `scripts/omp-chat-stream.ts`：独立、可按需安装的 OMP 扩展 | `bun test scripts/omp-chat-stream.test.ts` 模拟真实 Unix Socket / OMP 事件通过；未安装到用户扩展目录 | 代码完成 / 实机待验收 |
| SC-03 | `internal/chatstream` + `internal/nativeui`：有界 overlay；严格 session 身份；优先给现有 ChatBridge 提供文本/思考增量与工具状态 | Go 单测验证跨 Pane/Session 过滤、序号/epoch 去重、工具阶段、转录回补；WebView 仍走现有 120ms 全量投影 | 代码完成 / 实机待验收 |
| SC-04 | WebView 优化：独立 `chat:delta`、有序尾回合 suffix patch / 50ms 合并 | Go 端只传尾部至多 3 回合，且从最后实际交付的 Snapshot/事件核对历史前缀与非回合元数据；窗口滑动/状态改变自动退回完整快照。页面按 `baseSeq + conversationId` 严格验序；手机暂未发 delta | 代码完成 / 性能实测待补 |
| SC-05 | 扩展安装/Doctor、CLI 兼容窗口、真实隔离 Herdr+OMP 演练 | 独立 `--check/--install/--uninstall` 通过临时 HOME 测试；`scripts/check-omp-chat-stream-load.py` 在 OMP 18.16.0 上通过隔离 HOME 的 `-e` RPC ready 加载验证，无模型请求。尚无真实 Herdr/TUI 文本输出与 UI 现场验收 | 部分完成 |
| SC-06 | Pi 扩展复用 / mobile Host delta 事件 | 桌面移动端共用事件模型且身份与回放一致 | 待后续 |

## 4. 风险与护栏

1. **转录与原生事件竞态**：永远以会话文件为提交真值；增量只是 overlay。消息完成后等真值包含对应内容再清除，不能闪白，也不能重复追加历史行。
2. **同一 Agent 双视图**：只 attach 到当前 Herdr identity 对应的 pane/session；切换清空状态，禁止同名 Agent 或同 cwd 推测身份。
3. **事件风暴/丢帧**：IPC 帧、delta 和 overlay 均设硬限制，独立 coalescing。任何源序号空洞都立即让临时文本失同步并回退 Provider JSONL，绝不跨丢失 Token 继续拼接。下一条 `message_start` 可从新回合恢复；不得通过每个 Token 触发网络/RPC 全量读取。
4. **隐私**：文本只在用户级 Unix socket 内短暂传输；日志只写事件类型/统计，永不写内容。
5. **兼容**：Extension 可缺失、socket 不可用时，回退现有 JSONL；安装不篡改 Herdr 提供的 extension。
6. **移动端**：`frontend/src` 是桌面手机唯一 Chat 页面；后续 frontend 变更必须走同步与检查，不允许移动端自行维护另一版 UI。

## 5. 验收门槛

- 单测：增量拼接、out-of-order、epoch 重启、跨 pane/session 拒绝、字节上限、断流后的重放、旧 JSONL 路径不回退。
- `GOTOOLCHAIN=auto go test ./internal/apphook ./internal/chatstream ./internal/nativeui`；`GOTOOLCHAIN=auto go test ./...`；`GOTOOLCHAIN=auto go tool mygo build`；`git diff --check`。
- 真实隔离验证：在**临时 HOME + Herdr socket + scratch repo**启动 OMP，观察流式文本和工具生命周期；把 Chat/Terminal 交替、发送/中断/重启做一遍，验证始终只有一个 OMP。
- 当前工作树存在并行 Assistant 改动，**不得 reset、覆盖、顺带提交**。若全量构建因并行修改失败，要记录为阻塞而不是声称验收通过。
- 插件显式启用（**不会改写由 Herdr 管理的** `herdr-omp-agent-state.ts`）：在仓库根运行 `scripts/install-omp-chat-stream.sh --check|--install|--uninstall`。安装拒绝接管外来文件/符号链接，幂等、原子写入；随后**新开的** Herdr OMP 会话加载扩展。不要重启用户当前运行中的任务来验证。首次真实验收前不自动安装。
- `chat:delta` 为展示通道；MyGo 事件有序但重放/网络切换可能造成缺帧，TS reducer 必须精确匹配 `baseSeq`，任何不一致都从 canonical Snapshot 重新对齐。React 端统一同步 watermark，防止 Promise 和事件乱序。
- 事件 Patch 仅更新 Turn suffix；若元数据/交互/队列变化，仍发原来的 `chat:updated` 完整状态，保持跨终端 Chat 语义一致。

## 6. 逐批落地记录

- 2026-11-04：审计现有实时尾读与 MyGo Bridge；确认采用独立 Extension、既有 apphook transport 和只读临时投影。
- 2026-11-04：落地 `internal/chatstream` 的 bounded Buffer 与真值对账、`apphook chat.stream` IPC、`nativeui.handleAppChatStream` 严格会话围栏及 ChatBridge 临时 Turn，独立 `scripts/omp-chat-stream.ts` 扩展（与 Herdr 扩展并存）。定向 Go 测试和 Bun IPC 模拟测试全部通过。
- 2026-11-04：`GOTOOLCHAIN=auto go test ./...` 日志显示 Go 包通过（其中 nativeui ~139s）；`GOTOOLCHAIN=auto go tool mygo vet .` 退出码 0；`GOTOOLCHAIN=auto go tool mygo build` 已生成并签名 `build/darwin-arm64/Solco.app`。Bun socket 测试覆盖了 1030 个 Emoji 的分帧无损。**未做隔离 Herdr + 真实 OMP UI 验收，未安装扩展，未部署到用户正在运行的 App。**
- 2026-11-04（续）：SC-04 加入 `internal/nativeui/chat_delta.go` 的 50ms 合并 `chat:delta`、最多 3 个 Turn 的 suffix patch；`frontend/src/chat-delta.ts` 按序号/会话合并，缺帧用 `chat.snapshot()` 纠错。Go 定向测试、Bun reducer 测试、Frontend typecheck/build 通过。`go tool mygo generate` 重建类型客户端；共享页面已 `build:mobile` 并同步到 `solco-mobile/src/generated/chat-page.ts`，`check:chat-page` 通过。
- 2026-11-04（续验证）：`GOTOOLCHAIN=auto go test ./...` 全量日志无 FAIL，`GOTOOLCHAIN=auto go tool mygo vet .` 与最新 `go tool mygo build` 通过；产物 `build/darwin-arm64/Solco.app`（39.5MB）。手机 `pnpm typecheck` 通过，`pnpm test` 为 592 passed / 2 skipped。独立 Installer HOME 隔离测试、OMP Bun socket+Emoji 测试、前端 ChatDelta reducer 测试通过。**未进行真实 Herdr/OMP 同进程/屏幕验收，不能认定完整验收通过。**
- 2026-11-04（续）：SC-05 添加 `scripts/install-omp-chat-stream.sh` 显式安装/诊断/卸载和隔离 HOME 契约测试，不干扰 Herdr 自有 OMP Extension。未在用户 HOME 安装，未覆盖正在使用的 App。
- 2026-11-04（SC-04/05 加固）：发现两类真实危险路径：`chatstream.Buffer` 在 Extension 队列溢出导致 seq 缺口后会继续拼接错误的 Token；长对话转录窗口向前滑动时，`chatDeltaPatch` 只替换末尾三回合，会把旧前缀残留在 UI。本次修复：缺口立即标记 `Desynced`/丢弃临时文本、唤醒 JSONL 真值尾读，下一消息重建；只有已交付完整状态的不可变前缀与元数据未变时才允许 suffix Patch，否则发 `chat:updated`。工具结束也要等原生转录实际出现完成结果才能退卡。Go 定向回归新增掉帧、工具真值、滚动窗口、元数据及独立 Socket→ChatBridge 投影测试。
- 2026-11-04（SC-05 独立实测）：本机 OMP 18.16.0 用临时 HOME / 虚拟 Pane / 不可达的临时 Socket / 伪 API Key，在无模型请求下通过 `--mode=rpc --no-ui --no-extensions -e scripts/omp-chat-stream.ts` 的官方加载器，返回 `ready`、退出 0；命令已固化于 `scripts/check-omp-chat-stream-load.py`。**只证明真实 CLI 能加载 Extension，不是 Herdr TUI 单进程 Token 输出验收；未安装到用户 HOME。**
- 2026-11-04（加固验收）：`go test ./...` 全量退出 0；新增 Go 定向回归通过，`go tool mygo vet .` 通过，`go tool mygo build` 编译并签名 `build/darwin-arm64/Solco.app`；官方 OMP Extension 加载 Smoke、Bun Socket、隔离 HOME 安装脚本测试均通过。**未运行真实 Herdr TUI 的逐 Token 现场验收，故 SC-05 仍为部分完成。**
- 下一批：SC-05 隔离 Herdr/OMP 真正 TUI 同进程验证（逐字延时、遗漏帧、会话/Pane 切换、工具状态与中断）、Doctor UI/自动安装的产品决策；SC-06 手机 Host 对等流事件和 Pi 复用；性能基准与可视化稳定性测试。
