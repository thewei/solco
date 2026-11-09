# feedback/
> L2 | 父级: /CLAUDE.md

成员清单
model.go: Ask/Thread/Question/Answer/Attachment/ThreadSummary/Event 与状态常量——同时是 broker HTTP、桌面 WebView 桥与手机 Host v3 的线格式（camelCase，三处一起改）
threadkey.go: 线程归属的纯函数——ResolveProject（读 .git 目录/gitdir 指针/commondir，把 worktree 折回主仓库，不起 git 进程）、ClientName（MCP 握手名或 caller_info 的客户端家族，认不出 = "Agent"）、TitleFor、GapWindow（60 min）
store.go: Store——线程归属（显式 thread_id > task_id > 同项目+同客户端+60 min 间隔）、Ask 状态机（pending → answered / expired / ended；expired 之后仍可迟到回答，下一个同线程 Ask 的结果带 lateAnswers 且只交付一次）、Abandon（Agent 客户端取消调用→立即 expired 但仍可迟到回答，不是 ended）、Tick（默认超时 4 h、上限 24 h；超时 / Agent 失联 5 min 即 expired）、有界落盘 feedback.json（500 线程 × 200 条、0600、损坏文件改名保留后空启动）、Recall、SSE 订阅；线程带 `task`（Agent 随首个带它的 Ask 给出的原始任务，只写一次）；管理：DeleteThreads/Purge（只删已结束线程，等待中的永不删，dryRun 先数后删）、保留期 RetentionDays（0 = 永久，Tick 里清理）、Search（标题/问题/回答子串）；图片只在内存里等 Agent 取走，历史只留数量
import.go: ImportDecisions——一次性回放旧 mcp-feedback 的 decisions.jsonl（imported.json 作标记；旧记录没有客户端名，按 caller_info 家族归线；未回答的记为 ended，不可再答）
broker.go: Broker + NewHandler——loopback HTTP（/v1/asks* Agent 侧、/v1/inbox/* 收件箱与 SSE、/v1/recall、/v1/health）；每个请求要 Bearer 令牌、Host 必须是 loopback、带 Origin 头一律 403、从不发 CORS 头；读 /v1/asks/{id} 同时算 Agent 心跳；SSE 订阅者数 = UIAttached
owner.go: Run（flock 抢到就服务、否则 3 s 待命重试）、EnsureToken、Dir——hostd、Solco.app 与 Agent 侧 shim 都跑同一个 Run，换主不丢 Ask（状态在 feedback.json）；端口是 loopback 临时端口，写 broker.json 供发现
lock_unix.go / lock_windows.go: 平台文件锁（flock / LockFileEx）
client.go: Client——发现端点、连不上重读一次 broker.json（换主后端口会变）、仍连不上 ErrOffline（调用方把离线当一等状态）；Create/Result/Threads/Thread/Answer/End/Abandon/Recall/Stream、管理类 DeleteThreads/Purge/Search/Retention/SetRetention；broker 路由 POST /v1/inbox/threads/delete|purge、GET /v1/inbox/search、GET|PUT /v1/inbox/retention
pick.go: 上下文引用——ContextRef{kind file|folder|skill, name, path}、Pick（回答框的 File/Skill 快速选择：文件走 gitkit filesview 索引（15 s 缓存），Skill 走 agentcatalog.ListSkills，上限 40 条）、ResolveContext（按线程项目根校验：≤20 项、拒绝空/绝对/`..`/不存在/未知 Skill，kind 以磁盘为准，去重）；只传路径、不传内容；Host 发给手机前去掉绝对 Path；broker 路由 GET /v1/inbox/pick
snapshot.go: Source（Threads/Thread 两个读口，Client 满足）、可选 Manager（删除/清理/搜索/保留期/Pick，Client 满足；Host 据此宣告 `feedbackManage` 与 `feedbackContext`）、Snapshot（online + 线程列表 + 等待数 + 选中线程时间线）、ReadSnapshot/ReadSnapshotQuery（q 过滤列表，等待数不受影响）——桌面 WebView 桥与手机 Host v3 `GET /feedback` 共用的页面读法；broker 离线 = Online=false，线程不存在 = 无 Detail
notify.go: Notifier 接口、WorkerNotifier（飞书 Worker 卡片：项目 · Agent · 问题首行 + 只含 id 的落地页链接，失败只记日志）、LocalNotifier（没有 Solco 窗口订阅时的 macOS 通知）、DefaultNotifier（notify.json 每次读取，配置不用重启；Settings 里一旦有启用的 webhook 渠道（channelsConfigured），旧 Worker 文件不再调用，由 Host 在手机告知不到时调用渠道，避免同一 Ask 发两条飞书）
notify_channels_test.go: 启用的渠道取代旧 Worker 文件、关闭的渠道不算
mcpshim/: 外部 Agent 侧的 stdio MCP 服务器（官方 Go SDK）——interactive_feedback / _start / _wait / recall_decisions，工具名与入参兼容旧 Bun 服务（新增可选 thread_id）；`solco-hostd feedback-mcp` 启动它，它自己也参与 broker 所有权，所以没有 Solco 时 Agent 也能提问；工具描述是每个接入 Agent 都会读到的话，明确要求堵塞式长连接（一次调用保持打开、不轮询、不缩短 timeout_seconds、不拿假设当答案；客户端截断时退回 start + wait，`shim_test.go` 钉措辞），进度保活每 10 s 一次（需客户端带 progressToken）；initialize 下发 `instructions`（堵塞式规则进系统提示）、工具带 annotations（recall 只读、全部 openWorld=false）；结果带 `context`（用户挂的文件/文件夹/Skill 绝对路径，Skill 为 SKILL.md）与 `context_note`，描述与 `instructions` 都要求 Agent 去读这些路径；工具描述与 `instructions` 要求首个 Ask 带可选 `task`（用户原话）、description 写清已完成情况；broker 中途失联的错误带 session_id，让 Agent 用 _wait 接着等而不是重问

法则: 状态只有一个所有者（拿到 broker.lock 的进程）·令牌 + loopback + 无 Origin，三者缺一拒绝·日志只记操作名/id/计数，绝不记问题、答案、描述或图片·线程归属是只读展示规则，不据此做任何运行时决定·离线是一等状态
[PROTOCOL]: 变更时更新此头部，然后检查 ../../CLAUDE.md
