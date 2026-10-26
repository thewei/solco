# workboard/
> L2 | 父级: /CLAUDE.md

成员清单
workboard.go: herdr-projects 插件的能力门（2026-10-26 决策）——Detect 纯函数解析 `herdr plugin list --json`：已安装 + 已启用 + 版本在已验证窗口 1.2.x + 声明 `open` action 与 `board` pane 才 Supported；Reason 是日志/测试用的稳定码，不是用户文案；插件是任务权威，本包不存任务、不读插件文件
workboard_test.go: 门控回归——live 1.2.26 样本形状、其它 patch/v 前缀、与别的插件并存、未安装/禁用/新旧 minor/次 major/垃圾版本、缺 open action 或 board pane、非 JSON/空输出，Supported 是唯一门
task.go: Task/Detail 解析（list/details --json，字段形状不明按“存在但未知”处理）与保守分组 GroupOf（2026-10-26 方案 A）——ready to approve 需 approval+blocked+permission 且说明仍以插件自己的 "Approve to merge?" 开头（PR 失败时插件会改写它，hidden prFailure 由此可见）、details 已读、failure 为空、review 未失败；任何存疑归 needs you；ReadyCandidate 是进入 Ready 的唯一路径；Detail 另带只供详情页展示的字段（描述/Agent/Failure 标题·正文·next/Review by·text·findings 数），形状不明时留空且不影响解析与分组
task_test.go: 契约测试——真实 1.2.26 的 list/details 样本（含插件自带测试世界跑到 approval 阶段的四份 testdata：本地就绪、PR 就绪、Approve 时检查失败、轮询发现失败）、各 details 形状、GroupOf 全表、「只有候选才可能 Ready」穷举
client.go: 插件 CLI 唯一调用点——`sh <root>/bin/run.sh list|details <id> --json`，剥掉 HERDR_PANE_ID 等 Agent 身份环境（与插件自己的 DROP_ENV 同集）并把 HERDR_SOCKET_PATH 换成会话 socket；读：list/details；写：Approve（先重读 list+details，GroupOf 仍为 Ready 才执行，从不带 --anyway，否则 ErrNotReady）、SendBack(review)/Retry/Fix/Delegate/Stop/Archive、Add(title,repo,delegate)、Tell(id,text)、CoordinatorTell(project,text,delegate)；拒绝空/选项形 id 与选项形 title/repo
task.go 另有 P4/P5 字段：Task 的 App/Queue/ReviewRound/Research/Chat + PlanReady()/NeedsPick()/Tags()，Detail 的 Plan/Findings/Reviews/ReportLink/Limit*/Handovers/Attempts/QueueWords（形状取自插件源码 finish_line::details，缺失即空）；client.go 另有 ApprovePlan（re-list 且须仍在 plan 阶段才 approve，无 --anyway）、AddTask（--desc/--research/--chat/--after/--delegate）、DelegateNow（`delegate <id> --now`）
extras.go: 插件原生增强读面——`list --pinned --json`（{machine,projects[],tasks[]}）→ Pinned、Pin/Unpin（任务 id 或 Project 名）、`coordinator --project <repo> status --json` → Coordinator（state: none/starting/working/blocked/idle/exited + pane，Running() 才有可进入的 pane）；全部来自插件自己的 JSON，无 tab 名/目录推断
extras_test.go: 真实 1.2.26 样本解析、Running 判定、argv 与坏目标拒绝
manage.go: 管理面 P6——Archived（`list --archived --json`）/Restore/Clear、ArchiveDone（`archive --done --repo R --json`，只归档 done）、ArchiveProject/RestoreProject（`--repo R`，插件会停 Agent/协调者并关自动化）、CoordinatorControl（restart|new|stop）、Autopilots（`autopilot --json`，按 `project:R` 取名）/SetAutopilot（on 用已存 goal，无 goal 插件拒绝）；空/选项形目标一律拒绝
automations.go: 自动化管理 P7——Automations（`automations --json` 1.2.26 沙盒实测形状）与 on/off/run/remove 按 id 定位；AddAutomation（`automations add … --schedule|--when [--every-repo] [--if]`，触发器与条件语法归插件）；AutomationEvents 取钉住的 1.2.x 事件清单。client.go 另有 DelegateWith（`delegate <id> --<setting> V`）与 settings.go 的 TaskSettings（`show <id> --json`）
settingspec.go: 设置页「选择而非填写」的钉住规格表（1.2.x 名字→类型/选项/下限/单位）+ Spec.Fits（值不合规即降级文本）+ Amount 解析/格式化 + Client.Models（`models --json`）；testdata/show_everywhere_1.2.26.json 是漂移检查用的真实 show 快照，升级插件 pin 时重录
settings.go: 设置读写（用户 2026-10-26 显式授权）——`show --json [--repo R | --project-coordinator R]`（各范围返回不同集合）、`set <name> <value> [scope]`、`unset`；不复制取值集合、拒绝原文透传，`--` 开头的值/名字一律拒绝
client_test.go: argv、坏 id 拒绝、错误透传、空 root 全部失败、环境身份隔离（真实起 sh 脚本验证）、写动词 argv、Approve 只在 Ready 时执行且无 --anyway、点击时已变化的任务被拒绝

[PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
