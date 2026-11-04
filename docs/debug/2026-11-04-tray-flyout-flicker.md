# tray-flyout-flicker 取证循环
- 状态: 已复验关闭（用户 2026-11-04 实机通过；修复见 commit 5c19af7）
- 循环: 第 1 轮

## 现象（用户原话 + 具体操作路径）

用户：使用多个屏幕，如果不是在主屏幕点击托盘的图标，状态栏点击图标后出现的浮层会不停闪动。
追问确认：
- 复现情形 = **全屏 App 占据该屏时**（点击被全屏占据的非主屏菜单栏托盘图标）。
- 闪动形态 = **整个面板消失又出现**（开→关→开循环）。
- 无全屏占据时点击不闪（"这次没闪"，间歇性即场景依赖）。

## 环境

- 仓库: /Users/wilson/Workspaces/wh-studio/solco（main, ahead 56）
- 框架: vendored mygo v0.6.4 + deltas（Delta 3 = multi-display tray flyout anchoring）
- 显示器: 0=内建 Retina 1800×1169 (MAIN)；1=外接 1920×1080 左 (x −1920..0)；2=外接 1920×1200 右 (x 1800..3720)
- 用户活实例 pid 55886：主窗口在左屏 (−1704,47) space 544
- 沙箱: $HOME/.cache/solco-tray-flicker（独立 bundle id com.whstudio.solco.traydebug 的官方构建 + 探针 worktree）
- 进度文件: docs/debug/2026-11-04-tray-flyout-flicker.md

## 证据（append-only，带时间戳）

- 18:35 沙箱隔离实例运行成功（unpackaged 名字 Solco-dev 避开单实例锁；后改用 worktree 官方构建 traydebug id）
- 19:0x 探针（TRAYDBG→applog stderr）：副屏点击路径 recordClick 命中 screen=1、anchor (−757,1144)、trayAnchorWin 创建于左屏、popover 打开于左屏 (−928,926,366×218) popScreenIdx=1 —— 多屏锚定路径本身工作正常
- 19:1x **测量陷阱**：终端 `screencapture` 无屏幕录制 TCC 权限，非本 app 窗口全部被剥离 → 一度误判"popover 不渲染"；cua-driver get_desktop_state（有权限）证实主屏 popover 渲染完全正常（Agents 面板像素完整）
- 19:2x 真实 HID 点击（cua-driver desktop click，desktop-pixel→screen-point ÷2 映射）在左屏打开：窗口服务器采样（40ms CGWindowList）显示正常开动画后**稳定**，无开合循环 —— 普通 Space 场景无闪动（与用户"这次没闪"一致）
- 19:3x 用户确认：闪动只在**全屏 App 占据所点屏幕**时发生，形态为面板消失又现
- 19:4x 机制定位（代码层）：
  - `createFlyout` (window.go:228-230) popover 分支提前 return；AppKit 托管的 popover 窗口从未拿到非 popover 飞出面板在 :241 的 `collectionTransient|collectionIgnoresCycle|collectionFullScreenAuxiliary`
  - `trayAnchorWin` (Delta 3) 有 `MoveToActiveSpace`+Auxiliary，降生于被点屏幕的**当前 Space = 全屏 Space**
  - `showPopover` 调 `activateIgnoringOtherApps`（面板要键盘，保留）→ 激活把该屏 Space 翻到 Solco 窗口所在 Space → 锚+面板滞留隐藏的全屏 Space → 面板消失；popover 是 key window → 系统又把其 Space 拉前 → 再翻转 → **面板消失又现循环**
  - 主屏路径锚 = 真实状态项按钮（status-bar 层，全 Space 可见）→ 无 Space 搏斗 → 不闪（解释主/副屏不对称）
- 19:5x 修复后 A/B：修复版从左屏全屏 Space（沙箱自身窗口全屏）打开 → 面板稳定停留 (−928,24)，全屏不退出，CGWindowList 采样无抖动行；未修复版同场景同样稳定（因为该场景缺"激活翻转"驱动：全屏占据者是沙箱自己的窗口）——翻转驱动无法用 санкion 工具安全合成（desktop 坐标域钳制主屏、右屏不可达；不改用户 App 的全屏）
- 19:5x worktree 门禁：`go test ./...` rc=0 全绿，`mygo vet` 干净

## 当前假设（hunt 契约）

根因是 `adoptPopoverWindow` 未给托盘 popover 窗口设置与锚窗一致的空间行为（MoveToActiveSpace+FullScreenAuxiliary），因为 `createFlyout` 的 popover 分支在窗口创建前提前 return；全屏占据所点屏幕时激活翻转 Space，面板滞留隐藏 Space 与 key-window 拉回互相打架，形成消失又现循环。

## 根因（确认后一句话）

third_party/mygo/internal/darwin/popover.go `adoptPopoverWindow`（配合 window.go:228 的 popover 分支提前 return）：托盘 popover 窗口缺 `MoveToActiveSpace|FullScreenAuxiliary`，全屏 Space 搏斗中滞留/被拽。

## 已试过

1. 假设"开合循环在多屏锚定路径" → 探针+真实点击+窗口采样 → 排除（普通场景稳定）
2. 假设"popover 不渲染" → TCC 权限伪证，cua-driver 捕获推翻
3. 假设"AXPress 与真实点击差异" → 桌面级真实点击复现打开，同样稳定 → 排除
4. 假设"Space 搏斗" → 用户确认全屏场景+消失又现形态；代码层缺口唯一且自洽 → 成立（修复落地）

## 下一步

1. ✅ 修复落地 main checkout（popover.go adoptPopoverWindow + LOCAL-PATCH.md Delta 6 + AGENTS.md six deltas）
2. ✅ worktree 门禁全绿
3. ⏳ 主仓门禁 + 交付构建（后台中）
4. ⏳ 用户实机复验：装上新构建，在全屏 App 占据的副屏点托盘 → 面板应稳定（跟随激活后的 Space），不再消失又现
5. 复验通过后：删除临时 worktree（$HOME/.cache/solco-tray-flicker/wt）与探针二进制；LESSONS.md 回写

## 回归测试说明

AppKit 窗口/Space 行为无法在 Go 单测层触达（darwin 后端不进测试进程）；回归判据 = 用户实机复验 + Delta 6 文档（重放 base 时手工保住该 guard）。已在 LESSONS.md 记录可复用的测量陷阱。

## 第二个问题：主屏点击 → 面板跳到副屏左上角（同日追加）

- 状态: 修复已落地并已装到用户实例（pid 57959），待用户实机复验

### 现象（用户原话）

「把内容放在副屏，然后在主屏幕点击时，浮层会跳到侧屏的左上角」；「跟焦点有关系。如果焦点在副屏，但是我点击了主屏，就出现」「不是全屏出现的」。

### 证据（append-only）

- 沙箱预修复二进制 + 真实桌面点击主屏图标：面板落在 L25（状态栏层）(−1964,−155,366×218)——左屏左上角外沿，与用户症状一致 → 红侧成立
- 层级事实：主屏路径面板 = L25（锚=真实状态项按钮，AppKit 把 popover 放锚窗同级）；副屏路径面板 = L101（锚=trayAnchorWin）。此前采样器只盯 L101 → 漏报主屏路径
- 机制：t.button 的宿主窗口是 AppKit 可搬移/镜像的状态栏窗口（焦点、全屏、镜像变化时换屏/落到屏幕原点条带），NSPopover 跟随锚视图 → 面板落点 = 该窗口当前位置
- 与 Delta 6 无关：未修复二进制同样跳——是 Delta 3 只处理 screens[1..] 留下的主屏路径缺口
- 测量陷阱：AXPress 触发时 recordClick 读 mouseLocation=用户真实光标（竞态污染锚点）；desktop click 像素域=主屏×2（负 x 可达左屏，超域钳 0）；状态栏图标动态漂移，点击前必须重读；用户在用机器时合成点击不可靠（光标互抢）

### 根因（一句话）

third_party/mygo/internal/darwin/menu.go recordClick 的循环从 screens[1] 起：主屏点击不被记录，popoverAnchor 回退到 t.button——其宿主窗口被 AppKit 搬移后面板跟着跑。

### 修复

menu.go 循环改为 `for i := 0; ...`：主屏点击同样记录 clickScreen+clickAnchor，多屏时 popover 一律锚定 trayAnchorWin（记录位置），面板永远出现在被点图标下方；Delta 6 的 Space 行为随 flyout.Tray 路径自动覆盖主屏。LOCAL-PATCH.md Delta 3 补 2026-11-04 extension 说明。

### 验证

- worktree + 主仓门禁全绿（test rc=0、vet 干净、安装脚本 stage 1 vet 通过）
- 沙箱绿侧自动化被用户实时鼠标竞态阻断 → 绿侧判据转为用户实机复验（真实点击 mouseLocation 天然准确）

## 复验清单（用户，pid 57959 新构建）

1. 全屏 App 占据副屏时点副屏托盘 → 面板开一次后稳定（原闪动）
2. 内容在副屏、点主屏托盘 → 面板出现在主屏被点图标下方，不跳副屏左上角
3. 副屏点副屏托盘 → 面板在该副屏图标下方
4. 普通单屏使用回归如常
