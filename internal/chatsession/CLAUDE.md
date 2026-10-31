# internal/chatsession/
> L2 | Parent: ../../CLAUDE.md

Shell/MyGo 无关的 Chat 投影共享层：桌面 `ChatBridge` 与将来的手机 Remote Host 读同一份（`designs/remote-host/` P2）。不 import MyGo/nativeui；只读 Provider 自己的文件；不持久化、不记录对话内容。

成员清单

- `subagents.go`：子代理投影——`Read`（一个会话的全部子代理 `View`，Busy 标记 + 摘要）、`Linked`（父工具调用 → 子代理，只用工具自己的证据：Claude tool_use id == meta.toolUseId、omp `tasks[].name`、Codex `spawn_agent.task_name`）、`Card`（`View` → 页面卡片 `SubagentCard`，状态 running/done/stopped/failed）、`Headline`/`MarkBusy`/`Summary`（按 path+mtime+size 缓存）。
- `subagents_test.go`：三家链接规则与 stopped 仅来自 Provider 自己的中断标记。

法则: 无框架依赖·只读·链不上就当普通工具行·渲染路径零 IO
[PROTOCOL]: 变更时更新此头部，然后检查 ../../CLAUDE.md
