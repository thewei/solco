# Tab strip(标题栏 Tab 条:拖拽排序 + 浏览器式收缩)

> 状态:**已决策并实现(2026-11-02):A 的收缩 + B 的「All Tabs」菜单。**预览:直接用浏览器打开 `exploration.html`(自包含,三个条都能真拖拽、悬停出信息卡,滑块改 Tab 数量与窗口宽度)。

## 设计问题(一句话)

Tab 条里的 Tab 现在**不能拖拽排序**,且 Tab 一多只会横向滚动(每个 chip 固定 120–180pt、选中项 160–240pt)——要补上拖拽排序,并参考成熟浏览器:Tab 多时收起文字只留图标/状态,鼠标悬停显示足够信息,必要时展开。

## Phase 0 取证(代码现状)

| 项 | 现状 |
|---|---|
| Tab 条 | `internal/nativeui/tabbar.go`:`ScrollHorizontal` + 每 Tab 一枚 chip(品牌图/标题/pane 数/状态槽/悬停 ×),尺寸常量 `tabChipMin/MaxWidth` 120/180、选中 160/240,溢出出 ‹ ›,选中项 `ScrollIntoView` |
| 悬停信息 | 只有 `tipped(c, b, tab.Label, tipKeys(tab.Keys))`——标题 + ⌥⌘n 键帽,没有 Workspace/pane 数/状态说明 |
| 排序提交路径 | **已存在**:`internal/nativeui/reorder.go` 的 `treeNodeDrag{Kind:"tab"}` + `reorderTree` → `Manager.MoveTab` → Herdr `tab.move{tab_id, insert_index}`(只能在同 Workspace 内;侧栏 Tab 行已可拖)。预览不改 runtime,只在 Drop 提交 |
| 拖拽原语 | MyGo `Element.Drag(value)` / `Drop` 已在 `components.go:352` 用于侧栏行;Tab 条 chip 目前没接 |
| 刷新 | Herdr 的 `tab.moved` 事件已订阅(`herdr/events.go`),提交后投影自动更新 chip 顺序 |

## 方案

| | A 等分收缩(Chrome/Edge) | B 固定 + 全部 Tab 菜单(Edge/Arc) | C 手风琴(Vivaldi/Zen) |
|---|---|---|---|
| 收缩规则 | 所有 Tab 等分宽度,180→44pt(仅图标+状态点);选中项保 ≥160pt | 非选中 ≥110pt 保标题(截断);放不下尾部折进 ▾ 菜单,不滚动 | 只有选中项显示标题;其余恒 36pt |
| 读全信息 | 悬停信息卡 | 悬停卡 + 菜单(带搜索) | 悬停 ~0.35s 就地展开(推开邻居) |
| 极端拥挤 | 44pt 仍放不下才滚动 ‹ › | 菜单兜底,无滚动 | 条长几乎不变,无需滚动 |
| 拖拽 | 全条可拖,蓝线落点 | 可见区内可拖;菜单内的 Tab 要先选中 | 拖拽时全收起,幽灵跟手,最稳 |
| 额外能力 | — | 可选「固定」前 N 个(本机偏好) | — |
| 代价 | 全是图标时同一 Agent 多开难认 | 多一个入口;固定要存本机设置 | 一眼看标题最弱;悬停展开会位移抖动,要 dwell/防抖 + 宽度动画 |
| 实现成本(MyGo) | 中:自算宽度 + 复用 tipped→信息卡 + Drag/Drop | 中高:溢出菜单 + 搜索 + 固定偏好 | 高:悬停展开动画 + 拖拽时的宽度恒定逻辑 |

## 三案共同约束(不随选择变)

- Herdr 仍是 Tab 顺序的唯一来源:拖拽只在 Drop 调 `tab.move`(同 Workspace),**不**跨 Workspace、**不**做第二套 Tab 状态。
- 状态槽沿用 16pt 单一指示器(圆 = Agent,圆角方 = 服务);× 只在悬停/选中出现;⌥⌘1–9、点击、右键菜单、新建 `+ ▾` 行为不变。
- 悬停信息卡走 `tipped(...)` 家族(nova 面),键帽仍取自真实快捷键绑定。
- 日志只记操作名/Tab ID/索引,不记标题。

## 最脆弱假设

- 「图标 + 状态点足以区分被收起的 Tab」:同一 Agent 多开时靠品牌图区分不了——三案都靠悬停补;若实际用户常开 ≥8 个同类 Agent Tab,B(保标题 + 搜索菜单)更稳。
- MyGo 的 chip 宽度动画与 Drag 在同一 ScrollHorizontal 内不冲突(现有 `tabChipMotion` 已处理增删,拖拽中的目标宽度需要实测)。

## Decision

用户选择混合方案(2026-11-02)并已实现于 `internal/nativeui/tabbar.go`:

1. **等宽收缩**(`planTabStrip`,纯函数):自然宽度放得下就不动;放不下时未选中 chip 等分剩余宽度(上限 180),选中 chip 保 160–240 保住标题;未选中 chip 窄于 96pt 时只留品牌图 + 状态槽(无标题/pane 数/×);到 48pt 地板仍放不下才横向滚动并出 ‹ ›(‹ › 只在这时出现,否则它们占的宽度会把条重新挤进溢出)。宽度取上一帧栏宽,一帧内收敛。
2. **悬停信息卡**:`tipped` 的文本 = 完整标题 · N panes · 状态,外加 ⌥⌘n 键帽。
3. **All Tabs ▾ 菜单**:任何 chip 被收缩时出现在 + 之前;列出所有 Tab(品牌图/标题/pane 数/状态槽),≥8 个 Tab 带过滤框,⏎ 选第一项。
4. **拖拽排序**:chip 复用侧栏同一份 `treeNodeDrag{Kind:"tab"}` + `reorderTree` → `Manager.MoveTab` → Herdr `tab.move`(只在同 Workspace 内,仅 Drop 提交,预览不改 runtime);拖到的 chip 左/右半区出 2pt 插入条。
5. 未做:固定(pinned)Tab——Herdr 没有该概念,留待单独决策。
