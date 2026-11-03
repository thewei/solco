# chat-view-revert-on-return 取证循环
- 状态: 已修复 + 已复验关闭（第 1 轮循环完成；同沙箱 failing-before/passing-after 截图+trace 双向实锤；主树 `go test ./...` exit 0——与并行批次同树全绿；`go tool mygo vet .` 干净；git diff --check 干净）
## 现象（用户原话 + 具体操作路径）
用户：agent 固定在 Chat View 后，切换页面/切到其他内页，再回到这个 Agent 的 pane 时，它又切回 Terminal。要求检查所有决定 pane surface 的地方，把逻辑聚拢到一处，避免莫名变化。
## 环境（沙箱路径、构建产物路径、pid、trace 开关）
- 沙箱: ~/.cache/solco-chat-revert/（home/work/logs/app；scratch 项目 work/demo git 仓库，workspace w1，pi demo-agent @ w1:p1）
- 沙箱 app: $SB/SolcoSandbox.app（CFBundleIdentifier=com.whstudio.solco.sandbox；实例已按收尾规程关闭）
- herdr socket: $SB/home/.config/herdr/herdr.sock（**Solco 自己的 socket 从 HOME 派生（SocketPath=HOME/.config/herdr/herdr.sock），HERDR_SOCKET_PATH 只传给 spawn 的子进程——与 11-02 进度文件里写的 env 用法不同**）
- 日志: $SB/home/Library/Logs/Solco/solco.log（grep "surface trace"）
- 驱动: cua-driver 一次性 CLI 不保快照缓存（screenshot_context_missing/stale token）→ 用 `cua-driver mcp` 持久 stdio 会话一次脚本跑完序列；**pgrep -f SolcoSandbox 会撞见并行会话的同名沙箱——必须按启动路径/ps 甄别 pid，本轮曾把点击打进并行会话的 solco-launch-flash 实例（仅导航级噪音，无破坏动作）**
## 证据（append-only，带时间戳）
- 2026-11-03 09:41 沙箱 failing-before（cua-driver 驱动，surface trace + 截图）：
  1. 点击 pane 卡头的 Chat 切换钮 → showSurface(chat) → memo(p1)=chat；截图 fb-1-chat.png：Chat 邀请态。
  2. Files view 页签 → /files。
  3. Terminal view 首页签 → `showViewTerminal caller=sidebar.go:194` → showSurface(terminal from=chat) → **memo(p1)=terminal（记忆改写）**；fb-3-back.png：pi TUI。
  4. agent 行 → memo(p1)=terminal（surface_header.go:84）→ 再落 Terminal；fb-4-agentrow.png。**污染持久：按记忆恢复的入口也回不去。**
- 2026-11-03 09:43 单测红（隔离 worktree HEAD+探针）：TestHomeTabKeepsAPinnedChatPaneOnChat / TestFilesTabExitKeepsAPinnedChatPaneOnChat 双红，trace 与沙箱指纹一致。
- 2026-11-03 09:58 passing-after 中途发现第二处同型缺陷：openChat 无会话回落 **只写 memo(p1)=terminal（chat_live.go:93 旧 92-94）、连 Terminal 都不落**（trace 无 showSurface）——纯记忆污染。
- 2026-11-03 10:00 最终 passing-after（修复产物，同一沙箱，pid 71119）：
  1. Chat 切换钮 → memo(p1)=chat；fb→pa3-1。
  2. Files → （无 trace，正确）。
  3. Terminal view 首页签 → `showSurface chat from=chat caller=workspace.go:205`（landSelectedPane→showPaneSurface）→ **保持 Chat**，无任何 memo terminal；pa3-3-back.png：邀请态仍在。
  4. agent 行 → 新邀请分支（chat_live.go:93 现为 Chat 记账）→ **仍 Chat**；pa3-4-agentrow.png。
  - 单测全绿（含 6 个新回归 + 既有 pane_surface/menu/sidebar/right_panel/navigation 套件）；worktree `go test ./...` exit 0。
## 当前假设（一句话，hunt 契约）
（已裁决，见根因）
## 根因（确认后一句话 + file:line）
home 家族入口（首页签 sidebar.go:194、⌘1/Go 菜单 shell.go:932、⌘P Workspace command_center_ui.go:489、Files/Hosts/Automations 页签 toggle 退出、Changes toggle 退出 titlebar.go:328）经 showViewTerminal → showSurface(Terminal) 无条件 rememberPaneSurface(selectedPaneID, terminal)（workspace.go:167），把 Chat 记忆改写成 Terminal；此后一切按记忆恢复的入口（agent 行、selection sync、goToPane）都持久落 Terminal。恢复机制没坏，坏的是 home 家族的落地面 + 无条件记忆回写。同型缺陷二：openChat 无会话回落的 memo 回写（chat_live.go 旧 92-94）不改 surface 只改记忆。
## 修复（聚拢后的 owner 地图）
- `showPaneSurface(paneID)`（workspace.go）：「去这个 pane」唯一落地面——按 panePrefersChat 落 Chat/Terminal，**永不写记忆**。
- `landSelectedPane()`（workspace.go）：「回 Workspace 首页」唯一 owner（首页签/⌘1/Go 菜单/⌘P Workspace/页签 toggle 退出/Changes toggle 退出全走它）= holdCommitNavigation + 路由 + showPaneSurface(selectedPaneID)。
- openChat（chat_live.go）：Chat 记账只在真正落 Chat 的三个分支；无会话回落 = 偏好 Chat → 邀请态（与卡头切换钮一致），否则 showPaneSurface 落 Terminal + 提示，不再改写记忆。
- goToPane（history_live.go）、external_reveal（external_reveal.go）改走 showPaneSurface（去重散落的同型三元式）。
- 死代码 showPane（history_continue.go）删除。
- 显式选择仍写记忆（不变）：卡头 Terminal⇄Chat 切换钮（showSurface）、Open Terminal 菜单/按钮、openAgentTerminal、openAgentFromSidebar else、completeAgentOpen。
## 已试过（每条：假设 → 探针 → 结果）
- 静态读全决策链 → 假设成立，转实机取证。
- surface trace 探针（showSurface/rememberPaneSurface/syncWorkspaceForSelection/showViewTerminal，runtime.Caller）→ failing-before 指纹实锤。
- cua-driver 一次性 CLI → 快照缓存不跨调用（screenshot_context_missing）→ 改持久 mcp stdio 会话。
## 下一步（具体的下一个动作）
- 主树全量 go test（后台跑，并行批次同树）；收尾 LESSONS.md。
