# settings/
> L2 | 父级: /CLAUDE.md

成员清单
file_store.go: 磁盘设置持久化（原子写入、读取与默认合并）
settings.go: Settings 核心数据结构（通用设置、终端设置、会话状态 SessionSettings、外观透明度、代理与配额开关；ChatOutline 对话大纲/TOC 默认关闭；AgentsSettings 每 Agent 启动开关——Enabled 只存显式 false、缺键=开、IsEnabled 判定；General.Onboarded 首启引导完成标记；Notifications.Channels 用户的 webhook 渠道（只存 id/名称/开关，URL 与令牌在 secretstore）+ GraceSeconds，Grace() 夹在 5–600、默认 30）
settings_test.go: 设置序列化、默认值合并与结构校验
transparent_default_test.go: 外观默认透明度回归

法则: 成员完整·一行一文件·父级链接·技术词前置
[PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
