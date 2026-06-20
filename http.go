package main

import (
	"context"
	"crypto/subtle"
	"fmt"
	"io"
	"log"
	"net/http"

	"video-to-notes/internal/douyin"
)

// serveAPI 暴露一个 POST /ingest 端点：iOS 快捷指令把复制的抖音分享文本（剪贴板）
// 作为请求体投递过来，端点从中提取链接并走与 Telegram 消息完全相同的入队流程，
// 进度/结果发到 NOTIFY_CHAT_ID 对应的 Telegram 会话。
//
// 该端点仅用 Bearer token 保护，不做更多鉴权——务必绑定到内网/Tailscale 地址，
// 不要直接暴露到公网。
func (a *app) serveAPI(ctx context.Context) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ingest", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !a.authOK(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(io.LimitReader(r.Body, 64*1024))
		urls := douyin.ExtractURLs(string(body))
		if len(urls) == 0 {
			http.Error(w, "no douyin link found", http.StatusBadRequest)
			return
		}
		n := 0
		for _, u := range urls {
			if err := a.enqueue(ctx, a.cfg.NotifyChatID, u); err != nil {
				log.Printf("api enqueue: %v", err)
				continue
			}
			n++
		}
		fmt.Fprintf(w, "✅ 已加入队列（%d 个链接），进度看 Telegram", n)
	})

	srv := &http.Server{Addr: a.cfg.APIAddr, Handler: mux}
	go func() {
		<-ctx.Done()
		srv.Close()
	}()
	log.Printf("ingest API listening on %s", a.cfg.APIAddr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Printf("api server: %v", err)
	}
}

// authOK 用常量时间比较校验 Bearer token，避免计时侧信道。
func (a *app) authOK(r *http.Request) bool {
	want := "Bearer " + a.cfg.APIToken
	got := r.Header.Get("Authorization")
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}
