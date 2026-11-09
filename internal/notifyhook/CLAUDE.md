# notifyhook/
> L2 | 父级: /CLAUDE.md

成员清单
notifyhook.go: 用户自己的 webhook 通知渠道（2026-11-09 决策，「手机告知不到时调用 webhook」）——Event（kind question|agent|assistant|automation、title/text/project/agent/summary/thread/item/link，全是 Mac 自己的通知用词，无答案/图片/路径）、Target、Load（每次事件现读 settings.json 的渠道清单 + secrets.enc 里每个渠道的 {url,token}，跳过关闭/无密钥/不合规的 URL，所以改设置不用重启常驻 Host）、Sender.Send（并发 POST JSON，Bearer 令牌可选，10 s 超时，失败只记渠道名与状态）、ValidateURL（https，http 仅限 localhost/回环）、Words/Excerpt（有界文本）、SecretKey/EncodeSecret/DecodeSecret
notifyhook_test.go: URL 校验、Load 只取启用且有可用密钥的渠道且 URL 不进 settings.json、Send 的 JSON 与 Bearer 且错误不泄露 URL、文本有界

法则: URL 与令牌是秘密，只存 secretstore，永不进日志/错误/settings.json·只发 Mac 自己通知里已有的词·失败不影响任何 Agent 或 Ask·是否调用由 remotehost 决定（它才知道手机在不在线）
[PROTOCOL]: 变更时更新此头部，然后检查 ../../CLAUDE.md
