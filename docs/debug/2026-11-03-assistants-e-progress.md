# assistants-e 实施进度（长期记忆；每个切片结束必更新）
- 状态（2026-11-04）: A–E 全部完成并提交（E04 inbox、E05 常驻 Host `solco-hostd`、E06 移除 workboard/herdr-projects + 只读 `legacy-export`）；31 条 SOLCO 自有场景全部 verified；上游 1,193 条源用例的逐条台账未收口（见文末“台账现状”）
- 目标: `docs/personal-agent-go-mvp-plan.md` 阶段 A→E 全部完成（Go daemon 核心、Assistants 侧栏/WebView、Host v3 + 手机、C/D/E），并真机端到端验证。
- 授权: 用户（2026-11-03）一切自决、不提问；每个功能一个 commit（只 `git commit -- <paths>`）；agent 测试指定 glm flash 5.3。

## 规则（本任务专用）
1. 每个切片：先写失败测试（红）→ 最小 Go 实现（绿）→ 全量门禁 → commit → 更新本文件与台账。
2. 台账 `docs/shepherdr-2.1-parity-decisions.jsonl`：只有读过上游断言、有真实 target_test 文件并跑过才可 `verified`。
3. 子 module `plugins/solco-automations` 与根 module 互不 import；根仓只经 CLI JSON 对话。
4. 外部副作用不假称 exactly-once：claim → launch → reconcile，不确定即 `uncertain`。
5. 并行会话在 `solco-mobile` 有未提交 fixtures 改动：绝不 `git add -A`，只提交自己的路径。

## 切片台账（A→E）
| 切片 | 内容 | 状态 | commit |
| --- | --- | --- | --- |
| A1 | personalagent Store（SQLite）+ receipt/claim | 完成 | 见 git log |
| A2 | Herdr 执行适配（claim→launch→reconcile，uncertain，Resume，Busy） | 完成 | 同上 |
| A3 | CLI `assistant …` + JSON + daemon pump | 完成 | 同上 |
| A4 | 根仓 `internal/personalagent` Client + capability gate + 金文件契约 | 完成 | 见 git log |
| B1 | Goal/Commitment/Memory/Approval + CLI（coding task 必须 repoPath、goal 暂停挡 claim）| 完成（store+CLI；Chat 隔离在 B3） | 见 git log |
| B2 | PR Watch（确定性，github.go + watch.go + daemon 接线） | 完成（store+真机 e2e） | 见 git log |
| B3 | 侧栏 Assistants + /assistants WebView + AssistantsBridge | 完成（真机沙箱验证；Coordinator Chat 嵌入归 E01） | 见 git log |
| B4 | Host v3 路由 + golden | 完成（fakeCore 单测 + 11 个 golden；真二进制经 Host 的 e2e 见下） | 见 git log |
| B5 | 手机 /assistants | 完成 | 见 git log |
| C | 事件队列/Heartbeat/Memory 审阅 | 完成 | 见 git log |
| D | 统一规则 | 完成 | 见 git log |
| E | Coordinator/worktree/review/常驻 Host/移除 workboard | 完成（e1931ca E04, cd19616 E05, ab1aea2 E06） | 见 git log |

## 证据（append-only）
- 2026-11-03 modernc.org/sqlite 经 GOPROXY 下载很慢（>3 min），在后台拉取。
- 2026-11-03 A 真机 e2e（`plugins/solco-automations/scripts/e2e-assistants.sh`，独立 Herdr 会话 solco-e2e，pi + magpie/zhipu/glm-5.3-flash，scratch repo）：queued→running→done，run 记录真实 workspace w1/pane w1:p1；pi 真实回答 README 内容。
- 2026-11-03 e2e 发现并修复两个缺陷：(1) daemon 自我 relaunch 时不看 Assistants 在途 run → `Dispatcher.Busy()`（测试 TestBusyWhileAnAgentIsWorking）；(2) Resume 时窗格无 Agent 被误判 cancelled → `ErrAgentMissing` → run/task `uncertain`（TestResumeWithNoAgentInALiveWorkspaceIsUncertain）。
- 2026-11-03 重启 e2e（E2E_RESTART=1）：delay 0 → 重启后 uncertain、tasks=1 runs=1；delay 9（Agent 已在跑）→ 重启后 re-attach，最终 done、tasks=1 runs=1，无重复。

## 教训 / 坑
（见 `.agents/skills/solco-evidence-loop/LESSONS.md`，此处只记本任务特有）
- 2026-11-03 B1：migration v2（goals/commitments/memory_items/approvals），goal.go/memory.go/approval.go + CLI `assistant goal|memory|approval …`；SOLCO-A01/A02/A04/A05/A06/M01/M02/M04 在 `docs/solco-assistants-required-cases.jsonl` 标 verified（A03 等 B3 的 Chat 隔离，M03 等 C2 ContextBuilder）。
- 2026-11-03 B2：migration v3（events UNIQUE(source,resource,revision)、commitment cursor/last_error）；`Watcher.Poll`（bounded 后台 goroutine，不入 tick）；GitHubFetcher（401/403/404/429/5xx/离线→各自 code，永不当“无变化”）；e2e `plugins/solco-automations/scripts/e2e-pr-watch.sh`（本机假 GitHub + 真 Herdr + pi/glm-5.3-flash）PASS：未变化 0 task，改 head 后恰 1 task→done→needsYou=true。SOLCO-W01–W05/M05 verified。
- 2026-11-03 坑：e2e 里 daemon 用 `--standby` 会在 ~30s 因“二进制≠已安装”自退（shouldRelaunch）；e2e 脚本改成非 standby。macOS bash 3.2 下失败的 `[[ ]]` 不触发 `set -e` → 用 `assert`。
- 2026-11-03 A4：插件侧 TestGoldenFixtures 写 `testdata/*.json`（稳定 id/hash），根仓 `internal/personalagent` 严格解码（DisallowUnknownFields）+ 真实二进制往返测试；gate = `assistant overview` 能答。
- 2026-11-03 B3：`assistants_state/section/page/bridge.go` + `frontend/src/assistants/`；门 = 自动化插件门 ∧ `assistant overview` 探测；Assistants 区门开时取代 Projects 区（门关保持原样，E 阶段移除 workboard）。页面自己 3 s 轮询 Snapshot（IO 在桥 goroutine），每个命令带新 requestId。沙箱真机（`$HOME/.cache/solco-assistants-b3`，bundle id `com.whstudio.solco.sandbox3`，本地 link 的插件副本）：侧栏出现 Assistants 区且无 Projects；点 PR Assistant 进 `/assistants`，Needs You 显示 e2e 留下的 done 任务，点 “Got it” → 插件 needsYou=0、侧栏圆点与计数同步清除；“+” → New Assistant 表单 → 创建 Reviewer Bob 并自动选中，其 Watching/Done/Needs You 全空（与 PR Assistant 的 Watching 1 / Done 1 互不串线）。SOLCO-A03 仍 planned：其 Chat 隔离随 E01 的 Coordinator Chat 嵌入一起验。
- 2026-11-03 B3 坑：我的 shell 里还导出着阶段 A/B e2e 的 `SOLCO_ASSISTANTS_DB/HERDR_PLUGIN_STATE_DIR/…`，沙箱 app 继承后读的是 e2e 库（看到了旧 PR Watch 数据）。沙箱启动前先 `env | grep SOLCO_`，用 `env -u` 或 unset。
- 2026-11-03 B4：`internal/remotehost/personal_agent.go`：缝 `Options.PersonalAgent func(ctx)(*personalagent.Client, ok)`（nativeui `remote_personal_agent.go` 用原子指针发布，门关即撤），15 条路由（读 scope view / 写 scope manage，只用 POST），写必带 requestId，改动带 expectedRevision，审批带 64 位 hex actionHash；插件拒绝码原样映射 HTTP；`NotifyPersonalAgent` 帧只带计数；capability `personalAgent`/`personalAgentManage`/`personalAgentApprove`。Goal complete 不下放手机。

## 收尾证据（2026-11-04）
- 根 `go test ./...` 全绿、`mygo vet .` 干净、`mygo build` 成功；插件 `go test -race ./...` 413 passed；frontend `typecheck/build/build:mobile` 通过；mobile `typecheck/lint/test(549)/check:fixtures/check:chat-page/export:web` 通过；`git diff --check` 干净。
- 真 Herdr 隔离 e2e：`scripts/e2e-assistants.sh`（pi + glm-5.3-flash，queued→running→done，无重复）与 `scripts/e2e-pr-watch.sh`（PASS）在最终树上重跑 exit 0。
- E06 后沙箱 GUI（`$HOME/.cache/solco-assistants-e06`，bundle `…sandboxe06`）：新装先出 onboarding；跳过后侧栏为 Agents / Services / Assistants（+ → New Assistant…）/ Workspace，无 Projects 区、无 New Task 面板。
- SOLCO-E03 新测 `TestAnInterruptedUpgradeRollsBackAndTheNextStartFinishesIt`（迁移事务回滚 + 下次启动补完）；SOLCO-D03 `legacy-export` + 源码扫描测试禁止其它文件提及 `.herdr-projects`。
- 没有可用的 adb 真机连接（`adb devices` 空）：手机侧以 Host v3 golden + 真进程 Host e2e + 549 个 mobile 测试验证，未做真手机点按。

## 台账现状（诚实记录）
`node scripts/check-shepherdr-2.1-parity.mjs --gate=all`：31/31 SOLCO 自有场景 verified；上游 1,193 条静态源用例里只有 46 条有人工裁决行且全是 `planned`，其余 1,147 条未逐条审读断言。此门禁**未**通过，不能声称“上游全量用例已逐条等价”。计划文档自己写明 MVP/E 的功能验收不依赖全量签收；后续若要关账，按 `docs/shepherdr-2.1-test-parity.md` §2 的流程逐文件审读（核心 go-parity ≈10.5k 行、UI/client ≈14k 行）。
