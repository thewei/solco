# solcogit/
> L2 | 父级: /CLAUDE.md

Solco → Solco Git 的单向链接（2026-11-07 决定「Solco Git and the right panel」第 2、5 条）：在本机找到 Solco Git，并请它在某个视图、某个文件上打开某个仓库。这里不 import Solco Git（反向没有链接，也没有 Open as Workspace），链接里的路径从不进日志。

成员清单
- `solcogit.go`：`URL`（`solcogit://open?file=&repo=&view=changes|commits|files`，仓库必须绝对路径、文件必须相对且不越界、不超过 8192 字节，否则不生成）、`Locator`（`SOLCO_GIT_APP` 覆盖 → `/Applications` → `~/Applications`，必须是 `.app` 目录；答案缓存 5 s，因为界面每帧都在问"要不要画这个入口"；非 macOS 恒为"没有"）、`Link`（`Available` 决定入口显不显示，`Open` 经 `open -a <bundle> <link>`——只用 argv、不拼 shell，链接交给被选中的那个包而不是系统里恰好注册了 scheme 的另一份）。
- `solcogit_test.go`：链接形状（与 `../solco-git` 的 `TestOpenURLParsing` 各钉同一条字面量）、怪路径往返、拒绝清单、查找顺序/覆盖/缓存/重新检查/非 macOS、`Open` 的包与链接、先拒绝后问系统。

[PROTOCOL]: 变更时更新此头部，然后检查 /CLAUDE.md
