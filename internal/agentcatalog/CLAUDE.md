# internal/agentcatalog/
> L2 | 父级: ../../CLAUDE.md

Chat 输入框的只读目录（2026-10-31 产品决策）：Claude Code / Codex / pi / omp 各自的 Skills、自定义 prompt 命令、各 Agent 完整内置命令表（`/` 弹窗里的命令；文本类原样发送、选择器类出卡片、其余发送后转 Terminal）与可选模型。不写、不执行任何 Provider 命令；无框架代码。

成员清单

- `catalog.go`：`Kind`（text/picker/terminal/skill/prompt）、`Entry`、`Model`、`Catalog`；`builtins` 真机核实表；`Classify` 判定一条已发送文本的类别；`ModelVerb`（pi `/model `、omp `/switch `）；`Load` 扫描 skills（首个目录同名优先、上限 500）与 prompts；`piModels`/`ompModels` 十分钟缓存（omp 经登录 shell，stderr 兜底），`Model.Window` 来自列表的 context 列，`ModelWindow` 按转录里的模型 id 查窗口（歧义不猜）；`IsCompact` 判定 `/compact`；`DefaultModel` 读 agent 自己配置里的默认模型。
- `catalog_test.go`：skills 去重与描述折叠、嵌套 prompts、pi/omp 列表解析回归。

法则: 目录只读·内置命令表以各 Agent 自己的 `/` 弹窗为准·模型列表来自 Agent 自己
[PROTOCOL]: 变更时更新此头部，然后检查 ../../CLAUDE.md
