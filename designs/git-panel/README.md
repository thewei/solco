# Design Brief: Git 面板 · 布局 / Tab / 头部 / 提交列表 / 复制

> 工作流：`think` → `interactive-design-exploration`（Phase 0–3；Phase 4 在定稿后）。
> 原型：[`exploration.html`](exploration.html)（自包含，`file://` 直接打开，无构建、无外链）。
> 自检：`agent-browser eval "$(cat smoke.js)"` → `window.__smokeText`，当前 **97/97 PASS**（A/B/C 各 32 条 + 无 JS 报错）。

---

## Decision

| 字段 | 内容 |
|------|------|
| **Status** | `Approved 2026-10-30` |
| **Chosen** | Option A — 图标 Tabs + 单行头部 |
| **组合说明** | 无组合。Commits 行高 1 行（24pt）；改名 Changes / Commits；双击范围按交互规则表；复制 = `Selectable` + Copy Hunk / File Patch 兜底（不换 `ui/editor`） |
| **实现授权** | 是 |

---

## Problem

Git 面板（`/workspace` 的 Git 视图：侧栏引用树 + 中心 Local Changes / All Commits）的七处不顺：

1. `Local Changes | All Commits` 是纯文字 `segmentedControl`，没有图标，不像 shadcn 的 Tabs；
2. 侧栏头部（项目 / 分支 / 刷新）上下堆叠，占高且分支名被挤；
3. 搜索常驻占位；
4. **单击即切换分支**，不安全；
5. All Commits 的图谱列固定 76pt，提交点离右侧信息太远；
6. 整体偏松；
7. commit message 与 diff 代码无法选中复制。

---

## Phase 0 — 项目调研摘要

### 现状（代码）

| 项 | 发现 |
|----|------|
| 视图切换 | `git_sidebar_tree.go:32` `segmentedControl(c, &mode, "Local Changes", "All Commits")`，无图标 |
| 提交图谱 | `git_commit_graph.go:121-124` 图谱格固定 `Width(76) × gitGraphRowH 26`，不随实际车道数收缩 |
| 导航栏收起 | `git_review_split.go:25` `gitFilePaneCollapseBelow = 750`（原型已镜像：中心宽 < 750 默认收起文件导航，宽/窄各自记忆） |
| 激活语义 | `sidebarTreeRow` 返回 `activated \|\| row.Clicked()`；`listNav` 把 Enter/Space 当作点击 → 选中与激活需拆成两个状态 |
| 可复用 | `Element.DoubleClicked()`（`terminal.go`、`surface_header.go` 已用）、`Selectable()/CopyData/SelectedTexts/ContextMenu`、`segmentedTrack/segmentFace`、`switcherChip` + `ui.Popover`、`tipped` |
| 命名冲突 | Solco 侧栏顶部已有 **Sessions**（Agent 对话 *history*，时钟图标 `iconHistory`）。Git 视图叫 "History" 会撞名 → 原型一律用 **Commits** + git-commit 图标 |

### 成熟产品对照（**来自通用经验，未实机核对**；Fork 实机取证超时放弃）

| 点 | 做法 |
|----|------|
| 切换分支 | GitHub Desktop / Tower / Fork：**双击**或显式 Checkout 才切换；单击只选中 |
| 提交列表 | Fork / GitKraken / Sublime Merge：图谱列宽随车道数自适应，圆点紧贴 subject；ref 以 pill 前置；作者/时间/hash 右对齐成列；窄时先丢列 |
| 搜索 | VS Code / Fork：图标按钮 → 展开输入，Esc 收起 |
| Diff 复制 | GitHub / VS Code：行号不入选；右键 Copy；Fork 支持复制 patch |

### 约束清单

- **必须保留**：Herdr 为运行时权威；Git 走 `internal/gitworkbench`，不新增 runner / Herdr 方法。
- **必须对齐**：`design_system.go` token（Body 12 / BodySmall 11 / Caption 10.5；Radius Control 6 / Row 8；Control Compact 24 / Regular 28）；控件只经 `controls.go`；tooltip 只经 `tipped()`；图标走 `icons.go`；菜单仍是系统 `NSMenu`。
- **非目标**：Split/Unified 实际渲染（现为占位）、新增 Git 操作、改 Herdr。
- **实现落点**：`git_sidebar_tree.go`、`sidebar_switchers.go`、`sidebar_tree.go`、`git_review_split.go`、`git_workbench_actions.go`、`sidebar.go`（`commitsPane`）、`git_commit_graph.go`、`gd_surface.go`、`ds_components.go`（`segmentedTrack`）、`icons.go`、对应测试与 `CLAUDE.md` 头部。

---

## 方案

三个方案共享下方「共同提案」，只在 **视图切换 + 头部** 的结构上不同。

| | A · 图标 Tabs + 单行头部 | B · 中心工具条 Tabs | C · 侧栏 Rail |
|---|---|---|---|
| 切换位置 | 侧栏顶部 shadcn 式 Tabs（图标 + 文字 + Changes 计数） | 中心工具条左侧 `ltab` | 侧栏引用树上方两行 `srow`（含 Commits 总数） |
| 头部 | 32pt 单行：项目芯片 / 分支芯片 / 搜索 / 刷新 | 两行（63pt），项目与分支分层 | 32pt 单行 |
| 优点 | 改动最小；切换永远在同一位置；窄侧栏下项目收成图标 | 侧栏只剩引用树，最干净 | 计数最醒目，像 Mail/Notes |
| 代价 | 侧栏头部三行（头/Tabs/树） | 视图切换离引用树远；中心工具条变挤 | 占两行高，违背"更紧凑" |
| 推荐度 | **推荐** | 备选 | 不推荐 |

### 共同提案（A/B/C 都包含）

1. Tab 图标化：Changes（git-compare）/ Commits（git-commit）；Changes 带未提交数。
2. 头部单行；侧栏 ≤ 236pt 时项目芯片收成文件夹图标（分支名优先）。
3. 搜索 = 图标按钮 → 展开输入，Esc 收起；侧栏搜引用、中心搜文件 / 提交（各自 scope）。
4. **单击只选中，双击 / Enter 才执行**（见下表）；首次单击给一次性提示「Double-click to check out X」。
5. 提交列表：图谱列自适应（原型 30pt，原 76pt），点→subject 间距 < 40；ref pill 前置；作者 / 时间 / hash 右对齐；搜索时降级为圆点；行高 1 行 24 / 2 行 40（可选）。
6. 复制：diff 代码可拖选，行号 `user-select:none`；右键 Copy Selection / Line / Hunk / File Patch；commit message 可选，附 Copy Message / Copy Hash / Copy Subject；commit 正文超 3 行折叠。
7. 提交栏闲置时单行，聚焦才展开；工具栏去掉重复的 Stage All（节头已有）；文件列表去掉重复的 "Local Changes" 标题与 Total 页脚。
8. 窄窗口：文件导航 < 750 自动收起；作者 / 日期列按容器宽度隐藏。

### 交互规则（单击 / 双击）

| 对象 | 单击 | 双击 / Enter |
|------|------|--------------|
| 本地分支 | 选中（+ 一次性提示） | 切换；**仅工作区脏时**弹确认 "Switch Branch With Local Changes?"（干净时直接切，与现行为一致） |
| Worktree | 选中 | 在 Finder 打开 |
| Remote / Tag / Stash | 选中 | 复制引用名（"定位到 Commits" 延后，Solco 暂无 ref→commit 定位入口；Stash 的其余动作走右键菜单） |
| 目录 | 折叠 / 展开（不改仓库，保持单击） | — |
| 文件（改动文件树） | 选中并滚动到文件（保持） | — |
| 分支下拉（Popover） | 单击即切换（菜单语义，保持） | — |

---

## 风险与脆弱假设

1. **`Selectable()` 作用于虚拟化 `ui.List`**：只覆盖已构建的行，跨屏拖选不完整。缓解：右键 Copy Hunk / File Patch 不依赖选区；更大的备选是改用 `ui/editor` 只读 diff 视图（工作量明显更大，本轮不做）。
2. **命名**：把 Git 视图改称 "Commits"，需要同步测试里的 `"All Commits"` / `"Local Changes"` 断言（`git_surface_test.go`、`git_file_tree_fold_test.go`、`right_panel_git_test.go`、`bootstrap_page_test.go`）。
3. **搜索 scope**：侧栏搜引用、中心搜提交——两个入口含义不同；原型用图标 + placeholder 区分，需要真机确认不混淆。
4. **原型是 mock**：Git 数据、计时、`200 commits` 总数均为假数据；车道配色取自 `git_commit_graph.go`。
5. **Split / Unified** 仍是占位，不在本轮范围。
6. **脏时确认**：沿用 Git 自身"冲突则拒绝"的语义，Solco 不自作主张 stash。

---

## 实现交接（选型后）

- 验证：`GOTOOLCHAIN=auto go test ./...`、`GOTOOLCHAIN=auto go tool mygo vet .`、`GOTOOLCHAIN=auto go tool mygo build`、`git diff --check`；用 `SOLCO_GIT_LAYOUT_EVIDENCE` 渲染 Local Changes / Commits 对照原型截图；Computer Use 实机：单击分支不切换、双击才切换、diff 拖选 ⌘C 无行号、窄侧栏头部。
- 每个视觉属性走 token / `controls.go` / `segmentedTrack`，不在业务处 inline 覆盖。

## 截图

| 文件 | 内容 |
|------|------|
| `preview-a-changes.png` / `preview-a-commits.png` | 方案 A · Changes / Commits |
| `preview-a-commits-2line.png` | 两行提交行 |
| `preview-b-*.png` / `preview-c-*.png` | 方案 B / C |
| `preview-c-clean.png` | 工作区干净的空状态 |
| `preview-a-light.png` | 浅色 |
| `preview-a-narrow-*.png` / `preview-a-sidebar200.png` | 760 窄窗口 / 200 窄侧栏 |
| `preview-a-search-*.png` | 搜索展开（侧栏 / 提交） |
| `preview-a-state-*.png` | 单击提示 / 双击确认 / 选区复制 / 右键菜单 / 分支弹层 |
