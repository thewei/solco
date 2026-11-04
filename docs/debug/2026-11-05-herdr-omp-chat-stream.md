# Herdr + OMP Chat 原生流式读取取证循环（2026-11-05）

- 状态：**隔离真实 Herdr TUI 复验关闭；Solco WebView GUI 验收未关闭**
- 循环：第 1 轮
- 归属：`docs/chat-view-streaming-plan-2026-11-04.md` SC-05
- 原则：不接触用户活 Herdr、OPA 登录数据或正在运行的 OMP；不发外部模型请求

## 现象与目标

目标是证明 Solco Companion Extension 位于 Herdr 真正管理的 OMP **同一个 TUI/Pane/Agent Session** 内，能把原生文本 Token、工具生命周期实时发到 Solco 的 Unix Socket；不另起 OMP RPC 读取进程。

此前只证实了独立 OMP RPC 子进程经 Extension 的 9 帧事件，尚不足以证明 Herdr 所有权约束。

## 隔离环境

- 设备：macOS arm64，Herdr **0.9.5**（protocol 22），OMP **18.16.0**。
- 创建唯一短路径 `/tmp/sh-*/home` 与 `/tmp/sh-*/demo`；Herdr Socket = 沙箱 HOME 下 `.config/herdr/herdr.sock`，配置 `HERDR_CONFIG_PATH` 指定沙箱配置；`git init` 沙箱仓库。
- OMP `models.json` 仅包含本地 `127.0.0.1` SSE OpenAI-compatible fake 模型；两个请求场景：真正执行 `read` 工具（仅读沙箱文本）与正常分三段回答。
- Solco 事件 Socket 位于同一沙箱的 `events.sock`；Companion Extension 通过 `install-omp-chat-stream.sh --install` **仅安装到沙箱 HOME**，从不替换 Herdr 自有 Extension。
- 完整流程脚本：`python3 scripts/check-herdr-omp-chat-stream.py`；自动停止自己创建的 Herdr daemon，关闭 Socket/HTTP server 并删除沙箱。绝不对用户活 Herdr 的 Socket/Panes 调用 API。

## 失败前证据（在同一类隔离环境）

1. 最初沙箱目录沿用 macOS 长 `$TMPDIR`，`herdr server` 退出：`local socket name length exceeds capacity of sun_path of sockaddr_un`；短 `/tmp` 修正后独立 Herdr daemon 能正常监听。
2. Herdr `agent.start --kind omp` 首次报 `timeout`，在沙箱 Pane 的终端原样观察到：`Bun runtime must be >= 1.4.4 (found v1.4.0)`。开发 Shell 的 Bun 为 1.4.4，但 Herdr 创建登录 zsh 后 PATH 先解析到了旧版 Homebrew Bun。
3. 修正 PATH 后 Herdr 可启动 OMP，但第一次 `agent.prompt --wait` 报 `agent_prompt_stalled`；读取沙箱 Pane 显示 `Setup step 1 of 5 / Sign in to your providers / Search: Say hello`。Prompt 送入了 OMP 首次安装向导的搜索框，模型完全没收到。
4. 仅优先放开发环境的另一处 Bun 可令 OMP TUI 正常显示，却使 Herdr agent detector 等待超时，`agent list` 返回空。把 OMP 安装目录下经验证的 Bun 放在沙箱 PATH 前面后稳定获得 Herdr `interactive_ready` 事实。
5. `herdr agent.prompt --wait` 使用本地极快模型时可以报告 `agent_prompt_stalled`，但 `pane.read` 已包含 `Hello there!`，Extension Socket 也已收到完整 Token。等待命令以 Herdr 的工作状态采样为准，不是完成验证唯一真值；复验改用普通 `agent.prompt`，并对照 Pane/Socket/模型请求。

## 修复与通过后的证据

修改范围只在隔离验收脚本，**不修改生产 Herdr 或 OMP 配置**：

- 沙箱内 OMP 先 `config set startup.setupWizard false`，并在沙箱 `.zshenv/.zshrc/.zprofile` 把 OMP 的 Bun 前置到 PATH。
- 用 Herdr CLI 启动 `agent.start --kind omp`，之后对**同一个 pane**调用两次 `agent.prompt`。
- 第一轮 Extension 依次上报 `message_start, message_end, tool_start, tool_end, message_start, text_delta×3, message_end`；第二轮同会话上报 `message_start, text_delta×3, message_end`。
- **总计 14 帧、6 个 Token delta**；严格递增序号，epoch/Pane/session JSONL 不变，真实 `read` 工具调用关联 ID 一致且结束无错误。
- Herdr `agent list` 中**始终只有 1 个 Agent**；`pane.read` 显示真实 TUI 结果 `Hello there!`。
- 单一脚本连续在两个新沙箱执行，均 `PASS`；模型只连接 127.0.0.1，没有外部付费请求。
- 配套的 Go Socket→ChatBridge 单测和丢帧恢复测试见 `internal/nativeui/chat_stream_test.go` / `internal/chatstream/stream_test.go`。

## 根因及长期教训

- OMP TUI 的首次启动与本地模型的网络可用性是两回事：首次配置向导会合法占用输入，不能把 `agent.prompt` 返回作为模型开始工作的证明。
- `#!/usr/bin/env bun` 随 Herdr 创建的登录 Shell 重新解析 PATH。开发 Shell 的 Bun 版本**不代表 Pane 内 Bun 版本**；同样，其他 Bun 包装器不一定满足 Herdr 进程识别。沙箱须对真实登录环境做前置校验。
- 流式读取本体来自当前 TUI 的 Extension，Herdr 管唯一 Agent 进程；不需要启动 `omp --mode rpc` 再读另一份会话。

## 尚未通过的门槛与下一步

- **Solco 真正的 WebView 视觉验收**：当前脚本只在真实 Herdr TUI + Extension + 事件 Socket 验证；尚未启用本 App 的 WebView 输入/切换/渲染，不能对用户宣称 Chat 流式 UI 全面验收。
- **用户 HOME 安装**：产品化 Doctor 已在 Settings → Agents → OMP live text 实现（App 自带编译期嵌入的扩展源码，只有用户点击 Install/Update 才落盘）。本次仍只安装沙箱副本，未对用户 HOME 写入，不能把 Doctor 的 UI 单测当作实际用户安装验收。
- 后续：独立 Solco App 沙箱与 Herdr/TUI 同环境验收，模拟丢帧/中断/重启/切 Pane，确保仅一进程与 JSONL 真值回补；再推进 SC-06 Mobile Host 对等。
