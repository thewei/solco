# frontend/tests/
> L2 | 父级: ../CLAUDE.md

成员清单
- `work.test.ts`：Feedback 回答表单的持久化工作区（往返、三天丢弃、坏数据不抛、离线标记）；`focus.test.ts`：只有文本框会唤起键盘
- `chat-delta.test.ts`：复用 Chat View 自身的纯 delta reducer，验证有序尾 Patch、不修改前态、缺帧/跨会话拒绝；只由 Bun 跑，不进入手机或桌面 WebView 打包

[PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
