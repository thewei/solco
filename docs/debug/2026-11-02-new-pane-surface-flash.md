# new-pane-surface-flash 取证循环

- 状态: 已修复 + 已复验关闭（第 2 轮循环完成；沙箱双路径 passing-after；部署用户实例待并行批次让出工作树）
- 循环: 第 2 轮
- 更新: 2026-11-02 22:55

## 现象（用户原话）

「New Pane 它打开 Terminal 的时候还有些问题：打开时会先识别为对应的 Agent, 显示的 Agent 界面，然后过段时间提示没有 Agent 会话，再跳回 Terminal 界面。」

优化目标（用户拍板）：
1. 打开 Terminal → 从第一帧起就是 Terminal，绝不闪 Chat/Agent 界面。
2. 打开 Agent → 从第一帧起就是 Agent（Chat 界面）；启动放后台，前台总是显示 Chat 界面。延时监测可以接受。
3. 结果需要 computer use 实机验证（failing-before / passing-after）。

## 环境

- 沙箱: `~/.cache/solco-pane-repro/`（home/work/logs，scratch 项目 `work/pane-demo` git 仓库）
- herdr-config.toml: `$SB/herdr-config.toml`（data_dir → $SB/home/.herdr-data）
- 沙箱 app: `$SB/SolcoSandbox.app`（CFBundleIdentifier=com.whstudio.solco.sandbox——避开与用户活实例的 MyGo single-instance 锁；锁键=bundle id+名字，/tmp socket）
- 沙箱日志: `$SB/home/Library/Logs/SolcoSandbox/solco.log`（目录名随 bundle 名）
- 启动 env: `HOME=$SB/home HERDR_SOCKET_PATH=$SB/herdr.sock HERDR_CONFIG_PATH=$SB/herdr-config.toml HERDR_BIN_PATH=$HOME/.local/bin/herdr`（复现 B 追加 `SOLCO_LOOP_KIND=pi`）
- 临时 harness: window_actions.go NewTabFromMenu 的 `SOLCO_LOOP_KIND` env（与 surface trace 一起，修复落地前删除）
- trace 探针点: workspace.go（showSurface / syncWorkspaceForSelection / reconcilePaneSurface / openWorkspaceChat）+ new_tab_agent.go（startNewTab / agentSessionStarted / completeAgentOpen），grep 标记 `surface trace`
- 构建: `GOTOOLCHAIN=auto go tool mygo build` → `build/darwin-arm64/Solco.app`（0.12.0，含探针）

## 证据（append-only）

- 2026-11-02 21:15 用户活实例日志（~/Library/Logs/Solco/solco.log）：新建 pane（w5M:p1D/p1E/p1F）创建后 ~3–4 s 出现**两次**额外 `attach native terminal`（间隔 200–800 ms，`spawned:1 closed:0`）= surface 两次离开又回到 Terminal 的指纹，与用户描述的闪烁吻合。
- 2026-11-02 21:20 用户设置：`agent_view: "chat"`（Settings → General → Default Agent view = Chat 开启）；`pane_surfaces` 混有 terminal/chat 记忆；MRU kinds `[omp, antigravity, terminal, claude-code, pi]`。
- 2026-11-02 21:41 沙箱首次启动即退出(exit 0)：MyGo single-instance 锁与用户活实例相撞 → 沙箱拷贝改 CFBundleIdentifier 解决。
- 2026-11-02 21:44 Repro A（⌘T→Terminal，agent_view=chat）：干净。startNewTab(kind=terminal)→新 pane 单次 attach，syncInvalidCtx pref=false → 落 Terminal，无翻转。**纯 Terminal 路径在沙箱不复现闪烁。**
- 2026-11-02 21:44 MyGo "+" 弹出菜单对合成点击不响应（像素点击命中但菜单不开；Down/Return 被终端 pane 当作 split down 吞掉，日志 "pane action: split down" 为证）→ 加 SOLCO_LOOP_KIND 临时钩子绕过。
- 2026-11-02 21:54 Repro B（⌘T→pi，agent_view=chat）**failing-before 实锤**，时序：
  - 21:54:36.500 startNewTab(kind=pi)（启动事务后台跑）
  - 21:54:36.636 过渡投影落地：Herdr watcher 先把过渡 pane w1:p3 选上（空 pane，落 Terminal）
  - 21:54:40.479 agentSessionStarted pane=w1:p5（事务返回最终 pane；启动自带的投影已含 Agent 卡）
  - 21:54:40.489362 syncInvalidCtx pane=w1:p5 pref=True avail=True → openWorkspaceChat → **Chat 打开**
  - 21:54:40.489614 showSurface(terminal) from=chat —— **`showPane` 的 `showViewTerminal` 在 250μs 后把 Chat 打回 Terminal**
  - 此后无任何 completeAgentOpen（workbench 不再重建）→ pendingAgentOpen 无人消费 → **永远停在 Terminal**（截图 /tmp/pb2-8.png：选中 Tab=Pi、侧栏 pi-96bb04 卡可见、主区是 pi TUI 的 Trust 提示——按用户要求此刻前台应是 Chat）
- 2026-11-02 21:56 用户活实例日志成对 `attach native terminal`（间隔 200–800ms，spawned:1 closed:0）= 振荡另一指纹：重建晚来/投影抖动时 Chat↔Terminal 来回，中间夹 Chat 页空会话邀请/"Chat needs an Agent pane" toast，即用户看到的「Agent 界面→没有 Agent 会话→跳回 Terminal」。

## 根因（第 1 轮循环，一句话）

`startNewTab` 完成路径调用 `showPane(result.PaneID)`，而 `showPane` = `selectPane` + 硬编码 `showViewTerminal`（history_continue.go:298）：当启动事务自带投影已含 Agent 卡（agent_view=chat）时 `selectPane` 的 surface 同步先开 Chat，`showPane` 又在 250μs 后打回 Terminal；此后 surface 只能靠 pendingAgentOpen/completeAgentOpen 在「下一次 workbench 重建」被纠正——重建不来就永远停在 Terminal（沙箱实锤），重建带着投影抖动来就形成 Chat↔Terminal 振荡（用户实例指纹）。surface 由陈旧的「永远先 Terminal」默认决定，而不是由启动意图决定。

## 已试过

- 静态读 workspace.go / new_tab_agent.go / selection.go / runtime.go / surface_header.go 全决策链 → 无法唯一确定闪烁路径，转实机取证（正确决定：根因在时序，静态读不出）。
- 活实例日志 msg 清点 → 无 surface 切换日志 → 加 trace 探针后一次复现实锤。
- 沙箱 UI 驱动 "+" 菜单（像素点击/键盘）→ MyGo 弹出菜单不吃合成事件 → SOLCO_LOOP_KIND 临时钩子绕过（修复前删除）。

## 修复方案（第 1 轮循环末出的 think-Lightweight 方案，已按此实施）

startNewTab 按**启动意图**落 surface——selectPane 后若同步已落 Chat（卡已在投影里）则不再 showViewTerminal；否则维持现 fallback（pendingAgentOpen）。Terminal 路径不动。

## 修复（第 2 轮）

- `new_tab_agent.go`：`startNewTab` 与 `history_continue.go runContinue` 共用新 helper `landLaunchedPane(paneID)`——selectPane 后 surface 已被同步切到 Chat（启动事务自带 Agent 卡 + Agent view=Chat）就保住 Chat 并推 /workspace 路由；否则维持 showViewTerminal。`showPane` 的硬编码 Terminal 打回被移除（sibling sweep 命中 Continue 同型位，一并修）。
- 回归测试：`new_tab_agent_test.go TestStartNewTabAgentLandsOnChatWhenTheLaunchShipsTheCard`（红：未修代码 surface=terminal；绿：修复后 Chat；红绿均已实际跑）。fixture 教训：Agent 走 `projection.Agents`（reconcileWorkbench 会按它重建 workbench 目录，直接 seed 目录会被抹掉）；`s.runtime=nil`（否则 Manager.Projection 拉真实快照冲掉测试外壳）。
- 临时件全部移除：surface trace（workspace.go/new_tab_agent.go）、SOLCO_LOOP_KIND/SOLCO_LOOP_PROBE 钩子；误删的 `chatUnavailableNotice()` 已恢复（workspace.go 与 HEAD 零 diff）。

## 复验证据（passing-after，cua-driver 实机）

- Agent 路径（⌘T→pi，agent_view=chat）：修复产物上选中 Tab=Pi、侧栏新卡（pi-d2a7b2；最终产物 pi-563075）、主区**第一帧即 Chat 空会话邀请**（"Start the conversation" + Open Terminal），pi 后台启动，全程无 Terminal TUI 闪现（截图 /tmp/pa-0-crop.png、/tmp/pa-8-crop.png、/tmp/final6-crop.png）。
- Terminal 路径（⌘T→Terminal）：新 pane 第一帧即 shell 提示符，单次 attach，无 Chat 闪（截图 /tmp/pt-2-crop.png；最终产物再验 Tab 8/9/10 同样干净）。
- 门禁：隔离 worktree（HEAD+仅本修复 diff，避开并行批次的 automations 半成品）`go test ./...` 全绿、`go tool mygo vet .` 干净、`mygo build` 产物结构验证、`git diff --check` 与 gofmt 干净。

## 遗留

- 用户实例部署未做：工作树里有并行批次的半成品 `internal/nativeui/automations_ui.go`（untracked，引用未定义符号），solco-install 的 vet 关会（正确地）中止。等并行批次收尾后跑 `bash .agents/skills/solco-install/scripts/install.sh`。
