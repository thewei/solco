# GitHub Release Updates

Solco 使用 MyGo 官方更新器。安装版从 `thewei/solco` 的 GitHub Releases 拉取 Ed25519 签名清单，然后验证、下载并安装新版本。

## 发布

1. 修改 `mygo.config.ts` 中的 `version`。
2. 提交版本变更，然后创建匹配的 tag：`git tag v0.10.1 && git push origin v0.10.1`。
3. 等待 `release` workflow 完成。macOS、Windows 和 Linux 串行构建，避免并发刷新 draft release 后缓存 ID 失效。
4. 检查 draft release 内三平台安装包和 `update-*.json` 完整。
5. 发布并标记 latest：`gh release edit v0.10.1 -R thewei/solco --draft=false --latest`。

## 运行时

- `main.go` 注册 MyGo `updaterNative.Plugin`。
- 应用菜单提供 “Check for Updates…”。
- 未发布前，安装版不会看到 draft release；发布后才收到更新。

首次接入更新器的旧版本没有内嵌公钥，不能自更新到 v0.10.0。用户需要手动安装 v0.10.0 或更新版本，之后的应用才具备自更新能力。

[PROTOCOL]: 变更时更新此头部，然后检查 ../CLAUDE.md。
