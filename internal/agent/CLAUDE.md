# agent/
> L2 | 父级: /CLAUDE.md

成员清单
capabilities.go: Provider 能力投影，定义各 Agent 的暴露级别、启动能力、实时能力与权限/计划模式支持
card.go: Agent 状态与卡片投影模型，派生工作台呈现与状态标签
commit_message.go: 基于 Agent 提交意图推导 commit 信息与格式化
continuation.go: 会话历史继续策略与执行（原生恢复与上下文迁移）
directory.go: Agent 运行目录与项目映射管理
integration.go: 各 Provider 的运行时集成策略注册表
integration_health.go: 集成健康度探测与诊断分析
launch.go: 启动请求校验与幂等 LaunchRegistry 调度
markers.go: Agent 状态标记与特征定义
notifications.go: Agent 事件通知与分派
operational.go: Agent 运行时操作原语
transaction.go: 启动意图 LaunchIntent 与参数派生，映射各 Provider CLI 参数（Antigravity 默认全权限）及 Herdr 种类映射（claude, agy）
transaction_runtime.go: RunLaunchTransaction 核心事务调度，负责工作区匹配、标签重命名与 Herdr agent.start 调用
usage.go: Agent token 用量投影与聚合
workbench.go: 工作台 Agent 聚合视图与生命周期管理

法则: 成员完整·一行一文件·父级链接·技术词前置
[PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
