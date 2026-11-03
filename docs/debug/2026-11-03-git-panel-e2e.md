# git-panel-e2e 取证循环

- 状态: 第 2 轮复验完成（②⑦⑬ 设备复验通过；⑤ 单测钉住；①⑨⑬ 已归档；新发现⑭⑮ 已修；④ 待用户决策）
- 循环: 第 2 轮
- 任务: Git 面板真机端到端测试；发现问题现场修复复验；产品视角评审。

## 现象（任务原话）

> Git 功能/Git 面板还没有完全进行端到端测试。使用 Computer use 完整测试，测试出问题后现场验证修复，覆盖所有功能；可创建临时仓库；从产品经理角度审视界面布局、菜单优化；小修改自行拿捏，大改动请示。

## 环境

- 沙箱: `$HOME/.cache/solco-git-e2e`（home/work/logs/app/remotes）
- 临时仓库: `…/work/demo`（main + feature/next 分叉 + origin bare + origin-clone）
- 沙箱 app: `$SB/app/SolcoGitE2E.app`（CFBundleIdentifier `com.whstudio.solco.gite2e` 绕单实例锁；可执行文件名仍是 `Solco`）
- 实例 pid: 18170（launcher 父进程是僵尸是正常现象——MyGo macOS 经 LaunchServices 重新拉起真实进程）
- herdr socket: `$SB/home/.config/herdr/herdr.sock`（herdr 配置默认优先于 HERDR_SOCKET_PATH env）
- 日志: `$SB/home/Library/Logs/Solco/solco.log`
- 驱动: cua-driver CLI。**跨调用共享快照必须用同一个 `session` 标签**；像素点击坐标系 = 窗口截图 px（1568 宽）；AX frame 是屏幕点。
- 快速连点脚本: `/tmp/fast_clicks.py`（单 MCP 进程内多次调用，绕 2s 激活防抖/双击间隔）

## 测试结果（PASS 清单）

- 侧栏五视图 Tabs、⌘2、⌘R（快捷键路径部分受合成键盘限制）
- Changes 视图: 混合状态渲染（M/A/R/D/U/!全部正确）、rename 卡"renamed without changes"、untracked 合成 diff、词级高亮
- 树↔diff 双向选中同步、目录折叠
- Split/Unified 渲染（刷新后）、行级 stage（AX 路径；见发现⑫）、Viewed 勾选
- Commit 流: composer 展开、modal（6 files · review verified、文件清单、AI gate 提示）、提交落地、Undo Last Commit（改动保持已暂存 ✓）
- Fetch（behind badge）→ Pull（ff 成功；分叉拒绝符合 --ff-only）→ Push（含 merge commit）→ Up to date 全链路
- Clean state 页: ahead/behind chips、Push N、unpushed 标记、composer 隐藏
- 两段式 checkout: 单击仅选中（+toast 提示）、双击切换、脏树确认弹窗（Cancel 安全默认）
- Sequencer: 冲突横幅（手动刷新后）、操作名跟随（Merge/Cherry-pick）、Continue 禁用→启用、Show conflict files 过滤、确认式 Abort、Continue 完成 merge
- Actions 菜单（AXPress 可开）: sequencer 期间 Stage/Unstage/Stash/Undo 正确禁用
- Commits 视图: 车道/merge 渲染/ref chips 联动/评审区/作者列/C 沙箱作者名确认非 bug
- Find: 菜单开栏、查询 "1 of 1" + 高亮
- Settings → Git 页: 规则编辑器、generator 组、测试请求入口渲染完整

## 发现（bug，已修 ①②⑤⑦）

- **② [HIGH] staged rename 提交拆两半**: UI 提交 R 条目只带新路径 pathspec，旧路径删除留在 index（设备证据: 85ccd11 只加 calc.go，`D pkg/math.go` 残留）。根因 `internal/gitworkbench/commit.go:164`（draft.Paths 无 OldPath）。修复: pathspec 按 snap.ByPath 扩展 OldPath。回归 `commit_rename_test.go`（旧代码红: `D base.txt` 残留，修复后绿）。
- **① Split 切换不即时重绘**: segmented 翻转 + `split_diff=true` 持久化，已构建卡片保持 unified，⌘R 后才变 split；反向 Unified 即时。代码接线对称（setDiffLayout→rebuildDiffRows→gdRowsDirty→gdDiffSurface:95 每帧检查），与设备现象矛盾 → 插桩中（trace: gd_surface.go "gd rebuild trace"）。
- **⑤ 失败 toast 泄漏 git 原文**: "Pull failed: git pull: exit status 128"。修复: 分叉场景换人话（"local and upstream have diverged — merge or rebase first"）。
- **⑦ 激活刷新不取 sequencer 状态**: 外部 CLI 制造冲突后，激活刷新带回了冲突文件（`!`）但无横幅；sequencer 只在 loadBranches 里读，激活刷新不调它。修复: `loadSequencer()`（专用 sequencerGen）挂入 refreshGitOnActivation。
- **⑨ 冲突下 Commit 按钮态**: 看似可用实则静默忽略。可能是 ⑦ 的下游（sequencerChecked 为假→blocked=false→按钮 enabled），修复 ⑦ 后复验按钮外观与点击路径。

## 发现（报告/请示，不改码）

- **④ [请示] 分叉无 UI 前进路径**: ahead+behind 时 Push 被 git 拒、Pull --ff-only 拒、remote-tracking 分支无 Merge 入口，只能开终端。主按钮此时仍显示 "Push 2 commits"（必然失败）。方案空间: (a) 分叉时主按钮改 Pull/合并引导 (b) remote 分支上下文加 Merge into Current (c) Pull 对话框给 merge 选项。
- **⑧ 工作区创建唯一入口是侧栏区头 hover "+"**: 空状态中心无任何创建引导；自动化合成 hover 打不到。PM: 建议空状态加显式按钮。
- **⑪ 行级 stage 选择条（Clear/Stage N Lines）不在 AX 树**: 辅助功能不可达。
- **⑫ "Stage N Lines" 按钮前台 HID 点击两次无效（视觉命中），AX/hit-test 路径可用**: 疑似真实鼠标也点不中（热区错位或行内 z-order），需人工鼠标确认。
- **⑬ Undo Last Commit 疑似双触发**: 一次确认后同时出现成功状态与失败 toast（"working tree is not clean"）。自动化可能双按，修复轮后单次干净复测。
- 右键上下文菜单/悬停动作: 合成输入不可达（右键、ctrl+click、move_cursor 均失败），相关功能（row Stage/Discard/Copy Hunk、Merge into Current、Delete Branch、Stash Pop/Drop、Tag Delete）由既有单测覆盖，真机人工复核待做。

## 测试假象（非 bug）

- **⑥ toast 不消失**: MyGo toast 4s 过期但窗口在后台时计时暂停——沙箱窗口全程后台所致，前台正常。

## 证据时间线（摘要）

- 10:09 沙箱启动、建 demo 工作区；10:25 commit 85ccd11（rename 拆半发现）；10:38 ef1cee1 补交残留；10:4x fetch/pull/push/分叉死局；10:5x 分支两段式切换；11:0x merge 冲突全流程 + cherry-pick abort；11:1x 行级 stage（AX 路径成功）+ Find + Undo。
- 全部有截图（/tmp/solco-win.png 系列）+ solco.log + git 仓库状态三重证据。

## 下一步

1. 全量测试绿 → build → 同沙箱复验: ②（rename UI 提交后 index 干净）、⑦（外部冲突→仅聚焦→横幅出现）、⑤（分叉 Pull toast 文案）、⑨（冲突下按钮禁用态）、⑬ 单次 Undo。
2. ① trace 复现 → 根因 → 修复 → 去 trace → 复验。
3. check 门槛 + PM 评审汇总 + ④ 请示。

## 第 2 轮（同日 11:45–11:56，新构建 → 同沙箱复验）

- 沙箱启动配方更正: `open --env` 不传 HOME（会连到用户真实 Herdr 会话，已立即退出、未做任何操作）；必须用 python `subprocess.Popen(..., env=沙箱env, start_new_session=True)` 直接起可执行文件（nohup & 会在 shell 调用返回时被收走）。截图以 AX 树/日志/仓库状态为准——窗口在副屏时 `get_window_state` 的图可落后数分钟（曾拿到旧会话的 toast 当成现状）。
- **② 复验通过**: UI 提交 staged rename（R app.go→calc_renamed.go，带内容改动）→ `git show --stat` = `app.go => calc_renamed.go`，工作树干净，无残留 `D`。
- **⑦ 复验通过**: 外部 CLI 制造 add/add 冲突 → 仅激活窗口，横幅 “Merge needs attention · 1 file with conflicts”，Continue Merge 禁用、Abort/Show conflict files 可用。
- **⑬ 关闭**: 一次 Confirm 只有一对 `git op start/done`（ok:true，改动保持已暂存）；先前的“双触发”是自动化双按/脏树拒绝（11:20 那条 git_refusal），不是代码问题。
- **⑤**: 逻辑由 `TestPullRefusalOnDivergedBranchSaysSo` 钉住；设备上 Pull 在分叉时确实失败（log ok:false），toast 画面因截图滞后未能取到，不再追。
- **① 关闭（无法复现）**: AX 路径点 Split 即时重绘（trace 有 rebuild，像素大变），headless 逐帧探针也即时；归为后台窗口帧暂停假象（同 ⑥）。trace/探针已删除。
- **⑭ [新, 已修] Split/Unified 偏好重启后丢失**: `settings.json` 里 `split_diff:true`，重启却回 Unified。根因 `NewShell` 在应用 `WithSettings` 之前就读了 `s.settings.Workbench.SplitDiff`（读到的是默认值）。修复: 读取移到选项应用之后（shell.go）。回归 `TestSavedDiffLayoutSurvivesRestart`（旧顺序红、新顺序绿，已变异验证）。
- **⑮ [新, 已修] 合并/变基进行中仍画 Commit 栏**: 横幅已给出 Continue/Abort，下方却是一个只能禁用的 Commit 栏（subject 输入框能打字但永远发不出去）。修复: sequencer 活动时不画快速提交栏（git_review_split.go）。
- 观察（不改码）: Actions 菜单含 “Split Layout” 与工具栏分段控件重复且不显示当前态；Commit 弹窗里 Amend 出现两处（弹窗右上 + 底栏）；`AXPress` 打不开底栏 Commit 按钮（像素点击可以）——AX 可达性小缺口。

## 第 3 轮（同日 12:0x–12:4x，用户批示 ④(b) + ⑧ + Push 复测）

- **⑯ [HIGH, 已修] 任何 git 失败的 toast 只有 "git push: exit status 1"**: 被拒/鉴权/网络/远端钩子的原因全在 `Error.Stderr` 里，`Error()` 优先打印 `err`，用户看不到为什么——这就是 “Push 不通过” 的体感。修复: `gitworkbench.Explain(err)`（explain.go，认 git 协议用语，远端钩子的 `remote:` 原话原样带出，不含 URL）+ nativeui `gitFailureText`。设备复验: 远端前进后 Push → “Push failed: the remote has commits you don't have — pull first, then push”；pre-receive 钩子拒绝 → “…the remote declined the push: pub-test is protected, open a PR”。
- **⑰ [已修] Push/Pull 不跟随 upstream**: 一律用 `git remote` 第一个远端 + 同名分支；upstream 在第二个远端或名字不同时会推错地方。修复: 有 upstream 时 `PushTo(remote, branch, upstream.Branch)` / `PullFFOnly(upstream.Remote, upstream.Branch)`。回归 `TestPushToFollowsTheUpstream`（真实仓库，fork/topic）。
- **⑱ [已修] 无 upstream 的干净分支没有发布入口**（只剩 Fetch）: 干净态加主按钮 “Publish Branch”（`push -u`）。设备复验: pub-test 分支 → Publish → origin 出现 pub-test，状态 “Up to date with origin/pub-test”。
- **④(b) 已做**: 分叉时头部 Push 置灰（tooltip 说明原因）、干净态主按钮换成 “Merge origin/main…”（走原 Merge 对话框）并附 “Diverged — merge or rebase before pushing”；远程分支右键新增 “Merge into Current…” / “Rebase Current Onto This…”（`resolveBranchRef` 同时认本地与 remote-tracking，仍校验 Git 列得出）。无法直接完成时弹说明窗（`git_notice.go`）: FF 被拒 → “Can't Fast-Forward”＋原因；merge 冲突 → “Merge Needs Your Help”＋冲突文件（横幅 Continue/Abort 照旧）；rebase 冲突 → 立即 `rebase --abort`，“Rebase Stopped on Conflicts”＋文件，仓库回到原状（Solco 没有 rebase continue/skip 流程，故不留半成品）。设备复验全部通过（含 rebase 成功后 ahead 1/behind 0、冲突后 HEAD 不动且无 rebase 目录）。
- **⑧ 已做**: 空工作区中心加 “New Workspace from Folder…”（回归 `TestEmptyWorkspaceCenterOffersNewWorkspace`；设备上未能构造空 Session，单测覆盖）。
- 未覆盖/留给用户: 手机端 Git 写（remote_git_write.go）仍用 DefaultRemote+CurrentBranch，未跟 upstream；HTTPS 无凭据/SSH 口令（GIT_TERMINAL_PROMPT=0）只会得到 “can't sign in … authenticate once in a Terminal” 的说明，Solco 不弹凭据框。

## 第 4 轮：对齐 Fork.app（同日 12:5x，用户要求"以 Fork 为原型，一定要对齐"）

方法: 沙箱里同一仓库 `demo` 在 Fork 与 Solco 并排开，逐个右键/侧栏取证（AX 树 + 截图），列差距 → 只补"小而明确"的，大件列清单请示。

已对齐（每项带单测；设备复验: 沙箱 Solco 对 demo 实操）:

| Fork | Solco 之前 | 现在 |
|---|---|---|
| 当前分支行右侧 `1↓1↑` | 只有 ✓ | `↑1 ↓1` 徽标（`upstreamBadge`，设备已见） |
| Stash 行显示留言 | `stash@{0}  On main: wip…` | 只显示留言（`stashTitle`），ref 进 tooltip |
| 本地分支菜单 Rebase / Rename / Pull / Push | 无 | `Rebase Current Onto This…`、`Rename Branch…`；当前分支有 upstream 时 `Pull (Fast-Forward Only)` / `Push`（分叉置灰）。设备复验 Rename: `feature/next`→`feature/next-renamed` 成功 |
| 远程分支 Check Out | 无 | `Check Out…`（无同名本地分支则建 tracking；脏树先确认） |
| Tag Push | 无 | `Push Tag`（推到 upstream 所在远端，否则默认远端）。设备复验: origin 出现 `refs/tags/v0.1` |
| Stash Apply（保留 stash） | 只有 Pop | `Apply Stash`。设备复验: 冲突时 toast “Stash Apply failed: local changes would be overwritten — commit or stash them first”，stash 仍在 |
| 提交右键 New Branch / New Tag | 只有 Cherry-pick/Revert | `New Branch Here…` / `New Tag Here…`（对话框目标带 repo root，仓库切换后提交被拒）。设备复验 Tag: 建在所点提交 `42d5d13` |
| 文件右键 Show in Finder | 无 | `Show in Finder`（已删除文件/提交审阅里不出现） |

故意不照搬: Fork 的"所有文件连续滚动 diff"（Solco 已是逐文件卡片，且是用户确认过的布局）。

需要你拍板（体量大）: 用户选定 → 第 5 轮实现（见下）；未选: Ignore（.gitignore）。第 5 轮后用户追加: Stash / 工作区 Save as Patch（已做，见表）。

验证: `go test -short ./...` 绿、`mygo vet .`、`git diff --check`；回归 `TestStashTitleDropsGitFraming` `TestUpstreamBadge` `TestRefRowMenusCarryTheForkActions` `TestRenameBranchDialogOpensForTheMenu` + gitworkbench `fork_parity_test.go`。

## 第 5 轮：Fork 大件对齐（用户选定 10 项，同日）

域层 `internal/gitworkbench/fork_more.go`（真实仓库测试 `fork_more_test.go`），编排 `internal/nativeui/git_fork_more.go`（测试 `git_fork_more_test.go`）。

| 项 | 实现 | 设备复验（沙箱 demo） |
|---|---|---|
| 远程管理 | Remotes 头右键 `Add Remote…`；远程根行 `Fetch / Edit Remote URL… / Rename Remote… / Remove Remote…`；名字 `^[A-Za-z0-9][A-Za-z0-9._-]*$`，URL 拒空/前导 `-`/控制字符/`ext::` | 增→改名→改 URL→删（确认框）均改了 `git remote -v`；新增远端显示 “not fetched” |
| 自动 fetch | Settings → Git → Auto-fetch（Off/5/15/30/60 min，默认 Off），只 fetch、静默、`time.AfterFunc` 重挂；在 loadBranches 里惰性启动 | 设置页可见；定时行为由单测覆盖 |
| 删除远程分支 | 远程分支菜单 `Delete Remote Branch…`（确认） | `pub-test` 从裸仓库消失 |
| Tracking 子菜单 | 本地分支 `Tracking ▸ origin/main … / Stop Tracking` | 设置/清除 `branch.*.remote/merge` 与 git config 一致 |
| Reset to Here | 提交菜单 `Reset Current Branch to Here ▸ Soft…/Mixed…/Hard…`；**Hard 仅干净工作树**（放宽原“无 reset --hard”包级禁令，用户已批准），toast 带旧 HEAD 短哈希 | Hard 后 HEAD 精确回到所点提交；脏树 Hard 被拒 |
| Compare to Local Changes | 提交菜单 → 以该提交为 DiffBase 的审阅（含返回按钮 `Back to Commit`） | 9 个文件对比，说明文案齐全 |
| Save as Patch | 提交菜单 → 存储面板，默认名 `<hash8>-<subject>.patch`（`format-patch`）；**同日追加**：Stash 行 `Save as Patch…`（`stash show -p --include-untracked --binary`）与页面 Actions 菜单 `Save Changes as Patch…`（暂存+未暂存+未跟踪，忽略文件除外；在临时 index 里 `read-tree HEAD` + `add -A` 再 `diff --cached`，仓库自己的 index/工作树不动，空仓库对空树） | 三种来源均写出合法 patch，未跟踪文件在内，`git status` 前后一致 |
| 文件历史 / Blame | 文件右键 `File History…` / `Blame…`（`log --follow` / `blame --porcelain`，5000 行上限） | 两张表正常显示；已删除文件的 Blame 不可用 |
| 交互式 rebase | 提交菜单 `Interactive Rebase Onto Here…`；pick/reword/squash/fixup/drop + 上下移；≤100 提交；含 merge 的范围拒绝；已发布提交给黄色警告；**冲突立即 abort 并列出冲突文件**（与普通 rebase 同策略）；脏树拒绝（“Working tree is not clean”） | Move Down 重排 → HEAD 序变化；含 merge 范围 toast；脏树弹 “Interactive Rebase Didn't Happen” |
| 子模块 | 侧栏 `Submodules` 区（有子模块才出现），行菜单 `Update Submodule / Show in Finder / Copy Path` | `submodule add` 后区块出现，菜单正确 |
| Commit and Push | 提交选项菜单 `Commit and Push…` → 提交面板按钮 “Commit N files and Push” | 分叉时先提交成功，Push 被拒 toast “Push failed: the remote has commits you don't have — pull first, then push”（不吞原因） |

踩坑记录: headless Tester 里**刚新增的行**要 `Frame → Advance(1s) → Frame` 才能命中（进入动效期间 hit-test 为空），此前 “trunk/当前分支右键无菜单” 同因，真机无此现象。

验证: `go test -short ./...` 绿、`mygo vet .`、`git diff --check`；上表“设备复验”全部在沙箱 Solco（pid 6724）对 demo 实操通过。

## 第 6 轮：Fork 第三批（用户选定 11 项，同日）

域层 `internal/gitworkbench/fork_more2.go`（真实仓库测试 `fork_more2_test.go`），编排 `internal/nativeui/git_fork_more2.go`（测试 `git_fork_more2_test.go`）。

| 项 | 实现 | 设备复验（沙箱 demo） |
|---|---|---|
| A1 Pull 选项 + Fetch All | 页面 Actions 菜单 `Fetch All Remotes` / `Fetch All and Prune` / `Pull and Merge` / `Pull and Rebase`（头部 Pull 仍是 FF-only）；rebase 需干净树，冲突立即 abort 并列文件（弹窗 “Rebase Stopped on Conflicts”）；merge 冲突停在半路，沿用顶部条幅 + 弹窗 “Merge Needs Your Help” | 分叉 3↑2↓ → Pull and Merge 生成合并提交；add/add 冲突时 Rebase 弹窗列出 `clash.txt`、HEAD 不变；Merge 停在冲突态 |
| A2 Squash merge | 合并对话框第三段 `Squash`；干净树才可，冲突 `reset --merge` 回滚；成功只留暂存，不提交 | `sq.txt` 暂存、HEAD 不动、无 MERGE_HEAD |
| A3 Checkout Commit / Restore File / Apply Patch | 提交与 Reflog 行 `Check Out This Commit…`（确认后 detached）；提交审阅里文件右键 `Restore File from This Commit…`（确认，只进工作区不暂存）；Actions `Apply Patch…`（`apply --check` 通过才应用，只改工作区，8 MiB 上限） | 检出后 `HEAD detached at e87e18b`；菜单项可见；Apply Patch 由域测覆盖（需系统文件对话框） |
| A4 Annotated tag + Delete Remote Tag | `New Tag…`/`New Tag Here…` 改为对话框（名称 + 可选说明，有说明即 annotated，经 stdin 传）；Tag 行 `Delete Tag from Remote…`（确认；远端=upstream 的远端，否则 origin） | `git cat-file -t v0.3-rc` = `tag`，说明在列表里；删远端后 `ls-remote` 无该 tag、本地仍在 |
| A5 Worktree | 行菜单 `Open as Workspace`（复用 `createWorkspaceAt`）/ `Remove Worktree…`（主工作树不出现；Git 拒绝脏树，不 force） | 加 `wt-side` 后菜单四项齐全，确认后目录与登记都消失 |
| B6 冲突逐文件解决 | 冲突文件右键 `Keep Current Branch's Version` / `Take Incoming Version` / `Mark as Resolved`（仍含冲突标记则拒绝）；一侧删除文件时取该侧 = `git rm` | 带标记 Mark as Resolved → toast “still contains conflict markers”；Take Incoming 后文件内容为 remote side 且已暂存 |
| B8 Reflog | Actions `Reflog…` 面板（300 条）；行右键 Check Out / New Branch Here / Reset ▸ / Copy Hash；点击跳到提交审阅 | 面板列出 20+ 条含 checkout/rebase/pull；行菜单齐全 |
| B9 子模块 | Actions `Add Submodule…`（地址 + 可选目录，目录默认取仓库名；联网）；行菜单 `Remove Submodule…`（`deinit -f` + `rm -f`，确认） | 对话框与目录推断正确；本机路径起初被 Git 的 protocol.file 策略拒绝（第 7 轮修复，见下） |
| B10 Stash | Stashes 区头右键 `Stash With Message…` / `Drop All Stashes…`（确认）；文件右键 `Stash File`（`stash push -u -- path`） | 只暂存点选文件、另一未跟踪文件留在工作区；Drop All 后 `stash list` 为空 |
| B11 托管站点链接 | 提交右键 `Copy Commit Link` / `Open on GitHub|GitLab|Bitbucket|Codeberg|<host>`（仅 web 远端，本地路径远端不出现；GitLab 系 `/-/commit/`、Bitbucket `/commits/`） | 本机 demo 远端为本地路径 → 两项不出现（符合设计）；URL 解析由表驱动单测覆盖 |
| B12 Skip hooks / Sign-off | 提交面板两个复选框（逐次，不持久）→ `--no-verify` / `--signoff` | 预置失败 pre-commit 钩子，勾选后提交成功且信息带 `Signed-off-by` |

约束沿用: 不做 force push、不做 `git clean`、不做 Ignore；`internal/gitworkbench` 仍是唯一 Git 执行者，无新 Herdr 方法，日志不含路径/Git 文本。

验证: `go test -short ./...` 绿、`mygo vet .`、`git diff --check`；上表“设备复验”在沙箱 Solco 对 demo 实操通过（Apply Patch / 托管链接 / 子模块成功路径由单测覆盖）。

## 第 7 轮：真机补验 + 可读性与条理（用户反馈：继续跑完真机、数字可以大一点、更原生/清晰/有条理）

| 项 | 改动 | 设备复验 |
|---|---|---|
| 分支行 `↑6 ↓1 ✓` | 原为 10.5pt 暗灰、两数字挤在一起；改为 11pt Medium、亮一档（`TextMuted` 向 `Text` 混 55%），`↑`/`↓` 各自成字、间距 5，✓ 同组（文字徽标如 “not set up” 保持整块） | 放大截图：两数字清晰分开，✓ 不再贴着 ↓1 |
| 其余小数字 | 区块计数（Branches 4 / Tags 3 …）、Changes 文件行 `+1 −0`、文件数、提交详情的作者·时间/短哈希、分段标签计数：Micro/Caption(10–10.5) → BodySmall(11) | 侧栏与文件列表数字一档提升，行高/列宽不变 |
| Reflog 面板 | 原为 `HEAD@{n}` + 哈希 + 原文 + 绝对时间的四列“原始输出”；改为 动作类型徽标（commit/checkout/reset/rebase/pull）· 说明 · 短哈希 · 相对时间；`HEAD@{n}` 与绝对时间进 tooltip；说明行文字提到 11pt | 面板一眼能扫到“做过什么”；右键菜单不变 |
| 对话框字段 | 新增 `captionedField`（字段上方可见标题 + 可选提示）；Tag（Tag name / Message 并说明“带说明即 annotated”）、Add Submodule（Repository address / Folder，提示默认取仓库名）、通用文本对话框（分支名/Stash 说明等）都有常驻标题——占位符一输入就消失，不能是字段唯一的说明 | 三个对话框截图均有标题 |
| 确认按钮 | Git 的确认弹窗按钮由泛泛的 “Confirm” 改为动作名（Remove / Discard / Delete / Drop All / Squash / Check Out / Restore / Reset / Revert …），`confirmActionText` 一处映射，非 Git 确认保持 Confirm；Cancel 仍是默认键 | Remove Submodule、Delete Tag from Remote 弹窗按钮分别为 Remove、Delete |
| Actions 菜单 | 4 个低频同步变体收进 `Fetch & Pull Options` 子菜单（顶层少 3 项），其余分组不变 | 菜单顶层 15 项，子菜单四项齐全 |
| Add Submodule 本机路径 | **真机发现**：给本机绝对路径/`file://` 被 Git 默认的 `protocol.file.allow=user` 拒绝（“transport 'file' not allowed”），用户无法添加本地子模块。改为仅对**用户键入的本机路径**在该条命令上放行 file 传输（环境变量 `GIT_CONFIG_*`，不改任何配置文件；不递归，https/ssh 地址走原路径）；域测去掉原先全局放行，覆盖 `file://` 与绝对路径 + 删除 | 沙箱 demo：添加 → `.gitmodules` + gitlink 暂存、侧栏出现 Submodules 区；`Remove Submodule…` → Remove 后目录与登记消失 |
| 复制/打开提交链接 | 把 origin 临时指向 `https://github.com/acme/demo.git`、点 Refresh 后提交菜单出现 `Copy Commit Link` / `Open on GitHub`；Copy 后剪贴板为 `https://github.com/acme/demo/commit/<完整哈希>` | 已验证复制；“Open on GitHub” 只调 `OpenExternal`，不在沙箱开浏览器 |
| Apply Patch | 菜单项 → 系统文件对话框（含 “The patch is applied to the working tree only — nothing is staged or committed.” 说明）真机已出现；对话框是远程视图服务，无障碍树不可达，后台无法替它选文件，故“选中补丁后应用”仍由域测覆盖（`apply --check` + 应用 + 越界/超限拒绝） | 对话框出现并可取消 |

踩坑: 远端 URL 在 App 外被改（`git remote set-url`）后，侧栏要点一次 Refresh 才会重读 remotes（既有行为，非本轮引入）。

验证: `go test -short ./internal/gitworkbench ./internal/nativeui` 绿；`go vet`；真机截图对比如上。
