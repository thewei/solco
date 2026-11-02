---
name: solco-evidence-loop
description: Solco 取证驱动的问题修复循环：环境先行 → 实机取证 → 根因 → 修复 → 同环境复验 → 循环；带自更新 LESSONS.md 与 docs/debug/ 进度文件。任何 Solco 的 bug / 行为异常需要复现、定位、修复或实机验证时必须先读本 skill，修复未完成期间每轮循环都要回写进度与教训。
---

# Solco 取证循环（Evidence Loop）

一条「环境先行 → 实机取证 → 根因 → 修复 → 同环境复验 → 循环」的完整开发回路。
核心纪律：**没有 failing-before 的设备证据，不许动手改代码；没有 passing-after 的设备证据，不许宣称修复。**
静态读代码只能提出假设；假设必须靠可复现环境里的证据裁决。

## 与其他 skill 的分工（进入本循环时固定调用）

- **根因纪律** → `hunt`：假设必须能写成一句话（file:line + 条件），探针先于补丁。本 skill 的「取证」就是 hunt 的 repro/instrument 阶段在 Solco 里的落地。
- **需求/方案推演** → `think`：根因确认后，若修复方案存在真实取舍（≥2 条可行路线、或要动共享状态机/协议边界），先出 2–3 句 Lightweight 方案（改哪里、为什么、风险），拿用户批准再动手。不要在取证阶段偷跑方案。
- **验证检查** → `check`：修复落地、复验证据齐了之后，关闭循环前用 check 的门槛过一遍 diff（scope drift、sibling sweep、hard stops、验证声明必须有本 session 命令输出）。
- **实机驱动** → `cua-driver` + `solco-ui-audit` 的坑清单（坐标空间、独立弹层窗口、element token 一次性）。

## 进度文件（第一步就建，每步都回写）

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

回写时机硬规则：任何「读 log / 截图 / 跑探针 / 改代码 / 重建」动作完成后立即回写进度文件，再继续下一个动作。教训回写见最后一节。

## 第 0 步：环境先行（先于一切代码分析深潜）

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
HOME=$SB/home  HERDR_SOCKET_PATH=$SB/herdr.sock  HERDR_CONFIG_PATH=$SB/herdr-config.toml
HERDR_BIN_PATH=$(command -v herdr)   # 本机 herdr（0.9.5 @ ~/.local/bin/herdr 已验证）
日志: $SB/home/Library/Logs/Solco/solco.log（沙箱内的 PathLogs，不要去翻用户活实例的）
```

Solco 自己会 EnsureRunning 拉起 `herdr server`（internal/herdr/herdr.go），不需要手工起 daemon。
UI 里用侧栏 Workspace 区「New Workspace from Folder…」把 scratch 目录开成 workspace。
收工清理：`kill <pid>`；沙箱目录可留作下轮复用（进度文件里记路径）。

## 第 1 步：取证（failing-before）

在沙箱实例上按用户原话的路径复现，每类问题先加**定向 trace**再复现（hunt：行为/异步问题必须边取证边形成假设）：

- surface / pane / chat 归属类问题：`internal/nativeui/workspace.go` + `new_tab_agent.go` 的 `slog.Info("surface trace", …)` 探针点（showSurface / syncWorkspaceForSelection / reconcilePaneSurface / openWorkspaceChat / completeAgentOpen / startNewTab / agentSessionStarted），`grep "surface trace" $SB/home/Library/Logs/Solco/solco.log`。
- 终端 attach 抖动：log 的 `attach native terminal` + `native terminals synced`（`spawned/closed` 计数能读出 surface 离开/回到 Terminal 的次数）。
- 协议/运行时真值：`herdr --help`、`herdr session list --json`、`HERDR_SOCKET_PATH=$SB/herdr.sock herdr …`——以 CLI/schema 为准，截图永不作为唯一证据。
- UI 证据：`cua-driver get_window_state`（截图+AX 树）；弹层菜单是独立 NSWindow，窗口截图看不见，走 `invoke_menu` 或 AX。

取证完成的判据：能写出「在沙箱里执行 X，观察 Y，与预期 Z 相符/不符」。Y 必须是日志行、截图或 CLI 输出，写进进度文件。**问题复现不了就停下报告，不进入修复。**

## 第 2 步：根因（hunt 契约）

一句话假设：「根因是 X，因为 file:line 的 Y 条件」。假设必须覆盖**全部**观察到的症状（含用户顺带提的），覆盖不了就是症状级猜测。探针证伪立即整体丢弃假设，不许叠补丁。三个假设连续失败 → hunt Handoff 格式向用户交回。

## 第 3 步：修复

- 方案有取舍 → 先 `think` Lightweight（2–3 句：改哪、为何、风险），等批准。
- 最小改动、单一来源（同一动作家族一个实现）；不引入第二运行时/第二 Herdr 方法/新 store（AGENTS.md 边界）。
- 行为修复带回归测试（先在未修代码上跑红，再在修复上跑绿；测试进 `internal/nativeui/*_test.go` 既有风格）。
- 修复后**立即删掉临时 trace 日志**；要留的诊断必须 applog 结构化且不含终端字节/会话内容。

## 第 4 步：同环境复验（passing-after）

1. `GOTOOLCHAIN=auto go test ./...`（全绿才继续）。
2. `go tool mygo build` 重建 → **同一个沙箱**重跑第 1 步的同一操作序列。
3. 证据对照表写进进度文件：failing-before 证据 vs passing-after 证据（截图 + trace 日志行）。
4. 关闭前跑 `check`：diff 是否 on-target、sibling sweep（同型位置 grep 一遍）、hard stops、验证声明逐条对得上本 session 输出。
5. 用户活实例上的部署复验（需要时）走 solco-install / solco-ui-audit 的「替换二进制 → graceful quit → open」流程。

判定矛盾（复验仍闪/新症状）→ 回第 1 步开新一轮循环，进度文件循环数 +1，不许在同一假设上加第三次补丁。

## 自更新（每轮循环结束必须回写）

**每轮循环结束（无论成败）更新两处：**

1. `LESSONS.md`（本 skill 目录内）追加一条：`- <日期> <问题一句话> → <教训一句话>（落点 file:line 或流程节）`。 qualify：踩到的坑、推翻的假设、关键架构事实、工具/坐标/协议 quirk。不 qualify：一次性路径、任务进度（那属于进度文件）。重读时合并重复条目，超过 40 条时把最陈旧且已被流程条款吸收的删除。
2. 进度文件的状态/循环/下一步。

需要升级 SKILL.md 本体（流程缺一阶段、环境配方变了、herdr 版本/协议变了）时直接改，改完在 LESSONS.md 记一条「skill 升级原因」。

## 硬规则

- 用户未提交的改动（`git status` 里的他人/并行批次文件）只读不碰，不 `git add -A`/`commit -a`。
- 修复验证永不 kill 用户实例；沙箱实例的 pid 记在进度文件里。
- 「已修复」三个字出现时，进度文件里必须已有 passing-after 设备证据，否则不许说。
