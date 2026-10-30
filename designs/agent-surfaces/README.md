# Design Brief: Agent surfaces（Header 浮层 · 侧栏 Agents/Workspace · Pane 卡片 · Tab 位置 · 页面命名）

> 工作流：`/think` + `/interactive-design-exploration`。预览：直接打开 `exploration.html`（`file://`，无服务器、无依赖）。
> 可用 URL hash 预置状态，例如 `#v=tabs&tabsSide=0&many=1&tabStyle=browser`、`#v=pop&popTab=limits&lstyle=rows`、`#v=pane&focus=raised&theme=light`。

## Decision

| 字段 | 内容 |
|------|------|
| **Status** | `Approved 2026-10-30` |
| **Chosen** | 名称 **Quota**（用户改选，非推荐的 Limits）；徽标含 idle + Inline；额度列 Cards；子代理 Tree（首版仅 Claude Code）；Workspace Tile；Pane 聚焦 Ring；Tab bar Flat |
| **实现授权** | 是：A → B → C 全做 |

> 实现中凡原型/下文写 "Limits" 一律读作 **Quota**（路由 `/quota`、浮层 Tab 名、设置分区、窗口标题）。

---

## Problem

八条诉求本质上是同一件事：**把"一个 Agent 的状态"在每个地方用同一种方式说出来**，并把 Header 浮层、侧栏、Pane 卡片、Tab 的外形对齐。拆成六个关注点（原型右侧 Focus area 1–6）：

| # | 诉求 | 原型里看哪 |
|---|------|-----------|
| 1 | Usage 改名；浮层增加额度列；徽标 `2/4`；浮层 Tab 换成 Changes/Commits 同款 | Focus 1 · 6 |
| 2 | 侧栏 Agents 树状嵌套（子代理挂在父 Agent 下） | Focus 2 |
| 3 | 状态点移到右侧、与 Loading 重叠同位；最后活动时间；工作区名作淡色分区；Workspace 行同样优化 | Focus 2 · 3 |
| 4 | Pane 卡片聚焦不用蓝色；标题旁路径、右侧状态点；状态点抽公共函数 | Focus 4 |
| 5 | 设置里"Tab 是否在侧栏"，否则 Tab bar 占面包屑位置，带状态、可滚动 | Focus 5 |

---

## Phase 0 — 项目调研摘要

### 现状（读过的代码）

| 项 | 发现 |
|----|------|
| 浮层 | `quick_panel.go`：Flyout（Popover 材质）400×≤480；顶部是普通 `segmentedControl`（Agents / Services / Tasks 仅 herdr-projects 门开时）；Agents 按状态分组（`agent_quick_panel.go quickPanelGroups`）。 |
| Changes/Commits Tab | `git_sidebar_tree.go segmentTabs`：`ui.SegmentedBase` + 每段 图标+文字+可选计数。浮层应改用它。 |
| Header 徽标 | `titlebar.go titlebarActivity`：`attention+review+working` 的**合计**，浮在图标右上角。要改成 `等待你 / 在跑总数`。 |
| 状态点 **有四套实现** | ① `agent_visual.go statusDot`（Agent 卡片，图标角点）② `components.go treeRow` 的 `cornerMark` 圆点 + 尾部 `ui.Spinner`（树行）③ `terminal.go paneStatusDot`（`gorexActivityDot`/`gorexAttentionDot`，Pane 标题）④ 浮层/卡片再用 ①。语义相同、外形各异、位置不同。 |
| 侧栏 Agents | `sidebar.go agentItems` → `agent_card.go agentCardRow`（两行卡：标题 / 项目+git，角点状态，尾部 Spinner）。无按工作区分组，无嵌套。 |
| 侧栏 Workspace | `project_tree.go projectItems`：Workspace→Tab→Pane 三层 `treeRow`，文件夹图标，尾部只有 pane 计数；Tab 在此树里。 |
| Pane 卡片 | `terminal.go paneCard`：聚焦 = `Accent.Alpha(0.6)` 边框（即你说的淡蓝）；标题行已有 `shortDir(cwd)` 路径（`paneCardCaption`），状态点跟在标题后。 |
| 面包屑 | `breadcrumb_switcher.go` + `titlebar.go`：Workspace › Tab › Pane 三个 ghost chip。 |
| 额度 | `internal/usage` + `usage_state.go` 已就绪：`Service` 带 TTL 缓存与强制刷新底线；页面打开才读。 |

### 本机取证（决定子代理方案是否可行）

- `herdr agent list` 的字段只有 `agent / agent_session / agent_status / cwd / pane_id / tab_id / workspace_id / terminal_*`：**Herdr 没有"父子"信息**，嵌套必须来自别处。
- `herdr agent_session.value` = provider 会话 transcript 的路径（本机 omp 已验证；Claude 实现时确认）。
- **Claude Code 把子代理落盘**：`~/.claude/projects/<proj>/<session-uuid>/subagents/agent-<id>.jsonl` + `agent-<id>.meta.json`，meta 含 `agentType / description / toolUseId / spawnDepth`，主 transcript 的 `Agent`/`Task` tool_use 用同一个 id（本机 `herdr-client` 项目实测）。→ 可以从**会话文件**读出"哪个 Agent 派了哪个子代理"，运行中 = 其 jsonl 最近 ~20 s 有写入。
- omp 最新会话里没有 `task` 调用，Codex 未验证 → 首版只承诺 Claude Code，其余 provider 没数据就是平铺，不报错。
- `internal/history` 现状：`Sidechains` 恒为空、`isSidechain` 行被跳过 → 需要在只读的 history 域新增"会话的子代理清单"，不动运行时。

### 约束

- **必须保留**：Herdr 仍是 Pane/Agent/进程唯一权威；子代理只是**只读展示**，不新增 Herdr 方法、不新增运行时、不记录历史；Spinner 只在行可见时绘制；⌘W 关 Tab 语义；自动 attach 不用 `--takeover`。
- **必须对齐**：`controls.go` 控件脸、`segmentTabs` 面；`tipped()` 是唯一 tooltip；shadcn 对齐的圆角（行 8、图标钮 8）；状态色走 `StatusColor`；不另起 token。
- **非目标**：不改终端渲染；不加 Agent 分组/过滤的新交互（截图里 Agents 标题右侧的图标不做）；不显示机器/设备信息（设备切换仍在侧栏页脚）；不改 Services/Tasks 内容；不做子代理的控制（不能从侧栏停止/发消息）。
- **预计实现落点**：见文末 Handoff。

---

## 设计要点（原型已体现）

**统一状态指示器**（一个函数、一个 16px 槽位，点与 Loading 重叠同位）

| 状态 | 外形 | 来源语义 |
|------|------|----------|
| working | 12px 转圈（强调色） | 目录 Working 桶 |
| attention | 7px 实心点（警示橙） | Needs attention |
| review | 7px 实心点（绿） | Ready for review |
| idle | 8px 空心环（muted） | Idle |
| 无 Agent | 槽位留空，列对齐 | — |

祖先行（Workspace、折叠的 Tab）= 子孙里最强的状态（attention > review > working > idle）。

**徽标 `2/4`** = 等待你（attention + review）/ 在跑的 Agent 总数（含 idle）。分子取最强色调；0 等待时分子变 muted（`0/7` 安静）。备选分母：不含 idle。

**Limits 卡片（浮层里的"简单卡片"）**：一个标题数字（最紧窗口的百分比；计数套餐如 Cursor 500 次显示 `124 of 500 req`）+ 一根条 + 一行"窗口 · 重置时间" + 可选 chip（重置券）。细节留在完整页面。

---

## Options explored（单文件 `exploration.html`，右栏切换）

| 关注点 | 选项 | 差异 | 推荐 |
|--------|------|------|------|
| **名称** | Limits / Quota / Allowance / Meter / Usage | 见 Focus 6：句子测试 + 各处落位 | **Limits**：字面（窗口+重置），与 Claude "usage limits"、Codex "rate limits" 同词；不暗示花费/历史（我们明确不做）。 |
| 徽标 | Inline `2/4`（按钮里带数字）/ Corner（图标角标） | Inline 不挤图标、数字更易读；Corner 保持按钮方形 | Inline |
| 浮层分组 | 按状态 / 按工作区 | 状态=现状（先处理要你处理的）；工作区=与侧栏一致 | 状态 |
| Limits 卡片 | Cards（2 列）/ Rows（每家两条迷你条）/ Rings（环） | Cards 扫读最快；Rows 信息最密；Rings 最"仪表" | Cards |
| 子代理 | Tree（缩进+引导线，工作中展开，完成折成 `⤷N`）/ Chip（只有数字）/ Off | Tree 直接回答"谁派的"；Chip 省高度 | Tree |
| Workspace 行 | Tile（字母块+分支+脏点+状态）/ Two-line（带路径）/ Plain（现状图标+计数） | 窄侧栏下 Tile 最稳，分支在窄宽时先隐藏 | Tile |
| Pane 聚焦 | Ring（1px 中性线）/ Raised（聚焦抬升、其余变暗）/ Line（标题下 2px 线）/ Blue（现状） | Ring 最安静；Raised 最醒目；Line 最克制 | Ring |
| Tab 位置 | 侧栏树 / 标题栏 Tab bar；Tab bar 样式 Flat（与面包屑 ghost chip 同族）/ Browser（选中 Tab 与内容相连） | Flat 与现有标题栏一致；Browser 更像浏览器 | 默认侧栏；Bar 用 Flat |

### 交互状态

| 状态 | 触发 | 预期 |
|------|------|------|
| Popover · Agents / Limits | 点右上角徽标；浮层内切 Tab | 外点或 Esc 关闭 |
| Settings 开关 | Interaction states → Settings | 打开/关闭"Show Tabs in Sidebar"，Tab 在侧栏树与标题栏间移动 |
| Tab bar · 14 tabs | Interaction states → Overflow | 边缘渐隐、‹ › 滚动、激活 Tab 自动入视 |
| Nothing waiting | Interaction states → Nothing waiting | 徽标 `0/7` |

---

## 需要你拍板（未定稿前不实现）

1. **名称**：Limits（推荐）/ Quota / 其它。
2. **子代理数据源**：首版仅 Claude Code（会话文件里的 `subagents/`）？其余 provider 有数据再加。
3. **`2/4` 的分母**：含 idle（推荐，等于浮层里列出的全部）还是只算非 idle？
4. **Limits 卡片样式** 与 **Pane 聚焦样式**、**Workspace 行样式**、**Tab bar 样式**（上表推荐项可整体采纳）。
5. 额度读取时机增一条：**浮层 Limits Tab 打开时读一次（走缓存）**——这是对"页面打开才读、无后台轮询"的一次扩展，需要写进 AGENTS.md 的 Usage 决定。

---

## Success criteria（定稿前）

- [x] 原型内可切换全部方案 / 状态
- [x] 默认可在 `file://` 下打开（已用 headless Chrome 验证 pop / limits(cards·rows·rings) / tab bar(flat·browser·14 tabs) / pane focus / settings / name）
- [x] 视觉对齐项目 token（行高 30、圆角 8/7、字号 12/11/10.5、`segmentTabs` 面、状态色）
- [ ] 用户在 `interactive_feedback` 中选定方案
- [ ] Decision 表更新

---

## Implementation handoff（Phase 4，定稿后；估计分三个独立可合并的阶段）

> 每阶段合并后系统都可用；任何一阶段不落地不影响其他阶段。

**阶段 A · 统一状态指示器 + 侧栏 + Pane（纯呈现）**
- 新 `internal/nativeui/status_indicator.go`：`statusIndicator(c, state, opts)` 唯一实现（槽位、转圈、点、空心环、tooltip），替换 `agent_visual.go statusDot`、`components.go treeRow` 角点与尾部 Spinner、`terminal.go paneStatusDot`；保留"祖先=最强状态"的汇总函数。
- `agent_card.go`：移除图标角点，尾部 `时间 + 槽位`；`sidebar.go agentItems` 按 Workspace 分组（淡色分区标签）。
- `project_tree.go` / `components.go treeRow`：Workspace 行改 Tile 版；状态走槽位；分支/脏点来自既有 `gitMetaForCWD`。
- `terminal.go paneCard`：聚焦边框改 `t.Text.Alpha(~0.5)`（或选定样式）；`paneCardHeader` 状态移到右缘、操作钮滑入其左。
- 测试：状态映射表驱动；`TestControlsHaveOneFace`/tooltip 守卫不回归；槽位宽度在各行一致。

**阶段 B · 浮层（Tab 面 + 徽标 + Limits 列 + 改名）**
- `quick_panel.go`：`segmentedControl` → `segmentTabs`（图标+计数），新增 Limits Tab；`titlebar.go titlebarActivity` 改 `等待/总数`；`quickPanelContentHeight` 计入 Limits 高度。
- Limits 内容复用 `usageState.svc.Result`，打开该 Tab 时 `refreshUsage(false)`；无 provider 开启时显示"到设置里开启"。
- 改名：路由 `/usage` → `/limits`（`persistableRoute` 迁移旧值）、侧栏/标题栏/设置分区/窗口标题文案、docs/AGENTS.md；`internal/usage` 包名不动。
- 测试：徽标分子/分母/色调；浮层 Tab 顺序（Tasks 门）；路由迁移。

**阶段 C · 子代理 + Tab bar 模式**
- `internal/history`：新增只读 `SubagentsFor(sessionPath)`（读 `subagents/*.meta.json` + jsonl mtime 判活，缓存，无写入）；`internal/nativeui`：Agent 行下挂子行（运行中展开、完成折叠计数）。
- `settings.General.TabsInSidebar`（默认 true）+ Settings 开关；`titlebar.go` 在关闭时用 Tab bar 替换面包屑的 Tab 段（滚动、渐隐、‹ ›、激活入视、× 悬停、状态槽位）；侧栏树此时只列 Workspace。
- 测试：子代理清单解析（fixture 取自本机真实 meta 的脱敏副本）；开关两态渲染；14 Tab 溢出滚动。

**验证命令**：`GOTOOLCHAIN=auto go test ./...`、`go tool mygo vet .`、`go tool mygo build`、`git diff --check`；真机：打开浮层看 `x/y`、Limits Tab；侧栏看分组与状态槽位；设置里切 Tab 位置；开一个 Claude Code 子代理看嵌套。

**最脆弱假设**：Claude Code 的 `subagents/` 目录布局是未文档化的实现细节。若变更，嵌套会静默退化为平铺（不报错、不影响其他功能），且只影响阶段 C 的一个读取函数。
