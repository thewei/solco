# 2026-11-04 · Solco 打开一段时间后崩溃（JavaScriptCore Heap Collector）

> 进度文件（环境先行 → 实机取证 → 根因 → 修复 → 同环境复验）。用户原话：「只是打开长时间 Solco.app 过一段时间就会崩溃，你查一下日志是什么原因，并且修复」。

## 1. 取证

- `~/Library/Logs/DiagnosticReports/Solco-2026-11-04-{124559,140752,152550,160036,165404}.ips`：5 次崩溃，间隔约 0.6–1.4 小时。
- 5 份报告同形：`EXC_BREAKPOINT / SIGTRAP`，崩溃线程是 **`JSC Heap Collector Thread`**，pc 恒为 JavaScriptCore 内同一个偏移（`0x1a2061dec`，镜像偏移 24198636）；Go 的信号处理器把 SIGTRAP 重新 `raise`，所以栈上只剩 `raise` 与 Go 帧，真正的 brk 点被盖住。
- 同时刻主线程在 AppKit 的 `setNeedsDisplayInRect` / malloc 里（正常的 UI 工作），`Heap Helper Thread` 停在 `Heap::runBeginPhase`——也就是崩在一次 GC 的 begin phase（stop-the-world）。
- 进程内只有**一个** JSC VM 的堆（一个 collector 线程）。Solco 自己不创建 JS VM；这个 VM 是 WebKit 在**本进程**里反序列化 `WKScriptMessage.body` 用的：`third_party/mygo/internal/darwin/window.go` 的 `userContentController:didReceiveScriptMessage:` 每收到页面一次 `postMessage` 就读一次 `body`。
- WebContent 进程里的页面堆不在本进程；`vmmap` 的 WebKit Malloc 稳定在 70 MB 上下，没有泄漏迹象——问题不是内存增长，而是这个 VM 的 GC 次数。

## 2. 复现

把桌面 Assistants 页的轮询临时改成每 12 ms 一次 `assistants.snapshot()`（`// FLOODTEST`，已还原），页面→Go 消息约 80/s：

| 版本 | 结果 |
| --- | --- |
| 原 `postMessage` 传输 | 3 轮：约 10 分钟、3.5 分钟、12 分钟崩溃，三次同一个 pc |
| 原传输 + `JSC_useConcurrentGC=0`（`open --env`，已确认进入进程环境） | 约 12 分钟崩溃，同一个 pc、仍是 `JSC Heap Collector Thread`——该开关不起作用 |
| 页面消息改走 scheme handler（见下） | 25 分钟无崩溃（主动结束），见 §4 |

真实使用下页面→Go 的消息远少于 80/s（几个页面的 4–6 s 轮询、聊天的调用与确认），崩溃周期因此是小时级，与 5 份报告的间隔吻合。

## 3. 根因与修复

根因在 WebKit 的 UI 进程 JS VM 的 GC 与「主线程跑 Go 回调（purego）」这种宿主的组合上，不是 Solco 业务代码：任何经 `WKScriptMessageHandler` 读 `body` 的页面→Go 消息都会在这个 VM 里分配，累计到一定数量后 collector 线程在 begin phase 触发同一处断言。无法在 Solco 侧修 JavaScriptCore，也没有可用的选项关掉它。

修复是**绕开这个 VM**，而不是减少轮询（减少只会把崩溃推迟）：`third_party/mygo` Delta 5——

- `packages/bridge/src/bridge.ts`（及其生成物 `internal/bridge/bridge.js`）：`mygo:` 同源页面把每条 bridge 消息作为一次串行 `fetch("/__mygo/msg", {method:"POST", body})` 发送；队列保证与原 `postMessage` 相同的先后顺序；任何一次被拒（body 没送到、状态非 2xx）就**永久**回退 `postMessage`，所以只换了 Go 侧之前的页面也能工作。
- `protocol.go` 的 `serveBridgeMessage`：只处理 macOS、POST、路径 `/__mygo/msg`、scheme 为应用自己的前端 scheme；`Origin` 若存在必须是应用可信来源；body ≤ 96 MiB（32 MiB 附件的 base64）；空 body 返回 400 让页面回退；然后 `postMain(handleMessage)`——与原来一样在主线程、一样经过 per-window secret 检查，只是 body 不经过 JSC VM。
- 其余方向（Go→页面的 `evaluateJavaScript`、事件、channel）不变。

首条消息经 scheme handler 到达时记一行 `mygo: bridge messages arrive through the scheme handler`（`~/Library/Logs/Solco/solco.log`，只此一行，无内容）。

## 4. 复验

同一 12 ms 洪水（`// FLOODTEST` 临时构建，flood 期间 CPU ≈150%），新传输：

- 17:26 启动；日志里该行在首条消息出现，说明 fetch 路径真实生效（不是静默回退）。
- **最终结果**：运行 25 分钟（17:26–17:51，约 12 万条页面→Go 消息）无崩溃、无新崩溃报告，主动结束；此前同一洪水最长 12 分钟就崩。RSS 277–405 MB 平稳。

## 5. 未解决 / 注意

- 没有拿到 JavaScriptCore 崩溃点的符号（系统 dyld cache 里没有该函数的符号），根因描述停留在「UI 进程 JS VM 的 GC 在该宿主下触发断言」这一层；证据是：同一 pc、洪水可复现、绕开该 VM 后同一洪水从 ≤12 分钟崩溃变为 25 分钟无崩溃。
- 文件拖放在 macOS 仍走 `postMessage`（一次一条，非高频）。
EOF