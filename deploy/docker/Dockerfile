# video-to-notes bot 运行镜像：Go 静态二进制 + yt-dlp + ffmpeg + git
FROM python:3.12-slim-bookworm

RUN set -eux; \
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
