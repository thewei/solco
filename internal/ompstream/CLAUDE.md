# ompstream/
> L2 | 父级: ../CLAUDE.md

成员清单
- `ompstream.go`：OMP + Pi 共用只读 Companion Doctor（missing/current/outdated/foreign/error）；从 App 内嵌的唯一 `scripts/omp-chat-stream.ts` 生成 Pi 源码（仅唯一 `const streamAgent` 标记替换），分别原子安装到 `.omp/agent/extensions` 和 `.pi/agent/extensions`（Pi 按官方 `PI_CODING_AGENT_DIR` 绝对路径覆盖），相对路径 fail-closed，拒绝 symlink/foreign、不修改 Herdr 文件、不启动任何 Agent
- `ompstream_test.go`：temp HOME 测试 Pi/OMP 不同路径/不同 Agent 标记、独立生命周期与 Herdr 文件共存、外来目标/符号链接保护、坏/漂移嵌入源拒绝

[PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
