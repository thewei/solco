# internal/voice/
> L2 | 父级: ../../CLAUDE.md

Voice 的第二转写来源（2026-10-31 产品决策）：读取本机 Codex 保存的 ChatGPT 登录，经 `POST chatgpt.com/backend-api/transcribe` 用订阅转写。只读：不写 `auth.json`，过期令牌绝不自己续期，而是请 `codex app-server`（`account/read{refreshToken:true}`）自行刷新后重读。令牌只在内存，日志只记状态/耗时，不含音频与文本。无框架代码；同意对话框在 nativeui。

成员清单

- `chatgpt.go`：`ChatGPT`（`Status()` SignedOut/Expired/Ready + Plan、`Transcribe`、`renew`→`codexRefresh`/`refreshExchange`）；凭据取自 `$CODEX_HOME|~/.codex/auth.json`，表单字段 `file`（`codex.<ext>`），头 `Authorization`+`ChatGPT-Account-Id`+桌面端原样身份：`User-Agent: Codex Desktop/<已装 ChatGPT.app 版本，缺省回退常量> (Mac OS; arch)` + `originator: Codex Desktop` + `OAI-Product-Sku: CODEX`（2026-10-31 解包并实测；Cloudflare 对库默认 UA 返回 HTML 403，该 403 归为 `ErrBlocked`，不是登录过期）。
- `chatgpt_test.go`：凭据解析、过期判定、表单形状、401 后续期重试、JSON-RPC 刷新握手回归。

法则: 只读他人凭据·续期交还原主·用户明确同意·不记内容
[PROTOCOL]: 变更时更新此头部，然后检查 ../../CLAUDE.md
