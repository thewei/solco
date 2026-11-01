# Design Brief: Solco Mobile 重设计（2026-11-01）

> 预览：直接打开 `exploration.html`（`file://`）。hash 预置：`#t=home&v=A&s=home|peek|chat|new`、`#t=outside&s=notif|widget|share`、`#t=gaps`。
> 与桌面第四轮并行：`designs/ux-audit-4/`（议题 9）。

## Decision

| 字段 | 内容 |
|------|------|
| **Status** | `Approved 2026-11-01`（方向修订） |
| **Chosen** | **不做导航大改版**：保留现有抽屉 + 首页（相当于方案 C 的子集），只补：Android 通知 Approve/Deny/回复、主屏小组件、系统分享到 Agent、语音听写、History 搜索 + Running 标识 |
| **用户原话** | 「现阶段的设计可以满足我的需求，最好可以在现阶段的基础上继续做，不需要大幅的重设计」 |
| **实现授权** | 是（桌面 + 手机都做） |

## 调研

- 现状（solco-mobile）：Expo Router 抽屉 + 首页 Agent 看板 + LaunchComposer；Chat = 桌面 WebView 页；Sessions/Quota 藏在抽屉；Android 已有 foreground 保活 + attention 通知；`docs/mobile-ui-ux.md` 停在 2026-08-30，已过期。
- 竞品共性（Happy / Moshi / Omnara / Grok Bot）：**待办优先**（谁在等我）、**状态→预览→接管**三级深入、通知/小组件里直接批准、逐会话草稿、语音输入、系统分享入口。
- 本机缺口：待办必须进聊天才能批；没有 App 外入口（通知 action / 小组件 / 分享）；History 无搜索；Tasks 无入口；picker 卡文字被截断（真机截图）。

## 方案

| | A 收件箱优先（推荐） | B Agent 看板 + 底栏 | C 演进抽屉 |
|---|---|---|---|
| 导航 | 底栏 Now / Sessions / ＋ / Quota / Me | 底栏 Agents / History / Tasks / Quota + FAB | 保留抽屉 |
| 首页 | Needs you 置顶，卡内批准 | 按 Workspace 分组紧凑看板 | 现首页 + Needs you 内联批准 |
| 成本 | 中（导航壳重做） | 低 | 最低 |
| 风险 | 与桌面心智略不同 | 「等我」不突出 | 发现性差 |

App 之外（三案共用）：Android 通知 Approve/Deny/回复、主屏小组件、系统分享到 Agent。

## 约束

- 手机仍是 Host 客户端；导航客户端本地；不暴露 Herdr 类型；`/api/v3` golden 文件两端字节一致；Chat 页代码无手机分支。
- 非目标：iOS Live Activity（另评估）、手机上的 Terminal 作为默认界面、任何云账号/中继变更。
- 落点：`src/app/(home)/`、新增 `(tabs)`、`src/components/`（`pane-row.tsx`、`keep-alive-card.tsx`）、`modules/solco-foreground-service`（通知 action）、`docs/mobile-ui-ux.md`。

## Verification

```sh
pnpm typecheck && pnpm lint && pnpm test && pnpm doctor && pnpm export:web
pnpm check:fixtures && pnpm check:chat-page   # 需 sibling solco 仓库
```
真机（vivo，见 `vivo-phone-install`）：冷启动 → Now 首页；通知 Approve 后桌面卡片同步退役。

## 调研补充（2026-11-01，Grok Bot 官方文档）

- 通知：桌面在用时通知走桌面，手机约 3 分钟后才补发；Bot 级通知开关。→ 可作为后续「桌面在用则手机延迟提醒」的参照，本轮不做。
- 手机输入：Start dictation（键盘听写式，空输入框才出现语音通话）；Android 分享面板目前只收文本。→ 分享到 Agent 先做文本/链接，语音沿用系统键盘听写，不新增 RECORD_AUDIO。
- 无官方主屏小组件；有 Siri/Shortcuts/Live Activity（iOS）。→ 小组件优先级低于通知内回复。

## 实施进度

- 已做：通知内 **Reply**（已停止的 Agent：ready / ready-for-review；`REPLY_CATEGORY`，不拉起 App，经 `sendChat` 走与 Chat 输入框相同的 Herdr 门禁；失败以另一条通知告知）。纯函数 `parseReply`/`isReplyable` 有测试；Android 真机未验证（需 vivo 真机装 dev build 后实测通知输入框与后台投递）。
- 待做（各需 Host 或原生工作，未开始）：Approve/Deny（需通知携带 interactionId，Host 目前只在打开的 Chat 帧里给）、History 搜索 + Running 角标（Host 暂无 sessions 列表接口，需新增 v3 端点 + golden + Zod）、主屏小组件、系统分享到 Agent、语音听写。

## Delivered since (2026-11-01)

- Phone Sessions Continue (`POST /sessions/{id}/continue`), inline Approve / Deny on the blocked-agent notification (the card's own yes/no rows only), voice dictation through the desktop's Voice source (`POST /voice/transcribe`), and Share to Solco (Android text/links → agent draft or new session; `expo-share-intent@5.1.1`, iOS disabled). Home-screen widget (Android, `react-native-android-widget@0.26.0`, user-approved scope 2026-11-01): a 4×2 `SolcoAgents` widget with the Needs you / Working / Idle count bar and the three most urgent agents (title + status dot); a row tap opens that agent's Chat (`solco://chat/<paneId>?…`), ↻ re-reads, anything else opens the app. Titles are shown on the home screen only (`widgetCategory: not_keyguard`). Refresh = the system's 30-minute tick (headless JS reading through the app's own pinned client — no token in native code, nothing persisted) + a live redraw from the running app's tree + the ↻ tap; a Mac that does not answer shows "Can’t reach your Mac", never stale titles. Native changes (RECORD_AUDIO, share intent filter, widget receiver) need a dev-build rebuild.

## Delivered since (2026-11-01, round 2)

- **Connection policy**: one `ConnectionManager` (phone) owns route choice — network-change race with an RTT/direct-bias score, 1/2/4/8/10 s retry ladder, 15/30/60/120 s upgrade ladder, quiet in the background — fed by the network listener, query successes/failures, and the Host's new 5 s `hb` frame on the events socket (13 s of silence = dead path). Replaces `recoverAddress`/`optimizeRoute`/`first-success`/`use-route-optimizer`.
- **Visual pass**: one icon voice (`Icon`/`IconTile`, 1.5 px round lines), shared `Card`/`SectionLabel`/`PillButton`/`IconButton`/`Tap` and row/spacing tokens across Home, drawer, Sessions, Chat header, Connect/Pair, Quota, Changes; motion = press scale, staggered list entrance and reorder, skeleton loading, animated disclosure/progress bars, blocked-dot pulse, spring route banner, themed pull-to-refresh (native control on Android).
- **Smoothness evidence (vivo V2309A, `dumpsys gfxinfo`)**: the Metro dev build janked 30–60% of frames in drawer/Sessions/Chat transitions (p99 100–130 ms) because React and Hermes run in dev mode; the same code as a release build (`assembleRelease`, no Metro) measures 4–6% (p90 15–16 ms, p99 23–36 ms). Fixes that mattered in dev too: animate only the first screenful of a list and never `layout`-animate every row (12.5% → 4.9% in the first pass), virtualize Sessions/Changes (FlatList + `CardRow`), mount the Chat WebView and the Sessions list after the push animation (`useAfterTransition`), and set Sessions/Quota header options statically in `_layout.tsx`.

