# 2026-11-05 Chat Composer 新会话 Send 无法启用 + 完成后 Stop 不退出：真机取证

- 状态：两个失效路径均有独立 failing-before 和 passing-after 回归；Go 全量/Vet/Race/完整 bundle 已通过，正式安装中；Android ADB USB 仍断连
- 轮次：1
- 测试现场：桌面 Solco PID 60702，CUA 窗口 ID 37033；真机 vivo V2309A，ADB USB。只针对测试工作区“Solco Composer QA”（Herdr workspace w7X），测试目录 /tmp/solco-composer-e2e-f9JUBG/project；用户其他 Pane 不修改。
- Git 基线：main 15593b9，前序 Chat 完成修复仍在工作树未提交；另一 Agent 同时修改 Personal Agent/System Push/文档，不合并其改动。

## 用户症状
1. 某已完成消息的 Composer 持续显示 Stop，白色生成指示器不停闪。
2. 新建会话后 Composer 可输入内容，但发送按钮无法启用。
3. 用户显式授权创建一个测试工作区/会话并操作真机。

## 设备证据
- 10:06+08：ADB 显示 vivo V2309A 已连接；Mac Solco 60702 在运行，Herdr 上还有用户会话，不触碰用户活 Pane。
- 10:14+08：使用真实 Solco UI，在已授权新建的“Solco Composer QA”工作区启动 Pi（Mac CUA AX 输入 `QA verify first message`）。真实 AX 快照 `/tmp/solco-pi-draft-evidence.json` 显示 `AXTextArea Chat prompt` 非空且 `AXButton Send` 为 enabled=true。**因此 Mac Pi 新会话可发送，不能据此复现用户的禁用故障。**
- 10:16+08：点击测试 Pi Send 后，真实 AX `/tmp/solco-qa-after-complete.json` 出现 `Working…` + Stop；后续窗口出现 Thinking 和工具执行，说明 Agent **确实还在运行**，这是正常 Stop。测试 Agent 的模型选择实际为用户 Pi 默认模型而不是计划中的 fake model，因而及时点击**本测试 Pane**的 Stop，停止继续使用模型。未向用户其他 Agent 输入内容。
- 10:20+08：在同一测试工作区中新建 Oh My Pi (OMP) 测试 Tab，真实 AX `/tmp/solco-qa-omp-first.json` 先显示 Start the conversation。输入 `QA check OMP composer state` 后，`/tmp/solco-qa-omp-typed.json` 显示 `Chat prompt` 非空且 `Send.enabled=true`。不提交第二条模型请求。**Mac 侧两个 Provider 的新会话在此测试环境下均可启用 Send；手机或特殊启动状态仍需实机定位。**
- 根因假设（未确认）：手机在无转录文件（`no_conversation`）时只根据 Host 树的 fallbackStatus 计算 `disposition`；如原始状态 unknown/working 且缺乏 first prompt 的特殊允许路径，`frontend/src/App.tsx` 的 `canSend=disposition==='ready' && ...` 会错误禁用第一条。另一候选：真实用户 Pane 的高优先级 OSC/Hook 工作态比 Herdr 完成事实长寿。
- 10:21+08：ADB 设备从列表消失，`adb kill-server` / `adb start-server` 后仍空；macOS USB 设备清单也未列出 vivo，故不可伪称 Android 本轮 QA 通过。
- 10:22+08：新增 `TestComposerDispositionFollowsExactLivePaneNotCachedDirectory` 的 **RED**：`first prompt locked behind a provisional card: loading=false waiting=true disposition=unknown`（Herdr Projection.Agent.Status=idle，但 Workbench AgentCard 的 RuntimePhase 未初始化）。源头 `internal/nativeui/chat_ui.go chatDispositionNow()` 使用 `agentCardForPane()`，它优先返回滞后的 `workbench.directory.All()`，两个事实可能相反。
- 10:23+08：修复 `chatDispositionNow()` 使用 **绑定 Pane 精确匹配**的 `projection.Agents[].Status`（已经经过 Solco/OSC 仲裁），保留无 Pane/无 Agent fail-closed，**GREEN**。单测同时覆盖 stale working card 让 Stop 永不退出、stale idle card 在实际工作时意外启用 Send、Agent 消失的情况。
- 10:25+08：新增 `TestBackgroundAgentCompletionRetiresStaleWorkingBeforeChatOpens` 的 **RED**：`background agent remains stale after its real run ended: working`。源头 `internal/nativeui/chat_stream.go handleAppChatStream()` 只有正好显示该 Agent 的 Chat 时才处理 `agent_end`，后台 Pane 的真实完成事件直接被 early-return 掉，切回 Chat 时仍显示 Stop。
- 10:27+08：新增 UI lane 的**状态与文字分离**：`observeChatRunStatus` 使用真实 `AgentSession` Provider/Pane/转录绝对路径精确绑定的进程 Epoch/MessageID/Seq 围栏，在后台 Pane 也收敛 working→idle；最多 48 个只读暂存 fence，不重建 Herdr 生命周期、不持久化输出；仅当前绑定的 Chat 仍接收文字 Delta。**GREEN**。新增反例：wrong Provider/Pane/Session/Epoch/MessageID 和前一轮迟到的 `agent_end` 均拒绝；下一次新的 `message_start` 重新 working。
- 后续：`go test ./...` + Race + MyGo Vet/完整 App 构建；在 Mac 原 App 先备份后由官方安装脚本 graceful 更新，再以 UI/CUA 在 QA 工作区重验 Pi/OMP first Send；ADB 连接恢复后再进手机 QA 页面。

## 验收及分级
- 定向 `go test ./internal/nativeui -run 'Test(ComposerDispositionFollowsExactLivePaneNotCachedDirectory|BackgroundAgentCompletionRetiresStaleWorkingBeforeChatOpens|FinalAgentTurnRetiresStaleOSCProgramWorking)'` PASS；`go test -race ./internal/nativeui` 定向三例 PASS。真实 App 修复前 AX 证据在 `/tmp/solco-qa-omp-typed.json`、`/tmp/solco-pi-draft-evidence.json`。
- 全量 `GOTOOLCHAIN=auto go test ./...` PASS（exit 0），`go tool mygo vet .` PASS，`scripts/build-complete-macos.sh` PASS（Solco.app 67MB、通用 hostd、22.6MB DMG、App `codesign --verify --deep --strict` PASS）。
- 覆盖用户现用 Mac App 之前已签名备份：`~/Applications/.Solco-before-chat-composer-qa-20261105.app`。官方安装流程正在运行；实际新 App AX 验收待补。
- **Android**：ADB 仍未恢复，不能以 Mac CUA 或 Go mock 冒充 Android 真机复验。

## 安全与清理
- 原有 macOS 用户会话、Herdr 和 Agent 不停止；QA 项目的模型服务原拟使用 localhost，但测试 Pi 被原生配置选择为默认模型，已停止测试任务。
- QA 工作区只是可删除的测试对象；不清除用户的任意现有工作区或会话。清理由 GUI 或经明确授权的测试清理路径完成。
