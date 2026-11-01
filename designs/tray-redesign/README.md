# Design Brief: Tray 图标 + 状态精简（2026-10-31）

> 工作流：`/think` + `/interactive-design-exploration`。预览：直接打开 `exploration.html`（`file://`，无服务器）。

## Decision

| 字段 | 内容 |
|------|------|
| **Status** | `Approved 2026-10-31` |
| **Chosen** | Option B — N/M 比例 + 新盘羊头 template 图标 |
| **组合说明** | 图标 = 盘羊头（`TrayIconPNG()` Bézier rasterize，horn 螺旋 + head 楔形 + muzzle，`>—` 负空间）；标题 = `N/M`（N = attention + review，M = status center 总条目），无条目时为空 |
| **实现授权** | 是（2026-10-31，用户回复「B」） |
| **实现偏差** | 原型的 tooltip 文案含中文「需要你」，实现沿用既有 `attentionSegments` 英文语句（`2 need attention, 1 for review, 3 working`）；图标实际以 128×128 template PNG 交付（macOS 缩到 18pt） |

## Problem

现有 tray 图标（黑色终端框）与 App logo（银色盘羊头）视觉不统一；状态标题 `⚠ 2  ✓ 1  ⚡ 3` 占据过多菜单栏空间。需要一个 logo 同源的图标 + 更紧凑的状态显示。

## Phase 0 — 项目调研摘要

### 现状（代码）

| 项 | 发现 |
|----|------|
| 渲染入口 | `internal/nativeui/tray.go`（`TrayIconPNG()` 生成 32×32 黑色终端框 PNG，`SetTitle` 写入状态文本） |
| 数据 / 状态来源 | `internal/nativeui/status_center_model.go` → `StatusCenterSnapshot`（NeedsAttention / ReviewPending / Working / Interactions / Entries） |
| Tray API | MyGo `Tray.SetIcon(png, template bool)` + `SetTitle(string)` + `SetToolTip(string)`。**不支持自定义 NSView / 两行文字 / 彩色 title** |
| Template 模式 | macOS template icon = 单色剪影，浅色菜单栏自动转黑，深色自动转白。彩色 icon 需 `template: false` 并渲染两种色 |
| Logo 参考 | `resources/icon.png`：银色金属质感盘羊头，面部带终端提示符（`>—`），horn 从头顶延伸到左前方卷曲；原型用简化路径：horn 螺旋 stroke + head 楔形 fill + muzzle 小突起，19pt 依然可辨 |

### 约束

- **必须保留**：`StatusCenterSnapshot` 数据源不变；`TrayFingerprint` 机制不变（状态变化才重建）；点击 tray 打开 quick panel 的行为不变
- **必须对齐**：macOS 菜单栏 18pt 高度；template icon 单色约束（除非选择方案 A 放弃 template）；Solco 银色盘羊品牌
- **非目标**：不动 MyGo Tray API（不引入自定义 NSView/两行文字）；不改 quick panel 内容；不改 in-window Status Center
- **预计实现落点**：`internal/nativeui/tray.go`（`TrayIconPNG` 重写 + `TrayTitle` 新格式）、`internal/nativeui/tray_model.go`（`TrayTitle` 逻辑）、`internal/nativeui/tray_test.go`（用例更新）

## Options explored

| ID | 名称 | HTML 文件 | 核心差异 | 优点 | 缺点 |
|----|------|-----------|----------|------|------|
| A | 状态色 + 单数字 | `exploration.html`（切到方案 A） | 图标变色编码紧急度，title 只有一个数 | 空间最省，颜色 preattentive | 放弃 template，需管理浅/深色渲染 |
| B | N/M 比例 | `exploration.html`（切到方案 B） | N = attention + review（需要你），M = 总 agent 数，图标保持 template | 用户期望的 "1/2" 格式，实现最简单 | N 含义需 tooltip 解释；M 可能为 0 |
| C | 双状态紧凑 | `exploration.html`（切到方案 C） | Attention+Review 合并 ⚠N，Working 独立 ⚡M | 保留现有符号习惯，比现有少 1 组 | 仍占 2 组宽度；Review 和 Attention 混在一起不够精确 |

### 交互状态（单文件多态）

| 状态 | 如何触发 | 预期行为 |
|------|---------|---------|
| Idle | 点击「Idle」chip | 图标银色，title 空/0/5/空 |
| Working | 点击「Working」chip | 图标绿色，title 显示 3/0/5/⚡3 |
| Review | 点击「Review」chip | 图标橙色，title 显示 2/2/5/✓2 |
| Attention | 点击「Attention」chip | 图标橙色，title 显示 1/1/5/⚠1 |
| Mixed | 点击「Mixed」chip | 图标橙色，title 显示 2/2/5/⚠2 ⚡3 |
| Disconnected | 点击「Disconnected」chip | 图标红色，title 空/—/空 |

所有方案在浅色 + 深色菜单栏模拟下渲染。Tooltip hover 显示完整状态。

## Preview

直接双击或拖入浏览器打开 `exploration.html`（`file://`）。

| 文件 | 打开方式 | 看什么 |
|------|----------|--------|
| `exploration.html` | `file://.../designs/tray-redesign/exploration.html` | 顶部 icon 对比，下方 A/B/C 状态切换 |

## Success criteria（定稿前）

- [ ] 原型内可切换全部方案 / 状态
- [ ] 默认可在 `file://` 下打开（无服务器）
- [ ] 视觉与 Solco 盘羊 logo 一致
- [ ] 机制与实现一致（Tray API 只支持 SetIcon/SetTitle/SetToolTip，两行不可行）
- [x] 用户选定方案（B，2026-10-31）
- [x] README Decision 表已更新

## Implementation handoff（2026-10-31 已完成）

| 项 | 内容 |
|----|------|
| 改动文件 | `internal/nativeui/tray.go`（TrayIconPNG 重写）、`internal/nativeui/tray_model.go`（TrayCounts.Total + TrayTitle N/M）、`internal/nativeui/tray_test.go`（表更新）、`internal/nativeui/CLAUDE.md`（补 tray/status_center 成员）、`internal/nativeui/tray.go` 等 5 文件补 L3 头 |
| 验证命令 | `GOTOOLCHAIN=auto go test ./internal/nativeui/ -run Tray`, `GOTOOLCHAIN=auto go tool mygo vet ./internal/nativeui/` |
| 浏览器检查步骤 | 1. 真机打开 Solco，确认 menu bar 图标与 logo 同源；2. 触发 agent working/attention/review，确认 title 变化；3. hover 确认 tooltip |
| 与原型差异 | — |
