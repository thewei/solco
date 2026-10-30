# Design Brief: UX audit 第三轮（2026-10-30）

> 工作流：`/think`（Triage）+ `/interactive-design-exploration`。预览：直接打开 `exploration.html`（`file://`，无服务器）。
> URL hash 预置：`#t=agents&agents=A`、`#t=hosts&hosts=A`、`#t=tabs&tabs=A`、`#t=settings&settings=C`。

## Decision

| 字段 | 内容 |
|------|------|
| **Status** | `Approved 2026-10-30` |
| **Chosen** | 议题 1 = **A**；议题 2 = **A**；议题 3 = **A**；议题 4 = **C**（搜索 + Voice 独立页） |
| **实现授权** | 是（2026-10-30，「授权，直接实现并覆盖安装」） |
| **实现偏差** | 议题 3：真机取证发现现有最小宽 84、最大 180、非选中 Tab 本就不被均分——被截断的是长标题顶到 180；因此改为「选中 160–240、其余 120–180」（原型里的“非选中固定 120”未采用）；同时修了一个顺带发现的旧 bug：条一旦溢出就会出现 ‹ › 把视口变窄，选中 Tab 的 × 被切掉，现在选中后连续 3 帧重新入视（`tabRevealFrames`，`TestSelectedTabEndsWholeInView`）。议题 4：搜索命中不高亮页内行（侧栏命中列表 = 设置项 + 所在页，点击进入页并清空搜索）；索引是手写表，由测试扫描源码防漂移。议题 1：分组头摘要取该组首个已解析仓库，其它仓库的卡片行内保留自己的摘要。 |

## 调研（真机巡检 + 代码取证）

巡检 Workspace / Hosts / Changes / Settings（第二轮已处理的项不重复）。

- Agents 行：同一工作区下 3 个 Agent 同 cwd，每个卡片都渲染 `main · 68 · +2842 −334`（`agent_card.go` 按 cwd 取 `gitMetaForCWD`，不看同组是否已有）。
- Hosts 侧栏：名称与 `user@host` 同行，名称被截为 `oracle…` / `cyww-寻空…` / `Whisky Pro…`。
- Tab 栏：非选中 Tab 实为 84–180pt（长标题顶到 180），emoji + 任务前缀（`SEARCH:`、`PROJ:`）占满，几个 Tab 看起来一样。
- Settings：General 末尾挂着 Voice input（Endpoint / API key / Model）；无搜索（第二轮分诊 #7 记录未做）。

## 分诊表（Triage）

| # | 发现 | 证据 | 分类 | 处理 |
|---|------|------|------|------|
| 1 | Agent 行重复同一串 git 摘要 | `agent_card.go:140` | 设计问题 | **议题 1（待选）** |
| 2 | Hosts 名称被 endpoint 挤截断 | `hosts` 行 / 真机截图 | 设计问题 | **议题 2（待选）** |
| 3 | Tab 栏非选中 Tab 过窄 | `tabbar.go` / 真机截图 | 设计问题 | **议题 3（待选）** |
| 4 | Settings 无搜索 + Voice 混在 General | `page_settings.go` | 设计问题 | **议题 4（待选）** |
| 5 | Quota “Reading…” 闪烁 | 第二轮截图 | Cosmetic | 记录，不做 |
| 6 | Git 工具在标题栏 | 2026-10-30 已拍板 | **Keep** | — |

## 议题与方案

| 议题 | A | B | C | D | 推荐 |
|------|---|---|---|---|------|
| 1 Agents 行 | 摘要上移到分组头，行内只在 cwd 不同时回退 | 行内保留，仅显示与分组不同的部分 | 保持现状 | — | **A** |
| 2 Hosts 行 | 名称优先，endpoint 仅悬停/选中 | 两行行（名称 / endpoint） | 保持现状 | — | **A** |
| 3 Tab 栏 | 非选中最小 120，超出滚动 | 非选中只留图标 + 状态槽 | 保持现状 | — | **A** |
| 4 Settings | 搜索框 | Voice 独立成页 | A + B | 保持现状 | **C** |

## 约束清单

- **必须保留**：Herdr 为运行时权威；状态槽 / 控件脸 / tooltip 走既有单一来源；Git 工具在标题栏；Tab 栏默认（`TabsPlacement`）不变。
- **非目标**：不动 Quota；不新增 Herdr 方法；不改设置项语义。
- **实现落点**：`agent_card.go` + `sidebar_tree.go`（1）；Hosts 侧栏行（2）；`tabbar.go`（3）；`page_settings.go` + 新 Voice 页 + 设置标签索引（4）。

## Verification（实现阶段）

`GOTOOLCHAIN=auto go test ./...`、`GOTOOLCHAIN=auto go tool mygo vet .`、`GOTOOLCHAIN=auto go tool mygo build`、`git diff --check`、真机逐项对照原型。
