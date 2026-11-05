# chatstream/
> L2 | 父级: /CLAUDE.md

成员清单
- `stream.go`：OMP / Pi 共用有界增量事件契约与只读临时 Buffer；丢帧或 32KiB 文本/思考上限、64 工具上限一旦触发即撤销临时投影并置 Desynced，仅展示 Provider JSONL；下一条 message_start 恢复；工具结束需 Provider 转录确认
- `stream_test.go`：Pi + OMP 合法 Agent 标记与其他 Agent 坏消息拒绝、跨消息/进程去重、缺帧失同步→新消息恢复、文本/思考/工具容量超限撤销 UI 投影、工具结果落盘与转录覆盖回归

[PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
