# internal/usage/
> L2 | 父级: ../../CLAUDE.md

Usage 是 Solco 的只读额度读取域（2026-10-30 产品决策）：把各 Agent 自己的额度窗口读成统一的 `Snapshot{Plan,Windows,Notes}`，供 Usage 页呈现。不含框架/UI 代码（令牌经回调传入，Keychain 在 nativeui），不保存任何历史，不后台轮询：只在页面打开或手动刷新时读。每家逐项开启，只向该家自己的域名发请求；Claude 的 Keychain 读取需用户明确同意；令牌只在内存与调用方的 Keychain，日志只记 provider/state/耗时。

成员清单

- `usage.go`：ID（Claude/Codex/Cursor/Zhipu/Copilot/Antigravity）、Window（用量%、跨度、重置时间、Amount、Used/Max/Unit 计数额度、Group 模型组、Minor 次要限额）、Level（≥80% warn、≥95% bad）、Pace（已过 15% 窗口才外推）、Snapshot/State/Result、哨兵错误 ErrSetup/ErrUnauthorized/ErrUnavailable/ErrCustomEndpoint、Fetcher 接口。
- `service.go`：每家内存缓存（TTL 30 s，Claude 5 min）、手动刷新下限（10 s，Claude 60 s）、429 退避、失败保留旧读数（Stale）、并发 Refresh 跳过忙碌项；`NewServiceClock` 供测试步进时钟。
- `httpjson.go`：HTTP 客户端（20 s、禁跨域重定向）、getJSON/postJSON（401/403→未授权、429→限流、1 MiB 上限、错误只含 host/status）、工具目录与可执行文件查找。
- `codex.go`：起 `codex app-server`，JSON-RPC `account/rateLimits/read`；零凭据，不读 accountId。
- `claude.go`：`/api/oauth/usage`；令牌来自 Keychain `Claude Code-credentials` 或 `~/.claude/.credentials.json`；自定义 `ANTHROPIC_BASE_URL` 直接 ErrCustomEndpoint 不发请求；过期令牌不刷新。
- `cursor.go`：`cursor.com/api/usage-summary`（Cookie 形式）；旧请求制套餐（`/api/usage?user=<sub>` 的 gpt-4.maxRequestUsage>0，如 500 次）以请求数为准，替换 Total/API/Auto 百分比（Plan 显示 "Pro · 500 requests"），新套餐或该调用失败则只用 summary；令牌优先级 手动 > cursor-agent Keychain > 明文 state.vscdb（加密的 safeStorage 值跳过，绝不解密）。
- `copilot.go`：`api.github.com/copilot_internal/user`；令牌为手动 PAT 或 `gh auth token`。
- `zhipu.go`：`api.z.ai`（国际）/ `open.bigmodel.cn`（中国）额度接口，Bearer API key。
- `antigravity.go`：本机来源——`ps`+`lsof` 找 language_server 的 CSRF 与端口，仅回环请求；应用未运行即不可用。
- `antigravity_cloud.go`：离线来源（默认，需用户同意）——读 Antigravity 自存的 Google OAuth（`~/.gemini/jetski-standalone-oauth-token`→旧 `antigravity-cli/…`→Keychain `gemini/antigravity`，含 go-keyring-base64），过期则用其公开 installed-app 客户端在内存里刷新（不写回），POST `retrieveUserQuotaSummary`（daily 主机→生产主机）+ `loadCodeAssist` 取套餐；按模型组产出 5-hour/Weekly 窗口（Group）。`NewAntigravity(allowed)` 先云端后本机，未允许时只问本机；缺 Gemini 组时并入本机 Gemini 窗口。官方不支持第三方使用，风险由用户承担；客户端密钥可用 `ANTIGRAVITY_OAUTH_CLIENT_ID/SECRET` 覆盖。
- `*_test.go`：httptest/假 Fetcher/注入时钟的表驱动测试（含令牌不泄漏检查，`-race` 通过）。

法则: 只读·逐家同意·只访问自家域名·令牌不落盘不进日志·不记历史
[PROTOCOL]: 变更时更新此头部，然后检查 ../../CLAUDE.md
