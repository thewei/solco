# scripts/
> L2 | 父级: /CLAUDE.md

成员清单
- `omp-chat-stream.ts`：Solco 唯一 Chat Companion 源码；OMP 原样加载，Pi 的显式安装器只替换单一 `const streamAgent` 标记后安装到 `.pi/agent/extensions`；非阻塞收集 text/thinking/tool，另转发真正的 agent_end（整轮完成，与 message_end 不同）并通过共享 AppHook Socket 推送（仅 Herdr 根 Pane）
- `omp-chat-stream.test.ts`：Bun 测试伪 OMP/本机临时 Unix Socket，验证严格序号与事件帧
- `check-omp-chat-stream-load.py`：OMP 18.x 官方 -e 加载器的隔离 HOME/RPC ready smoke；不调用模型、不开真实 Herdr/TUI
- `check-omp-chat-stream-live.py`：隔离 HOME + loopback SSE 假模型 + 原生 OMP 18.x RPC + Unix Socket，确认 read 工具/3 段 text_delta；其 `run(agent)` 被 Pi 测试复用（Pi RPC 无 ready 帧）。绝不访问付费模型或用户 Herdr
- `check-pi-chat-stream-live.py`：Pi 0.80.10 真实 RPC + 本地模拟模型 + 单一源码 Pi 版本的 10 帧/工具/3 delta/agent_end 验收
- `check-herdr-pi-chat-stream.py`：SC-06 真实 Herdr Pi TUI 双轮验收；隔离短 HOME + 原生 Herdr Pane 单一 Pi Agent + 本地模型，16 帧/6 delta/每轮 agent_end/工具/原 TUI 输出，严格身份/seq/私有 JSONL；不修改用户运行环境
- `build-tailcat-mobile.sh`：手机端 Tailcat 隧道的唯一可复现构建——`android` 出 AAR（默认 arm64，`ANDROID_ABIS` 可加宽）、`ios` 出 XCFramework（设备+模拟器），落到 `../solco-mobile/modules/solco-tailcat-transport/{android/libs,ios/Frameworks}` 并写 `.sha256`；输入是嵌套 Go 模块 `mobilebridge/tailcatbridge`（gomobile 门面，不进根模块依赖）；产物从不手拷、不入库
- `check-hostd-chat-stream-process.py`：真实编译并运行隔离 `solco-hostd` 辅助进程；私有 Herdr 服务/证书/设备凭据/TLS bootstrap，接收 Pi Socket 帧，终止释放 Socket；不注册用户 launchd
- `check-herdr-omp-chat-stream.py`：SC-05 真实 Herdr 0.9.5 + OMP TUI 集成验收；短 /tmp HOME 独立 Herdr Socket + scratch Pane + 真 OMP + 127.0.0.1 假模型 + Solco Extension 独立安装到沙箱 HOME；一窗格连续两次 Herdr agent.prompt（16 帧含 read tool、6 text_delta、2 个 agent_end），校验原 TUI 显示、单 Agent/连续序号/同一转录路径；无需付费 API，不接触用户的 Herdr/OMP/App。
- `install-omp-chat-stream.sh`：按需 --check/--install/--uninstall 独立 OMP 扩展；拒绝覆盖外来文件/符号链接，不触碰 Herdr 扩展
- `install-omp-chat-stream.test.sh`：隔离 HOME 的安装/升级/幂等/外来文件拒绝/卸载回归
- `publish-remote-helper.sh`：打包/发布远程桌面 Helper
- `embed-remote-helper.sh`：构建时把远程桌面 Helper 嵌入 App
- `embed-hostd.sh`：打包应用常驻 Host Helper
- `build-hostd.sh`：构建通用（arm64+x86_64）常驻 Host Helper
- `build-complete-macos.sh`：本地完整 macOS App+DMG 打包闭环：读取 mygo.config.ts 版本，先构建并嵌入 `resources/darwin/solco-hostd`，再运行 MyGo build，签名校验 App/双架构 Helper/DMG；`--verify-only` 只验收既有产物，不注册 launchd 或替换用户 App；release CI 已有同等 Helper 构建步骤
- `generate-shepherdr-2.1-test-index.mjs`：生成 Shepherdr 2.1 测试索引
- `check-shepherdr-2.1-parity.mjs`：检查 parity 决策/测试覆盖
- `remote-helper.pin`：远程桌面 Helper 发布版本固定标记

[PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
