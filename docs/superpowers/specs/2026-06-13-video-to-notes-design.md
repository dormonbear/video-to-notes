# video-to-notes — 设计文档

日期：2026-06-13
状态：已确认设计，待写实现计划

## 目标

把「抖音分享链接 → 视频笔记」这个工作流自动化。用户把抖音分享口令发给一个**自建 Telegram bot**，bot 自动下载无水印视频、用 Gemini 2.5 Flash 解析，生成结构化 markdown 笔记并写入本地 Obsidian 库，全程一个动作。

## 关键决策（已与用户确认）

| 决策点 | 选择 | 理由 |
|---|---|---|
| 视频获取 | 自建 bot + yt-dlp 直接下载（绕开第三方 @DouYintg_bot） | 实测 yt-dlp 一秒下完无水印 720p mp4，解析不费劲 |
| 语言 | Go | 官方 Gemini SDK `google.golang.org/genai` 原生支持视频 File API；单静态二进制便于 launchd 常驻；Rust 无官方 SDK 需手撸 |
| 视频理解模型 | Gemini 2.5 Flash | 便宜、快、支持视频理解，几分钟短视频绰绰有余 |
| 笔记内容 | 一句话摘要+标签 / 核心要点 / 完整转写 | 用户选定（偏听觉文字，画面作辅助） |
| 笔记去向 | 直接写 .md 文件到本地 Obsidian vault 目录 | bot 是独立进程不能调 MCP；直写文件比 REST API 更稳、不依赖 Obsidian 开着，Obsidian 自动识别新文件 |
| 部署 | 本机 Mac 常驻进程（long-polling） | vault 在本机；long-polling 不需要公网域名/webhook |

## 实测验证（已完成）

- `yt-dlp` Douyin extractor 存在，对真实链接 `https://v.douyin.com/EklG9cO2IMQ/` 解析成功
- 拿到完整元数据（标题/作者/播放数据）与多档格式：
  - 带水印 720p 58MB（用户原先看到的"50MB+"）
  - **无水印 `h264_720p` 39MB**（最高清无水印，h264 兼容性最好）
  - 无水印 `bytevc1_720p` 15MB（同分辨率 h265 压缩，画质几乎一致）
- 实测下载 39MB mp4 成功、本地播放确认画质 OK

## 架构

单 Go 进程跑在 Mac 上，long-polling 监听 Telegram Bot API。

### 数据流

```
TG 消息(含 v.douyin.com 链接 / 抖音分享口令文本)
  → 正则提取链接(兼容"复制打开抖音…"口令文本)
  → yt-dlp 下载无水印 mp4 + 元数据(标题/作者/原链接) → 临时文件
  → Gemini File API 上传视频 → 轮询至 ACTIVE
  → 调 gemini-2.5-flash + 结构化 schema → {summary, tags[], key_points[], transcript}
  → 渲染 markdown(frontmatter + 三段) → 写入 vault 目标子文件夹
  → TG 回复 "✅ 已生成 + 一句话摘要 + 笔记相对路径"
  → 清理临时视频文件
```

进度反馈：用 editMessage 原地更新「⬇️ 下载中 → 🧠 分析中 → 📝 写入中 → ✅ 完成」。

## 模块拆分（小文件、高内聚低耦合）

```
video-to-notes/
├── main.go                 # 启动、加载 config、注册 handler、启动 long-polling
├── internal/config/        # 从 .env 加载并校验配置
├── internal/douyin/        # 提链接 + 调 yt-dlp 二进制下载 → (videoPath, Meta)；可替换模块
├── internal/gemini/        # 官方 SDK：上传视频、轮询、结构化输出 → Note 数据
├── internal/prompt/        # Gemini prompt 模板（中文系统指令）
├── internal/note/          # 渲染 markdown(frontmatter+三段) + 写入 vault
├── .env.example            # 配置模板
└── docs/superpowers/specs/ # 本设计文档
```

### 各模块接口（草案）

- `douyin.Download(shareText string) (videoPath string, meta Meta, err error)`
  - `Meta{ Title, Author, SourceURL string }`
  - 内部：正则提 URL → `exec.Command("yt-dlp", "-f", "<无水印最优>", "-j" 取元数据 + 下载)`
  - 格式选择：优先 `h264_720p` 无水印，避免 `download_addr`（带水印）
- `gemini.Analyze(ctx, videoPath string) (Note, error)`
  - `Note{ Summary string; Tags []string; KeyPoints []string; Transcript string }`
  - 用 `google.golang.org/genai` File API 上传 → 轮询 state==ACTIVE → `GenerateContent` 带 `ResponseSchema` 强制结构化 JSON
- `note.Write(meta douyin.Meta, n gemini.Note, vaultDir string) (relPath string, err error)`
  - frontmatter：`source`、`author`、`title`、`date`、`tags`
  - 正文：`## 一句话摘要` / `## 核心要点`(bullets) / `## 完整转写`
  - 文件名：`{date}-{安全化标题}.md`，写入 vault 目标子文件夹

## 笔记模板

```markdown
---
source: https://v.douyin.com/xxxx/
author: 海云日记
title: 我在大厂做Agent落地踩过的那些坑
date: 2026-06-13
tags: [agent, 面试, 大模型]
---

## 一句话摘要
{summary}

## 核心要点
- {key_point_1}
- {key_point_2}

## 完整转写
{transcript}
```

## 错误处理

- 消息中无可识别抖音链接 → 回复提示，要求发分享口令/链接
- yt-dlp 失败（解析失效/网络）→ 回复明确错误；解析逻辑隔离在 `douyin` 包，失效时只改这一处
- 视频过大或 Gemini 上传/推理失败 → 回复错误并保留日志
- vault 写入失败（路径不存在/无权限）→ 回复错误，不静默吞掉
- 原则：每步失败都通过 TG 回复显式暴露，绝不假装成功

## 测试

- `douyin`：用已验证真链接做集成测试（提链接 + 下载 + 格式选择正确）
- `gemini`：用已下载的 `/tmp/best.mp4` 跑通上传+结构化输出，断言四字段非空
- `note`：单测 markdown 渲染（frontmatter 转义、标题安全化、模板正确）
- 端到端：发链接 → vault 目标文件夹出现笔记 → 内容三段齐全

## 待提供的配置值

实现阶段需要用户提供（先在 `.env.example` 占位）：

1. `VAULT_PATH` + 目标子文件夹（Obsidian 库根路径，可由 obsidian-vault MCP 配置推断）
2. `GEMINI_API_KEY`（Google AI Studio 申请）
3. `TELEGRAM_BOT_TOKEN`（@BotFather 新建自己的 bot）

## 依赖

- 系统：`yt-dlp` 二进制（已装，version 2026.06.09）、`ffmpeg`（yt-dlp 合流时可能需要）
- Go module：
  - `google.golang.org/genai`（官方 Gemini SDK）
  - Telegram bot 库（`github.com/go-telegram/bot`，纯 Go、支持 long-polling）

## 非目标（YAGNI）

- 不做 web UI / 数据库
- 不做多用户（个人单用户工作流）
- 不自己实现抖音签名解析（交给 yt-dlp）
- 不做画面逐帧描述（用户未选；Gemini 仍会看画面辅助理解音频内容）
- 暂不上云常驻（先本机跑通；CLAUDE.md 已记录可后续迁移）
