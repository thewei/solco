# Solco Personal Agent — Go 原生核心、WebView 复用、阶段 A–E 交付规划

> **状态：2026-11-03 的 Go-only A–E 实施路线与历史计划；截至 2026-11-04 已有 Go Personal Agent / Coordinator / Memory / Host / Desktop+Mobile Assistants 代码。** 本文件保留后端模块边界与源测试基线，但一些“待实现/未上线”文案是规划当时的状态，不代表当前仓库。**新产品交互规划以 `docs/assistants-chat-first-redesign.md` 为准：Assistant 以共享 ChatView 为主页，任务/记忆/Context 收入更多面板，手机同一方案。新增的 **联系人/三种会话/群聊/@提及** 产品规范见 `docs/assistants-conversation-product-spec.md`（UX0–UX6），Go 授权 Directory、Herdr Skill 通讯/Mailbox 与 Control Center 领域实现见 `docs/assistants-collaboration-plan.md`（N0–N6）。** 不采用生产 Rust TaskBackend。
>
> **完整测试驱动实施规范**：`docs/shepherdr-2.1-test-parity.md`；冻结 2.1 全量测试目录 `docs/shepherdr-2.1-test-cases.jsonl`（**168 文件 / 1,193 静态声明**）、分批决策台账 `docs/shepherdr-2.1-parity-decisions.jsonl`（目前 46 planned，0 verified）、**31 条必测 Solco 自有用例** `docs/solco-assistants-required-cases.jsonl`（31 planned，0 verified）、对应生成/验收脚本均已建立。**测试资产 A0 已完成，不等于 Go 业务实现已完成。**
>
> 对应移动端：`solco-mobile/docs/personal-agent-mobile-plan.md`。架构权威仍是 `docs/client-product-architecture.md`；**本文是下一阶段的批准设计，不描述已经上线的行为**。实施入口若修改插件所有权、/projects、/automations、Host 生命周期，先向该权威文档写入明确的批准决议。
>
> **技术基线（保留）**：以 Shepherdr **2.1.0** 固定提交 `8df9402c63ebd97ae05ed309b0444e2d755cacc6` 为 **Go 功能/测试的唯一主基准**（行为、状态机、可移植测试），吸收成熟设计而不是部署其 TypeScript/Bun/hc 后端；**不再 Fork/部署 herdr-projects v1.2.27**。生产 MVP 从第一天使用现有 Go automations daemon、Herdr CLI/SessionStarter 和真实 Session；E 必须达到所采纳的 2.1 能力对照并提供 Go 常驻服务。**产品入口由旧侧栏 Projects 改为 `Assistants`（New Assistant）**，原 `Agents` 继续表示运行中 Agent，`Workspace` 继续是真实 Herdr 工作区。

## 1. 审计：之前路线会拖慢 MVP 的原因

| 旧方案的额外成本 | 仓库证据 | 现在的调整 |
| --- | --- | --- |
| 以旧 Rust v1 为主功能基线，之后才对照 2.1 并重写 Go | Shepherdr 2.1 已整合 Coordinator/Tasks/Review、Automations、Heartbeat、durable queue 等完整机制；Solco 已有 Go Herdr 启动/Runner | **2.1 为唯一主基准**；直接按其可验证行为设计 Go，跳过 Rust Fork/双任务数据库与二次迁移 |
| Rust Personal Automation + Go 旧 Automation 双调度器 | `plugins/solco-automations/internal/{daemon,schedule,runner}` 已持有调度、Agent、历史、补跑与重叠队列 | **只增强现有 Go Scheduler，第一天所有新旧规则仍是同一个 daemon** |
| 把新 Goal/Memory 放在 App Go 进程 | Mac 完全退出后内嵌 UI 与当前 Remote Host 消失；旧 Go 自动化已有 launchd daemon | 长期状态由唯一 Go daemon 持有，桌面 Go Host 只代理；未来把可远程的 Host 边界移入常驻 Go service |
| 重新开发 Chat 或项目 WebView | `frontend/src/projects/ProjectsPage.tsx` 已直接嵌入 `App` Chat；`projects_webview.go` 已有 URL=/projects、Go bridge、events | 抽取现有 Projects WebView 的通用布局/卡片/Chat 容器，**新产品只换数据投影和菜单** |
| 先做全套长期记忆、Autopilot、Reviewer 再可用 | 上游能力复杂，Mac 无 Shepherdr 2.1 Linux bubblewrap 同等级隔离 | MVP 只做可证明的 Goal→Watch→Agent→Result→Approval 闭环；高级记忆和自动审查后置 |
| 为每次 PR 轮询启动 LLM | 现有 Cron 可定时执行真正 Agent，成本较高 | Go 定时器调用**确定性 gh/GitHub API**比较状态；**仅变更时**才创建 Agent，减少成本和重复 |

**最小路径（采用）**：在当前 Go 自动化插件添加 Assistant 实体、personal 规则/持久存储/条件预检/目标关联，复用原 Herdr Runner；桌面借用现成 `/projects` WebView 的 **组件**（不是数据模型）建立 `/assistants`，手机用已有 Host v3。整个业务主路径对照 Shepherdr 2.1 Go 行为金样，避免“先上线 Rust 再替换 Rust”。

**能力边界**：Shepherdr 2.1 已有成熟的任务委派、Review、worktree、Heartbeat、持久化执行等机制，但它的 `server/core/*` 直接依赖自己的 `CoreSource`、`CoreStore`、`CoreAgents` 与 hc 终端；**不能直接照搬测试运行环境或通过改名接入旧 Herdr Socket**。MVP 每个 Assistant 仅支持一个 Coordinator Agent、一个 Goal/Watch 的清晰闭环，可创建第二个 Assistant 验证隔离；C–E 扩大为所采纳的 2.1 功能等价。其 **Linux bubblewrap-only 的 Autopilot 不能在 macOS 作为已实现安全隔离**；原项目的默认自动合并/高权限参数不照搬。

## 2. 明确 Go 化范围

**属于“100% Go 业务核心”的部分（E 阶段必须满足）**：

- Assistant 持久身份、可选 Workspace 关联、各自的 Goal / Commitment / Memory/检索和删除 / Execution / Result / Approval / Inbox；
- Task/Coordinator/Worker/Watcher/Review 状态机、依赖、队列、预算、执行身份关联；
- cron/once/interval/event/conditional Watch、持久调度、事件去重、崩溃恢复；
- 受控 Herdr Agent 启动/等待/观察、Git worktree/审查/PR、任务结果和用户批准；
- Mac 常驻 Go daemon、Host v3 业务路由、WS 事件、通知、设备授权与迁移工具。

**不在此含义之内**：

- Herdr 自身的实现语言及第三方 Agent CLI（Claude/Codex/pi）、其 PTY/Session；
- WebView 里的 React/TypeScript 界面、`frontend/src/App.tsx` 与移动 Expo/React Native；
- macOS Remote Desktop 的独立 Swift helper（已有其他项目范围）、系统库；
- 数据库引擎的实现语言。优先选 *无 CGO 的 Go SQLite 驱动*（备选 `modernc.org/sqlite`），必须本机 arm64 打包/压测通过。

**完成的判据不是仓库里没有 `.rs` 文档/例子，而是生产构建、启动、执行、后台调度、任务结果链里没有我们维护的 Rust/TS sidecar、FFI 或被替代的 herdr-projects runtime。** 保留 MIT 上游原文、固定行为夹具和出处不会违反此目标。现有 `hc`（如 Go 自动化中的可选 backend）不得被“暗中当成”运行当前 Herdr Session 的执行器；本产品只用 `herdr` backend。

## 3. 既有实测代码边界（2026-11-03）

- Mac 是 Go + MyGo，Herdr 当前 `0.9.5`；`internal/app/session_launch.go` 已有 `SessionStarter.StartSession`，经 `agent.RunLaunchTransaction` + `LaunchRegistry` 同 requestId 幂等。**当前 LaunchRegistry 是进程内 map，不是重启后持久收据**，长任务必须补 Durable Receipt。
- `plugins/solco-automations` 是**独立 Go module**（`github.com/thewei/solco/plugins/solco-automations`）：`internal/daemon` 有单调度进程/文件锁，`internal/schedule` Cron、补跑，`internal/runner` 实际执行，`internal/host` Herdr Agent/worktree，`internal/history` Run；`add --draft / approve`、`on_overlap skip|queue`、`cleanup_ttl_hours` 已在现有 Fork。这是核心的**第一归属仓位**，不能让根 module 直接导入该子 module 的 `internal/*`。
- 现有桌面 `internal/automations.Client` 经 CLI 读取 Go 插件；`internal/remotehost/automations*.go` 已供手机管理，`/api/v3/events` 已推送 automation；**不拆第二个 Scheduler**。
- 现有 `frontend/src/projects/ProjectsPage.tsx` 复用 `App` Chat，并有 `Aside`、`Compose`、`useProjects`；`internal/nativeui/projects_webview.go`、`projects_bridge.go` 是原来的绑定桥。**它们仍绑定旧 workboard 的 Task/Coordinator 语义；不能只改 URL 就冒称 Personal Agent 已接通。**
- `frontend/src/main.tsx` 当前 SPA 有 /chat、/projects、/files-viewer 3 条嵌入路由；`frontend/src/theme.css` 公用 tokens。移动端同一个 Chat 通过 `pnpm sync:chat-page` 生成快照，禁止单独复制或改 `src/generated/chat-page.ts`。
- **主基线**：Shepherdr 2.1.0 `8df9402c63ebd97ae05ed309b0444e2d755cacc6`（MIT）。`server/core/{source,automations,tasks,worktree,agents,heartbeats,store}.ts` 与 `server/{automations,heartbeat}.ts`、`server/common/trigger.ts`；`test/core-*.test.ts` 是 Go 对齐用的行为证据，不是可以直接在 Herdr 0.9.5 上运行的集成测试。Rust v1.2.27 仅在核实旧 Herdr API 差异时作为历史次要资料，**不再创建生产 Fork 或固定参考仓依赖**。Mac 不支持 2.1 Linux/bubblewrap Autopilot 隔离。

### Shepherdr 2.1 固定源码与测试对齐策略

A01 将 [Shepherdr 2.1](https://github.com/eliasstravik/shepherdr/tree/v2.1.0) 固定为**离线源码研究与 MIT 测试参考**，记录 SHA、`UPSTREAM.md`、原作者版权和选择性移植说明。生产 app/daemon **不分发、不依赖** Bun/TS/Shepherdr server 或 hc；也不再创建 Rust v1.2.27 Fork。研究源码可留在隔离研究目录（当前 `/tmp/solco-shepherdr-research.gPyMey/shepherdr` 仅临时存在，不是构建依赖）；需固定时使用独立 `../solco-shepherdr-reference` checkout，但检出的是 **v2.1.0**，不是 v1。**不能把上游 TS/hc 测试“直接通过”当作 Go/Herdr 验收。**

**行为对照矩阵（每个领域：读测试→提取状态转换/输入输出/异常→Go fixture 与单测→Herdr 0.9.5 隔离会话 E2E）：**

| 对齐域 | 上游 2.1 源文件/测试 | Solco Go 目标 | 阶段与完成证据 |
| --- | --- | --- | --- |
| durable automation | `server/core/automations.ts`, `test/core-{automations,database-lock}.test.ts` | 当前 Go `schedule/daemon` + 新事务收据/queue | A/B 基本幂等；C 完整并发/重启故障注入 |
| Time+Event/Heartbeat | `server/common/trigger.ts`, `server/heartbeat.ts`, `test/core-heartbeats.test.ts` | Go `Trigger`/`Watch`/`Heartbeat` | B PR 变化；C 组合触发与静默预算 |
| task/worker/deps | `server/core/{tasks,agents,dependencies}.ts`, `test/core-{tasks,dependencies,failed-starts}.test.ts` | Go Task 状态、Session/Pane ID、依赖链 | A 单任务；E 多任务/重启等价 |
| git/worktree/review | `server/core/{worktree,git,github-pr,github-merges,review-reader}.ts`, `test/core-{worktree,github-pr,github-merges,review-reader}.test.ts` | Go worktree/review/approval | B 手动审阅；E 完整 review/merge |
| needs-you/permissions | `server/core/{questions,cards,mailbox,unread}.ts`, `test/core-{cards,mailbox,unread,native-asks}.test.ts` | Go Approval/Inbox + Host v3 | B 一次性审批；C/E 完整恢复 |
| coordinator/autopilot | `server/core/{pilot-*,heartbeats}.ts`, `test/core-{coordinator-restart,pilot-lifecycle,pilot-wakes}.test.ts` | Go Coordinator 控制器 + 真实 Herdr Agent | B 人工协调；C 受限 heartbeat；E 可证明任务链 |

**完整性审计结论**：已索引 2.1 的 **168 个测试文件、1,193 条静态 test 声明（36 条动态/参数化声明）**。所有源案例必须在 `docs/shepherdr-2.1-parity-decisions.jsonl` 经过人工读取断言后落到 Go 等价、真实 Herdr 适配、现有 WebView/手机复用测试、明确拒绝或带理由的不适用；**不应把上游全部 UI/hc 测试一比一翻译成 Go**。完整映射、A0–E4 批次、Go-only 最终门禁见 `docs/shepherdr-2.1-test-parity.md`。该测试索引是完整来源，但 **0 条 Go parity 测试在本次被认定 verified**。

**明确不对齐**：Shepherdr 的 hc-specific terminal/agent launch/socket、Bun SQLite 的 API 形状、其整套 React 插件 UI、macOS 不具备的 Linux 隔离执行、默认高权限/自动合并。**额外自有能力**：个人 Assistant 的长期身份/记忆、非仓库目标、用户可撤销的持续承诺、Solco 现有移动配对/Remote Desktop。2.1 的 Heartbeat 并不等于用户想要的多 Assistant 长期语义记忆；**Assistant 自身的记忆隔离、来源、审阅、更正/删除、跨助手分享**必须增加 Solco 原生测试。

## 4. 收敛后的目标架构

~~~text
   Desktop MyGo shell                         Solco Mobile (Expo)
   ├─ /assistants WebView                     ├─ Assistants/Today/Inbox
   └─ existing Chat WebView/React App         └─ existing Chat snapshot
            |                                           |
      MyGo bound service                         paired /api/v3
            |                                           |
            +---------------- Solco Go Host -------------+
                                     |
                          CLI/Unix RPC adapter
                                     |
                  Go Personal Agent Daemon (单写者)
                   (继承/延伸 solco-automations)
                  ├─ Assistant(identity/scope) / Goal / Memory
                  ├─ Commitment / Rule / Durable event queue
                  ├─ Task/Execution/Approval/Result
                  ├─ Coordinator/Agent orchestration
                  ├─ unified Schedule + Provider monitors
                  └─ SQLite transactions + persisted receipts
                                     |
                      existing Go Herdr runner
                                     |
              Herdr runtime 0.9.5 — 唯一 Workspace/Pane/PTY
                                     |
                     Claude / Codex / pi / other agents
~~~

**唯一 Scheduler**：已有 Go daemon 是触发事实所有者。Personal Agent 不再额外创建 Rust keeper 或 MyGo 轮询定时器；MyGo UI 与手机只是控制端。

**唯一持久数据写者**：Go daemon 持久化新 Personal Agent 数据和触发事件；旧 `automations.yaml` / `history.jsonl` 仍由既有 Go 插件管理，首阶段不重写这两种旧数据。Go App 不直接修改 daemon 的私有 SQLite/YAML/JSONL。

**长期进程**：
- MVP 可先使用当前 Go 自动化 daemon 的 launchd 守护并让 Solco.app 内的 Host v3 继续照常响应手机。Mac 窗口关闭 ≠ 退出进程；完全退出 App 使当前 Host 断线，**不能声称手机仍能远程控制**。
- 阶段 E 将需要远程管理的 Host/Router/认证协调迁入常驻 Go Service，或让 App 作为同一常驻 Host 的 UI 客户端；这涉及现有 Chat/RemoteDesktop/Relay/TLS 绑定和端口的单一 Owner 切换，不可简单双开两个 /api/v3 服务器。E 完成后才能宣布“Mac GUI 关闭/退出，手机仍能远程调用”。

## 5. 最小数据契约：先保证长期任务不会丢

### 5.1 Rust/v2 行为的 Go 数据模型

SQLite（仅建议使用兼容的纯 Go 驱动，未加依赖）单一事务库：
- **`assistants`**：id、name、description、role_prompt_ref、scope（personal / workspace）、workspace_refs（可选）、coordinator_session_ref（独立真实 Herdr Agent 会话）、default_policy、status、version；这是用户创建的持久 Agent 身份，**不能复用 PaneID/RepoPath 当 Assistant ID**。首启可以提供一个默认 `My Assistant`，但用户创建多个 Assistant 时各有独立状态与记忆权限。
- `goals`：id、**assistant_id**、title、objective、status、version、created_at、updated_at；
- `commitments`：id、assistant_id、goal_id、success_criteria、exit_condition、policy_id、state、next_check_at、version；
- `rules`：id、assistant_id、commitment_id、schedule_spec、event_spec、watch_resource、enabled、overlap、catch_up、last_cursor、last_fire；
- `events`：id、source、resource、revision、seen_at、payload_ref，UNIQUE(source,resource,revision)；
- `runs`：id、rule_id、event_id、idempotency_key UNIQUE、claim_state、external_session、workspace_id、pane_id、agent_session_id、status、created_at、finished_at；
- `tasks`：id、**assistant_id**、run_id、kind、brief_ref、repo_path_ref、state、needs_you、review_state、version；
- `approvals`：id、task_id、action_hash、expected_revision、expires_at、state；
- `memory_items`：id、**assistant_id**、scope（assistant / workspace / shared-with-consent）、source_ref、content_ref、kind、consent、version、updated_at（MVP 只保留手工确认/运行结果的轻量摘要）；不同 Assistant **默认不可互读私人记忆**，共享须明示授权、可撤销；
- `command_receipts`：request_id UNIQUE、payload_hash、status、result_ref、created_at；
- `schema_version`：迁移水位线与旧数据版本。

**不要拆成 Go App 一份 Goal/Memory、插件再存 Task 的双权威。** 本计划以一个 daemon DB 统一承诺、任务和执行收据。UI projection 与有限缓存只读。

### 5.2 状态机（必要的最小集合）

- Goal/Commitment：`draft → active/watching → paused → completed/cancelled`；未知/失败不等于完成。
- Task：`draft → queued → claimed → starting → running → blocked/review_required → done/failed/cancelled/uncertain`。
- Run 的准入：先在事务中写 `command_receipt + run claim + event cursor`，提交成功后才能向 Herdr 产生副作用；启动后保存 Herdr 真实 ID。
- **重要限制**：本地 DB 事务不能与外部 Herdr 启动组成跨进程原子事务，不能宣称“严格 exactly-once 实际执行”。启动成功却未回 ID → `uncertain` → 查询 Herdr、可用的运行标记和结果；不能确认就停下来要求人工确认，禁止自动再次启动有副作用的任务。
- Result = 运行状态 + 可验证产物/证据 + 人工 Review 结论，而非“Agent 回了 Done”。
- Approval 带 action_hash、version、expiry；拒绝已过时/已处理的卡。撤销手机 Scope 后老卡不能再写入。

### 5.3 第一条外部 Monitor：GitHub PR（确定性）

`Go daemon` 按间隔查询 GitHub PR API（公共 repo 可只读，私有用用户已授权 `gh`/只读 token；不将凭据持久化到日志或 DB）。只存 PR ID、状态、head revision、merged_at、ETag/更新时间、last_check/error 等事实；**仅状态发生用户关心的变化**时生成事件、启动 Herdr Agent。429/401/网络失败只记失败和重试、不得当“PR 未合并”。最后一次 revision 去重，睡眠后最多对账一次而不补洪水。MVP 固定一类 PR watch，通用 webhook 邮件/日历移到 C。

`brief` 按任务生成：目标、触发的事实/来源、允许的工具、禁止的写/合并动作、时间预算、预期报告、完成证据、目标关联 ID。外部网页内容始终是数据，不能覆盖用户授权。

## 6. 最快 MVP：交付顺序重排

**对比结果**：过去先 Rust Task backend + Rust Scheduler，再 Go 产品层/手机，最后重写 Go；当前改为**从 A 开始就一个 Go scheduler + 一个 Go DB + 真正 Herdr Agent**。阶段 A、B 完成即可以用户测试，C/D/E 为渐进能力补齐，**没有第二次“从 Rust 数据迁 Go”**。

| 阶段 | 用户立刻获得什么 | 里程碑权威 | 不等待 |
| --- | --- | --- | --- |
| **A0：2.1 全量测试资产（已完成）** | 168 文件 / 1,193 源案例、可复现索引与分批台账 | 仅测试审计工具，无业务 runtime | 完整 Go 实现；0 条 parity 通过声明 |
| **A：Go 核心骨架** | 从现有 Agent Chat 发起一个持久 task，看到真实状态/结果 | Go daemon + Herdr | Rust plugin、完整 Review、手机所有页面 |
| **B：Personal Agent MVP** | PR Watching→触发后 Agent 分析→Needs You→手机查看/暂停 | 同一 Go daemon/Host v3 | 通用监测、无限 Autopilot、完整记忆 |
| **C：主动智能/可靠性** | 组合事件、Heartbeat、Memory、恢复更稳 | Go 事务与监测 | 第二 Scheduler/新 Runtime |
| **D：自动化彻底统一** | 原有与个人自动化一个规则中心/历史/预算 | 同一 Go Scheduler | Rust 任务引擎移植 |
| **E：完整 Go 核心收口** | Coordinator/任务编排/Watcher/Review/常驻远程完整等价，移除 Rust runtime 依赖 | **Go-only 产品核心（必须）** | 不移植 WebView/Herdr/Swift 源码 |

### 阶段 A — Go 核心骨架（独立可合并）

**A01（资产完成，行为移植未做）** 固定 **Shepherdr 2.1.0** SHA + LICENSE/UPSTREAM.md；已用 AST 盘点全部 `test/*.test.ts(x)` **168 文件/1,193 条声明**，生成可重现索引、校验脚本及 46 条计划台账；同时识别 36 个可能运行时展开的测试。下一步**不能只挑 8–12 组就宣称覆盖完整 2.1**：由 `docs/shepherdr-2.1-test-parity.md` 的 **A1/A2→B1/B2/B3→C1/C2→D1/D2→E1/E2/E3/E4** 按批阅读所有断言、先红后绿、保存 Go 测试或有理由不适用的证据。禁用 hc 特有语义；不引入旧 Rust Fork/第二 Runtime。

**A02** 在 `plugins/solco-automations` 同一 Go module 内扩展 `internal/personalagent`（或经代码审查确认的相邻内部包），构建最小 `Assistant + Task + Run + Receipt` Store/Service。单 daemon 访问数据库；CLI 新增 `assistant list/create/show`、`assistant task create/list/show`、`assistant run show`（拟议命令，需按现有 CLI 风格确定正式拼写），输出带版本的 JSON 并接受 stdin JSON（不让 prompt 出现在 argv）。一个 Assistant 有独立 ID，默认不要求绑定仓库。

**A03** `Go TaskDispatcher` 调用现有 `internal/host`、`internal/runner`，复用 Herdr worktree/Agent Start/Prompt/Wait；存入真实 Herdr Session/Workspace/Pane ID；支持 manual task（一个 Agent）与 research-only；无自动 Merge。

**A04** 先持久 claim 再启动，`uncertain` 回读和启动被人类信任/授权问题阻塞的状态处理；新任务 requestId 与 content hash 幂等；单进程/重启测试。

**A05** 根 module 的 `internal/personalagent` 只是一层 Go CLI/Domain Adapter，MyGo 能取真实列表；加 capability detection（不覆盖现有 `internal/workboard` 旧插件 adapter）。

**A 验收**：新 Go CLI 通过一个真实 Herdr 0.9.5 隔离 Session 启动 Agent，得到 ID/执行结果；在关 UI / 重启守护后不会重新创建第二个 Task；旧 `automations.yaml` 与历史不变。**A 可单独发布为技术预览，不依赖 B**。

### 阶段 B — 可用 MVP：Go Goal + WebView + 手机（独立可合并）

**B01** 在**同一 daemon DB** 实现 `Assistant`（新建/重命名/停用/可选 Workspace 关联）、Assistant-owned Goal/Commitment draft→approve、ExecutionRef、最小 Memory（用户明确确认、有 sourceRef、按 Assistant 隔离，候选不默认成事实）、Policy/Approval；支持 pause/resume/cancel；不推断已完成。MVP 至少能新建第二个 Assistant，证明记忆、Chat 身份、任务、审批和权限不会串线。此能力在上游 2.1 中没有完整同等测试，必须先通过 **SOLCO-A/M** 自有案例。

**B02** 在现有 `daemon` 的定时 tick 增加 PR 条件预检和事件归集：只跟踪一种 PR 资源，变更→一次 `research-only` Agent，成功后生成结构化结果与 Needs You。0 变化=0 LLM；权限不足/离线=显式 retry state。

**B03** 侧栏用 **`Assistants`** 替换旧工作看板 **`Projects`**（不是替换或改名现有 `Agents` 与 `Workspace`）。头部 `+`→ **`New Assistant`**，每一行代表一个持久 Assistant（用户自定义名称），嵌套显示其 Waiting/Watching/Tasks；**不再自动按 repo 创建 Project**。WebView 新入口 **`/assistants`**（Assistant 详情的路由 ID/导航在 Go 层映射），抽取 `frontend/src/projects/ProjectsPage.tsx` 的 shared `App` Chat、Aside/Compose/theme，但全新的 Go `AssistantsBridge` 读取 Assistant/Goal/Task 投影，不借旧 `workboard` 实例；展示英文说明 `Your assistants remember, follow up, and coordinate work.`。原旧 `/projects` WebView 与 `internal/workboard`/`projectsSection` 的引用先 feature-gated、B 验收后从主侧栏移除，历史兼容入口有短期只读迁移路径。**不能仅把 `Title: "Projects"` 改成 "Assistants" 而保留旧仓库任务的语义。** 真实 Workspace 树保留。

**B04** `internal/remotehost` 新增 capability-gated GET **assistants**/overview/goals/tasks/runs/approvals、POST **assistant create/rename/pause**、goal draft/approve/pause/resume 与精确审批决定、`personal-agent.changed` WS（仅 id/revision/count），与当前配对、Scope、Relay 路径共用；先生成 Host golden。外部兼容路径可保留 `/api/v3/personal-agent/...` 命名空间，不因为 UI 改叫 Assistants 就重发配对或强制改所有旧路由。

**B05** 手机同步 golden/Zod、HostClient、Queries；优先只建 Today/Watching/Needs You + Run Detail，点击现有 Chat 打开真正 Pane；手机端项目导航保持 client-local；移动端没有调度器。

**B 验收**：用户在现有 WebView Chat 创建 PR Goal→确认→Go 确定性 watch→触发一次真实 Herdr Agent→生成结果→手机看见、查看对应 Chat、暂停；另可委派一个手动 coding task，用户可查看 diff，合并仍人工完成。旧自动化、Terminal、Remote Desktop 无回归。**B 是 MVP，不等 C–E**。

### 阶段 C — 对齐 Shepherdr 2.1 的持久事件与自主语义

**C01** 时间 + 事件组合 Trigger、持久 event queue、水位线/优先级、跨资源去重，Task added/failed/blocked/completed、Git main-red/PR merged 事件由已验证事实产生。

**C02** Heartbeat Checklist + Progress/Complete：人类可改的目标检查规则、下次计划与完成证据；默认**不进行每分钟模型反复唤醒**。定期实际模型唤醒需有限预算与批准，Mac 不冒称 Linux bubblewrap 隔离已存在。

**C03** Memory 项目来源、候选→用户审阅→版本冲突/删除、轻量检索；ContextBuilder 有 token/权限预算，不拷贝私有大段历史/凭据。

**C04** 故障注入：数据库锁、事件写失败、Agent 已启动响应丢失、daemon crash、Herdr restart、WS 重连、手机双设备并发审批；未确认返回 `uncertain`，而不是重试外部副作用。

**C05** 任务分解的 Coordinator：先复用用户实际的 Herdr Agent + 系统提供的 Agent Skill/CLI 承接多步骤任务草案，**Agent 只能提出命令，不可跳过 daemon 认证/审查**。

**C 验收**：外部事件重复三次只生成一次执行意图；休眠后可恢复观察，证据明确；Memory 可纠正删除；一个 Goal 可有多个独立 Task，不丢 Link。

### 阶段 D — 一套 Go Automation Core

**D01** 无需迁移两个异语言 scheduler：本路线始终只有一个 Go daemon；D 的工作是从 legacy `automations.yaml`/`history.jsonl` + personal DB 两种规则表示整合成同一版本化 Go Rule/Run Store，而非切换 Rust→Go。

**D02** 保留 0.12.x 已有的 `draft/approve`、Cron/once、overlap skip/queue、catch_up_minutes、history 30 天/5000、cleanup_ttl_hours、headless/interactive、agent/model/MCP 参数和旧 CLI；新增组合 Trigger 与外部监控以及 per-Goal budgets。

**D03** 旧规则以 `legacyRuleId` 与迁移 ledger 单条 disabled dry-run、next-fire 对账、保存 lastOccurrence/eventCursor，确认后转新格式，旧规则只读历史保留；不能两个格式都激活同一规则。双设备手机在切换中只可读，不可编辑旧入口。

**D04** 统一手机和桌面 Auto 入口的数据层、状态与审批：从旧 `/api/v3/automations` 向新 Personal API 路由有兼容层、能力位/Golden，旧手机可以继续使用旧接口直到正式废弃窗口；迁移失败可按规则回滚。

**D 验收**：所有原有自动化行为/历史可对账，不损失 Cron、补跑、重叠队列和清理；同一规则任何时候只有一个 dispatcher，旧配置有可读备份；无 Rust daemon。

### 阶段 E — **完全过渡到 Go 业务核心（必须交付）**

**E01. Coordinator 原生 Go 任务控制器**：对齐 Shepherdr 2.1 的 coordinator、parent-child task 和 per-space 生命周期，但改为 **per Assistant** 持久身份，每位 Assistant 有自己的 Coordinator Session/Memory/Policy，可选择绑定多个现有 Herdr Workspace。Task draft、任务依赖图、状态跟进和需求拆分由 Go 管；LLM 仍是 **Herdr 管理的正常 Agent**，Go 不托管第二模型 Runtime。前端复用同一个 Chat `App` 组件，根据真实 Herdr SessionRef 切换实例。

**E02. Agent worker / worktree Go 等价实现**：对齐 Shepherdr 2.1 `core/tasks.ts`、`core/worktree.ts`、`core/failed-starts.ts`：独立分支、基线校验、隔离工作树、安全取消/清理、任务继续与重试、进程退出状态关联。继续调用已有 Herdr Launcher / plugin host 能力；默认不操作主 checkout。对 git worktree 目录名、符号链接、失败清理做硬路径校验。

**E03. Review 与 Merge 状态机**：实现 check/build/test→第二 Agent Review（可选）→对 diff/commit/hash 形成审阅证据→等待人类批准→原子地 re-check 版本/branch head 执行合并/PR；失败回到 Task、保留工作树。任何默认 auto-merge 必须单独批准，**MVP 及阶段 E 默认人工合并**。

**E04. Watcher/Inbox 与 Agent 事件归一化**：从 Shepherdr 2.1 的 stalled/needs-you/health/failed-starts/news 中吸收可验证部分；优先 Herdr `events.subscribe`/`agent get`/transcript 事实，只有低成本的确定性 fallback poll；不每分钟让 LLM 猜进度。Inbox 状态、通知、审阅结果都是同一 Go DB 快照，WebView/手机复用。

**E05. Go resident core + remote continuity**：收敛当前独立 Go automation daemon 与 Personal Core 为 **同一个 Go 常驻服务/单一调度进程**（launchd user service，Linux 可扩展 systemd），GUI 只是客户端。将手机需要的 Host v3 服务/配对证书/Relay 连接以**单一 Owner**转交常驻 Go 服务：先复用现有请求处理器的业务 seams，禁止同时监听同端口或使用两套令牌；桌面只消费同一 Host/本地 IPC。Remote Desktop、macOS TCC/Helper、UI-bound Chat 等需按其原生命周期契约迁移/分流，不能为了“后台常驻”放宽系统授权。

**E06. 移除旧 herdr-projects 产品集成**：不发布/调用 `herdr-projects` Rust binary、Shepherdr Bun server/hc、不保留 Rust Keeper/TaskBackend/双 Scheduler/第二 Agent PTY；B 验收后从主侧栏移除旧 `projectsSection`，E 清理 `internal/workboard`、旧的 `routeProject/routeTask/routeArchived`、`projects_bridge.go` 中专属于旧 CLI 的模型与命令、旧门禁及测试（先提供用户数据的只读导出/迁移检查，未安装的旧插件直接消失），保留真正的 Herdr `Workspace` 导航和 `Agents` 实时列表。完整产物只包含 Go 业务可执行、WebView 静态资源和已有系统/Herdr/Swift helper 依赖；源仓可保留 Shepherdr 2.1 固定测试来源/Go 金样，但运行时不依赖 Bun/Cargo。

**E07. 全量测试覆盖 + 数据迁移演练**：以 **Shepherdr 2.1.0** 全量源测试目录为主基准，要求 `node scripts/check-shepherdr-2.1-parity.mjs --gate=all` 最终通过：**1,193 条源案例**全都有人工作出的 Go 等价/Herdr 适配/现有 WebView 或手机测试/安全拒绝/有理由的不适用裁决，不能通过删掉上游 UI/hc 测试凑数字；**另有 `docs/solco-assistants-required-cases.jsonl` 31 条 Solco 独有需求全部 verified（不可标不适用）**。这构成 **1,224 项静态追踪义务，不等于必须新写 1,224 个 Go 测试**；采纳的每项核心能力要有 Go/真实 Herdr 证据。历史 Rust v1 只用于核实旧 Herdr API。A/B/D 时代的 Assistant/Goal/Memory/Task/Run/Automation ID 保持不变，schema migration 可备份、dry-run、回滚只读；真实 Pane 历史不重编号。**这个全量 gate 仅阻断 E 最终完成，不阻塞 B MVP**。

**E Go-only 完成验收（不能删减）**：
- 运行中 `ps` / launchd service 清单没有 Solco 自维护的 Rust Task/Automation/Coordinator 进程，产品打包不需要 `cargo`、`bun` 作为后端；
- 编码任务从创建→多 Agent→检查→人工 Review→合并，在同一 Go 状态机闭环；目标长期复查与自动化在 App 退出、GUI 重启之后继续，Mac 关机/睡眠时明确暂停；
- 手机即使 Mac GUI 退出仍能由常驻 Go Host 管理目标/查看历史/发起准许的 Herdr 操作；不能复用的 UI-bound 能力明确 disabled 而非虚报；
- Herdr Session/Pane 只由 Herdr 管理；旧 Goal/自动化和审批历史可以无损升级/查询；2.1 全量测试源案例均有明确最终裁决和实际测试/不适用证据（`--gate=all`）；Solco 自有 Memory/Assistant/安全测试另需全绿；
- 100% Go 核心并不等于去写新的 PTY、自己实现大模型或废弃 React Chat。

## 7. WebView 与手机：最低开发量设计

### Mac 界面：复用而不是重做

第一版在已有 `frontend` SPA 新增 **`/assistants`** route 和 Go Bound Service（`AssistantsBridge`）；提取 `projects/ProjectsPage.tsx` 的通用 **Chat+右侧 Inspector+Dock/Aside/Compose** 结构与现有 tokens。Solco Sidebar 旧 `Projects` 区直接产品替换为 **`Assistants`**，`+` → `New Assistant`；每个 Assistant 的 Chat 绑定它当前真实 Coordinator Herdr Session，Inspector 展示该 Assistant 的 Goal/Commitment/Task。**同一个 `App` Chat** 根据 Coordinator / Task Pane 身份切换，不创建第二 Chat transcript/queue。保留原 `Agents`（实时 Agent 列表）和 `Workspace`（Herdr 运行时树），旧 `/projects` 只在兼容窗口内受 gate 保护，E 删除旧 CLI 集成。

建议 UI 最小信息架构：`Needs You | Watching | Tasks | Done`（四个筛选），单条 Goal/Run detail 的 Drawer，底部/中部现有 Chat。Memory Review、复杂自动化编辑、Dashboard 趋势图后置，绝不为了 MVP 建第二套 UI 框架。React 仅 ViewModel+交互；写动作通过 Go Bridge，不能直接读本地插件 SQLite。

**双客户端 Chat**：保留 `frontend/src/App.tsx` 单一来源；若编辑共享 Chat 源码，则桌面 `bun run build:mobile`、手机 `pnpm sync:chat-page` 与 `pnpm check:chat-page` 必须成对验证；Personal Agent 新业务组件可以属于桌面 SPA，但不私自复制现有 Chat。原 `/projects` 的 bridge 和 source 只在具备 feature gate 的小切片里重构，避免一次重写整个 WebView。

### 手机界面

最低实现复用现有配对 HostClientV3/Queries/事件/Toast/通知能力，只新建 **`/assistants`** 的列表、Assistant detail（含 Goal/Task/Run）、Approval。所有业务调用用同一 Host v3，打开 Chat 跳到有效 instance/pane；View 与 Manage 权限区分，过期操作 409；Mac Host 当前不可连接显示离线，而不是“任务已经停止”。下一阶段升级独立常驻 Host 后手机无须改 API base。

## 8. 详细任务归属与验收矩阵

| 任务域 | 最早阶段 | 建议实现目录（**提案，不是现有文件**） | 测试 |
| --- | --- | --- | --- |
| Go DB / durable receipts / task state | A | `plugins/solco-automations/internal/personalagent/{store,task,receipt}.go` | SQLite 事务、重复请求、重启、已启动结果丢失 |
| Herdr Worker Adapter | A | 同 Go module 新 `internal/personalagent/dispatch.go`，复用 `internal/host`/`runner` | 0.9.5 真实隔离 Session/Pane，blocked/uncertain |
| Desktop Go bridge/gate | A/B | 根仓 `internal/personalagent`、`internal/nativeui/personal_agent_*.go` | JSON contract, UI 路由、真实会话跳转 |
| Watch PR + Goal approval | B | Plugin `internal/personalagent/{watch,goal,approval}.go` | 无变化零 Agent、merged once、401/429/超时 |
| WebView Assistants UI | B | `frontend/src/assistants/*`，重用 `frontend/src/projects/*` 纯布局组件与 shared `App` | New Assistant、多个 Assistant 切换、记忆隔离、bridge 重连、无双 Chat |
| Host v3 + mobile | B | Mac `internal/remotehost/personal_agent*.go`；手机 `src/contracts/host-v3.ts`/`src/lib/host-client-v3.ts`/`src/features/host/queries-v3.ts`/`src/app/assistants/*` | Host goldens、Zod、assistant scope/权限、重连、真实设备 |
| Trigger/Memory | C | Plugin `internal/personalagent/{events,heartbeat,memory}.go` | 多次重复、时区/DST、Memory 删除与注入 |
| Unified rules | D | Plugin `internal/{schedule,daemon,personalagent}` | 旧 Cron/queue/catchup/TTL 回归、dry-run 迁移 |
| Full Coordinator/Review/Resident Host | E | Plugin `internal/personalagent/{coordinator,review,worktree,watcher}.go`；按经过验证的跨 module 提取计划迁至常驻 Go Core | 混合并发、repo 保护、失败恢复、GUI 完全退出可手机连 |

**Go 跨 module 纪律**：当前 automations plugin 是独立 `go.mod`；根 module 不直接导入其 `internal/*`。MVP 根仓通过既有 `internal/automations.Client` 的本地 CLI/受控接口衔接，减少重构。E 如需合并为 root Go module，先定义稳定 public package、回归两边 CI、再原子迁移，不能复制两份 scheduler 代码。

**macOS 权限边界的现实限制**：Go CLI 的审批、参数校验、actionHash 能阻止**通过受控 API 发起的违规命令**，但当某个 coding Agent 已获得与用户同一 UID 的无限制 shell 时，它可能绕过 CLI、直接访问文件/执行 Git；这不是 Go/Rust 能靠软 Policy 消除的。MVP 禁止无人值守的高权限写入/合并，单次任务保留人工审批与可审阅工作树；想保证自动任务不可越权必须另做实际操作系统级隔离与端到端安全验收（Mac 不冒称 Linux bubblewrap 已可用）。

### 核心异常矩阵（A/B 开始就测试）

| 情况 | 正确行为 |
| --- | --- |
| 重复的同一 PR revision、同一 requestId | 同一个 Run/Task 回执；无第二 Agent |
| Herdr 创建 Agent 成功，但响应丢失 | state uncertain，先查真实 pane/session，不自动再启动 |
| 本机关闭窗口、App 完全退出、Mac 睡眠、Mac 关机 | 分别显示不同状态：daemon 可继续/Host 断开/暂缓/不可执行，不混为“完成” |
| 两手机重复批准，或者用户改过任务 | 只第一项生效；另一项 409 stale；无重复 Merge |
| 任务需要首次信任/MCP 权限 | blocked+Needs You；未经明确授权不自动答过权限 |
| Git worktree 中途失败 | 保留待审查工作树，禁止擦除用户未提交文件 |
| 原有自动化规则 | 不中断、无丢历史、无双调度，迁移前后 next-fire 可对照 |
| GitHub API 不可达、401、429 | 记 lastError、有限重试，不启动 Agent、不把“未读到”视为“未合并” |
| 未确认自动任务尝试写文件/合并/控制桌面 | 后端权限拒绝；LLM 的“我有权限”不能代替系统授权 |
| 外部 PR 文本写着“忽略以上规则” | 作为不可信数据，不影响允许的工具/权限/触发条件 |
| WebView 创建失败、移动端无新 capability | 保留旧 UI/可用 Chat；新入口隐藏或展示明确不可用说明 |

## 9. 上线、依赖和明确不做

**阶段门禁（实施后）**：
- root Go：`GOTOOLCHAIN=auto go test ./...`、`GOTOOLCHAIN=auto go tool mygo build`；
- nested automations Go module：`cd plugins/solco-automations && GOTOOLCHAIN=auto go test ./...`（root `go test ./...` **不会**覆盖子 module）；
- 前端：`cd frontend && bun run build` 与共享 Chat 修改后的 `bun run build:mobile`；
- 手机：`pnpm typecheck`、`pnpm lint`、`pnpm test`、`pnpm sync:fixtures` / `pnpm check:fixtures`、`pnpm check:chat-page`、`pnpm export:web`；
- 端到端：**专用 HOME / HERDR_SESSION / Git scratch repo**，不可在用户的现有会话和真正 Git worktree 做危险验证；`git diff --check`、`git diff --cached --check`。
- 新数据库驱动选型：优先 `modernc.org/sqlite`（纯 Go、`database/sql`），E 的 resident 服务与 Mac arm64 CGO_DISABLED / 构建体积必须实测；若不合适再对比 `github.com/ncruces/go-sqlite3/driver`，**不能在不验证模块/打包的情况下承诺零二进制开销**。
- **Shepherdr 2.1.0 为唯一主源码/测试基准**（MIT，固定 SHA），不进入运行依赖；Rust v1 仅作为旧 Herdr CLI 适配的可选历史参考，不 Fork、不打包。
- 依赖的外部账户：PR 监控只需 GitHub public REST，私有仓库需要用户已登录 `gh` 或明示只读 Token；其他源（邮箱/日历）不属于 B MVP。不额外需要云服务器/API 网关；现有 Relay 仅复用当前连接。
- 新设置项尽量少：Goal 首次批准时选择目标、间隔、只读/写审批、预算；其他沿旧自动化默认，不加十几项设置。

**本轮已交付 A0 测试资产（见 `docs/shepherdr-2.1-test-parity.md`）**：源码 AST 全量索引、46 条已计划的上游优先案例、31 条已安排的 Solco 自有必测案例、可再生成和两套台账联动的验收脚本；其余 1,147 条上游案例尚待按批审阅。尚未实现 Go Task/Coordinator/Memory，未运行真实 Agent 或改变现有自动化和用户文件。
