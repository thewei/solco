# internal/secretstore/
> L2 | 父级: ../../CLAUDE.md

Solco 自己的凭据存放处（2026-11-01 用户批准，替代 mygo.Secrets/Keychain）：一个 0600 的 AES-256-GCM 加密文件（`<userDataDir>/secrets.enc`），密钥由「本机硬件 UUID + 文件内随机盐」经 HKDF-SHA256 派生，条目名作为附加认证数据。原因：钥匙串条目的访问权限绑定创建者的代码签名，重装/重编译后每条 Solco 条目都会让 macOS 弹登录钥匙串密码。不含框架/UI 代码；别的应用的钥匙串条目（Claude Code、cursor-agent、Antigravity）不归这里管。

成员清单

- `store.go`：`Store`（Get/Set/Delete）、`Legacy` 接口（旧 Keychain 适配器，由 nativeui 注入）、`New`（仅内存，不碰磁盘与 Keychain）、`Use(path)`（启用文件）。旧 Keychain 条目在该 key 第一次被读取时迁入文件并从 Keychain 删除；每个 key 最多问一次 Keychain（命中/未命中都写进文件的 `checked`），Set/Delete 同样会清掉旧条目且不再让它回流。文件缺失/损坏视为空；换机器或损坏的条目单独丢弃；原子改写（临时文件 + rename）。
- `store_test.go`：往返与重启、文件无明文且 0600、换机器解不开、条目与 key 名绑定（互换密文失败）、损坏文件自愈、旧条目只迁一次并删除、未命中跨重启只问一次、Delete 不让旧条目回流、Set 取代旧条目、仅内存模式不碰 Keychain/磁盘。

边界：这是「0600 文件」级别的保护——防备份/拷走数据目录，防不了以同一用户身份运行的恶意进程（与 `~/.codex/auth.json`、`~/.gemini/…token` 同级）；密钥不是秘密。

法则: 一个文件·一种密钥来源·每个 key 最多碰一次钥匙串·日志不含任何凭据
[PROTOCOL]: 变更时更新此头部，然后检查 ../../CLAUDE.md
