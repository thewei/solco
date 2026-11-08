# internal/agentcatalog/
> L2 | 父级: ../../CLAUDE.md

Chat 输入框的只读目录（2026-10-31 产品决策；2026-11-01 增 Cursor / agy）：Claude Code / Codex / pi / omp / Cursor / agy 各自的 Skills、自定义 prompt 命令、各 Agent 完整内置命令表（`/` 弹窗里的命令；文本类原样发送、选择器类出卡片、其余发送后转 Terminal）与可选模型。不写、不执行任何 Provider 命令；无框架代码。

成员清单

- `catalog.go`：`Kind`（text/picker/terminal/skill/prompt）、`Entry`、`Model`、`Catalog`；`builtins` 真机核实表；`Classify` 判定一条已发送文本的类别；`ModelVerb`（pi `/model `、omp `/switch `）；`Load` 扫描 skills（首个目录同名优先、上限 500）与 prompts；`piModels`/`ompModels` 十分钟缓存（omp 经登录 shell，stderr 兜底），`Model.Window` 来自列表的 context 列，`ModelWindow` 按转录里的模型 id 查窗口（歧义不猜）；`IsCompact` 判定 `/compact`；`DefaultModel` 读 agent 自己配置里的默认模型（agy 支持 `~/.gemini/antigravity-cli/settings.json` 回退 `~/.gemini/settings.json`，无配置时回退可用首项）。Cursor：只有 skills（`/name`，`~/.cursor`/`~/.claude`/`~/.agents` + 项目），**无模型列表与切换**（`/model <filter>` 取首个匹配且改全局默认，用户只允许 Gemini 3.8 Flash，其余会让账单暴涨）；agy：`agy models`（`Model{ID: 展示名, Detail: id}`）+ `/model ` 输入式切换，`Catalog.ModelNote` 提示会改默认模型（手机端 2026-11-01 起随 `modelNote` 一并获得切换）；`IsMaxMode` 把含 `max` 令牌的模型从所有 UI 列表剔除。思考强度（2026-11-01）：`Effort{Label,Send}`（Send=agent 自己的输入式命令全文，页面/手机走普通发送）；`EffortVerb`（pi `/thinking `、omp/Claude `/effort `）；pi 按 `~/.pi/agent/models.json` 的 `thinkingLevelMap`（无 map 的推理模型 off…high，`withPiEfforts` 不改缓存）；omp 取 `omp models` 的 thinking 列（+off/auto）；Claude 固定六档 + `EffortNote`（Claude 按模型落盘）；agy 把 `(High|Medium|Low|Minimal)` 变体折成一个 family 行（`Pick` 选首个变体，`groupAgyEfforts`）；`Classify` 对 `<verb><level>` 放行为文本，裸 `/effort` 仍转 Terminal。
- `catalog_test.go`：skills 去重与描述折叠、嵌套 prompts、pi/omp 列表解析回归；agy 模型解析丢 max、`IsMaxMode` 令牌规则、Cursor/agy 目录；pi/omp/agy/Claude 的强度解析与 Classify 放行（真机抓取的行）。

法则: 目录只读·内置命令表以各 Agent 自己的 `/` 弹窗为准·模型列表来自 Agent 自己
[PROTOCOL]: 变更时更新此头部，然后检查 ../../CLAUDE.md
