# 视频笔记发布到博客 + 博客依赖升级 — 设计文档

日期：2026-06-14
状态：已与用户确认方向（用户授权直接落地文档并执行）

## 背景与目标

video-to-notes bot 目前把抖音视频笔记写成 Obsidian 格式存本地 vault。用户希望改为发布到自己的博客 **dormon.net**（Astro / AstroPaper，github.com/dormonbear/dormon.net），通过博客自带的 RSS 用 Folo app 阅读。顺带升级长期未维护的博客依赖。

分两个**互相独立**的阶段，按顺序做：

- **阶段一：博客集成**（bot 侧为主）— 让 bot 输出博客文章并 push 到博客仓库。
- **阶段二：博客依赖升级**（dormon.net 侧）— 把停滞约 22 个月的依赖迁到最新。

两阶段无依赖关系；先做阶段一（低风险、见效快），再做阶段二（破坏性迁移、单独工程）。

---

## 阶段一：博客集成

### 已定决策

| 点 | 决定 |
|---|---|
| 发布位置 | 直接进主博客集合 `src/content/blog/`，用 `video-note` tag 区分（主站当前无内容，用户接受混入）|
| RSS | 复用博客现有 `rss.xml.ts`（输出 blog 全集合）→ Folo 订 dormon.net 的 RSS，无需改博客侧 |
| 发布方式 | 自动发布（`draft: false`），保留 `BLOG_DRAFT=true` 开关可改草稿门控 |
| 标题 | Gemini 额外生成简短干净标题（≤20 字）作为博客 `title`；抖音原 title 太长（常是频道简介），仅存正文备查 |
| 输出格式 | bot 增加可配置 `NOTE_FORMAT=obsidian\|blog`，默认 `obsidian`（向后兼容），本机改用 `blog` |
| 推送 | 复用已有 `internal/gitsync`：把 dormon.net 仓库当作 VAULT_PATH，`NOTE_SUBDIR=src/content/blog`，`GIT_SYNC=true` |

### 关键洞察：阶段一**不需要改博客代码**

博客的 RSS 已输出 blog 集合全部文章；只要写入的 markdown 符合 AstroPaper 的 collection schema，文章自动进站点 + RSS。所以阶段一是**纯 bot 侧**改动。

### AstroPaper blog schema（强校验，必须严格符合）

```
title: string (必填)          ← Gemini 生成的简短标题
pubDatetime: date (必填)       ← 发布时间，ISO 8601（如 2026-06-14T06:06:00Z）
description: string (必填)     ← 我们的「一句话摘要」
tags: string[] (默认 [others]) ← 模型 tags + "video-note" 标记
draft: boolean (可选)          ← 默认 false；BLOG_DRAFT 控制
author: string (默认 SITE.author)
```

> 注意：schema 不合法会让 `astro build` 整站失败。frontmatter 必须 100% 合规：`pubDatetime` 用合法 ISO 时间、`title`/`description` 非空、`tags` 为字符串数组。生成时做兜底（title 空则用 summary 截断；tags 至少含 video-note）。

### 博客文章格式

文件名（= slug，需 ASCII、唯一）：`{date}-douyin-{videoID}.md`
例：`2026-06-14-douyin-7650479446944032101.md` → `/posts/2026-06-14-douyin-7650479446944032101/`

```markdown
---
title: "Agent 落地踩坑实录"
pubDatetime: 2026-06-14T06:06:00Z
description: "本期介绍在大厂做 AI Agent 落地的经验与挑战。"
tags: ["Agent", "AI", "video-note"]
draft: false
---

> 来源：[抖音 @夜航船](https://v.douyin.com/xxxx/)

本期介绍在大厂做 AI Agent 落地的经验与挑战。

## 核心要点
- ...

## 完整转写
...
```

### 模块改动（bot 侧）

- `internal/prompt`：prompt 增加要求生成 `title`（简短标题）。
- `internal/llm`：结构化 schema 增加 `title` 字段；`note.Data` 增加 `Title`。
- `internal/douyin`：`Meta` 增加 `ID`（视频 id，用于 blog 文件名/slug）。
- `internal/note`：新增 blog 渲染器；`Write` 按格式分派（obsidian / blog）。引入 `Options{Format, Draft, Tag}`。
- `internal/config`：增加 `NOTE_FORMAT`（默认 obsidian）、`BLOG_DRAFT`（默认 false）、`BLOG_TAG`（默认 video-note）。
- `main.go`：把格式选项传给 `note.Write`，传入 `Meta.ID`。
- 复用 `internal/gitsync` push 博客仓库。

### 本机落地配置

`.env` 改为：
```
NOTE_FORMAT=blog
VAULT_PATH=/Users/dormonzhou/Projects/dormon.net
NOTE_SUBDIR=src/content/blog
GIT_SYNC=true
```
bot 写完文章 → 在 dormon.net 仓库 commit+push → 博客（Vercel/Pages）重建 → RSS 更新 → Folo 读到。

### 错误处理

- frontmatter 兜底：title 空→用 summary 前 20 字；tags 始终含 video-note；pubDatetime 始终合法 ISO。
- git push 失败：复用 gitsync 现有错误上报（Telegram 提示「同步失败」），文章已在本地仓库。

### 测试

- `note` blog 渲染单测：frontmatter 字段齐全、合法（YAML 可解析、tags 含 video-note、draft 正确）、文件名为 ASCII slug。
- 端到端：发抖音口令 → dormon.net/src/content/blog 出现合规 .md → `astro build` 不报 schema 错。

---

## 阶段二：博客依赖升级

### 现状与跨度（截至 2026-06-14）

AstroPaper 4.3.1，最后提交 2024-08-10。主要跨度：

| 依赖 | 现 | 新 | 性质 |
|---|---|---|---|
| astro | 4.13 | 6.4 | 跨 2 大版本（5 的 content layer、6 的进一步变更）|
| tailwindcss | 3.4 | 4.3 | v4 彻底重写（CSS-first，弃 config 文件，改 Vite 插件）|
| @astrojs/react | 3 | 5 | 跨 2 大版本 |
| react | 18 | 19 | 大版本 |
| eslint | 8 | 10 | flat config 强制 |
| typescript | 5.5 | 6.0 | 大版本 |

这是**跨 5 个生态的破坏性迁移**，AstroPaper 主题本身 v4→v5 也大改。不是 `pnpm update` 能搞定。

### 方法

- 开分支 `upgrade/deps`，分步迁移、每步 `pnpm build`（含 `astro check`）+ `astro dev` 验证。
- 跟随 AstroPaper 最新版的迁移路径（content layer API、Tailwind 4、配置文件位置变更）。
- 阶段一已让博客有了首批文章（video notes），升级后用它们验证 schema/渲染/RSS 不回归。
- 升级顺序建议：Astro 4→5（content layer）→ Astro 6 → @astrojs/react+react 19 → Tailwind 3→4 → ESLint 9/10 flat config → TS 6 → 收尾。
- 风险高的（Tailwind 4、ESLint flat）可独立小步提交，便于回滚。

### 风险与回滚

- 主站无内容，损失面小；每步独立提交，坏了 `git revert` 单步。
- 阶段一的 video-note frontmatter 字段（title/pubDatetime/description/tags/draft）若在新版 content layer 下语义变化，需同步调整 bot 的 blog 渲染器（跨阶段联动点，已记录）。

---

## 非目标（YAGNI）

- 阶段一不改博客 UI / 不加独立 notes 集合（用户接受混入主站，tag 区分足够）。
- 不做草稿人工审核流水线（默认自动发，留 BLOG_DRAFT 开关足矣）。
- 不在阶段一动任何依赖；升级严格留到阶段二。
