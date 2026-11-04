# ompstream/
> L2 | 父级: ../CLAUDE.md

成员清单
- `ompstream.go`：OMP Chat Companion Extension 的安全本地 Doctor（missing/current/outdated/foreign/error）与显式安装/卸载；安装内容来自 App 内嵌的唯一 `scripts/omp-chat-stream.ts`，不读取仓库路径、不修改 Herdr 扩展或会话，拒绝 symlink/foreign，原子替换
- `ompstream_test.go`：temp HOME 测试完整生命周期、与 Herdr 文件共存、外来目标与符号链接保护、坏嵌入源拒绝

[PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
