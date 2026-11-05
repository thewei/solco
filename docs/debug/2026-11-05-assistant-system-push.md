# Solco Assistant 系统级后台推送｜Host → Expo → FCM (2026-11-05)

## 已实现的链路

本地通知（原有）：手机 Android App / foreground service 还在运行时，监听已配对 Mac 的 Host Socket，收到 \`personal-agent.changed\` 或轮询 Activity 后用 Android 本地通知；成功安排通知后只 ACK \`device:<id>:notifications\`，不改变用户手动标记已读的 Activity。

**系统推送（新增，可选且明确授权）：** 手机 Connection 设置里主动按 **Enable system push**，调用 Expo \`getExpoPushTokenAsync({projectId})\`，经已有 TLS 指纹固定或已配对 relay 通道，向 Mac 的 \`POST /api/v3/notifications/push/register\` 注册。Host 只保存对应已配对 DeviceID 的 Token（\`<RemoteHostDir>/assistant-push.json\`，权限 \`0600\`）；从不随 bootstrap/status 返回 Token。Mac Host 正在服务时启动一个 30s tick；当设备**没有 live Socket**，逐一从唯一 PersonalAgent 插件 SQLite 读取该设备的 notification-only Activity 游标、Proactivity quietNow，向 Expo HTTPS 推送**仅**泛化锁屏消息及不透明 Assistant Activity 路由。

调用 Expo Push API 成功只能得到 ticket；Host 先原子保存 \`(AssistantID, activity seq, ticketID, sentAt)\`，不 ACK。约 15 分钟后查询 Expo receipt，只有 \`ok\` 才推进该设备已有的通知游标（原来的「Mark seen」游标不变）。\`DeviceNotRegistered\` 即停推并删除对应 Token；请求/receipt 未知则不 ACK，24 小时后长冷却（2 小时）再尝试 at-least-once 重送。设备撤销时 Mac 删除登记并关闭 Socket；手机主动 Disable 则删除 Token。FCM 原生 Token 轮换只通过 \`/refresh\`，服务器必须确认当前设备**此前仍明确启用**，所以竞态中的旧客户端不能重新打开。

### API

| Endpoint | Scope | 结果 |
|---|---|---|
| \`GET /api/v3/notifications/push\` | View | \`{"schema":1,"enabled":false/true,"provider":"expo"}\`，无 Token |
| \`POST /api/v3/notifications/push/register\` | Manage | 严格 \`{"token":"ExponentPushToken[...]"}\`，显式启用 |
| \`POST /api/v3/notifications/push/refresh\` | Manage | 仅已启用的设备允许换 Token；否则 \`409 push_disabled\` |
| \`POST /api/v3/notifications/push/disable\` | Manage | 删除仅当前配对设备 Token |
| \`POST /api/v3/notifications/push/test\` | Manage | 向当前设备发送泛化测试通知；\`{"schema":1,"queued":true}\` 仅代表 Expo 接收 |

\`capabilities.assistantSystemPush\` 仅在 PersonalAgent core 可用时下发。老版本 Host 完全不显示开关；禁用本功能不影响现有本地通知和 Android 前台保活服务。

## 用户必须配置的真实外部条件（尚未在本轮环境完成）

1. 在 <https://expo.dev> 创建/关联 **EAS Project**，取得 UUID。移动仓库在构建环境填 \`EXPO_PUBLIC_EAS_PROJECT_ID\`，生成 \`extra.eas.projectId\`。
2. 在 <https://console.firebase.google.com/> 创建 Android Firebase App，包名准确为 \`com.whstudio.solco.mobile\`，下载 Android **google-services.json**。构建环境填 \`SOLCO_GOOGLE_SERVICES_FILE=/absolute/path/to/google-services.json\`（文件无需也不应该带入本仓库）。
3. 在 Firebase 控制台服务账号中创建 **FCM v1 service account**，用 \`eas credentials\` 把私钥上传至 Expo 项目；**不要把服务账号 JSON 放进 App、Mac Host、Git、UI 或 Push Payload**。这是 Expo 云服务对 Google FCM 的认证。
4. 构建一个包含 FCM 原生配置的 Android **development/release build**（Expo Go Android 不支持远程推送）。先在带 Google Play Services 的真机/模拟器测试。Android 设置里允许 App 通知和对应 \`agent-attention\` 通道。
5. 打开 Mac 的 Solco Remote Host，手机完成配对，Connection → System push → **Enable**。可按 **Send a system push test** 验证设备是否实际收到系统通知（API 的 \`queued\` **不是设备送达证明**）。将手机 App 彻底结束后，让 Assistant 生成新的任务状态/审批/OMP 汇报 Activity，观察 Mac 在线时是否经系统通知送达。
6. 如在 Expo 项目开启了 Enhanced Push Security，必须在创建 Mac Host 时设置 \`Options.PushAccessToken\`（专供 Expo 请求的 Bearer）并安全管理；默认使用 Expo 不要求该字段的项目模式。

> 本次环境没有 EAS Project ID、FCM v1 凭证和正式 Android 构建：实现和模拟 Expo API receipt 测试通过，**未验证真实手机在 App 被杀后收到 FCM**。旧版 vivo/国行 Android ROM 若无 Google Play Services，FCM 可能无法获得 Token，此时仍靠系统允许的前台服务 + 本地通知；强杀 App 会终止此兜底。若要覆盖无 GMS 的 ROM，需要后续接入 vivo/厂商推送或统一推送（通常要企业资质、应用商店包与平台审批）。

## 边界与安全

- Mac 必须运行且 Host 监听已启动；Mac 睡眠/关机/网络断开时没有替它继续做推送的服务器。目前不是云常驻 Agent。
- Expo ticket \`ok\` 只是 Expo 接收，receipt \`ok\` 是 Expo 已交给 FCM/APNs，**两者都不是手机屏幕实际展示或人的已读确认**。推送可能延迟、被系统省电限制、过期、合并、丢弃；Activity 在 SQLite 持久化，可待手机再次连接后补读。
- 推送内容严格只有 \`Solco · Assistant needs you\` / \`Solco · Assistant activity\` 与泛化摘要，不传目标标题、路径、用户 Prompt、Evidence、真实 Provider transcript、设备配对凭据、Assistant 名称。附带仅不透明 Assistant ID 的路由。Expo/FCM 会接触推送 Token 和上述泛化消息；隐私敏感安装环境应保持 opt-out。
- 当前只对 **Android** 开放注册；未实现 iOS APNs 凭证与系统推送。Android 偏好前台服务和通知 Socket，只有无 live Socket 才由 Mac 补发 Expo，尽量避免双重打扰。冷启动点击路由必须通过 App 的 Assistant Activity 路由白名单，不能借通知执行审批/工具动作。
- Retry 语义为 at-least-once；Host 在 Expo API 接收成功但持久化 ticket 前崩溃、或者 receipt 过期等，可能重复弹通知。绝不承诺 exactly-once。

## 自动测试

\`GOTOOLCHAIN=auto go test ./internal/remotehost -run '^TestPush'\`：认证设备独立、Token 0600 与损坏文件报错、未知 JSON/伪造 DeviceID、撤销/关闭、Mac Socket 在线抑制、静默时间、Expo ticket 与 receipt 分离、provider 404/DeviceNotRegistered、无 receipt 的长冷却恢复、显式 test push 不 ACK、Token 轮换与拒绝关闭后重开。移动侧 \`pnpm typecheck && pnpm lint && pnpm test && pnpm check:fixtures\`，严格解析 Golang golden 和冷启动推送的白名单路由。
