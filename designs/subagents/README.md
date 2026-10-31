# Sub-agent views(子代理视图)

> 状态:**已定稿并实现(2026-10-31,P1→P4)。** 见下方 Decision。
> 打开 `exploration.html`(`file://`,无依赖)。右栏 Scenes 切状态,Controls 切 Provider / 主题 / 宽度。

## Decision (待定)

**推荐:** 单一方案 A —— 一份只读 `Subagent` 记录,五处呈现(Chat 卡片 / 下钻抽屉 / 侧栏嵌套 / Header 弹层嵌套 / Tasks 托盘双区)。
用户已确认:五处全做、Claude Code + omp + Codex、只出 1 个推荐方案 + 关键状态稿、授权真机验证。待用户点头:下面「待确认的 4 个取舍」。

## 设计问题(一句话)

子代理现在只是 Chat 里一条 `Delegated <input>` 工具行、侧栏里仅 Claude Code 的嵌套行;用户看不到它在做什么、做完没有、结果是什么,也点不进去。

## Phase 0 取证(本机真实数据,不是猜的)

| | Claude Code | omp | Codex |
|---|---|---|---|
| 子代理在哪 | `<会话>/subagents/agent-<id>.jsonl` + `.meta.json` | `<会话目录>/<id>.jsonl` + `<id>.md` | **独立 rollout 文件**(同一 `~/.codex/sessions` 树) |
| 父子关系 | meta 无父字段;靠目录归属。`meta.toolUseId` = 主 transcript 里 `Agent` tool_use 的 id | 子 jsonl 第 2 行 `session.parentSession`;主会话 `task` 工具结果 `details.progress[{id,agent,status,task}]` | 子 rollout `session_meta.source.subagent.thread_spawn{parent_thread_id,depth,agent_path,agent_nickname}`;主会话 `spawn_agent{task_name,message}` → 输出 `{task_name:"/root/<name>"}` |
| 类型 / 说明 | meta `agentType` / `description`(有 `requestShape:"background"`) | `progress[].agent`(如 `scout`)/ `task` | **无类型**;昵称(`Nietzsche`)+ `spawn_agent.message` |
| 运行状态 | jsonl 近 20 s 有写入(现有规则)+ 主 transcript `<task-notification>` | `progress[].status`(`pending` 等) | 主会话 `wait_agent` 之后到 `agent_message FINAL_ANSWER` |
| 结果 | 子 jsonl 最后一条 assistant 文本 | `<id>.md` | 主会话 `agent_message{Message Type: FINAL_ANSWER}` |
| 子对话格式 | 与主 transcript 同格式(`isSidechain:true`)→ 可复用现有 transcript 解析 | 同 omp 会话格式 | 同 Codex rollout 格式 |
| pi | 未发现子代理机制 → 不显示,不报错 | | |

**Codex 样本是现场触发的**(`codex exec` 让它 `spawn_agent` 一个 PING 子代理,真机 rollout 已核对形状,临时目录已清理);此前本机 14 个含 `spawn_agent` 字样的 rollout 全是说明文字,没有真实调用。
Claude 的 `subagents/` 样本来自 2026-10-25 的 `herdr-client` 会话。

### 现状(代码)

- 侧栏:`internal/history/subagents.go` 只读 Claude 的 `subagents/*.meta.json` + mtime;`agent_card.go` `openSubagents`(运行中展开,完成折叠计数)。Herdr 无父子信息。
- Chat:`frontend/src/turn.tsx` `toolCategory` 用正则 `task|agent|dispatch|spawn` 归 `agent` 类,一行 `Delegated …`,摘要 `Delegated N tasks`。
- Header 弹层(Agents/Quota/Services/Tasks)与 Tasks 托盘(`chat_plan.go`)都不认识子代理。

## 约束(来自 AGENTS.md / 既有决策)

- **必须保留:** 只读呈现;不新增 Herdr 方法;不写 Provider 文件;日志不含对话内容;状态点只用唯一的 `status_indicator`;控件走 `controls.go`;Header 触发器 `waiting/total` 的含义不变;Chat 仍锁定 Claude / Codex / pi / omp。
- **必须对齐:** chat-view 的墨色四档(`--i1..--i4`)、半径 Control 6 / Row 8 / Card 10 / Pill、无投影、hairline 边;`tipped()` 唯一 tooltip。
- **非目标:** 控制子代理(停止 / 发消息);子代理的计费 / 配额;给 pi 造数据;Herdr 侧任何改动。
- **布局是未文档化实现细节:** 读不到或格式变了 → 静默退化成现在的平铺 / 普通工具行(`Degraded layout` 场景)。

## 方案 A(推荐)

**统一记录:** `id · provider · type · description · status(running/done/stopped/failed) · started/ended · lastLine · result · parentCallId · depth`,由 `internal/history` 的只读 reader 按 Provider 产出,缓存、有界(沿用 24 条上限)、无写入。

| 呈现 | 要点 |
|---|---|
| **Chat 卡片** | 取代 `Delegated …` 工具行:类型 chip + 说明 + 耗时 + 状态槽;运行中一行实时尾巴(`Read terminal.go`);结束后 3 行结果摘要 + `N tool calls · 时长`;`Prompt` 展开父代理给的任务;`Open` 进抽屉。**1–2 个并发 = 卡片,≥3 个 = 名册(roster)**,行点击进抽屉 |
| **下钻抽屉** | 右侧 420pt,子代理自己的 transcript(复用 Chat 的 turn 渲染)+ 父任务 + 只读标记;`Esc` / `Back to call` 关闭;窗口 <1000 时盖住 Chat 列 |
| **侧栏** | 三家都嵌套在父行下;运行中显示,完成沉入 `N done`(可展开);Codex chip 用昵称,omp 用 agent 名 |
| **Header 弹层** | **不加第五个 Tab**(440pt 已 4 个):Working 里的父行带 `2 sub-agents` 徽标并嵌套子行,`N done` 折叠;`waiting/total` 不计子代理(用户不能寻址它们) |
| **Tasks 托盘** | 一个托盘两个区:计划项 / `Sub-agents`。**不按文本把计划项与子代理配对**(没有可靠 id,猜配对会骗人);有子代理运行时头部写 `· 2 sub-agents running` |
| **退化** | 取不到 → 普通工具行,无报错、无空卡片 |

### 被否决

- 第五个 Header Tab:挤、且子代理不可寻址,不该进 `waiting/total`。
- 计划项 ↔ 子代理按标题匹配:无证据链,宁可分区。
- 子代理控制(停止 / 发消息):Herdr 没有这条链路,违反「呈现层 only」。
- 独立「子代理」页面:抽屉足够,且不离开当前对话上下文。

### 最脆弱假设(premise collapse)

**三家的落盘布局都是未文档化的实现细节。** 若变了:读不到 → 退化为现在的行为,只影响对应 Provider 的一个 reader。
**Codex 的子 rollout 需要按 `parent_thread_id` 反查**(没有「父 → 子」目录)。方案:父会话开始时间之后、同日期目录内,只读每个文件首行 `session_meta` 过滤,结果按父 id 缓存,上限 24;若扫描成本过高就只在 Chat 打开且父会话有 `spawn_agent` 调用时才扫。

### 简化闸门

- **最小版(一句话):** 只做 Chat 卡片 + Claude Code,侧栏已有。
- 新增文件预计 6–8 个(见 Handoff),0 个新配置项、0 个新命令、0 个新依赖、0 个 Herdr 方法。

## 待确认的 4 个取舍

1. **Header:嵌套在父行(推荐) vs 单独 Subagents 分组/Tab。**
2. **Chat:1–2 个卡片 / ≥3 个名册(推荐) vs 一律名册。**
3. **Tasks 托盘:双区不配对(推荐)**——计划项与子代理不互相引用。
4. **Codex 子 rollout 扫描:按需(Chat 打开 + 父会话有 spawn_agent)(推荐) vs 随侧栏常驻读取。**

## Handoff(定稿后实施,每阶段可独立合并)

1. **P1 数据层(只读):** `internal/history/subagents.go` 泛化为 `Subagent`(加 `Provider/Status/Result/ParentCallID/Depth`);新增 `subagents_omp.go`、`subagents_codex.go`;Claude 补 toolUseId 与结果。表驱动测试 + 退化测试(缺目录 / 坏 meta / 未知布局)。
2. **P2 Chat 卡片 + 名册:** bridge 在 `ChatBridgeState` 带 `subagents[]`,`TimelineStep` 工具步骤带 `subagentId`;`frontend/src/turn.tsx` 渲染卡片;`mygo generate` 重生 TS;`smoke` 覆盖 5 种状态。
3. **P3 抽屉:** bridge `OpenSubagent(id)` 返回只读对话(复用 history transcript 解析 + 尾随);`frontend/src/drawer.tsx`;Esc / 窄窗盖层。
4. **P4 侧栏 + Header + Tasks 托盘:** `agent_card.go`/`sidebar.go` 去掉「仅 Claude」限制;`titlebar` 弹层嵌套行;`dock.tsx` 双区。
5. **文档:** `designs/subagents/README.md` 记 Decision;`AGENTS.md` 产品决策条目;`internal/history/CLAUDE.md`、`internal/nativeui/CLAUDE.md`、`docs/client-product-architecture.md`。

### 验证

- `GOTOOLCHAIN=auto go test ./...`、`go tool mygo vet .`、`cd frontend && bun run typecheck`、`git diff --check`。
- 真机(已授权):三家各派一个真实子代理(Claude `Agent`、omp `task`、Codex `spawn_agent`),核对 ① Chat 卡片状态迁移 running→done ② 抽屉内容与子文件一致 ③ 侧栏 / 弹层嵌套与沉底 ④ 重命名子目录后静默退化。
- 日志检查:无 prompt / 结果正文。

### 回滚

纯呈现层 + 只读 reader;关掉即回到 `Delegated …` 行与 Claude-only 侧栏。无数据迁移,无外部状态。

## Decision(2026-10-31,用户批准,按推荐定稿)

- 范围:Claude Code(`<session>/subagents/agent-*.jsonl|.meta.json`)、omp(`<session>/<name>.jsonl`,header `parentSession`)、Codex(同日期目录下的子 rollout,首行 `thread_spawn`);pi 没有子代理。全部只读 `internal/history`,Herdr 无父子信息也不新增。
- Chat:1–2 个子代理为卡片,≥3 个为 roster;“Open”进入只读抽屉(同一 transcript 解码器,无写入)。
- Header 弹层:子代理嵌套在父 Agent 下并带 `N sub-agents` 徽标,不加第五个 tab,不计入 `waiting/total`。
- Tasks tray:两段(计划项 / Sub-agents),不做文字配对。
- Codex 子 rollout 随侧栏刷新常驻读取(合并刷新 600 ms / 活跃 1.5 s,渲染路径零 IO)。
- 状态:running = 近 20 s 有写入,或最后一个工具调用仍挂起(10 min 内);done/failed 由 `Ended`/父工具结果推出;**stopped(2026-10-31 追加,真机核验)** 只认 Provider 自己的中断标记——Claude 子文件末行 user 行 `[Request interrupted by user]`(TaskStop 与 Esc 同一标记,父会话 task-notification 同时写 `<status>killed</status>`)、Codex `turn_aborted`;omp 没有对应标记,永不显示 stopped。
- Codex UI 路径已真机验证(卡片 / tray / 侧栏 ↳N / 完成态);性能基准 `go test -bench CodexSubagents ./internal/history`:3 天 × 400 个 rollout 冷扫 ≈ 110 ms(仅首次),之后每次刷新 ≈ 4 ms(只 stat)。
- 读取/链接/卡片投影放在 Shell 无关的 `internal/chatsession`,与将来的 Remote Host P2 共用。

## 文件

| 文件 | 说明 |
|---|---|
| `exploration.html` | 可点击探索稿(8 个场景 × 3 个 Provider × 深/浅 × 3 档宽度) |
| `preview-*.png` | live / parallel / settled / failed / drawer / drawer-narrow / sidebar / tasks |
