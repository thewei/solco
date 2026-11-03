# LESSONS

- 2026-11-03 MyGo macOS 直接启动二进制时启动父进程秒变僵尸，真实 app 由 LaunchServices 重新拉起为子 pid → 判断沙箱实例存活要用 `pgrep -f "沙箱app路径"`，别看 `$!`（流程：第 0 步环境）。
- 2026-11-03 Solco 单实例锁 key = CFBundleIdentifier+name，锁 socket 在 /tmp 不随 $HOME 隔离 → 沙箱必须复制 .app 并改 CFBundleIdentifier（`PlistBuddy Set` + `codesign --force --deep -s -`），直接跑 build 产物会静默 exit 0（main.go:55 在日志初始化前 return）。
- 2026-11-03 Solco 自己拉起的 herdr 用 `$HOME/.config/herdr/herdr.sock`，HERDR_SOCKET_PATH env 对它不生效 → CLI 操作沙箱运行时要把 HERDR_SOCKET_PATH 指到这个路径。
- 2026-11-03 合成输入到不了 MyGo 自绘控件的三类东西：mouseMoved（全部 hover 动作）、右键上下文菜单、ctrl+click 模拟右键 → 这些功能留人工复核；AXPress 对 AXButton/AXMenuButton/AXMenuItem 全有效；后台 PX hit-test 能按到前台 HID 点不中的像素（"Stage N Lines" 按钮疑似热区错位，需真鼠标确认）。
- 2026-11-03 MyGo toast 计时在窗口处于后台时暂停 → 后台沙箱里"toast 永不消失"是测试假象，不是 bug。
- 2026-11-03 cua-driver 一次性 CLI 调用不共享快照缓存，element_token 跨调用必须带同一个 `session` 标签；click 的 x,y 是 1568 宽截图 px，AX frame 是屏幕点（换算 ×1.225）；需要亚秒连点时用单 MCP 进程（会话内多调用）绕 2s 激活防抖。
- 2026-11-03 本 harness 的 bash `export` 跨调用持久 → 沙箱 HOME 用完立刻 `export HOME=/Users/wilson` 还原，否则 cua-driver 等工具找不到自己的目录。
- 2026-11-03 编辑工具 `PUT N.=M` 是替换语义不是插入 → 追加用 `PUT >N`；本轮两次误替换（graphGen 字段、ensureGitSnapshot 调用）都被 `go test`/`go vet` 兜住。
- 2026-11-03 Split/Unified 切换不重绘（①）根因未定位，头号嫌疑：`gdRowIdentity`（gd_rows.go）的行身份不含布局模式，ui.List 按键缓存跨布局复用旧行元素 → 再查先插桩 `gd rebuild trace`（gdDiffSurface 的 dirty 分支），别盲改。

## 2026-11-03 沙箱 UI 驱动（Assistants B3）
- 在同一个 shell 里跑过 e2e 脚本后，导出的 `SOLCO_*`/`HERDR_PLUGIN_*` 环境变量会留在会话里，之后启动的沙箱 app 会继承并指向 e2e 库。启动沙箱前 `env | grep -E 'SOLCO_|HERDR_'`，并 `unset`。
- 沙箱 app 与活实例同 bundle id 会被 single-instance 锁秒退（exit 0、无日志）：拷贝 `Solco.app`，`PlistBuddy` 改 `CFBundleIdentifier`/`CFBundleName`，`codesign --force --deep -s -` 重签；日志目录名随 `CFBundleName`。
- 要让沙箱看到本地插件：`HOME=$SB/home herdr plugin link <插件副本>`（副本里自己 `go build -o bin/solco-automations`，别覆盖活实例用的 bin）。
- Computer Use：所有调用的 `target` 必须是对象 `{scope,pid,target_id}`，点击要带最近一次截图的 `coordinate_token`；悬停才出现的 “+” 要点两次（第一次只是悬停/折叠了区头）；后台 app 用 `computer_type` 走 AX 值写入即可。
- 后台任务用 Shell `block_until_ms:0` 起（`&`+nohup 会随调用结束被回收）。

## 2026-11-04 Assistants E06/E07 收尾
- 删大批文件后提交：`git commit -- <paths>` 需要把已 `git rm` 的路径也列进去；路径多时用 `--pathspec-from-file=<file>`（zsh 下 `$(…)` 换行不会拆词，直接传会报 pathspec 错）。
- 台账门禁（`check-shepherdr-2.1-parity.mjs`）需要真实的上游审读才能关；SHA 8df9402… 可 `git fetch --depth 1 origin <sha>` 后 checkout（tag `v2.1.0` 解析出的 HEAD 与它不同）。不审读就不要标 verified/not_applicable。
- 给“迁移被打断”写测试：`migrations` 是包级切片，测试里追加一条必失败的 SQL（先建表再写不存在的表），断言版本号不变、半成品表不在、旧数据在，再换回可用迁移确认补完。
- 沙箱直接 `exec` 启动二进制时要用 `block_until_ms:0` 后台；`nohup … &` 在调用结束就被回收。用 `computer_resolve_app {pid}` 取 target_id（`computer_apps` 列不出沙箱）。

## 2026-11-04 对标台账关账的教训
- 逐条审读上游测试不是走过场：1,193 条里至少挖出 3 个真缺陷（取消后仍认领、合并覆盖 ignored 文件、拼错字段被静默丢弃）。每个“看起来对不上”的上游断言先写一个红测试再决定修或 n/a。
- 台账脚本要核对 `target_test` 的函数名真实存在，别只看文件存在；再用 `go test -v -run '^(A|B)$'` 数 `--- PASS`，`ok` 不代表命中（没匹配也 ok；opt-in 测试会 SKIP，要带环境变量跑）。
- 全量并行跑 4 套 gate 时 `TestRevealGrowsTheWindowToTheCommit` 会因机器争用超时（单跑 11s 全过）：门禁结果以单独重跑为准，别在并行下判红绿。
- 严格解码（`DisallowUnknownFields`）会让原来“带多余字段”的测试变红——这正是它的价值；测试里的多余字段要删，而不是放宽解码。

## 2026-11-04 真机联调的教训
- 看手机截图时每张都写**新文件名**（`/tmp/ph/s_<纳秒>.png`）：同名文件会让图像读取回显旧内容（状态栏时钟/电量对不上就是这个信号），一度让我以为手机在 Remote Desktop 里乱点。
- 客户端与核心的**枚举词汇要对着真核心验证**：fixtures 只覆盖 `active` 时，`paused` vs `disabled` 这种错位在契约测试里永远绿，只有真机点一次 Pause 才暴露。
- 插件二进制不在 App 包里，装 App ≠ 装插件：安装脚本要顺带重建插件，否则 App 探测不到 Assistants 核心就整块隐藏；App 只在启动时探测，重建后需重启 App。
- 手机键盘弹起会让底部输入条整体上移：先截图再点发送，别按键盘前的坐标盲点。
- macOS 屏幕录制/辅助功能授权是用户的安全决定：真机验证 Remote Desktop 前请人授权，别替他点。
