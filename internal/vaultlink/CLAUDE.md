# vaultlink/
> L2 | 父级: /CLAUDE.md

Solco → Solco Vault 的单向链接（2026-11-09 决定「Solco Vault on the phone」）：Remote Host 经它把手机的操作转给独立应用 Solco Vault 的手机门 `bridge.sock`（协议由 `../solco-vault/internal/bridge` 定义，两边各钉同一份 JSON 字面量）。Solco 不存任何密钥、不持有开关、不 import Solco Vault。

成员清单
- `vaultlink.go`：`Client`（`New("")` 取 `SOLCO_VAULT_HOME` 或 `<用户配置目录>/Solco Vault`；每次调用一连接一行请求一行响应，30 s 期限）——`Pending`（状态、`CanReveal`、待批问题）、`Answer`（允许一次/拒绝）、`Browse`（条目名与字段名）、`Reveal`（一个值，只在返回里，从不进日志）、`Available`（Vault 目录存在：Host 才向手机宣告 `vault`）；Vault 没开 = `ErrOffline`（一等状态）；Vault 的拒绝是 `*Error{Code}`（`phone_off`/`reveal_off`/`locked`/`not_found`/`gone`/`invalid`/`note`）
- `probe.go`：手机端「三级探测」的前两级（2026-11-10 B7）——`Installed()`（Spotlight bundle 索引 `mdfind`，退到 /Applications 与 ~/Applications 路径；三态 Yes/No/Unknown，缓存 30 s，从不弹窗：不用 AppleScript 的 `application id`，找不到时它会弹「在哪里？」）、`Running`（bridge 能连）、`Launch`（只在「已安装且未运行」时 `open -b com.whstudio.solcovault`，只传 bundle id；`ErrNotInstalled`/`ErrAlreadyRunning`）；`Available` 现在先看安装三态（装了没跑过也宣告，No 即使残留目录也不宣告，Unknown 才退回 home 目录判断）
- `probe_test.go`：可注入 Prober 的三态、缓存、Launch 三种拒绝、真系统 Prober 对 Finder / 不存在的 bundle
- `vaultlink_test.go`：与 Vault 仓库同字面量的 pending 响应、`answer` 请求形状、拒绝保留错误码、无人监听 = 离线、browse 与 reveal

法则: 开关与状态只属于 Vault 应用·值只在 Reveal 的返回里·离线是状态不是错误
[PROTOCOL]: 变更时更新此头部，然后检查 /CLAUDE.md
