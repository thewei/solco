# Design Brief: 手机端 = 电脑端的投影（2026-11-02）

> 预览：直接打开 `exploration.html`（`file://`）。hash 预置：`#t=nav&v=A`、`#t=pushdown&s=files|changes|services`、`#t=devices&s=panel|signin`、`#t=background&s=home|chat|service`、`#t=newtab&s=menu|draft|sent`、`#t=notif`。
> 前序：`designs/mobile-redesign/`（2026-11-01 定「不做导航大改版」）。本稿是用户 2026-11-02 明确要求的**重新设计**，会覆盖那条 Decision 里「保留抽屉」的部分，所以必须由用户重新选型。

## Decision

| 字段 | 内容 |
|------|------|
| **Status** | `Decided 2026-11-02` |
| **Chosen** | 导航 **A 投影标签**；Git **G3**（只读 + stage/commit + push/pull）；Files **只读 + Attach to chat**；设备只连桌面已登录/可直连的（MFA 提示回 Mac）；桌面 **＋ 总是出菜单**（⌘T 沿用 last-used）；子代理 + 服务合并为 Background 分组 |
| **追加（同日，用户）** | (a) 桌面侧栏 Agents 视图也并入后台服务，用图标区分子代理/服务；(b) 桌面 Tab 条支持拖拽排序，并参考成熟浏览器 Tab：Tab 多时收起文字只留图标/缩略标题，悬停显示完整信息、可展开 —— 这两项**需先探索**（`designs/tab-strip/`），不直接实现 |
| **实现授权** | 是，按「分阶段」逐阶段落地；追加 (a)(b) 先出探索稿 |

## 已完成（不依赖选型，已改代码）

1. 常驻通知：前台也常驻，文案是实时的 `Needs you: N · Working: N · Idle: N`（`KeepAliveService.post` 原位重发，`LOW` 重要性新频道 `solco.keepalive.status`）。
2. 工具聚合行 `1 failed` 不换行（`.failed {flex:none; white-space:nowrap}`，同类：Tasks 标题、子代理表头/页脚）。
3. Context 弹窗在手机上以输入框为定位盒、左右贴边，不再溢出。
4. Herdr session 切换折叠成一行（点开才选，选完收起）。

## 调研（现状事实，均来自代码）

**手机现在有什么**：Home（Needs you/Working/Idle）、Chat（桌面同一页面，含子代理卡片/抽屉）、Terminal（observe 栅格）、Changes（只读 status + 单文件 diff）、Sessions（搜索 + Running 标识 + Continue）、Quota、Compose/Launch、通知 Reply/Approve、小组件、Share。
**Host 现有路由**（`internal/remotehost/server.go`）：bootstrap / tree / events / peek / chat(+stream/send/stop/answer/queue/attachments/catalog/files/subagents) / launch(+catalog/files/attachments) / workspaces/{id}/git(+diff) / quota / sessions(+continue) / voice / p2p / terminals stream。**没有**：文件浏览、Git 写操作、后台服务、设备（Host `Instances()` 明确只列本机 Session，见 `backend.go`）。
**桌面对应物**：侧栏五个视图标签 Terminal/Changes/Sessions/Files/Hosts；Changes/Files/Sessions 三页共用「项目头」（项目 + 分支切换）；Agents 区跨项目；页脚是「设备 + Herdr 会话」一个芯片（`device_switcher.go`）；Services = `service_index.go` 的 `pane.process_info × lsof` 扫描 + Header 浮层 Services 页签；子代理 = `internal/history/subagents.go` + `chatsession`（三家 Provider）。
**桌面设备机制**：设备档案只在 Herdr（`herdr machine …`）；切换 = 事务式 SSH 桥（`herdr.ConnectMachine`）；需要登录/MFA 的由桌面 Terminal 接力，Solco 不收凭据。

## 约束

- 手机仍是 Host 客户端，导航客户端本地，不移动 Mac 焦点；不暴露 Herdr 类型；新增 `/api/v3` 路由必须配 golden 文件，两端字节一致。
- Herdr 仍是唯一运行时；不新增 Herdr 方法；设备档案不在 Solco/手机落第二份注册表；手机不持有任何设备凭据。
- Chat 页代码无手机分支（只加 `PHONE_FEATURES` 开关）。
- 破坏性 Git 动作（reset/discard/branch 删除/rebase/冲突解决）永不下放。

## 一、下放什么（逐项评估，附位置与风险）

| 功能 | 手机价值 | 下放形态 | Host 新增 | 风险 | 结论 |
|------|---------|----------|-----------|------|------|
| **Files** | 高：看 Agent 刚改了什么/读 README/把文件当上下文递给 Agent | 项目作用域的树 + 预览（文本/代码/Markdown/图片）；顶部名称搜索；「Attach to chat」把 `@path` 放进某 Agent 草稿 | `GET /workspaces/{id}/files?dir=`、`/files/content?path=`（复用 `internal/filesview` 的忽略规则与 8 MiB 上限；`..`/越界一律拒绝，`contained`） | 低（只读）；正文不进日志 | **做，只读** |
| **Changes（Git）** | 高：Agent 干完活要「看一眼 → 提交」 | 现有只读视图 + 分阶段写：G1 已有；G2 暂存/取消暂存 + 提交（先弹确认，带 Subject）；G3 Fetch/Pull/Push | `POST …/git/stage|unstage|commit|push|pull|fetch`（scope manage；复用 `gitworkbench.Runner`，不新起 Git 进程逻辑） | G2 中；G3 中（Push 影响远端，需确认）；冲突/丢弃/分支管理**不做** | **G1 已有；G2 推荐；G3 待你拍板** |
| **Sessions** | 中：续接历史会话 | 已有（搜索/Running/Continue）；补：行内「Go to Pane」与桌面一致 | 无 | 低 | **保持** |
| **Services（后台服务）** | 中高：Agent 起了 dev server，想知道是否还在跑、端口多少 | 与子代理合并成一个「Background」分组（见第四节） | tree 的 pane 上加 `background[]` | 低（只读） | **做，只读** |
| **子代理** | 高 | 同上；点击进抽屉（已有 Chat 页能力） | tree `background[]` + 已有 `/chat/subagents/{id}` | 低 | **做** |
| **Tasks（herdr-projects 看板）** | 中：「审批计划」是典型手机动作 | Needs you 里的一个来源；仅插件门开 | `/tasks`（只读）+ Approve | 中（依赖插件 1.2.x 门） | **后期，不在本轮** |
| **设备（电脑端已连过的远程机器）** | 高：手机变中控 | 见第三节 | `bootstrap.instances[]` 带设备信息 + `POST /devices/{id}/connect` | 高（SSH 桥、掉线、权限） | **做，分两步** |
| Quota | 已有 | — | — | — | 保持 |
| Settings（除远程访问外）、Hosts SSH 配置编辑、Worktree/分支管理、Library/Reports、Terminal 分屏布局、设备 Add/Sign-In | 低/需键盘/需 MFA | 留在电脑 | — | — | **不下放** |

判据：①手机上是否是「看一眼/点一下」而不是「长时间编辑」；②是否能在不引入第二运行时的前提下由 Host 现有读口径提供；③破坏半径。

## 二、导航：三个方案（结构不同）

| | A 投影标签（推荐） | B 项目枢纽 | C 抽屉加行 |
|---|---|---|---|
| 骨架 | 底栏 **Agents · Changes · Files · Sessions**；顶部「项目头」（项目 ▾ · 分支）+ 左上设备芯片；＋在顶栏右侧 | 底栏 **Now · Projects · Sessions · Me**；Projects 里点项目进枢纽，枢纽内分段 Agents/Files/Changes | 保留抽屉；抽屉加 Files / Changes / Services 行 |
| 与桌面的对应 | 一一对应：侧栏视图标签 + 项目头 + 页脚设备芯片 | 弱对应 | 弱对应 |
| Agents（跨项目）与项目作用域的关系 | Agents 标签不分项目；其余三个标签共用项目头（桌面同款规则） | 项目被抬成一级入口 | 抽屉里分组 |
| 「谁在等我」 | Agents 标签顶部 Needs you，徽标在标签图标上 | Now 标签 | 首页 |
| 成本 | 中（导航壳重做，页面基本复用） | 中高 | 低 |
| 风险 | 底栏占高度（Chat/Terminal 全屏页隐藏它） | 项目层多一次点击 | 发现性差，Files/Git 藏深 |

**我的立场**：选 A。理由：用户目标是「投影」，A 是桌面信息架构的逐一映射，心智零切换；Files/Changes/Sessions 三页共用一个项目头也是桌面既有规则（不需要重新发明）。若你更看重「改动最小」选 C；B 不推荐（它把 Agent 看板降成一个标签，违背「Agent 优先」）。
推翻条件：真机上底栏 + 项目头占掉 >120dp 导致 Chat 太挤 → 改 C。

## 三、设备（item 5）与 Herdr 会话（item 4）= 一个芯片一块面板

- 顶栏左上一个芯片：`This Mac · default ▾`（与桌面页脚芯片同构）。点开 → 一块面板：
  - **Herdr 会话**（折叠成一行，当前会话名；点开才列，选后收起 —— 本轮已落地的折叠行就是它的雏形）。
  - **设备**：Herdr 里保存的机器（`herdr machine list`），每行状态点：`Online`（桌面已有共享 SSH 连接或密钥可直连）/ `Needs sign-in`（需要在 Mac 上登录/MFA）/ `Disabled`。
- 切换设备 = 手机侧事务：Host 为该设备拉起/复用自己的 SSH 桥（`herdr.ConnectMachine` 同一份实现），成功才把手机的 `instance` 换过去；**失败/取消不改变当前**。不移动 Mac 窗口所在的设备。
- 线上形态**最小改动**：现有每个路由都已按 `instance` 寻址，所以设备只是 `bootstrap.instances[]` 里的更多条目（新增可选字段 `{ kind:'local'|'device', label, status }`），外加 `POST /devices/{id}/connect`（scope manage）。Backend.Instances 增加「已桥接的设备」。
- **诚实的限制（必须告诉用户）**：设备上的 Agent 其会话文件、cwd、Git 仓库在**远端**，而 Host 的 Files/Git/Sessions/Quota/Voice 读的是**本机**。所以设备实例上这些标签要么隐藏、要么走各自的 `capabilities` 按实例关闭——和桌面对远程实例的做法一致（桌面 `activeIsRemote()` 已经隐藏 Tasks/Sessions 范围）。设备上保证可用的是：Agents 列表、Terminal（Herdr observe 经桥）、launch。**设备上 Chat 能否可用，取决于桌面 `chat_live` 对远程窗格的处理，我没有在代码里找到远程分支，实现前必须先在真机上确认**（这是未决项，见底部 Unknowns）。
- 需要登录/MFA 的设备：手机只显示 `Needs sign-in` + 一句「在 Mac 上登录」，不尝试收集任何凭据（与桌面「Solco 不收凭据」一致）。

## 四、子代理 + 后台服务合并为一个「Background」分组（item 8、9）

统一概念，一个 tree 字段：`TreePane.background[] = { kind:'subagent'|'service', id, label, status, since, port? }`（上限 8 条，参与 tree 内容哈希，桌面 `AgentFacts` 快照一并发布，沿用 `Poke`）。

- **形状说话**（桌面状态槽位规则）：子代理 = Agent 圆形指示（运行转圈/完成空心环/失败实心红点）；服务 = 圆角方块。颜色表紧急度。
- **Home/Agents 列表**：父 Agent 行右侧一个 `2 ▾`（`1 running`），点开行内嵌套（与桌面侧栏同样的折叠规则：全部完成 10 分钟后自动折叠，用户有意展开则保持）。
- **跳转**：子代理行 → 打开该 Agent 的 Chat 并直接弹出**底部全高抽屉**（桌面 `openSubagentDrawer` 的手机版；Chat 页已有 `chat.OpenSubagent`，Host 已有 `GET …/chat/subagents/{id}`）；服务行 → 服务底部卡片（名称、端口、运行时长、命令行；**只读**，v1 不提供 Stop）。
- **Chat 页内**：1–2 个子代理是卡片，≥3 是名册（共享页面已有，本轮不改）；Tasks 托盘已有 Sub-agents 分节。服务不进 Chat 页（不属于对话），只在列表与抽屉。
- 现状澄清：Chat 页的子代理卡片/抽屉在手机端代码里已接通（`subagents` 帧字段 + `subagentDrawer: true`），缺的是**列表层的嵌套入口**与**服务**。如果你在真机 Chat 里完全看不到子代理卡片，那是另一个 bug（我没法在无设备环境复现），需要你给一次具体会话。

## 五、桌面「＋」菜单与空白 Chat（item 7）

- 现状：Tab 条已有 `+ ▾` 分裂按钮（`new_tab_agent.go`：＋ 用上次种类，▾ 列种类）。你要的改动：**点＋直接出下拉**（Terminal / 各 Agent），不再有「＋直接开」和「▾」两个目标。
- 选 Terminal → 现有 `newProjectTab`。
- 选 Agent 且默认视图是 Chat → **立刻**切到一个「空白新会话」页（共享 Chat 页，空草稿，composer 常亮，和手机 `/compose` 同构）；用户写第一条消息 → 页面**立刻**把这条消息渲染为第一条（Working 态），同时后台跑 `app.SessionStarter`（`SessionSpec.Prompt` 已支持首条消息随启动送达）；pane 出现并绑定后，**原位**换成真实数据（用已有的 `EchoPending` 对账，不闪、不跳）。
- 选 Agent 且默认是 TUI → 保持现有行为（直接开 Tab 进终端）。
- 失败：消息留在草稿框并带原因（与手机 pending 页一致）；`PromptPending`（Claude 首次信任文件夹）→ 显示 Agent 的问题卡，文本保留。

## 六、常驻通知（item 1，已做）

前台/后台都有；点开进 App；`Needs you: N · Working: N · Idle: N` 随 tree 事件原位更新；Mac 不可达显示 `Can’t reach your Mac — retrying`。

## 进度（2026-11-02）

- P2 Background：完成（桌面侧栏服务行 + tree `background[]` + 手机 Home 折叠列表 + 子代理抽屉跳转）。
- P1：Host `GET /workspaces/{id}/files`、`/files/content`（`internal/remotehost/files_browse.go`，capability `files`，golden 已钉）+ 手机底栏 Agents · Changes · Files · Sessions（`(home)/(tabs)`）、项目头、Files 只读树/预览、Attach to chat（选 Agent 后以 `@path ` 作为草稿打开其 Chat）。未真机验证。

## 分阶段（每阶段独立可合并）

1. **P1 作用域外壳 + Files 只读**（Host：files 两路由 + golden；手机：导航壳 + 项目头 + Files）。
2. **P2 Background**（Host：tree `background[]` + golden；手机：列表嵌套 + 服务卡片 + 抽屉入口）。
3. **P3 Git G2 + G3**（暂存/取消暂存/提交 + Fetch/Pull/Push，带确认）。**已实现（2026-11-02，用户拍板 G3）**：Host `POST …/git/stage|unstage|commit|fetch|pull|push`（capability `gitWrite`，golden 已钉）+ 手机 Changes 行内暂存/取消暂存、Commit 面板、Pull/Push 确认；丢弃/重置/分支管理/冲突解决仍不下放。
4. **P4 设备**（Host：`instances` 扩展 + connect；手机：设备芯片面板）。
5. **桌面 ＋ 菜单 + 空白 Chat** 与上面并行，互不依赖。

## 验证

```sh
# 手机
pnpm typecheck && pnpm lint && pnpm test && pnpm check:fixtures && pnpm check:chat-page
# 桌面
GOTOOLCHAIN=auto go test ./internal/remotehost/... ./internal/nativeui/... && GOTOOLCHAIN=auto go tool mygo vet .
```
真机（vivo，见 `vivo-phone-install`）：冷启动→底栏四标签；通知文案随 Agent 状态变化；Files 打开一个 >1 MiB 与一个越界路径（应拒绝）；设备掉线时当前实例不变。

## Unknowns（明确延后，带 owner）

| 未知 | 为什么阻塞/不阻塞 | Owner |
|------|------------------|-------|
| 远程设备上的 Chat 是否可用 | 不阻塞 P1–P3；阻塞 P4 的承诺文案 | 我，P4 开工前用真机确认 |
| 设备桥在手机断线时的保活策略（Host 何时收桥） | 不阻塞；P4 默认「最后一部手机离开 5 分钟后收桥」 | 你确认默认值 |
| 服务 Stop | v1 不做，需单独决策（会杀进程） | 你 |

## 待决策（Phase 3 闸门）

1. 导航：A / B / C？
2. Git：停在只读，还是做到 G2（暂存+提交）？要不要 G3（Push/Pull）？
3. Files：只读 + `Attach to chat` 够不够，还是要编辑？
4. 设备：只连「桌面已登录/可免交互直连」的设备，需要 MFA 的让你回 Mac——可以吗？
5. 桌面＋：确认「点＋总是出菜单」取代现有分裂按钮；⌘T 仍直接用上次种类？

- P4 设备（2026-11-02）：完成。bootstrap.instances 带 `kind/label/status`，`POST /devices/{id}/connect`，设备实例上 Files/Git/Chat/附件/Continue 一律 409 `instance_remote`、launch 仅限已有 Workspace 的 Terminal；桥归桌面管（用户选 A）。真机验证：桌面 Manager 对已保存的 omarchy-wilson 走 checking→online→connected，Projection 读到远端 Workspace（/home/wilson/…），observe/peek 命令可建。手机端 UI 未上真机。
