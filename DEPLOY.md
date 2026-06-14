# 部署到国内 VPS

架构：国内 VPS 跑 bot → yt-dlp 国内直连下载抖音 → OpenRouter 客户端经 VPS 上的代理调 Gemini → 笔记写进一个 git 仓库并自动 push → 本机 Obsidian 用 Git 插件拉。

```
Telegram ──► bot(VPS, 国内IP)
               ├─ yt-dlp 下载抖音        (直连，不走代理)
               ├─ ffmpeg 抽音频
               ├─ OpenRouter→Gemini       (走 OPENROUTER_PROXY)
               └─ 写 .md → git push ──► 远程仓库 ──► 本机 Obsidian(Git 插件 pull)
```

关键点：**只有 OpenRouter 请求走代理**（`OPENROUTER_PROXY`），yt-dlp 不走代理。所以 **千万不要在 systemd 里设全局 `HTTP_PROXY`/`HTTPS_PROXY`**，否则抖音下载也被代理、必失败。

## 1. 准备笔记 git 仓库

新建一个私有仓库（GitHub/Gitee），只放视频笔记：

```bash
# 本机：建仓并让 Obsidian 把它当作 vault 内的一个文件夹（或独立 vault）
git init video-notes-vault && cd video-notes-vault
mkdir video-notes && git add -A && git commit -m "init" 
git remote add origin git@github.com:<you>/video-notes-vault.git && git push -u origin main
```

本机 Obsidian 装 **Obsidian Git** 插件，设置自动 pull（如每 5 分钟），vault 指向这个仓库（或把它放进现有 vault）。

## 2. VPS 准备

```bash
# 依赖
sudo apt update && sudo apt install -y ffmpeg git python3-pip
sudo pip3 install -U yt-dlp        # 或用官方二进制
yt-dlp --version && ffmpeg -version | head -1

# 用户与目录
sudo useradd -r -m -d /opt/video-to-notes video2notes
sudo mkdir -p /opt/video-to-notes && sudo chown video2notes:video2notes /opt/video-to-notes
```

clone 笔记仓库到 VPS，并配置 push 凭证（部署密钥/PAT），确保 `video2notes` 用户能 push：

```bash
sudo -u video2notes git clone git@github.com:<you>/video-notes-vault.git /opt/video-to-notes/vault
# 配 git 身份（commit 用）
sudo -u video2notes git -C /opt/video-to-notes/vault config user.name "video2notes"
sudo -u video2notes git -C /opt/video-to-notes/vault config user.email "bot@localhost"
```

## 3. 代理

在 VPS 上跑你的代理（Clash/mihomo 等），暴露一个本地 HTTP 端口（如 7890）。验证从 VPS 经代理能访问 OpenRouter：

```bash
HTTPS_PROXY=http://127.0.0.1:7890 curl -s -o /dev/null -w "%{http_code}\n" \
  -H "Authorization: Bearer $OPENROUTER_API_KEY" \
  https://openrouter.ai/api/v1/credits   # 期望 200
```

> 不想配代理？把 `MODEL=z-ai/glm-4.6v`、`OPENROUTER_PROXY=direct`，国内直连即可（模型换成 GLM）。

## 4. 部署二进制与配置

本机交叉编译（已在 `dist/`，按 VPS 架构选 amd64/arm64）：

```bash
GOOS=linux GOARCH=amd64 go build -o dist/video-to-notes-linux-amd64 .
scp dist/video-to-notes-linux-amd64 vps:/opt/video-to-notes/video-to-notes
scp deploy/.env.server.example vps:/opt/video-to-notes/.env   # 然后上去填值
scp deploy/video-to-notes.service vps:/etc/systemd/system/video-to-notes.service
```

在 VPS 上编辑 `/opt/video-to-notes/.env` 填入 token、key、代理端口等（见模板注释），并 `chmod 600 .env && chown video2notes .env`。

## 5. 启动

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now video-to-notes
sudo systemctl status video-to-notes
journalctl -u video-to-notes -f          # 看日志
```

给 bot 发一条抖音口令 → 几十秒后笔记 push 到仓库 → 本机 Obsidian Git 自动 pull 出现笔记。

## 排查

- bot 起不来：`journalctl -u video-to-notes -e`，多半是 .env 缺值。
- 下载失败：确认没设全局 HTTP_PROXY；`sudo -u video2notes yt-dlp <链接>` 手测。
- 分析失败 403/区域：代理没生效，检查 `OPENROUTER_PROXY` 与 VPS 代理端口。
- git 同步失败：`sudo -u video2notes git -C /opt/video-to-notes/vault push` 手测凭证。
