# 博客依赖升级 Implementation Plan（阶段二）

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans. 本计划是**破坏性迁移**，每步以 `pnpm build`（含 `astro check`）为验证门，逐步提交便于回滚。Steps use checkbox (`- [ ]`).

**Goal:** 把 dormon.net（AstroPaper 4.3.1，停滞约 22 个月）的依赖迁到最新：Astro 4→6、Tailwind 3→4、@astrojs/react+React 18→19、ESLint 8→10(flat)、TypeScript 5→6，站点构建/渲染/RSS 不回归。

**Architecture:** 在分支 `upgrade/deps` 上分阶段升级，每阶段独立提交并 `pnpm build` 验证。用阶段一产出的 video-note 文章作为 content collection 的回归样本。

**Tech Stack:** Astro 6 / AstroPaper、Tailwind 4、ESLint 9+ flat config、React 19、pnpm。

> **执行前提**：阶段一已完成，`src/content/blog/` 已有至少一篇合规文章；当前 `git status` 干净。每个 Task 末尾的 `pnpm build` 必须通过才进入下一个。失败时先修该阶段错误再继续，必要时 `git revert` 单步。

---

## Task 0: 基线与分支

- [ ] **Step 1: 确认基线可构建**

```bash
cd /Users/dormonzhou/Projects/dormon.net
pnpm install
pnpm build
```
Expected: 当前（旧）依赖下构建成功。若失败，先记录基线问题再升级。

- [ ] **Step 2: 建分支**

```bash
git checkout -b upgrade/deps
```

---

## Task 1: Astro 4 → 5（content layer 迁移，最关键）

参考 AstroPaper 与 Astro 官方 v5 升级指南。Astro 5 引入 Content Layer，content collection 定义与 API 有破坏性变更。

- [ ] **Step 1: 升级 Astro 与官方集成到 5**

```bash
pnpm dlx @astrojs/upgrade
```
（该命令会把 astro 与 @astrojs/* 升到匹配 Astro 5 的版本。）若交互失败，手动：
```bash
pnpm add astro@5 @astrojs/rss@latest @astrojs/sitemap@latest @astrojs/check@latest
pnpm add -D @astrojs/react@4
```

- [ ] **Step 2: 迁移 content 配置到 content layer**

Astro 5 要求 content 配置文件位于 `src/content.config.ts`（原 `src/content/config.ts`），并用 `loader`（`glob`）定义集合：
```ts
import { glob } from "astro/loaders";
import { defineCollection, z } from "astro:content";
import { SITE } from "@config";

const blog = defineCollection({
  loader: glob({ pattern: "**/*.md", base: "./src/content/blog" }),
  schema: ({ image }) => z.object({
    author: z.string().default(SITE.author),
    pubDatetime: z.date(),
    modDatetime: z.date().optional().nullable(),
    title: z.string(),
    featured: z.boolean().optional(),
    draft: z.boolean().optional(),
    tags: z.array(z.string()).default(["others"]),
    ogImage: image().or(z.string()).optional(),
    description: z.string(),
    canonicalURL: z.string().optional(),
  }),
});

export const collections = { blog };
```
然后 `git mv src/content/config.ts src/content.config.ts` 并改成上面内容。

- [ ] **Step 3: 修复 collection API 破坏性变更**

Astro 5 中 entry 的 `slug` 改为 `id`，`render(entry)` 改为 `import { render } from "astro:content"`。在用到这些的文件里（`src/pages/posts/[slug]/`、`getStaticPaths`、`getSortedPosts.ts`、`rss.xml.ts` 等）按 build 报错逐处改：
- `entry.slug` → `entry.id`
- `entry.render()` → `const { Content } = await render(entry)`
- `getCollection("blog")` 不变

- [ ] **Step 4: 构建验证**

```bash
pnpm build
```
Expected: 通过。按报错把残余的 slug/render/类型问题改完。

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "chore: migrate to Astro 5 content layer"
```

---

## Task 2: React 18 → 19

- [ ] **Step 1: 升级**

```bash
pnpm add react@19 react-dom@19 && pnpm add -D @types/react@19 @types/react-dom@19 @astrojs/react@4
```

- [ ] **Step 2: 构建验证 + 修复**

```bash
pnpm build
```
按报错修复（React 19 移除了部分废弃 API；AstroPaper 的 React 组件很少，通常无改动）。

- [ ] **Step 3: Commit**

```bash
git add -A && git commit -m "chore: react 19"
```

---

## Task 3: Astro 5 → 6

- [ ] **Step 1: 升级到 6**

```bash
pnpm add astro@6 && pnpm dlx @astrojs/upgrade
```

- [ ] **Step 2: 构建验证 + 按 Astro 6 迁移说明修复**

```bash
pnpm build
```
按报错处理 Astro 6 的破坏性项（弃用 API、配置项更名）。逐处改到构建通过。

- [ ] **Step 3: Commit**

```bash
git add -A && git commit -m "chore: astro 6"
```

---

## Task 4: Tailwind 3 → 4（CSS-first，风险高）

Tailwind v4 弃用 `tailwind.config.cjs` + `@astrojs/tailwind`，改用 `@tailwindcss/vite` 插件与 CSS `@theme`。

- [ ] **Step 1: 换插件与依赖**

```bash
pnpm remove @astrojs/tailwind
pnpm add tailwindcss@4 @tailwindcss/vite@4
```

- [ ] **Step 2: 配置迁移**

- `astro.config.ts`：移除 `tailwind()` 集成，改在 `vite.plugins` 加 `tailwindcss()`（来自 `@tailwindcss/vite`）。
- 全局 CSS 入口：把 `@tailwind base/components/utilities` 改为 `@import "tailwindcss";`。
- 把 `tailwind.config.cjs` 里的主题/插件迁到 CSS 的 `@theme { ... }`（AstroPaper 用 CSS 变量做配色，多数可直接映射）。`@tailwindcss/typography` 改用 `@plugin "@tailwindcss/typography";`（在 CSS 中）。

- [ ] **Step 3: 构建 + 目视核对样式**

```bash
pnpm build && pnpm preview
```
打开预览，核对首页/文章页/暗色模式样式无明显回归。

- [ ] **Step 4: Commit**

```bash
git add -A && git commit -m "chore: tailwind 4 (css-first)"
```

---

## Task 5: ESLint 8 → 9/10（flat config）+ TypeScript 6

- [ ] **Step 1: 升级 lint 链**

```bash
pnpm add -D eslint@latest @typescript-eslint/parser@latest eslint-plugin-astro@latest astro-eslint-parser@latest typescript@6
```

- [ ] **Step 2: 迁移到 flat config**

新建 `eslint.config.js`（flat config），整合 `eslint-plugin-astro` 推荐配置；删除旧 `.eslintrc.*`。

- [ ] **Step 3: 验证**

```bash
pnpm lint
pnpm build
```
Expected: lint 与 build 均通过（按报错修配置）。

- [ ] **Step 4: Commit**

```bash
git add -A && git commit -m "chore: eslint flat config + typescript 6"
```

---

## Task 6: 收尾与回归

- [ ] **Step 1: 全量验证**

```bash
pnpm install && pnpm build && pnpm preview
```
核对：首页、文章列表、单篇 video-note 文章渲染、tags 页、RSS（访问 `/rss.xml`）、暗色模式、OG 图生成均正常。

- [ ] **Step 2: 确认 video-note 文章未回归**

确保阶段一产出的 `src/content/blog/*-douyin-*.md` 在新版下仍正常渲染并出现在 RSS（这是跨阶段联动点：若 content layer 语义变化导致 frontmatter 解析不同，回到 video-to-notes 的 `internal/note` 调整 blog 渲染器）。

- [ ] **Step 3: 合并**

```bash
git checkout main && git merge --no-ff upgrade/deps -m "chore: upgrade blog deps to latest"
```

---

## 说明

- 本计划的「修复」步骤天然带探索性（破坏性变更在 build 时暴露）。每个 Task 以 `pnpm build` 为硬门，按真实报错处理，逐步提交便于回滚。
- 顺序刻意：先 Astro 5 的 content layer（影响最大、最该先稳住），再 React、Astro 6，最后 Tailwind 4 与 ESLint flat（最独立、最易回滚）。
