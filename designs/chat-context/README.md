# Chat · 上下文容量(Context window)

> 状态:**Explore 完成,等待 Select(见 Decision)**。产品代码在选型前不动。
> 打开方式:直接双击 `exploration.html`(`file://` 可用)。深链:`exploration.html#<A|B|C>/<open|warn|full|nolimit|fresh>`。

## Decision

| 字段 | 内容 |
|---|---|
| **Status** | Approved 2026-10-31 |
| **Chosen** | Option B — Breakdown + Compact(点击环打开) |
| **组合说明** | 窗口未知(Claude/Codex 不报)时**不画进度条与百分比**:只显示已用量 + 缓存命中率 + 明细;环为中性圈,Compact 不做实心强调 |
| **实现授权** | 是 |

## Problem

输入区现有的上下文环只有浏览器 `title` 悬停提示且窗口大小靠猜;要给它一张真正的卡片(参考截图:已用 / 窗口 + 进度条 + 平均缓存命中率),并决定要不要加更多信息和动作。

## Phase 0 现状(已核实)

| 项 | 发现 |
|---|---|
| 入口 | `frontend/src/App.tsx` `ContextRing`(composer 工具栏,`state.contextTokens` 为真才画) |
| 已用量 | `TranscriptMessage.ContextTokens` → `ChatBridgeState.ContextTokens`(Claude / Codex / omp / pi 转录均有) |
| 窗口大小 | 现为猜测:`>200k ? 1M : 200k`。omp/pi 的模型表带 context 列(`omp models`、`pi --list-models`,agentcatalog 已缓存 10 分钟);Claude / Codex 不报 → 只能"假定" |
| 缓存命中率 | `history.ReadUsage` → `SessionUsage`(input / output / cache read / cache write),只读、流式读一遍会话文件,无需落盘 |
| 命令 | `/compact` 已是 Claude / Codex / omp 的文本命令(agentcatalog),走现有 prompt 投递 |
| 禁令 | 无费用估算、无后台轮询、不记历史、桥接只做展示(AGENTS.md Quota 页同款边界) |

## 约束

- 必须保留:桥接只做展示;Herdr 唯一运行时;窗口未知时诚实标注,不假装精确。
- 缓存命中率 = cache read ÷ (input + cache read + cache write),**整个会话**;只在卡片打开时读一次。
- 没有任何用量(全新会话)时环不出现(与现在一致),不画 0%。
- 非目标:费用、配额(那是 /quota 页)、按类别拆分上下文占用(转录里没有这个数据)。
- 预计落点:`internal/nativeui/chat_bridge.go`(`ContextUsage` 方法)、`internal/agentcatalog`(窗口)、`frontend/src/App.tsx` + `chat.css`。

## 方案

| ID | 名称 | 触发 | 核心差异 | 优点 | 缺点 |
|---|---|---|---|---|---|
| A | Hover card | 悬停 / 键盘聚焦 | 就是参考截图:已用/窗口、进度条、平均缓存命中率 | 最轻;与截图一致;无新增控件 | 不能做动作;触屏/键盘需聚焦 |
| B | Breakdown + Compact | 点击 | A + 会话 token 明细(Input/Output/Cache read/Cache write)+「Compact conversation」按钮(≥70% 变实心) | 一眼看到为什么贵/为什么满;可直接压缩 | 信息更多;`/compact` 要真机验证各家行为 |
| C | Meter + banner | 点击环 | 环旁常驻百分比(≥70% 琥珀、≥90% 红);≥85% 时输入框上方出横幅(New session / Compact) | 主动预警,不用打开卡片 | 常驻元素 + 横幅,界面更吵;横幅是新模式 |

### 状态(原型内右侧面板可切)

| 状态 | 说明 |
|---|---|
| Closed | 默认,悬停(A)/点击(B、C)交互 |
| Open · 40% | 正常 |
| Open · 78% | 琥珀 |
| Open · 94% | 红 |
| window unknown | 窗口为假定值:数字带 `~`,卡片注明"This agent does not report one" |
| Fresh | 无用量:环不出现 |

## 截图

| 状态 | 文件 |
|---|---|
| A · 40% | `preview-a-open.png` |
| B · 78% | `preview-b-warn.png` |
| C · 94% | `preview-c-full.png` |
| A · 窗口未知 | `preview-a-nolimit.png` |

## Success criteria(定稿前)

- [x] 原型内可切换全部方案 / 状态;`file://` 打开无报错
- [x] 视觉沿用 chat.css 的 ink 阶梯、`--panel`、`--hair2`、TOC 卡片的圆角/阴影
- [x] 用户已选定方案 B;窗口未知时只显示已用量(不画条、无百分比、不写“假定值”)

## Implementation handoff(Phase 4)

| 项 | 内容 |
|----|------|
| 改动文件 | `internal/nativeui/chat_context.go`(+test)、`internal/agentcatalog/catalog.go`(窗口解析、`ModelWindow`、`IsCompact`)、`internal/history/{usage,pi,codex}.go`、`internal/nativeui/chat_ui.go`(/compact 不回显)、`frontend/src/context.tsx`、`App.tsx`、`chat.css`、`mygo.ts`(生成) |
| 验证命令 | `GOTOOLCHAIN=auto go test ./...` · `go tool mygo vet .` · `cd frontend && npx tsc --noEmit` |
| 真机检查 | 1. 打开 omp 会话 Chat 2. 点输入区的环 → 卡片 `used / window (%)`,与 omp 状态栏百分比一致 3. 点 Compact → 终端出现 compaction,Chat 无残留气泡 |
| 与原型差异 | 窗口未知时无进度条/百分比(用户决定);≥70% 才在环旁出现百分比(原型 C 的做法,用户未选常驻);悬停不开卡片,仅点击 |

