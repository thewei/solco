# internal/changelog

Settings → About 页与更新检查系统的数据源包。

- `changelog.go`：`Current()` 解析最新版本、`All()` 解析所有版本、`Between()` 提取跨版本更新集、`CompareVersions()` 负责版本比较、`ParseReleases()` 解析 Markdown 格式的更新说明。
- `snapshot.md`：发布流程（`.agents/skills/solco-release`）在发布时同步的完整 CHANGELOG 快照，供离线展示与跨版本对比。

法则: 纯数据解析包，不直接产生副作用，解析规则与 CHANGELOG.md 的写作约定一一对应。
[PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
