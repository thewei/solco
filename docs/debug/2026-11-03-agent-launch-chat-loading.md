# agent-launch-chat-loading 取证循环
- 状态: 修复已落在主树（rebase 到并行批次 4ab0171/3ffa1aa 之上），待复验
- 注: 与并行批次 2026-11-03-chat-view-revert-on-return（home 家族记忆污染）共享关注面但现象不同——本循环只管「New Pane 选 Agent」的启动流；并行批次文件（workspace.go/surface_header.go/shell.go/titlebar.go/sidebar_services.go/service_*、internal/app/*、internal/settings）只读不碰。

## 现象（用户原话 + 操作路径）
「New Pane 我选择了 Agent 打开, 发现它并不会主动帮我切换到新的 Tab, 还有一个问题：页面会有多次变动。先是显示 Chat 界面，然后跳回 Terminal 页面启动对应的 Agent；Agent 完成之后又跳回 Chat 页面，比较反复。
我期望的是固定在 Chat 页面，然后在后台运行这个 Terminal 启动对应的 agent pane。Chat 界面可以在 Agent 还没启动完成前处于 Loading 状态，并禁止用户输入；出现问题没办法正常启动 Agent 的话，就在 Chat 界面提供一个链接，可以让用户主动跳回 Terminal 界面进行处理。」

用户拍板的目标行为（= 设计批准，2026-11-03）：
1. 点 + 选 Agent 起瞬间就切到 Chat（Loading 态），全程固定，永不闪 Terminal；
2. agent pane 的 Terminal 在后台启动（不 attach、不显示）；
3. Loading 期间禁止输入；
4. 启动失败时 Chat 界面给出「跳回 Terminal」的主动入口。

## 环境
- 隔离 worktree: $SB/wt（HEAD=06366ed 干净树 + 临时探针，修复在 worktree 先行）
- 沙箱: ~/.cache/solco-launch-flash/（home/work/logs/app）
- 沙箱 app: $SB/app/SolcoSandbox2.app，CFBundleIdentifier=com.whstudio.solco.sandbox2（避开用户实例与并行批次沙箱的 single-instance 锁）
- 启动 env: HOME=$SB/home HERDR_SOCKET_PATH=$SB/herdr.sock HERDR_CONFIG_PATH=$SB/herdr-config.toml HERDR_BIN_PATH=$HOME/.local/bin/herdr
- 日志: $SB/home/Library/Logs/SolcoSandbox2/solco.log，grep "launch trace"
- 临时 harness: SOLCO_LOOP_LAUNCH=<kind> + SOLCO_LOOP_DELAY_MS（NewShell 后定时 startNewTab，收工删除）

## 用户活实例日志取证（2026-11-03 读 ~/Library/Logs/Solco/solco.log）
- 22:44 后无 surface trace 行 = 用户现役二进制（~/Applications/Solco.app，重建于 09:08:47）含 2383899 surface-flash 修复但无探针（HEAD 无探针，探针属并行批次未提交改动）。
- 今日 attach 三连指纹（同 pane 100–400ms 内 attach ×2–3，spawned/closed 交替）：w5M:p1Q 09:03:22→09:03:30、w6A:pN 08:53:46→08:54:36 —— surface 反复离开/回到 Terminal 与用户「反复变动」吻合；无本地 agentSessionStarted 探针可看，需沙箱。
## 沙箱取证（worktree @06366ed + launch trace 探针 + SOLCO_LOOP_LAUNCH 钩子）
- 环境: 沙箱 herdr server 绑定 $SB/home/.config/herdr/herdr.sock（herdr configDir=XDG config，不是 $SB/herdr.sock——CLI 操作必须带 HERDR_SOCKET_PATH 指到它）；app pid/窗口 id 见日志；设置文件 $SB/home/Library/Application Support/Solco/settings.json，**应用退出时会用内存态回写覆盖手改，改设置必须先 kill 再改再启动**。
- Run1（agent_view 默认 TUI）：09:54:12.051 startNewTab(pi) → 期间 watcher 54ms 后就交付新 tab 投影（tabs=2 agents=0），0.7s 后 agents=1（卡片已到），但**选择原地不动整整 4s** → 09:54:16.019 startResult → landLaunchedPane 才 selectPane+落 Chat（prefers=false→落 Terminal）。**投诉 A 实锤：Tab/表面切换只在 StartSession 返回时发生，启动期（~4s）用户看不到任何切换。**
- Run4（agent_view=chat，等价用户环境）：09:56:32.521 click → 全程 cur=chat → 09:56:36.498 startResult(pane=w1:p4) → landBefore(cur=chat) → selectPane → syncSel invalid=true **prefers=true avail=true** → openWorkspaceChat → landAfter cur=chat。**表面状态机全程 Chat，无一次翻转（trace 全量无 showSurface(terminal)）。**
- Run4 异常 #1：09:56:36.499635 `attach native terminal w1:p4` —— 发生在 selectPane 内部（landBefore 与 syncSel 之间）= applyPaneSelection→syncSelectionPresentation→syncTerminals，**Chat 是当前表面时新 pane 的终端仍然被 attach**（settled 7602 字节 = pi TUI 在不可见处渲染）；违反「hidden Tabs keep no attachment fleet」的精神（同一表面 Chat 之下挂了活终端子进程）。
- Run4 异常 #2：**09:56:36.503 landAfter(cur=chat) 之后，log 里无任何 surface/projection/selection 事件，但 ~09:57:43 与 09:58:25 出现两次 `attach native terminal w1:p4`（spawned:1 closed:0），且 10:0x 窗口截图显示 /workspace 中心是 pi TUI = Terminal 表面**。09:56:36.503→09:57:43 之间日志零事件 → 翻转绕过了 showSurface/syncWorkspaceForSelection/reconcilePaneSurface/openChat 全部探针，或发生在探针未覆盖的路径（syncSelectionPresentation 的同 pane 重选、router.go:154、或渲染层）。Run5 已对 render switch、workspaceSurfaceState 全部 setter（带 caller）、applyPaneSel/applyTabSel/applyProjectSel、syncSelectionPresentation 加探针，截图 3s 节拍对时间轴。
- 用户活实例再核对：09:03 的 attach 三连（w5M:p1Q/p1P 交替，61 字节首帧）是用户**快速点选侧栏 pane 的导航**，不是启动闪烁；用户今晨的启动（chat-first，修复后路径）不产生 attach 行，日志里找不到启动期翻转的直接痕迹 → 以沙箱 Run5 的渲染级探针为准。
## 当前假设（Run4 后修订，hunt 契约）
主嫌疑：Chat 落地成立后，`syncSelectionPresentation` 在 Chat 表面下无条件 syncTerminals attach 新 pane 终端（GWB-040 例外洞），随后某条不经 showSurface 的路径（待 Run5 定位）把渲染翻回 Terminal——即「Chat 落地 → (静默)Terminal 显示 agent 启动 → (身份/投影事件)回到 Chat」的用户三段舞。投诉 A 与 Run1 已实锤（结构性：点击后 4s 无切换）。
## 根因（第 1 轮循环，2026-11-03 确认）
一句话：启动期间主表面没有「启动意图持有」，每一条 surface 决策路径都在拿**启动中窗格的过渡事实**（无卡的过渡投影 → syncSel invalid+prefers/avail=false → resetToTerminal；投影/卡片可用性抖动 → reconcilePaneSurface 翻转；晚到的 workbench 重建 → completeAgentOpen 强开 Chat；加上 Run4 实锤的落地面下 syncSelectionPresentation 仍 attach 新 pane 终端）各自为政，于是 Chat↔Terminal 由时序随机决定——用户的三段舞是这些路径的合成。投诉 A 是同一根因的另一面：点击后没有任何立即的表面/Tab 动作，一切等 StartSession 返回（沙箱实测 ~4s）。
附注：Run4 沙箱 09:57:43/09:58:12/09:58:25 的 Chat↔Terminal 三连翻转，caller 全部是 titlebar.go:290/311（showViewTerminal/showViewChat = surfaceToggle 按钮），时段与并行批次的 cua-driver 自动点击重叠、Run5（并行批次安静后）10 分钟零翻转 → 该三连判定为**并行会话合成点击污染**，不是自动代码路径；但其所经路径（toggle→showSurface→memo 写 Terminal）正是并行批次「记忆污染」课题，与本循环的启动保持互补。
## 修复（2026-11-03，主树）
launch-held Chat：pendingLaunch{RequestID,ProjectID,Kind,At,Failed,ErrText} 从点击瞬间持有主表面——beginLaunch 立即 push /workspace + surface.openChat（启动卡：spinner + Starting <Agent>…，无 composer = 禁止输入）；watcher 投影一旦出现 pending workspace 的新 Tab 即选中（投诉 A 的「主动切换」，先于事务返回）；syncWorkspaceForSelection/reconcilePaneSurface 在 hold 期间让位（avail=false 也保 Chat，绝不 resetToTerminal）；StartSession 返回 → landLaunchedPane → 清 hold 落真 Chat；失败（err / PaneID 空 / 45s 看门狗）→ 失败卡 + Open Terminal 按钮 → 用户主动跳 Terminal（此后记忆/正常规则接管）。
- 新文件 internal/nativeui/chat_launch.go（pendingLaunch 状态机 + 渲染卡 + adopt/retire/watchdog）与 chat_launch_test.go（6 条回归：点击即 Chat、无卡过渡投影保 Chat——红绿实测（launchHold 置 false 时该条红，恢复后绿）、落清 hold、失败卡 Open Terminal 出口、外项目导航弃 hold、看门狗只超时自己）。
- 接线：new_tab_agent.go startNewTab（beginLaunch 于点击、finishLaunch 于落地、failLaunch 于 err/空 pane）；chat_ui.go workspaceChatSurface 首分支（hold 时统一卡头 + renderLaunchState）；workspace.go 两处 guard（syncWorkspaceForSelection 无效分支 + reconcilePaneSurface 顶）；runtime.go applyProjection 在 s.projection 赋值前 adoptPendingLaunchTab（diff 需旧投影）；selection.go 三个 apply* 尾部 retireForeignLaunch；shell.go pendingLaunch 字段。
- Rebase：并行批次 4ab0171（showPaneSurface/landSelectedPane 落地面统一）+ 3ffa1aa（Settings→Agents 开关 + 引导页）先后落库；checkout HEAD 后重放我方 hunks（workspace/shell/CLAUDE.md），并恢复其未提交的 service-health shell 字段两块（probeMu/probeKinds/serviceHealth + NewShell inits）——此两块本在 /tmp/my-shell.go 保存副本里，diff 后原样回填。本修复与其 showPaneSurface 架构互补：hold 期间连 showPaneSurface 的记忆/可用性判断都不参与（reconcile 顶让位），failure 出口 launchOpenTerminal→showViewTerminal 是显式选择、记忆照常写（符合其「显式选择才写记忆」规则）。
- 静态事实：adoptRuntimeSelection 仅 bootstrap（runtime.go:195）；watcher 投影不跟随 Herdr focus → 今天代码里 Tab 切换只发生在 StartSession 返回后（landLaunchedPane），启动期间（round-1 实测 ~4s）选择原地不动 = 投诉 A 的结构性缺口。
- openChat 无源回落（chat_live.go ~95）：identity 未就绪时 liveSourcePending=false → 走 "No chat source" 分支 → rememberPaneSurface(Terminal) 污染记忆 + toast；rebindWorkspaceChat 的 sourceless latch（workspace.go:244-260）则是无源落邀请态、无回落。谁在启动流里调 openChat 待实机裁决。

## 复验证据（passing-after，2026-11-03）
- 回归测试红绿：`TestLaunchHoldKeepsChatThroughACardlessProjection` 在 launchHold 临时置 false 时红（surface=terminal，正是用户报告的翻转）、恢复后绿；internal/nativeui 全量 ok（含并行批次 pane_surface_memory_test）；全仓 go test ./... 33 包 ok。
- 实机（沙箱 ~/.cache/solco-launch-flash，主树构建 0.12.0）：⌘T（last-used=pi）→ 日志 `agent launch held chat`（hold 生效）→ 新 pane w1:p6 创建、终端后台 attach（用户要求的后台启动）；截图 /tmp/pa-final.png：Tab 条新 Pi Tab 已选中 + 中心 Chat（"Start the conversation" + composer，无任何 pi TUI）；+2 分钟 /tmp/pa-late.png 仍 Chat，零翻转。
- Gate：go build ./internal/... ✓；go tool mygo build 产物 Solco.app 结构验证 ✓；go tool mygo vet、git diff --check、gofmt 随 final-gates 跑（结果见会话输出）。
- 注: `go tool mygo vet` 于收尾时被并行批次的在途编辑阻断（gd_surface.go/git_service_snapshot.go/git_graph_refs.go 的 undefined slog/graphGen，11:2x 起的 git-graph E2E 中间态；HEAD 本身完好，bg_3 于 10:4x rebase 后全量 ok）。待其落库后补跑一次 vet 即闭环；部署同样等其收尾后走 solco-install。
## 证据（append-only，带时间戳）
- (待填)

## 当前假设（一句话，hunt 契约）
投诉 B 的 Chat→Terminal 翻转来自启动完成后的某条无源回落/记忆污染路径（嫌疑：openChat 无源分支、过渡投影的 fallback 选择），投诉 A 是 startNewTab 在 StartSession 返回前不切 Tab 的结构性缺口；两者都待沙箱探针裁决。

## 已试过
- 静态读 new_tab_agent.go/workspace.go/surface_header.go/selection.go/chat_live.go/runtime.go 全链 → 三个候选翻转源，无法唯一确定 → 转实机。

## 下一步
- worktree 加 launch trace 探针 + SOLCO_LOOP_LAUNCH 钩子 → 构建 → 沙箱复现 failing-before。
