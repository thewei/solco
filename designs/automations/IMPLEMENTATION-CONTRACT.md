# Automations 实现契约（Phase 4 接口真相源，2026-11-02 定稿）

定稿方案：**Option A 管理列表 + 详情抽屉**；权威实现 = fork 增强 `DnzzL/herdr-automations`（MIT）为 Solco 自有插件 **`com.whstudio.solco.automations`**，落在 `solco/plugins/solco-automations/`。Solco 只做呈现；门控照搬 `internal/workboard` 模式。

## 1. 插件身份

```toml
id = "com.whstudio.solco.automations"
name = "Solco Automations"
version = "0.12.0"          # verified window (solco 侧 Detect): 0.12.x
min_herdr_version = "0.9.3" # 与 solco-hooks 一致
```
保留 `[[startup]] daemon`、`[[panes]] board`、`[[actions]] run-now/install-cli`。LICENSE 保留 MIT + README 注明 fork 来源与增强清单。

## 2. 配置 schema（automations.yaml，向上游兼容 + 增量）

上游全部字段保留（name/cron|workflow/repo/workspace/workspace_id/session/agent/template/model/prompt/mcp_config/agent_args/timeout_minutes/catch_up_minutes/disabled）。新增：

| 字段 | 类型 | 默认 | 语义 |
|------|------|------|------|
| `draft` | bool | false | 草稿：daemon 不调度、`run` 拒绝；仅 list 可见 |
| `rationale` | string | "" | Agent 起草时的分析（与 prompt 一起保存，纯展示） |
| `on_overlap` | `skip\|queue` | skip | queue = 命中重叠时记一次 pending，上个 run 结束后补跑一次（不叠多次） |
| `cleanup_ttl_hours` | int | 0（关） | >0 时 daemon 每日自动清理已合并且超龄的 run worktree |

## 3. CLI（二进制名保持 `herdr-automations`）

上游命令全保留（daemon/service/add/list/run/pause/resume/history/put/rm/seen/cleanup/pane/install-cli/version）。变更与新增：

```
add            新增 --draft、--rationale "..."、--on-overlap skip|queue、--cleanup-ttl-hours N
approve <name> draft→active（非 draft 报错 refusals 原样）
list [--json]  每条增加 draft/rationale/on_overlap/cleanup_ttl_hours/next_run/last_run/history30
history [name] [--json]
put            透传新字段（drawer 的编辑保存走 put --original <name>）
cleanup        新增 --ttl <hours>（覆盖 per-automation cleanup_ttl_hours，--dry-run 保留）
```

### list --json 形状（solco 客户端按此解码；上游既有字段不动）

```json
{"result":{"paused":false,"invalid":[{"name":"x","line":12,"message":"..."}],
 "automations":[{
   "name":"nightly-ci-triage","draft":false,"disabled":false,
   "cron":"0 9 * * 1-5","schedule_text":"工作日 09:00","once_at":null,
   "repo":"~/Workspaces/wh-studio/solco","workspace_mode":"worktree",
   "agent":"claude","model":"","prompt":"...","rationale":"...",
   "on_overlap":"skip","timeout_minutes":60,"catch_up_minutes":120,
   "cleanup_ttl_hours":0,
   "next_run":"2026-11-03T09:00:00+08:00",
   "last_run":{"run_id":"r-128","status":"done","started_at":"...","duration_seconds":2280,
               "worktree":"auto/nightly-ci-1101","workspace_id":"...","pane_id":"..."},
   "history30":{"done":18,"failed":2,"skipped":1}}]}}
```
（时间一律 RFC3339 本地时区；`history30` 由 history.jsonl 近 30 天聚合。）

### history --json 形状

```json
{"result":{"runs":[{"run_id":"r-128","name":"nightly-ci-triage","trigger":"cron|catchup|manual",
  "status":"scheduled|running|done|failed|skipped|missed|invalid|cancelled|queued",
  "started_at":"...","finished_at":"...","duration_seconds":2280,
  "worktree":"auto/nightly-ci-1101","workspace_id":"w-...","pane_id":"p-...","agent":"claude"}]}}
```
新增状态 `queued`（overlap=queue 的待补跑）。**运行留存**：history.jsonl 启动时与每日轮转裁掉 30 天前记录（常量 `historyRetention=30d`，上限 5000 条记录）。

### 通知

`done` 也发 `herdr notification show`（成功=绿色语义文案「✓ <name> 完成 · <时长>」，失败照旧）；`seen` 收件箱语义不变。

## 4. Agent skill

`skills/creating-automations/SKILL.md` 更新：教 Agent 用 `add --draft --rationale`（先分析再产出结构化 prompt，rationale 写目标/输入/护栏/成本）、`approve`、`run`、`list --json`、`history --json`、`rm`、`pause/resume`；强调「创建默认走草稿，由人批准启用」。

## 5. Solco 侧（呈现层，禁止第二运行时）

| 落点 | 内容 |
|------|------|
| `internal/automations/automations.go` | `PluginID="com.whstudio.solco.automations"`、verified window 0.12.x、`Detect(listJSON) Availability{Supported,Reason}`（enabled+版本+bin 存在），镜像 `internal/workboard/workboard.go` |
| `internal/automations/client.go` | `type Exec func(ctx, args...) (string, error)`；`NewClient(pluginRoot)` 执行 `<root>/bin/herdr-automations`（无 run.sh，直接二进制；剥 agent 身份 env 同 workboard `cliExec`）；`NewClientWithExec` 测试 seam；方法：List/History/Add/Approve/Run/Pause/Resume/Remove；refusals 原样透传 |
| `internal/nativeui/page_automations.go` | `routeAutomations="/automations"`；`showViewAutomations`（toggle 回 Terminal，同 page_files）/`goAutomations`/`automationsPage`；gate 关 → 安装引导空态（`herdr plugin link <repo>/plugins/solco-automations` + `herdr plugin enable`） |
| 列表（Option A） | 行 = 状态槽（运行中=square spinner、待审=solid accent dot、失败=solid warn/bad dot、启用=hollow ring、暂停/一次性=muted）+ 名称 + 副行（schedule_text · agent · repo）+ 右侧「下次/状态」+ 尾 chip（✓ 上次 · ✗ 超时 · ✦ 草稿 · 运行中 ·m）；顶栏 = segment（全部/启用/草稿/已暂停）+ ✦让 Agent 起草 + ＋新建 |
| 抽屉 | 点行 → 右侧抽屉：标题+状态 chip、rationale 卡、prompt 卡、事实格（触发/仓库/模式/agent/策略/清理）、最近 5 次运行（每行 打开窗格[herdr focus workspace/pane]；「在 Chat 查看」仅当 internal/history 按 agent+cwd+±2min 唯一命中 session 时显示，否则不画）、底部 ▶ 立即运行/暂停|恢复/编辑（表单 → `put --original`）/删除…（确认） |
| 新建 sheet | name/cron(预设+自定义)/once/repo/agent/prompt/rationale(折叠)/高级(overlap/timeout/cleanup) → `add --draft`（默认草稿，保存后提示去抽屉批准） |
| ✦ 让 Agent 起草 | sheet 显示建议发给 Chat 的 ask 文本 + 复制按钮（v1 不直接接线 Chat） |
| 门控接线 | `nativeui` 内 `automationsState{avail,detecting…}` + `detectAutomations`（30s 焦点去抖，镜像 workboard_ui）；`automationsAvailable()` = Supported；vtabs 第 6 枚（bolt 图标，`icons.go` 新增）仅 gate 开时出现；⌘6 |
| 其余触点 | `router.go` render case+title、`session.go persistableRoute`、`app_menu.go` Go 菜单+`viewDestinations`+⌘6、`command_center_ui.go` action、`shortcuts_sheet.go` 行、`route_header.go` spec。sidebar 主体不换页（无 sidebarInnerPage 分支） |

## 6. 验证

- 插件：`cd plugins/solco-automations && go test ./...`（上游 19 个测试文件全绿 + 新逻辑新增测试）
- Solco：`GOTOOLCHAIN=auto go test ./...`、`GOTOOLCHAIN=auto go tool mygo vet .`、`go tool mygo build` 结构校验
- 集成冒烟：`herdr plugin link plugins/solco-automations && herdr plugin enable com.whstudio.solco.automations` → `herdr-automations add --draft ...` → Solco 列表/抽屉/批准全链路手测清单（写进 README handoff）

## 7. 红线（不可越）

不发明 Herdr 方法；自动 attach 永不 `--takeover`；日志不落 prompt/rationale/对话内容（只落 name/run_id/操作/错误/计数）；插件是唯一权威，Solco 无自有调度器/PTY/状态镜像；gate 关时零入口。
