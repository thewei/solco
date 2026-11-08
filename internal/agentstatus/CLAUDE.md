# agentstatus/
> L2 | 父级: /CLAUDE.md

成员清单
protocol.go: OSC 7501 rev 0.4 报告体解析：冒号分对、未知键忽略、同键后者胜；对级语法错仅跳过该对，但空 id、key/app/msg/title 硬上限、坏 base64、禁用控制字符整份拒绝；空 base64 允许、原子性防根记录污染；提供 State/Kind/Report 与 StatusWord
records.go: 终端侧记录模型：每 id 一记录、报告整体替换、`clear` 无 id 清空全部/有 id 清子树、达 256 条按最近更新时间 LRU 逐出（含 root，规范原文无豁免）；进程退出与 OSC 133 A 丢 working/blocked（done/error/idle 存活）；Display 按紧急度排名（blocked>working>error>done>idle，root 同分优先、同级取新），app 从最近祖先继承
store.go: 每 Pane 的有锁单一状态来源仲裁（OSC 7501 in-flight > Solco 自有 apphook > Herdr spool fallback）：记录集合与生效来源独立，消除无协议心跳的 2 分钟错误回退，处理 OSC 133 A/RIS/Herdr 真实前台进程结束与 Pane 真实删除；ApplyProgramReport/ApplyPassiveProgramReport/ApplySolcoHookStatus/ApplyHerdrHookStatus/ApplyControl/ApplyVerifiedAgentEnd/HasTransient/Display/DisplayFor/DisplayTerminal/PaneClosed/Reset；只在精确 Agent/Pane/Session+完成的 agent_end 后，VerifiedAgentEnd 能清掉 OSC stale working/blocked，却保留 done/error，绝不可由单条 message_end 触发；DisplayFor（2026-11-06）= Display + Herdr 实测 runtime 状态：runtime `blocked` 压过 Solco 来源的 `working`（omp/pi 无 Solco 状态 hook，仅 chat-stream 的 run 级 working，原先会把 ask/审批提问盖住整个 run），其余不让位，活 OSC 记录权威不变；Observe(pane, runtime, state_change_seq)（2026-11-06 第 4 轮）= chat-stream 猜测（ApplyStreamRunStart 的 working / stream 来源的 agent_end idle）的边沿撤销：只在流标记之后 Herdr 的 state_change_seq 严格变大且与猜测相反（working↔idle/done，idle↔working/blocked）时撤销，首次观察只作基线、计数回退（Herdr 重启）只重设基线，Hook/OSC 来源从不撤销——解决 agent_end 丢失时 Stop 卡住与 agent_end 后 idle 粘住
protocol_test.go: 解析回归——spec 原例（terraform blocked、working:progress=50、clear）、对级规则（未知键/同键后者胜/字符集外跳对）、整份丢弃（未知状态、id 语法、坏 base64、控制字符、超限）、字段规则（progress/kind/app 的态相关与缺省语义、无填充 base64）
records_test.go: 记录回归——整体替换、clear 子树与清空、瞬态丢弃与 Reset、Display 排名（blocked 子记录压过 working root）与 app 继承、LRU 逐出（陈旧 root 被逐、现行 root 存活）
store_test.go: 来源优先级回归——Observe 边沿撤销（同 seq 不撤、基线、计数回退、Hook/OSC 免疫）、DisplayFor 只让 blocked 压过 Solco working（idle/无 runtime 事实/OSC/未知 pane 不让位）、native transient 不靠心跳、Solco Hook 胜 Herdr spool、clear 恢复直接 Hook、released 和 VerifiedAgentEnd 保留 done、后者仅基于真实 Agent run end 撤销 transient、prompt/RIS 清理、无效消息不变、PaneClosed 重置

[PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
