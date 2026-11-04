# chatstream/
> L2 | 父级: /CLAUDE.md

成员清单
- `stream.go`：OMP 有界增量事件契约与只读临时 Buffer；缺帧立即置 Desynced 并仅展示 Provider 原生 JSONL，下一条 message_start 才恢复；工具结束需 Provider 转录确认结果后才能退休临时状态
- `stream_test.go`：坏消息拒绝、跨消息/进程去重、缺帧失同步→新消息恢复、工具结果落盘与转录覆盖回归

[PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
