# 2026-11-05 Solco Mac + vivo Android 安装与实机接力验收

**状态：已完成可执行的安装 / 前台与常驻 Host 接力验收；系统级 FCM 推送与实时 Token 在真机屏幕上的联合验收仍有独立门槛。**

## 环境与安全边界

- Android：vivo V2309A，USB ADB 授权，ABI `arm64-v8a`，`com.whstudio.solco.mobile`。现有包使用 Android Release（Hermes JS 自带，不依赖 Metro），当前 Android 包元数据 `versionName=0.1.0`（与应用产品版本不一致，待后续统一），签名仍是 Expo 预构建的 debug-keystore，不是 Play Store 商用发布签名。
- Mac：`~/Applications/Solco.app`，`com.whstudio.solco`，0.12.2，原有运行实例正常退出，重新安装并以固定 **Solco Local Dev** 身份签名；未替换或停止 Herdr daemon/Agent。
- 旧 Mac bundle 安全备份：`~/Applications/.Solco-before-20261105-acceptance.app`，签名校验通过；没有修改项目 Git 状态以进行安装。
- 两端均使用用户已有的远程配对；没有导出设备 Token、HTTP 鉴权、用户转录或私有证书。
- 来源：两端构建从**当时的工作区**打包，而非固定 commit 的复现构建；并行 Assistant/System Push 代码后续仍在变化，不得将用户手机或 Mac 上的安装包声称为所有未提交功能的最新快照。

## 实测与观察

| Gate | 结果 | 证据与范围 |
|---|---|---|
| ADB 真机 | **PASS** | vivo V2309A 通过 USB 授权，非模拟器 |
| Android Release Build | **PASS** | arm64-v8a 单 ABI，约 72 MB，Gradle 943 tasks，Build Successful |
| Android 覆盖安装 | **PASS** | `adb install -r` → `Success`；保留应用数据和之前的配对；原生 Launcher 启动，初始 PID 12286 |
| Android 页面真实展示 | **PASS（只读）** | 从真机截图目视确认已有 OMP 对话、工具/编辑记录与 Composer 渲染；非 Metro dev banner |
| Android 主路由可打开 | **PASS（只读）** | 前台 guard + UI 自动化：Agents / Changes / Files / Sessions / Quota 逐个显示有效原生树，Assistants 打开；未发送 Agent prompt/提交 Git/执行远程桌面 |
| Mac 覆盖安装 | **PASS** | 官方 `solco-install` 流程 7/7，安装到 `~/Applications/Solco.app`，固定身份签名并启动，首次 PID 96298 |
| Mac Helper | **PASS** | App 包含 `Contents/Helpers/solco-hostd` 和 Remote Desktop helper，双架构 `x86_64 arm64`；包签名验证通过 |
| launchd Hostd 注册 | **PASS** | 用户 LaunchAgent `com.whstudio.solco.hostd` 已启用并可查到运行进程 |
| 桌面退出 → Hostd 接管 | **PASS** | 桌面正常 Quit 后，Hostd PID 99305 接管 `100.88.0.1:7788` 和 AppHook Socket；`/api/v3/bootstrap` 对未授权请求返回 401；TLS 服务端证书 SHA-256 前后一致 |
| 手机冷启动 + Hostd | **PASS** | 桌面关闭，仅 Hostd 运行时：Android `am force-stop` + Launcher 冷启动，新进程 PID 14646；Mac 设备、Tailscale 路由与至少 7 行真实 Agent 状态重新显示 |
| Hostd → 桌面恢复 | **PASS** | 桌面再次启动，PID 1760 接管相同地址与 Hook Socket；Hostd 日志记录 yielding / apphook stopped；TLS SHA-256 仍相同 |
| 用户 HOME 中 OMP/Pi Extension | **PASS（安装/诊断）** | App 自带同一 canonical 源码，Go Manager 安全安装；两个 Doctor 状态从 missing → current，分别在 `~/.omp/agent/extensions` 与 `~/.pi/agent/extensions`；Herdr 原有状态扩展不变；**只对新启动的 Agent 生效** |
| Android Wi-Fi 切换及恢复 | **PASS（基础存活）/ PARTIAL（路由透明性）** | 关闭 Wi-Fi 约 16 秒、通过 `trap` 恢复；Android 进程 PID 18107 始终存活；恢复 Wi-Fi 后回到 Agents 页，Mac、Tailscale、Agent 行正常。原始页面是 Chat，因此中途“Host 名称不可见”不构成路由失败证据；未精确测量 Relay 回退耗时、Token/WS 连续性 |
| Android 运行稳定性 | **PASS（本次观察）** | 安装/导航/冷启动/接管/Wi-Fi 恢复均未见 `AndroidRuntime:E`、`ReactNativeJS:E` 致命记录；不是长期稳定性证明 |
| 手机 TS / 契约 / 共享 Chat | **PASS（源码检查）** | 最新源码 `pnpm typecheck` 退出 0；Vitest 609 passed / 2 skipped；Host 187 Golden fixtures 同步；`check:chat-page` Hash 一致 |
| Mac Go 全量回归与安装包签名 | **PASS** | 最新源码 `GOTOOLCHAIN=auto go test ./...` 退出 0，安装包 `codesign --verify --deep --strict` 通过；只证明运行时/回归测试，并非主窗口可见所有状态都通过逐项 UI 验收 |
| 真实 Pi/OMP 增量投递到 Android 的逐 Token 画面 | **未验收** | 先前的隔离 Herdr TUI / RPC / Host WebSocket IPC 集成门禁已通过；但本次没有向用户活 Agent 发送测试 prompt，也未在设备上录屏逐帧确认真实模型输出 |
| 真实关闭进程后 Expo/FCM 系统推送 | **BLOCKED：构建配置** | 真机有 Google Play Services 和 `POST_NOTIFICATIONS` 授权，但 `EXPO_PUBLIC_EAS_PROJECT_ID`、`SOLCO_GOOGLE_SERVICES_FILE` 未配置；没有完成 Firebase Android app + FCM v1 与 EAS 配置；本次不可宣称系统通知真正送达 |
| Remote Desktop 远程操控/屏幕权限 | **未验收** | 需要明确发起屏幕共享会话、系统捕获/控制授权；本轮只读页面与基础连接，不触发用户窗口操控 |

## 结束时状态

- Mac Solco GUI：已恢复运行，原 Tailscale Host 端口由 GUI 持有。
- `solco-hostd`：launchd 常驻启用，GUI 占有端口时按租约礼让；GUI 退出后能自动接管。
- Android：独立 Release APK 已覆盖安装，原有配对保留；Wi-Fi 恢复到之前的开启状态，`wlan0` 恢复有效地址。最终 PID 18107，前台仍在 Solco，通知权限已授予。
- OMP/Pi：两个只读 Companion 文件已安全安装，但**没有重启任何已有 Agent**。
- Git：并行开发中的未提交 System Push / Assistant 工作保留，不混进本轮安装证据。

## 后续最小闭环

1. 将 Android 的 Expo EAS Project UUID 与 Firebase `google-services.json`（仅本机，不入 Git）配置好，再通过 EAS Credentials 配置 FCM v1，然后构建 Release 测试真实系统推送 Ticket → Receipt → 手机锁屏展示；确认设备是否允许后台 push。
2. 在隔离 Herdr Agent Pane 上，使用真实 Android 客户端观看 2 次 OMP/Pi 受控 prompt 的逐段 Chat 增量、缺帧后的 Snapshot、Session 切换，并记录时延/帧回退；不得向现有用户 Agent 发送任意测试文本。
3. 远程桌面另走用户批准的屏幕采集权限与实际触控流程；避免把“页面打开”误标为“远程操控完成”。
4. 若需保证真实安装包包含持续开发中的 System Push 代码，待该分支实现/契约同步与源码测试稳定后**再构建并覆盖安装两端**；当前安装时点不等于之后的未提交代码快照。
