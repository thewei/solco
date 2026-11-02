# Design Brief: Services 三表面重设计（侧栏区块 + QuickPanel Services 页）

> 工作流：`interactive-design-exploration`。预览：直接打开 [exploration.html](exploration.html)（`file://`），右栏切换 A/B/C 与状态（Row hover / 无运行服务 / 浅色主题），`›` 或 Esc 收起右栏全画布演示。

## Decision


---
| 字段 | 内容 |
|------|------|
| **Status** | `Approved 2026-11-02` |
| **Chosen** | Option A — 清单平铺（Agents 同构两行卡 + 多工作区淡色组标签） |
| **组合说明** | 点击按 `panePrefersChat`（Agent→Chat、手动→Terminal）；未运行 Scripts 不进侧栏（只留浮层 Scripts 组）；Project-wide 歧义服务不进侧栏（只在浮层 Other listeners） |
| **实现授权** | 是（2026-11-02 用户选定 A 并授权 Phase 4） |

## Problem

**单一决策点**：Services 以什么行模型与分组结构出现在侧栏（Agents 下方），使「什么在跑、跑在哪个 Pane、谁启动的、怎么访问」一眼可读，并让侧栏、托盘浮层、Header Agent 浮层三处共用同一套行模型？

### 用户原始诉求 → 补全后的完整需求（含 2026-11-02 定稿标记）
| # | 诉求 | 补全/澄清（基于 Phase 0 取证） |
|---|------|------------------------------|
| 1 | 侧栏 Agents 下方增加 Services 区块，布局交互参考 Agents | 插在 `section:agents` 之后、`section:pin`/Projects 之前；区块头同款（标题+计数+折叠），行距/圆角对齐 agent 卡 |
| 2 | 显示「对应的开发服务」 | 数据 = 现有只读投影：`serviceIndex.Panes`（Pane 前台进程拥有监听端口）+ 持久 `service` Scripts（架构文档 1042 行明文允许）；**没有** uptime/健康/崩溃态，v1 只有「监听中」一种状态，不伪造 |
| 3 | 状态栏图标浮层 + Header Agent 浮层的 Services，重新设计更清晰 | 两处是**同一个** QuickPanel（`quick_panel.go`，托盘左键与 Header 各一个锚点）；重设计一次覆盖两处；现状（Scripts + 裸端口列表）替换为命名行 + 归属 |
| 4 | 点击跳转到对应 Pane 查看任务详情 | 归因已有：`serviceIndex.Panes[paneID]`；点击 → `selectPane(paneID)` 并按 `panePrefersChat` 落当前表面（Agent 卡 → Chat，手动 → Terminal），与点 Agent 卡行为一致 |
| 5 | 彩色服务图标 + 端口 | `proc_brands.go` 已有 60+ 进程品牌 SVG 与友好名（node/vite/python/go/redis…），但当前统一染成 TextMuted 单色；补一张「品牌色表」按品牌色渲染，无品牌时退回中性字形 |
| 6 | 关联 Agent 时清晰标注「Agent 开启的后台服务」 | 行内归属 chip：Agent 迷你 mark + 名字；脚本启动标「Script · 名字」；纯手动的标「Manual」；歧义 daemon（cwd 归 Project、无法定位 Pane）**定稿：不进侧栏**，只在浮层 Other listeners 出现 |
| 7 | （补全）端口访问 | 行尾 hover 出 `↗` Preview（现有 `openLocalPreviewURL(LocalPreviewURL(port))`），不再放裸域名行 |
| 8 | （补全）未运行 Scripts 的去处 | **定稿：不进侧栏**，只保留浮层 Scripts 组（Run 动作照旧），避免空跑脚本常驻噪音 |
| 9 | （补全）空态 | 「No services running」+ 一句引导；区块无行时仍保留头（与 Changes 空态页同理） |

---

## Phase 0 — 项目调研摘要

### 现状（读过的代码）

| 项 | 发现 |
|----|------|
| 渲染入口 | 侧栏区块：`internal/nativeui/sidebar.go:74-92`（Agents/Pin/Projects/Workspace，无 Services）；Agent 卡嵌套服务行已存在：`agent_card.go:441-479`（`cardService`/`serviceRow`，点击开 Terminal，v1 只读无 Stop）；浮层：`quick_panel.go` + `quick_panel_services.go:19-98`（Scripts 行 + 「Listening ports」裸端口 + Preview 按钮，340×480 固定） |
| 数据来源 | `internal/services/ports.go`（`lsof -Pan -iTCP -sTCP:LISTEN` + cwd 查找，10s stale-while-refresh）→ `internal/nativeui/service_index.go`（`sidebarServiceIndex{Panes, Projects}`，前台 PID 匹配、cwd 歧义归 Project）；每次启动重算、切实例清空；**非 Herdr 概念，无持久实体** |
| 已有交互 | 指示器：`status_indicator.go:95-105` 服务=7×7 圆角方块（绿=在跑，「形状=种类，颜色=紧急度」，2026-10-30/31 决策）；Tab bar 已显示服务方块；嵌套 serviceRow 点击开 Terminal；端口 Preview 仅浮层内 |
| 图标/token | `proc_brands.go`（品牌 SVG 单色染色 `brands.go markView` + `procFriendlyName`）；`theme.go` 暗色 `Sidebar #17171a / Panel #212121 / Text #ececee / TextMuted #8b8c92 / Success #30d158 / BorderSubtle rgba(255,255,255,.07)`，亮色对应；字阶 Micro 10 / BodySmall 11 / Body 12；侧栏行 26/30/34、树行 −2、行圆角 4、卡片圆角 10 |
| 相关文档 | `docs/client-product-architecture.md` :265（Services=Script+观测投影）、:385-391（浮层单层、Run/Preview 显式动作、禁新运行时）、:1042（侧栏 Services 区块成员资格=持久 service Scripts + 检测服务）、:948-955（区块折叠/高度本就是持久偏好位）、2026-10-30 agent-surfaces 决策 |

### 已知数据局限（设计不越界）

- 每 Pane 只投影**一个**前台服务进程（`paneServiceActivity{Proc, Ports}`）+ 其全部端口；同 Pane 多服务会折叠为一行。
- 无 starting/crashed 状态机、无 uptime/健康检查、无停止/重启（v1 只读，不建第二监视器）。
- 归因是启发式（PID/cwd），非数据；歧义时只有 Project 级归属。

### 约束

- **必须保留**：`statusIndicatorService` 指示语言（形状=种类）；只读 macOS 投影 + root 限定；浮层单层无详情页；`paneServiceActivity` 每 Pane 一行的现实；`cardService` 嵌套行为（Agent 卡内服务行不删，统一行模型）；扫描节拍（10s/5s debounce/8s 超时）不动。
- **必须对齐（视觉）**：上表 theme.go token、`tipped` tooltip、`compactButton`/segmentTabs 面、Micro/BodySmall 字阶、树行圆角 4。
- **明确非目标**：Stop/重启按钮、健康检查、uptime、远程设备（`instance_remote`）服务、端口编辑/收藏、新 Herdr 方法、第二个扫描循环、会话内容进日志。
- **预计实现落点**（定稿后）：`internal/nativeui/sidebar.go`（新增 `servicesSection`）、`service_index.go`（导出侧栏行列表，扫尾不改）、`agent_card.go`（serviceRow 换新行模型+彩色 mark）、`quick_panel_services.go`（重排：Running/Scripts/Other listeners）、`proc_brands.go`（品牌色表 + `procMarkForPane` 返回彩色 mark）、折叠/高度偏好复用既有持久位；`internal/services`、`internal/herdr` 不动。

---

## 设计要点（原型已体现，三选项共用）

| 要点 | 内容 |
|------|------|
| 行模型 | `[品牌彩mark 16] 名称(BodySmall semibold) :端口(等宽 Micro muted) … hover↗ [状态方块]`；第二行（A/C）归属 chip + 上下文 |
| 归属三态 | Agent 启动（迷你 agent mark + 名字）｜Script 启动（terminal 字形 + 脚本名）｜Manual（纯文字）｜+ Project-wide（歧义 daemon，不可点） |
| 点击 | 跳 Pane（`selectPane` + `panePrefersChat`）；Project-wide 行不可跳 |
| 浮层重构 | Services 页 = 工作区上下文 chip → Running（新行模型）→ Scripts（Run）→ Other listeners（无归属裸端口，诚实降级） |
| 空态 | 侧栏区块保留头 + 一句空态文案；浮层同 |

---

## Options explored（单文件 exploration.html，右栏切换）

| 关注点 | 选项 | 差异 | 推荐 |
|--------|------|------|------|
| 组织结构 | **A 清单平铺** | Agents 同款两行卡；多工作区时加淡色工作区组标签；归属在第二行内联 | ✅ 推荐：与 Agents 视觉/交互完全同构，扫读最快，分组逻辑最薄 |
| 组织结构 | **B 按启动者分组** | 区块内分「Started by agents / Scripts / Manual / Project-wide」四组，归属由组表达，行变单行 | 「这是 Agent 开的服务」诉求最强；但打破与 Agents 的同构，组常空 |
| 组织结构 | **C 嵌入 + 汇总** | Agent 卡内嵌套完整服务行（含彩 mark/端口）+ 顶部紧凑单行索引区 | 上下文最强；同一信息两处出现，密度最高、实现面最大 |

### 交互状态（单文件多态，右栏 Interaction states）

| 状态 | 触发 | 预期 |
|------|------|------|
| Default | 默认 | 暗色主题完整三表面 |
| Row hover | 点状态卡或真实 hover 首行 | 行浮起 + 尾部 `↗` Preview 显现 |
| 无运行服务 | 点状态卡 | 区块空态文案 + 浮层空态 |
| 浅色主题 | 点状态卡 | 全套 token 翻转（theme.go 亮色值） |

---

## 需要你拍板（未定稿前不实现）

1. **选型**：✅ 定稿 Option A — 清单平铺。
2. **点击行为**：✅ 定稿跳 Pane 并按 `panePrefersChat` 落表面（Agent→Chat、手动→Terminal）。
3. **未运行 Scripts**：✅ 定稿只留浮层 Scripts 组，不进侧栏。
4. **Project-wide 歧义服务**：✅ 定稿不进侧栏，只在浮层 Other listeners 出现。
5. **是否授权进入实现**：✅ 已授权（2026-11-02）。

---

## Preview

默认直接双击或拖入浏览器打开 `designs/services-surfaces/exploration.html`（`file://`）。

| 文件 | 打开方式 | 看什么 |
|------|----------|--------|
| `exploration.html` | `file://…/designs/services-surfaces/exploration.html` | 右栏切 A/B/C；Row hover / 无运行服务 / 浅色主题；`›` 全画布 |

### Static-server exception

不适用（自包含单文件，无 fetch/module）。

---

## Success criteria（定稿前）

- [x] 原型内可切换全部方案/状态（file:// 验证 Default、hover、Esc）
- [x] 视觉与项目 token 一致（theme.go 暗色/亮色实测值、字阶、圆角、指示器规格）
- [x] 机制与实现一致（数据边界=serviceIndex 投影 + Scripts；无虚构状态/uptime；归因启发式如实标注）
- [x] 用户已选定方案
- [x] README Decision 表已更新

---

## Implementation handoff（Phase 4，2026-11-02 已实现）

| 项 | 内容 |
|----|------|
| 改动文件 | `internal/nativeui/sidebar_services.go`（新增：行模型/归属/分组/区块渲染/rememberScriptPane）、`quick_panel_services.go`（重构：Running / Other listeners / Scripts）、`brands.go`（visualMark 品牌色位 + markView 染色）、`proc_brands.go`（procBrandTints 品牌色表 + procBrandAsset）、`sidebar.go`（Agents 下挂 servicesSection）、`shell.go`（servicesOpen/scriptPanes）、`session.go`+`settings/settings.go`（ServicesOpen 持久化）、`script_actions.go`（launchScript 记录 pane）、`agent_card.go`（嵌套服务行换彩色 mark + 友好名）、`session_test.go`、新增 `sidebar_services_test.go` |
| 验证命令 | `GOTOOLCHAIN=auto go test ./...` · `GOTOOLCHAIN=auto go tool mygo vet .` |
| 真机检查步骤 | 1. 起一个 dev server（如 `vite`）→ 侧栏 Agents 下方出现 Services 行（彩色 mark + 名称 + `:端口` + 归属 + 绿方块）；2. 点击行 → 跳到该 Pane 的当前表面（Agent→Chat、手动→Terminal）；3. 右键行 → Open Chat / Pane 动作 / Preview :端口；4. 托盘或 Header 打开浮层 Services 页 → Running（命名行+归属+Preview）/ Other listeners（无归属端口）/ Scripts（Run）；5. 折叠 Services 头 → 重启后折叠态保持；6. 浮层内 Run 一个 service 脚本 → 服务起来后侧栏行的归属显示脚本名 |
| 与原型差异 | 行尾 hover `↗` Preview 改为右键菜单项（MyGo 行内不嵌套按钮，与 Agent 卡菜单同规）；组标签用 Micro 灰字（与 Agents 分组头同族），非独立折叠头；Scripts 未进侧栏、Project-wide 未进侧栏（定稿）；浅色主题走 theme.go 自动，无需切换 |
