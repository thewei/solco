# LESSONS — 取证循环自更新记忆

每轮循环结束后追加（见 SKILL.md「自更新」）。格式：`- <日期> <问题> → <教训>（落点）`。
重读时合并重复；超过 40 条时清理已被流程吸收的陈旧条目。

## 工具与编辑

- 2026-11-02 用 edit 工具给函数体加一行日志时误用 `PUT N*:`（块替换），一次毁掉 4 个函数体（workspace.go showSurface/openWorkspaceChat/syncWorkspaceForSelection/reconcilePaneSurface + new_tab_agent.go startNewTab/completeAgentOpen），只能靠此前 read 过的内容手工重建 → 在函数内部插一行用 `PUT >N:`（N 行之后插入）；`PUT N*:` 会替换整个块；收到 auto-repair/syntax 警告立即重读现场再继续，绝不在坏语法上叠第二个 edit。
- 2026-11-02 并行批次的未提交改动（internal/remotehost/chat.go 等 4 个文件）在工作树里 → 动手前 `git status` 记录，全程只读不碰，重建/测试不受它们影响即可。

## 环境与运行时

- 2026-11-05 Herdr 沙箱 OMP TUI 的原生流式联调先被旧 Bun 1.4.0、首次设置向导和极快模型工作状态漏采样误导 → 短 /tmp HOME 避免 Unix Socket 超长；隔离 `.zshenv/.zprofile/.zshrc` 先用 OMP 同目录 Bun，沙箱 `omp config set startup.setupWizard false`；以 `agent list`、`pane.read` 与同一 Socket 的 epoch/seq/Pane/session 身份验证成功，不单信 `agent.prompt --wait`（`scripts/check-herdr-omp-chat-stream.py`）。

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

## 第 1 轮循环（2026-11-03 chat-view-revert-on-return：home 家族改写 surface 记忆）

- 2026-11-03 沙箱 CLI 对不上 Solco 的 socket：Solco 自己的 Herdr socket 从 HOME 派生（`herdr.SocketPath` = `$HOME/.config/herdr/herdr.sock`），`HERDR_SOCKET_PATH` 只传给 spawn 的子进程（terminal attach 等）→ 沙箱内用 CLI 驱动 daemon 要用 `$SB/home/.config/herdr/herdr.sock`（落点 internal/herdr/herdr.go SocketPath）。
- 2026-11-03 cua-driver 一次性 CLI 每次调用独立 transport 会话，快照/截图缓存不跨调用（`screenshot_context_missing`、`stale_element_token`）→ snapshot+click 全序列放进一个 `cua-driver mcp` 持久 stdio 会话（python 起子进程讲 JSON-RPC），AX 不可达的元素用 `screenshot_frame` 像素兜底。
- 2026-11-03 并行会话可能跑着**同名沙箱**（同样改了 com.whstudio.solco.sandbox）：`pgrep -f SolcoSandbox | head -1` 会拿到别人的 pid，点击打进别人的窗口（本轮只造成导航级噪音）→ 先 `ps` 按启动路径甄别 pid，list_windows 后再截图核对内容（workspace 名/cwd）。
- 2026-11-03（架构记忆）`showSurface` 曾经「落 surface + 写 paneSurfaceMemo」耦合在一个函数里：任何经 showSurface(Terminal) 的落地都会改写记忆——home 家族（首页签/⌘1/⌘P Workspace/页签 toggle 退出）就是这样把 Chat 记忆持久打回 Terminal 的（沙箱 trace + fb-3/fb-4 截图实锤）。2026-11-03 起落地面与记忆分离：导航走 `showPaneSurface`/`landSelectedPane`（永不写记忆），只有显式选择（卡头切换钮、Open Terminal 动作、openChat 成功/邀请）写记忆；**新增「去某 pane / 回 workspace」的代码一律走这两个 owner**，不许再手写 `panePrefersChat ? Chat : showViewTerminal` 三元式（goToPane/external_reveal 已收编）。
- 2026-11-03（架构记忆）记忆回写必须与真实落地同分支：openChat 曾在入口无条件先盖 chat、无会话回落再盖 terminal 且**连 Terminal surface 都不落**（只改记忆 + toast）——surface 没变记忆先变是同型污染；修复后 Chat 记账在三个真正落 Chat 的分支里，回落经 showPaneSurface 不写记忆（单测 TestOpenChatKeepsAPinnedSessionlessPaneOnItsInvitation / TestOpenChatLandsASessionlessPlainPreferenceOnTerminal 钉）。

## 第 1 轮循环（2026-11-03 agent-launch-chat-loading：启动期表面无持有）

- 2026-11-03 沙箱应用的**退出保存会用内存态覆盖手改的 settings.json**（kill 触发 SaveSession 回写）→ 改沙箱设置必须 kill → 改文件 → 再启动，绝不在活实例存在时手改（落点 internal/nativeui/session.go SaveSession）。
- 2026-11-03 两个并行沙箱同屏时，对方 cua-driver 的像素点击会落进我的窗口（本轮 09:57:43/09:58:12/09:58:25 的 Chat↔Terminal 三连翻转，caller 全是 surfaceToggle 按钮路径 = 污染不是代码；对方安静后同环境零翻转）→ 判「翻转是否代码行为」：看 showSurface/memo 探针 caller 是选择/切换 owner 还是按钮 handler，并对照对方会话活跃时段。
- 2026-11-03（架构记忆）启动流不参与表面决策的正确形状是「意图持有」：pendingLaunch 从点击起持有 Chat（rendering launching 卡、无 composer 即禁止输入），syncWorkspaceForSelection/reconcilePaneSurface 在 hold 期间让位、watcher 投影先选新 Tab——不要在每条中间投影路径上猜 prefers/avail（落点 internal/nativeui/chat_launch.go）。
- 2026-11-03 沙箱启动用 `open --env` 不会传 HOME，应用会连到用户真实 Herdr 会话（立即退出、未操作；幸好只是看了一眼）→ 一律 python `subprocess.Popen(env=…, start_new_session=True)` 直接起可执行文件，起后先 `ps eww -p <pid>` 核对 HOME 再动手（落点 solco-development SKILL 沙箱配方）。
- 2026-11-03 窗口在副屏/后台时 `cua-driver get_window_state` 的截图可滞后数分钟（把旧会话的 toast 当成现状）→ 状态判断以 AX 树 + solco.log + 仓库状态为准，截图只做布局观察；toast 类断言用单测钉。
- 2026-11-03 `NewShell` 里任何读 `s.settings` 的初始化必须放在 `for _, opt := range opts` 之后（WithSettings 才换入已存设置）：Split/Unified 偏好曾因此每次重启回 Unified（TestSavedDiffLayoutSurvivesRestart）。
- 2026-11-03 为跑单测临时 `git stash -- <file>` 会把并行会话在同一文件里的未提交改动一并收走（已原样 pop 回来）→ 变异验证用复制备份文件再还原，不要 stash。
- 2026-11-03 `ui.Context.SetTheme` **会重置窗口根背景为主题底色**（`c.root.bg = t.Background`），抹掉 `Shell.View` 为 vibrancy 设的 `Root().Background(Transparent)`：changes_panel 为压掉选中蓝在列表外 `SetTheme(quiet)…SetTheme(real)`，结果每次评审区出现（点提交）整块侧栏失去材质闪白。无头 Tester 默认 vibrancy 关 + 只比 RGB 看不出 → 复现需 `tester.SetVibrancy(true)` 并比 **alpha**（侧栏透明像素数）；落点 internal/nativeui/changes_panel.go，守卫 `TestReviewingACommitKeepsTheSidebarVibrancy` / `TestSetThemeIsOnlyCalledWhereTheRootIsRestored`。教训：闪/白屏类 bug 先问"无头环境和真机在哪个开关上不同"（vibrancy、ReduceMotion、前后台），再下"无法复现"的结论。
- 2026-11-03 `go get` 走默认代理会卡 >5 min → `GOPROXY=https://goproxy.cn,https://proxy.golang.org,direct`（秒级）；别把 `GOFLAGS=-mod=mod` 留在 shell 里。
- 2026-11-03 macOS 没有 `setsid`，BSD `sed -i` 要 `sed -i ''`；`a && b` 链里它们静默失败会让后续半截编辑生效 → 脚本用 `nohup … &`，改文件优先用编辑工具/python。
- 2026-11-03 独立命名 Herdr 会话做 e2e：`nohup herdr --session <name> server &`，CLI 用 `herdr --session <name> …`，收尾 `herdr session stop <name>`；plugin daemon 用 `HERDR_PLUGIN_STATE_DIR/CONFIG_DIR` + `SOLCO_ASSISTANTS_*` env 隔离（plugins/solco-automations/scripts/e2e-assistants.sh）。
- 2026-11-03 daemon 在 `--standby` 且二进制路径≠已安装路径时会自我 relaunch 退出（daemon.go shouldRelaunch）→ 判忙必须包含所有在途 run（含 Assistants dispatcher），否则会把还在等 Agent 的 run 丢在 `running`；沙箱里的二进制天然≠已安装，会放大该问题。
- 2026-11-03 herdr 的 `agent_not_running` 一码两义（工作区已关 / 窗格里没 Agent）→ 区分要再查 workspace list；活工作区无 Agent = `uncertain` 交给人，不当取消。
- 2026-11-03 macOS bash 3.2 下裸 `[[ … ]]` 失败不触发 `set -e`（脚本会假 PASS）→ e2e 脚本一律用 `assert '[[ … ]]'` 辅助；daemon e2e 不要 `--standby`（沙箱二进制≠已安装路径会 ~30s 自退）。
- 2026-11-04 多屏托盘浮层闪动（全屏 Space 搏斗）→ popover 采纳窗必须带锚窗同款 collectionBehavior（MoveToActiveSpace|Auxiliary）；落点 third_party/mygo/internal/darwin/popover.go adoptPopoverWindow（Delta 6）
- 2026-11-04 终端 screencapture 无 TCC 会把其他 App 窗口从截图中剥离，壁纸照常 → "窗口不渲染"先对照 CGWindowList onscreen 再下结论，授权捕获走 cua-driver
- 2026-11-04 cua-driver desktop 像素坐标 = 主屏点数×2（÷2 映射），负 x 可达左侧屏，超出域即钳 0 → 右侧/第三屏点不可达，只能 move_cursor+AXPress 组合
- 2026-11-04 两个同名 Solco 进程并存时 System Events AX 按名字解析会串进程（window 1 取错）→ 用 unix id 过滤并注意其内部仍可能按名缓存；托盘图标位置每轮点击前重读（状态项动态位移）
- 2026-11-04 单实例锁键 = bundle id+name，改签 .app 副本会在 hardened-runtime 校验处 SIGTRAP；并实例隔离用 unpackaged go build（名字 Solco-dev）或 worktree 改 mygo.config.ts identifier 官方构建

## 第 N 轮（2026-11-05 Assistants Mac.app 隔离实机验收）

- 2026-11-05 沙箱 Chat 页永远 "Working…" 却不渲染 → 根因是沙箱 HOME 没装 Herdr omp 集成与 `solco-chat-stream.ts`（无 `agent_session` 身份），补装即正常；沙箱必须复刻用户环境的扩展集，别先怀疑产品（落点 acceptance_hold_test.go、SKILL 第 0 步附）。
- 2026-11-05 `computer_resolve_app(bundle_id)` 会经 LaunchServices 以真实 HOME 另起一个实例（污染真实数据目录）→ 一律用 `computer_apps` 取 pid/target_id；误起后杀掉并清 `~/Library/Application Support/<Name>`。
- 2026-11-05 WebView 内坐标点击/`computer_key` 静默无效、AX `press`/`set_value` 有效；AX `set_value` 不写入中文 → 验收用 AX + 英文文案，旁证用 sqlite/会话文件。
- 2026-11-05 工具 shell 里 `source env.sh` 永久改 HOME/PATH，随后的 `rm -rf "$HOME/…"` 删到了沙箱目录、`rg`/`gofmt` 找不到 → 只在子 shell source；清理用绝对路径。
- 2026-11-05 `git add -N .` 把并行会话的未跟踪文件也标成已加入（已用 `git reset -- <path>` 还原）→ 永不 `-N .`/`add .`，只对自己的文件 `git add <path>`。
- 2026-11-05 e2e 夹具的脚本键对 CJK 消息可能落空 → 用 `last:<substr>` 脚本键或 `*` 兜底；GUI 里能输入的文案要与脚本键同语种。
- 2026-11-05 skill 升级原因：加入「Assistants/插件类 Mac.app 隔离实机验收配方」，因为本轮反复踩到沙箱缺口、启动方式、WebView 输入、shell 环境污染。


## 第 N+1 轮（2026-11-05 AC-02 定时 Routine 实机验收）

- 2026-11-05 点了 GUI 却没反应、红绿灯是灰色 → 目标 App 不是 active/key window，输入被静默丢弃；先点它的 Dock 图标（或窗口标题栏）再操作；`computer_start_control` 会因超时失效，报 "Start remote control first" 就重新 start，且**不要与点击并行**调用（竞态）。
- 2026-11-05 想验证"到点触发"别去改 yaml 的 cron：approved Routine 的定义哈希一变就被规则调度器静默排除（这是正确的安全行为，不是 bug，也不会再有 fact）；正确做法是 `assistant routine draft` 用自然语言 `every day at HH:MM`（当前时间+3 分钟）建一个新草案 → GUI 批准 → 等整点。草案接口拒绝原始 cron（`needs_clarification`）。
- 2026-11-05 夹具 `last:<substr>` 键也会匹配 Coordinator 简报文本本身 → Coordinator 可能自己起草例行任务；夹具子串要选只出现在用户真实消息里的词。
- 2026-11-05 手动 "Run Now" 走旧 Automations 路径，不经 Assistants 的 skill/批准关卡也不产生 routine fact，**不能当作"定时触发"的证据**；定时触发验收必须看 `tasks.rule_id<>''` + `goal_facts` 的 `routine_done|routine_failed`。
- 2026-11-05 首次定时运行会因 `skill_not_granted` 失败并出现 skill 审批卡（正确门禁）；批准后任务自动重启（attempt 2）并写 `routine_done`。验收脚本要预期这条两步路径，而不是把第一次 failed 当缺陷。
- 2026-11-05 `routine_failed` fact 原先只写"failed"，原因只在 runs.error → 现在附稳定码 `(skill_not_granted)`（只取 `code: ` 前缀，不带自由文本）。教训：账本里的失败事实必须带可行动的稳定原因码。
- 2026-11-05 查库前先 `.schema`/`select *` 看真实列名（goal_facts.text、events 无 seq、runs.error），别凭记忆写列。
- 2026-11-05 并发/依赖类验收：用 Coordinator 自己的 CLI 动作（`goal create` + `goal set-plan`）写计划，证据取 `runs.started_at/finished_at`（同毫秒启动=并行；依赖项晚于前置完成=门禁）与 `goal_facts` 序列，别靠肉眼看 GUI 刷新。
- 2026-11-05 每个新增的 `Solco-…` 系统前缀（Run-Id、automated status request、wake）都会进到 OMP 会话的首条用户行 → 同时检查三处：Chat 页折叠（assistantBrief）、history 标题清洗（cleanTitleCandidate）、Sessions 列表；漏一处就会在侧栏/列表里以乱码标题露出。
- 2026-11-05 给 Go 结构体加线上字段一律 `omitempty`（如 `Assistant.System`），否则会动所有 golden；改完必须 `go tool mygo generate` 再 `bun run build`，并记得 solco-mobile 的 chat-page 需重新同步。
- 2026-11-05 重建隔离实例的固定配方：写 `stop` 文件 → `launchctl remove` → worktree 移到 HEAD → 重打 `mygo.config.ts` 补丁 → 重建插件二进制 + `go tool mygo build` → 重新 `launchctl submit`；沙箱全新所以会再出现 onboarding，CLI 建的 Assistant 要等一次轮询才进侧栏。
- 2026-11-05 WebView 输入：`computer_set_value` 对 Chat 输入框读回为空且不触发 React 状态 → 先 `computer_click` 输入框拿到 caret，再 `computer_type`（必须带 app target）+ `computer_key Return`；已验证 Coordinator 启动并回复。
- 2026-11-05 **别在持久 Shell 里 `source env.sh`**：它改了 HOME/PATH/XDG，后续 `touch ~/.cache/…`、清理命令全落到沙箱路径（实际踩到）；一律用 `( . env.sh; cmd )` 子 shell。CLI 的 `goal create` 把 `assistantId` 放进 JSON body，不是位置参数。
- 2026-11-05 `git commit -m … -- <paths>` 不会带上**未跟踪**的新文件（会得到一个缺文件、无法编译的提交）：新增文件先 `git add -N <file>`，再 pathspec 提交，提交后 `git status --short` 确认没有遗留 `??` 属于自己的文件。
- 2026-11-05 权限边界放核心不放扩展：TS 扩展只整形调用，身份/动作 allowlist/属主校验在 Go（`assistant --as`），并用一个 Go 测试对比扩展 `actions` 与核心 `coordinatorScope`，防两边漂移；改完用真实 Herdr+OMP e2e（`-tags e2e`）验证，不要只信 bun 单测。
- 2026-11-05 Shell 工具里 `GOROOT` 会被环境带成旧版本（报 `version go1.25.9 does not match go tool version go1.26.2`）：一律 `env -u GOROOT go test …`，不要用 `GOTOOLCHAIN=local`。
- 2026-11-05 macOS 没有 GNU `sed -i`（`sed -i 's/…/…/' f` 会报 "command i expects \ followed by text"）：多行/带引号替换一律 `python3`；且别用 `a && b` 把"可能失败的 sed"和后面的测试串起来——失败会让测试悄悄没跑。`gofmt -l` 报出的文件先 `gofmt -d` 看是不是自己改的（`daemon/personal.go` 等有历史遗留），只格式化自己的文件。
- 2026-11-05 新增"系统自发起任务"的入口（trigger、webhook→wake、routine 之外的任何东西）必须先问：它是否受 proactivity 等级 / Quiet Hours / 每日预算约束、是否会被自己产出的事件再次触发？否则就是绕过额度的后门（trigger 首版漏了前者，靠 `TestTriggerObeysProactivityLevelQuietHoursAndBudget` 补上）。外部输入（webhook reason）一律当数据：清洗后只给 owner 看，绝不进事件行/prompt/日志。
