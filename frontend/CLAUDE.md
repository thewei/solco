# frontend/
> L2 | 父级: /CLAUDE.md

Chat WebView 页（产品决策 2026-10-11）的唯一源码：一份嵌入式 SPA 同时服务桌面（`mygo build` 内嵌 `dist`，经 `mygo://localhost` 加载）与手机（`build:mobile` 产出单文件 `dist-mobile/mobile.html`，solco-mobile 经 `scripts/sync-chat-page.mjs` 生成 `src/generated/chat-page.ts` 快照）。页面只与自己的桥（`mygo-runtime`）对话；页面代码禁止出现按客户端分叉的分支。`src/` 变更未完成直至：`bun run build` + `bun run build:mobile` + solco-mobile `pnpm sync:chat-page` + `pnpm check:chat-page`。

成员清单
main.tsx: 桌面入口，createRoot 挂 `App`，引入 highlight.js github 主题、`nozoom` 与 `theme.css`（`vite.config.ts`，target safari15）
mobile.tsx: 手机入口（`vite.mobile.config.ts` + vite-plugin-singlefile），渲染同一个 `App`，引入 `nozoom`，零客户端分支
nozoom.ts: 双指缩放关闭（副作用导入）：gesture 事件与 Ctrl+wheel 的默认缩放被取消——页面是原生窗口里的 App UI，双指放大不原生；普通滚动不受影响
App.tsx: 页面根：桥接状态（`chat.*` / events 订阅）、`data-theme`/`data-contrast`/`data-prose` 写入 html、sticky-bottom 跟尾、会话绑定与开关（`bindLiveAgent` 等）
mygo.ts: `mygo generate` 生成的类型化桥客户端——唯一桥 API 面，禁止手改
turn.tsx: 转录行渲染器：用户气泡（提及 chips、折叠）、助手纯文本、run 折叠（thinking+工具）、工具行、changes 卡、脚注（时间/复制/重发）、`Markdown`→`CodeBlock`——fenced 块统一脸：SVG 以 `<img>` data URL 预览（Preview|Code），mermaid 经懒加载库渲染图（Diagram|Code，strict 模式，350ms 流式去抖，解析失败回落源码+一行 `mmerr` 提示，主题翻转经 MutationObserver 重绘），超 30 行折叠、复制取 `textContent`
turnlist.ts: 转录虚拟滚动（@tanstack/react-virtual）
composer-surface.tsx: Chat / Assistant 冷启动 / Group 共用的 React Composer Box、Toolbar、Textarea 与扩容规则；发送与模型等状态仍由各场景 controller 拥有，不复制 Herdr runtime
composer.tsx: 输入区：`/` `$` 目录弹层（agentcatalog 经桥）、模型/思考档面板、附件 chips、语音键
dock.tsx: 输入区上方卡片栈：Tasks tray、Question 卡、Blocked 卡、Working line
queue-card.tsx: 追问队列卡：计数徽标、拖拽/⌥↑⌥↓ 重排、单行撤回、Cancel all
subagents.tsx: 子代理卡片与 `SubagentContext`（嵌套只读事实）
drawer.tsx: 子代理只读抽屉（`chat:subagent-open` 事件驱动，宽度拖拽记忆）
toc.tsx: 左栏用户回合 tick-rail 目录
drafts.ts: 每会话草稿与所选附件的 localStorage 持久化（14 天清理，跨 WebView 回收存活）
context.tsx: composer 行的上下文用量环与提示
icons.tsx: 内联 Lucide 描边图标集（页面保持零图标依赖）
native.ts: Safari 式状态 pill（指针下链接 href），Chat 与 Files 预览共用
FilesViewer.tsx: `/files` 预览面的 Markdown/图片渲染（github-markdown-css，按 `data-theme` 换肤）
theme.css: 设计令牌唯一源：ink ramp（--i1..i4）、hair/tint、`--warning` 等，浅深两套 + 高对比变体
chat.css: Chat 本体样式（含 `.codeb` 代码块族、`.mmprev` mermaid 画布、`.mmerr` 解析错误行）
files-viewer.css: Files 预览面样式
assistants/Progress.tsx: Solco 可信 Task/Goal facts 与 Worker 汇总 WorkPulse（真实状态，不伪装 Agent 回复）
assistants/GroupChat.tsx: Group 的 @ 定向/无 @ 广播；Composer 外壳复用 composer-surface
assistants/AssistantsPage.tsx: `/assistants` 对话优先页（2026-11-04，designs/assistants-chat-first）：嵌入同一个 `App` 作 Coordinator 对话；未启动时同款壳 + 自带输入框与 agent 选择，首条消息经 `assistants.ensureChat` 拉起 Coordinator 并在 Chat 绑定后经 `chat.send` 恰好发送一次；Needs-You 条、⋯ 菜单（含 Archive Assistant 的二次确认）、More 抽屉（列表经 `assistants.drawer` 仅开着时读取）
assistants/assistants.css: 上述页面样式（沿用 theme.css 令牌）

[PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
