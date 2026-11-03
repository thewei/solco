# assistants-e 实施进度（长期记忆；每个切片结束必更新）
- 状态: A 已完成并真机验证（待 commit）；下一步 A4/B1
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
| A4 | 根仓 `internal/personalagent` Client + capability gate | 待办 | - |
| B1 | Goal/Commitment/Memory/Approval + CLI（coding task 必须 repoPath、goal 暂停挡 claim）| 完成（store+CLI；Chat 隔离在 B3） | 见 git log |
| B2 | PR Watch（确定性） | 待办 | - |
| B3 | 侧栏 Assistants + /assistants WebView + AssistantsBridge | 待办 | - |
| B4 | Host v3 路由 + golden | 待办 | - |
| B5 | 手机 /assistants | 待办 | - |
| C | 事件队列/Heartbeat/Memory 审阅 | 待办 | - |
| D | 统一规则 | 待办 | - |
| E | Coordinator/worktree/review/常驻 Host/移除 workboard | 待办 | - |

## 证据（append-only）
- 2026-11-03 modernc.org/sqlite 经 GOPROXY 下载很慢（>3 min），在后台拉取。
- 2026-11-03 A 真机 e2e（`plugins/solco-automations/scripts/e2e-assistants.sh`，独立 Herdr 会话 solco-e2e，pi + magpie/zhipu/glm-5.3-flash，scratch repo）：queued→running→done，run 记录真实 workspace w1/pane w1:p1；pi 真实回答 README 内容。
- 2026-11-03 e2e 发现并修复两个缺陷：(1) daemon 自我 relaunch 时不看 Assistants 在途 run → `Dispatcher.Busy()`（测试 TestBusyWhileAnAgentIsWorking）；(2) Resume 时窗格无 Agent 被误判 cancelled → `ErrAgentMissing` → run/task `uncertain`（TestResumeWithNoAgentInALiveWorkspaceIsUncertain）。
- 2026-11-03 重启 e2e（E2E_RESTART=1）：delay 0 → 重启后 uncertain、tasks=1 runs=1；delay 9（Agent 已在跑）→ 重启后 re-attach，最终 done、tasks=1 runs=1，无重复。

## 教训 / 坑
（见 `.agents/skills/solco-evidence-loop/LESSONS.md`，此处只记本任务特有）
- 2026-11-03 B1：migration v2（goals/commitments/memory_items/approvals），goal.go/memory.go/approval.go + CLI `assistant goal|memory|approval …`；SOLCO-A01/A02/A04/A05/A06/M01/M02/M04 在 `docs/solco-assistants-required-cases.jsonl` 标 verified（A03 等 B3 的 Chat 隔离，M03 等 C2 ContextBuilder）。
