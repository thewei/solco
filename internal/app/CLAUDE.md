# app/
> L2 | 父级: /CLAUDE.md

成员清单
launch_service.go: 应用级启动服务核心，管理 SessionStarter 与 LaunchRegistry 幂等注册表
remote_git.go: 远端手机客户端 Git 状态读取适配器
remote_git_write.go: 远端手机客户端 Git 变更提交/分支写入适配器
remote_launch.go: 远端手机客户端新会话启动网关（RemoteLauncher）
remote_machines.go: 远端机器会话与机器列表管理
remote_queue.go: 远端离线操作命令队列
session_launch.go: 桌面与手机共用的新会话启动服务（SessionStarter：StartSession / Kinds），处理 Terminal 与 Agent（含 Antigravity 默认全权限）启动事务
settings_service.go: 应用级设置读取与持久化服务

法则: 成员完整·一行一文件·父级链接·技术词前置
[PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
