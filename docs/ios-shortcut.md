# iOS 快捷指令投递抖音链接

手机上复制抖音分享口令 → 快捷指令 POST 到 bot 的 `/ingest` 端点 → 自动下载、解析、发文章。进度和结果照常在 Telegram 看。

链路：`快捷指令 → Cloudflare Tunnel(https) → mbp-server :8787 /ingest → 入队 → worker → 博客`

---

## 1. 服务端：启用 ingest 端点

在 mbp-server 的 `/opt/video-to-notes/.env` 追加（token 用 `openssl rand -hex 24` 生成，下面是已生成的一个）：

```env
API_ADDR=:8787
API_TOKEN=<你的 API_TOKEN>
NOTIFY_CHAT_ID=<你和 bot 私聊的 chat id>
```

**取 `NOTIFY_CHAT_ID`**：给 bot 随便发条消息，然后：

```bash
curl -s "https://api.telegram.org/bot<TOKEN>/getUpdates" | jq '.result[-1].message.chat.id'
```

（容器是 `network_mode: host`，绑 `:8787` 即可被本机的 cloudflared 访问。）

重新部署容器后，日志应出现 `ingest API listening on :8787`。

---

## 2. Cloudflare Tunnel：给端点一个公网 https 地址

手机在外网/蜂窝下也能用，且不在 mbp-server 上开任何入站端口。

```bash
# mbp-server 上
cloudflared tunnel login                     # 浏览器授权，选 dormon.net 域名
cloudflared tunnel create video2notes
cloudflared tunnel route dns video2notes v2n.dormon.net
```

`/etc/cloudflared/config.yml`：

```yaml
tunnel: video2notes
credentials-file: /root/.cloudflared/<tunnel-id>.json
ingress:
  - hostname: v2n.dormon.net
    service: http://localhost:8787
  - service: http_status:404
```

```bash
cloudflared service install      # 开机自启
systemctl start cloudflared
```

验证：`curl https://v2n.dormon.net/ingest` 应返回 `method not allowed`（说明通了，GET 被拒）。

> 安全：端点只靠 Bearer token 保护。token 务必保密；要更严可在 Cloudflare Access 上再加一层。

---

## 3. iOS 快捷指令

新建快捷指令，3 个动作：

1. **接收** — 顶部「分享表单」开启，接收类型选「文本」「URL」。
   （这样在抖音 App 里点分享 → 选这个快捷指令就能直接传链接。）

2. **获取 URL 内容**（Get Contents of URL）：
   - URL：`https://v2n.dormon.net/ingest`
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
