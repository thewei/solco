# Owner 与 Agent 身份隔离威胁模型 + 最小 Grant 模型(A04 前置)

状态:2026-11-05。范围:Solco Assistants 核心(`plugins/solco-automations/internal/personalagent`)。
结论先行:**在"Agent 与用户是同一 OS 用户、且能执行 shell"的前提下,纯软件手段无法证明"这是人在操作"。**
本轮交付的是:(1) 把能证明的部分做成 Go 强制(fail-closed、Owner 封印的 Grant);(2) 把不能证明的部分明确列为残余风险,并把"信任锚放哪"这一决策交给用户。

## 1. 参与者与资产

| 参与者 | 能力 |
|---|---|
| Owner(人) | 在 Solco 桌面 / 已配对手机上点审批、改设置 |
| Coordinator / Worker Agent | 以 Owner 同一 OS 用户跑在 Herdr pane 里,有 shell、文件系统、网络 |
| Solco.app / plugin daemon / solco-hostd | 同一 OS 用户的常驻进程,持有核心 SQLite 的写入权 |
| 外部发送者(webhook) | 只持有 hook key 的明文密钥,走 Host `/hooks/…` |

资产:审批决定(`approval.decide`)、自治等级(`autonomy.set`)、Grant 本身(`grant.set`)、仓库发布(`repo.publish`)、记忆、Routine 启用、凭据文件(`secrets.enc`、`~/.codex/auth.json` …)。

## 2. 入口清单与实测

实验在隔离目录 `HERDR_PLUGIN_STATE_DIR=/tmp/idexp` 下用**真实构建的插件二进制**完成(2026-11-05):

| # | 入口 | 动作 | 结果 | 含义 |
|---|---|---|---|---|
| E0 | `assistant --as <id> memory propose` | Agent 提议记忆 | 成功 | 设计内的 Agent 能力 |
| E2 | `assistant --as <id> memory approve` | Agent 批准自己的记忆 | **`forbidden`**("a person's action, not a Coordinator's") | `coordinatorScope` 白名单有效 |
| E5 | `assistant --as <id> grant list` | Agent 读 Grant | **`forbidden`** | Grant 面不在 Coordinator 范围 |
| E1 | 裸 `assistant memory approve`(无 `--as`) | 同一用户直接调用 | **成功** | 裸 CLI 就是 Owner/系统面,**Agent 的 shell 同样能调**。`--as` 是约定,不是身份 |
| E3 | `sqlite3 assistants.sqlite "update memory_items …"` | 直接写库 | **成功**(文件 0600、同用户) | 任何核心表都可被同用户改写,绕过所有 Go 校验与 receipts |
| E4 | 往 `capability_grants` 伪造一行(无 owner key / 错封印 / 列类型错) | 再 `grant decide` | 不授权(`no_owner_key` / `bad_seal`);列类型错的行最初让决策报 `internal`,已改为"不可读行 = 惰性 + `bad_seal`"(`TestUnreadableGrantRowIsInert`) | 伪造 Grant 行不产生权限 |
| E4c | `grant decide approval.decide` | 请求 Owner 专属能力 | **`deny` / `owner_only`**,并写一条 receipt | Owner 专属能力永远不能被 Grant 授予 |

仅从代码阅读得出(未实验):Herdr API socket(同用户可 `pane.send_input` 到任何 pane,含 Owner 的终端)、Host v3(需要设备 token,hash 存储;`secrets.enc` 同用户可读,密钥由硬件 UUID 派生,不防同用户进程)。

## 3. 核心判断

1. **`--as` 不是身份边界。** 它防的是"Coordinator 扩展里的 Agent 误调用",防不了"Agent 开 shell 调裸 CLI / 直接写库"。
2. **同一 OS 用户内,任何"文件里的秘密"都对 Agent 可读。** 所以靠"口令/令牌文件"证明人不可行。
3. 因此能做的只有两类:
   - **让 Agent 没有的东西成为授权的必要条件**(Owner 私钥只在 Owner 的设备/进程里签名;核心只存公钥)。
   - **缩小爆炸半径**(Grant 显式范围、每日上限、TTL、不可变、撤销)。

## 4. 本轮交付的最小 Grant 模型(PolicyEngine 骨架)

`grants.go` / `api_grant.go`(迁移 v27 `capability_grants`、`authorization_receipts`):

- **封闭能力表**:未知能力 `invalid`;`grant.set` / `autonomy.set` / `approval.decide` 为 Owner 专属,永不可被授予;`repo.publish` 为高风险,必须带显式 scope。
- **Owner 封印**:Grant = Ed25519 对 `"solco-grant-v1\n"+canonical JSON` 的签名(含 id、助手、能力、effect、scope、上限、签发/到期)。核心只持公钥(`SOLCO_OWNER_PUBKEY_FILE` 或 `<StateDir>/owner.pub`,hex/base64)。**无公钥 = fail-closed**(`no_owner_key`);公钥损坏 = 日志告警并同样 fail-closed,不拖垮核心。
- **不可变**:改动 = 撤销 + 新 Grant(id 不可复用);TTL ≤ 90 天;scope 只接受显式值,**不接受 `*`**;可设 `maxPerDay`。
- **deny 优先**:deny Grant 无需封印,命中即拒。
- **子范围取交集**(`NarrowScope`),子任务不能比父更宽。
- **收据**:每次决策写 `authorization_receipts`,放行时 `authorizedBy = "grant:<id>"`——它**不冒充**人的审批。
- **CLI**:`assistant grant create|list|show|revoke|decide`,全部不在 `coordinatorScope`;`list` 输出不含封印。
- 测试:`grants_test.go`(含变异验证:把 `Verified` 恒置 true 后 `TestForgedGrantRowsAreInert` 立即红)。

**接线(2026-11-05,用户决定"信任锚先留在 L1")**:
- 第一个调用方是 `memory.autosave`:Agent 自己的 `memory propose`(`--as`,作者 `agent`)在同一事务里问 `authorizeTx`;仅当存在**已验证、在 scope(kind)内、未过限额/未撤销/未过期**的授权才直接转为 approved,并写 allow receipt(`authorizedBy grant:<id>`)。文件来源、人写入、其它作者永不走这条;凭据类内容仍先被拒;无 owner 公钥时整段跳过;普通"没有授权"的提议**不写收据**(噪声),只有 allow/deny 写。
- Owner 端入口:`assistant owner status|register|rotate` + Solco 桌面 More ▸ Autonomy 顶部"Keep memories without asking"。私钥只在 Solco 的 `secrets.enc`(`assistants.owner.key`,与 Solco 其它自有凭据同级保护),首次 Allow 生成并注册公钥;**核心此后拒绝任何别的公钥**(`owner_key_exists`),换钥只能由当前私钥签封的 `rotate`(换钥后旧授权全部失效、需重新签)——所以裸 CLI 调用换不掉锚;文件层面仍可写(R1 不变)。
- 验证:`TestRealPluginBinaryOwnerGrantAutosave` 用真插件二进制跑通 root 签名→核心验证→Agent 提议自动保存/越 kind 等待/撤销即停/陌生人签名的授权惰性;双端 `TestGrantSealGoldenVector` 钉住同一字节。

**尚未做**:发布/花钱/对外动作(`repo.*`、`task.delegate`、`automation.manage`)没有接线,也不应在信任锚升级前接线;手机端无 Autonomy/Grant 页面。

## 5. 加固阶梯(从便宜到彻底)

| 级 | 做法 | 防住什么 | 防不住 |
|---|---|---|---|
| L0(现状) | `--as` 白名单 + Go 校验 + receipts | Agent 误用 | 任何有 shell 的 Agent |
| L1(本轮) | Owner 封印 Grant,伪造行惰性 | 直接写库伪造授权、缺公钥时误放行 | 替换 `owner.pub`(见 R1) |
| L2 | 公钥放 Keychain(Solco.app 签名 ACL,Agent 的 shell 取不到无提示) | R1 | 能调用 Solco.app 自身签名身份的进程 |
| L3 | 私钥在 Secure Enclave / 触控 ID 门控的签名;或手机端签名(配对设备私钥) | 同一 Mac 上的所有软件攻击 | 物理 / 屏幕控制 |
| L4 | Agent 跑在独立 OS 用户/沙箱,核心库与 Herdr socket 对其不可达 | E1、E3 整类 | 运维复杂度高,Herdr 当前无此形态 |

## 6. 残余风险

- **R1 信任锚可被替换**:`owner.pub` 是同用户可写文件。有 shell 的 Agent 可换成自己的公钥再自签 Grant。由 `TestKnownResidualAgentThatCanWriteTheKeyFileCanMintGrants` 作为 tripwire 固定;信任锚移出 Agent 可达范围后该测试必须翻转。
- **R2 撤销可被回滚**:撤销是库内一行;同用户可改库使其"未撤销"。上限由 TTL ≤ 90 天与 `maxPerDay` 约束。
- **R3 E1/E3 未解**:裸 CLI 与直接写库仍能做 Owner 的所有事(批准、遗忘、改记忆)。Grant 骨架只对"接线后经过 `Authorize` 的动作"有约束力;要让审批本身抗 Agent,需要 L3/L4。
- **R4 Webhook 明文密钥存插件 SQLite**(同用户可读),与 R3 同类。

## 7. 待用户决策:信任锚放哪?

1. **Keychain(L2)**:成本低,马上可做;防"换文件",不防"调用 Solco 自身身份的进程"。
2. **手机配对设备签名(L3 变体)**:Owner 在手机上批准时签名,Mac 上的 Agent 拿不到私钥;依赖 Android/手机端工作(当前未授权)。
3. **Secure Enclave + 触控 ID(L3)**:强,但需要 Solco.app 增加原生签名通道,并改变"后台自动审批"体验。
4. **暂不动(维持 L1)**:接受 R1/R3,把 Grant 骨架接到现有动作上,后续再升级信任锚。

在此决策前不应把 Grant 接到任何会发布/花钱/对外的动作上。
