# Shepherdr 2.1 → Solco Go Assistants：全量测试审计与分批实施门禁

> **状态：2026-11-04，A–E 的 Go 业务已实施，31 条 Solco 自有场景全部 verified；上游 1,193 条源用例的逐条审读台账尚未收口（46 条 `planned`，1,147 条未审读），`--gate=all` 因此仍会失败。**（下文 §1 的“0 条 verified”是 2026-11-03 建索引时的快照。）
> **产品主计划**：`docs/personal-agent-go-mvp-plan.md`；**手机计划**：`solco-mobile/docs/personal-agent-mobile-plan.md`。本文是下一步的测试驱动实施规范，不能冒充已上线的产品事实。
> **固定上游**：[Shepherdr v2.1.0](https://github.com/eliasstravik/shepherdr/tree/v2.1.0) SHA `8df9402c63ebd97ae05ed309b0444e2d755cacc6`，MIT（保留原 LICENSE/作者出处）。旧 Rust `herdr-projects 1.2.27` 不再是基准/Fork/运行依赖，只能用于查询 Herdr 0.9.x 历史协议。

## 1. 全量测试索引：覆盖所有源用例，但不冒称 Go 已通过

采用 TypeScript AST 从该固定 SHA 的 `test/*.test.ts` 和 `test/*.test.tsx` 读取全部 Bun `test/it` 声明，连源行号、`describe` 分组、模板测试与参数化标记一起登记：

| 类型 | 数量 | 处理 |
| --- | ---: | --- |
| **测试文件** | **168** | 包括 1 个 `rows.test.tsx`，全部登记 |
| **静态测试声明** | **1,193** | 每条都有原文件、行号和场景名称；不是断言总数 |
| **动态/参数化声明** | **36** | 可在循环/`test.each` 中运行多次，须追读具体循环取值；实际展开数量不能由静态索引推断 |
| **Go 业务等价候选** | **467** | 读取所有 source assertion 后翻译为 Go 状态机/测试 |
| **Herdr 适配候选** | **68** | 将 hc-only RPC/Terminal 情形映射到现有 Herdr 0.9.5，拒绝照搬 hc |
| **Solco UI 行为候选** | **522** | 现有 Shared Chat / WebView / Expo 做相应 UX 测试，不复制原 DOM/CSS |
| **已有客户端与移动适配** | **86** | 以现有 Host、PWA、Voice、配对与平台测试提供对等证据 |
| **平台专项裁决** | **50** | Linux namespace、hc/Bun/plugin 等，要有明确拒绝或不适用原因及必要安全测试 |

自动建议的排期：`A=184`、`B=211`、`C=89`、`D=10`、`E=699`，合计 1,193。这只是**文件/场景规则生成的首次粗分**：E 中多数是已存在的 UI/平台行为，不能据此推断 E 要重写 699 个 Go 测试；D 上游用例不多，是因为 Solco 旧自动化迁移没有上游等价测试，必须补自有用例。

**真实状态**：46 条安全、核心、WebView/Host 场景已经进入首轮批次台账、状态均为 `planned`；另外 1,147 条尚未逐一审阅源码里的全部断言。**目前 0 条新 Go 用例被宣称 verified。**

### 本次已建立的文件

- `docs/shepherdr-2.1-test-cases.jsonl`：冻结的完整源测试清单（1,193 行），字段为 `id/source_file/source_line/suite/scenario/declaration/parameterized/disposition/phase/domain`；不复制源测试实现正文。
- `scripts/generate-shepherdr-2.1-test-index.mjs`：要求匹配上游固定 SHA、干净 checkout，TypeScript AST 解析，并拒绝文件数/声明数改变。
- `docs/shepherdr-2.1-parity-decisions.jsonl`：上游源测试的人工裁决与实施证据，独立于生成目录；现有 46 条 `planned`。
- `docs/solco-assistants-required-cases.jsonl`：**31 条 Solco 独有产品必测场景**（Assistant 身份、长期记忆、Watch、跨端、自动化迁移、Go-only），均为 `planned`；每个包含 `id/batch/scenario/status/target_test/evidence`，后续验证后手动记录真实测试。不得以“上游 2.1 没有”作为删除理由。
- `scripts/check-shepherdr-2.1-parity.mjs`：同时验证**上游 1,193 条静态声明的人工裁决**与**Solco 自有 31 条必测场景**；有实际文件、源断言审阅、验证证据才可标记 `verified`。`--gate=all` 必须覆盖两套台账（合计 1,224 项静态追踪义务，**不是 1,224 次 Go 测试**）。

**重现步骤（要求已装 frontend 的 TypeScript 包）**：

~~~bash
node scripts/generate-shepherdr-2.1-test-index.mjs \
  --source=/path/to/clean-shepherdr-v2.1.0 \
  --check
node scripts/check-shepherdr-2.1-parity.mjs

# 当前均会按预期失败：因为还没有把计划中的测试实施与验证。
node scripts/check-shepherdr-2.1-parity.mjs --gate=A1
node scripts/check-shepherdr-2.1-parity.mjs --gate=all
~~~

**本轮上游运行证据**：曾在隔离的 Shepherdr 2.1 checkout 尝试 `bun test test/core-automations.test.ts test/core-database-lock.test.ts test/core-mailbox.test.ts`，命令在 90 秒限制内未结束（超时）；**没有任何一组上游 Bun 测试被本轮宣称通过**。后续可在具备依赖/浏览器的独立上游环境再跑，但它只证明 2.1 的 TS/hc 代码在其环境中是否工作，**不等于 Go/Herdr 兼容**。所有阶段必须将其真实 `expect`/fixture/异常测试变成 Go、Herdr Contract、WebView 或 Expo 的验收证据。

## 2. 每条上游用例的必须审阅流程

1. **定位真实断言**：通过 S21 case ID 定位 `test/foo.test.ts:line`，阅读全部 `expect`、fixture、mock、环境分支与循环展开。一个测试包含多个不变量时写成 Go `t.Run` 或等价多条断言，不能凭标题猜行为。
2. **解耦运行时**：分离 `Given/When/Then` 中的业务事实、2.1 自己的 `CoreSource/CoreStore/CoreAgents`、hc Session、Bun SQLite、Git、外部 API、UI。Solco 用当前 Go daemon + 真实 Herdr 0.9.5 + 现有 WebView/Chat/手机；不得复制 hc socket 或私有 UI 源码到后端。
3. **明确裁决**：`go`（Go 状态机）、`herdr_adapter`（Herdr 真实协议适配）、`existing_ui`（现有 Chat/React Native 复用测试）、`unsupported_contract`（不支持时返回明确拒绝）、`not_applicable`（有出处的排除）。静态索引的 `disposition` 只表示初筛，不代表完成审查。
4. **先红后绿**：Go 单测/Host JSON Golden/UI 测试先复现正确失败；用最小 Go 实现使其通过；验证旧功能无回归。测试中的模型、GitHub、时钟、权限和 Git 分别用受控 fixture 隔离，真实 Herdr 相关边界另跑 scratch E2E。
5. **逐案留证**：台账改为 `status=verified` 前必须设置 `reviewed_assertions=true`、`target_test` 的真实文件和 `evidence`（测试命令/结果/实机记录）。`not_applicable` 需写出原因；高风险的不可用行为仍要有负面测试，不能删除案例来凑覆盖率。
6. **批次验收**：`--gate=A1/B1/...` 看被选定批次里的条目，不把全部自动建议阶段作为 MVP 阻断；`--gate=all` 只用于 E 最终**每条源用例都有明确裁决与证据，而且 31 条 Solco 自有需求都有实际测试证据**的全量签收。

台账计划记录形状示例：

~~~json
{
  "id": "S21-27ec9a665622",
  "batch": "A1",
  "action": "go",
  "status": "planned",
  "target_test": "plugins/solco-automations/internal/personalagent/receipt_test.go#TestDurableReceiptBeforeLaunch",
  "rationale": "必须先写持久收据再创建 Agent，重启后禁止双派",
  "reviewed_assertions": false,
  "evidence": null
}
~~~

`target_test` 当前是**拟创建的测试，不是已存在/通过的代码**。手动决策文件是持久台账，生成的 source case 文件是不可改写的来源索引。

## 3. 分 14 批执行：每批有独立回归与可回滚点

| 批次 | 2.1 测试源与实施重点 | 通过条件 |
| --- | --- | --- |
| **A0 测试基线** | 固定 2.1、168 文件/1,193 用例索引、自动生成脚本、判定台账 | **本次已完成**（未运行 Go 产品测试） |
| **A1 Durable Store** | `core-database-lock`、`core-automations` claim/restart、`core-mailbox` crash | 先持久 claim 后调用 Agent；DB 锁、超时、重复消息都不会无记录启动 |
| **A2 Herdr Execution** | `core-failed-starts`、`core-ids`、`core-client` 不确定响应 | 临时 Herdr 0.9.5 Session 真实 Agent/Pane 启动、状态回读、重启恢复；丢响应不再起第二个 |
| **B1 Assistant + Approvals** | `core-mailbox/questions/cards` 和 Solco 自有身份/记忆隔离用例 | 2 个 Assistant 各有 ID、权限、SessionRef、私有 Memory；旧审批不能批准新动作 |
| **B2 Watch/Goal MVP** | `core-automations` 跨机器/递归、`core-tasks` 精确 Merge 审批和自有 PR 检查 | PR 无变化 0 模型调用，变化时一次真实 Task；无需系统授权不能自动合并 |
| **B3 WebView + Mobile** | `layout-answers/sending/tasks`、`core-native-asks`、Solco Host goldens | 新 `Assistants` 侧栏 + New Assistant，复用同一 Chat，手机可查/暂停/审批并跳真 Pane |
| **C1 可靠事件调度** | `core-automations` 多事件/失败、`core-heartbeats/health` | 事件水位线、重复去重、防循环、预算、长时间恢复和有限补跑正确 |
| **C2 记忆与主动目标** | `core-pilot-wakes/plan-limits` 的可采用部分 + Solco 自有 Memory 测试 | 可纠正/撤销的长记忆，清楚来源，不跨助手泄露；Goal 结束有证明 |
| **D1 规则/存储合并** | `core-automations` 规则语义 + 原 Go `solco-automations 0.12.x` 回归 | 旧 cron/once、draft/approve、queue/skip、catchup、TTL、history 不退化，迁移不双调度 |
| **D2 自动化 UI/兼容** | `layout-automations` + 当前 Mac/手机 `/api/v3/automations` | 老手机仍能用；新旧 Rule 查询/控制结果一致，切换可回退 |
| **E1 Coordinator/依赖图** | `core-tasks/dependencies/coordinator-restart/task-relocation/pilot-lifecycle` | Go 状态机可追踪依赖/子任务/真正 Coordinator 会话，重启不误停别人 |
| **E2 Git/Worktree/Review** | `core-worktree/git/github-pr/github-merges/review-reader/tasks` | 提交与 Diff 精确审查、旧 approval 失效、脏文件保护、并发 Merge 与失败回滚 |
| **E3 平台/沙盒判定** | `core-native-ends/asks/secrets/pilot-sandbox/pilot-policy` 等 | Mac 没有隔离时禁高权无限 Autopilot；hc-only 能力安全拒绝/适配/解释 |
| **E4 Go-only 发布收口** | `core-remote/client/protocol` 选用行为 + Solco Go launchd Host 与旧 workboard 移除 | 退出 GUI 手机仍可访问常驻 Go Host；旧数据可审阅、无 Rust/TS 业务 sidecar |

### A1–B3 最短可用主线：测试先行的执行顺序

**A1.1** 在现有 Go `plugins/solco-automations` 独立 module 加 SQLite `Store` 与 `CommandReceipt`，只有 daemon 是 writer。首先为 `S21-27ec9a665622`、`S21-ee1aaf698a58`、`S21-0524107496f5` 建伪外部服务故障注入测试；存储不碰旧 YAML/JSONL。

**A1.2** 建立 Event/Run/Task 的唯一 requestId + payload hash，写事务成功后才允许调用真正 Herdr。不能声称 DB 与 Herdr 外部副作用形成跨进程 exactly-once；不确定状态回读后人工处置。

**A2.1** 复用 `internal/app/session_launch.go` 的事务语义和 `internal/herdr` 协议；`plugins/solco-automations` 独立 `go.mod` 不可直接 import 根 module 的 `internal/*`。通过已有插件 Herdr Host、受控 CLI 或经审查的 public adapter 复用，不建立第二个 Session runtime。

**A2.2** 在临时 HOME / 临时 Herdr Session / scratch Git repo 真实验证 Blocked、Running、Finished、Uncertain，显示真实 PaneID，不能靠标题/CWD 猜测运行中的 Agent。

**B1.1** 同一 Go daemon DB 里建立 Assistant 身份、可选 Workspace 关联、独立 Goal/Commitment/Memory/Policy/CoordinatorSessionRef；MVP 至少建两个 Assistant 证明隔离。允许不关联仓库的个人目标。

**B1.2** Assistant 首次使用自然语言生成可编辑 Draft，而非直接启动永久自动化；明确 owner、频率、成本上限、只读/需要确认的动作，审批以 actionHash+expectedRevision 抵御旧卡和并发。

**B2.1** PR Watch 用确定性 GitHub/gh 只读预检；同一 revision 只触发一次 Herdr Agent；401/403/404/429/超时都不能推断 PR 未合并。Goal 的退出条件独立于 Agent Turn End。

**B2.2** 任务结束记录真实结果、审阅证据、需用户确认。默认不自动写仓库或 Merge；有用户级 shell 权限的 Agent 可能绕过软策略，这不是 Go Policy 自带的沙盒保证，不能虚称强隔离。

**B3.1** 复用现有 `frontend/src/projects/ProjectsPage.tsx` 的纯布局/`App` Chat/Aside/Dock/Compose，而非旧 workboard 后端；新 `/assistants` 和 Go `AssistantsBridge`；左侧 `Agents` 继续表示 live agents，`Workspace` 继续表示 Herdr 工作区。旧 `Projects` 在新入口可用后替换，旧插件数据到 E4 才审阅清理。

**B3.2** Mac Host v3 先产真实 golden，再手机 `pnpm sync:fixtures`、Zod/Query/路由；两个 Assistant 的 Chat 各绑定真实 Herdr 身份，手机断线与旧设备正确显示不可用/离线。

**B3 MVP PASS**：两位 Assistant 独立记忆与任务、一个 PR 长期跟进无变化零模型调用、真实 Agent 响应/Result、手机看见同一 Pane、审批/暂停、重启不重复运行、旧 Automations/Chat/Git/Terminal/Remote Desktop 无回归。无需完成 E 的多 Agent Review 和全部 1,193 条源用例，MVP 仍可真实可用。

## 4. 必须新写的 Solco 测试：2.1 缺少的个人语义记忆

审查 `server/core` 和对应测试后，**尚未发现与“每个 Assistant 独立、可审阅/撤销的长期语义记忆”直接等价的完整测试体系**；2.1 更完整的是 Heartbeat、Task/Autopilot 和持久自动化。须补这些**Solco 自有需求测试**，不假称来自上游：

| ID | 场景和预期 | 批次 |
| --- | --- | --- |
| **SOLCO-A01** | 创建/重命名 Assistant 后 ID、已有 Task/Goal/会话关联不变 | B1 |
| **SOLCO-A02** | 没有 Repo/Workspace 也能拥有个人长期目标 | B1 |
| **SOLCO-A03** | 两个 Assistant 同一 Workspace 下，Memory/Approval/Task/Chat 不能串线 | B1 |
| **SOLCO-A04** | 停用 Assistant 后不再新发任务；原 Herdr Pane/人工更改不被清理 | B1 |
| **SOLCO-A05** | 同 requestId 重复创建只产生一个 Assistant；不同 body 报冲突 | A1/B1 |
| **SOLCO-A06** | 两手机并发更新 Assistant，旧 expectedRevision 409 | B3 |
| **SOLCO-M01** | Agent 总结只产生 Memory 候选，明确批准后才进入长期记忆 | C2 |
| **SOLCO-M02** | 每条 Memory 有 sourceRef、版本、作用域、撤销授权、产生时间 | C2 |
| **SOLCO-M03** | 改正/删除后 ContextBuilder 立即不再使用旧记忆 | C2 |
| **SOLCO-M04** | 跨 Assistant 共享 Memory 必须明确授予并支持撤销 | B1/C2 |
| **SOLCO-M05** | PR/网页中的提示词注入不能提升工具权限或修改批准规则 | B2/C2 |
| **SOLCO-M06** | Context/token 预算有界，不因摘要把密钥/全量历史写日志 | C2 |
| **SOLCO-W01** | PR 没变化时 0 Agent/LLM、lastChecked 正常记录 | B2 |
| **SOLCO-W02** | PR revision、事件重复、重启和网络中断下只建一个 Run | B2 |
| **SOLCO-W03** | GitHub 401/403/404/429/5xx/离线都不同于“没有变化” | B2 |
| **SOLCO-W04** | Goal 暂停恰逢触发，提交前重验权限禁止新 Agent | B2 |
| **SOLCO-W05** | Agent 结束 turn 不能自动将 Goal 判完成，必须满足退出条件 | B2 |
| **SOLCO-W06** | DST/时钟回拨/睡眠恢复不会补跑洪水 | C1 |
| **SOLCO-W07** | 缺硬沙盒时不得承诺无授权的写、删除、合并的安全保证 | B2/E3 |
| **SOLCO-R01** | 关闭窗口/完全退出 GUI/daemon 继续/Host 离线的不同状态准确 | B3/E4 |
| **SOLCO-R02** | 常驻 Host 保持同一个配对身份/证书指纹/设备撤销权限 | E4 |
| **SOLCO-R03** | 手机离线重连不重发审批/Run，撤销手机后 HTTP/WS 拒绝 | B3/E4 |
| **SOLCO-R04** | 无真实 SessionRef 时不可由 Pane 标题或路径猜 Agent | A2/B3 |
| **SOLCO-R05** | D 迁移期间现有旧手机 `/automations` 不退化/不双派 | D2 |
| **SOLCO-D01** | 旧自动化 Cron/queue/catchup/draft/TTL/30 天历史有同等测试 | D1 |
| **SOLCO-D02** | 逐条规则迁移水位线准确，旧/新不会执行同一个 occurrence | D1 |
| **SOLCO-D03** | 旧 `~/.herdr-projects` 仅显式备份/导出，绝不卸载即清空 | E4 |
| **SOLCO-E01** | Git head 变化后旧 Merge 批准失效，脏文件/符号链接受保护 | E2 |
| **SOLCO-E02** | 多 Assistant Coordinator 并发时不共享其他助手权限/会话 | E1 |
| **SOLCO-E03** | 数据库升级崩溃/launchd 双启动时只一个 dispatcher | E4 |
| **SOLCO-E04** | Run/Approval/Agent 通知和日志不泄漏 prompt、密钥、私有 PR 正文 | A1–E4 |

以上自有测试先写成 Go `_test.go`、Host 合同、WebView/手机测试和安全专项验收；不要为测试造第二个 Agent runtime。

## 5. 查漏补缺后新增的硬约束

1. **唯一权威**：Go daemon 写 Assistant/Goal/Memory/Rule/Task/Run/Approval；MyGo 和手机只通过受控接口；真实 Agent/PTY/Pane 完全由 Herdr 管理。现有 App `LaunchRegistry` 是进程内幂等，不足以承担 daemon 崩溃恢复。
2. **外部副作用不可原子事务化**：SQLite 的 commit 不能与 Herdr Agent 启动组成真实跨进程 exactly-once；`claim→launch→reconcile`，确认不了进入 uncertain/人工处理，禁止盲目重试。
3. **平台安全差异**：2.1 的 Linux-only bubblewrap Autopilot、hc grant model 不得被 macOS 模拟为已隔离。能跑同 UID shell 的 Agent 可能绕开 Go 的软权限；无真实 OS 隔离时默认人工审批高风险动作。
4. **存储/MIT**：Go 从 2.1 移植算法与 Go test fixture 时记录原始许可、来源 ID 和改动，源索引不纳入 app bundle；不用上游 Bun/TS sidecar/hc。
5. **迁移而不是一删了之**：只替换工作看板旧 `Projects` 侧栏，真正的 `Agents` 和 `Workspace` 不变。先旧数据只读备份/撤回入口；D 合并 Go Auto 配置，E4 才删除遗留 CLI/路由/门禁；不自动删除原 `~/.herdr-projects`。
6. **跨 Go module**：`plugins/solco-automations` 独立 `go.mod`，根 App 不可 import 子模块 `internal/*`。MVP 通过已有 CLI/有限本地 IPC；E4 若迁移 Host 属主到 Go 常驻服务，需单一端口/证书/配对 authority，禁止双服务共享。
7. **手机长连接真实边界**：A–D 当前 Host 仍在 GUI 进程；GUI 彻底退出时手机不能继续连接（即便 daemon 仍做任务）。E4 才有 GUI-independent Host。
8. **测试隔离**：真实 Herdr E2E 只能用临时 HOME/Session/scratch Git；GitHub 401/429 用模拟 HTTP；不可在用户真实 repo 做自动 Merge/清理；测试日志不携带提示词/令牌。
9. **不能通过删测试使门禁绿色**：最终 1,193 源测试必须逐条有采用/适配/复用/安全拒绝/不适用**理由与证据**；任意案例含多条断言都需要逐个审阅，不容用一条空 Go Test 代替。
10. **功能范围没有越界**：不复制 Shepherdr 2.1 的整套 UI、Bun 服务、hc Session；产品目标是 Solco 的 Assistant 记忆/主动跟进/协调，现有 Shared Chat 与 Remote Host 平滑演进。

## 6. 每批完成的证据格式与命令

每批以一次可独立合并/撤回的切片实施，保留源案例 ID、读过的原断言、Go Test 失败/通过日志、E2E（若涉及 Herdr）、运行时权限/错误、API Goldens、旧功能回归、回滚计划。先关新能力，验证通过再开启有限用户路径。

~~~bash
# root Go module
GOTOOLCHAIN=auto go test ./...
GOTOOLCHAIN=auto go tool mygo build

# nested automations Go module — root go test 不覆盖它！
cd plugins/solco-automations
GOTOOLCHAIN=auto go test ./...

# Desktop WebView
cd ../../frontend
bun run build
bun run build:mobile

# Mobile repo（Host golden 应先由 Mac 端生成，再同步）
pnpm sync:fixtures
pnpm typecheck
pnpm lint
pnpm test
pnpm check:fixtures
pnpm check:chat-page
pnpm export:web
~~~

手机 UI 或 Chat 有改动时，还需同步 `pnpm sync:chat-page`（不得手改生成快照）。Android 真机与 Mac 临时 Herdr 真实 Session 的证据分别留档；可以未做的验证必须标记为未做。

**E 总验收**：台账 `--gate=all` 无未裁决案例；采纳的 Go/Herdr/Host/UI 行为与自有 SOLCO-* 测试有实际证据；现有自动化不丢历史/不双调度，旧 plugin 入口与 Rust/TS 业务 sidecar 从 Solco 的生产运行链移除。原有 Herdr Runtime、WebView React 和 Swift helper 不属于必须 Go 重写的范围。

**这轮实际交付仅限完整库存/首批计划台账/索引与校验脚本；Go 核心代码、Agent 工作流和现有自动化数据均未变动。**
