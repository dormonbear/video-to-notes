# iOS 快捷指令投递抖音链接

手机上复制抖音分享口令 → 快捷指令 POST 到 `/ingest` 端点 → 自动下载、解析、发文章。进度和结果照常在 Telegram 看。

**已部署**：端点公网地址 `https://video2note.hk.dormon.net/ingest`

链路：
```
快捷指令 ──HTTPS──▶ Cloudflare DNS ──▶ dormon-hk OpenResty(:443)
            └─ proxy_pass 127.0.0.1:18787 (frps)
               └─ frp TLS 隧道 ──▶ mbp-server :8787 /ingest
                  └─ 入队 → worker → Gemini → 博客 → Telegram 回执
```

> 公网暴露走 dormon-infra 既有的 frp → dormon-hk 边缘（与 FreshRSS/RSSHub 同一条链路），命名遵循 `<business>.<region>.<domain>` → `video2note.hk.dormon.net`（dormon.net=Cloudflare 境外边缘）。

---

## 服务端（已配置，备查）

`/opt/video-to-notes/.env`：
```env
API_ADDR=:8787
API_TOKEN=<32 字节随机串，openssl rand -hex 32>
NOTIFY_CHAT_ID=<你和 bot 私聊的 chat id>
```

- mbp frpc：`/opt/frp/frpc.toml` 加 `video2note` 隧道（localPort 8787 → remotePort 18787）
- dormon-hk：`conf.d/video2note.hk.dormon.net.conf`（acme dns_cf 证书 + proxy 到 127.0.0.1:18787）
- Cloudflare DNS：A `video2note.hk.dormon.net → 156.251.176.16`（proxied=off）

容器 `network_mode: host`，日志应有 `ingest API listening on :8787`。

---

## iOS 快捷指令

导入已签名的快捷指令文件即可（iOS 不允许导入未签名文件，故必须用 macOS `shortcuts sign` 签过）：

1. 生成 / 重新签名（token、URL 已内置）：
   ```bash
   shortcuts sign --mode anyone \
     --input  抖音转笔记.shortcut \
     --output 抖音转笔记-signed.shortcut
   ```
2. 把 `抖音转笔记-signed.shortcut` AirDrop 到 iPhone（或经 iCloud Drive）→ 打开 → 添加。

**用法（两条路任选，至少一条有口令即可）**：
- 抖音 App → 视频右下「分享」→ 滑到底「更多」→ 选「抖音转笔记」，或
- **复制**分享口令后直接运行快捷指令。

### 快捷指令内部（3 个动作）

1. **文本** = `「快捷指令输入」 + 「剪贴板」`（两个变量中间一个空格）。
   合并两源是关键：从分享触发时「快捷指令输入」有值；直接运行时「剪贴板」有值。空的那源只是多个空格，不影响提链接。
2. **获取 URL 内容**：`POST https://video2note.hk.dormon.net/ingest`
   - 请求头 `Authorization: Bearer <API_TOKEN>`
   - 请求体类型 **表单（Form）**，字段 `text` = 上一步的「文本」。
     （不要用「文件」body——它不发送文本变量，会发出空 body。）
3. **显示通知** = 上一步结果，看到 `✅ 已加入队列` 即成功。

---

## 端点契约

```
POST /ingest
Authorization: Bearer <API_TOKEN>
Body（取第一个非空）：表单字段 text → 表单字段 q → 原始 body
     —— 任意含抖音 / Twitter / 网页链接的文本，分享口令也行，自动提取分类

200  ✅ 已加入队列（N 个链接），进度看 Telegram
400  no supported link found
401  unauthorized
405  method not allowed
```

- 一条请求可含多个链接，全部入队；完全相同的链接在本次请求内去重。
- **视频级去重**：worker 下载拿到视频 id 后、Gemini 分析前，若 vault 已有 `*-douyin-{id}.md`（任意日期）则跳过——挡住同一视频的不同链接形式与跨天重复提交，回执 `ℹ️ 该视频已发布过，跳过`，不重复花费 / 发布。
- 进度/结果发到 `NOTIFY_CHAT_ID` 的 Telegram 会话，与直接发给 bot 完全一致。
