# OSC 7501 rev 0.4 — 合规整改与验收规划

- 规划日期：2026-11-04
- 状态：**代码整改已实施，终端/Herdr 真机最终验收未闭环**（2026-11-04）；每项证据见审计报告 §6。
- 来源：**docs/osc7501-conformance-audit-2026-11-04.md**（OSC-01 至 OSC-13）
- 规范基线：https://www.superlogical.com/rex/docs/build/program-status （rev 0.4）
- 架构唯一权威：**docs/client-product-architecture.md**。本规划不得重新定义 Herdr / Solco 的 runtime ownership。

## Goal 与不可妥协的边界

**Goal**：Solco 在真实 Herdr Pane/PTY 上完整、稳定、安全地接收 OSC 7501 rev 0.4，遵循原协议字节/状态/记录/生命周期语义；通用终端状态以规范为准，Agent 识别继续以 Herdr runtime + 原有 Hook session identity 为准；**原生 transient > Solco 自有 Hook > Herdr Hook 兜底**，不依靠沉默超时接管。

边界：
- 不新增客户端 PTY / runtime registry / Herdr 私有协议，不绕开官方 MyGo Terminal。
- 不破坏 MyGo 现有三个其他本地 patch；仅最小修改 OSC 7501 相关第四个 patch。
- 不将 cargo/brew 等普通程序变成假 Agent。若没有 Agent 实体，状态应属于 Pane/Terminal 级展示模型。
- 不把隐藏视图当成进程退出，不以静默时间替代官方“无需心跳”生命周期规则。
- Hook 仍负责 OSC 本身没有的 session identity；无协议支持的 CLI 维持原回退行为。
- 保留全部既有 staged/unstaged/untracked 修改；未经请求不 stage、commit、push、reset、clean。
- 所有修改遵循仓库 L3→L2→L1 文档回环、复用 Go/Herdr/MyGo 已有 seam。

## Phase A — 先建立不可作弊的合规测试矩阵（阻塞后续阶段）

### OSC-T01 / Byte-level golden & negative corpus
- Owner：**third_party/mygo/plugins/terminal/osc7501_test.go**。
- 将审计中的临时 4099-byte 红灯测试正式落地；补 4096/4097 字节 BEL 与 ESC反斜线边界、逐字节切块、取消/恢复、长未知 key、重复查询及一个 chunk 多个查询。
- 验证输入经过 scanner 后终端输出正常；查询回复不得泄露 report 的 id/msg/title；回应个数必须匹配合法查询数。
- **完成标准**：修复前明确红灯，修复后相关 corpus 全绿；没有仅因截断而“修好”的假阳性。

### OSC-T02 / Protocol parser conformance corpus
- Owner：**internal/agentstatus/protocol_test.go**。
- 补显式 id=、state=clear:id=（禁止 root 清空）、未知 17 字节 key、33 字节 app 超限、重复字段中较早字段超限、超长已知字段、空文本 base64、各控制字符、坏 UTF-8、最小/最大 id、padding、周边空白。
- 明确区分“畸形 pair 跳过”和“违反硬上限整报文拒绝”；保持原子性。
- **完成标准**：每一项对应 spec 原句、测试输入、预期接受/拒绝和存储不变断言。

### OSC-T03 / Arbitration & lifecycle corpus
- Owner：**internal/agentstatus/{records,store}_test.go** 和 nativeui 集成测试。
- root working + child blocked + root idle，必须显示 blocked；Hook 接管后 Store.Display 与 UI 有同一 effective source。
- 验证 3 分钟以上合法 working 不因静默丢失，真实 process exit/OSC 133 A 才丢瞬态；done/error 跨 prompt、隐藏、可见恢复仍存在。
- 逐个模拟多 Pane、连续重复报告、clear 子树、LRU 被逐、session identity 变化及 Hook-only 旧 Provider。
- **完成标准**：每个来源竞争场景都断言“当下唯一生效状态”和来源，不能只断言返回 bool。

## Phase B — 严格修复扫描器及报文解析

### OSC-F01 / Scanner full-sequence cap（P1）
- Owner：**third_party/mygo/plugins/terminal/osc7501.go**。
- 以整个 OSC 序列计数，**OSC 前缀 + 消息体 + BEL/ESC反斜线** 都占预算；超 4096 整报文丢弃，不得截断后接受。
- 在非法序列取消或超限后，扫描器仍能同步到下一条合法 OSC。
- 正确计数同一次读块中的每个 feature query，不用单个 bool 丢数量。
- **验证**：OSC-T01；原有 vendored Terminal 单测；所有其他 MyGo delta 不回归。

### OSC-F02 / Parser strict validation（P0/P1/P2）
- Owner：**internal/agentstatus/protocol.go**。
- 保留 id 是否显式出现的信息；显式 id= 必须作为不合法 id 拒绝整报文，绝不可回退根记录。
- 对每个已识别或未知 pair 先检查 key 长度等硬限制；app 长度>32 必须整报文拒绝（而非安静忽略）；对所有重复字段检查各自适用的硬限制，不能因为后面的合法覆盖就忽略前面的违反上限。
- 继续允许纯语法错误 pair 跳过；unknown key 不产生副作用；最后的有效同名字段覆盖之前值。
- 依据规范测试空值/空 base64 与首尾 whitespace，UTF-8/C0/DEL/C1 严格拒绝。
- 在解析完全成功后才一次性修改 Set，保证失败报文无部分影响。
- **验证**：OSC-T02；OSC-01、03、04、12、13 全关闭。

### OSC-F03 / Native UI callback thread boundary（P1）
- Owner：**internal/nativeui/terminal.go**, **program_status.go**。
- OnProgramStatus 在 terminal goroutine 发出，按现成 **Shell.applyOnWindow** 将回调转到窗口更新线程；记录源可先锁保护，但 Shell 投影与交互 broker 只能在 UI lane 更新。
- 防止 window/pane 已退出或重 attach 后的迟到事件写进新 Pane：加入必要的 attachment/generation 验证，避免仅用 paneID 判同一个监听源。
- **验证**：nativeui 目标测试 + race，实机快速切换/关闭/重连 100 次无 stale status、无 panic。

## Phase C — 让记录生命周期与 Herdr 的真实运行事实对齐

### OSC-F04 / Official lifecycle events（P1）
- Owner：MyGo terminal boundary + **internal/agentstatus**；Herdr API 只用于确认 runtime 事实。
- 按官方终端解析事件处理 **OSC 133 A**（清 working/blocked）、**RIS**（清所有）、**DECSTR**（不清）及实际被报告程序结束。
- 先核查 libghostty-vt / MyGo 是否已有正确事件，优先复用；不得通过正则扫描终端可见字符串猜事件。
- Herdr terminal attach helper 退出不一定等于 Pane 中的前台程序退出。只有 Herdr 提供权威 process/session lifecycle 时才能作为 process exit；缺少事件则记录协议 gap，避免伪造。
- **验证**：OSC-T03；脚本实际发出 OSC 133 A、RIS、DECSTR 并观察每个 id 的留存；确认 done/error 一直保留至 replaced/clear/full reset。

### OSC-F05 / Visibility must not destroy records（P1）
- Owner：**internal/nativeui/terminal.go**、新旧 Pane/Terminal identity 生命周期协调层。
- 将“可见 Terminal view 分离”与“Herdr Pane 被删除/Terminal reset”分开。只关闭 widget/attach，不调用彻底 PaneClosed；仅权威 Pane 删除或确切的 terminal replacement 才释放对应 Set。
- 可见 UI 本身不是持续捕获协议的 runtime；必须研究在不可见 Pane 情况下是否能通过 Herdr 已有协议或有限的可观察通道获得报告/回答探测。**不能为这个目标创建第二个常驻 PTY 或每个隐藏 Tab 的 attach fleet**。
- 若 Herdr 缺乏规范事件传递能力，记录为后端协议 gap，并在产品文案中限定支持范围；禁止声称隐藏 Pane 的实时 OSC 状态完全兼容。
- **验证**：同 Pane 先 done，切到其他 Tab 再返回，done 仍在；切换备用屏幕不清记录；真正 Pane 删除后才释放内存。

### OSC-F06 / Feature query attach round trip（P1 / OPEN）
- Owner：MyGo Terminal 和已支持的 Herdr attach。
- 在隔离 HOME + 独立 Herdr socket + 非用户生产 Pane 中，由程序发送 OSC 7501;?（BEL 和 ESC反斜线），验证**程序标准输入真实读到**符合协议的 reply（不是仅扫描器单测）。
- 对多查询、并行多个 Pane、不支持时静默及失联重连分别测试。
- 若 Herdr attach 不能回写到真正程序，先核对 Herdr 正式 API / 当前实现；缺口交由 Herdr，不在 Solco 自造 terminal IO 通道。

## Phase D — 协议优先、Hook 回退与通用程序边界

### OSC-F07 / Single effective status source（P0）
- Owner：**internal/agentstatus/store.go**。
- 每个报告完整 Apply 后以 **Set.Display** 决定当前有效状态；不能只用刚更新 id 的 state 覆盖所有记录。
- 明确记录当前主来源（OSC 或 Hook）、来源切换依据与当前 terminal generation；**Store.Display、返回给 UI 的 status、blocked detection** 一致。
- 只根据真实的 process exit、session identity 交接、符合规范的 clear/状态变更、明确用户动作切换来源；避免时间阈值覆盖长时间运行的合法 working。
- Hook-only Provider 不受影响；Hook session identity 无论状态主来源均必须继续更新。
- **验证**：OSC-T03，修复 OSC-02、08、11。

### OSC-F08 / Generic Program Status and Agent semantics separation（P1）
- Owner：**internal/nativeui/program_status.go** 与现有 Pane/Agent 视图投影；如需 DTO 优先复用已有字段。
- OSC 7501 本身不含 Agent session ID。接收到 brew/cargo/deploy 状态只能给当前 terminal/pane 的状态模型；不可由该状态创建 Herdr Agent 对象。
- 存在 Herdr/Hook 确认的 Agent 时，可向其活动状态投影有效的 Program Status；不要用 app 字段猜测或创建虚假 Agent。
- **验证**：普通 shell 发 state=working 后 Agent 列表不增加 Agent；已有 Claude Code Agent 收到 native blocked 后显示 blocked，session identity 保持一致。

### OSC-F09 / UI safety & notification behavior（P2）
- Owner：**internal/nativeui**。
- 仅在需要展示时使用 title/msg，始终按普通文本渲染；额外剔除方向覆盖/不可见格式字符；保留来源 Pane 标识。
- 若新增通知、声音等终端外副作用，必须按 Pane 限流与去重，不能每帧报告都刷通知。
- 当多个 child 同时 blocked 时，定义符合 Solco UX 的最紧急记录选择（该选择属于产品层，不是规范 MUST），切换状态不得吞掉待用户行动的记录。
- **验证**：恶意文字、不透明方向字符、频繁重复状态、分 Pane 来源归属。

## Phase E — 合规验收与文档闭环

### OSC-G01 / 目标测试（每阶段）
~~~sh
GOTOOLCHAIN=auto go test ./internal/agentstatus -count=1
GOTOOLCHAIN=auto go test -race ./internal/agentstatus -count=1
(cd third_party/mygo && GOTOOLCHAIN=auto go test ./plugins/terminal -run '^TestOSC7501' -count=1)
GOTOOLCHAIN=auto go test ./internal/nativeui -run 'Test.*(ProgramStatus|Hook.*Status|Pane.*Status)' -count=1
git diff --check
git diff --cached --check
~~~

### OSC-G02 / 完整门禁（合入前）
~~~sh
GOTOOLCHAIN=auto go test ./...
GOTOOLCHAIN=auto go tool mygo vet .
GOTOOLCHAIN=auto go tool mygo build
git diff --check
git diff --cached --check
~~~

### OSC-G03 / 真机互通验证
使用隔离 HOME 与独立 Herdr socket；不能操作用户日常正在运行的 Pane。至少覆盖：
- 泛用 shell 程序（无 Agent）发 idle/working/blocked/done/error/clear + 多 id。
- Claude Code（支持 OSC 7501 的版本）与 Hook 同时发状态，确认原生状态优先、session identity 不丢。
- Hook-only Agent 不支持协议时的完整回退。
- 切换 Tab、Pane zoom、Terminal / Chat 切换、窗口隐藏恢复；观察普通程序状态、Agent 状态、历史持久记录。
- 同时发送多个查询，程序端真正收到每次协议回复；ESC反斜线/BEL 与跨读块。
- OSC 133 A、RIS、DECSTR、真正 process exit；验证 working/blocked 与 done/error 清理差异。
- 长时间 working 超过现行 2 分钟，且 Hook 噪声持续；不能错误覆盖 native。
- 真实软件进程/窗口/UI 更新无跨线程状态污染；只允许 Herdr 拥有 runtime。

### OSC-G04 / 文档与判定

1. 同步 **third_party/mygo/LOCAL-PATCH.md**、**internal/agentstatus/CLAUDE.md**、**internal/nativeui/CLAUDE.md**，L3 文件头契约与各修改保持一致。
2. **docs/client-product-architecture.md** 更新为最终真实链路；审计文档每条缺陷写关闭证据（测试名/CLI/截屏/Herdr 输出），而非仅标记“已做”。
3. 达标条件：所有 rev 0.4 **MUST/MUST NOT** 条款合规，全部 OSC-01…13 按确认结果关闭，P0/P1 = 0，目标单测、race、全仓测试、build 全绿，真实隔离 Herdr/PTY/可见性行为验证通过。
4. 若隐藏 Pane capture/query 受 Herdr 正式能力限制，必须将其记为公开的兼容性限制；不能宣称完全兼容。可分阶段发布“可见 Terminal 上部分支持”。
5. 禁止因为第三方 Agent 未启用 7501 就判失败；能力检测与降级属于协议设计。反之，已经声明支持 7501 的终端必须履行查询应答与记录规则。

## 完成顺序与依赖

~~~text
OSC-T01/T02/T03（红灯验收用例）
           ↓
OSC-F01/F02（扫描/解析的原子与上限修复）
           ↓
OSC-F03（UI lane） + OSC-F07（唯一有效状态）
           ↓
OSC-F04/F05/F06（生命周期/可见性/真实查询）
           ↓
OSC-F08/F09（通用程序/Agent 边界与外部呈现）
           ↓
OSC-G01/G02/G03（全量门禁 + 实机）
           ↓
OSC-G04（文档闭环 / 完整兼容声明或显式限制）
~~~

风险最高的跨域工作是 **OSC-F04/F05/F06**：Herdr runtime 生命周期与隐藏 Pane 的观测不能通过客户端猜测实现。必须先验证 Herdr 真实能力，再决定最小修复与正确兼容性声明。

[PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md

## 2026-11-04 实施进度

- **OSC-T01/T02/T03**：回归测试已落盘并通过（包括真实 MyGo Conn 双查询及 Source Priority）。
- **OSC-F01/F02/F03/F07/F08**：已实现；对应测试通过。嵌套通用程序以 `app` 匹配现有 Agent Provider，其他报告为被动 Terminal 记录，不抢占 Solco 自有 Hook。
- **OSC-F04**：OSC 133 A/RIS + Herdr `pane.process_info` 返回 shell-idle 事实已接线，真实 CLI 程序生命周期验收仍待进行。
- **OSC-F05**：不再因 view detach 清理终端记录；隐藏 Pane 的 OSC 原始传输/Feature Query 没有官方后台消费通道，本轮不新增 attach fleet，**此项为明确的部分兼容边界**。
- **OSC-F06**：MyGo 的真实 Conn 模拟往返已通过；隔离的 `herdr terminal attach` 端到端仍待进行。
- **OSC-F09**：当前未从 title/msg 创建通知，安全地保持无外部效果；若将来展示内容需再验方向控制字符。
- **OSC-G01/G02**：全仓 `go test ./...`、`mygo vet .`、`mygo build`、agentstatus race 和目标测试均通过（2026-11-04）；**OSC-G03** 隔离 Herdr/PTY 真机端到端尚未执行，隐藏 Pane 的完整 OSC 7501 能力仍有限制；**OSC-G04** 文档及架构边界已同步，不宣称全时端到端兼容。

[PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
