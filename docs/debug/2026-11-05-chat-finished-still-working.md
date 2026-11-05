# 2026-11-05 Chat 停止按钮与流式光点不收敛：取证修复

- 状态：**修复已部署**。红绿单测、真实隔离 Herdr/OMP/Pi TUI、Go 全量回归、完整 macOS 构建/覆盖安装与 OMP/Pi Extension 更新通过；旧进程原生 run-end 与 Android 逐 Token 视觉验收仍属于单独门禁。
- 循环：第 1 轮（2026-11-05）
- 范围：Solco 桌面与 Android 共同 Chat 页面，Provider OMP/Pi。原有 Herdr daemon/Agent 不停止；原转录不可变。

## 现象与影响

用户报告文字已经全部输出，没有新 Token，Composer 持续显示 **Stop**，助手消息最后的白色光点持续闪烁。共享前端 `frontend/src/App.tsx` 的 `working = disposition === "queue"` 同时驱动 Stop 和 tail 的 `live` 指示，所以不是两个独立 CSS 问题。桌面 `chatDispositionNow()` 读 AgentCard RuntimePhase，手机 `toPageState()` 把 Host `agentStatus=working` 映射为 queue。

## Failing-before 原始证据

1. `scripts/omp-chat-stream.ts` 仅转发 `message_end`（一条 assistant 消息边界），没有转发 Provider 的 **`agent_end`**（整个 prompt、工具链都结束）。真实 OMP/Pi 第一次 Assistant message_end 后**还有** tool_start/tool_end 和第二条 assistant，直接使用 message_end 结束会提前停止。
2. `bun test scripts/omp-chat-stream.test.ts` 失败：实际 IPC 帧 9 个，预期多一个 run-level agent_end。
3. `go test ./internal/nativeui -run TestFinalAgentTurnRetiresStaleSolcoWorkingStatus` 失败：`agent_end` 后仍然 status="working"、hook="working"，Composer 停留 Stop。
4. `go test ./internal/chatstream -run TestAgentEndMeansWholeTurnCompleteNotOneMessage` 不编译，Buffer 缺少 `Completed`；Remote Host 没有完成状态计算。
5. 新增缺帧测试 `TestAgentEndRecoversLostMessageBoundaryWithoutLyingAboutText` 首次失败：真实 run-end 在缺少 message_end 或 Token 丢帧后不能清理 working。
6. 新增 OSC 测试 `TestFinalAgentTurnRetiresStaleOSCProgramWorking` 首次失败：旧 OSC 7501 working 比 Solco Hook idle 优先，已经停止仍显示 `disposition=queue`。
7. 原型有明确优先级 `OSC 7501 > Solco 自有 Hook > Herdr spool`；**没有心跳和固定超时可以合法推断任务结束**。

## 根因

Solco 的独立 Companion 缺失 provider-owned run-completion 事件；已结束的消息不意味着整个 Agent 回合结束，Herdr 的较低优先级 idle 也不能越过残留的 Solco/OSC working。流式传输缺帧时丢失了必要的消息 identity，导致无法确认真实 run end。

## 修复

- 唯一源 `scripts/omp-chat-stream.ts` 添加真实 Pi/OMP `agent_end` 观察器，沿既有私有 Unix Socket 递增 `epoch/seq/message_id` 发送，不改 Agent、Herdr 运行时与转录文件。
- `internal/chatstream.Buffer` 单独保存 `Completed`，`message_end` **绝不**提前置位；Agent 运行结束可以在中途 `message_end` 或工具通知丢失时仍证明终结。丢失的内容继续禁止显示，且只保留原 MessageID 作身份校验，不能凭 SessionPath 以外的事件误完成。
- 桌面严格匹配 **Provider + Pane + SessionPath + Epoch + MessageID** 后，以真实 `agent_end` 完成高优先级本地 Hook；只清理 OSC 的 transient working/blocked，保留已经 `done/error` 的终态记录。ChatBridge 状态随之从 queue→ready，末消息 `live:false`，下一次 message_start 又会 working。
- 手机 Host 对相同 Agent/Pane/Path 的完成状态出 **全量 `chat` 帧**（不能仅出 `chat_delta`，否则按钮状态不会更新）。Host 的 `PromptAgent` 仍由 Herdr 原生 RPC 决定是否可发送；成功新请求撤销此前 run-end，绝不绕过 busy 拒绝。
- Pi 与 OMP 统一协议、原有 Doctor 安装入口不变，`frontend/src` 不动，无需复制第二份 Chat UI。

## Passing-after 证据

- Go Buffer 正常完成、缺失 message_end、缺帧、跨 Provider/Pane/Session、重复事件的定向回归通过。
- NativeUI `TestFinalAgentTurnRetiresStaleSolcoWorkingStatus`：之前 `disposition=queue && live=true`，run end 后 `ready && live=false`；`TestFinalAgentTurnRetiresStaleOSCProgramWorking` 通过。
- Host 认证 Pi WebSocket：完成后 `t=chat` 全帧、`agentStatus=idle` 且末 turn `live=false`，并保持严格身份围栏。
- Bun 扩展 IPC 测试通过。
- 真实 OMP/Pi RPC + loopback 假模型各 10 帧含 `agent_end`，真 read 工具与 3 段文本流；真实 Herdr OMP/Pi TUI 各连续两回合 **16 帧**（含每次 run 的 agent_end），6 段 Token，真实工具与 JSONL 对齐，没启动用户活 Agent 的第二实例。
- `GOTOOLCHAIN=auto go test ./...` 退出 **0**；`GOTOOLCHAIN=auto go tool mygo vet .` 退出 **0**；`scripts/build-complete-macos.sh` 退出 **0**（签名 App、通用 hostd、DMG）。
- 官方 `solco-install` 7/7 通过，`~/Applications/Solco.app` 版本 **0.12.2** 完成覆盖、固定身份签名并成功启动（PID **60702**）；旧包备份 `~/Applications/.Solco-before-composer-fix-20261105.app` 签名验证通过。没有停止 Herdr daemon/Agent。
- Solco-owned OMP/Pi Companion 真实用户目录 Doctor 均从 `outdated` → `current`，`~/.omp/agent/extensions/solco-chat-stream.ts` 和 `~/.pi/agent/extensions/solco-chat-stream.ts` 均可找到 `pi.on("agent_end", ...)`；Herdr 原扩展未改，旧运行 Agent 不被重启。
- Android 无 APK 源码/共享 WebView 页面变更，不重装已配对 Release；手机最新源码 `pnpm typecheck`、`pnpm test`（610 passed / 2 skipped）、`pnpm check:fixtures`（187 synced）与 `pnpm check:chat-page` 均通过。
- `TestVerifiedAgentEndRetiresOnlyInFlightOSCAndKeepsCompletedRecords` 证明真实 run-end 只清过期 OSC working/blocked、保留 done；`TestAgentEndRecoversLostMessageBoundaryWithoutLyingAboutText` 验证缺帧不阻止真实结束、但不伪造流式文本。
- `go test -race ./internal/agentstatus ./internal/chatstream ./internal/nativeui ./internal/remotehost -run 'Test(AgentEnd|VerifiedAgentEnd|FinalAgentTurn|PiChatStream)' -count=1` 退出 0；手机仍经 ADB 连接，Solco 进程 PID 18107 存活，但当时用户前台切换至另一 App，因此**没有强制切回手机**、不将旧进程的可视化收尾标记为实机验收。

## 遗留门槛

- **已运行的旧 OMP/Pi 进程不动态加载更新后的 Extension**。安装新 App + 更新文件可让新启动的 Agent 使用 `agent_end`；不能在旧进程上凭空制造完成事件。不可擅自停止或重启用户 Agent。
- 本轮按 evidence-loop 只使用隔离 Herdr 沙箱做回合级测试；Android 真机 UI 流式结束须使用下一次新启动的 OMP/Pi 会话或严格隔离的真机连接方案，不能对当前用户活 Agent 发送测试提示词。
