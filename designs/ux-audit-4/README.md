# Design Brief: UX audit 第四轮（2026-11-01）

> 工作流：`/think`（Triage）+ `/interactive-design-exploration`。预览：直接打开 `exploration.html`（`file://`，无服务器）。
> URL hash 预置：`#t=header&v=A&s=detail`、`#t=quota&v=A&s=page|popover`、`#t=sessions&v=A&s=list|detail`、`#t=remote&v=A`、`#t=newpane&v=A&s=closed|menu`、`#t=defview&v=A`。
> 手机端（议题 9）单独成稿：`designs/mobile-redesign/`。

## Decision

| 字段 | 内容 |
|------|------|
| **Status** | `Approved 2026-11-01` |
| **Chosen** | 议题 1 = **A**；2 = **A**；3 = **A**；5 = **A**；6 = **A**；7 = **A**（v1 不含 Split-with-Agent）；议题 8 = 修复 1–3 步（不合并 Keychain 条目）；议题 4 = 自由文本 `Autocomplete` |
| **实现授权** | 是（桌面 + 手机都做；手机在现有设计上渐进增强，不大改版，见 `mobile-redesign/`） |
| **进度** | #4、#8 已修复并带回归测试（`TestGitEndpointKeepsCustomTextAfterBlur`、`TestCachedSecretsReadEachItemOnce`、`TestClaudeExpiredSourceRestsInsteadOfAskingAgain`、`TestClaudeDeniedKeychainRests`）；其余议题按序实现 |

## 调研（代码取证）

| # | 现状 | 根因/证据 |
|---|------|-----------|
| 1 | 内页标题是 20pt 粗体（`routeHeaderBlock`），Workspace 页是 ghost 芯片面包屑 | `route_header.go`；2026-10-29 shadcn 决议 Phase 5 把内页大标题留待复审，现在复审 |
| 2 | 浮层 `quotaMiniCard` 只露一个 headline 窗口（固定 96pt）；内页 `usageCard` 是全窗口 + hero/compact；总览是 3 张孤立大数字；Used/Left 分段在汇总行右下角 | `quick_panel_quota.go`、`page_usage.go`；两处各自拼数据 |
| 3 | “继续”走 `PlanHistoryContinuation`，对已在跑的会话其实已返回 `StrategyAlreadyLive` 并跳到 Pane；缺的是列表上看不出“它在跑”以及按钮文案 | `history_continue.go`、`agent/continuation.go`、`liveAgentIdentities()` + `hookIdentities` |
| 4 | Endpoint 用 `ui.Combobox`：键入文字只过滤，失焦回退到最后一次**选中**项，所以自定义值保存即被重置 | `page_settings_git.go` `comboField(... commitAPIPresets)`；`ui.Autocomplete` 才允许自由文本 |
| 5 | Remote Access 4 组 14 行：Listener / Relay / P2P 三条状态、Address、Port、Relay 地址、Key、ICE/TURN、Pair 的说明句… | `remote_settings.go` `settingsRemote` L210-307 |
| 6 | 视图记忆已有：`paneSurfaceMemo` 记住每个 Pane 最后用的 Terminal/Chat，缺“新 Pane 默认进哪个” | `workspace.go` L362-400、`surface_header.go` |
| 7 | 新 Tab 只有 `+` → `CreateTab`（Terminal）。`SessionStarter.Kinds()` 已有可用 Agent 列表（手机启动页已在用） | `app/session_launch.go`、`tabbar.go` L128、`project_tree.go` |
| 8 | **启动卡死**：`shell.go` L801 在 UI 线程同步 `commitKeyPresent()` 读 Keychain，授权框不点就整窗不出；Quota 每次抓取 `st.secrets.Get` 都重读；Claude 凭据过期时不缓存→下次又读 `security find-generic-password`；日志里可见 33–42 s 的授权等待 | `shell.go`、`usage_state.go`、`usage/claude.go` |

## 分诊表

| # | 发现 | 分类 | 处理 |
|---|------|------|------|
| 1 | 页内大标题 | 设计问题 | **议题 1（待选）** |
| 2 | Quota 浮层/内页分叉 + Used/Left 位置 | 设计问题 | **议题 2（待选）** |
| 3 | 会话 ↔ 运行中 Agent 关联 | 设计问题（后端已具备） | **议题 3（待选）** |
| 4 | Endpoint 保存被重置 | **Bug** | 直接修（改 `Autocomplete` 或自由文本字段 + 预设按钮），加回归测试 |
| 5 | Remote Access 过载 | 设计问题 | **议题 5（待选）** |
| 6 | 默认视图 TUI / Chat | 新功能（小） | **议题 6（待选）** |
| 7 | 新 Tab 选 Agent | 新功能 | **议题 7（待选）** |
| 8 | Keychain 启动阻塞 / 反复授权 | **严重 Bug** | 直接修，见下 |
| 9 | 手机端重设计 | 方向性 | `designs/mobile-redesign/` |

## 议题与方案

| 议题 | A | B | C | 推荐 |
|------|---|---|---|------|
| 1 Header | 页面名面包屑：第一段=页面名，进内页追加一段 | 项目为根：项目芯片起头 | 现状 20pt 大标题（参照） | **A** |
| 2 Quota | 总览一行 + “Needs a look”；Used/Left 进标题栏工具簇；一行一卡，浮层与内页同一卡片模型（`compact`/`full` 两种密度） | Used/Left 放内容区工具行 | 取消开关，只读“剩余” | **A** |
| 3 Sessions | 行内状态胶囊（Running / Needs you / Open·idle）+ 悬停主按钮变 “Go to pane” | 顶部 “Running now” 置顶分组 | A + Running 筛选 + 反向链接 | **A**（C 可后补） |
| 5 Remote | 状态卡（地址/链路/Pair）+ 已配对手机 + 折叠 Advanced；14 行→3 行 | 三步流程 | 现状 | **A** |
| 6 默认视图 | 设置项 + 每 Pane 记忆并存（记忆优先于默认，新 Pane 用默认） | 默认覆盖记忆 | — | **A** |
| 7 新 Pane | 分裂按钮 “+ ▾”：点 + 用上次的；▾ 出 MRU 列表，Terminal 永远最后 | “+” 永远出列表 + 数字键 | 新 Tab 启动页（卡片网格） | **A** |

## 议题 8 修复计划（待授权，无设计分歧）

1. **启动不再碰 Keychain**：`commitKeyPresent()` 移出 UI 线程，改为首次用到时异步读并缓存；任何授权框都不能阻塞首帧。
2. **进程内 secret 缓存**：每个 Keychain 条目每进程最多读一次（`usage.<id>.token`、`.signin`、commit key），写/删时失效。
3. **过期凭据负缓存**：Claude 凭据过期后在内存标记，不再每次打开 Quota 都去读 Keychain；用户点 Refresh/重新授权时才重试。
4. **（可选）合并条目**：把 Solco 自己的 secrets 合并到单个 Keychain 条目，授权最多一次。代价：迁移逻辑。默认不做，除非 1–3 后仍有多次弹窗。
5. 说明：dev 构建与安装版签名不同，Keychain ACL 不共享——这是开发期反复弹窗的另一来源，安装版用稳定签名身份后应只授权一次。

## 约束清单

- **必须保留**：`settings.Remote` 字段、`remotehost.Server`、配对码流程；`/quota` 手机线协议和 golden 文件不变；Herdr 为唯一运行时，`--takeover` 禁用；Quota 不后台轮询、令牌只进 `mygo.Secrets`。
- **必须对齐**：`controls.go` 是控件唯一外观；`tipped(...)` 是唯一 tooltip；状态槽 `status_indicator.go`；`breadcrumbRow` 的 ghost 芯片。
- **修订的既有决议**：2026-10-29 shadcn “内页 20pt 标题留待 Phase 5”→ 本轮裁决为取消大标题；`unified_header_test.go`、`modal_title_test.go`（Title 20 只许 `route_header.go`）需随之改写；`settings_search.go` 索引需同步（Remote Access 行变动，`TestSettingsIndexCoversEveryRow`）。
- **非目标**：新增 Herdr 方法；Split-with-Agent（需“split + 在 pane 起 Agent”两段事务，v1 不做，待你确认）；手机端实现（见 mobile-redesign）。
- **实现落点（预计）**：`route_header.go`、`breadcrumb_switcher.go`、`page_usage.go`、`quick_panel_quota.go`、`history_*.go`/`page_history.go`、`page_settings_git.go`、`remote_settings.go`、`page_settings*.go`、`tabbar.go`、`project_tree.go`、`app/session_launch.go`、`settings/settings.go`、`shell.go`、`usage_state.go`、`usage/claude.go`。

## Verification

```sh
GOTOOLCHAIN=auto go test ./...
GOTOOLCHAIN=auto go tool mygo vet .
GOTOOLCHAIN=auto go tool mygo build
git diff --check
```

手动：① 清空 Keychain 授权后冷启动，窗口应立即出现；② Git 设置填自定义 Endpoint → 保存 → 重进仍在；③ 在运行中的会话上悬停→“Go to pane”落到对应 Pane；④ 每个内页标题栏均为面包屑；⑤ Quota 浮层与内页同一数据同一卡片。

[PROTOCOL]: 选型后更新 Decision；实现后同步 `internal/nativeui/CLAUDE.md` 与 `docs/client-product-architecture.md`。

## 实施进度（2026-11-01）

议题 1–8 与 Bug #4/#8 已在桌面实现：面包屑标题、Quota 统一卡片、Sessions 运行态药丸 + Go to Pane、Endpoint 自由文本、Remote Access 状态卡 + 折叠 Advanced、默认 Agent 视图（`General.AgentView`）、Tab 条 `+ ▾`（Agent MRU，Terminal 最后，无 split-with-Agent）、Keychain 启动不读/进程缓存/负缓存。手机端见 `designs/mobile-redesign/` 实施进度。
