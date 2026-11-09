# vaultlink/
> L2 | 父级: /CLAUDE.md

Solco → Solco Vault 的单向链接（2026-11-09 决定「Solco Vault on the phone」）：Remote Host 经它把手机的操作转给独立应用 Solco Vault 的手机门 `bridge.sock`（协议由 `../solco-vault/internal/bridge` 定义，两边各钉同一份 JSON 字面量）。Solco 不存任何密钥、不持有开关、不 import Solco Vault。

成员清单
- `vaultlink.go`：`Client`（`New("")` 取 `SOLCO_VAULT_HOME` 或 `<用户配置目录>/Solco Vault`；每次调用一连接一行请求一行响应，30 s 期限）——`Pending`（状态、`CanReveal`、待批问题）、`Answer`（允许一次/拒绝）、`Browse`（条目名与字段名）、`Reveal`（一个值，只在返回里，从不进日志）、`Available`（Vault 目录存在：Host 才向手机宣告 `vault`）；Vault 没开 = `ErrOffline`（一等状态）；Vault 的拒绝是 `*Error{Code}`（`phone_off`/`reveal_off`/`locked`/`not_found`/`gone`/`invalid`/`note`）
- `vaultlink_test.go`：与 Vault 仓库同字面量的 pending 响应、`answer` 请求形状、拒绝保留错误码、无人监听 = 离线、browse 与 reveal

法则: 开关与状态只属于 Vault 应用·值只在 Reveal 的返回里·离线是状态不是错误
[PROTOCOL]: 变更时更新此头部，然后检查 /CLAUDE.md
