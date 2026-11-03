# hostd/
> L2 | 父级: /CLAUDE.md

成员清单
lease.go: Host v3 端口的唯一所有者租约——`host.lock`（flock，进程崩溃由系统释放）+ `host.yield`（桌面要接管时写入，15 s 内有效）；TryAcquire/Release/Claim（桌面礼让协议：先试，再请求让位并轮询，超时 ErrHeld 并撤回请求，绝不双监听）
config.go: ConfigFrom/RelayFrom/P2PFrom——保存的 Remote Access 选择 → Host 配置，桌面与常驻服务共用（未知模式=off，永不放宽）
run.go: 常驻服务主循环 Run——设置 off 不监听；租约空闲且无新鲜让位请求才接管；桌面让位请求→关监听放租约；设置变化→Reconfigure；失败 5 s 后重试；Host 是最小接口，测试用假 Host
serve.go: 无 GUI Host 的装配 NewServer（Herdr Manager 的 Backend + 经 `herdr plugin list` 找到的 Assistants 插件缝，30 s 缓存；不接任何依赖窗口状态的缝，bootstrap 因此如实不声明）、RemoteDir（与桌面同一个 `<userData>/RemoteHost`＝同一证书/设备库/令牌）、LoadRemoteSettings（只读读 settings.json）
launchd.go: 用户级 launchd 服务 com.whstudio.solco.hostd（RunAtLoad+KeepAlive+ThrottleInterval 3，日志 ~/Library/Logs/Solco/hostd.log）；Locate（Contents/Helpers|Resources）、Installed、Install/InstallSelf、Uninstall、Status
run_test.go: 让位/接管/关闭/过期请求的状态机
identity_test.go: 两个所有者共享证书指纹与设备库（SOLCO-R02）；plist 渲染

法则: 同一时刻一个所有者·身份只有一份·常驻只服务不需要窗口的能力·能力如实声明
[PROTOCOL]: 变更时更新此头部，然后检查 ../../CLAUDE.md
