# Herdr Pane 历史读取 API：实机验证（2026-11-05）

状态：**已验证：保留缓冲区的完整纯文本可通过官方只读 Socket API 读取；完整 ANSI/Alt-screen 会话仍有缺口。**

## 目的与版本

- 目标：不运行 Herdr TUI、不接管 Terminal、不滚动共享视口，获得和 Herdr TUI Copy Mode 同一份 Ghostty Scrollback 文本。
- 实际安装及源码：Herdr **v0.9.5**、Protocol **22**。源码从公开 `herdrdev/herdr` 的精确 `v0.9.5` 标签读取，不根据新旧文档猜方法。
- 现场：`default` session 的 `w3:p13`（运行中 Pane，非隔离测试），只调用查询方法；没有发送按键、没有更改共享视口、没有记录终端正文。

## 证据与结果

| API | 参数 / 实际结果 |
| --- | --- |
| `pane.get` | 起初 `scroll.max_offset_from_bottom=8120`、`viewport_rows=51`，证明服务端持有超过一千行历史 |
| `pane.read` recent, lines=80 | 约 8 KB，`truncated=true` |
| `pane.read` recent, lines=1000 | 104321 bytes，`truncated=true` |
| `pane.read` recent, lines=30000 | 仍 104321 bytes，`truncated=true` |
| `pane.selection.read` | 绝对行区间 `[0, 8024]` 一次返回约 838 KB；未经过 `pane.scroll` |
| 新封装 `Manager.ReadPaneRetainedText` | 实机 opt-in 测试读取 **8041 个保留屏幕行坐标、838974 bytes**；读取前后的 `scroll.offset_from_bottom` 均为 0，PASS |

行数是 Ghostty 的屏幕行（包括软折行），导出的纯文本换行数量更少，不能用文本换行数当作总缓冲区行数。以上指标采样时缓冲区正在变化，因此不把不同时间的最大偏移进行精确字节比对。

## 源码真值

- `src/app/api_helpers.rs:116`：`line_limit = lines.map(|lines| lines.min(1000) as usize)` —— **pane.read 与 agent.read 一律最多 1000 个屏幕行**。
- `src/app/api/panes.rs:1512`：`handle_pane_read` 仅透传上述受限快照，并把 `revision` 设置为 0。因此 `pane.read.revision` 不能作为快照内容修订号。
- `src/app/api/agents.rs:270`：`handle_agent_read` 也直接调用 `read_terminal_snapshot`。v0.9.5 **没有**在 `agent.read` 里自动滚动 Agent TUI 拼接会话的逻辑。
- `src/app/api/panes.rs:234-287`：`pane.selection.read` 构造绝对坐标 `Selection::absolute_range`，调用 `runtime.extract_selection`，可使用 `content_revision` 拒绝内容变化。
- `src/pane/terminal.rs:3094`：`ghostty_extract_selection` 直接使用 `core.terminal.read_text_screen(..., false)`，与 Herdr TUI 的底层保留缓冲区一致；返回的是文本，不是 ANSI 样式或原始 PTY 字节。
- `src/selection.rs:276`：TUI 鼠标选择的绝对行号 `max_offset_from_bottom - offset_from_bottom + viewport_row`；`src/server/pane_input.rs:170-186` 使用 `runtime.scroll_up/down` 改变服务端 Ghostty 视口。

## 官方 API 读取流程（已实机验证）

1. `pane.copy_motion`，`cursor={row:0,col:0}`、`motion=line_end`，得到当前 `content_revision`。
2. `pane.get` 取得 `scroll.max_offset_from_bottom` 与 `scroll.viewport_rows`；`totalRows = maxOffset + viewportRows`。
3. `pane.copy_motion` 定位最后一行 `row=totalRows-1` 的 `line_end`，要求上一步固定的 `content_revision`，取得真正的最后一列，避免硬编码终端宽度。
4. `pane.selection.read`：`anchor={row:0,col:0}`、`cursor={row:last,col:lastNonBlankCol}`，携带 `content_revision`，直接提取整个**当前保留**缓冲区。
5. 收到 `stale_content` 时返回重试信号，**不将不同时刻的历史拼成一段**。

此流程不触碰 `pane.scroll`、`terminal attach`、`--takeover`、PTY 输入或 Herdr 私有恢复文件。Solco 已新增 `internal/herdr/retained_history.go` 和 Schema-shaped 测试。该 API 是显式历史查询／导出入口，不允许每次终端渲染时自动全量调用。

## 能力边界与下一步

- **已达成**：读取全部**当前尚未被淘汰的** Ghostty 主屏幕文本，与 TUI 滚动／复制共享底层数据；不受 `pane.read` 的 1000 行上限限制。
- **不保证**：从 Pane 创建起的无限历史。Scrollback 按字节容量淘汰旧行；`experimental.pane_history` 只负责可选的重启回放，不提供无限归档。
- **不保证**：全屏 Agent 的自身对话历史。Alternate Screen 可没有主屏幕 scrollback；`agent.read` 也不是会话 API。应使用 Provider 原生历史或它主动提供的接口。
- **尚缺**：任意区间高保真 ANSI/样式、原始 PTY 历史流和稳定的偏移分页。现有 `pane.selection.read` 仅返回纯文本，`terminal session observe` 返回屏幕渲染帧而不是完整原始输出。
- **性能**：全量文本读取在一次 Socket 响应中进内存；支持全文导出但不应轮询。真正的大数据量分页/彩色回放，应优先给 Herdr 上游增加只读 range + content revision 接口，而不是由 Solco 持有第二套 PTY/scrollback。

## 复验命令

```sh
GOTOOLCHAIN=auto go test ./internal/herdr -run '^TestReadPaneRetainedText' -count=1
SOLCO_HERDR_TEST_PANE=<已授权的PaneID> SOLCO_HERDR_TEST_SESSION=default \
  GOTOOLCHAIN=auto go test ./internal/herdr -run '^TestReadPaneRetainedTextLive$' -v -count=1
```

真机测试只输出字节数、屏幕行数量与视口是否不变，不输出任何正文。
