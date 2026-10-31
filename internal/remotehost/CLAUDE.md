# remotehost/
> L2 | 父级: /CLAUDE.md

成员清单
config.go: 监听策略纯数据层——Mode（off/loopback/lan/tailscale，零值即 off，未知值报错而不放宽）、Config.Validate/ListenAddr（lan 只认私网/链路本地单个网卡地址，tailscale 只认 100.64/10 与 fd7a:115c:a1e0::/48，永不返回 0.0.0.0/::，不开 socket）
tls.go: 自签 ECDSA P-256 证书（LoadOrCreateCert，私钥 0600、10 年、到期前 30 天重建）与 Fingerprint（DER 的 SHA-256 十六进制；手机按指纹固定，不依赖 CA）、writeFileAtomic（同目录临时文件 + rename）
pairing.go: 一次性配对码（128 位 base32、6 分钟、单次、同一时刻只有一个活动码、常量时间比较；配对码不是可复用凭据，绝不写日志）
devices.go: 已配对设备库 DeviceStore（devices.json 0600，只存 token 的 SHA-256 + 名称 + 时间；明文 token 仅 Add 返回一次；Authenticate 常量时间、LastSeen 一分钟节流写盘；Revoke 即刻失效并回调关闭该设备连接；损坏文件报错而不是静默解除全部配对）
backend.go: Host 与 Herdr 之间唯一的小接口 Backend（本机 Session 列表/投影/事件订阅/pane.send_input/observe 命令）与 EventSource，NewManagerBackend 适配 herdr.Manager；instance、pane、terminal 都要先在这里解出才进 socket 路径或 argv
tree.go: 投影 → /api/v3/tree 线格式（BuildTree：全不透明 ID、保持 Herdr 顺序、不含聚焦字段）、内容哈希与 paneForTerminal/paneIDs
server.go: Server（New/Start/Reconfigure/ListenAddress/serve/Close/Handler/StartPairing；Reconfigure=先关监听与全部 WS、作废配对码、再按新配置绑定，失败即保持停止，绝不回落到更宽的地址）：TLS 1.3 + 指纹、POST /pair（免鉴权）、bootstrap、tree（ETag=内容修订）；authorize(scope)+guard 是唯一鉴权入口（Bearer 头或 ['solco.bearer', token] 子协议，旧 shardlane.bearer 拒绝）；Scope 位（view/terminal-input/manage，V1 全授）；撤销经 OnRevoke 异步关闭该设备所有 WS（不阻塞 Settings；先发 close 帧再取消，取消 ctx 会让库不发帧就断连）；instance 必须是 Backend 的本机 Session
peek.go: PeekLine（pane.read visible 的最后一行含字母/数字的行，去控制字符、≤200 字节且按 rune 截断）与 GET /panes/{paneId}/peek；每 pane 1 s 节流缓存（≤256 项），pane 必须先在 tree 里解出；读失败只回通用 502
netaddr.go: Candidates(mode)——Settings 在 lan/tailscale 下列给用户选的网卡地址（lan=私有网段且非 Tailscale，tailscale=100.64/10 与 fd7a:115c:a1e0::/48，永不列回环/链路本地/通配），过滤是纯函数，列出的必能通过 Config.Validate
events.go: GET /events WebSocket：hello{revision} 后只推 tree.changed；Herdr 事件仅作重读触发器（150 ms 去抖，内容真变才推；窗格集合变化重订阅；失败指数退避）
catalog.go: GET /api/v3/panes/{paneId}/chat/catalog（scope view）——Chat 输入框 `/` `$` 与模型菜单的数据，来自 internal/agentcatalog（各 Agent 自己的 Skills/命令/模型，只读、不执行任何命令；pi/omp 模型列表沿用其十分钟缓存）；provider 取自 Herdr 报的窗格 agent 种类、根目录取窗格 cwd，与转录是否存在无关（新窗格也能列）；未知 agent 回空目录；wire 比桌面 ChatCatalog 多一个 `modelVerb`（`/model `、`/switch `），手机用它自己拼“切换模型”的文本；Options.Catalog 可注入（测试不跑登录 shell）；目录内容不进日志
catalog_test.go: 按窗格目录取、401/未知 pane/未知 instance 不触达目录、未知 agent 回空
attach.go: POST /api/v3/panes/{paneId}/chat/attachments（scope terminal-input）——手机以 base64 上传一个文件，经 Options.Attach（App 注入，落到 internal/chatattach 的 ChatAttachments 缓存目录）保存并回绝对路径；上限与桌面同（单文件 32 MB，请求体按 base64 放宽）；Attach 为 nil 时 bootstrap 不声明 `attachments` 且路由回 404 attach_unavailable；文件名/内容/路径不进日志
attach_test.go: 存储与回路径、401/未知 pane/未知 instance/坏 JSON/坏 base64/空文件都不触达存储、无存储时 404 且不声明能力、超 32 MB 回 413
question.go: 聊天帧里的 `interaction` 卡与 POST /panes/{id}/chat/answer——只在 agent 为 blocked 时派生（omp `ask` 来自转录 Reader.Ask、Claude 屏幕提问来自 Backend.ReadPane + chatsession.DetectScreenPrompt，读屏 ≥1.2 s 节流）；状态在每 pane 的 askCursor（ask 的步数、刚答过的卡 2.5 s 内隐藏）；id = pane+来源+步数+问题+选项哈希，答题时重新派生并比对（过期/已答 409 interaction_gone）；按键规则与桌面共用 conversation.AnswerKeys/MultiSelectKeys；投递只走 agent.send_keys / agent.prompt（被阻塞的输入框回退 pane.send_input），Herdr 拒绝原样回传，答案正文不进日志；不含 Queue
picker.go: Claude Code/Codex `/model`、`/permissions` 的 Question 卡——只在 chat_send 发出 picker 命令（agentcatalog.Classify==KindPicker）后 armPicker 开 6 s 出现窗，窗内/在屏期间读屏（chatsession.DetectPicker，非 blocked 的 agent 也可带卡）、离屏即撤卡（最长 10 min）；答题仍走 handleChatAnswer（Claude 用 `s` 仅本会话，Close＝Escape），答后重开窗读下一步列表；屏幕内容不进日志
chat.go 的 chat/stream：手机唯一会发的消息是 `{"t":"older"}`（该 socket 的 Reader 上移一页，翻页只属于这条流，重连回到最新一页），其余消息忽略
files.go: GET /api/v3/panes/{id}/chat/files?token=（scope view）——手机输入框 `@` 补全，只列窗格 cwd 内匹配条目的名字（chatsession.ListFiles，contained=true：`..`/指向目录外的符号链接列空），不读内容；未知窗格 404
files_test.go: 列表/子目录下钻、越界与空数组线格式、需要 view token 且窗格已知
queue.go: 跟进队列的 Host 侧——`FollowUps` 接口（Enqueue/Get/Cancel，实现是 app.RemoteFollowUps → 桌面同一个 conversation.FollowUpQueue，一会话一槽、指纹来自 Herdr agent_session、不确定即墓碑不自动重发）；chat_send 在 agent 处于 working 且请求带 `queue:true` 时入队，chat 帧带 `queued{text,state,detail}`，`POST …/chat/queue/cancel` 撤回；错误码 queue_unavailable/queue_full/queue_failed/no_queued/already_delivered；capability `queue`
queue_test.go: 入队/撤回/满槽/不可用/capability（fakeQueue）
picker_test.go: 命令后出卡/答题按键/离屏撤卡、出现窗到期、普通文本不开窗
subagent.go: GET /panes/{id}/chat/subagents/{subID}（ScopeView）——手机子 Agent 抽屉，经 chatsession.Reader.Subagent，未知 id 404 unknown_subagent
subagent_test.go: 抽屉读取、未知 id、只读转录
launch.go: P4 启动新会话——POST /api/v3/launch（scope manage）：requestId + 恰一个 workspaceId|path + kind（terminal 或 Launcher.Kinds() 里的 agent；terminal 不带 prompt）+ 可选 prompt（≤64KiB，空=只启动 Agent）；路径须绝对且无 `..`/控制字符；cwd 来自投影（workspace）或请求（path）；Launcher 在 WithoutCancel+150 s 内跑（手机断线不打断事务），LaunchError 带稳定 code（path_unavailable/launch_conflict/launch_uncertain/launch_failed），池里 4 s 等 pane 入投影否则 504 launch_uncertain；无 Launcher=404 launch_unavailable，bootstrap 仅在有 Launcher 时带 capabilities.launch + launchKinds；日志只记设备/kind/pane/code，绝不记 prompt 与路径。具体 Launcher = internal/app/remote_launch.go 的薄适配，真正的启动逻辑在桌面/手机共用的 internal/app/session_launch.go（SessionStarter：agent.RunLaunchTransaction + LaunchRegistry 幂等 + live kind 列表）；Launcher.Launch 返回 LaunchResult{PaneID, PromptPending}——Agent 已创建却卡在人类才能答的提示（Claude 首次「信任此文件夹？」，不 interactive_ready）时 prompt 并未发出：响应带 `promptPending:true`（启动成功而非 launch_uncertain），手机把 prompt 带进 Chat 草稿。该判定在共享层 app.SessionStarter（agent_status=blocked 且阶段 not-ready），幂等重放答同一句
launch_test.go: 假 Launcher——无 Launcher/未鉴权/坏请求表/未知 instance 与 workspace/workspace cwd 与 path 透传/无目录 workspace/拒绝原样回传与普通错误 502/pane 迟迟不现 504/bootstrap 广告
git.go: 只读 Git 面——GET /api/v3/workspaces/{id}/git（分支、ahead/behind、已暂存/未暂存/未跟踪文件）与 /git/diff?path=（单文件 unified diff，行预算 3000 行/400 KiB/单行 2000 字节，超限给 truncated）；scope view；GitReader 接缝，Options.Git 为空=404 且 bootstrap 不带 capabilities.git；目录来自投影里 Workspace 的 cwd（只本机），path 必须是快照列出的文件（`..`/`-p` 等一律 unknown_file）；非仓库=409 not_a_repo；失败只回通用错误，不泄路径/diff 正文。具体实现 internal/app/remote_git.go（RemoteGit，复用 gitworkbench.Runner，不另起 Git 进程逻辑）
git_test.go: 假 GitReader——无 reader 404/未鉴权/workspace 目录透传/not_a_repo/未知 workspace、instance、file/不透明失败/capability 广告
terminal.go: GET /terminals/{id}/stream：每 WS 一个只读 `terminal session observe` 子进程（手机自己的栅格，不 attach/takeover、不重排 Mac 窗格），帧 = JSON 头文本帧 + ANSI 二进制帧；输入只走 pane.send_input（文本≤64KiB、键名白名单正则）；resize=用新栅格重启（100 ms 去抖）；每设备 ≤2 路；Herdr 结束流以 4404 + 机读原因关闭
question_test.go: 屏幕提问显示/单次答题/二次答题 409 且不触达 Herdr、未知选项与错 id 拒绝、未 blocked 与投递失败（卡留着）、omp 多问题逐步（recommended 起始行、多选 space 序列）、自由文本行先选再打字（含 pane.send_input 回退）、流里出卡与离开 blocked 即撤
remotehost_test.go: 模式/地址表（含通配拒绝）、证书稳定与续期/损坏重建、配对码单次/过期/替换/取消、token 不落盘、撤销、LastSeen 节流、损坏文件、名称边界
chat.go: P2 只读 Chat——GET /panes/{paneId}/chat 与 WS /panes/{paneId}/chat/stream；pane → Herdr agent_session → history.ResolveLiveSource → chatsession.Reader（与桌面同一投影）；无会话 409 no_conversation；流内 1 s 轮询、4 s 重绑/子代理刷新，序列化相同不重推；每设备 ≤4 路；Options.Home 可注入（测试）；Options.Identity 是桌面 hook 学到的会话身份的借用口（Herdr 的 agent 列表没有 Claude Code/Codex 的 session）
chat_send.go: P3 子集——POST /panes/{paneId}/chat/send（Sendable 才发；working 须 interrupt 才 Esc→Ctrl-C→发；blocked/failed needs_terminal；Herdr 拒绝原样 refused；DeliveryUncertain 504 不重发；正文不进日志）与 /chat/stop（一次 Esc）；Backend.PromptAgent/SendAgentKeys 即 Herdr 既有 agent.prompt/agent.send_keys
chat_send_test.go: 门禁表驱动（拒绝从不触达 Herdr）、interrupt 顺序、Herdr 拒绝/不确定投递、Stop
contract_chat_test.go: 可选真机契约（SOLCO_REMOTEHOST_CONTRACT_CHAT=1）：真 Herdr + 真 omp，经 Host 发 prompt、经共享投影读回答案
chat_test.go: 投影形状、三类拒绝（401/404/409）、文件增长推新帧
peek_test.go: PeekLine 表驱动（空屏/边框行/控制字符/多字节截断）、端点节流与失效、未知 pane 不触达 Herdr、失败不泄路径
netaddr_test.go: 候选过滤表、列出即可 Validate、真机枚举不报错、Reconfigure 换口/作废配对码/坏配置与通配保持停止/关闭后不再可配对/端口被占失败
server_test.go: 假 Backend 驱动——无 token/错 token/非 Bearer 全 401、子协议鉴权与旧前缀拒绝、bootstrap、配对一次性与 TLS 指纹固定端到端、off 不开口与通配拒绝、树顺序/无聚焦/ETag 仅内容变才动/未知 instance 不触达后端/后端错误不泄路径、events hello/tree.changed/静默/重订阅、终端帧顺序与头、未知 ID 与坏栅格 4xx 且不建命令、输入走对 pane 与键名校验与 Herdr 拒绝原样回传、resize 重启并回收旧子进程、Herdr 关闭→4404、每设备上限与释放、撤销关 WS 并杀子进程
contract_test.go: 可选真 herdr 契约（SOLCO_REMOTEHOST_CONTRACT=1）：隔离 session 里整链路——51×19 首帧整屏、输入回显；结束删除 session
serve_launch_test.go + export_test.go: 外部测试包（可 import internal/app 而不成环）经 ServeLauncher / ServeGit 给常驻 Host 装上真 RemoteLauncher 与 RemoteGit，让手机真机能走完整启动链路
contract_test.go: 另有 SOLCO_REMOTEHOST_SERVE=<dir>：真 TLS + 真 herdr session 常驻给外部客户端（solco-mobile `pnpm e2e:v3`），写 pair-url.txt/instance.txt，等 <dir>/done 退出（SOLCO_REMOTEHOST_SERVE_MINUTES 调整常驻时长；SOLCO_REMOTEHOST_SERVE_IDENTITY=`codex=<rollout 路径>` 代替 Mac 应用的 hook 记忆，给 Claude/Codex pane 一个会话身份）

设计与协议草案: designs/remote-host/README.md。本包不 import MyGo、不 import nativeui；P1 已含 peek、地址候选与 Reconfigure；挂载与设置页在 nativeui/remote_settings.go；剩真机总验。

[PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
