# internal/chatsession/
> L2 | Parent: ../../CLAUDE.md

Shell/MyGo 无关的 Chat 投影共享层：桌面 `ChatBridge` 与将来的手机 Remote Host 读同一份（`designs/remote-host/` P2）。不 import MyGo/nativeui；只读 Provider 自己的文件；不持久化、不记录对话内容。

成员清单

- `subagents.go`：子代理投影——`Read`（一个会话的全部子代理 `View`，Busy 标记 + 摘要）、`Linked`（父工具调用 → 子代理，只用工具自己的证据：Claude tool_use id == meta.toolUseId、omp `tasks[].name`、Codex `spawn_agent.task_name`）、`Card`（`View` → 页面卡片 `SubagentCard`，状态 running/done/stopped/failed）、`Headline`/`MarkBusy`/`Summary`（按 path+mtime+size 缓存）。
- `turns.go`：`ChatTurn/ChatStep/ChatTool/ChatChange` 线格式与 `Turns`（时间线 → 页面回合；改动卡的 `+/−` 仅在传入工作树快照时出现）。
- `plan.go`：`ChatPlan/ChatPlanItem` 与 `PlanOf`（Claude TodoWrite/TaskCreate·Update、Codex update_plan、omp todo → Tasks 面板）。
- `changes.go`：`TurnChanges`（edit 类工具白名单 + 文件头，只读文件名，不解析 diff）。
- `window.go`：`Fold`（解码投影 → 一个渲染窗口：最新一页、新消息延长、超界前移）、`LastModel`/`LastContext`、`MaxAccumulated`。
- `attach.go`：`AttachSubagents`（给步骤打 SubagentIDs，并列出被链接的卡片）。
- `question.go`：`TranscriptQuestion`（omp/pi 转录里尚无结果的 `ask` 调用 → 结构化问题序列；自 nativeui/chat_ask.go 迁入）与 `Reader.Ask`。
- `screenprompt.go`：`DetectScreenPrompt`/`IsOmpReviewSheet`（Claude AskUserQuestion / 权限页 / omp Review 页的可见屏纯解析；自 nativeui/chat_hooks.go 迁入）。
- `reader.go`：`Reader`（`Open/Sync/State/Ask`：一个转录文件的增量解码器 + 与桌面相同的投影）与 `State` 只读线格式，供 Remote Host P2。
- `question_test.go`：ask 读取规则（单问/多问/多选/带 recommended 拒绝/已有结果或后续用户回合不再出卡）。
- `reader_test.go`/`plan_test.go`/`changes_test.go`：Reader 仅在文件变化时报变更、未知 Provider/空路径拒绝、窗口规则；任务清单与改动卡规则（自 nativeui 随代码迁入）。
- `subagents_test.go`：三家链接规则与 stopped 仅来自 Provider 自己的中断标记。

法则: 无框架依赖·只读·链不上就当普通工具行·渲染路径零 IO
[PROTOCOL]: 变更时更新此头部，然后检查 ../../CLAUDE.md
