# herdr/
> L2 | 父级: /CLAUDE.md

成员清单
actions.go: Herdr Workspace/Tab/Pane/Agent 的权威 mutation RPC 适配器；支持 workspace.move、tab.move、pane.swap、pane.move、pane.resize、pane.zoom 显式 mode（toggle|on|off，live schema PaneZoomMode）、pane.release_agent（ReleaseAgent：pane_id+source+agent，live schema PaneReleaseAgentParams；客户端撤除 agent 条目的唯一通道）
actions_contract_test.go: SplitPane RPC 与 mutation → snapshot 时序契约；ZoomPane 显式 mode 线格式与非法 mode 本地拒绝契约
reorder_contract_test.go: Workspace/Tab/Panes 排序、跨 Tab 移动与分栏调整 RPC 的 method、字段、回读与输入校验契约
herdr.go: Herdr Manager、PATH/常见安装位置优先的 CLI 发现（env → PATH → 安装位置 → 登录 shell）及受限时 session list（6 秒）；RunCLI 是应用侧唯一的 CLI 一次性调用入口（runCLIEnv 是其 env 覆盖内部门），ErrCLINotFound 是“没装 Herdr”的哨兵；MinSupportedProtocol 与 ErrProtocolTooOld 是协议兼容门（pingRPC/ping 分离，EnsureRunning 遇旧存活服务器绝不二次拉起），ProbeRuntime 把会话 socket 的活体身份暴露给设置页；UpgradeRuntime(ctx, session) 跑 `herdr update --handoff` 后回 ping 验证，旧服务器若活着经 `server stop`（sessionEnv 以 HERDR_SESSION 定域）请下台，升级语义全归 Herdr CLI；普通 `herdr update` 因安装目录不可写失败时（updateNotWritable 认 CLI 原话），自动经 RunAuthorizedUpdate 走系统授权（Linux pkexec --disable-internal-agent / macOS osascript administrator privileges，GUI 不经手凭据）在原位完成升级；仅当授权也被拒或无授权代理才返回 ErrUpdateNotWritable，由界面给 UpdateAsRootCommand（sudo herdr update --handoff）终端出路
instances.go: Session 列举与实例身份的解析、管理
instances_test.go: Session 实例发现与边界回归
rpc.go: socket RPC 请求/响应、超时与交付不确定性边界
ref.go: Herdr runtime identity 归一化
projection.go: session.snapshot 到客户端一次性投影的解码、Splits 分栏与布局结构；协议门共用 herdr.go 的 protocolError（旧协议错误可被 errors.Is 识别）
projection_test.go: snapshot 数据与 runtime 标识映射回归
bridge.go: SSH Unix socket 转发、远程 RPC 通道生命周期；每台机器一座桥同时转发 API socket 与紧邻的 `<stem>-client.sock`（`herdr terminal attach` 按 API socket 推导 client socket，只转 API 则终端附着不上）；桥骑 sshcontrol.go 的共享连接（已登录时 ssh 把转发交给 master 后退出，关桥要在 master 里 cancel）；ConnectMachine 是设备切换的可取消前半（不碰 shell 状态），CloseBridge(id) 在设备被 Disable/Forget 后收掉该机器的桥（持机器自己的锁，进行中的拨号先落地再关），CloseBridges 在应用退出时全收
sshcontrol.go: Solco 自有 OpenSSH ControlMaster（`~/.solco/ssh-ctl/<12 位 hash>`、ControlPersist=10m）——口令/MFA 只在用户终端里输入一次（signInShell 带 AddKeysToAgent），之后桥与 remoteHome 的非交互调用骑同一条共享连接；MasterLive/CloseMaster/ValidateTarget；FailureKind（auth/host-key/network/server/other）+ ClassifyFailure 把 ssh 原话分成可恢复类，BridgeError 携带类别；Herdr 自己的连接与凭据不碰，Solco 不存任何凭据
sshcontrol_test.go: ControlPath 短、稳定且每目标一条；复用参数绝不创建 master；登录脚本替换死 socket 并留下 master、活 master 原样保留、目标被引号转义；失败分类表与 BridgeError 解包；目标校验；waitForward 的共享连接交接/ssh 失败/超时语义
terminal_task.go: 必须由用户交互完成的三步（machine add / 登录 / Set Up）的唯一交接通道——RunInTerminal 写出自带退出码标记的脚本交给 Terminal.app（`open -a Terminal`），TerminalTask.Result 轮询退出码；StartMachineAdd/StartMachineSignIn/StartMachineSetup；别名原样交给 Herdr（它生成的 ssh 配置 Include 用户 ~/.ssh/config，Port/IdentityFile/ProxyJump 随别名走），Solco 只读退出码、不读终端输出、不存凭据
terminal_task_test.go: Result 读退出码标记、脚本记录退出状态、machine add / Set Up 命令形状、坏目标在打开任何 Terminal 之前即被拒绝
events.go: Herdr 增量事件订阅与投影校正触发
events_test.go: 订阅事件字段与时序的协议回归
launch_transport.go: Agent 启动与无焦点 Tab 创建的安全流程
launch_transport_test.go: Agent 启动事务与远程边界契约
process_info.go: Pane 前台进程元数据适配
process_info_test.go: 前台进程元数据回归
machines.go: 远程机管理及状态入口，启动 machine list 读取 6 秒 deadline、防止阻塞工作区；MachineStates/MachineStatus（`machine status`，Herdr 自己的探测；任一档案出错时 CLI 退出码为 1 但仍在 stdout 打印全部判定，所以有判定就照单全收，不因退出码丢掉其余设备）、Enable/Disable/Rename/RemoveMachine（只传 profile id）、ListAllInstances（本机 Session + 每台已保存机器一个实例）
machines_test.go: 机器配置与状态回归；CLI 退出 1 仍保留全部判定（`TestMachineStatusKeepsTheVerdictsOfAnExitingCLI`，无判定时错误带 CLI 的 stderr）；桥 socket 路径、API/client 双转发（`TestBridgeForwardsCarryTheClientSocketBesideTheAPISocket`）、CloseBridge 只收被点名机器的桥（进程、两个 socket、登记项，不动别的机器）
search_gate.go: 搜索、复制等 Herdr 搜索协议适配
search_gate_test.go: 搜索消息边界回归
scroll_contract_test.go: Herdr scroll 消息与状态协定
herdr_test.go: Manager/协议基础回归（含 ErrProtocolTooOld 哨兵与 EnsureRunning 对旧存活服务器只 ping 一次、绝不二次拉起的回归）
run_cli_test.go: RunCLI 经解析出的二进制执行、失败时带上 stderr 的回归；UpgradeRuntime 决策表（兼容交接不动 stop、旧服务器存活则 stop、命名 session 定域、无服务器不 stop、更新失败浮出 stderr）
actions_test.go: Herdr 操作基础测试
plugin.go: Herdr 插件 CLI 边界——PluginListJSON（`herdr plugin list --json`，插件能力门的唯一数据源）与 Manager.InvokePluginAction（本机 session 内运行一个已声明的插件 action，经 sessionEnv 定域，之后回读权威投影供调用方采纳 Herdr 落点焦点；远程机器实例拒绝、不重试）与 FocusPluginPane（action 在 Herdr 客户端之外调用只开 Tab 不移焦点：按清单声明的 pane 标题在投影里找到它，未聚焦才调 Herdr 自己的 `plugin pane focus`）；HasPluginPane/ForceFocusPluginPane（已打开的插件 pane 只重新聚焦：投影里的焦点可能落后于 Herdr，`plugin pane focus` 对已聚焦 pane 是空操作）
plugin_test.go: plugin list 的 argv、invoke 的 argv 形状与空 id 拒绝、远程实例在触达 CLI 之前即被拒绝的回归

[PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
