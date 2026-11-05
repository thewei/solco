---
name: solco-development
description: Use when developing, debugging, reviewing, packaging, or handing off the Solco macOS client (Go + MyGo Native UI) — the Herdr runtime/protocol boundary, native Terminal attach, pane layout projection, Agent conversation/history semantics, Git Workbench, or release validation; also for any Solco bug / 行为异常 that needs 复现、定位、修复或实机验证 (the evidence-driven fix loop below). Enforces Solco client identity, Herdr runtime authority, mature-API-first implementation, native-first presentation (one Chat WebView page shared with the phone), visible-only attachments, one canonical shell, bounded logging, and documentation truth alignment.
---

# Solco Development

Use this skill for engineering work in the Solco repository (Go 1.27.1 + MyGo Native UI; Herdr is the runtime authority). The Rust/GPUI implementation is not on this branch.

The skill controls process. Architectural truth lives in `docs/client-product-architecture.md`; do not duplicate or override it here.

One flow, two branches:

1. **正常改动**：实现 → `Verification` 门禁 → `Completion review`。
2. **bug / 行为异常 / 需要实机证明的修复**：进入文末 `Evidence loop（取证修复循环）`（第 0–4 步）——其第 4 步的全量 `go test` 就是同一道门禁，循环关闭后同样过 `Completion review`；进度文件在 `docs/debug/`，教训回写 `LESSONS.md`。

## Mandatory first reads

Before changing code:

1. read `AGENTS.md` and `CLAUDE.md`;
2. read `docs/client-product-architecture.md` (its §1 is the canonical Workspace/Project/Herdr domain language);
3. when the work touches the active migration contract, read `docs/mygo-native-execution-rules.md` and `docs/mygo-native-migration-roadmap.md`.

Then inspect:

```sh
git status --short --branch
git diff --stat
git diff --cached --stat
```

Preserve user/staged changes. Do not reset, stage, commit, or push unless the user explicitly asks.

## Product/runtime boundary

Keep the names and ownership distinct:

- **Solco** is the application, product, shell, package, bundle, release artifact, and user-facing brand.
- **Project** is the Solco product concept backed by one Herdr runtime workspace.
- **Herdr** is the actual backend/runtime. Genuine Herdr API, protocol, CLI, socket, `Workspace` runtime type, `workspace_id`, `workspace.*` methods, errors, and integration names stay Herdr.
- Never use a Herdr runtime workspace as though it were a Solco Workspace; map it through the Project projection boundary. Never rename a real Herdr backend concept merely to make a branding search cleaner. Never present the client itself as Herdr.

## Mature-semantics-first ladder

Before hand-writing a capability, search the owner ladder in this order:

1. Herdr API;
2. MyGo Native UI components and official plugins (Terminal/Ghostty included);
3. a proven compatible implementation pattern;
4. custom Solco code.

If custom code is necessary, record the concrete reason: missing API, ownership conflict, or measured performance limitation.

## Ownership router

- Herdr daemon bindings (discovery/bootstrap, socket RPC, events, direct terminal attach) → `internal/herdr`
- MyGo-native desktop presentation (shell, router, pages, theme/icons/components, dialogs) → `internal/nativeui`
- Agent product semantics (launch, conversation, handoff, usage) → `internal/agent`
- application service coordination → `internal/app`; client preferences → `internal/settings`
- read-only coding-agent history domain → `internal/history`
- Git review model (status/diff/commit) → `internal/gitworkbench`
- Right Panel Files tool → `internal/filesview`; local preview → `internal/preview`
- resident service monitoring → `internal/services`; command entry → `internal/commandcenter`; user scripts → `internal/scripts`
- diagnostics & log export → `internal/diagnostics`; bounded structured logging → `internal/applog`
- app entrypoint/window lifecycle → `main.go`

Do not put a capability in `main.go` merely because that is convenient.

## Herdr runtime rule

Herdr remains authoritative for workspaces, tabs, panes, layouts, terminal sessions, Agent runtime state, persistence, scrollback, and process lifecycle.

Do not introduce: a second Herdr runtime, a client-owned PTY/pane-layout model/runtime registry, external-terminal launchers for runtime continuation, or invented socket methods and event shapes. When a required runtime capability is absent, document the protocol gap and solve it at the Herdr boundary.

For Herdr socket/event work, inspect the live protocol schema before changing request shapes. Treat protocol compatibility as an explicit verified range (current baseline: Herdr 0.9.3 / protocol 22), not exact-version equality and not open-ended forward compatibility; a successful ping followed by an unsupported protocol is a compatibility error, not proof the service is unavailable. For nontrivial commands, verify request params, outer success discriminator, and nested result field against the schema, then add a schema-shaped contract test. Validate each subscription's required fields (global vs pane-scoped), synchronously validate the initial `events.subscribe` acknowledgement, and never let a failed event stream leave a startup overlay permanently active.

For nontrivial Herdr-owned runtime capabilities, verify all three applicable surfaces before designing client state: snapshot/query, controller/command, and incremental event. A bounded query result must not be mistaken for the runtime authority when Herdr exposes a controller plus correction event. For structural mutations, apply the authoritative result payload to the smallest local projection and fetch only genuinely missing metadata; reserve full snapshot refreshes for bootstrap, explicit manual refresh, or exceptional recovery.

`session.snapshot.layouts` is the Pane topology authority: native UI maps visible Pane rectangles into same-window MyGo Terminal elements running `herdr terminal attach <terminal_id>`. Automatic attach never uses `--takeover`; hidden Tabs keep no attachment fleet. Navigation never attaches a terminal or reimplements a focus chain as a side effect.

## Native UI rule

Chat UI is the official MyGo `ui.WebView` page over the single bound-service bridge (product decision 2026-10-11; the page talks only to its own bridge from the app content origin). History and every other surface stay Native UI until their own explicit task moves them. Still forbidden: private AppKit embedding, a localhost HTTP backend for desktop presentation, a second desktop presentation backend, and a second Chat page implementation.

`frontend/src` is the one Chat page source for BOTH clients: the phone (solco-mobile) embeds a generated snapshot, it never forks the page and the page gains no per-client branches. A `frontend/src` change is not done until `bun run build:mobile` (solco/frontend) + `pnpm sync:chat-page` (solco-mobile) have refreshed the snapshot; `pnpm check:chat-page` must pass afterwards. `solco-mobile/src/generated/chat-page.ts` is generated output — never hand-edit it, and never patch presentation only on one client.

Use MyGo native widgets before custom drawing; reuse `theme.go`, `icons.go`, and `components.go` before one-off page styling. One canonical shell: do not add alternate Sidebar implementations or duplicate row/navigation builders for the same shell state; improve the canonical path in place. Keep framework/platform code out of `internal/herdr` and ordinary domain packages.

Workspace primary-surface navigation has one owner pair in `internal/nativeui` (2026-11-03): `showPaneSurface(paneID)` for "go to this pane" and `landSelectedPane()` for "back to the workspace home" (header home tab, ⌘1 / Go-menu Workspace, palette Workspace, view-tab exits). They land the pane's remembered surface (`paneSurfaceMemo` via `panePrefersChat`) and NEVER rewrite it — `paneSurfaceMemo` is written only by explicit choices (card-header toggle, Open Terminal actions, `openChat` success/invitation). New navigation entry points must ride these owners; never hand-write a `panePrefersChat ? Chat : showViewTerminal` ternary and never call `showViewTerminal` right after `selectPane` (both shapes persisted a Chat-pinned pane on Terminal — 2026-11-02 flash and 2026-11-03 revert loops, `docs/debug/`).

## Logging and privacy

Application logs are structured and size-bounded (`internal/applog`, MyGo `PathLogs`). Never log terminal output, prompt/conversation bodies, credentials, environment dumps, or provider secrets. Log operation names, stable runtime IDs, errors, state counts, and timing needed for diagnosis.

## Verification

Normal gate:

```sh
GOTOOLCHAIN=auto go test ./...
GOTOOLCHAIN=auto go tool mygo build
git diff --check
git diff --cached --check
```

For packaging changes also: build the `.app`, verify `Solco.app` exists under `build/darwin-arm64/`, verify the executable runs, lint `Info.plist`, and perform available signing/structural verification. Packaging identity lives in `mygo.config.ts` (app + frontend keys, since the N19 chat WebView frontend).

Real-app UI acceptance (only-the-real-app questions: picker contents, multi-Pane input/resize, rendering) drives a real app run through the Computer Use tools with mandatory isolation — temporary `HOME` + the HOME-derived Herdr socket, never the user's live instance — and asserts Herdr/protocol ground truth over pixels alone, recording PASS/FAIL evidence per scenario. The Rust-era acceptance harness scripts were removed with the Rust implementation; rebuild the harness entrypoints against the Go app before the next acceptance session.

Bug investigations, behavior misidentification, and any fix needing on-device proof run the evidence loop below instead of an ad-hoc path. It maintains `docs/debug/` progress files and self-updating lessons in `LESSONS.md` beside this file.

## Evidence loop（取证修复循环）

一条「环境先行 → 实机取证 → 根因 → 修复 → 同环境复验 → 循环」的完整开发回路。
核心纪律：**没有 failing-before 的设备证据，不许动手改代码；没有 passing-after 的设备证据，不许宣称修复。**
静态读代码只能提出假设；假设必须靠可复现环境里的证据裁决。

### 与其他 skill 的分工（进入本循环时固定调用）

- **根因纪律** → `hunt`：假设必须能写成一句话（file:line + 条件），探针先于补丁。本循环的「取证」就是 hunt 的 repro/instrument 阶段在 Solco 里的落地。
- **需求/方案推演** → `think`：根因确认后，若修复方案存在真实取舍（≥2 条可行路线、或要动共享状态机/协议边界），先出 2–3 句 Lightweight 方案（改哪里、为什么、风险），拿用户批准再动手。不要在取证阶段偷跑方案。
- **验证检查** → `check`：修复落地、复验证据齐了之后，关闭循环前用 check 的门槛过一遍 diff（scope drift、sibling sweep、hard stops、验证声明必须有本 session 命令输出）。
- **实机驱动** → `cua-driver` + `solco-ui-audit` 的坑清单（坐标空间、独立弹层窗口、element token 一次性）。

### 进度文件（第一步就建，每步都回写）

每个调查任务建一个 `docs/debug/<YYYY-MM-DD>-<slug>.md`，**每次阶段切换、每条新证据、每轮循环结束都更新它**——上下文丢了它就是唯一真相：

```markdown
# <slug> 取证循环
- 状态: 取证中 | 根因已确认 | 修复中 | 复验中 | 已修复 | 已复验关闭
- 循环: 第 N 轮
## 现象（用户原话 + 具体操作路径）
## 环境（沙箱路径、构建产物路径、pid、trace 开关）
## 证据（append-only，带时间戳：trace 日志摘录、截图路径、CLI/协议真值）
## 当前假设（一句话，hunt 契约）
## 根因（确认后一句话 + file:line）
## 已试过（每条：假设 → 探针 → 结果）
## 下一步（具体的下一个动作）
```

回写时机硬规则：任何「读 log / 截图 / 跑探针 / 改代码 / 重建」动作完成后立即回写进度文件，再继续下一个动作。教训回写见「自更新」。

### 第 0 步：环境先行（先于一切代码分析深潜）

隔离沙箱 = 临时 HOME + 专用 Herdr socket/config + scratch 项目。**绝不把验收跑在用户的活实例上**（部署复验是例外，走 solco-install/ui-audit 的替换流程）。

```sh
SB=$HOME/.cache/solco-<slug>; rm -rf "$SB"
mkdir -p "$SB/home" "$SB/work" "$SB/logs"
git init -q "$SB/work/demo" && cd "$SB/work/demo"   # scratch 项目按需造内容
printf 'data_dir = "%s/.herdr-data"\n' "$SB/home/.herdr-data" > "$SB/herdr-config.toml"
GOTOOLCHAIN=auto go tool mygo build                  # 仓库根；产物 build/darwin-arm64/Solco.app
"$SB/app" &  # 即 build/darwin-arm64/Solco.app/Contents/MacOS/Solco 直接启动，env 见下
```

启动 env（全部必带）：

```
HOME=$SB/home  HERDR_BIN_PATH=$(command -v herdr)   # 本机 herdr；daemon 由 Solco EnsureRunning 拉起
日志: $SB/home/Library/Logs/Solco/solco.log（沙箱内的 PathLogs，不要去翻用户活实例的）
```

herdr 的 socket/config 都从 HOME 派生：`$SB/home/.config/herdr/herdr.sock`、`…/config.toml`。`HERDR_SOCKET_PATH` 只传给 Solco spawn 的子进程（terminal attach 等），对本机 CLI 不生效——CLI 驱动沙箱 daemon 一律用上面的 socket 路径（落点 internal/herdr/herdr.go `SocketPath`）。
并行会话可能在跑**同名沙箱**（同 bundle id `com.whstudio.solco.sandbox`）：`pgrep -f SolcoSandbox` 会拿到别人的 pid。按启动路径甄别自己的实例（`ps` 看二进制路径），`list_windows` 后先截图核对内容（workspace 名/cwd）再点击——2026-11-03 曾把点击打进并行会话的窗口。

Solco 自己会 EnsureRunning 拉起 `herdr server`（internal/herdr/herdr.go），不需要手工起 daemon。
UI 里用侧栏 Workspace 区「New Workspace from Folder…」把 scratch 目录开成 workspace。
收工清理：`kill <pid>`；沙箱目录可留作下轮复用（进度文件里记路径）。

### 第 0 步附：Assistants/插件类 Mac.app 隔离实机验收配方（2026-11-05 定型）

适用：要在真 Solco.app + 真 Herdr + 真 OMP 上验 Assistants/Automations 链路，又不能碰用户的数据。

1. **沙箱后端**：`go test -tags e2e -run TestAcceptanceHold`（`plugins/solco-automations/internal/personalagent/acceptance_hold_test.go`）起隔离 HOME + Herdr + OMP（本地 fixture 模型）+ 插件守护进程，写出 `env.sh`，保持到 stop 文件出现。沙箱 HOME 必须装齐用户环境里会有的东西：`herdr integration install omp` 和 `solco-chat-stream.ts`，否则 Herdr 报不出 `agent_session`，Chat 页会永远停在 "Working…"（那是沙箱缺口，不是产品缺口）。
2. **隔离 App**：用 git worktree，改 `mygo.config.ts` 的 name/identifier（例 SolcoAcc / com.whstudio.solco.acc）后 `go tool mygo build`——单实例锁键 = identifier+name，对副本重签会 SIGTRAP，必须重建。
3. **启动**：`launchctl submit -l <label> -o out -e err -- up.sh`（脚本里 `unset GOROOT`、显式 PATH、source env.sh、exec 二进制）。后台任务绝不依赖工具 shell 存活；停止用 `launchctl remove` + stop 文件。
4. **驱动**：Computer Use 以 `pid + target_id` 取目标（`computer_apps` 列出）。**不要用 bundle_id 解析**——会走 LaunchServices 以真实 HOME 启动一个新实例。
   - 原生控件（侧栏、菜单）：坐标点击可用。
   - WebView 内控件：坐标点击和 `computer_key` 都不可靠（窗口被遮挡/未成为 key 时静默丢事件）→ 用 AX：`computer_app_action press` 点按钮，`computer_set_value` 填输入框（先 `computer_app_state` 取 `snapshot_id`）。AX `set_value` 对中文文本不回写，验收文案用英文，或把 fixture 脚本键改成 `*` 兜底。
   - 每次改动后用 `sqlite3`/OMP 会话文件/CLI 取旁证，不要只信截图。
   - `computer_start_control` 报 "busy" 时先 `computer_release_control` 再重试；会话会自行失效，每段操作前先确认。
5. **持久 shell 陷阱**：在工具 shell 里 `source env.sh` 会把 `HOME`/`PATH` 永久改成沙箱的，之后的 `rm -rf "$HOME/..."`、`rg`/`ps` 都会错位。只在子 shell `( . env.sh; … )` 里用，收尾前 `export HOME=/Users/<user>` 并用绝对路径清理。
6. **清理清单**：`launchctl remove`、杀 App、删 `~/Library/Application Support/<Name>` 与 `~/Library/Logs/<Name>`（数据目录不随 HOME 走）、`/tmp/sw-*`、`git worktree remove --force`、`computer_release_control`。
7. **git**：别用 `git add -N .`（会改动索引）。新文件要提交时只 `git add <明确路径>` 再 `git commit -- <路径>`。

### 第 1 步：取证（failing-before）

在沙箱实例上按用户原话的路径复现，每类问题先加**定向 trace**再复现（hunt：行为/异步问题必须边取证边形成假设）：

- surface / pane / chat 归属类问题：`internal/nativeui/workspace.go` + `new_tab_agent.go` 的 `slog.Info("surface trace", …)` 探针点（showSurface / syncWorkspaceForSelection / reconcilePaneSurface / openWorkspaceChat / completeAgentOpen / startNewTab / agentSessionStarted），`grep "surface trace" $SB/home/Library/Logs/Solco/solco.log`。
- 终端 attach 抖动：log 的 `attach native terminal` + `native terminals synced`（`spawned/closed` 计数能读出 surface 离开/回到 Terminal 的次数）。
- 协议/运行时真值：`herdr --help`、`herdr session list --json`、`HERDR_SOCKET_PATH=$SB/home/.config/herdr/herdr.sock herdr …`——以 CLI/schema 为准，截图永不作为唯一证据。
- UI 证据：`cua-driver get_window_state`（截图+AX 树）；弹层菜单是独立 NSWindow，窗口截图看不见，走 `invoke_menu` 或 AX。快照/截图缓存不跨 CLI 调用（一次性 CLI 报 `screenshot_context_missing`/`stale_element_token`）——snapshot→click→verify 全序列放进一个持久 `cua-driver mcp` stdio 会话（python 子进程讲 JSON-RPC），AX 不可达元素用 `screenshot_frame` 像素坐标兜底。

取证完成的判据：能写出「在沙箱里执行 X，观察 Y，与预期 Z 相符/不符」。Y 必须是日志行、截图或 CLI 输出，写进进度文件。**问题复现不了就停下报告，不进入修复。**

### 第 2 步：根因（hunt 契约）

一句话假设：「根因是 X，因为 file:line 的 Y 条件」。假设必须覆盖**全部**观察到的症状（含用户顺带提的），覆盖不了就是症状级猜测。探针证伪立即整体丢弃假设，不许叠补丁。三个假设连续失败 → hunt Handoff 格式向用户交回。

### 第 3 步：修复

- 方案有取舍 → 先 `think` Lightweight（2–3 句：改哪、为何、风险），等批准。
- 最小改动、单一来源（同一动作家族一个实现）；不引入第二运行时/第二 Herdr 方法/新 store（AGENTS.md 边界）。
- 行为修复带回归测试（先在未修代码上跑红，再在修复上跑绿；测试进 `internal/nativeui/*_test.go` 既有风格）。
- 修复后**立即删掉临时 trace 日志**；要留的诊断必须 applog 结构化且不含终端字节/会话内容。

### 第 4 步：同环境复验（passing-after）

1. `GOTOOLCHAIN=auto go test ./...`（全绿才继续）。
2. `go tool mygo build` 重建 → **同一个沙箱**重跑第 1 步的同一操作序列。
3. 证据对照表写进进度文件：failing-before 证据 vs passing-after 证据（截图 + trace 日志行）。
4. 关闭前跑 `check`：diff 是否 on-target、sibling sweep（同型位置 grep 一遍）、hard stops、验证声明逐条对得上本 session 输出。
5. 用户活实例上的部署复验（需要时）走 solco-install / solco-ui-audit 的「替换二进制 → graceful quit → open」流程。

判定矛盾（复验仍闪/新症状）→ 回第 1 步开新一轮循环，进度文件循环数 +1，不许在同一假设上加第三次补丁。

### 自更新（每轮循环结束必须回写）

**每轮循环结束（无论成败）更新两处：**

1. `LESSONS.md`（本 skill 目录内）追加一条：`- <日期> <问题一句话> → <教训一句话>（落点 file:line 或流程节）`。 qualify：踩到的坑、推翻的假设、关键架构事实、工具/坐标/协议 quirk。不 qualify：一次性路径、任务进度（那属于进度文件）。重读时合并重复条目，超过 40 条时把最陈旧且已被流程条款吸收的删除。
2. 进度文件的状态/循环/下一步。

需要升级本节本体（流程缺一阶段、环境配方变了、herdr 版本/协议变了）时直接改，改完在 LESSONS.md 记一条「skill 升级原因」。

### 硬规则

- 用户未提交的改动（`git status` 里的他人/并行批次文件）只读不碰，不 `git add -A`/`commit -a`。
- 修复验证永不 kill 用户实例；沙箱实例的 pid 记在进度文件里。
- 「已修复」三个字出现时，进度文件必须已有 passing-after 设备证据，否则不许说。

## Completion review

Before handoff or commit:

- [ ] intended diff only; no user changes reset/staged accidentally;
- [ ] Solco is the client identity in product surfaces; Herdr remains factually named at the backend boundary;
- [ ] one canonical shell path remains; no second runtime/PTY/layout owner introduced;
- [ ] all static gates pass; relevant runtime smoke done; manual-only acceptance explicitly listed;
- [ ] canonical architecture matches code; required third-party notices preserved;
- [ ] no commit unless explicitly requested.
