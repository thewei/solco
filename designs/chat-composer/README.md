# Chat · Composer 能力 / TOC 导航条 / ChatGPT 订阅 Voice

> 状态:**Explore 完成,等待 Select(见末尾 Decision)**。产品代码在选型前不动。
> 打开方式:直接双击 `exploration.html`(`file://` 可用)。深链:`exploration.html#<variant>/<state>/<agent>/<theme>`,如 `#C/slash/codex/light`。
> 范围锁定:Chat 只面向 Claude Code / Codex / pi / omp。

## Design Brief

单一决策点:Chat 的输入区怎样**在不复刻各家 TUI** 的前提下,覆盖附件、粘贴、Skill、原始命令、模型切换;并加一条 TOC 刻度条;再给 Voice 增加 "ChatGPT 订阅" 来源。

## Phase 0 现状(已核实)

| 项 | 现状 |
|---|---|
| 输入区 | `frontend/src/App.tsx` composer:`+` = 原生文件选择器 → 插入 `@path`;`@` 补全(`ListFiles`);听写(`Transcribe` → OpenAI 兼容端点);模型 chip **只读**(`state.model`);无 `/`、`$`、粘贴图、拖拽 |
| 投递 | Herdr `agent.prompt` / `agent.send_keys`,失败时 pane 级回退(`launch_transport.go`)。命令与 Skill 只是**文本**,由各家 TUI 自己解释 |
| Voice | `settings.Voice{EndpointURL,APIKey,Model}` → `ChatBridge.Transcribe`(multipart `/audio/transcriptions`) |
| Codex 额度 | `internal/usage/codex.go` 刻意走 `codex app-server`,**不读 auth 文件**(零凭据先例) |
| 禁令 | AGENTS.md:禁止 Bookmarks;读取别家登录态须先弹窗同意(Quota 页先例);令牌只在内存;不进日志;桥接只做展示 |

## 本机取证(2026-10-31)

- **ChatGPT 桌面端 = Codex Electron 应用**(`/Applications/ChatGPT.app`,bundle `com.openai.codex`)。解包 `app.asar` 读到听写实现:`POST {chatgpt backend-api}/transcribe`,multipart `file`(+ 可选 `language / dictation_session_id / attempt_id / duration_ms`),头 `Authorization: Bearer <access_token>` + `ChatGPT-Account-Id`,返回 `{text, asset_pointer, asset_ttl, asset_format}`。
- **实测**:用 `~/.codex/auth.json`(`auth_mode: chatgpt`)的令牌向 `https://chatgpt.com/backend-api/transcribe` 发 `say` 生成的 m4a → **200**,`"Selco voice test. One, two, three."`。服务端返回 `asset_ttl: "30d"`:**音频会被保留约 30 天**,必须写进同意弹窗。
- `codex app-server`(v2 schema,106 个方法)**没有**听写/转写方法;`thread/realtime/*` 是实时会话,不是听写。`account/read{refreshToken:true}` 可让 Codex 自己刷新令牌。
- 令牌 JWT `exp` 约 10 天;刷新令牌会**轮换**——Solco 若自己刷新会把 Codex 登出,所以 **Solco 只读、不刷新**,过期时让 `codex app-server` 刷新后重读文件。
- Skill 目录:Claude `~/.claude/skills`(125)、Codex `~/.agents/skills`+`~/.codex/skills`、pi `~/.pi/agent/skills`(93)、omp `~/.omp/agent/skills`;调用写法 `/name`、`$name`、`/skill:name`、`/skill:name`。
- 模型来源:Codex `~/.codex/models_cache.json`(含 `input_modalities`)、pi `pi --list-models`(含 images 列);Codex `/model` 是选择器(二进制串 *choose what model and reasoning effort to use*),**无参数形式**。
- 其余项(Claude/Codex 文本型命令清单、`/model <id>` 是否接受参数、图片路径投递)见探索稿 **M · 能力证据表**,黄色项必须真机转绿或砍掉。

## 约束

- 必须保留:Herdr 唯一运行时、桥接只做展示、`@path` 机制、Question dock 流程。
- 不做:Bookmarks(截图里的书签图标)、在页内复刻任何 TUI 选择器、后台轮询、令牌落盘、令牌刷新。
- 选择器型命令(`/model`、`/resume`、`/permissions`、`/settings`、`/tree`…)一律 **Open Terminal**。

## 方案

| | 入口 | 优点 | 代价 |
|---|---|---|---|
| A Type-first | 键入 `/`、`$` | 零新增控件 | 可发现性差 |
| B Menu-first | `+` 菜单 | 可发现 | 选 Skill 三次点击;手敲命令无反馈 |
| **C Both(推荐)** | 键入为引擎,`+` 为同一份列表的鼠标入口 | 两种习惯都覆盖 | 多一个菜单(无新逻辑) |

共同点:附件以 chips 展示(缩略图/文件),发送时展开为 `@path`;粘贴/拖入图片写入 `<userDataDir>/ChatAttachments/`(有界、自动清理);模型 chip 变弹层(Claude/pi/omp 发 `/model <id>`,待真机;Codex 走 Open Terminal)。

**T · TOC**:数据全部来自 `state.turns`(用户消息 + 紧随的助手回复首个标题/首条要点 + 用户消息里的 `@` 附件),零 Go 改动;点击只做页内滚动;窄窗隐藏;刻度过多时抽样。

**V · Voice**:Settings → Voice 增加 `Source: API endpoint | ChatGPT subscription`;选后者弹同意框(音频发往 chatgpt.com、约 30 天保留、令牌只在内存且不刷新、计入订阅限额);Test 录 3 秒验证。属于对**未公开接口**的使用,风险与 Antigravity 同类,需用户明确接受并在弹窗里如实写出。

## Phase 4 验证清单(选型后)

1. 真机四个 Agent × {粘贴图片, 拖入文件, `/` 文本命令, Skill 调用, 模型切换},黄项逐个转绿或砍掉。
2. `codex app-server account/read{refreshToken:true}` 刷新后 `auth.json` 的 `last_refresh` 更新且 Codex 仍已登录。
3. Voice:`go test`(httptest 模拟 `/transcribe`:200 / 401 / 429 / 令牌不进日志)、真机录音 → 文本。
4. TOC:40 轮长对话滚动跟随、窄窗隐藏、浅/深色。
5. `GOTOOLCHAIN=auto go test ./...`、`go tool mygo vet .`、`bun run typecheck`、`git diff --check`。

## Decision (2026-10-31)

**Chosen:** C · Both — typing `/` or `$` as the draft's single leading token opens one popover, and the `+` menu is the mouse entry to the same lists (Attach file or image… / Skills / Commands).

- **Command scope (the largest option):** Skills + the user's own prompt commands + a short hand-verified table of built-in commands per agent + picker commands read off the TUI and shown as a Question card. Picker cards exist only for the numbered list pickers verified on a real device — Claude Code `/model`, Codex `/model` and `/permissions` — plus whatever follow-up step they open (Codex's effort step, Claude's "Switch model?" confirmation, which carries no key hint). `/resume`, omp `/model` and every pi/omp list are not numbered lists: the entry sends the command and switches to Terminal.
- **Model switch v1:** the model chip is a popover. pi → typed `/model provider/id`; omp → typed `/switch provider/id` (omp's `/model <arg>` goes to the LLM as a prompt, verified); Claude Code and Codex → their picker as a card. Claude's card answers with `s` ("this session only"), because typed `/model x` and Enter both save the model as the user's default. The chip is disabled unless the agent is idle.
- **Attachments:** paste, drop and the file dialog all attach as chips; images are saved by the bridge to `~/Library/Caches/<app>/ChatAttachments/` (moved out of Application Support on 2026-10-31: a path with a space is not one `@token`) (14 days, 200 MB, oldest pruned, 32 MB per file) and every chip is sent as `@path`. Base64 is the page → Go transport (same as dictation); Go → agent is the file path, since base64 text inlined into a prompt is tokenised as text.
- **TOC rail:** the Codex-style tick rail (no bookmark button: Bookmarks stay forbidden).
- **Voice:** second source = the ChatGPT sign-in Codex holds (`POST chatgpt.com/backend-api/transcribe`), read-only, never refreshed by Solco, behind an explicit consent dialog in Settings → Voice; the user accepted the unofficial-interface and 30-day server-side retention risk.
- **Not built:** a Voice "Test" button (native settings cannot record); a Claude/Codex typed model list (Claude's picker is truer than a hard-coded alias list; Codex's `models_cache.json` is not what the routed picker shows); `/resume`-style pickers as cards.

### Verified on a real device (Solco 0.10.0, lab agents in `/tmp/chat-lab`)

| Check | Result |
|---|---|
| `+` menu, `/` list with Codex built-ins, picker kind labels | ✅ |
| Codex `/model` → card → pick → effort step as a second card | ✅ |
| pi chip → typed `/model` | ✅ footer "Model: deepseek-flash" |
| omp chip → typed `/switch` (Herdr foreground refusal → `pane.send_input`) | ✅ footer "Gemini 3.8 Flash" |
| Claude `/model` card → pick → `s` | ✅ "Set model to … for this session only" |
| Codex `$skill` through `agent.prompt` | ✅ loaded `tdd` |
| Claude slash-command records (`<command-name>` / `<local-command-stdout>`) | ✅ rendered as `/model haiku` + a muted output line |
| Paste image (⌘V) → chip → Return | ✅ Claude Code and omp read the file and answered (second round, below) |
| Voice transcription via the ChatGPT sign-in | ⚠ request shape live-tested earlier (200); the in-app mic path and the Codex-side renewal were not exercised |

## Round 2 (2026-10-31): slash experience, keyboard, how each agent reads text

User feedback: the `/` list did not behave as expected, everything must work from the keyboard, and Solco must follow each agent's own conventions because Solco only sends text.

### What each agent actually reads (probed on real sessions in `/tmp/chat-lab`)

| Thing | Claude Code | Codex | pi | omp |
|---|---|---|---|---|
| File / image | `@path` | `@path` | `@path` | `@path` |
| Path with spaces | `@"a b/c.png"` | same | same | same |
| Skill | `/name` | `$name` (also mid-sentence) | `/skill:name` | `/skill:name` |
| Built-in command | its own `/` popup | its own `/` popup | its own `/` popup | its own `/` popup |
| Ends with `@path` + Enter | **Enter swallowed** by the file picker; Herdr still says delivered | submits | submits | **Enter swallowed** |

Conclusions implemented:

- `herdr.Manager.PromptAgent` (the one door for chat, queue and launch prompts) appends a single space when the prompt's last token is an `@` mention (`submitText`); every other prompt is untouched. Go trims prompt text earlier, so the space cannot live in the page.
- Mentions of paths containing whitespace are quoted `@"…"`; chips and the TOC understand both forms. Attachments live in the user cache directory (no space).
- `internal/agentcatalog` carries each agent's full built-in command list. Text commands (compact, init) are sent as typed; numbered pickers (Claude/Codex `/model`, Codex `/permissions`) become Question cards; everything else (`/status`, `/resume`, omp `/model`, …) is sent as typed and hands over to Terminal. Hand-over commands leave no pending echo (it would hang on "Working…" because no transcript row consumes it).
- Keyboard: the `/` `$` popover (↑↓ ⏎ ⇥ Esc; a command without arguments runs on ⏎, otherwise ⏎ inserts it), the `+` menu and the model panel all take ↑↓ ⏎ Esc; the selected row has a visible accent rail. The Chinese IME's `、` (for `/`) and `¥` (for `$`) count as lead keys without rewriting the draft until a row is taken.

### Device checks (second round)

| Check | Result |
|---|---|
| `/` list with the agent's own commands, rail highlight, ↑↓⏎ | ✅ |
| `/status` runs and switches to Terminal, no stuck "Working…" | ✅ |
| `+` menu keyboard, model panel Esc | ✅ |
| ⌘V image → chip → Return, Claude Code | ✅ read the file, answered |
| ⌘V image → chip → Return, omp (Herdr foreground refusal → `pane.send_input`) | ✅ read the file, answered `PINEAPPLE` |
| `$` mid-sentence in Codex, IME `、`, model panel ↑↓⏎ beyond Esc | not exercised on the device |

Verify: `GOTOOLCHAIN=auto go test ./internal/herdr -run SubmitText`, `./internal/agentcatalog`, `./internal/nativeui -run 'Attach|TerminalCommand'`.

## Addendum 2026-11-01 — thinking effort in the model panel (user-approved: all providers, desktop + phone, side effects shown as a note)

Real-device matrix (pi 0.x, omp, Claude Code 2.1.308, Codex, agy 1.4.12; the user's own configs backed up and restored afterwards):

| Agent | How effort is set | Levels come from | Persistence | In the panel |
|---|---|---|---|---|
| pi | `/thinking <level>` (bare `/thinking ` lists them; an unknown level is refused with "Available levels: …") | pi's own `~/.pi/agent/models.json` `thinkingLevelMap` per model (null = unsupported; probed: glm-flash lists low/high/max); a reasoning model without a map lists off…high; `pi --list-models` marks thinking yes/no | session only (`settings.json` unchanged) | the current model's levels |
| omp | `/effort <level>` (bare lists `off auto` + the model's own, with `(current)`) | the `thinking` column of `omp models` (`-` = none) | session only (`config.yml` unchanged) | `off`, `auto` + the column |
| Claude Code | `/effort [low|medium|high|xhigh|max|auto|ultracode [on|off]]` (bare = its own dialog) | its own argument hint | **persists**: `settings.json` `modelSettings["<model>"].effortLevel` | six fixed levels + the note "Claude Code saves this as the model's default effort." |
| Codex | the second step of `/model` (no `/effort`) | the picker | the picker's | none of its own: the "Choose in the agent's picker…" row already walks model → effort as Question cards |
| agy | effort is part of the model name: `Gemini 3.8 Flash (High/Medium/Low)`; `(Thinking)` is a name, not a level | `agy models` | persists (existing model note) | the family is one model row (selects its first variant), the variants are its efforts |
| Cursor | effort is a model variant | — | — | disabled (cost; no model list) |

Mechanics: every effort is one exact prompt text (`Effort{Label, Send}` in `internal/agentcatalog`) that the page or the phone sends through the ordinary send path — no new bridge or Host method. `agentcatalog.Classify` lets `<verb><level>` through as text while a bare `/effort` still goes to Terminal. The current effort is not highlighted (no cheap reliable source; agy is the exception because it is in the model name). `max` is offered where the agent offers it: it is an effort level, not a Max-mode model. The phone wire gained `models[].efforts|pick`, `efforts`, `effortNote`, `modelNote` (golden `chat-catalog.json` in both repos), and agy's typed switch now reaches the phone because the note travels with it.

Verify: `GOTOOLCHAIN=auto go test ./internal/agentcatalog ./internal/remotehost`, `pnpm test` in solco-mobile; panel: open the model chip on a Claude Code / pi / omp / agy pane.
