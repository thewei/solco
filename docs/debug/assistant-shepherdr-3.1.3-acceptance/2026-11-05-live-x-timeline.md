# AC-02/AC-04 实机：X.com 时间线去重总结（只读，用户 2026-11-05 授权）

用户授权原话："可以，修改为，把我时间线的消息去重总结"（只读浏览自己已登录的 X）。

## 环境

- 真实 OMP 18.16.0 + 真实 `ego-browser` v2.2.0（用户已登录会话）+ 用户默认模型（`magpie/group/glm-flash`）。
- **隔离 Herdr server**：`XDG_CONFIG_HOME`/`HERDR_CONFIG_PATH` 指向 `/tmp/sx-*`，独立 socket；独立 SQLite、工作目录、插件 state。用户的 live Herdr（`~/.config/herdr`）、Solco 数据和真实 Assistants 未触碰。
- 入口：`SOLCO_LIVE_X=1 go test -tags live -run TestLiveXTimelineDigest -timeout 20m ./internal/personalagent`（`live_x_test.go`，默认跳过）。

## 链路（均由真实组件完成）

1. Task `requiredSkills=[ego-browser]` + 用户授权的 skill grant。
2. Dispatcher → Herdr 新 workspace → OMP（无 `--model`，用户默认模型）。
3. Worker 的 transcript 证明加载并使用了 `ego-browser`（`SKILLS=[ego-browser]`，`TOOLS=bash:6 read:3`）。
4. Solco 从固定 `--session-dir` 的 OMP transcript 读回结果：`Status: done`，Sources 27 条全部 `grounded=true`（`https://x.com/<user>/status/<id>`）。
5. 只读：Worker 报告只有 tab 点击与滚轮，无发帖/回复/点赞/转发/设置动作；结果中不含 cookie/token/账号标识。

## 结果摘要（不含时间线正文）

- 190 条原始 DOM 捕获 → 27 条去重后的帖子（For you 15、Following 13、跨 tab 重复 1）。
- 按主题分组，每项带链接与互动数；"Blockers: None"。

## 结论 / 未覆盖

- PASS：真实 Worker 通过 ego-browser 技能完成、证据可追溯（W05 的"真实站点"子项，Mac 侧 Worker 路径）。
- 未覆盖：这条是直接 Task 路径；"定时 Routine → 审批 → 触发"在受控页上已通过 e2e，真实站点 + Routine 组合尚未跑；Mac.app GUI 与 Android 真机展示尚未验收。
