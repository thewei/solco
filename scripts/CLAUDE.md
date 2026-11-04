# scripts/
> L2 | 父级: /CLAUDE.md

成员清单
- `omp-chat-stream.ts`：Solco 独立 OMP 只读事件扩展；非阻塞收集 text/thinking/tool 状态并通过现有 apphook Socket 推送（仅 Herdr 根 Pane）
- `omp-chat-stream.test.ts`：Bun 测试伪 OMP/本机临时 Unix Socket，验证严格序号与事件帧
- `check-omp-chat-stream-load.py`：OMP 18.x 官方 -e 加载器的隔离 HOME/RPC ready smoke；不调用模型、不开真实 Herdr/TUI
- `install-omp-chat-stream.sh`：按需 --check/--install/--uninstall 独立 OMP 扩展；拒绝覆盖外来文件/符号链接，不触碰 Herdr 扩展
- `install-omp-chat-stream.test.sh`：隔离 HOME 的安装/升级/幂等/外来文件拒绝/卸载回归
- `publish-remote-helper.sh`：打包/发布远程桌面 Helper
- `embed-remote-helper.sh`：构建时把远程桌面 Helper 嵌入 App
- `embed-hostd.sh`：打包应用常驻 Host Helper
- `build-hostd.sh`：构建 Host Helper
- `generate-shepherdr-2.1-test-index.mjs`：生成 Shepherdr 2.1 测试索引
- `check-shepherdr-2.1-parity.mjs`：检查 parity 决策/测试覆盖
- `remote-helper.pin`：远程桌面 Helper 发布版本固定标记

[PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
