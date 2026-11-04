# OSC 7501 rev 0.4 合规审计 — Solco

- 审计日期：2026-11-04
- 仓库：/Users/wilson/Workspaces/wh-studio/solco
- 基线提交：4b0ce58（main，本地领先 origin/main 49 commits）
- 结论：**NOT COMPLIANT / 尚未完全兼容 rev 0.4**。不能以现有绿灯测试替代完整协议合规证明。
- 状态：审计时的 NOT COMPLIANT 为修复前基线；2026-11-04 已实现修复（见 §6 复验），**真实 Herdr/PTY/隐藏 Pane 端到端仍未验收**。
- 规范唯一对照：https://www.superlogical.com/rex/docs/build/program-status （rev 0.4, 2026-10-10）
- 修复顺序与验收：**docs/osc7501-compliance-implementation-plan.md**

## 1. 审计范围和架构约束

当前链路：Herdr terminal attach 输出 → vendored MyGo Terminal OSC 扫描器 → OnProgramStatus → internal/agentstatus（Parse/Set/Store）→ internal/nativeui 的 Agent/Pane 投影。并行存在 apphook status / session identity 回退链路。

审计涉及：
- **third_party/mygo/plugins/terminal/osc7501.go**, **terminal.go**, **osc7501_test.go**
- **internal/agentstatus/protocol.go**, **records.go**, **store.go** 及测试
- **internal/nativeui/terminal.go**, **program_status.go**, **chat_hooks.go**, **history_state.go**
- **docs/client-product-architecture.md** § OSC 7501 决策及 **third_party/mygo/LOCAL-PATCH.md**

**权威性分层**：规范约束报文、记录、生命周期、探测与安全。规范不定义 Solco 的 Hook 优先级。Solco 的产品决策另要求“OSC 7501 对正在工作的原生程序状态优先，Hook 仅作适当回退”。Herdr 继续独占进程/PTY/Pane/Agent 会话事实。禁止通过客户端私有 PTY 或第二 runtime 规避生命周期缺口。

工作树已有大量与 Assistant/Personal Agent 相关的 staged/unstaged/untracked 修改；本审计未改动、清理、stage 或提交这些资产。

## 2. 确认缺陷（按修复优先级）

| ID | 级别 | 位置 | 证据与后果 | 规范/产品约束 |
|---|---|---|---|---|
| OSC-01 | P0 | internal/agentstatus/protocol.go:135; records.go:28-47 | 显式空 id 不被拒绝。临时探针验证 Parse(state=done:id=) 返回 root；因此 state=clear:id= 会清全部记录。 | § Records and ids：不合法 id 整报文拒绝，禁止落到 root。 |
| OSC-02 | P0 | internal/agentstatus/store.go:59-72; program_status.go:21-35 | ApplyProgramReport 直接返回刚收到的状态，忽略 Set.Display 的更高优先记录；探针：root idle 更新之后，blocked child 仍在集合，但 UI 收到 idle。 | § Records and ids：root/child 同时有效；Solco 现有紧急度投影不得丢 blocked。 |
| OSC-03 | P1 | third_party/mygo/plugins/terminal/osc7501.go:15-16, 84-94 | 扫描器按 body 长度判断 4096，而不是包括 OSC 前缀和 ST 的完整序列。临时红灯测试证明 4099 字节完整序列被接受。 | § Limits：整个序列最大 4096 字节，违反则整体丢弃。 |
| OSC-04 | P1 | internal/agentstatus/protocol.go:97-132, 194-200 | key 长度超 16 被当作畸形 pair 跳过，仍接受整个报告；探针对 17 字节未知 key + state=done 返回 accepted=true。并且最后值覆盖机制没有验证所有重复字段的单项上限。 | § Limits：每对先查硬上限，违反则整报文拒绝。 |
| OSC-05 | P1 | internal/nativeui/program_status.go:21-35; terminal.go:809-811; history_state.go:413-419 | MyGo 文档明确 OnProgramStatus 在终端 goroutine 执行；回调直接更新 Shell projection、Agent 和 broker，没有像 Hook 路径那样 applyOnWindow。这是**经代码确认的跨线程更新风险**，race 未经设备复现。 | Solco UI-lane 约束，线程安全。 |
| OSC-06 | P1 | records.go:75-87; store.go:83-95; terminal.go:809-811 | DropTransient/Reset 虽实现，但没发现 OSC 133 A 或 RIS 输入事件绑定；Terminal 也未设 OnExit 生命周期回调。Hook released 不能代替所有通用程序的进程退出。 | § States：process exit / OSC 133 A 丢 working/blocked；§ Records：RIS 全清，DECSTR 不清。 |
| OSC-07 | P1 | internal/nativeui/terminal.go:760-770, 902-929 | 只为可见 Pane 建立 Terminal；从可见集合移除就 PaneClosed，连 done/error 也清空；此时 Herdr 的原有 Pane/PTY 仍可能活着。隐藏期间也不再扫描 OSC / 应答查询。 | § Records：记录属于终端而非屏幕；§ States：done/error 跨退出/新 prompt 存活；移动 UI 不应误认为 runtime 已终止。 |
| OSC-08 | P1 | internal/agentstatus/store.go:24-29, 83-95 | 任意 2 分钟未发 OSC 7501 的 working/blocked 可被后续 Hook 接管；探针确认 still-working 原生记录在 3 分钟后被 Hook 覆盖，而记录本身仍存在。 | § States 明确无心跳、不能要求持续重发；Solco “原生状态优先”决策不能仅靠沉默失效。 |
| OSC-09 | P1 | third_party/mygo/plugins/terminal/osc7501.go:40-122; terminal.go:836-840 | 查询返回一个 bool，同一读块出现多个 OSC 7501;? 时只会发一次 reply。未做实际 PTY 端到端确认。 | § Feature detection：每次合法查询均应收到能力应答。 |
| OSC-10 | P1 | internal/nativeui/program_status.go:28-34; chat_hooks.go:152-185 | 任意通用程序的 report（例如 cargo/brew）都会调用 upsertProjectionAgent；即使 Pane 原先没有 Agent，也会创建一条 Agent 投影。 | OSC 7501 是通用程序协议，Solco 的 Agent 身份仍来自 Herdr/Hook，不得由通用状态伪造。 |
| OSC-11 | P2 | internal/agentstatus/store.go:83-127 | Hook 已获准接管后，Store.Display 仍先看任何遗留 OSC done/idle 记录。探针：Hook 返回 working，但 Display 仍是 done。 | 投影的单一生效来源应一致。 |
| OSC-12 | P2 | internal/agentstatus/protocol.go:173-179 | 空 base64 文本被拒绝；规范语法允许空值，空文本可由零长度标准 base64 表示。需要补兼容测试后按规范处理。 | § Syntax：value 字符集使用 *，title/msg 为 base64 UTF-8。 |
| OSC-13 | P1 | internal/agentstatus/protocol.go:149-151, 222-238 | 33-byte app 字段超硬上限，却当成缺省 app 接受整报文；临时探针确认 state=working:app=33个x 返回 accepted=true。 | § Limits：app≤32；违反硬上限整报文拒绝。 |

## 3. 协议符合性矩阵

状态说明：**PASS** = 代码与现有目标测试覆盖；**FAIL** = 已发现矛盾；**PARTIAL** = 部分实现但条件缺失；**OPEN** = 尚无完整端到端证据；**OPTIONAL** = 规范允许不做。

| 条款 | 判定 | 证据 / 缺口 |
|---|---|---|
| OSC 7501; pairs，ST=ESC反斜线/BEL | PASS | osc7501_test.go framing |
| 跨 read chunk 重组 | PASS | TestOSC7501Framing |
| 忽略其他 OSC 编号 | PASS | TestOSC7501IgnoresOtherSequences |
| 取消不完整序列、丢弃超长 body | PARTIAL | 取消路径测试通过；whole-sequence cap FAIL |
| 完整序列 ≤4096 | FAIL | OSC-03 红灯探针 |
| pairs 用冒号分割 | PASS | protocol.go |
| key 是小写 ASCII | PASS | validKey |
| key 上限 16、整报文拒绝 | FAIL | OSC-04 |
| value 指定字符集，畸形 pair 跳过 | PASS | validValue / TestParsePairRules |
| 未知合法 key 忽略 | PASS | TestParsePairRules |
| 同键最后值生效 | PASS | TestParsePairRules（超限重复值未覆盖） |
| key/value 周边 whitespace 去除 | PARTIAL | 仅 Trim ASCII 空格，未覆盖其他空白 |
| state 必填、未知 state 丢整报文 | PASS | TestParseWholeReportDiscards |
| 六种状态含 clear | PASS | protocol.go |
| 无 id 为 root | PASS | protocol.go / records.go |
| 显式 id 语法 1–8 段、单段≤32、总≤128 | PARTIAL | 非空合法/非法覆盖，id= 错误接受 |
| 不合法 id 不得误写 root | FAIL | OSC-01 |
| root 与 child 可同时记录 | PASS | TestSetDisplayRankAndInheritance |
| 按 id 整记录替换，省略字段消失 | PASS | TestSetReplaceAndClear |
| clear:id 删除相应子树 | PASS | TestSetReplaceAndClear |
| clear 无 id 删除全部 | PASS | TestSetReplaceAndClear；受 OSC-01 威胁 |
| app 从最近祖先继承 | PASS | TestSetDisplayRankAndInheritance |
| kind 仅 blocked/未知视为缺省 | PASS | TestParseFieldRules |
| progress 仅 working/blocked、0–100 | PASS | TestParseFieldRules |
| app 格式及≤32 | FAIL | 非法字符视为缺省正确；但长度>32 的硬上限应该拒绝整报文（OSC-13） |
| title 编码≤256、解码≤192 | PARTIAL | last value 检查，重复字段之前的超限未保证拒绝 |
| msg 编码≤2732、解码≤2048 | PARTIAL | 同上 |
| base64 padding 可选 | PASS | TestParseFieldRules |
| UTF-8 和 C0/DEL/C1 拒绝 | PASS | decodeText / tests |
| 空 base64 文本 | FAIL | OSC-12 |
| 256 records / 最旧更新时间 LRU | PASS | records.go / TestSetLRUCap |
| working/blocked 在进程退出后清理 | PARTIAL | 仅 Hook released；通用程序退出链路缺失 |
| OSC 133 A 新 prompt 清瞬态 | FAIL | 未发现接线 |
| done/error 跨退出、新 prompt 保留 | PARTIAL | Set 正确，Pane detach 会清空全部 |
| RIS 清全部，DECSTR 不清 | PARTIAL | Set.Reset 有；未发现 reset 事件接线 |
| 主/备用屏幕切换不影响记录 | OPEN | 需真实 Terminal attach 测试 |
| 不要求 heartbeat | FAIL（生效状态） | OSC-08 的 2 分钟接管 |
| 一次 feature query 得到能力应答 | PASS（单次） | TestOSC7501Query；仅 OnProgramStatus 非 nil |
| 多次 query 一一应答 | FAIL | OSC-09 |
| query 通过 Herdr attach 原路回到程序 | OPEN | 无隔离 Herdr/PTY 实测 |
| 记录隐私：回复不回显 id/title/msg | PASS（源码） | 固定 osc7501Reply |
| 记录文案绝不作为 markup | PASS（当前面） | 当前只将 status 字符串送进投影 |
| 文本方向控制字符外显安全 | OPEN | 当前未展示 title/msg；扩展通知 UI 前验收 |
| 通知/声音外部效果限流 | OPTIONAL | 当前 OSC 链路没有直接通知动作 |
| OSC 9;4 映射、收到 7501 后停映射 | OPTIONAL | 当前没有 OSC 9;4 → 7501 映射 |
| Herdr/Hook 会话身份仍独立 | PASS（设计） | chat_hooks.go 持续处理 identity；运行级需联调 |
| 多子记录最紧急状态不被降级 | FAIL | OSC-02 |
| 普通程序状态不伪造 Agent 身份 | FAIL（代码路径） | OSC-10 |
| 不可见 Pane 的状态与查询 | OPEN / 缺口 | OSC-07，需端到端复现 |
| 终端回调安全进入 UI lane | FAIL（代码路径） | OSC-05 |

## 4. 执行验证与复现实验

执行环境为实际 Solco checkout；本轮未改业务代码。以下验证已执行：

- **GOTOOLCHAIN=auto go test ./internal/agentstatus -count=1 -v** → PASS，16 个测试。
- 在 **third_party/mygo** 执行 **GOTOOLCHAIN=auto go test ./plugins/terminal -run '^TestOSC7501' -count=1 -v** → PASS，4 个测试。
- **GOTOOLCHAIN=auto go test -race ./internal/agentstatus -count=1** → PASS；该结果只覆盖 Store 测试，不证明 nativeui 回调线程安全。
- **GOTOOLCHAIN=auto go test ./internal/nativeui -run '^TestHandleAppHookStatusUpdatesProjectionAndInteractions$' -count=1** → PASS；仅 Hook 既有集成路径。
- **git diff --check** → PASS（未暂存修改）。**git diff --cached --check** → FAIL：三份本轮开始前已 staged 的 Assistant 规划文档存在末尾空行，未改动这些文件；该失败不能归因于 OSC 修改。
- 使用仓库根目录临时 Go 探针（运行后删除）：
  - state=done:id= → accepted=true，ID=""；**FAIL**。
  - state=done:17-char-key=x → accepted=true；**FAIL**。
  - state=done:msg= → accepted=false；**FAIL（宽容互通项）**。
  - state=working:app=33个x → accepted=true、app=""；**FAIL（字段硬上限）**。
  - working root → blocked child → idle root：第三次返回 idle，Store.Display 为 blocked；**FAIL**。
  - done report → hook working：hook 输出 working，Store.Display 为 done；**FAIL**。
  - still working native → 三分钟后 Hook blocked：applied=true；**产品优先级 FAIL**。
- 在 vendored Terminal 测试目录创建后删除临时 TestOSC7501AuditWholeSequenceCap，**故意红灯**：4099-byte 序列仍被接受为 4090-byte body。测试退出码 1，说明现有绿灯测试遗漏边界。

以上探针文件均由 shell trap 清理。没有把预期红灯测试永久提交到业务测试套件；下一实施阶段应将其改成先红后绿的正式回归测试。

### 未完成验证

- 全仓 **go test ./...** 曾发起，但 Devspace 工具报内部错误，未返回最终退出码或测试结果；不能宣称 PASS。MyGo app build、实机 UI 未运行。
- 未在隔离 HOME + 独立 Herdr socket 下检查查询回复是否穿透 attach。
- 未证明 OSC 133 A / RIS / process-exit 的真实 terminal 事件契约。
- 未对所有移动端/远程状态订阅做端到端验收。
- 这些事项在符合性矩阵中保留为 OPEN，不得表述为 PASS。

## 5. 修复原则与交付门槛

1. **协议优先于便捷实现**：MUST/MUST NOT 严格对照 rev 0.4；产品决策只管优先级与展示，不得修改协议语义。
2. **协议通用状态 != Agent 身份**：状态可归属 Pane/Terminal；只有 Herdr/Hook 已确认 Agent 时才升级 Agent 状态投影。
3. **记录属于活着的 Terminal**：分离可见 UI ≠ process exit ≠ full reset；不可将三者合并为 PaneClosed。
4. **原生状态优先但不永久假死**：以真实生命周期事件与 session identity 为切换依据，而非 2 分钟静默判定。
5. **测试先红后绿、整序列原子性**：扫描、解析、记录、Hook 仲裁、UI lane、attach E2E 都有负例；每个 FAIL 必须留可复现断言。
6. **不得越权**：Herdr 仍是 runtime/PTY 权威；优先使用现有 Herdr/MyGo 能力，缺口需先核实 API，不造私有 socket 或第二 PTY。
7. **兼容声明条件**：全部规范 MUST 项通过 + 同环境真实端到端通过 + 无 P0/P1 未关闭，方可宣布 rev 0.4 完全兼容。

[PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md

## 6. 修复实施回环（2026-11-04）

**新增用户权威要求**：Solco **自身 Hook 绝对优先于 Herdr 的 Hook**。实现遵守：正在运行的原生 OSC 7501 transient > Solco 自有 apphook socket > Herdr 事件 spool（无 Solco/OSC 时兜底）。所有投影更新统一经过这套 `internal/agentstatus.Store`；Herdr 的进程、Pane、PTY、Agent 会话事实仍为权威，不增加第二 runtime。

| 缺陷 | 代码落点 | 本轮修复状态 |
|---|---|---|
| OSC-01 空 id 回落 root | protocol.go + strict limits tests | **已修复，目标测试通过** |
| OSC-02 并行 child blocked 被 root idle 掩盖 | store.go + priority tests | **已修复，目标测试通过** |
| OSC-03 完整序列上限 | mygo/osc7501.go + 4096/4097 两终止符边界 | **已修复，目标测试通过** |
| OSC-04 key/重复字段硬上限 | protocol.go + strict corpus | **已修复，目标测试通过** |
| OSC-05 终端 goroutine 修改 UI | terminal.go guarded applyOnWindow | **代码修复，race/真机仍需确认** |
| OSC-06 OSC 133 A / RIS / process exit | terminal callback + Herdr shell-idle process_info seam | **代码修复，真机生命周期仍需确认** |
| OSC-07 隐藏 Pane 错删记录 | syncTerminals 不再 PaneClosed；权威 Pane/Terminal replacement 清理 | **记录保留已测；隐藏期间原生流不可观测（协议限制）** |
| OSC-08 2 分钟静默超时压过原生状态 | store.go 删除超时、真实事件清理 | **已修复，1 小时静默用例通过** |
| OSC-09 多个 query 只有一次 reply | scanner 有序事件与 Terminal Conn 回传 | **已修复，真实 MyGo Conn 单测通过** |
| OSC-10 普通程序伪造 Agent | program_status.go 无已知 Agent 不 upsert | **已修复，目标测试通过** |
| OSC-11 Store.Display 与 hook 返回值不一致 | store.go 来源跟踪 | **已修复，目标测试通过** |
| OSC-12 空 base64 被拒 | protocol.go | **已修复，目标测试通过** |
| OSC-13 app>32 仍接受 | protocol.go 硬限制 | **已修复，目标测试通过** |

**补充发现/修复**：`records.go` 同紧急度 root 与更晚的 child 存在 nondeterministic 展示覆盖，现保证 root 同级稳定优先；`applyProjection` 对 Herdr snapshot 应用之前重叠加本地仲裁状态；`herdrhooks` spool 不再伪装成 Solco 自有 apphook 事件。

**最终验证（2026-11-04）**：`GOTOOLCHAIN=auto go test ./...` **PASS（exit 0）**；`go test ./internal/agentstatus -count=1` PASS；`go test -race ./internal/agentstatus -count=1` PASS；MyGo Terminal `TestOSC7501*` PASS（含模拟 Conn 双查询→双 reply）；`go test ./internal/nativeui -run '^TestOSC7501' -count=1` PASS；`go tool mygo vet .` PASS；`go tool mygo build` **PASS（exit 0）**，产物 `build/darwin-arm64/Solco.app` 和 0.12.2 arm64 DMG。真实 Herdr socket/PTY 与关闭窗口恢复、移动端传播尚未执行；不能将构建通过视为真机互通通过。

**不能宣称的内容**：隐藏 Pane 在没有 attach 的情况下不接收原始 OSC 字节、不能应答程序发送的 7501 feature query；Solco Hook 提供隐藏 Agent 的独立状态通道，但不是 7501 本身的全时驻留能力。尚未针对真实 Herdr CLI 程序执行隔离 HOME 的原路查询回复验收。保持当前协议支持范围描述，不伪造全面兼容声明。

### Solco Hook 特有边界补测

- `TestOSC7501NestedGenericCommandDoesNotOverrideAgentHook`：同一 Claude Pane 的 `app=cargo` blocked 进入 Terminal 记录，但不抢走 Solco Hook 的 working；之后 `app=claude-code` 的 blocked 才可成为 Agent 状态。
- `TestOSC7501HerdrForegroundExitDropsOnlyTransient`：Herdr `pane.process_info` 测得 shell idle 时，清掉 blocked 而保留 done。
- `TestOSC7501TerminalQueryReplyAndCallbacks`：真实 MyGo `Conn` 的两个查询得到两个独立回复；report → prompt → reset 回调顺序一致。
- 原有 `OSC-10` 条款不仅覆盖“无 Agent 时不合成”，现还覆盖**同 Pane 内嵌套通用程序不抢占 Agent 状态**。
