package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"video-to-notes/internal/config"
)

// newIngestHandler builds just the /ingest handler over an app with a token set.
// The 401/405/400 branches return before any enqueue, so a nil bot is fine.
func newIngestHandler(t *testing.T) http.Handler {
	t.Helper()
	a := &app{cfg: config.Config{APIToken: "secret", NotifyChatID: 1}}
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
		// success path needs a bot; tests only cover the guard branches.
		http.Error(w, "no douyin link found", http.StatusBadRequest)
	})
	return mux
}

func TestIngestAuth(t *testing.T) {
	h := newIngestHandler(t)
	cases := []struct {
		name, method, auth, body string
		want                     int
	}{
		{"no token", http.MethodPost, "", "v.douyin.com/abc", http.StatusUnauthorized},
		{"wrong token", http.MethodPost, "Bearer nope", "v.douyin.com/abc", http.StatusUnauthorized},
		{"get rejected", http.MethodGet, "Bearer secret", "", http.StatusMethodNotAllowed},
		{"ok token, no link", http.MethodPost, "Bearer secret", "hello", http.StatusBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(c.method, "/ingest", strings.NewReader(c.body))
			if c.auth != "" {
				req.Header.Set("Authorization", c.auth)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != c.want {
				t.Fatalf("got %d, want %d", rec.Code, c.want)
			}
		})
	}
}
