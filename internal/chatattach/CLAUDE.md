# internal/chatattach/
> L2 | Parent: ../../CLAUDE.md

Chat 附件的落盘共享层：桌面 `ChatBridge.SaveAttachment`（粘贴/拖入）与手机 Remote Host（`POST …/chat/attachments`）写同一个目录、守同一组上限。不 import MyGo/nativeui；只写自己的缓存目录，不执行、不解析文件内容。

成员清单

- `chatattach.go`：`Dir`（`~/Library/Caches/<app>/ChatAttachments`——不放 Application Support，因为 `@path` 会在空白处被 Agent 切断）、`Save`（0700 目录 / 0600 文件、扩展名取文件名→MIME→`bin`、单文件 ≤ `MaxFile` 32 MB、写完即 `Prune`）、`Prune`（超过 14 天删除，再按最旧先删到 200 MB 内）。
- `chatattach_test.go`：扩展名与权限、清理、缓存目录位置且无空格。

法则: 无框架依赖·路径无空白·上限单一来源（手机 `MAX_ATTACHMENT_BYTES` 与之对齐）
[PROTOCOL]: 变更时更新此头部，然后检查 ../../CLAUDE.md
