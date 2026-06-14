# video-to-notes bot 运行镜像：Go 静态二进制 + yt-dlp + ffmpeg + git
FROM python:3.12-slim-bookworm

RUN set -eux; \
    # 国内 apt 镜像（阿里云），避免 deb.debian.org 海外源拉取慢
    sed -i 's|deb.debian.org|mirrors.aliyun.com|g; s|security.debian.org|mirrors.aliyun.com|g' /etc/apt/sources.list.d/debian.sources 2>/dev/null || true; \
    sed -i 's|deb.debian.org|mirrors.aliyun.com|g; s|security.debian.org|mirrors.aliyun.com|g' /etc/apt/sources.list 2>/dev/null || true; \
    apt-get update; \
    apt-get install -y --no-install-recommends ffmpeg git ca-certificates; \
    rm -rf /var/lib/apt/lists/*; \
    pip install --no-cache-dir -i https://pypi.tuna.tsinghua.edu.cn/simple yt-dlp; \
    # 容器内 git：信任挂载的仓库、固定提交身份、不签名（无人值守）
    git config --system --add safe.directory '*'; \
    git config --system user.name "video2notes-bot"; \
    git config --system user.email "bot@dormon.net"; \
    git config --system commit.gpgsign false

# 预编译的 linux/amd64 静态二进制（构建前放到 build context）
COPY video-to-notes /usr/local/bin/video-to-notes
RUN chmod +x /usr/local/bin/video-to-notes

WORKDIR /vault
ENTRYPOINT ["/usr/local/bin/video-to-notes"]
