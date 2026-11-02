# Design Brief: Automations — herdr 自动化的原生视图 + Agent 驱动生命周期

> **工作流 skill**：`interactive-design-exploration`（Phase 0 调研已完成 2026-11-02）

## Decision

| 字段 | 内容 |
|------|------|
| **Status** | `Approved 2026-11-02` |
| **Chosen** | Option A — 管理列表 + 详情抽屉 |
| **组合说明** | 权威实现 = fork 增强 DnzzL/herdr-automations 为 `com.whstudio.solco.automations`（draft/approve、rationale+prompt 共存、30 天运行留存、skip/queue 策略、成功通知、TTL 清理）；B 的「需要你」聚合以列表 chip 呈现，v1 不做看板列 |
| **实现授权** | 是（用户 2026-11-02 选型时明确批准 Phase 4） |

---

## Problem

侧栏顶部视图 tabs（Terminal/Changes/Sessions/Files/Hosts）新增第 5 个目的地 **Automations**：它的信息架构应是什么（管理列表 / 运行看板 / 双栏监控），以及「Agent 起草 → 人审批」的自动化创建流如何进入界面——同时让运行中的自动化在真实窗格可见。

---

## Phase 0 — 项目调研摘要

### solco 仓库现状（代码）

| 项 | 发现 |
|----|------|
| 渲染入口 | `internal/nativeui/router.go:17-51`（route 常量 + render dispatch）；`sidebar.go:186-207` `sidebarViewTabs` 一排 `gorexIconTab`；`titlebar.go/page_files.go/page_hosts.go` 的 `showViewX`（toggle 回 Terminal）/`goX`；`route_header.go:41-53` header spec |
| 视图新增触点 | routeAutomations + router case、persistableRoute（session.go:26）、Go 菜单 `viewDestinations`（app_menu.go:159-163，⌘6 空闲）、命令中心 action、shortcuts sheet、icons.go |
| 插件门控先例 | `internal/workboard/workboard.go`：`Detect(herdr plugin list --json)` → `Availability.Supported` 唯一开关；enabled + 版本窗口 1.2.x + 入口点检查；`workboard_ui.go` 30s 焦点去抖探测；`client.go` exec seam（剥 agent 身份 env）；UI 全部 `if s.workboardAvailable()` 隐藏 |
| **已有自动化客户端** | `internal/workboard/automations.go` 已包 herdr-projects 的项目级 automations：`AddAutomation{Name,Prompt,Repo,Schedule\|When,EveryRepo,If}` / `SetAutomation` / `RunAutomation` / `RemoveAutomation`；事件 `added/approval/review-failed/failed/needs-you/merged` |
| 插件机制（本机核实） | `herdr plugin install/uninstall/update/link/enable/list/config-dir/action/pane` 全部可用；本机已装 herdr-projects 1.2.26、agent-handoff 0.1.0、**com.whstudio.solco.hooks 0.1.0（Solco 自有插件先例）**；无 automations 插件 |
| Agent 启动路径 | `internal/app/session_launch.go` `SessionSpec{...,Prompt}` 非空即随启动投递首条 prompt（`agent.RunLaunchTransaction`）；`herdr.Manager.PromptAgent` 对活 agent 追加 prompt |
| Agent skill 通道 | `internal/agentcatalog` 扫 `~/.claude/skills`、`~/.agents/skills`（Codex/pi/omp/Cursor/agy 共享）等；`internal/agenthooks` 有「写 `~/.solco/` 自有文件 + 幂等注册」的安装器模式 |
| 状态形状语言 | `internal/nativeui/status_indicator.go`：工作=spinner、就绪=hollow ring、其余=solid dot、服务=rounded square；16pt 单一槽位 |
| 主题 token | `theme.go`：暗色 bg #141516 / surface #212121 / hover #2a2a2e / border #333338 / text #ececee / muted #8b8c92 / accent #0a84ff / success #30d158 / warning #ffd60a / danger #ff453a；亮色 #f7f7f5/#ffffff/#e2e2e6/#18181b/#71717a |

### 参考实现 DnzzL/herdr-automations（已克隆 /tmp，MIT）

Go Herdr 插件 v0.11.3：`herdr-plugin.toml` 声明 `[[startup]]` 常驻 daemon（机器级 flock 单实例、空闲自停、二进制变更自重启）、`[[panes]]` TUI 看板、`[[actions]]` run-now。30s 墙钟轮询 + robfig/cron；触发即开**真实可见** Herdr workspace（worktree per run `auto/<slug>-<ts>`）→ `agent start` → `agent prompt --wait` → `agent wait` 切片；历史 = append-only JSONL（7 状态 × 3 触发器）；CLI `daemon/list/run/pause/resume/history/put/rm/seen/cleanup/pane/install-cli`；`skills/creating-automations/SKILL.md` 教 Agent 把意图翻译成 YAML 并 verify（list→run→history）。

**已识别的可叠加优化点**（对照我们的需求）：
1. 无草稿/审批流——run 直接开火；我们的自动化应由 Agent 起草后进 **Needs review** 状态，人批准才启用（对应 workboard 的 plan approval 先例）
2. 提示词是静态字符串——用户要求「Agent 分析后产出完善提示词并放在一起」：自动化记录应同时携带 **rationale（分析）+ 结构化 prompt**
3. 历史无界、无运行留存；无成功通知（只有失败 toast）；无 worktree 清理 TTL；重叠只能静默丢弃（无 skip/queue 策略）
4. 运行只留 pane id——Solco 可把 run 关联到 provider session 文件（`internal/history`），直接「在 Chat 里看这次运行」

### 业界对照

| 系统 | 创建 | 触发 | 运行 | 管理 |
|------|------|------|------|------|
| ChatGPT Tasks | 聊天里一句话（"每天早上…提醒我"）或 Tasks 页表单 | recurring / one-off | 后台 headless，完成推送通知 | Tasks 管理页；暂停/编辑/删除；≤10 个活跃 |
| Claude Code Routines（云） | Web/Desktop/CLI `/schedule`（"Create with Claude" 对话式） | Schedule(cron/一次性) + API 端点 + GitHub 事件，可组合 | 每次运行=完整云 session；fire 文本按不可信包裹 | 任务详情页 + History（run 可作为 session 回看）；权限暂停等人工 |
| Claude Code Desktop / `/loop` | Desktop 表单 / 会话内 | 本机 cron（1 分钟粒度）/ 会话内自适应间隔 | 本机（访问本地文件） | 7 天过期；session 恢复 |

共识：**任务 = prompt + 触发 + 目标环境，打包一次反复运行；Agent 可对话式创建；每次运行可追溯；有专门管理面**。这正好是 herdr 插件 + Solco 原生视图的组合。

### 约束

- **必须保留**：插件是自动化唯一权威（Solco 纯呈现）；门控照搬 workboard 模式（`Availability.Supported` 唯一开关，gate 关时视图入口隐藏）；不发明 Herdr 方法；运行 = 真实 Herdr workspace/pane（worktree per run），自动 attach 永不 `--takeover`；日志不落 prompt/对话内容/凭据；视图机制与 Files/Hosts 一致（toggle 回 Terminal、persistableRoute、菜单/命令中心/快捷键）；状态形状语言不另造
- **必须对齐**：theme.go 色值、controls.go 控件面、ghost-chip 面包屑、`tipped` tooltip、segmentTabs；原型内色值即实现 token
- **非目标（v1）**：远程机器执行（参考插件单机）；事件/API 触发器（v1 = cron + 一次性 + 手动）；herdr-projects 项目级 automations 的迁移/合并（留在项目页）；手机端 Automations
- **预计实现落点**：新插件仓库 `com.whstudio.solco.automations`（fork 增强 herdr-automations：草稿/审批、rationale+prompt 共存、运行留存 30 天、skip/queue 策略、清理 TTL、成功通知、run↔session 关联）+ solco 侧 `internal/automations`（Detect/Client）+ `internal/nativeui/page_automations.go`、sidebar.go、router.go、session.go、app_menu.go、command_center_ui.go、shortcuts_sheet.go、icons.go + agent skill 安装（`~/.solco/skills/` → 各 provider skills 目录，参照 agenthooks 幂等模式）

---

## Options explored

| ID | 名称 | HTML 文件 | 核心差异 | 优点 | 缺点 |
|----|------|-----------|----------|------|------|
| A | 管理列表 + 详情抽屉 | `exploration.html`（Variant A） | 一列自动化行，点开右侧抽屉看 prompt/事实/运行 | 与 Sessions/Files 心智一致；信息密度高；实现最薄 | 运行中的活感弱，需抽屉才能看细节 |
| B | 运行看板 | `exploration.html`（Variant B） | 按状态分列（需要你/运行中/已排程/已暂停），卡片化 | 运行/需要你一眼可见；审批草稿自然成为一列 | 列多时密度低；自动化数量大时找东西靠滚动 |
| C | 双栏监控 | `exploration.html`（Variant C） | 左栏列表 + 右栏单自动化详情/运行时间线 + 全局运行条 | 单自动化生命周期最完整；运行条给全局活感 | 占宽最大；两级选择，浏览成本高于 A |

### 交互状态（单文件多态）

| 状态 | 如何触发（原型内） | 预期行为 |
|------|-------------------|----------|
| 默认 | 选中变体即呈现 | 列表/看板/双栏的常态 |
| 详情抽屉 / 运行实况 | 右栏 state 卡，或画布内点行/卡片 | A 滑出抽屉；C 点亮 RunStrip 实况段 |
| Agent 草稿审批 | 右栏 state 卡，或画布内「让 Agent 起草」 | 草稿卡展示 意图→分析→结构化 prompt，批准后启用 |
| 运行中 | Variant A state 卡 | 列表出现活运行横幅与 spinner 行 |

---

## Preview

双击打开 `designs/automations/exploration.html`（`file://`）。右栏切变体与状态；`›` 收起面板全画布；Esc 退状态。

### 截图（2026-11-02 `file://` 实测，专用 Chromium 1440×920，零 console 错误）

| 状态 | 文件 |
|------|------|
| A · 默认列表 | `preview-a-list.png` |
| A · 运行中横幅 | `preview-a-running.png` |
| A · 详情抽屉 | `preview-a-drawer.png` |
| A · Agent 草稿审批 | `preview-a-draft.png` |
| B · 看板默认 | `preview-b-board.png` |
| B · 运行实况 | `preview-b-live.png` |
| C · 双栏默认 | `preview-c-split.png` |
| C · 运行实况（RunStrip） | `preview-c-live.png` |
| C · 草稿审批（右栏内联） | `preview-c-draft.png` |

---

## Success criteria（定稿前）

- [x] 原型内可切换全部方案 / 状态（右栏 state 卡与画布内行/卡片两条路径均实测；Esc 退状态、`›` 收面板验证通过）
- [x] `file://` 直开可用
- [x] 视觉与 theme.go token 一致（色值已在上表列出）
- [x] 机制与实现一致：原型中的行=插件 CLI 的 list 行、运行=真实 worktree workspace、审批=插件的 draft→approve 动词，非魔法状态
- [x] 用户已选定方案（2026-11-02：Option A + fork 增强为 com.whstudio.solco.automations，授权实施）
- [x] README Decision 表已更新

---

## Implementation handoff（Phase 4，定稿后填）

| 项 | 内容 |
|----|------|
| 改动文件 | 插件：`plugins/solco-automations/`（fork DnzzL/herdr-automations v0.11.3 → com.whstudio.solco.automations 0.12.0：draft/approve、rationale、on_overlap skip\|queue、30 天留存、成功通知、cleanup TTL、SKILL.md 草稿优先；14 包 204 测试绿）。solco：`internal/automations/`（Detect 门控 + Exec 客户端，12 测试）、`internal/nativeui/page_automations.go`+`automations_ui.go`（/automations 列表+抽屉，13 测试）及触点 router/session/route_header/sidebar/icons/app_menu/shortcuts/shortcuts_sheet/command_center/dialogs/shell、`ux_audit3_test.go` 菜单审计扩展 |
| 验证命令 | 插件 `cd plugins/solco-automations && go test ./...`；solco `GOTOOLCHAIN=auto go test ./... && GOTOOLCHAIN=auto go tool mygo vet .`；真机冒烟 `herdr plugin link plugins/solco-automations` → `herdr-automations add --draft …` → `list --json` → `approve` → `rm`（2026-11-02 全部通过：门控 Supported、List/History 解码真机输出成功） |
| 浏览器检查步骤 | 1. `GOTOOLCHAIN=auto go tool mygo dev` 2. 侧栏第 6 枚图标（闪电）出现且进 /automations 3. ⌘6 / Go 菜单 / ⌘K palette 均可达 4. 空态 + ＋新建（默认草稿）→ 列表出现 ✦ 草稿行 5. 点行开抽屉：rationale/prompt/事实/运行 6. 抽屉「批准并启用」→ 行变 hollow ring + next_run 7. `herdr-automations run <name>` 开真实 worktree 窗格，行内 spinner 8. 删除走共享确认 |
| 与原型差异（如有） | ① 编辑走 `put --original` 表单；② 「在 Chat 查看」仅当 internal/history 唯一命中 agent+cwd+±2min 时绘制，否则隐藏；③ ⌘6 菜单项在窗口构建时定型（运行中卸载插件需下次启动收回，palette/侧栏实时）；④ ✗ 超时（时长≥timeout）与 ✗ 失败分 chip；⑤ 打开窗格仅在该窗格仍在本 session 投影时出现 |
