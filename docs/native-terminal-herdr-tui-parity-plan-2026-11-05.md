# Solco 原生 Terminal 与 Herdr TUI 能力对等：目标、技术方案、实施计划与可验证验收

- 文档状态：**Approved Next / 待执行**（基线调查已完成；**不是**实现完成报告）
- 制定日期：2026-11-05
- 适用：Solco macOS MyGo Native UI、Solco Host v3、Solco Mobile（Android 优先，iOS 按兼容能力验收）
- 调研基线：Herdr v0.9.5 / Socket Protocol 22；MyGo v0.6.4（仓库本地 third_party/mygo 补丁版）；Solco Mobile React Native + xterm.js
- 已完成的先决条件：本地 commit **15593b9** 引入 **Manager.ReadPaneRetainedText**，实机在保留 8041 行的 Pane 读取 838974 字节且没有移动视口；细节见 **docs/herdr-pane-history-api-validation-2026-11-05.md**。
- 文档执行原则：每个任务都有可复现用例、可观察服务端事实、通过阈值、失败处置和证据记录，未过门禁不得标记完成。

## 0. 一句话目标与不做的事

**目标**：用 Solco 原生 Terminal/Chat/Git/Files 等视图作为唯一日常产品界面；Herdr 作为唯一运行时权威，向桌面提供附着渲染、向手机提供独立观察画面，并通过原生 Socket API 提供历史查询、搜索、复制、选区、输入和状态。对用户可见行为达到 Herdr TUI 同级或明确记录的不支持边界。

**不做**：
1. 不重写 PTY、Ghostty VT parser、Pane/Tab/Workspace 管理器和 Agent 生命周期。
2. 不常驻第二个隐藏 attach，不因用户切换视图而结束 Herdr Pane 进程。
3. 不解析或修改 Herdr 的 session-history.json，不用自己数据库替代 Herdr scrollback；Agent 自身历史另归 Provider/History 域。
4. 不把一次 **pane.selection.read** 返回的纯文本当作 ANSI 原始输出喂给 MyGo Terminal；不发明未确认的 Herdr RPC。
5. 不因一个桌面交互缺口而强制使用 **--takeover**、重启 Herdr、修改用户正在工作的服务端配置。

## 1. 现状与真实代码边界（CURRENT）

| 能力 | 当前已有的实现/入口 | 还需审计/补齐 |
|---|---|---|
| 桌面 Pane 原生画面 | internal/nativeui/terminal.go → syncTerminals → terminal.New；internal/herdr/herdr.go → TerminalAttachCommand | 首帧、断连复原、附着数/控制权、视觉一致性 |
| 桌面原生 PTY 连接 | internal/nativeui/attach_conn.go → Herdr terminal attach；attach_timing.go | resize、退出、goroutine/PTY 子进程回收；不得无故 takeover |
| 鼠标/滚动 | internal/nativeui/attach_mouse.go、terminal_scroll.go、terminal_mouse_test.go、terminal_scroll_test.go | 主屏幕/Alt-screen/Agent TUI 的分流、事件幂等、拖选 |
| Herdr 权威布局 | internal/herdr/projection.go（session.snapshot.layouts）；nativeui/terminal.go（visiblePaneGeometry） | Split/Zoom、窗口切换、隐藏 Pane 不附着；同时更新不得错位 |
| 搜索 | internal/herdr/search_gate.go（pane.copy_search）、internal/nativeui/terminal_find.go | 搜索高亮与服务端坐标一致、滚动定位、变更重试 |
| 全部保留文本 | internal/herdr/retained_history.go；retained_history_test.go（commit 15593b9） | 显式 UI 入口、导出/复制、超大响应上限与动态输出竞争 |
| 桌面运行状态 | internal/nativeui/terminal.go OnProgramStatus / OnProgramStatusControl；internal/agentstatus、internal/apphook | 不能通过重附着丢弃有效 OSC 7501 状态；隐藏 Pane 状态依靠 Hook/Herdr |
| 手机终端 | internal/herdr/herdr.go → TerminalObserveCommand；internal/remotehost/terminal.go；solco-mobile/src/app/terminal/[terminalId].tsx | 帧序列/重连、横屏/输入法/触控、权限、与 Mac 并行 |
| 手机 VT 渲染 | solco-mobile/src/components/native-terminal.tsx → xterm.js WebView | 每次 full frame 的画面重置、帧差分不可乱序、客户端局部 scrollback |
| 语义 Agent Chat | frontend/src 单一 Chat WebView；internal/history 与 Host conversation 语义层 | **不得**用 Terminal 历史代替 Provider 对话记录 |
| Herdr TUI 退路 | Herdr 官方 CLI/TUI 可独立打开 | 作为独立调试/灾难恢复入口，不是 Solco 日常页面 |

**架构事实**：本项目已经是「原生 Terminal 呈现 + Herdr daemon」。本计划是**补齐对等性与闭环**，不是将现有 TUI 再迁移一次。历史文件 **docs/terminal-interaction-spec.md** 中含早期 Rust/Hosted-TUI 表述，应用当前真实代码和 **docs/client-product-architecture.md** 为准，不能据旧描述回退架构。

## 2. 不变量与通道选择（M0 批准后不得随意改变）

### 2.1 唯一权威

- Herdr：PTY 和进程、Term ID、Pane ID、布局、服务端 Ghostty 缓冲区/Scrollback、Agent 运行时、会话持久化。
- Solco：UI 焦点/导航意图、可见终端渲染组件、临时展示缓存、选择/查找输入框、明确授权的用户导出、状态提示。
- 客户端 MyGo Ghostty/xterm 的 VT 状态是**显示镜像/缓存**；它可以维护屏幕/局部历史以渲染，但**不能**成为第二份权威运行历史。
- 三类身份分离：Session/Instance Key → Workspace/Tab/Pane ID（结构）→ terminal_id（渲染流）。移动后 Pane ID 可能变化，重附着必须以新快照确认；切勿由旧 Pane ID 推断终端身份。

### 2.2 数据通道及行为

~~~text
                         Herdr Server / Ghostty / PTY
                                    │
                 ┌──────────────────┼──────────────────┐
                 │                  │                  │
        命令/事件/查询         Desktop controller       Mobile observer
     Herdr Socket Protocol     terminal attach         terminal session observe
       (协议 22, JSON)          (单控制权)              (独立 cols×rows)
                 │                  │                  │
         Solco Herdr Adapter   MyGo Ghostty         Remote Host WS → xterm.js
       ┌─────────┼─────────┐       │                  │
     Snapshot  History  Search   可见 Pane            手机终端屏幕
        │        │       │
    Layout/UI 复制导出 结果定位

  Desktop 低层输入：attach Conn → Herdr
  Mobile 已授权输入：Host → pane.send_input (不是 observer 连接的写权限)
  Agent 语义 prompt：Agent/Conversation API (不是模拟敲终端)
~~~

### 2.3 三条必须坚持的纪律

- **实时显示**取 attach 或 observe 的帧，不用重复轮询 pane.read；看不到历史不等于后台数据不存在。
- **浏览/复制保留文本**取 pane.selection.read / pane.copy_search；不要用纯文本快照反复「重新打印」到当前终端。
- **滚动**先判断 Herdr 当前处于主屏幕还是 Agent 的 Alternate Screen（必须结合状态/进程/屏幕证据），不能仅凭 Pane 标记为 Agent 就把滚轮发给 Agent。滚动到历史时遵从权威 viewport，返回 live 时恢复 offset=0。

## 3. Herdr v0.9.5 精确 API 合约

每次实施前先运行 **herdr --version** 与 **herdr api schema --json**，检查 request/response discriminators、字段是否可空、事件权限和版本门。官方在线文档可能随版本变化，最终以**本机 schema + 对应 tag 源码 + 隔离实测**为准。

| 目标 | 接口 / CLI | 处理方法和限制 |
|---|---|---|
| 布局/聚焦/Pane 身份 | session.snapshot | 解析 layouts、panes、terminal_id；仅投影权威事实，保持 UI 自己的导航意图 |
| 实时桌面 | herdr terminal attach terminal_id（默认不 takeover） | 只对选中 Tab 的可见 Pane attach；首次完整帧 + 后续增量让 MyGo VT 解析 |
| 多个只读观察者 | herdr terminal session observe terminal_id --cols N --rows M | 有独立网格；terminal.frame → seq、full、width、height、base64 ANSI bytes；terminal.closed 终止 |
| Pane 滚动元数据 | pane.get | 响应类型 **pane_info**；scroll.offset_from_bottom、max_offset_from_bottom、viewport_rows |
| 主屏幕滚动 | pane.scroll | 绝对 offset_from_bottom（0 为底）；**修改 Herdr 共享视口**，不是手机私有滚动 |
| 近期屏幕文本/ANSI | pane.read | **read** 结果包含 source、format、text、truncated；v0.9.5 对 lines 硬上限 **1000**；read.revision 固定 0，不能当内容锁 |
| Agent 近期文本 | agent.read | v0.9.5 与 pane.read 共用同一个有界函数；不能当完整 Agent 聊天历史 |
| 全部保留的主屏幕纯文本 | pane.copy_motion + pane.get + pane.selection.read | commit 15593b9 的 **ReadPaneRetainedText** 已实机验证；返回内容修订/行数；不需要滚动、无 ANSI 样式、可能大响应 |
| 当前范围搜索 | pane.copy_search | 使用 Herdr 返回匹配位置与 content_revision，支持搜索位置/数量、前后方向；内容变化时丢弃过期结果 |
| 选择区域复制 | pane.selection.read | 传绝对坐标 anchor/cursor，可携带 content_revision；从 Herdr 缓冲区取当前内容而非本地 VT 伪造 |
| 桌面鼠标事件 | attach 下的 Mouse SGR + pane.scroll/pane.send_text | 主屏幕滚动服务端；真正的 Alt-screen Agent 所需鼠标送其 PTY；无权/已退出 Agent 不写 |
| 手机授权输入 | pane.send_input（Host 路由） | ScopeTerminalInput 校验；观察者仍为只读，手机输入经过另一条已授权的 socket 通道 |
| 实时状态/纠正 | events.subscribe；pane.scroll_changed、layout.updated、pane.* | 先检查订阅 ACK，带 session/pane 过滤、序列/版本保护；异常时权威 snapshot 重对账 |
| 重启后恢复历史 | experimental.pane_history + session-history.json | 可选、由 Herdr 自己持久化；不承诺保存所有原始 PTY 输出；服务器重启不等于原进程存活 |

**内部服务端读取流程的源码事实（v0.9.5）**：
1. src/app/api_helpers.rs → read_terminal_snapshot：min(lines,1000)。
2. src/app/api/panes.rs → handle_pane_read / pane_selection_text：pane.read 有限；pane.selection.read 使用绝对行号及 content_revision。
3. src/selection.rs → absolute_row_for_viewport_row：TUI viewport 坐标转换为缓冲区绝对行号。
4. src/pane/terminal.rs → ghostty_extract_selection：从 Ghostty.read_text_screen 读取；单次输出为纯文本。
5. src/server/pane_input.rs → attach wheel 分流/scroll 上下调整。
6. src/app/api/agents.rs → handle_agent_read：同样受 snapshot 的行数上限。

**当前缺口：**没有经过验证的「独立、可分页、带 ANSI 样式的任意历史行范围」公开 API；也没有「全部原始 PTY 字节的永久日志」API。功能设计必须标注为 Protocol Gap。不要依赖恢复文件和私有内存结构绕过。

## 4. 技术实施细则（HOW）

### 4.1 Terminal 渲染与客户端镜像

- 使用现有 **internal/nativeui/terminal.go** 的 **terminal.New(terminal.Options{Conn: ...})** + **attach_conn.go**。MyGo Ghostty 是成熟 VT 解析器，不自行解析转义序列。
- 以 Herdr attach 画面的首次完整帧为同步起点；断线/切换/resize 后验证下一完整帧确实覆盖旧画面，避免文字残影、重复 scrollback、跳帧。
- 固定测试 CJK 全角、emoji/组合字符、TrueColor/256 色、bold/underline、OSC8 link、alternate screen、bracketed paste、cursor shape、Kitty keyboard、OSC 7501/133、同步输出及 CR 进度条。
- 不能推断 Herdr 与 MyGo 固定使用同一 libghostty ABI：记录双方版本及构建；对颜色情况和 VT 控制码做行为对照。
- **终端清屏**不应只调用客户端 ClearScreen，因为服务端下一帧会画回原历史。核查官方 server 是否有受支持的对应动作；没有则隐藏或给出真实行为说明，禁止标称已清服务端历史。
- **已核对当前 pinned 代码：** third_party/mygo/plugins/terminal/terminal.go 确实提供 **Options.OnScroll、Options.OnClear、Options.ResizeByConn**；mirror.go 有 **Terminal.NewMirror**，且 terminal_test.go / mirror_test.go 已有基础测试。应在现有 Herdr attach 管道内评估是否复用这些钩子：OnScroll 只在应用未接管滚轮时触发，不得与 routeAttachMouse 发送双份滚动；OnClear 只能交给受支持的服务端清理动作，不能本地擦掉权威画面；ResizeByConn 必须以真实服务端 resize 契约为准。保留目前 attach 工作路径，不另造镜像运行时。

### 4.2 全历史查询、搜索、复制与导出

**现有底座**：ReadPaneRetainedText(ctx, session, paneID) 已完成。按如下顺序运行：
1. pane.copy_motion 的 line_end 探针取得 **content_revision**；注意 pane.get.pane.revision 与它不同，pane.read.revision 也不能替代。
2. pane.get 取 max_offset_from_bottom + viewport_rows，转成合法 uint32 行号，注意 overflow / nil scroll。
3. pane.copy_motion 定位尾行真实末列，且强制同一个 revision。
4. pane.selection.read 从绝对行 0 到尾行，带 revision；如果发生 stale_content，**有界重试/提示用户重试**，不可无限自旋。
5. 输出文本仅供独立 History Viewer、复制、用户主动导出；严禁 Feed 回 Terminal、写进应用日志或自动同步到其他设备。

**待完善**：
- UI 增加 **Copy All Retained Text**、**Export Retained Text…**、**Find In Pane**，入口放 Terminal Pane 右键菜单或 Header 的更多菜单；保留当前 shell 焦点和输入状态。
- 对主动导出：原子写临时文件再 rename，默认文件权限 0600（macOS/Unix）、不覆盖现有文件、明确提示可能含机密；取消不产生副作用。
- 读取后显示「来自当前保留缓冲区，可能不包括已淘汰历史 / 全屏 Agent 原生会话」；不是无限历史。返回字节数/RetainedRows/Revision 以便验证，默认 UI 不打印内部元信息。
- 大输出要定义上限和渐进式交互：现阶段一次 RPC 读全量时，UI 必须异步执行、可取消、显示真实进度/忙碌并报告数据过大；禁止在每次 repaint 读取。
- 如需真正分页/ANSI 回放：先在 Herdr 上游申请或实现**受支持的 read_range(start_row,end_row,format,revision)** 能力，加入 schema 和版本门；在此之前不能假装现有 pane.selection.read 已支持格式/稳定游标。
- 如果探索旧 API 的「区间分块拼接」，必须通过端点包含/排除、软换行、Unicode 字宽、尾随空格、滚动淘汰及 revision 相等的**字节级对照**；不达标不得发布。当前先使用已验证的整段只读接口。

### 4.3 滚动与 TUI 模式分流

**真相源**：**pane.get.scroll**；客户端 **terminalSurface.scrollOffset/scrollMax** 仅为镜像状态，不能改变权属。

- 主屏幕/有 host scrollback：鼠标滚轮 → **pane.scroll** 绝对偏移；更新来自 **pane.scroll_changed** / projection reconcile。速度与 trackpad 惯性需要保留方向、限幅、去重；滚到 0 恢复 live-follow。
- 真正 Alternate Screen / 鼠标跟踪的 Agent TUI：仅在进程实际存在、已确认 Agent 状态且特定模式下转发 SGR wheel/press/drag 至 PTY（当前 routeAttachMouse 使用 pane.send_text）。不将其内部历史当成 Herdr Scrollback。
- Shell / Agent 已退出 / status=unknown/released / 真实 shell idle：不把鼠标控制序列误写入终端命令行；已有 gate 必须保留并加强测试。
- **当前易错点**：不能只凭 max_offset_from_bottom=0 判定 Alternate Screen；新 Pane 的初期和空 Scrollback 也可能是 0。要结合前台进程和客户端观测到的 VT mode，并对缺失信号采用安全降级。
- 滚动期间新的输出：在历史区域停留时保持选择位置与可读性；回到 live 后跟随输出。验收必须与实际 Herdr TUI Copy Mode 的行为对照，而不仅是数学 offset 相等。
- 多个客户端共用 pane.scroll 会互相改变服务端视口：手机/副窗口默认通过独立 observer 而不是 pane.scroll 控制其他设备；如要多窗口私有历史视口，记录为上游独立 Viewport API 能力缺口，不能暗称互不影响。

### 4.4 鼠标、键盘、IME 与快捷键

- 桌面单一输入顺序：当前 UI 焦点 → Solco 快捷键解析 → terminal attach Conn → Herdr 处理；普通 Agent prompt 通过 Agent API，不是 Terminal 原始键入。
- 使用 **attach_mouse.go** 现有 SGR 拆包和 routeAttachMouse；断帧、连续事件、拖拽/释放顺序、右键、modifier 在单测中可重放。
- **LocalDragSelect** 依 Pane 是否 host scrollback-backed 切换；选区复制从服务端权威优先，若使用 MyGo 本地选择须证明结果与 Herdr 相同，超过屏幕/重连后优先切换到 API 选择。
- IME：中文拼音、日语联想、emoji 组合输入，测试只提交最终 text 一次；Cmd+C/V、bracketed paste、Option/Alt、Shift、Ctrl+C、Esc、Home/End、功能键、光标位置、输入焦点移入/移出弹窗。
- 禁止将桌面窗口快捷键发送为 Agent Ctrl 组合键；禁止自动把剪贴板、密码、终端内容写入日志。
- OSC8/Cmd+Click 链接处理及 OSC52 剪贴板权限必须遵从 MyGo/应用安全策略，不能因为原生化放宽默认信任。

### 4.5 Pane 生命周期、Tab/Chat/Diff/Files 导航

- **session.snapshot.layouts** 决定哪些 Pane 可见；**syncTerminals** 只开这些 Pane 的 attach；切换隐藏 Tab 或转到 Chat/Diff/Files 只释放显示资源，不关闭 Herdr Pane、不清空合法 Agent 状态。
- Split/Zoom/Resize 使用 Herdr 原生 mutation 及权威回包/事件，禁止 Solco 发明第二套布局坐标权威。
- 命名、CWD、Terminal ID、焦点由 projection 校正；被关闭的 Pane 不再可写，迟到 frame/status 丢弃。
- 遵守现有 **showPaneSurface(paneID)** / **landSelectedPane()**，不得在跳回工作区时强制覆盖用户的 Chat/Terminal 选择。
- 如 controller 竞争，默认拒绝/提示，绝不静默 --takeover；释放 attach 后实测原进程还在运行、重新进入可恢复屏幕与输入。
- 维护正确的 PTY/Conn 子进程回收，关闭 Solco.app 不应杀死 Herdr Server 的工作 Pane；仅关闭属于 Solco 的附着子进程。

### 4.6 Remote Host 与手机（桌面同时在线）

- Host 保持 **terminal session observe** 独立 grid、首帧 full、后续 delta；WS 一条文本头紧跟一条二进制 ANSI 帧，接收端必须对 seq/full/尺寸做校验。
- 手机旋转、系统键盘打开/关闭只更新自己的 observer 网格：Host debounce（当前 100ms）、手机 debounce（当前 250ms），观察者重启后需要新 full frame，不能沿用旧尺寸下的 delta。
- 手机触控本地浏览历史，默认不调用共享 **pane.scroll**，不扰动 Mac 的画面。需服务器历史时明确使用已授权 API/Host 的「只读历史查询」端点（尚未接入，必须有权限/响应大小/限速门）。
- 手机输入走 **ScopeTerminalInput** 受控 **pane.send_input**，观察者本身永远不获取控制权。断链重连不得重发已经提交的命令或重复 Enter。
- 每个设备 observer 上限目前为 **2**，Host maxFrameLine 为 **8 MiB**；测试需覆盖尺寸 10–400 cols、3–200 rows 的现有合法边界与超限拒绝。
- 发生 terminal.closed / Host 4404：展示 Pane 已不存在，停止重连；网络中断才执行有上限的重连退避。任何错误必须可见，不留永远 loading。
- 三端同屏试验：Mac 本机交互 + Android 观察（并可在授权时输入）+ Herdr TUI 仅作只读观测基准；绝不因手机旋转改变 Mac 的实际 pane cols/rows。

### 4.7 Agent / Assistant 与 Alternate Screen 专项

- Shell 命令输出历史属于 Herdr 主屏幕 Scrollback。
- Pi/OMP/Claude Code 等若使用 Alternate Screen，其历史可能由 Agent 自身维护；**pane.read / pane.selection.read 并不等价于完整 Provider 对话**。
- Chat/Assistant 使用现有单一 **frontend/src** 页面以及 Agent/Host Conversation 与 Provider 会话 API；Terminal 只是一个并列的实时视图。
- 对 Agent 的工作、等待确认、完成、会话身份：保留 Solco Hook 优先、Herdr fallback、OSC 7501 rev 0.4 的既定冲突规则；不得把显示层重附着等同于 Agent 退出。
- 验证 Pi、OMP、普通 Shell、Vim/less 逐项按“主屏幕是否支持 Host scrollback、Alt-screen wheel、上翻状态、回到底部、鼠标/IME输入、退出后行为”建矩阵。
- 对未知 Agent/未确认的 PID，选择不发送会破坏 shell 的鼠标输入，并显示明确的兼容限制而不是推测“完整支持”。

### 4.8 性能、可靠性与安全

- **显式导出/搜索**不能阻塞 UI 帧；对历史长文本使用带 context 超时和失败原因的后台 RPC；当内容不断变化时有界重试、可取消、不会无限拉取。
- 限制并发全量历史读取，避免 10 MiB+ 的 Pane 乘以多 Pane/设备导致内存尖峰；按实际 RSS 记录，不强行替服务端扩容。
- 连接和渲染监测：attach 创建数/关闭数/首帧时间、active observers、队列长度、full/delta seq gap、RPC 错误类型、重连次数、画面 settle 时间；**不得**记录终端正文、shell 命令、Token、隐私路径。
- 用户授权：所有面向手机的 history/search/export 端点必须另行定义 scope/ACL；服务器每次解析 terminal_id→pane_id，不能直接接受客户端指定的裸 Pane 身份并假定属于当前实例。
- 安全模式：复制和导出默认仅由明确用户动作触发；不会后台建立搜索索引保存敏感终端正文；文件权限 0600（Unix）、可取消且可审计操作元数据。
- 障碍降级：无 Herdr / 版本过低 / Socket 失败 → 有可恢复错误，不启动另一个并行 daemon；终端已关闭 → 附着安全拆除；启动信任目录失败 → 保留精确错误，不重复启动 Agent。

## 5. 分阶段执行计划：每一阶段必须过验收门

| 阶段 | 交付目标 | Owner（建议目录） | 前置 | 完成门禁 |
|---|---|---|---|---|
| **P0 基线和隔离夹具** | Schema/能力矩阵、固定 Shell/Alt-screen/大输出 fixtures、测试机器/环境证据 | docs/、scripts/、internal/herdr | 无 | V00–V05 全通过，不能触达用户 live Herdr |
| **P1 历史 API 强化** | 对 commit 15593b9 验证大历史、边界字符、stale_content、权限；无 UI 重渲染式调用 | internal/herdr | P0 | V10–V15 |
| **P2 桌面终端画面与布局** | attach 首帧/增量/分屏/resize/隐藏 detach/无 takeover 的协议与 UI 对等 | internal/nativeui、internal/herdr | P0 | V20–V27 |
| **P3 历史/查找/复制 UX** | Copy All、Export、Find、视口/selection 语义统一，显式错误与大数据保护 | internal/nativeui、internal/herdr | P1+P2 | V30–V39 |
| **P4 输入与全屏程序** | 鼠标路由、Mac IME、快捷键、Alt-screen Agent、OSC、剪贴板 | internal/nativeui、internal/agentstatus | P2 | V40–V49 |
| **P5 手机观察与输入一致性** | Android xterm、全帧/差分、旋转/重连/授权、多端互不抢 controller | internal/remotehost + ../solco-mobile | P0+P2 | V50–V59 |
| **P6 恢复/故障与安全** | 消失的 Pane、断链/进程退出、重复连接/关闭、敏感数据保护/回滚策略 | 全相关边界 | P3+P4+P5 | V60–V69 |
| **P7 性能与交付闭环** | 指标、长时间稳定性、真实 app/ADB 验收、Release Go/No-Go | docs/debug、scripts、构建链 | P6 | V70–V79 |

**并行原则**：P1 与 P2 可在 P0 后并行；P4 可与 P3 并行；P5 可以先做协议测试但移动端真实验收依赖稳定的桌面基线。一个 PR/Commit 尽量只关闭一个阶段子目标，附证据、失败用例和实际通过命令。禁止把所有阶段合并成一次大改。

## 6. 验收用例：目标 → 测试动作 → 断言 → 证据

### 6.1 P0：基线与环境（V00–V05）

| 编号 | 测试动作 | 必须证明 |
|---|---|---|
| V00 | 记录 herdr --version、herdr api schema --json、MyGo pinned 版本、Host/Mobile 版本、设备 OS | 版本范围与全部能力均有实证，不混用官网最新版字段 |
| V01 | 准备全新临时 HOME/config/socket + scratch Git 项目，启动隔离 Herdr server 和单独 Solco.app | 不连接或重启用户 live daemon；所有资源带 run_id |
| V02 | Snapshot 新建 1 Workspace / 2 Tabs / 3 Panes，验证身份和终端 ID 唯一性 | Pane/布局/终端绑定与 Herdr snapshot 一致 |
| V03 | 运行 10,000 个**带序号**的输出行（含长行/软折行/中日韩/emoji/ANSI） | fixture 头尾序号、真实屏幕行范围、预期 SHA256 存入独立 test metadata，不写实际用户数据 |
| V04 | 运行备用屏幕程序并生成滚动、状态变更 | 主屏幕 vs Alternate Screen 的可观测行为记录清楚 |
| V05 | 记录 TUI 与 Solco 并行基线截图/尺寸/键鼠输出 | 对照测试使用同一夹具及同尺寸，不能用不同窗口大小造成误判 |

### 6.2 P1：历史 API（V10–V15）

| 编号 | 执行及断言 |
|---|---|
| V10 | 同一 Pane 请求 pane.read lines=1000 与 30000：返回上限一致，truncated=true；不会误报全部历史 |
| V11 | ReadPaneRetainedText 一次读到 V03 夹具的最早和最晚标识行；保留行 >= 10000（或按真实 scrollback 字节容量达到预期），与 Herdr TUI 复制内容逐字对照 |
| V12 | 读前/读后 pane.get.scroll.offset_from_bottom 相等；服务端进程、终端维度、focus 都不变 |
| V13 | 注入输出更新和 stale_content，历史读取返回清晰失败/有界重试，不合并两个 revision |
| V14 | Unicode/CRLF/软折行/尾行无换行/长行/尾随空格/resize 后检查复制文本的约定；不把屏幕行数误当文本换行数 |
| V15 | 大响应/超时/cancel：峰值内存、最长 RPC、UI 更新不中断；超过设计限额给出可行动错误，无二次拷贝风暴 |

### 6.3 P2：桌面原生画面/布局（V20–V27）

| 编号 | 执行及断言 |
|---|---|
| V20 | 在隔离实例启动 1 个 shell Pane，打开 Solco，拿到首帧并能 echo 输入；无 Herdr TUI UI 残留在画面 |
| V21 | Split、Zoom、Resize、移动 Pane、切换 Tab：界面每个可见 pane_id/terminal_id/rect 与 session.snapshot.layouts 一一对应 |
| V22 | 隐藏 Tab 和从 Terminal 去 Chat/Diff/Files：隐藏 Pane attach 子进程计数归零，Herdr Pane 仍存活、输出继续 |
| V23 | 回到 Terminal 首帧无残影、颜色正确、无需重新启动 PTY；不会丢掉后台输出 |
| V24 | 两个同时可见 Pane 仅两个 attach；不会因为频繁 UI repaint 创建额外 attach；自动 attach 不含 --takeover |
| V25 | 与 Herdr TUI 同一夹具比对 RGB/Unicode/emoji/光标/soft-wrap/Vim/less/CR 进度条/OSC8；记录截图及客观差异 |
| V26 | resize 过程中启动有状态的全屏程序、快速交错输出；无错乱/死锁/重绘花屏，程序尺寸与服务端权威一致 |
| V27 | attach Conn 被强制关闭/程序不可用：有可恢复错误；不会无穷 spinner，不杀 Herdr Pane；下次连接恢复 |

### 6.4 P3：Scrollback/查找/复制（V30–V39）

| 编号 | 执行及断言 |
|---|---|
| V30 | 从底部滚到序号 1，利用 Herdr scroll 指标验证顶部到达；滚回 offset=0，live-follow 恢复 |
| V31 | 历史中持续输出 1000 行，旧视口保持可读，不闪回底部；用户主动回 live 后看到最新 |
| V32 | 快速滚轮/触摸板惯性 100 次，不超 maxOffset，顺序和去重正确，CPU 无明显风暴 |
| V33 | Terminal 菜单触发 Copy All，返回与 ReadPaneRetainedText 同一 revision 的文本；不会在命令行发送 Ctrl+C |
| V34 | 导出文本文件并比对 SHA256；确认权限 0600、用户取消不写文件、旧文件不被意外覆盖 |
| V35 | Cmd+F 输入已知词，服务端 pane.copy_search total/matches 与测试 fixture 对照；搜索跳转定位可见 |
| V36 | 在输出变化后发旧 revision 的选区/搜索请求，必须得到 stale_content 处理，不显示假命中 |
| V37 | 跨视口拖选 / Shift 选择 / 选词 / 选行 / 复制 CJK、宽字符、软折行；与 Herdr TUI 复制结果比较 |
| V38 | 选区存在时 resize、切换正常屏/备用屏；遵守 Herdr 选择失效或恢复规则，无错位粘贴 |
| V39 | 最长历史 + 多 Pane：仅主动操作时发全量 RPC；搜索、滚动和输入保持可用，日志里无终端正文 |

### 6.5 P4：键鼠/Agent（V40–V49）

| 编号 | 执行及断言 |
|---|---|
| V40 | 普通 Shell 的鼠标滚轮不发送控制序列给 shell；仅滚 Herdr 主屏幕缓冲区 |
| V41 | Pi/OMP 等真实 Alt-screen：鼠标滚轮/点击/拖动是否传给应用按其真实 VT 模式决定；不能变成乱字符 |
| V42 | 瞬时 status=unknown、Agent 刚退出、真实 shell idle：禁发有风险的 Agent 鼠标输入 |
| V43 | Cmd/Ctrl/Option/Alt、功能键、Delete/Home/End、粘贴、bracketed paste 100 次，不多发/漏发 |
| V44 | 中文 Pinyin、日文 IME、组合 emoji 输入 100 次，文本仅提交一次；对输入法取消/切换焦点正确 |
| V45 | 切到查找栏/弹窗/Chat 再返回：焦点稳定、无双输入、首次键入不丢 |
| V46 | OSC 7501、OSC 133 与 Hook 竞争：状态优先级符合当前产品架构；隐藏 Pane 不凭空退出 |
| V47 | OSC8 打开链接与 OSC52 请求剪贴板：按用户策略执行，不跨越设备授权 |
| V48 | Vim/less/OMP/Pi 在 2 Pane + 1 隐藏 Pane 下运行，resize、滚动、鼠标、退出均无异常 |
| V49 | 当 API 不支持某命令时 UI 禁用/提示，绝不凭猜测发控制序列或悄悄降级为 takeover |

### 6.6 P5：Remote / Android / 并行（V50–V59）

| 编号 | 执行及断言 |
|---|---|
| V50 | 手机观察一个正在工作的 Pane：第一帧 full=true，后续 seq 连续/明确可重置；内容与 Herdr 服务端同尺寸渲染一致 |
| V51 | 故意丢失一帧、重复帧、乱序或尺寸变化：客户端不能将错误 delta 直接应用；恢复使用新的 full frame |
| V52 | 手机旋转、软键盘弹出，observer 重新启动/更新尺寸，Mac Pane cols/rows、控制权和画面不变 |
| V53 | Host ScopeTerminalInput 禁用：输入被拒且不会抵达 PTY；启用才可输入 |
| V54 | 输入一次 echo+Enter：Mac/手机都看见同一个执行结果，网络重连不重复命令 |
| V55 | Android touch 滚动只浏览手机当前画面/缓存，Mac Herdr 视口 offset 不发生无意变化 |
| V56 | 手机强制断网/恢复、Host restart：重连有上限且恢复 full frame；没有重复进程/观察者泄漏 |
| V57 | Herdr Pane 关闭：Host 4404 → 手机 gone，不再指数重连 |
| V58 | 两个 observer/一个 Desktop controller 并行：不出现 --takeover，第三个 observer 触发资源限额正确错误 |
| V59 | 手机终端长时间运行，同时后台 Assistant/Chat 通知：UI 响应正常、权限/Session/Terminal ID 无串线 |

### 6.7 P6：故障、安全、恢复（V60–V69）

| 编号 | 执行及断言 |
|---|---|
| V60 | Solco.app 被强制退出，Herdr 进程/Pane 继续；重启 Solco 正确重附着 |
| V61 | Herdr daemon 被隔离停止并重启：提示原进程丢失；可选 pane_history 仅恢复最近屏幕，不谎称进程仍在 |
| V62 | 更换 Session、移动 Pane、关闭 Tab：旧 terminal_id/Pane ID 对应的写入全部失效，迟到事件丢弃 |
| V63 | 不支持的协议版本/老服务端：拒绝不兼容能力但保留可工作的退路，不重复启动新 daemon |
| V64 | Controller 冲突：保持原持有者、不隐式 --takeover，显示接管需要手动授权或观察模式 |
| V65 | 高并发开关视图/resize/Agent 启停，资源回收完整；测试后 attach/observe 无孤儿 |
| V66 | 终端输出包含密钥样式字符串，日志/遥测/崩溃附件不包含正文；导出只由明确用户动作创建 |
| V67 | 非法 terminal_id、跨实例、手机无权限、超长输入、恶意 ANSI 和超大 frame 被正确拒绝 |
| V68 | 非稳态/连续输出时历史提取有界返回或取消；UI 可继续工作、无死锁 |
| V69 | 所有曾经发现的 terminal bug 对应单测且问题原环境复验 PASS，证据附 docs/debug |

### 6.8 P7：性能、验收与发布（V70–V79）

| 编号 | 执行及断言 |
|---|---|
| V70 | 固定夹具测 attach 首帧 p50/p95、输入→回显 p95、resize settle p95、重连首帧、UI 帧延迟、RSS |
| V71 | 对比 P0 的同机器/同固件/同字体基线；不允许引入显著回退；每项阈值在 P0 固化并记录单位、样本数 |
| V72 | 连续 60 分钟每分钟输出，主屏幕/Alt-screen/多 Pane/切页循环，内存与 FD 数无持续单调上涨 |
| V73 | 至少 100 次 show/hide、20 次 attach 断连恢复、20 次 resize、20 次 Alt-screen 进入退出，无孤儿与卡死 |
| V74 | 完成真实 macOS app 输入、渲染、快捷键、中文/日文 IME 的截图和可复现步骤 |
| V75 | 完成真实 Android ADB 启动、两端并行与重连/旋转/输入权限验收，存记录（可选 iOS） |
| V76 | 执行全仓 Go 测试、MyGo 打包、前端/移动端 typecheck+vitest，失败须明确是否本任务引入，**全仓红灯不可宣称完成** |
| V77 | 正式包启动、签名、Info.plist、必要的 libghostty 资源、安装覆盖升级和原配置恢复实测 |
| V78 | 用功能对照矩阵逐条标出 Pass/Blocked/Unsupported，附 Herdr TUI 同夹具对照和协议缺口；不以截图漂亮代替行为正确 |
| V79 | Go/No-Go 评审：所有 P0–P7 强制门禁绿灯，未解决的功能差异有产品声明和回退路径，才关闭项目 |

## 7. 可执行验证方法与测试基础设施

### 7.1 测试环境隔离（硬性要求）

1. **绝不对用户正在使用的 Herdr Session 运行 mutating/e2e 测试**。为每次测试创建独立的临时 HOME、config、socket、scratch 工作目录与进程组；参考 .agents/skills/solco-development/SKILL.md 的 Evidence loop 模板，测试结束只清理本次创建的资源。
2. 封装一个 **scripts/terminal-parity-harness**（规划中的工具，当前尚不存在）：创建隔离环境 → 启动 Herdr → 运行 fixture → 启动 Solco.app → 取服务端快照 → 驱动 GUI/ADB → 收集证据 → 关闭子进程。不能仅凭服务器 CLI 自测替代真实 UI。
3. 每条测试产生 **run_id、版本、fixture_sha256、Pane/Terminal 身份、行为断言、结果、耗时、截图路径、故障分类**；只写元数据，不采集个人会话内容。
4. 对服务端协议测试使用 fake Herdr Socket 和 **herdr api schema --json** 生成的合法响应形状；对渲染语义同时使用真实 Herdr 服务器/真实客户端。
5. 使用强制隔离的测试 Shell/Agent，而不是在任意已有用户 Pane 里发送 reset、exit、Ctrl+C 或调试命令。

### 7.2 可立即执行的静态/单测命令（CURRENT）

~~~bash
# 在 /Users/wilson/Workspaces/wh-studio/solco
herdr --version
herdr api schema --json > /tmp/solco-herdr-protocol-22.schema.json

# 已存在的完整保留文本 API 与协议回归
GOTOOLCHAIN=auto go test ./internal/herdr -run '^TestReadPaneRetainedText' -count=1

# 只有在已授权且受控的测试 Pane 上执行；正式验收改用独立沙箱
SOLCO_HERDR_TEST_PANE=<fixture-pane-id> SOLCO_HERDR_TEST_SESSION=<fixture-session> \
  GOTOOLCHAIN=auto go test ./internal/herdr -run '^TestReadPaneRetainedTextLive$' -v -count=1

# Herdr 适配器完整回归
GOTOOLCHAIN=auto go test ./internal/herdr -count=1

# 全仓质量门（须在同一 Git 状态和测试环境记录）
GOTOOLCHAIN=auto go test ./...
GOTOOLCHAIN=auto go tool mygo build
git diff --check
git diff --cached --check

# 进入 solco-mobile 仓库执行
cd ../solco-mobile
pnpm test
pnpm typecheck
pnpm lint
pnpm check:fixtures
pnpm check:chat-page
~~~

注意：上面 HERDR_TEST_SESSION 是 opt-in 现有测试的参数，其底层会根据 Session 解析 Socket。构建后产物地址由当前 MyGo 配置决定；不要在隔离测试中覆盖用户已安装的 APP。

### 7.3 真机 UI/ADB 门禁步骤（计划中的 harness 实现）

- macOS：用隔离环境启动真实 Solco.app；通过 Computer Use / 可访问性自动化执行点击、键入、滚轮、拖选、resize、Split、切 Tab、Chat/Diff/Files/Terminal 切换；截图对照，同时通过 Herdr pane.get、session.snapshot、process_info、连接计数核对服务端真值。
- Android：手机通过 USB/ADB 连接测试 Host；启动测试 APK，打开受控 Terminal；测试键盘输入、横屏旋转、软键盘收放、断网重连、连接权限；每次操作对照 Mac 服务端 Pane 尺寸和内容状态。要验证实际端到端，而不是只跑 TS 纯函数测试。
- 手机无法运行时：标记 **Blocked-device**，继续完成协议/桌面阶段，但 P5/P7 **不得**宣称已通过 Android 验收。
- 终端 TUI 对照：使用同一**固定 fixture、相同字符栅格与一致版本**，在隔离环境**先后**获取 Herdr TUI 基准和 Solco 原生画面/选区输出；绝不为了并列截图而让 TUI 和 Solco 抢占同一个 controller。以 cell/文字/属性的行为一致性为主，像素截图用来定位差异而非要求不同字体逐像素相同。
- 对每个失败保留「failing-before + 根因 + 修复 commit + passing-after」证据文件；追踪在 docs/debug/<日期>-terminal-parity-*.md，必要时将长期教训写入 .agents/skills/solco-development/LESSONS.md。

### 7.4 建议的性能 SLO（P0 先测基线再确认）

- p95 **本地键入到回显** ≤ 150ms，attach **首个有效画面** p95 ≤ 1s；端到端 Android LAN p95 输入回显 ≤ 500ms。属于候选阈值，必须在 P0 记录机器、基础版、测量协议与置信区间，不可把本表当已实测。
- 交互导致的 UI 主线程单次阻塞不超过 100ms；任意后台历史全量查询不应占用 UI 线程。
- 数据丢失/重复输入/错发到其他 Pane/隐式 takeover/用户进程被关闭为 **0 容忍**；不会无限重试或永远 loading。
- 稳定性场景 60 分钟 RSS/FD 不持续单调上涨；端到端资源曲线、GC 与 attach/observe 活跃数必须有原始统计文件。
- 基线本身无法满足时先形成可复现性能缺口，仍不得以「构建通过」充当体验验收。对 SLO 变更必须写原因及实测对照。

## 8. 数据与目标的可验证对应关系（Release Gate）

| 用户目标 | 证明所需的最少证据 | 不满足时 |
|---|---|---|
| 完全不依赖 Herdr TUI 也能运行 | 在没有启动 TUI 的隔离环境中完成 V20、V40、V50 的真实操作 | 保留可用入口，标 Not Ready |
| 等同 Herdr TUI 的**保留主屏幕**历史 | V11/V12 的完整文本/序号/视口一致 + V34 导出 hash | 标记 History Gap，不声称完整 |
| 真实彩色、全屏 TUI 画面 | V25/V26/V41 的双端截图与 VT 控制流证据 | 维持既有 attach，记录精确不兼容 |
| 桌面/手机同时观看且互不破坏 | V52/V55/V58 的 Herdr cols/rows、controller、scroll offset | 禁止把共享滚动当独立视口 |
| Agent 状态与 Chat 不丢失 | V46/V48/V60 与 Provider 会话状态对照 | 不以 Terminal 文本作会话替代 |
| 可安全交付 | V66–V69 + V70–V79 的真机与全仓绿灯 | No-Go，保留上一个稳定版本 |

每个用例记录：**Test ID / Goal / Environment / Steps / Expected server fact / Expected visible fact / Observed / Artifact paths / PASS|FAIL|BLOCKED / Fix commit**。禁止只写「看起来没问题」。通过阈值明确的功能才能关单。

## 9. 风险清单与决策门

| 风险 | 预防/恢复措施 | 阻塞阶段 |
|---|---|---|
| 客户端 Ghostty 的局部 scrollback 与 Herdr 镜像漂移 | 使用官方 attach frame；服务端 scroll 事实校正；历史内容单独 API 查询；不可从文本回放 VT | P2/P3 |
| 纯文本选择无法重建颜色和原始输出 | 对外如实标注文本；请求上游样式 range API；保留 VT 实时帧 | P3/P7 |
| 主屏幕/Alt-screen 误判造成「乱码输入」 | 组合 VT mode + 真实前台进程/状态；未知安全降级；真实 Pi/OMP 验收 | P4 |
| 强制 controller 冲突 | attach 无 --takeover；显式用户选择/只读 observer | P2/P6 |
| 暴露敏感 Scrollback 到移动端/日志 | 默认无终端全文同步；单独授权和限流；0600 导出 | P3/P5/P6 |
| 手机 observer 丢帧/乱序 | full/delta seq 检验，重连必须 full；不可无上下文应用 delta | P5 |
| 自动化测试破坏工作 Session | HOME/socket 隔离、进程组清理、明确唯一 run_id | P0 |
| 并行开发冲突/既有测试红灯 | 独立阶段、先盘点未提交变更；记录本阶段引入/基线失败，禁止覆盖其他修改 | 全阶段 |

## 10. 文档与源码地图（可直接定位）

### 10.1 Solco 仓库内：权威与实现

| 路径 | 查什么 |
|---|---|
| AGENTS.md、CLAUDE.md、.agents/skills/solco-development/SKILL.md | 实施流程、所有权边界、构建/验收门、Evidence loop |
| docs/client-product-architecture.md | 产品身份、Herdr 运行时权威、导航和可见 Pane 约束（架构真相） |
| docs/herdr-pane-history-api-validation-2026-11-05.md | 2026-11-05 的完整历史读取源码核实、实机数值、限制 |
| docs/herdr-attach-mouse-forwarding.md | Herdr attach 与鼠标真实路由的探查和已有取舍 |
| docs/remote-client-architecture.md | Host/Mobile 身份权限、WebSocket、远程通道 |
| docs/terminal-interaction-spec.md | 较早的终端交互场景清单，含历史信息；**不能直接视为当前代码架构** |
| docs/osc7501-conformance-audit-2026-11-04.md、docs/osc7501-compliance-implementation-plan.md | OSC 7501 状态与剩余端到端验证 |
| docs/ui-acceptance-testing.md、docs/macos-packaging-and-development.md | GUI 取证、安装/签名/验证实践（大小写实际路径以仓库文件为准） |
| internal/herdr/herdr.go、rpc.go、projection.go、events.go | attach/observe argv、Socket RPC、layout、订阅 |
| internal/herdr/retained_history.go、search_gate.go、find.go、actions.go | 全历史、搜索、滚动状态/控制；相关 _test.go 为契约样例 |
| internal/nativeui/terminal.go、attach_conn.go、attach_mouse.go、terminal_scroll.go、terminal_find.go | 桌面显示、PTY 桥、键鼠分流、scroll reconciliation、查找 |
| internal/nativeui/terminal_mouse_test.go、terminal_scroll_test.go、attach_mouse_test.go | 当前行为测试，增加 sibling cases 而不是新建另一套实现 |
| internal/remotehost/terminal.go、backend.go | 手机 WS + observer + scope 和 Pane 解析 |
| ../solco-mobile/src/app/terminal/[terminalId].tsx | Android/iOS 屏幕、重连、横屏/输入 |
| ../solco-mobile/src/components/native-terminal.tsx | xterm.js WebView VT、touch、IME |
| ../solco-mobile/src/lib/terminal-protocol-v3.ts、terminal-protocol-v3.test.ts | frame pairing、seq、输入编码和尺寸规则 |

### 10.2 Herdr 官方资料（在线参考；实施必须匹配 v0.9.5）

- Socket API / schema / events: https://herdr.dev/tui/docs/socket-api/
- CLI 的 Direct Terminal Attach 与 Observe: https://herdr.dev/tui/docs/cli-reference/
- TUI Copy Mode、搜索与滚轮行为对照: https://herdr.dev/tui/docs/keyboard/
- Session 恢复、pane_history 与进程是否存活: https://herdr.dev/tui/docs/session-state/
- TUI 配置与 Scrollback 限额: https://herdr.dev/tui/docs/configuration/
- 官方确定版源码（v0.9.5 tag）: https://github.com/herdrdev/herdr/tree/v0.9.5/src
- 读取上限: https://github.com/herdrdev/herdr/blob/v0.9.5/src/app/api_helpers.rs
- Pane RPC: https://github.com/herdrdev/herdr/blob/v0.9.5/src/app/api/panes.rs
- 终端缓冲区与 Selection: https://github.com/herdrdev/herdr/blob/v0.9.5/src/pane/terminal.rs
- UI → 绝对行号: https://github.com/herdrdev/herdr/blob/v0.9.5/src/selection.rs
- 实际 attach 输入及鼠标分流: https://github.com/herdrdev/herdr/blob/v0.9.5/src/server/pane_input.rs
- Agent read: https://github.com/herdrdev/herdr/blob/v0.9.5/src/app/api/agents.rs

### 10.3 MyGo / Ghostty / 移动端参考

- MyGo 官方 Terminal 插件: https://mygo.egoist.dev/docs/plugins/terminal
- MyGo Terminal 的 Conn、Mirror、ResizeByConn、OnScroll、OnClear、Search、Snapshot: 同上文档的相应小节；**当前仓库 pinned 的 third_party/mygo 才是最终可用 API**。
- 本地 pinned MyGo Terminal 的具体能力：third_party/mygo/plugins/terminal/terminal.go（Options.OnScroll/OnClear/ResizeByConn、Snapshot）、view.go（鼠标、选中、焦点）、mirror.go（NewMirror）、terminal_test.go 和 mirror_test.go（可直接复用的底层测试模式）。
- MyGo Terminal 包文档: https://pkg.go.dev/github.com/egoist/mygo/plugins/terminal
- Ghostty: https://ghostty.org/ ，VT/输入的具体规范以当前 libghostty-vt 版本核准。
- xterm.js: https://xtermjs.org/ ，具体行为以 solco-mobile 已锁定版本及测试为准。

## 11. 建议的任务分配与一次开发循环模板

按 P0 → P1/P2 → P3/P4/P5 → P6 → P7 推进，先关闭**服务端权威和显示镜像一致性**，再扩大 UX。每个小任务：

1. 选定一个编号 Vxx，抄入 docs/debug 的进度文档。
2. 在隔离环境使该 Vxx 失败并保留真实证据（如果是纯新增功能，保留缺能力的基线）。
3. 按 Herdr API → MyGo 插件 → 已验证模式 → 少量自有代码的次序设计最小修复。
4. 修改单一 Owner 的实现，并增加 Schema-shaped 单测/对应移动端测试。
5. 在同一隔离环境复验 Vxx，确认服务端事实 + UI 事实一致。
6. 跑相关包测试/全仓门禁，审查改动与关联场景，单独 commit，记录 hash、失败和 PASS。
7. 若不满足，保留阻塞与回退，不把设计稿当做功能已落地。

**最终完成定义（DoD）**：在完全不启动 Herdr TUI 的情况下，macOS 原生 Terminal + Android observer 能对所有本计划承诺的功能通过真实 UI 验收；历史保存范围、ANSI 读取与 Agent 内部历史等不支持项有明确产品声明；所有强制测试全绿、性能不回退、无误输入/控制权抢占/数据泄漏，且证据、Commit、运行版本都可追溯。
