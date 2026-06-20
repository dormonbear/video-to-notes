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

新建快捷指令，3 个动作：

1. **接收** — 顶部「分享表单」开启，接收类型选「文本」「URL」。
   （这样在抖音 App 里点分享 → 选这个快捷指令就能直接传链接。）

2. **获取 URL 内容**（Get Contents of URL）：
   - URL：`https://video2note.hk.dormon.net/ingest`
   - 方法：`POST`
   - 请求头：`Authorization` = `Bearer <你的 API_TOKEN>`
   - 请求体：`文件` / 原始文本，内容设为「快捷指令输入」（Shortcut Input）

3. **显示通知**（可选）：内容设为上一步的结果，看到 `✅ 已加入队列` 即成功。

**用法**：抖音 App → 视频右下「分享」→ 滑到底「更多」→ 选这个快捷指令。或复制分享口令后手动运行快捷指令（口令文本里有链接即可，端点会自动提取）。

---

## 端点契约

```
POST /ingest
Authorization: Bearer <API_TOKEN>
Body: 任意含抖音链接的文本（分享口令也行，自动提取）

200  ✅ 已加入队列（N 个链接），进度看 Telegram
400  no douyin link found
401  unauthorized
405  method not allowed
```

一条请求可含多个链接，全部入队。进度/结果发到 `NOTIFY_CHAT_ID` 的 Telegram 会话，与直接发给 bot 完全一致。
