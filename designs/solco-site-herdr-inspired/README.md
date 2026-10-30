# Design Brief: Solco 官网参考 Herdr 改版

## Decision

| 字段 | 内容 |
|------|------|
| **Status** | `Approved 2026-10-30` |
| **Chosen** | Option B — Workflow Story |
| **组合说明** | 可组合，例如「A 的 README 目录 + B 的三步工作流」 |
| **实现授权** | 是；先在探索稿修复 Hero 背景与 footer 对齐，再实现 site/。 |

## Problem

Solco 官网应如何吸收 Herdr 的暗色编辑型排版与纵向叙事，同时保持 Solco 品牌与真实产品证据？

## Phase 0 — 项目调研摘要

### 现状

| 项 | 发现 |
|----|------|
| 渲染入口 | `site/index.html` + `site/styles.css`，无 JS、无构建。 |
| 当前视觉 | 浅灰 Apple 风：`--bg #f6f6f4`、`--ink #191b20`、sticky navbar、三列 features。 |
| 真实证据 | `assets/screens/*.webp` 六张真机图，产品 Hero 为 1600×1073。 |
| 品牌规则 | 新公羊图标只在 navbar/footer/favicon；`resources/icon.png` 是主图。 |
| Herdr 参考 | `https://herdr.dev/`：72px 导航、#0e0f11、#131416 卡片、#f3f3f4 主字、#8a8e96 次字、#1f2125 分线、120px 段落节奏、README sticky TOC、大截图与 terminal 安装块。 |
| 部署 | GitHub Pages 发布 `site/`，`.github/workflows/pages.yml`。 |

### 约束

- **必须保留**：真实截图；公羊品牌图标来源与使用位置；Solco owns presentation / Herdr owns runtime 的表述；无框架静态站点；macOS Apple Silicon 下载链接。
- **必须对齐**：采用 Herdr 的排版系统与暗色节奏，但不复制其文案、logo、安装命令或品牌资产。
- **非目标**：不做多页站、CMS、JS 框架、后端下载器、第三方字体或动画系统。
- **实现落点**：`site/index.html`、`site/styles.css`、`site/CLAUDE.md`；必要时更新根 `CLAUDE.md` 一行描述。

## Options explored

| ID | 名称 | HTML 文件 | 核心差异 | 优点 | 缺点 |
|----|------|-----------|----------|------|------|
| A | Editorial Dark | `exploration.html` Variant A | 最贴近 Herdr：大 Hero、statement、三列注解、README 目录、五个 feature section、终局 CTA、四列 footer。 | 结构最完整，编辑感和证据密度最好。 | 页面最长，TOC 在移动端隐藏。 |
| B | Workflow Story | `exploration.html` Variant B | 以三步工作流为主，正文压缩为四节，显式架构边界和 terminal 代码块。 | 认知负担最低，工作流清楚，实现风险小。 | 捕获力略弱于 A，少一个 Files 局部证据。 |
| C | Release Digest | `exploration.html` Variant C | 以 0.10 发布为核心，强调 Git Review、Devices 和 FAQ。 | 转化目标明确，新闻感强。 | 版本更替后文案维护成本高。 |

### 交互状态

| 状态 | 如何触发 | 预期行为 |
|------|----------|----------|
| Variant A / B / C | 点击右侧 Variants 卡片 | 画布切换三套完整页面方案。 |
| 全画布 | 点击右上 `›` 或无状态时按 Esc | 右侧 Storybook 面板收起。 |
| Story 折叠 | 点击 Story 标题 | 底部说明收起或展开。 |

## Preview

直接双击或拖入浏览器打开：

`designs/solco-site-herdr-inspired/exploration.html`

不需要静态服务器；所有 CSS、截图和图标均为相对本地资源。

## Success criteria

- [x] 三个完整方案可在同一 HTML 中切换。
- [x] 可在 `file://` 下打开；右侧面板与方案切换可用。
- [x] 设计 token 来自 Herdr 调研，不复制其品牌内容。
- [x] 只使用真实 Solco 截图与现有公羊图标。
- [x] 用户选定方案或组合：B，并要求 Hero 右侧增加背景图；footer 标题与 item 左对齐。
- [x] 用户授权进入实现阶段。

## Implementation handoff

| 项 | 内容 |
|----|------|
| 改动文件 | `site/index.html`, `site/styles.css`, `site/CLAUDE.md`; 如命名变化同步根 `CLAUDE.md`。 |
| 验证命令 | `git diff --check`; 本地 HTML 资源检查；GitHub Pages 部署后 HTTP 200 与关键内容检查。 |
| 浏览器检查 | 桌面 1440、窄屏 390；navbar/Hero/Features/Architecture/Download/Footer/锚点滚动。 |
