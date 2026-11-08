# apphook/
> L2 | 父级: /CLAUDE.md

成员清单
event.go: 核心事件契约定义，StatusEvent、SessionEvent（携带 CWD 供跨 pane daemon 会话解析）和只读 OMP ChatStream Handler
server.go: 进程内 Unix Domain Socket 服务，多协议载荷（JSON-RPC、直接 JSON、Envelope、Tab 文本）解析与状态规范化（提取 CWD 与 session 属性）；chat.stream 有界 JSON-RPC 只读事件；SC-06 主窗口与常驻 Host 同 Socket 所有权保护：仅 ECONNREFUSED 证明残留 Unix socket 可清理，超时/符号链接一律拒绝；Close 只清理自己创建的 Socket inode/仍指向自己的软链接，失败 Start 不能删别人 Socket
server_test.go: Hook 服务全协议格式解析、死锁防范与 Socket 生命周期回归测试；额外验证 Bind 失败随后 Close 不能删除已有活跃 Socket
chat_stream_test.go: OMP chat.stream Socket 有效/无效帧与回调路径测试

[PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
