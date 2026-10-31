# Design Brief: UX audit（2026-10-30）

> 工作流：`/think`（Triage）+ `/interactive-design-exploration`。预览：直接打开 `exploration.html`（`file://`，无服务器）。
> URL hash 预置：`#t=status&status=B&review=blue`、`#t=cmd&cmd=C`、`#t=empty&empty=B&page=hosts`、`#t=pop&pop=A`。

## Decision

| 字段 | 内容 |
|------|------|
| **Status** | `Approved 2026-10-30` |
| **Chosen** | 议题 1 = **A**（形状=种类，服务=圆角方块；Agent「待审阅」保持绿色圆点）；议题 2 = **A**（Scope 芯片，⇥ 轮换）；议题 3 = **B**（继续上次 + 建议；Changes 干净态合一）；议题 4 = **A**（浮层 440 宽；分组 Needs you / Working / Idle）；小项一并做（陈旧条、空 Projects 折叠） |
| **实现授权** | 是（2026-10-30，「授权，直接实现并覆盖安装」） |
| **实现偏差** | ⌘K 芯片只做 All / Agents / Workspaces / Commands：Files、Sessions 要读文件系统/目录，违背「打开面板零 IO」，保留各自页面的搜索框；行内「Agent:」等标题前缀改为行尾种类标签 |
| **2026-10-31 修订** | 议题 4 改选 **C**（用户拍板）：Agents 为主——页签名居左，Agents/Quota/Services/Tasks 四枚图标常驻右上角（点 Agents 即回列表），Needs you/Working/Idle 过滤芯片带计数单选可取消；浮层宽 440 → 340；Quota 页签迷你卡改一行一张（已实现，`quick_panel.go`/`quick_panel_quota.go`） |

## 调研（真机巡检 + 代码取证）

巡检了 Workspace / Changes / Sessions / Files / Hosts / ⌘K / Header 浮层 / Settings（Computer Use 截图）。

## 分诊表（Triage）

| # | 发现 | 证据 | 分类 | 处理 |
|---|------|------|------|------|
| 1 | **绿点同色异义**：Agents 里绿点=Ready for review，Workspace 里绿点=服务在跑 | `operational.go` `opReadyForReview→ToneSuccess`；`service_index.go` 服务 `ToneSuccess` | 设计问题 | **议题 1（待选）** |
| 2 | ⌘K 无类型/快捷键提示、Agent 行冗余 “Agent:”、不含 Workspace/文件/会话、DEVICES 区头被页脚压住 | 真机截图；`command_center_ui.go` | 设计问题 | **议题 2（待选）** |
| 3 | Files/Hosts 空态整页空白；Changes 干净时四处都在说“没事”；同名 Host 无法区分 | 真机截图；`page_files.go:54`、`page_hosts.go:148`、`gd_surface.go gdChangedPill` | 设计问题 | **议题 3（待选）** |
| 4 | Header 浮层 4 个 Tab 被截成 “Age… / Servic…” | 真机截图（我上一轮引入的回归） | Bug + 设计 | **议题 4（待选宽度策略）** |
| 5 | Changes 干净态残留“Files changed on disk”陈旧条 | `staleBanner` 在无差异时仍显示 | Bug | 选定议题 3 后一并修（隐藏条件 = 无 diff） |
| 6 | Hosts 无过滤框 | `page_hosts.go` 仅分组树 | Accepted improvement | 随议题 3 B/C 一并做 |
| 7 | 侧栏 Projects 区“0 / No projects yet”常驻占高 | 真机截图 | Accepted improvement | 无任务且插件未启用时折叠（无需设计） |
| 8 | Sessions 侧栏的 Quota 行 | — | **Keep**（2026-10-30 用户拍板的位置） | — |
| 9 | Tab 标题 / Pane 头标题 / TUI 自带标题三处重复 | 真机截图 | Cosmetic | 记录，不做 |

## 议题与方案

| 议题 | A | B | C | 推荐 |
|------|---|---|---|------|
| 1 状态词汇 | 形状=种类（服务=方块） | 端口芯片 `:3000` | 只加图例 | **A**，Review 是否改蓝单独选 |
| 2 ⌘K | Scope 芯片 | 前缀模式 | Recent 优先 + 行尾提示 | **C**（A 可叠加） |
| 3 空态 | 现状 | 继续上次 + 建议 | 自动打开上次 | **B**（Changes 干净态合并一块） |
| 4 浮层 Tab | 加宽 440 | 仅选中项带文字 | Agents 为主 + 芯片 | **A**；分组名 → Needs you |

## 约束清单

- **必须保留**：Herdr 为运行时权威；不新增 Herdr 方法；Quota 页位置；统一状态槽位 `status_indicator.go`；控件脸只走 `controls.go`；tooltip 只走 `tipped`。
- **必须对齐**：`theme.go` / `design_system.go` token（字号 12/11/10.5、圆角 Row 8）。
- **非目标**：不改 Settings、Sessions 页；不做多主题新增。
- **实现落点**：`status_indicator.go`（议题 1）、`command_center_ui.go` + `internal/commandcenter`（议题 2）、`page_files.go` / `page_hosts.go` / `gd_surface.go` / `internal/sshconfig`（议题 3）、`quick_panel.go`（议题 4）。

## Verification（实现阶段）

`GOTOOLCHAIN=auto go test ./...`、`GOTOOLCHAIN=auto go tool mygo vet .`、`GOTOOLCHAIN=auto go tool mygo build`、真机逐页对照原型。
