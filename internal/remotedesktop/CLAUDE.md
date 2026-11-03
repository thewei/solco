# remotedesktop/
> L2 | 父级: /CLAUDE.md

Remote Desktop 的 Mac 侧：手机实时观看整个 Mac 桌面并完整操控。Solco 仍是客户端、Herdr 仍是运行时权威——这里不碰 Herdr，只有屏幕/输入/剪贴板，且不导入任何框架（nativeui 与 remotehost 单向依赖本包）。

成员清单
service.go: Service 门面（Host 信令端点、设置页、托盘共用）——决定「会话能否存在」：协议版本 → SDP 大小 → 模式/档位 → 已启用 → 忙（同一时刻仅一个会话；同一台手机再次请求 = 替换它自己的旧会话，手机网络切换后新 offer 先于 Mac 发现旧链路死亡）→ 后端可用 → 屏幕录制权限 → 本机授权（Authorizer，45 s `ApprovalTimeout`，可降级 View Only；需要授权却无 Authorizer = 拒绝；授权前不拉起 helper）→ 辅助功能缺失强制 View Only → backend.Open → session.Prepare → rtc.Answer → Attach；SetSettings 关闭即结束会话；DisconnectDevice 供 Revoke
protocol/: 平台无关的线格式词汇——Info/Offer/Answer/SessionState/DeviceInfo、control 通道各消息、三档质量（smooth 720p/2500、standard 1080p/5000、high 1440p/9000 kbps @30fps）、稳定错误码、上限；JSON 标签同时是 helper IPC 的标签（也被 `internal/remotehost/testdata/api-v3/remote-desktop-*.json` 与手机端 Zod 钉住）
platform/: OS 缝 Backend/Capturer/Input/Clipboard 接口；`macos`（监督 Swift helper 子进程，只从 `Contents/Helpers`/`SOLCO_REMOTE_HELPER`/开发期兄弟仓库定位，启动前 codesign 校验）、`auto`（按 GOOS 选后端）、`unsupported`（Linux/Windows 的扩展位：只返回 unavailable，后续在此接入各自的采集实现）
helperipc/: Go 端 helper Unix socket（u32 大端长度 + kind + payload，令牌走环境变量 SOLCO_RD_TOKEN，LOCAL_PEERPID 校验对端），版本化
rtc/: Pion 应答方（非 trickle、H.264 baseline 42e01f、control 可靠有序 + pointer 无序 maxRetransmits 0、PLI/FIR 去抖、8 s 断线宽限）
session/: 单个会话的行为——Prepare（选显示器、算编码尺寸、不采集）→ Attach(link) 起 frameLoop/eventLoop/tickLoop，链路 Connected 才开始采集（每次新 generation，旧代帧丢弃）；View-only 强制（`view_only` 错误每秒至多一次）；指针按 seq 去旧、按下沿用已应用移动的最后位置；按住的键/键钮上限 32，断链/关闭/切屏/权限丢失/手机请求/10 s 静默看门狗都 releaseAll；text.insert ≤4096 rune 且去控制符；剪贴板 ≤256 KiB 纯文本；每 2 s 推 stats；显示器消失回落主屏；fail() 异步（先告知手机，150 ms 后 Close）
session/screen.go: 屏幕侧功能——`platform.Screen`（可选接口，对 Capturer 做类型断言）：防窥黑屏（privacy.set，gamma 表，采集在 gamma 之前不受影响）、锁屏状态每 2 s 轮询并随 session.state 下发（locked/canUnlock/blackout）、手机 unlock.request 与「自动解锁」（仅 Mac 主人开启；每次锁屏最多 2 次、间隔 6 s；密码只在键入瞬间经 `Config.UnlockSecret` 取，不发手机、不进日志）
自适应码率: rtc 内 Pion GCC（TWCC）→ session `OnBandwidth` → `protocol.AdaptBitrate`（估值 85%，夹在档位/5 与档位目标之间，变化<10% 或间隔<1 s 跳过）
mapper/: CoordinateMapper——归一化 0…1 坐标 ↔ 显示器点坐标（多屏、Retina 缩放、letterbox）

不变式
- 日志与响应不含屏幕内容、键入文本、剪贴板、SDP、令牌、配对码；错误只带稳定 code
- 手机断开 = Mac 立即释放所有按住的键/键钮；Mac 用户随时可 Disconnect（设置页、状态栏浮层、Revoke）
- 锁屏密码只存 `appSecrets`（键 `remotedesktop.unlock`），设置文件仅 `UnlockSaved`/`AutoUnlock` 两个布尔
- 关闭设置开关 = 终止会话且 Host 不再通告 `capabilities.remoteDesktop`

[PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
