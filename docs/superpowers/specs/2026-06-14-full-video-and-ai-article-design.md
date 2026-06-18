# 迭代设计：整段视频解析 + AI 二次加工文章

**日期**：2026-06-14
**目标**：bot 从「只抽音频 + 输出逐字稿」升级为「解析整段视频（画面+语音）+ 输出 AI 二次创作的成稿文章」。

---

## 背景与约束

上一版退化为只抽音频，是因为整段视频 base64 超过 Gemini ~20MB 内联上限报 HTTP 500。本次要恢复视频理解，必须解决两个硬约束：

1. **provider 路由**：OpenRouter 官方文档明确——
   - Google Gemini (AI Studio)：`video_url` 只接受 YouTube 链接，不收 base64。
   - Google Gemini (Vertex AI)：不支持视频 URL，**必须用 base64 data URL**。
   → 因此请求体必须强制 `provider: {order: ["google-vertex"], allow_fallbacks: false}`。
2. **大小**：base64 内联仍受 ~20MB 限制。用 ffmpeg 压制到 <15MB。Gemini 视频理解默认按 **1fps 采样**，所以 `-r 1` 不损失模型可见信息，且让体积与时长解耦（长视频也安全）。

## 改动范围

### 1. `internal/douyin/douyin.go` — 产出压缩 mp4 而非 mp3
- 下载后用 ffmpeg 转码：`scale=-2:480 -r 1 -c:v libx264 -crf 28 -c:a aac -b:a 40k -ac 1 -pix_fmt yuv420p -movflags +faststart`。
- 大小兜底：若输出 > 18MB，用更激进参数（360p / crf 32）重压一次。
- 返回 `.mp4` 路径，删除原始视频。函数签名不变（返回 media 路径）。

### 2. `internal/llm/llm.go` — 发视频 + 强制 Vertex
- content part 由 `input_audio` 改为 `video_url`，`url = "data:video/mp4;base64,..."`。
- 请求体加 `provider: {order:["google-vertex"], allow_fallbacks:false}`。
- `noteSchema()` 字段：`title, summary, tags, article`（去掉 `key_points, transcript`）。
- 解析结构体同步。

### 3. `internal/prompt/prompt.go` — 新任务描述
- 指示模型：观看完整视频（画面+语音），写一篇**结构化、可独立阅读的中文文章**（二次创作整理，融合画面中的关键信息/演示/图表），用 markdown `##` 分节；**不要输出逐字稿**。
- 字段：title / summary / tags / article。

### 4. `internal/note/note.go` — 渲染成稿
- `Data` 字段：`Title, Summary, Tags, Article`（删 `KeyPoints, Transcript`）。
- `renderBlog` body：来源行 + `Article`（summary 仅进 frontmatter description）。
- `renderObsidian` body：`## 一句话摘要` + summary + `Article`。

### 5. `main.go` — selftest 子命令（测试用）
- `video-to-notes selftest <douyin-url>`：跑完整 Fetch→Analyze→Write，打印生成的 markdown 与文件路径，不连 Telegram。用于在服务器（国内 IP）真实验证整条链路。

## 测试策略
- **单元测试**（`note_test.go`）：改为断言 body 含 `Article`、不含「完整转写」。`renderBlog`/`renderObsidian` 用新 `Data`。
- **构建**：`go build ./...` 绿。
- **端到端**：服务器容器内 `selftest` 跑一个真实抖音链接，确认：视频成功下载+压制 <15MB、Vertex 返回成稿文章（非逐字稿）、笔记落盘。
- **发布**：交叉编译 linux/amd64 → 替换二进制 → `docker compose up -d --build` → bot 正常起。

## 成本
gemini-2.5-flash on Vertex，视频按 1fps 采样：约 258 tok/帧。10 分钟视频 ≈ 600 帧 ≈ 155k input tokens ≈ 约 $0.05/条。Flash 单价低，可接受。

---

## 实测结论（2026-06-14 落地）

链路本身完全跑通：5:39 抖音视频 → 压制 3.74MB mp4 → Gemini(Vertex) 解析整段视频 → 产出结构化中文成稿文章（治理/模型/知识库/协同/数据安全/规划分节，非逐字稿）。

**真正的拦路虎是 China→OpenRouter 的代理出口对大传输极不稳定**，排查链：
1. 整段视频 base64 ≈ 5MB 请求体，经代理上传时被中断（EOF / `RemoteDisconnected`）。小请求（几百字节）稳定，>1MB 开始大量失败，且**随时间剧烈波动**（同一节点 30 分钟内从可用变全挂）。
2. **节点层面**：AI 组用的 IEPL/高级节点全部走 `obfs(http)` 插件，扛不住持续大上传；用户自有的 `dormon-us-ny`、`dormon-hz-akamai`（vless+reality，干净）在好窗口可用，其中 **dormon-us-ny 最稳**。
3. **客户端层面**：Go `net/http` 默认对 https 协商 HTTP/2，大上传的 h2 分帧经这条隧道被损坏（`tls: bad record MAC`）；python urllib（HTTP/1.1）相对更稳。

**最终修复（三层）**：
- 网络路由：mihomo 新增专用 `OpenRouter` select 组（默认 `dormon-us-ny`，备选 `dormon-hz-akamai`、`AI`），把两条 `openrouter.ai` 规则从 `AI` 改指向它。配置已持久化（备份 `config.yaml.bak.video.*` / `.bak.reorder.*`）。面板可手动切备选节点。
- 客户端 `llm.New`：`ForceAttemptHTTP2=false` + `TLSNextProto={}` 强制 HTTP/1.1；`DisableKeepAlives=true` 每次新连接（避免复用半死连接）。
- `llm.Analyze`：整个请求/响应/解析包进 4 次重试（读取截断、空响应、非 200、解析失败均重试），新连接 + 退避，抗瞬时抖动。

**残留风险**：节点波动是外部网络问题，非代码可根治。当前 4 次重试 + 单节点（dormon-us-ny）在好窗口稳定；遇长时间坏窗口仍可能失败。后续如需更强鲁棒性，可做「失败后经 mihomo 控制器自动切下一个节点重试」的多节点 failover。
