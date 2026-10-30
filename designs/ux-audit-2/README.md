# Design Brief: UX audit 第二轮（2026-10-30）

> 工作流：`/think`（Triage）+ `/interactive-design-exploration`。预览：直接打开 `exploration.html`（`file://`，无服务器）。
> URL hash 预置：`#t=keys&keys=A&keysView=sheet`、`#t=sess&sess=B`、`#t=clean&clean=A&ahead=2`、`#t=bugs`。

## Decision

| 字段 | 内容 |
|------|------|
| **Status** | `Approved 2026-10-30` |
| **Chosen** | 议题 1 = **A**；议题 2 = **A** + 按天分组并去掉冗余单组头；议题 3 = **A**；议题 4 两个 Bug 都修 |
| **实现授权** | 是（2026-10-30，「授权，直接实现并覆盖安装」） |
| **实现偏差** | 议题 1：Tab 菜单不含 Next/Previous Tab（⌃⇥ 是 Agent 切换器，不是 Tab 轮换）；速查表“Navigation palette ⌘P”改为真实的 Search ⌘K，并补 Switch Agent ⌃⇥；Tab 提示只放进 tooltip（不在 chip 内常驻写键）。议题 2：键盘 → / ← 不再折叠信息卡（Enter = 打开会话），Trash 里仍是点击折叠。议题 3：最近提交取 HEAD 第一父链。 |

## 调研（真机巡检 + 代码取证）

巡检了 Workspace（Tab 栏模式）/ Changes 干净态 / Sessions / Files / Hosts / Quota / Settings / 菜单栏。

- `app_menu.go`：只有 ⌘1 / ⌘, / ⌘W / ⇧⌘P；Go 菜单的 Changes/Sessions/Files/Hosts 无加速键；Tab 菜单不存在；无 Help 菜单。
- `shortcuts.go` / `switcher.go`：只有 Ctrl+Tab 轮换；Tab 栏的“＋”、各 Tab 无快捷键提示。
- Sessions 行：点击只展开“调试信息”（`cd … && omp --session …`、Session ID、文件路径）；“This project”下多一条重复的单组头；Codex 标题被 `# AGENTS.md instructions for …` 污染。
- Changes 干净态：左侧一列 “0 files” 空栏 + 分隔线，中间一条横幅，其余空白；`↑2` 未推送只在标题栏出现，页面主体不提。
- Workspace 行：Tab 栏模式下 `selectedTabID` 恒非空，`project_tree.go:47` 的 `selectedTabID == ""` 永不成立。

## 分诊表（Triage）

| # | 发现 | 证据 | 分类 | 处理 |
|---|------|------|------|------|
| 1 | 当前 Workspace 在侧栏不高亮（Tab 栏模式） | `project_tree.go:47` | Bug（上一轮回归） | **议题 4，直接修** |
| 2 | Codex 会话标题是注入的 AGENTS.md 指令 | `history/parse_utils.go injectedUserContent` 缺 `# AGENTS.md instructions for` | Bug | **议题 4，直接修 + 测试** |
| 3 | 无 Tab 快捷键、Go 菜单无加速键、无速查表 | `app_menu.go`、`shortcuts.go` | 设计问题 | **议题 1（待选）** |
| 4 | Sessions 行点击=看调试信息；真正的“读 / 继续”被藏在行尾；冗余单组头 | `page_history.go`、`history_info.go` | 设计问题 | **议题 2（待选）** |
| 5 | Changes 干净态空栏 + 大片空白 + 不提未推送 | `gd_surface.go`、`git_review_split.go` | 设计问题 | **议题 3（待选）** |
| 6 | Git 工具在标题栏 | 2026-10-30 已拍板 | **Keep** | — |
| 7 | Quota “Reading…” 闪烁、Tab 标题截断、Settings 无搜索 | 真机截图 | Cosmetic | 记录，不做 |

## 议题与方案

| 议题 | A | B | C | 推荐 |
|------|---|---|---|------|
| 1 快捷键 | 不动 ⌘1（Workspace），Changes/Sessions/Files/Hosts = ⌘2–5；Tab：⌘T 新建、⌥⌘1–9 选择；Go/Tab 菜单带加速键 | 浏览器式：⌘1–9 选 Tab，页面改 ⌃1–5（终端内可能被占用） | 不加键，只加 Help ▸ Keyboard Shortcuts（⌘/）速查表 | **A**（速查表可叠加，ctrl 组合需真机验证） |
| 2 Sessions 行 | 点击=阅读；Continue 悬停可见；信息收进 ⓘ 小卡 | 分栏预览（像 Mail），Continue 在预览头部 | 保持现有展开，只做清理（命令默认折叠、按天分组） | **A**（按天分组可叠加） |
| 3 Changes 干净态 | 状态页：Push N commits 主按钮 + 最近提交 + 去 Commits/终端 | 直接落到 Commits，顶部一条状态条 | 只去掉空栏与分隔线（最小改动） | **A** |

## 约束清单

- **必须保留**：Herdr 为运行时权威；不新增 Herdr 方法；Git 工具在标题栏；统一状态槽位；控件脸只走 `controls.go`；tooltip 只走 `tipped`，快捷键提示取自真实绑定。
- **必须对齐**：`theme.go` / `design_system.go` token；系统 `NSMenu`。
- **非目标**：不改 Settings；不动 Quota；不新增 Herdr 协议；history 域保持只读（Continue 走 Herdr）。
- **实现落点**：`app_menu.go`、`shortcuts.go` / `switcher.go`、新速查表 + Help 菜单（议题 1）；`page_history.go` / `history_info.go` / `history_detail.go`（议题 2）；`gd_surface.go` / `git_review_split.go` / `sidebar.go`（议题 3）；`project_tree.go`、`internal/history/parse_utils.go`（议题 4）。

## Verification（实现阶段）

`GOTOOLCHAIN=auto go test ./...`、`GOTOOLCHAIN=auto go tool mygo vet .`、`GOTOOLCHAIN=auto go tool mygo build`、`git diff --check`、真机逐项对照原型（含 ⌃/⌥ 组合在终端内的冲突检查）。
