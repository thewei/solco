# Design Brief: Solco Mobile 外壳(新会话首页 · 侧栏 · Chat ⇄ Terminal)

> 工作流:`/think` + `/interactive-design-exploration`。预览:直接打开 `exploration.html`(`file://`,无服务器、无依赖)。
> 深链:`exploration.html#<A|B|C|D>/<scene>/<dark|light>/<match|black>/<line|keys>`,如 `#C/chat/dark/match/line`。
> 冒烟:`agent-browser open file://$PWD/exploration.html && agent-browser eval "$(cat smoke.js)"` → `PASS 84 checks`。
> 范围:**只设计手机外壳与 Chat⇄Terminal 的关系**。Host 连接/配对不在本轮(见「风险与未决」)。产品代码在选型前不动。

## Decision

| 字段 | 内容 |
|------|------|
| **Status** | `Approved 2026-10-31`(选型完成;产品代码**尚未授权**,见下) |
| **Chosen** | **C · 窥视条 + 终端层**:Chat 为 Agent 会话的主界面;Composer 顶部嵌一行终端实况(同一张卡),点开是整屏终端层,⌄ / 下拉收起;纯 Terminal 会话没有 Chat,直接整屏终端 |
| **组合说明** | 终端底色 = **同 Chat**(`--content`,ANSI 随主题);终端默认输入 = **整行 + 键行**;首页的 Agent / 项目芯片 = **Composer 上方一行**;侧栏 = **按 Workspace 分组 + 顶部摘要芯片**(与桌面同构) |
| **实现授权** | **否**。用户指定的下一步:先**另开一轮 `/think` 讨论 Go 版 Remote Host**(风险 1);Host 方案定稿前不改 `solco-mobile` 产品代码 |
| **未被选中(留档)** | A 分段头(推荐过,可作为 C 的退路:在 C 的层头加 `Chat \| Terminal` 分段即得);B 滑页(手势冲突与可发现性代价) |
| **选 C 带来的待办细则(实现前要补)** | ① 窥视条的取数:Host 需给「终端最后一条非空行」的**只读投影**(不是转发 ANSI,不新增 Herdr 方法,风险 2/3 一并看);② Agent 在 `blocked` 且无法结构化时窥视条变琥珀并等价于桌面 Blocked 卡的 `Open Terminal`;③ ~~终端层打开时的 resize 提示(风险 2)~~ — 2026-10-31 Remote Host 探针:手机走 `terminal session observe`,栅格独立、不重排 Mac 窗格,不再需要该提示;但键盘弹起/收起会换栅格(重启 observe),层内要处理一次整屏重绘;④ 层头去掉下拉手势与 ⌄ 之外的冗余入口,避免与终端内滚动冲突 |

---

## Problem

同一个 Agent 会话里,**Chat 和 Terminal 在手机上怎么切、怎么融为一体**。首页 / 侧栏 / 纯终端三块用户已定,三个方案共用。

### 用户已定(不再探索)

1. **首页 = 新会话空态**(参照 DeepSeek 截图):左上 `≡` 开侧栏;居中问候;底部 Composer 卡。「深度思考 / 智能搜索」两个芯片换成 **`Agent` / `Terminal`** 模式芯片;输入框参照桌面 Chat Composer(24 圆角卡、`+`、单色圆形主按钮)。
2. **侧栏**与桌面侧栏同构(按 Workspace 分组、状态槽在行尾、底部设备芯片),推屏式打开(同 DeepSeek 第二张截图)。
3. 点 **Agent** → 默认进 Chat View,**可切回 Terminal**;点 **Terminal** → 整屏终端。
4. 多考虑手机特性(屏幕高度、输入法)与 **Terminal 和会话的融合(背景、过渡)**。

## Phase 0 — 项目调研摘要

### 现状(读过的代码与文档)

| 项 | 发现 |
|----|------|
| 现有手机端 | `solco-mobile`(Expo 54 / RN 0.81 / Expo Router / Tamagui 2 / Gorhom BottomSheet 5);首页是 **Agent 看板**(Needs You / Working / Done / Idle 分区卡),`new-agent.tsx` 是三步表单(Kind → Pane → Launch),`tui.tsx` 是**全局共享**的 Herdr TUI 页 |
| 已验证的手机终端资产 | `docs/tui-virtual-keyboard-design.md`:四页虚拟键盘(QWERTY / ⌘ 导航+`^C` 预设 / 123 / #+)、一次性修饰键锁存、键走 WS 保序、点底部两行才拉起 IME、滚动惯性 |
| 桌面 Chat 语言 | `designs/chat-view/`:助手无容器正文、中性 18 圆泡、一行过程摘要、24 圆 Composer、ink 四级(`--i1..--i4`);dock 栈:Tasks / Question / Blocked(`Open Terminal`);状态槽 = `status_indicator.go` 单一槽 |
| 桌面令牌 | `frontend/src/theme.css`:`--content #f5f5f7/#161618`、`--panel #fff/#1f1f22`、`--r-bubble 18`、`--r-composer 24`、`--spring cubic-bezier(.32,.72,0,1)` |
| 桌面 Agent 范围 | Chat **只面向 Claude Code / Codex / pi / omp**(AGENTS.md);`solco-mobile/new-agent.tsx` 仍列 10 种 kind,是 Rust 时代遗留 |
| 手机端 UX 契约 | `docs/mobile-ui-ux.md`:Agent 优先、Workspace 导航放 sheet 不做常驻、深色优先、键盘/动态字体/慢网都要验 |
| **Host 现状** | Go 版 Solco **没有 Remote Host**:`solco-mobile` 的 `/api/v2` 契约、golden fixtures、`docs/remote-client-architecture.md` 全部指向已退役的 Rust Host(`shardlane-remote`)。grep `internal/` 与 `main.go` 零命中 |

### 约束

- **必须保留**:Herdr 是唯一运行时;手机端不碰 Herdr socket/CLI、不解析 ANSI/Provider 文件;Chat 的 Question dock / Blocked / Tasks 语义;手机导航是客户端本地状态;自动 attach 不用 `--takeover`。
- **必须对齐**:桌面令牌与 Chat A+B 语言(中性、无投影、hairline、accent 只给链接/焦点);单一状态槽(working=转圈、idle=空心环、其余=实心点、服务=圆角方块);触控目标 ≥ 42pt。
- **非目标**:Host 连接/配对/传输;平板分屏;Git/Files/Quota/Hosts 页面搬到手机;终端多 Pane 布局。
- **预计落点(定稿后)**:`solco-mobile/src/app/index.tsx`(Agent 看板 → 新会话首页)、`_layout.tsx`(抽屉)、`agent/[agentId].tsx`(Chat⇄Terminal)、`tui.tsx`/`components/native-terminal.tsx`/`tui-keyboard.tsx`(终端)、`components/conversation/composer.tsx`、`components/workspace-picker.tsx`(→ 首页选项目/Agent 的 sheet);`new-agent.tsx` 并入首页后退役。

## 方案(共用首页 / 侧栏 / 纯终端,只在 Agent 会话里的 Chat⇄Terminal 不同)

| ID | 名称 | 切换方式 | 终端顶栏代价 | 可发现性 | 手势冲突 | 说明 |
|----|------|---------|-------------|---------|---------|------|
| **A** | 分段头 | 头部居中 `Chat \| Terminal` 分段,一键切换(淡入淡出) | 54 安全区 + 46 | 高 | 无 | 最字面;状态与标题一直可见 |
| **B** | 滑页 | 两页横滑;终端页**零顶栏**;页点 + 右缘 `›_` 拉手 | 仅 54 安全区 | 低 | **有**:左缘=侧栏;终端内横滑=选文本 → 翻页只认键行/拉手;回侧栏靠键行最左的 `‹ Chat`(shell 为 `≡`) | 手势优先、最沉浸,代价是学习成本 |
| **C** | 窥视条 + 终端层 | Chat 为主;Composer 顶部嵌一行终端实况(同一张卡),点开 = 整屏层,⌄ 收起 | 54 + 44 | 中(仅 Agent 会话;需要你时变琥珀) | 无 | 与桌面 Blocked 卡的 `Open Terminal` 同构;纯 shell 没有 Chat,直接是整屏终端 |

### 实测:手机终端到底有多少行(`exploration.html` 终端左上角 `cols×rows`,按真实布局量出)

390×844、等宽 11.6px、51 列:

| 状态 | A | B | C |
|------|---|---|---|
| 整行 + 键行 · 无键盘 | 51×36 | 51×39 | 51×36 |
| 整行 + 键行 · **软键盘展开** | **51×19** | 51×22 | 51×19 |
| 仅键行 · 无键盘 | 51×39 | 51×42 | 51×40 |
| 仅键行 · **软键盘展开** | 51×22 | 51×25 | 51×22 |

结论:**软键盘(302px)才是最大的成本,其次是整行输入条(3 行),顶栏只差 3 行**。所以顶栏不是选型的决定因素;19 行对 Claude Code 这类 TUI 已经偏紧,这是「整行 vs 仅键行」做成设置项的依据。

### 融合设计(三个方案共用,可在 Controls 里对比)

| 手法 | 做法 | 在原型里看 |
|------|------|-----------|
| 同底色 | 终端底 = Chat 的 `--content`;ANSI 调色板随主题(浅色下用深墨色版);可选「固定黑」 | Controls → Terminal 底色 |
| 输入条变形 | Chat 的 Composer 与终端的整行输入**同位置、同圆角、同色**;终端多一排键行(`esc tab ctrl ↑↓←→ \| ~ /` + 键盘钮) | 切到 Terminal 看底部 |
| 整行输入 | 手机默认**整行输入再发送**(IME 友好:中文整句一次 `send_input`);点终端本体 = 直接键入;`⌨` 打开已验证的四页虚拟键盘 | Controls → Terminal 输入 |
| 状态连续 | 头部状态槽/标题跨 Chat、Terminal 保持;C 的窥视条随状态变琥珀 | ⑦ 提问态 |
| 提问双呈现 | 同一个提问在 Chat 是 Question 卡(点选),在 Terminal 是原始 TUI 菜单 | ⑦ ↔ ⑪ |

### 交互状态(原型内右栏 Scenes 全部可点)

| Scene | 预期 |
|-------|------|
| ① 首页 · Agent / ② Terminal 模式 | 模式芯片切换;Agent 模式多一个 Agent 芯片;Terminal 模式空输入时主按钮变 `Open` |
| ③ 输入中(键盘) | 问候上移,Composer 贴键盘;打字后主按钮由麦克风变发送 |
| ④ 选项目 / Agent | 底部弹层(实现对应 Gorhom Bottom Sheet) |
| ⑤ 侧栏 | 主层右推 304pt,行 = Agent/Terminal,状态槽在行尾,底部设备芯片 |
| ⑥ Chat 工作中 / ⑦ 提问 / ⑧ 输入中 | 桌面 Chat A+B 语言;Tasks 托盘、Question 卡可点 |
| ⑨ Terminal Agent 会话 / ⑩ 键盘+键行 / ⑪ 提问界面 | 三方案切换方式不同 |
| ⑫ 普通 shell | 整屏终端,头部只有 `≡` 与标题(B 无头) |
| ⑬ 断线重连 | 横幅 + Composer 置灰;设备芯片变琥珀 |

可真交互:`≡`/scrim、侧栏行、模式芯片、发送(Agent→新会话回显,Terminal→shell)、选项目/Agent、Question 选项、A 的分段、B 的拖动翻页/拉手、C 的窥视/收起、Esc 收起抽屉/弹层/键盘。

## 拍板记录(2026-10-31,已答)

1. Chat⇄Terminal 的切法 → **C**(原推荐 A;用户选 C)。
2. 终端底色 → **同 Chat**。
3. 终端默认输入 → **整行 + 键行**。
4. 首页 Agent / 项目芯片 → **Composer 上方一行**。
5. 侧栏分组 → **按 Workspace + 顶部摘要芯片**。
6. 下一步 → 写 Decision,并**另开一轮 `/think` 讨论 Go 版 Remote Host**。

## 风险与未决

| # | 问题 | 为什么重要 | 建议 |
|---|------|-----------|------|
| 1 | **Go Solco 没有 Remote Host**;`solco-mobile` 的 `/api/v2` 与 golden fixtures 属于已退役的 Rust Host | 「连接 Solco 客户端」是这条线的硬前置,不是 UX 能解决的 | 选型后单开 `/think`:Host 放哪(`internal/` 新包)、复用哪些现有域(`internal/conversation`、`internal/history`、桥的 Question/投递逻辑)、传输(LAN + Tailscale 先行)、配对 |
| 2 | 手机 attach 终端的 **resize/takeover 语义**:手机 51 列、Mac pane 往往 150+ 列;自动 attach 不得用 `--takeover` | 决定终端页是「手机自己的视口」还是「会把 Mac pane 缩窄」 | Host 设计时在 Herdr 边界验证;UI 上终端左上角已有 `cols×rows` 位置可承载提示 |
| 3 | Chat 在手机上是**复用桌面的 WebView 页面**还是 RN 原生重写 | 前者零漂移但与「phone-native」的既有契约冲突;后者要重做 Question/Tasks/TOC/Outbox 语义 | 选型后和 Host 一起决定;本稿的 Chat 外观与两者都兼容 |
| 4 | 整行输入对**方向键菜单**(Claude 的 `/model` 选择器、omp Review)不友好 | 本稿默认值的最脆弱假设 | 键行常驻 `↑↓←→⏎`;提问在 Chat 里是点选卡,不依赖终端菜单;可切「仅键行」 |
| 5 | `solco-mobile` 的 Agent 列表仍是 10 种 | 与桌面「Chat 只面向 4 个 Agent」不一致 | 首页 Agent 芯片只列 Claude Code / Codex / pi / omp |

## Preview

| 文件 | 看什么 |
|------|--------|
| `exploration.html` | 右栏:Variants(A/B/C/D 并排)· Scenes(13 个)· Controls(主题 / 终端底色 / 终端输入 / 软键盘)· Story |
| `smoke.js` | 84 项断言:4 变体 × 13 场景渲染、主题/底色/输入形态、首页发送、抽屉、A 分段、B 拉手与行数、C 窥视条、提问回答、断线、弹层、深链 |

### 截图

| 状态 | 文件 |
|------|------|
| 首页 · Agent 模式 | `preview-home.png` |
| 首页 · 选项目 / Agent 弹层 | `preview-pick.png` |
| 侧栏 | `preview-drawer.png` |
| Chat · 需要你(浅色) | `preview-ask-light.png` |
| 并排 · Chat(深) | `preview-compare-chat.png` |
| 并排 · 终端 + 键盘(深,看行数) | `preview-compare-term-kb.png` |
| 并排 · 终端(浅,同 Chat 底色) | `preview-compare-light.png` |
| 终端「固定黑」在浅色主题下 | `preview-term-light-black-kb.png` |

## Success criteria(定稿前)

- [x] 原型内可切换全部方案 / 状态(`smoke.js` 84/84)
- [x] 默认可在 `file://` 下打开(无控制台异常,smoke 驱动全流程)
- [x] 视觉与项目令牌一致:色值/圆角/ink 阶取自 `frontend/src/theme.css`;Chat 语言取自 `designs/chat-view`;状态槽取自 `status_indicator.go`;键行取自 `tui-virtual-keyboard-design.md`
- [x] 机制与实现一致:Chat 范围锁 4 个 Agent;Question 卡对应桌面 `PendingInteraction`;Blocked → `Open Terminal` 对应 C 的窥视条
- [x] 用户已在 `interactive_feedback` 中选定方案(2026-10-31:C + 同底色 + 整行输入 + 现稿芯片 + Workspace 分组)
- [x] Decision 表已更新

## Implementation handoff(Phase 4,定稿后)

| 项 | 内容 |
|----|------|
| 成熟 owner(先于自写) | 抽屉:`expo-router/drawer`(Expo Router 自带,基于 Reanimated;不直接 import React Navigation);弹层:Gorhom Bottom Sheet 5(已在用);B 的翻页:`react-native-pager-view` 或 Reanimated 手势(仅选 B 时评估);终端:沿用 `native-terminal.tsx`(WebView xterm)与 `tui-keyboard.tsx` |
| 改动文件 | 见「预计落点」;`new-agent.tsx` 退役;`src/theme/tokens.ts` 需对齐桌面令牌(现为 `#08090C` 蓝紫系,与桌面中性色不同)——单一真相源,先扫 inline override 再改 |
| 前置 | Go Remote Host(风险 1)。没有它,本稿只能落成对着 fixtures 的 UI,不能连真机 |
| 验证命令 | `pnpm typecheck && pnpm lint && pnpm test && pnpm doctor && pnpm export:web`(solco-mobile) |
| 真机检查 | 375 与大屏 iPhone 宽度、Android 手势导航、键盘开/关(IME 中文整句)、Dynamic Type、深浅色、慢网/断线、抽屉与 sheet 的键盘行为、长会话流式 |
| 与原型的已知差异 | 原型的会话/终端内容、设备状态均为模拟;终端行数按等宽 11.6px 估算,真机以 xterm 实测为准 |
