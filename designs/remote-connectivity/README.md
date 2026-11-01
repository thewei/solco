# 手机连接：直连 → P2P → Relay 的统一选路

状态：**已决策，实现中**（2026-11-01）。Phase A（选路/提示）与 Phase B（WebRTC P2P）一起做；真机与 coturn 验证待完成。

## 现状（已验证）

| 项 | 状态 |
| --- | --- |
| Settings → Remote Access：Relay 开关 / 地址 / 注册钥匙 / 状态 | 已有（`internal/nativeui/remote_settings.go`） |
| 桌面拨出 relay 隧道（alma-relay PROTOCOL 逐字实现） | 已有，真生产 relay 契约通过（`SOLCO_RELAY_PROD_CONTRACT=1`） |
| 一个二维码 | 已是一个：`solco://pair?h&p&fp&c&n&r&rt`，同时带直连地址与 relay |
| 手机 relay 路由 | 已有：配对时 relay 为首连路由；之后直连地址依次尝试，再回 relay（`host-v3-provider.tsx`） |
| P2P | **没有**。relay（Cloudflare 版 1.0.0）只有隧道，无信令端点；`/turn` 返回 404（未配 TURN） |
| 后台定时择优 | 没有（只在读失败时恢复一次） |
| 手机端切换提示 | 没有 |

2026-11-01 联调发现：本机经代理到 Cloudflare 的 TLS 握手 0.4–5 s 不等，约四分之一卡死。桌面拨号已加 8 s 单次上限（`relayDialTimeout`），卡住即进退避重试。选路必须按“握手可能很慢或丢包”设计，不能把一次失败当判决。

## 选路模型

路由集合 = `direct`（QR 里的 LAN/Tailscale 地址与历史地址）· `p2p`（Phase B）· `relay`。所有路由承载同一份 `/api/v3`，业务代码不感知。

1. **首连**：并行探测 `direct`（每地址 2 s 内 `GET /bootstrap` 且指纹匹配）与 `relay`（探 `<r>/c/<rt>/api/v3/bootstrap`，8 s）。直连先到先用；直连全败则用 relay；任何一路都不会等另一路超时。
2. **顺序偏好** direct > p2p > relay：只有更高优先级路由探测成功才会切走；relay 永远是兜底，不是失败时才“发现”。
3. **后台择优**：App 在前台且已连上 relay/p2p 时，每 60 s（指数退避到 5 min）静默探测更高优先级路由；网络类型变化（Wi-Fi↔蜂窝、换 SSID）立即重探。探测成功连续 2 次才切（防抖），切换时只重连 events/chat/terminal 等长连接，请求在途的读可重试。
4. **不改信任语义**：direct 仍凭证书指纹；relay 仍是用户扫码登记的端点；P2P 的对端身份凭同一个设备 token 与指纹校验，不引入新信任根。

## 手机端提示（只在手机）

- 路由发生**切换**时给一条非阻塞 toast（≤3 s，可点开详情）：`已切换到 LAN（192.168.5.59）· 延迟 12 ms` / `直连不可用，已改用 Relay` / `已恢复直连`。
- 主机卡片/会话页头保留一个小路由徽标（已有 `HostRoute` 文案：LAN / Tailscale / USB / Relay），P2P 增加 `P2P`。
- 去抖：同一对路由 30 s 内不重复提示；首连不提示；不记录位置，只记录路由类型。
- 设置页（手机）加“连接详情”：当前路由、各路由最近探测结果（成功/失败/耗时）、上次切换时间。仅内存，不落盘。

## Phase A：不引入新依赖（建议先做）

桌面零新协议；手机端：
- `route-manager`（`src/lib/`，纯逻辑 + 可注入探测与时钟，vitest 覆盖）：探测、防抖、择优、切换事件。
- `host-v3-provider.tsx` 把现有“失败后恢复”改为向 route-manager 取当前路由；定时器与网络变化订阅放这里。
- `src/components/` 一个 route toast；`host-card-model.ts` 已有路由文案。
- 验证：route-manager 表驱动单测（慢握手、直连抖动、网络切换）；真机 vivo 上：Wi-Fi 开→关→开，观察 relay→direct 回切与 toast。

## Phase B：P2P（WebRTC DataChannel）

- **信令**：不改 relay。手机已经通过 direct 或 relay 通到 Host，在这条已认证通道上新增 `POST /api/v3/p2p/offer`（SDP offer/answer + ICE 经 `…/p2p/candidates` 交换，scope view，bootstrap 声明 `capabilities.p2p`）。
- **承载**：一条 DataChannel 复用现有 HTTP-over-frames 语义（请求/响应/流/WS 多路复用，与 relay 的 hreq/wopen 同构），手机 `host-client-v3` 增加第三种 transport，业务层不变。
- **依赖**：桌面 `github.com/pion/webrtc/v4`（纯 Go；Remote Host 包已声明“不引入 MyGo”，符合）；手机 `react-native-webrtc` + Expo config plugin（**需要重做 Development Build**，Android 与 iOS 各自验证）。
- **STUN/TURN**：Settings 增加 STUN/TURN 地址（relay 的 `/turn` 目前 404，需在服务器配 coturn：`TURN_URLS` / `TURN_SECRET`，开放 UDP 3478 与 49160-49200）。无 TURN 时对称 NAT（常见于蜂窝）打洞失败，自动留在 relay。
- **不做**：不把终端字节、会话正文或 token 写日志；P2P 也只暴露既有 `/api/v3`。

### 取舍（请决策）

- Tailscale 本身就是带 NAT 穿透的 P2P。已装 Tailscale 的用户 `direct` 路由已覆盖这个场景；WebRTC 只对“没有 Tailscale、也不在同一 LAN”的用户有增量，代价是新增两个依赖、一次原生重建、一台 coturn。
- 若只想要“快”：Phase A + 开 Tailscale 通常比自建 WebRTC 更稳、改动小一个数量级。

## 需要用户决定

1. 先只做 Phase A，P2P 等 Tailscale 覆盖不足的反馈后再定？还是 A、B 一起排期？
2. P2P 选型：WebRTC DataChannel（推荐，若做）还是不做、以 Tailscale 作为“P2P”？
3. 是否愿意在 relay 服务器上部署 coturn（B 的前提）？
4. 后台择优间隔默认 60 s 是否合适；是否需要桌面 Settings 里的开关（建议不需要，默认开）。

## Decision

2026-11-01，用户确认：
1. Phase A 与 B 一起排期。
2. P2P = WebRTC DataChannel（桌面 pion/webrtc，手机 react-native-webrtc，需要重建 dev build）。信令走已有的 relay 路由（`GET /api/v3/p2p/ice`、`POST /api/v3/p2p/session`，非 trickle SDP）。
3. 愿意部署 coturn（relay 是 Cloudflare Worker，coturn 需独立主机；`TURN_URLS`/`TURN_SECRET` 配在 Worker，或桌面 Settings → Remote Access → Direct P2P 的 ICE/TURN 字段手填）。默认 STUN 为 miwifi + cloudflare（Google STUN 在用户网络不可达，已实测）。
4. 后台择优默认开：直连 60 s 起、翻倍至 5 min；P2P 15 s 起、翻倍至 5 min；不加桌面开关。
- 优先级：直连 > P2P > relay；P2P 通道断开即回落 relay 并在手机上提示，直连命中后丢弃 P2P。
