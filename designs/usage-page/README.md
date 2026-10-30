# Design Brief: Usage 页面（Sessions 下的额度/Token 用量）

## Decision

| 字段 | 内容 |
|------|------|
| **Status** | `Approved 2026-10-30`（实现授权待最后确认） |
| **Chosen** | Option A — Cards |
| **凭据策略** | 批准：逐 Provider 开启、仅发往该厂商、Claude Keychain 显式同意、凭据只进 `mygo.Secrets`。补充：Cursor 可读取 Cursor.app / cursor-agent 自身的 user token |
| **分期** | 一次做完 6 家 + Settings（不分 P1/P2） |
| **首版排除** | 费用估算（$）、用量阈值通知、后台自动刷新（仅页面打开时刷新） |
| **历史趋势持久化** | 排除：不记录历史数据，只显示本地日志推出的 30 天 Token 柱图 + 当前额度 |
| **实现授权** | 是（2026-10-30，按上述范围） |

---

## Problem

在 Sessions 视图的侧栏加一个 **Usage** 入口与页面：读取本机已有 Agent 的登录凭据 → 读取各家额度 → 用图表呈现；另有设置页，供需要独立 accessToken 的渠道使用。首批：Claude Code、Codex、Cursor、Zhipu GLM、GitHub Copilot、Antigravity。

决策点：**页面用什么结构组织「6 家 × 多个限额窗口 × 30 天用量」？以及凭据读取策略是否可接受？**

---

## Phase 0 — 调研摘要

### 仓库现状

| 项 | 发现 |
|----|------|
| 入口 | Sessions 的 `historyNav`（`internal/nativeui/history_scope.go`）：This project / All sessions，Usage 作为第三行 |
| 页面标题 | 只在标题栏（`route_header.go`）；页面 Actions 是最前面的 `⋯`；每个内页最多一个 Refresh（侧栏项目头图标） |
| 设置 | `settingsSections`（`page_settings.go`）→ 新增 `/settings/usage` |
| 本地 Token 用量 | `internal/history/usage.go` 已有 `ReadUsage`（Claude/Codex/Pi/OMP；其他 Agent 没有 Token 数） |
| 凭据存储 | `mygo.Secrets`（Keychain，≤2560 B）是成熟 owner，不自造 |
| 禁区 | "cloud accounts, telemetry"；日志不得含凭据/正文；不得第二个 runtime |
| 文档 | `docs/client-product-architecture.md` 无 usage/quota 相关内容，需新增产品决策 |

### 竞品调研（CodexBar / ClaudeBar / UsageNow）

可迁移的机制：

1. 每个限额是一个**窗口**（5 小时 / 每周 / 每月），显示已用 %、重置倒计时。
2. **节奏标记**（pace）：条上画「按时间均匀消耗应在此处」的刻度，并外推「按此速度何时耗尽」。
3. 诚实的陈旧/未知状态：失败时保留上次值并标注"Showing last values from …"，不显示 0。
4. 「限额」与「活动」分层：限额来自厂商，活动（30 天 Token）来自本地日志。
5. 零凭据优先、逐 Provider 开启、凭据入 Keychain、只发给该 Provider 的主机。

### 本机实测的额度来源

| Agent | 来源 | 凭据 | 备注 |
|-------|------|------|------|
| Codex | 本地 `codex app-server` JSON-RPC `account/rateLimits/read` | **无** | 本机已验证，真实值已用于原型 |
| Antigravity | 本地 127.0.0.1 language server `GetUserStatus`/`RetrieveUserQuotaSummary` | **无** | 默认离线读 Antigravity 自存 OAuth 登录（需同意，风险自负）；否则仅 App 运行时可用 |
| Claude Code | `GET api.anthropic.com/api/oauth/usage` | 读 Keychain `Claude Code-credentials`（只读、不刷新） | Anthropic 限流严格，缓存约 15 分钟；走本地代理时无额度，显示 Custom endpoint |
| Cursor | `cursor.com/api/usage-summary` | Access token（cursor-agent 登录或手填） | |
| GitHub Copilot | Copilot 内部 API | `gh auth` 或 PAT | |
| Zhipu GLM | `api.z.ai` / `open.bigmodel.cn` 配额接口 | API Key（手填，Keychain） | 站点可选（国际/国内） |

### 约束

- **必须保留**：Herdr 为 runtime owner；Usage 只读；`internal/usage` 只做探测/缓存，不含 UI；凭据只入 `mygo.Secrets`；日志只记 provider 名、状态、耗时。
- **必须对齐**：`controls.go` 控件、`tipped()` 提示、Title Case 按钮、标题只在标题栏。
- **非目标**：费用估算（$）、用量通知、历史趋势存储、云端账号、遥测、CodexBar CLI 依赖。
- **预计落点**：`internal/usage/`（新）、`internal/nativeui/page_usage.go`（新）、`history_scope.go`、`page_settings.go`、`internal/settings/settings.go`、`internal/nativeui/CLAUDE.md`、`AGENTS.md`、`docs/client-product-architecture.md`。

---

## Options explored

文件：`exploration.html`（`file://` 直接打开，右栏切换 Variant / Scene / Theme / 窗口宽度 / 交互状态）。

| ID | 名称 | 核心差异 | 优点 | 缺点 |
|----|------|----------|------|------|
| A | Cards | 一页 6 张卡，每卡含全部窗口 + 30 天迷你柱图 | 一眼看全；信息密度高；最贴近 CodexBar 菜单 | 窗口多的 Provider 卡片偏高；无法深看单个窗口 |
| B | Master–detail | 侧栏 Usage 展开 6 个 Provider 子项（带迷你条），主区是单个 Provider 的环形仪表 + 节奏折线 + 活动柱图 + 按模型表 | 单家可深看（节奏图、模型分布） | 需要逐家点开；侧栏被占用更多；全局视图弱 |
| C | Overview + Drawer | 紧凑表格行总览，点行右侧抽屉看详情 | 6 家并排比较最清晰；窄窗口友好 | 抽屉里的图表空间受限；多一层交互 |

状态：Full / This Mac as-is / Errors / Empty / Refreshing；⋯ 菜单（Used/Left、Usage Settings…）；Tooltip；Keychain 同意对话框；Settings（逐 Provider 开关、token 输入/测试/保存、站点选择）。

截图：`preview-a-full.png`、`preview-b-full.png`、`preview-c-full.png`、`preview-a-mac.png`、`preview-a-error.png`、`preview-a-empty.png`、`preview-a-light.png`、`preview-a-narrow.png`、`preview-b-narrow.png`、`preview-menu.png`、`preview-tip.png`、`preview-settings-token.png`、`preview-consent.png`。

### 原型里已固化的设计规则

- 节奏外推只在窗口已过 **≥15%** 后给出（避免刚重置时的噪声预测）；陈旧/出错的数据**不显示**节奏。
- 失败保留上次值 + 红框说明 + 动作按钮（Retry Now / Update Token…），不清零。
- Claude 走自定义端点时明确显示 "Custom endpoint · local session logs only"，不假装有额度。
- Claude Keychain 读取前先有 Solco 自己的同意对话框，系统再弹一次确认。

---

## 推荐

**A（Cards）为默认页，带 C 的"紧凑行"作为窄窗口回退**；B 的节奏折线/按模型表作为后续「Provider 详情」可选增强，不进 P1。理由：6 家的核心问题是"谁快满了、何时重置"，A 一屏回答；B 把答案藏在点击后。

最脆弱的假设：**各厂商的私有额度接口保持稳定。** 若失效，该 Provider 退化为"Unavailable + 本地 Token 活动"，其余 Provider 不受影响（每家独立探测、独立缓存、独立错误态）。

### 分期（每期可独立合并）

- **P1 零凭据**：Codex + Antigravity 额度，Claude/Codex 的本地 30 天 Token 活动；Usage 行与页面；不涉及 Settings。
- **P2 有凭据**：`/settings/usage`；Claude（Keychain 同意）、Cursor、Copilot、Zhipu；`mygo.Secrets`。

---

## 与现有规则的冲突点（需用户决策）

AGENTS.md 的 "Still forbidden: cloud accounts, telemetry" 与"读取本机凭据并调用厂商 API"不直接冲突（无 Solco 账号、无上报），但这是**新的出站网络调用 + 读取第三方凭据**，需要在 AGENTS.md / architecture 里记录一条产品决策：仅在用户逐 Provider 开启后访问；仅发给该 Provider 主机；Claude Keychain 需显式同意；凭据不落日志、不落 settings 文件。

---

## 验证

```sh
# 原型冒烟（12 项）
cd designs/usage-page
agent-browser open "file://$PWD/exploration.html"
agent-browser eval "$(cat smoke.js)"   # 期望: "PASS 12 checks"
```

实现期（定稿后）：`GOTOOLCHAIN=auto go test ./...`、`GOTOOLCHAIN=auto go tool mygo vet .`、`GOTOOLCHAIN=auto go tool mygo build`，加 `internal/usage` 的探测解析用录制响应的表驱动测试，以及真机核对 Codex 额度与 `codex` 自身输出一致。

---

## Agent skill checklist

- [x] Phase 0 调研（仓库 + 竞品 + 本机探测）
- [x] Phase 1 框定（方案 A/B/C）
- [x] Phase 2 交互 HTML 与截图
- [x] Phase 3 用户选型（A Cards，2026-10-30）
- [x] Phase 4 实现（internal/usage + internal/history/usage_daily.go + nativeui usage_*.go / page_usage.go）
