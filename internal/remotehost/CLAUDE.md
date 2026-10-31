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
terminal.go: GET /terminals/{id}/stream：每 WS 一个只读 `terminal session observe` 子进程（手机自己的栅格，不 attach/takeover、不重排 Mac 窗格），帧 = JSON 头文本帧 + ANSI 二进制帧；输入只走 pane.send_input（文本≤64KiB、键名白名单正则）；resize=用新栅格重启（100 ms 去抖）；每设备 ≤2 路；Herdr 结束流以 4404 + 机读原因关闭
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
contract_test.go: 另有 SOLCO_REMOTEHOST_SERVE=<dir>：真 TLS + 真 herdr session 常驻给外部客户端（solco-mobile `pnpm e2e:v3`），写 pair-url.txt/instance.txt，等 <dir>/done 退出

设计与协议草案: designs/remote-host/README.md。本包不 import MyGo、不 import nativeui；P1 已含 peek、地址候选与 Reconfigure；挂载与设置页在 nativeui/remote_settings.go；剩真机总验。

[PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
