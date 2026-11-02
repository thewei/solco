# LESSONS — 取证循环自更新记忆

每轮循环结束后追加（见 SKILL.md「自更新」）。格式：`- <日期> <问题> → <教训>（落点）`。
重读时合并重复；超过 40 条时清理已被流程吸收的陈旧条目。

## 工具与编辑

- 2026-11-02 用 edit 工具给函数体加一行日志时误用 `PUT N*:`（块替换），一次毁掉 4 个函数体（workspace.go showSurface/openWorkspaceChat/syncWorkspaceForSelection/reconcilePaneSurface + new_tab_agent.go startNewTab/completeAgentOpen），只能靠此前 read 过的内容手工重建 → 在函数内部插一行用 `PUT >N:`（N 行之后插入）；`PUT N*:` 会替换整个块；收到 auto-repair/syntax 警告立即重读现场再继续，绝不在坏语法上叠第二个 edit。
- 2026-11-02 并行批次的未提交改动（internal/remotehost/chat.go 等 4 个文件）在工作树里 → 动手前 `git status` 记录，全程只读不碰，重建/测试不受它们影响即可。

## 环境与运行时

- 2026-11-02 隔离沙箱配方已验证可行：临时 HOME + `HERDR_SOCKET_PATH`/`HERDR_CONFIG_PATH`/`HERDR_BIN_PATH`，Solco 自行 EnsureRunning 拉起 `herdr server`（internal/herdr/herdr.go:146），本机 herdr 0.9.5（~/.local/bin/herdr）；不需要手工起 daemon。
- 2026-11-02 用户活实例日志在 `~/Library/Logs/Solco/solco.log`（rotate 3×2MiB）；可用 msg：`attach native terminal`、`native terminals synced`（spawned/closed 计数）、`hook session event` 等；**没有 surface 切换日志**——查表面行为必须先加 trace 再复现。
- 2026-11-02 「native terminals synced」的 `spawned:1, closed:0` 连续两次（间隔几百 ms）= surface 两次离开又回到 Terminal 的指纹；这是诊断 surface 闪烁的可复用信号，不用加日志也能从活实例日志粗判。

## 架构事实（pane surface 决策链）

- 2026-11-02 pane 显示 Terminal 还是 Chat 的唯一答案是 `Shell.panePrefersChat(paneID)`（surface_header.go）：先查 `paneSurfaceMemo[paneID]`，无记忆时 `General.AgentChatFirst()`（Settings → General → Agent view）且 `agentCardForPane(paneID)` 命中才 Chat。落点：`syncWorkspaceForSelection`（project/tab/instance 上下文变化）与 `reconcilePaneSurface`（同 tab 内换 pane）。
- 2026-11-02 `agentCardForPane` 两级查找：workbench.directory 按 PaneID → 原始 `projection.Agents` 兜底；所以「是不是 Agent pane」完全跟着 Herdr 投影的 Agents 列表走，投影抖动会直接抖 surface。
- 2026-11-02 Agent 启动链路：`startNewTab`（new_tab_agent.go）→ SessionStarter.StartSession（异步 dispatch）→ `showPane`（先落 Terminal）→ `pendingAgentOpen`（15s patience）→ workbench 重建后 `completeAgentOpen` 见到卡才 `showSurface(Chat)`。**当前形态 = 先 Terminal 后 Chat 的可感知翻转**，用户要求的是前台恒 Chat、启动放后台。
- 2026-11-02 Chat 打不开的提示 toast 来自 `openWorkspaceChat → chatUnavailableNotice`（workspace.go:204 "Chat needs an Agent pane…"）；sourceless pane（有卡无会话）走 Chat 页自己的邀请态，无 toast（rebindWorkspaceChat L284 注释明确「so no toast」）。
- 2026-11-02 设置持久：`pane_surfaces`（pane→terminal/chat 记忆）、`agent_view`（默认视图）在 `~/Library/Application Support/Solco/settings.json`；用户当前 `agent_view: "chat"`。

## 实机驱动（cua-driver）

- 2026-11-02 驱动 Solco 前先读 solco-ui-audit 的坑清单：click 收原始像素、弹层菜单是独立 NSWindow（窗口截图/AX 不可见，用 invoke_menu）、element token 跨快照必失效、终端 pane 常是审计会话自己的 stdin（永不点 Close Pane、不发裸文本）。

## 第 1 轮循环（2026-11-02 new-pane surface 闪烁）

- 2026-11-02 沙箱 app 与用户活实例同时运行会立即退出(exit 0、无日志)：MyGo single-instance 锁键 = bundle id + 名字（/tmp/mygo-<uid>-<sha>.sock）→ 沙箱拷贝 .app 后改 CFBundleIdentifier/CFBundleName 再 ad-hoc 重签即可并存（落点 third_party/mygo/single_instance.go:39）。
- 2026-11-02 沙箱日志目录名跟随 bundle 名（改名 SolcoSandbox 后日志在 `~/Library/Logs/SolcoSandbox/`）→ 找沙箱日志先看 `$SB/home/Library/Logs/<CFBundleName>/`。
- 2026-11-02 MyGo 的 `Button.Menu` 弹出菜单**不吃 cua-driver 合成点击**（像素点击命中按钮但菜单不开；键盘 Down/Return 被焦点所在的终端 pane 当作输入，甚至触发 "pane action: split down"）→ MyGo 菜单类交互改用键盘可达路径（⌘T/⌘K）或临时 env 钩子，别在像素点击上耗超过一轮。
- 2026-11-02 cua-driver CLI 跨调用保留快照上下文必须带同一个 `session` 标签，否则像素点击报 `screenshot_context_missing`；每次点击前重新 get_window_state——Tab chip 宽度会变，旧坐标会点进 × 或 chip。
- 2026-11-02 后台 GUI 进程用 bash `&` 启动会随调用结束被 SIGHUP 杀掉（app 静默消失、无日志）→ GUI 实例一律用命名服务（proc://）方式启动。
- 2026-11-02（架构记忆）Agent 启动事务返回的 pane 与 watcher 过渡投影的 pane 可能不同（沙箱 w1:p3 → w1:p5）：Herdr 先建 tab/pane（watcher 事件先到并被 adopt），事务完成后再报最终 pane；「启动后立即落 surface」的逻辑必须以事务返回的 pane 为准（落点 new_tab_agent.go startNewTab）。
- 2026-11-02（根因记忆）`showPane` = selectPane + 硬编码 showViewTerminal（history_continue.go:298）；selection 同步（syncWorkspaceForSelection/reconcilePaneSurface）可能在 selectPane 内就开出 Chat——任何在 selectPane 之后无条件调 showViewTerminal 的路径都会 Chat↔Terminal 振荡；completeAgentOpen 只在 workbench 重建时求值，投影不更新就永不运行（挂起的 pendingAgentOpen 无人消费，沙箱实锤）。

## 第 2 轮循环（2026-11-02 修复与复验）


- 2026-11-02 nativeui 测试外壳**不 hermetic**：tabBarShell 的 Shell 会从磁盘按 HOME 恢复持久会话状态（用户实例的 w5M:p1D / 沙箱的 w1:p4 都曾泄进测试断言），且 `s.runtime` 非 nil 时 `startNewTab` 内的 `Manager.Projection` 会拉真实（空）快照冲掉 fixture → 测试里显式 `s.runtime=nil`、`clearLocalSelection`、清 paneSurfaceMemo，断言只读本测试的输入。
- 2026-11-02 给 workbench 直接 seed 卡片会被 `reconcileWorkbench` 按 `projection.Agents` 重建抹掉 → 涉及 Agent 卡的 fixture 一律走 `projection.Agents`（agentCardForPane 的兜底查找正是生产形态）。
- 2026-11-02 红绿流程不要动共享工作树：未修版验证用「复制改动文件 → python 还原 hunk → 跑红 → 恢复」的文件快照法（git stash 在有并行批次的树上是禁区）。
- 2026-11-02 沙箱实例的恢复路由会改变侧栏渲染 → 影响 ensureLaunchKinds 的触发链（agentGroupItems 只在 Workspace 页渲染）：Settings 路由启动的实例 catalog 永远不加载，⌘T 恒 Terminal。复现「启动种类」类问题前先把沙箱 route 设为 /workspace。
- 2026-11-02（架构记忆）sibling sweep 命中：`landLaunchedPane` 同型位除了 startNewTab 还有 history_continue.go 的 Continue 完成路径——凡是「launch 事务完成后落 surface」的代码都必须走按意图落地的 helper，不许 showPane。
