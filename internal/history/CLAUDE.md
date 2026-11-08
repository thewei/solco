# internal/history/
> L2 | 父级: ../../CLAUDE.md

History 是 Solco 的只读 Provider 会话目录；扫描 Provider 原生文件，Catalog 是纯内存的列表行索引（标题/计数/项目/≤240 字描述行），**永不保存会话正文**：没有转录缓存、没有消息全文索引、没有数据库，阅读一律由 `HistoryService.Open` 现读原始会话文件。不会承接 Herdr 的 Agent/PTY 生命周期，也不会修改来源历史文件。调用方向为 `nativeui → HistoryService → Catalog / Scanner`，同层解析器不能反向依赖 UI。Sessions 的删除（移入回收站/放回/永久删除）由同级 `internal/sessiontrash` 搬动文件，本包仍只读：只提供 `Forget`（文件被搬走后立即摘除行与路径占用）与 `SummarizeSource`（从单个来源文件读回一行列表摘要，供回收站列表）。

成员清单

- `models.go`：Provider AgentID、会话元数据、消息及来源类型，统一跨解析器的数据语言。
- `service.go`：HistoryService 查询/扫描/阅读入口；`Forget` 把已离开来源目录的会话摘出 Catalog；Project/Provider 范围在 LIMIT 前应用（Project = 该文件夹及其所有子文件夹，按路径边界匹配：`/work/ab` 不属于 `/work/a`），暴露完整 Provider 列表及各 Provider 会话总数（Sessions 标签计数，元数据只读，不受列表上限影响；`ProviderCountsIn(项目)` 给出一个项目内的同类计数）；`Open` 每次解析原始会话文件并切出一个有界窗口（无任何落盘缓存）；`ScanWithProgress` 在 `Scan` 之上提供分批进度（每 100 个可查询来源通知一次，供 UI 渐进刷新列表）。
- `catalog.go`：纯内存 Catalog：Put/Forget/RemoveMissing/KnownFiles 写入面（Forget 按会话 key 摘行并释放文件路径占用，未知 key 忽略）、列表行描述行提取；文件路径唯一，`(mtime,size)` 为扫描变更信号。
- `catalog_read.go`：只读元数据查询、Provider 集合与各 Provider 非归档会话计数（可限定项目：`ProviderCountsIn`）、Project/Provider 范围过滤（Project = 文件夹及其子文件夹，`withinProject` 是列表/搜索/计数共用的唯一规则；`ProjectContains` 把同一规则给不经 Catalog 的回收站行复用）与 metadata 搜索（标题/项目/路径/id/Provider，大小写不敏感）；同一遍历内完成排序与 LIMIT。
- `legacy.go`：一次性删除旧版遗留的 `history.sqlite3`（含 WAL/SHM/journal）——旧版把会话正文复制进 FTS/页缓存，现已无读者。
- `scanner.go`：多 Provider 来源目录协调与增量索引（变更文件解析一次、只保留列表行元数据）；`SummarizeSource` 解析单个来源文件并给出一行列表摘要（不入 Catalog）；解析走至多 4 个 worker 的小池，行按目录列举顺序提交，结果与顺序扫描一致；首个失败取消本轮且不做清理；每提交 `scanBatchSize`（100）行在锁外通知一次进度观察者（含完成时的尾部一批）。
- `scan_utils.go`：增量扫描的文件及目录遍历辅助。
- `path_key.go`：Project 路径归一化，用于索引过滤。
- `locator.go`：来源会话 identity / 定位契约。
- `parse_utils.go`：解析器共用安全裁剪、路径与时间工具。
- `claude.go`：Claude Code 会话流解析器。
- `claude_blocks.go`：Claude 原生消息块映射。
- `codex.go`：Codex rollout 会话流解析器。
- `codex_blocks.go`：Codex 工具与内容块映射。
- `usage.go`：按需流式读取单个会话文件的 token 用量（输入/输出/缓存读写）与按模型×思考档位的路由聚合（Pi/OMP、Claude Code 按消息 id 去重、Codex 取累计 total，并带回它自己 rollout 里的 `model_context_window`→`SessionUsage.Window`）；不进目录库、只读、256 MiB/8 MiB 行上限，不支持的 Provider 返回 ErrNoUsage。
- `subagents.go`：子代理清单统一记录 `Subagent`（Claude `<session>/subagents/agent-*`、omp、Codex、pi、Antigravity；`SubagentsFor` 分派；Active=20s 内写过或末个工具调用挂起(Busy,10min)且未 Ended；Stopped=Provider 自己的中断标记（Claude 子文件末行 `[Request interrupted by user]`、Codex `turn_aborted`、omp 末条对话行为 `stopReason:"aborted"` 且已 session_exit、Antigravity `killed`）；布局缺失即空，Herdr 无父子关系）
- `subagents_codex.go` / `subagents_omp.go` / `subagents_agy.go`：Codex 子 rollout（`thread_spawn`↔`spawn_agent`）、omp 子会话（`parentSession`↔`task`）与 Antigravity 子会话（`conversation_summaries.db` 的 `parent_conversation_id` 与子 brain 目录）的只读识别与缓存
- `subagents_read.go`：`ReadSubagent`/`SubagentDetail`（抽屉与卡片摘要、handback 结果、FirstTs）
- `usage_daily.go`：Quota（原 Usage）页的 30 天活动条数据——按本地日汇总 Claude Code / Codex 会话文件里的 token（每个文件按 (mtime,size) 只解析一次，缓存只在内存；不落盘、不记录历史、日期补零到今天）。
- `pi.go`：Pi / Oh My Pi 会话解析器；会话标题优先取 agent 自己写的 `title`/`title_change` 行（文件序最后一行生效，空标题不清除旧值；omp 改名会话即此），无标题行才回退首条消息启发式；`custom_message` 里 attribution 用户的行作为用户轮入线——`skill-prompt`（`/skill:name …` 技能调用，用户原话在 `details.prompt`，回退 `args`/`content`）是那次用户输入在转录里的唯一记录，2026-11-01 前被整行丢弃致 Chat 首条消息缺失；agent 归属的通知行（launch-completion、async-result…）不入时间线；合并后的 assistant 消息带保序 `Parts`（text/thinking/tool 索引 + 逐块时间戳，使回复与工具调用不被拆成两个桶），工具结果恒设 `Output`（空输出也算已完成，nil=仍在跑）。`TranscriptMessage.Parts` 为空表示顺序即 Text→Thinking→ToolCalls（Claude/Codex）。
- `live.go`：Provider 会话的只读增量 decoder / 追尾接口（含 `ResolveLiveSource` 的 agy/Cursor 定位、`NewLiveSource` 选源、`Replaced` 接缝：源重建而非追加时解码器整体重读）。
- `agy.go`：Antigravity CLI 解析器与列表——只读 `brain/<id>/.system_generated/logs/transcript_full.jsonl`（PLANNER_RESPONSE 的 thinking/content/tool_calls 与随后的 GENERIC 结果步骤按先进先出配对，无 call id；CHECKPOINT=压缩，SYSTEM_MESSAGE=meta，ERROR=失败调用；USER_REQUEST 取出原话、`Model Selection` 提示取模型）；标题与工作区来自同目录旁只读的 `conversation_summaries.db`，有 parent 的子代理会话不列出；CLI 与 IDE 两个 brain 根。
- `cursor.go`：Cursor Agent 解析器与列表——解析 `CursorStoreSource` 合成的 JSONL 流（system 不显示、`<user_query>` 取出、`isSummary`=压缩、`highLevelToolCallResult.isError` 标错，相邻 assistant 合并）；`summarizeCursorTranscript` 是列表行的轻量读法（用户轮读全文、assistant 轮用占位、不读工具输出），`meta.json` 给标题/cwd/时间；`cursorListRefs` 的 mtime/size 含 WAL；用户消息头 `<timestamp>…(UTC+8)</timestamp>` 经 `cursorMessageTime` 成为用户行真实时间与会话起止（其余行仍无时间）。
- `cursor_store.go`：`CursorStoreSource`——把 `store.db`（meta['0'].latestRootBlobId→根 blob protobuf 字段 1 的消息哈希序→OpenAI 风格 JSON）经系统 `sqlite3` 命令只读（`mode=ro`/`immutable=1` 互为回退；`SQLiteRunner` 是将来换驱动的接缝）合成为只追加 JSONL；stat 签名不变就不起进程；哈希前缀不吻合（压缩/回滚）则重建并 `Replaced`；瞬时读失败保持原流；永不写库。
- `catalog_test.go`：内存 Catalog 的写入/排序/搜索/路径唯一/并发（-race）契约与描述行提取；Forget 同时释放行与路径。
- `legacy_test.go`：旧 SQLite 目录清理（只删 history.sqlite3 家族、幂等）。
- `claude_test.go`：Claude 会话解析行为。
- `codex_test.go`：Codex 会话解析行为。
- `agy_test.go` / `cursor_test.go`：agy 转录与 Cursor store 形状（真机 2026-11-01）回归——配对/ERROR/CHECKPOINT/合并、live=整文件、追加/压缩重建、轻量摘要=完整解析、真实 sqlite3 读取（无 sqlite3 时跳过）、列表与 WAL 戳。
- `live_parity_test.go`：live 增量解码（一次灌入 / 逐行追加）的投影必须与整文件解析的 Mainline 逐字段一致——Chat 的三条入口（新会话 / 已有 Agent 窗格 live tail / 历史记录）共用同一份转录的守卫。
- `history_test.go`：跨 Provider 解析与历史模型语义。
- `live_test.go`：增量读取/解码的边界行为。
- `pi_test.go`：Pi Provider 解析和 live 增量回归；`TestPiTitleRowWinsOverFirstMessage` 钉 agent 自生成标题优先（含空 title 行不清除、title 行不计 unknown）；`TestPiSkillPromptCustomMessageIsTheUserTurn` 钉 skill-prompt 行=用户首条消息（批量=live，agent 通知行不入线）。
- `scanner_test.go`：来源扫描、去重与索引协调；同 mtime 大小变化仍重读；每 100 个来源一批的上报契约；SummarizeSource 读回一行摘要。
- `service_test.go`：HistoryService 列表/读取/扫描契约；Open 始终反映原始文件的当前内容（追加后无需重扫）；Forget 后所有读取面一致。
- `service_filter_test.go`：Provider/Project 提前过滤、完整 Provider 集合和各 Provider 计数（排除归档）的回归；Project 范围含子文件夹且不误收同前缀兄弟目录（列表/搜索/计数同一规则）与 `ProjectContains` 表驱动。

[PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
