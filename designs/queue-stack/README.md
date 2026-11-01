# Queued message stack(排队消息堆叠卡片)

> 状态:**已定稿并实现(2026-11-01)。** 用户已确认:多条消息堆叠展示、拖拽排序、收起显示总数+最新一条、逐条取消;**逐条按顺序发送**(一条消息占用一个回合边界),手机端本轮一起改。

## 设计问题(一句话)

跟进队列是"每会话一槽":第二条 follow-up 直接被 `ErrQueueSlotTaken` 拒掉,桥上只有一根 `queuedText` 字符串,页面上只有一枚 "Queued after turn" chip + 全量 Cancel——排队超过一条、调整顺序、取消其中一条都做不到。

## Phase 0 取证(代码现状,不是猜的)

| 层 | 现状 |
|---|---|
| 队列引擎 | `internal/conversation/queue.go`:`FollowUpQueue` = `map[ConversationID]*QueueItem`,一槽;状态机 Queued→Waiting→Delivering→Delivered,FailedRecoverable 可重排,DeliveryUncertain 是冻结墓碑(阻塞替换、不自动重发、不假装未发出) |
| 投递 | `delivery.go` `DeliverOnce`:一周期一会话一条 prompt;阻塞/超时保留队列(≤120 周期后转 recoverable 文本);身份指纹在边界复核 |
| 桌面桥 | `chat_bridge.go`:`QueuedText string` 单字段;`CancelQueued` 全取消 |
| 页面 | chip:`.queued` 一行 + Cancel 按钮;原生页 `chat_ui.go` 同款一行 |
| 手机 | `internal/remotehost/queue.go`:`FollowUp{text,state,detail}`,第二次入队回 409 queue_full;solco-mobile 走同一份页面代码,靠 `page-state.ts` 适配 |

## Decision

1. **数据模型**:`FollowUpQueue` 每会话改为有序列表(`ConversationQueue.Items`),顺序即投递顺序。`Enqueue` 追加;`CancelItem`/`CancelAll` 撤回;`Move` 排序;`Items`/`Head` 读;`Transition`/`ClaimDelivery`/`CompleteDelivery` 全部按 request id 键控(取消A/入队B/移动/认领互相竞态不会让旧 worker 误发——沿用既有保证)。Delivered/Cancelled 条目即出列;**DeliveryUncertain 仍是冻结墓碑**:阻塞追加(新错误 `ErrQueueFrozen`)、扣住后面所有条目、唯一出路是显式取消(= 用户确认,`legalQueueTransition` 新增 `DeliveryUncertain → Cancelled`)。
2. **发送语义(用户已选)**:逐条按顺序发送,一条消息一个回合边界——agent 能看到上一条的执行结果再执行下一条。worker 每周期只取队头;完成出列后 `DeliveryOutcome.More` 让排水循环回来处理下一条(5s 步进,复用既有 retained 节奏)。被否决:合并成一条 prompt 一起发(agent 一次看到全部指令、看不到逐条结果)。
3. **排序边界**:只有 worker 未认领的条目(Queued/FailedRecoverable)可移动;队首 Waiting/Delivering 前缀钉住不可跳——已递交给发送路径的 prompt 不会被换序甩开。
4. **卡片**:收起 = 计数徽标 N + 最新一条文本(数组末条),点击展开/再点收起;展开 = 头部 "Queued · N" + Cancel all,行列表带拖拽把手、逐行 ✕(delivering 行除外)、下一条要发的标 `next`、failed/uncertain 行警示色;>4 行内滚;pointer 拖拽(非 HTML5 DnD)+ ⌥↑/⌥↓ 键盘等效;手机 `features.reorder=false` 隐藏拖拽,卡片与逐条取消照常。
5. **wire**:chat 帧 `queued` → `{items:[{id,text,state,detail?}]}`;新端点 `POST …/chat/queue/item/{requestId}/cancel`;chat/send 入队总是成功,唯一拒绝是 409 `queue_blocked`(冻结);cancel-all 端点保留。
6. **范围(用户已选)**:桌面 + 手机同轮;solco-mobile 只是同一份页面代码的状态适配器,页面组件天然共享。

## 影响面

| 位置 | 改动 |
|---|---|
| `internal/conversation/queue.go` | 单槽 → 有序列表:Enqueue 追加、Items/Head、CancelItem/CancelAll、Move(pinned 前缀)、ErrQueueFrozen、Snapshot/Restore 保序 |
| `internal/conversation/delivery.go` | 一周期 = 队头一条;`More` 驱动逐条排水;Transition/Complete 按 requestID |
| `internal/nativeui/chat_*` | Shell 删 `chatQueuedText` 改现读账本;桥 `queuedItems` + CancelQueuedItem/ReorderQueued;原生 chip → `Queued after turn · N` + Cancel all;Header 菜单条件跟随 |
| `frontend/` | `queue-card.tsx`(收起/展开/拖拽/逐条取消/Cancel all/⌥↑⌥↓);mygo.ts 类型;chat.css |
| `internal/remotehost` + `internal/app` | FollowUps 接口 Items/CancelItem;帧 items;新取消端点;queue_blocked |
| `solco-mobile` | Zod items 契约、page-state 适配(uncertain/failed → outcome 句子不变)、client cancelQueuedItem、PHONE_FEATURES.reorder=false |

## 约束(来自 AGENTS.md / 既有决策)

- 不新增 Herdr 方法;队列与投递引擎仍只在 conversation/app;页面是纯表现层,只通过桥调用。
- 不确定即墓碑的诚实原则不变:card 上的 uncertain 行绝不假装未发出,取消即显式确认。
- 日志不含提示正文;手机端凭据/正文零接触(本改动不新增任何凭据面)。

## 最脆弱假设(premise collapse)

- **"下一条等的是新回合的边界"**:一条投递后 agent 开新 turn,下一条的 `WaitTurnBoundary`(60s 有界等待 + retained 重试)自然等它结束——与今天"turn 中途入队"走的是同一条路径,无新机制。若某 provider 的边界探测在连续投递下失真,表现为第二条延迟而非错发(保留队列、绝不静默)。
- **收起态显示"最新一条"而非"下一条要发的"**:用户明确要最新;下一条在展开里以 `next` 标出。
