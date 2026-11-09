# agenthooks/
> L2 | 父级: /CLAUDE.md

成员清单
script.go: 内嵌通用 Agent hook 脚本模板（v10，含 Herdr pane.report_agent_session 会话中继与 source: "herdr:<agent>" 权威源校验；支持基于 PTY/TTY 交互进程拓扑与 Herdr 权威源双层解析 Codex 跨 pane daemon；Cursor 内的 Claude 副本与继承父线程的 Codex 事件静默丢弃）与所有权/版本标记
agenthooks.go: Hook Doctor 领域包（Claude Code / Antigravity / Cursor / Codex 四个 provider）：Provider 诊断（脚本版本+接线完整性）、UpgradeScripts（启动时只静默替换 Solco 自己装过的旧版脚本文件，不碰接线/信任/外来文件）、安全幂等安装（原子写入、拒绝覆盖外来脚本、备份、最小化改写 ~/.claude/settings.json、~/.gemini/config/hooks.json 与 ~/.cursor/hooks.json 的 hooks 段；Cursor 事件为 camelCase：sessionStart/beforeSubmitPrompt/preToolUse/postToolUse/stop）
trust.go: Manager.TrustWorktrees——用户在设置里开启 Claude Code 时向 Claude 自己的 ~/.claude.json 的 projects 头部拼入唯一一条 ~/.herdr/worktrees 信任记录（其余字节原样、保持文件权限、原子写、已有记录——含显式 no——绝不改、文件不存在则不写）；Codex 信任按仓库根且不继承，由 solco-automations 插件运行时应答
agenthooks_test.go: 诊断/安装/幂等/外来文件保护/版本升级回归（临时 HOME，全路径注入）

边界
- 只写两类文件：自有脚本（~/.solco/hooks/）与 provider 配置的 hooks 段；其余内容逐值保留
- Codex 已接线（~/.codex/hooks.json，事件 SessionStart/PreToolUse/PostToolUse/PermissionRequest/Stop）但其 hook 需用户在 Codex 内逐条信任（config.toml `hooks.state."<hooks.json>:<snake_event>:<组>:0"` 的 trusted_hash，未信任的 hook 被静默跳过，2026-11-02 实测）：诊断只读该表是否有记录，缺记录即 StateNeedsApproval（无 Solco 动作）；Solco 永不写 config.toml/trusted_hash、不用 bypass 参数；脚本对继承父线程 CODEX_THREAD_ID 的子 codex 事件静默（与 herdr 官方脚本同闸）
- Aider 无 hook 机制，诊断为 unsupported（状态由 Herdr 屏幕检测兜底），永不安装
- 脚本本身不记日志、失败静默（fail-open）；生命周期权威仍在 Herdr

[PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
