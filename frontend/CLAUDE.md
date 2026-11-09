# frontend/
> L2 | 父级: /CLAUDE.md

Chat WebView 页（产品决策 2026-10-11）的唯一源码：一份嵌入式 SPA 同时服务桌面（`mygo build` 内嵌 `dist`，经 `mygo://localhost` 加载）与手机（`build:mobile` 产出单文件 `dist-mobile/mobile.html`，solco-mobile 经 `scripts/sync-chat-page.mjs` 生成 `src/generated/chat-page.ts` 快照）。页面只与自己的桥（`mygo-runtime`）对话；页面代码禁止出现按客户端分叉的分支。`src/` 变更未完成直至：`bun run build` + `bun run build:mobile` + solco-mobile `pnpm sync:chat-page` + `pnpm check:chat-page`。

成员清单
main.tsx: 桌面入口，createRoot 按路径挂 `App`（/chat）、懒加载的 `AssistantsPage`（/assistants）或 `FeedbackPage`（/feedback）——Files 预览自 2026-11-08 起是原生的，这里不再有它的页面（`/files-viewer` 路由、`FilesViewer`、github-markdown-css 都已删）；引入 highlight.js github 主题、`nozoom` 与 `theme.css`（`vite.config.ts`，target safari15）
mobile.tsx: 手机入口（`vite.mobile.config.ts` + vite-plugin-singlefile），默认渲染同一个 `App`，宿主在脚本前注入 `__SOLCO_ROUTE__="feedback"` 时渲染 `FeedbackPage`（与桌面按 pathname 选页同理，页面代码本身零客户端分支），引入 `nozoom`
index.html / mobile.html: 两个 HTML 壳的首帧：内联 `--content` 同一对色（`light-dark(#fcfcfc, #0a0a0a)`，与 `theme.css`、Go `webviewBackground`、原生 `designTokens().Content` 一对，`TestWebviewBackgroundMatchesPageTokens` 钉住）；`mobile.html` 的 CSS 适配当前主题，由 `protocol.ts` 在首屏注入 `themeSeed` 对齐浅/深色消除闪烁
nozoom.ts: 双指缩放关闭（副作用导入）：gesture 事件与 Ctrl+wheel 的默认缩放被取消——页面是原生窗口里的 App UI，双指放大不原生；普通滚动不受影响
App.tsx: 页面根：桥接状态（`chat.*` / events 订阅）、`chat:delta` 有序尾 Patch / 缺帧 snapshot 自愈、`data-theme`/`data-contrast`/`data-prose` 写入 html、sticky-bottom 跟尾、TOC 大纲轨（`Boolean(state?.outline)` 默认关闭）、会话绑定与开关（`bindLiveAgent` 等）；输入区上方的 `.changes-pill`（「N files changed | Review」：宿主给 `state.changedFiles` 且 `canReview` 才画——手机从自己的 Git 状态读，桌面恒为 0 所以永不显示；Review 走 `reviewChanges("")`）
mygo.ts: `mygo generate` 生成的类型化桥客户端——唯一桥 API 面，禁止手改；SC-04 包含 chat:delta 的 ChatDelta DTO
turn.tsx: 转录行渲染器：用户气泡（提及 chips、折叠）、助手纯文本、run 折叠（thinking+工具）、工具行、changes 卡、脚注（时间/复制/重发）、Markdown（模块级静态插件引用与组件映射复用）→CodeBlock——fenced 块统一脸：SVG 以 `<img>` data URL 预览（Preview|Code），mermaid 经懒加载库渲染图（Diagram|Code，strict 模式，350ms 流式去抖，解析失败回落源码+一行 `mmerr` 提示，主题翻转经 MutationObserver 重绘），超 30 行折叠、复制取 `textContent`
turnlist.ts: 转录虚拟滚动（@tanstack/react-virtual；手机 15 行/overscan 3/估计高 120，桌面 40 行/overscan 8/估计高 160；未命中测量缓存时回退真实 DOM 坐标定位）
chat-delta.ts: 桌面/手机唯一的流式回合尾 Patch 合并器；baseSeq + conversationId 双重门控、缺帧返回 null 触发 Snapshot
测试：`tests/chat-delta.test.ts` 只测纯 reducer，`tests/connect-text.test.ts` 钉 Connect 文本；都未进入 WebView 产物
composer-surface.tsx: Chat / Assistant 冷启动 / Group 共用的 React Composer Box、Toolbar、Textarea 与扩容规则；发送与模型等状态仍由各场景 controller 拥有，不复制 Herdr runtime
composer.tsx: 输入区：`/` `$` 目录弹层（agentcatalog 经桥）、模型/思考档面板、附件 chips、语音键
dock.tsx: 输入区上方卡片栈：Tasks tray、Question 卡、Blocked 卡、Working line（`.shim.working` 文字 + `.elapsed`；手机显示「Working · 4m 16s」，桌面保留省略号）；`SessionState`：加载态附带骨架 `.sk-rows`（仅手机样式显示，桌面仍是转圈行），等待态带 `draft` 标记（新会话草稿由宿主自己的 hero 邀请，手机样式隐藏页内邀请）
queue-card.tsx: 追问队列卡：计数徽标、拖拽/⌥↑⌥↓ 重排、单行撤回、Cancel all
subagents.tsx: 子代理卡片与 `SubagentContext`（嵌套只读事实）
drawer.tsx: 子代理只读抽屉（`chat:subagent-open` 事件驱动，宽度拖拽记忆）
toc.tsx: 左栏用户回合 tick-rail 目录
drafts.ts: 每会话草稿与所选附件的 localStorage 持久化（14 天清理，跨 WebView 回收存活）
context.tsx: composer 行的上下文用量环与提示
icons.tsx: 图标渲染器 `Icon`：路径表来自生成文件 `icons.generated.ts`（`design/icons.json`，Tabler 描边；默认线宽取 `--icon-stroke`：指针 1.5、触控 2）；页面保持零图标依赖；`StopIcon` 是画出来的方块，不是图标
icons.generated.ts / tokens.generated.css: `design/gen.mjs` 生成，禁止手改：颜色角色、圆角档（`--r-*`，`@media (pointer: coarse)` 切到触控档）、`--ff-ui`（桌面系统字体、触控 DM Sans）、`--icon-stroke`
native.ts: Safari 式状态 pill（指针下链接 href）与选区辅助，Chat 页使用（Files 预览页 2026-11-08 退役）
theme.css: 引入 `tokens.generated.css`（颜色角色/圆角/字体/线宽的唯一来源），自己只保留令牌表没有名字的值（`--accent-dim`、`--float-shadow`、骨架色、墨色阶 `--i1..i4`/`--hair*`、`--spring`）+ DM Sans `@font-face`（`fonts/`，拉丁子集）；`--content` 与 Go `webviewBackground` 的一致性由 `TestWebviewBackgroundMatchesPageTokens` 钉住
fonts/: DM Sans 400/500/700 woff2（SIL OFL 1.1，附 `DM-Sans-OFL.txt`）；单文件手机构建内联为 data URL，桌面构建作资产
chat.css: Chat 本体样式（含 `.codeb` 代码块族、`.mmprev` mermaid 画布、`.mmerr` 解析错误行）；`.chat-log` 具 `-webkit-overflow-scrolling: touch` 惯性滚动与 `overscroll-behavior-y: contain`；.older 具触摸目标尺寸（手机 38px/带 .spin 转圈）；文件末尾的「Phone density」块以 `html[data-host="phone"]` 为键（手机 16/24 阅读尺寸、44px 触控、浮层/停靠卡的 T3 外形；桌面保持 13px 密度，只共享颜色/圆角/字体），另含 `.changes-pill`、加载骨架 `.sk-rows`、手机 `.jump`（44px，让位于输入区与停靠卡）
assistants/Progress.tsx: Worker 汇总 WorkPulse 与 `ago`（真实状态，不伪装 Agent 回复；2026-11-07 起页头不再画 facts 条，PlanCard/Milestone 与 `.as-plan`/`.as-ms`/`.as-fact-kind` 样式已删，Goal 计划只在 Advanced 的 Goals 里）
assistants/ResultCards.tsx: 聊天上方的任务结果卡（2026-11-07，确定性，不经模型）：桥 `snapshot.results`（核心 `assistant results`，24 h 内 done/failed/review_required 的任务，最多 4 张）→ 标题 + 状态 + Open Pane（仅当窗格仍在树中）/ Review（交给 Tasks）；收件箱已在问的任务不重复画，叉掉只在本页会话内记住；经 `App` 的 `dockAbove` 画在 composer 栏最上方
assistants/GroupChat.tsx: Group 的 @ 定向/无 @ 广播；Composer 外壳复用 composer-surface；`GroupsSheet`（2026-11-07：群不再是侧栏入口，由 Primary 的 ⋯ → Groups… 列出/新建，群页头有 ‹ 回 Primary）
assistants/AssistantsPage.tsx: `/assistants` 对话优先页（2026-11-04，designs/assistants-chat-first）：嵌入同一个 `App` 作 Coordinator 对话；未启动时同款壳 + 自带输入框与 agent 选择，首条消息经 `assistants.ensureChat` 拉起 Coordinator 并在 Chat 绑定后经 `chat.send` 恰好发送一次；Needs-You 条、⋯ 菜单（含 Archive Assistant 的二次确认）、More 抽屉（列表经 `assistants.drawer` 仅开着时读取）
assistants/Behavior.tsx / WorkStyle.tsx: Settings 页签的两节——Behavior 只剩一个「Carry on by itself」开关 + Quiet hours（提醒间隔/日预算保留原值），WorkStyle 的 MCP 收进「advanced」折叠；更多抽屉只有 Needs you / Tasks（Open·Watching·Routines·Done 四个筛选）/ Memory / Settings 四个页签（2026-11-07，Autonomy 页签已删，核心能力仍在）
feedback/FeedbackPage.tsx: `/feedback` 收件箱（项目筛选是下拉菜单 `ProjectFilter`——项目可能很多，超 8 个出搜索框，按「等待多者在前」排序，不再用 chips）（2026-11-09，designs/feedback-inbox 方向 A）：左线程列表（Needs you / All、项目 chips、按天分组）+ 右时间线（Agent 提问卡——提问说明与问题文本经 Chat 的 `Markdown`/`.prose` 渲染，标题、列表、加粗、代码块、表格同 Chat；你的回答气泡、静默间隔、迟到回答表单）；经 `feedback.*` 桥每 1.5 s 轮询（页面不可见不读）；落点（通知/深链/⌘K）由快照的 `focus` 一次性交付；窄屏（≤700px，手机复用同一页）列表→线程两屏；草稿只存页面内存；手机键盘升起后（`resize` + 聚焦后 350 ms）把整张回答表单（含 Send）滚回键盘之上，宿主侧用 `KeyboardInset` 缩 WebView（solco-mobile `app/feedback.tsx`）；1-9 选项、⌘↵ 发送只作用于最新等待表单；回答框随文字长高（桌面上限 240px、手机 40vh，超出内部滚动）；列表有搜索（标题/问题/回答，`feedback.Snapshot(selected, query)`）、末句预览、已处理线程变淡、`⋯` 菜单进入多选批量删除 / `Tidy`（清理 N 天前 + 保留期）；线程顶部 Task 卡（Agent 给的原始任务）与首末活动 / 轮数 / 客户端版本，每条消息一键复制，每个 Ask 的等待/回答时长，长线程的「回到最新」按钮；管理控件只在 `canManage` 时出现（桌面恒真，手机取 Host `feedbackManage`）；回答框有 File / Skill 两个按钮（`ContextPicker.tsx`：防抖搜索 `feedback.pickContext`，勾选成 chips，发送时作为 `context` 交给 Agent，只传路径；只在 `canPick` 时出现，手机取 Host `feedbackContext`）
feedback/connect-text.ts: 「Connect an Agent」发出的全部文本的唯一来源（纯函数，`tests/connect-text.test.ts` 钉措辞）：各客户端的注册方式 + 给 Agent 的 Prompt；核心要求是**堵塞式长连接**——一次 interactive_feedback 调用保持打开直到用户回答（用户可能要几分钟到几小时才看到）、不轮询、不缩短 timeout_seconds、不拿假设当答案；Codex 的 tool_timeout_sec 默认 60 s 会截断每个提问，所以 Codex 配置固定写 `tool_timeout_sec = 86400`；客户端仍截断时退回 interactive_feedback_start + 反复 interactive_feedback_wait（wait_seconds 低于其单次上限）
feedback/Tidy.tsx: 「Tidy」对话框（2026-11-09）：清理 N 天前已结束线程（先 dryRun 数出条数再二次确认）与自动保留期（forever/30/90/365 天），经 `feedback.cleanup` / `feedback.setRetention`；管理只在 `canManage` 时可达
feedback/Connect.tsx: 「Connect an Agent」对话框（2026-11-09）：把随 App 的 `solco-hostd feedback-mcp`（桥 `mcpBin`，非 bundle 的开发运行用标准安装路径）按客户端格式化——一段让任意 Agent 自行注册并改用 interactive_feedback 提问的 Prompt、Claude Code / Codex 命令、Cursor/Claude Desktop 的 `mcpServers` JSON——各带一键复制；仅桌面（手机没有 Mac 路径可给，`FeedbackPage` 对 `data-host="phone"` 不画入口）；列表脚的入口与空状态按钮共用它
feedback/feedback.css: 上述页面样式（只用 tokens.generated.css 与 theme.css 的墨色阶；2026-11-09 起读起来同 Chat：`--col` 居中栏、Agent 的话是无卡片正文、回答是 tint 气泡、待答表单是 `.ask` 提问卡、字重 500；按钮重置用 `:where` 以免 `.fb button` 的特异性吃掉行内边距；手机密度键在 `html[data-host="phone"]`，不再用 pointer: coarse）
assistants/assistants.css: 上述页面样式（沿用 theme.css 令牌；`.as-scrim` z-index 30 高于 More 抽屉 10 与菜单 20/21，抽屉开着时弹窗也完整）

[PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
assistants/AssistantsPage.tsx（2026-11-05）：Coordinator 定期汇报由原 Herdr OMP 对话生成；UI 独立显示 queued/claimed/uncertain 交付状态，uncertain 需检查原 Terminal 并二次确认 skip/retry，exact throughSeq + requestId；`mygo.ts` 必须由 Go `mygo generate` 自动生成，移动端仍复用同一 Chat 组件。
