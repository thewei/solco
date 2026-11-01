# internal/changelog

Settings → About 页的数据源包。

- `changelog.go`：`Current()` 解析嵌入的 `snapshot.md`——`## v…` 开一个发布（第二个 `## v` 结束前一个），`### 名称` 开分组，`- 条目` 归入当前分组；只返回最新一个发布。
- `snapshot.md`：发布流程（`.agents/skills/solco-release`）在发布时从仓库根 `CHANGELOG.md` 截取目标版本小节写入，并随版本 bump 提交；dev 构建显示上一个已发布版本的改进。

法则: 只读嵌入快照，不读网络、不读运行时文件；解析规则与 CHANGELOG.md 的写作约定一一对应。
