package main

import (
	"embed"
	"encoding/json"
	"net/http"
)

//go:embed web/index.html
var webFS embed.FS

func setSecurityHeaders(w http.ResponseWriter) {
	// Vue 完整构建（运行时模板编译）需要 unsafe-eval；页面本身零外部资源，
	// default-src 'self' 已禁止任何 CDN / 远程字体 / 远程脚本。
	w.Header().Set("Content-Security-Policy",
		"default-src 'self'; style-src 'unsafe-inline'; script-src 'unsafe-inline' 'unsafe-eval'; "+
			"connect-src 'self'; img-src 'self' data:; frame-ancestors 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
}

func NewHandler(cfg *Config, st *State) http.Handler {
	page, err := webFS.ReadFile("web/index.html")
	if err != nil {
		panic("内嵌前端缺失：" + err.Error())
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// 只读服务：不提供任何写方法
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		switch r.URL.Path {
		case "/", "/index.html":
			setSecurityHeaders(w)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			if r.Method == http.MethodHead {
				return
			}
			_, _ = w.Write(page)
		case "/api/data":
			setSecurityHeaders(w)
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_ = json.NewEncoder(w).Encode(st.Snapshot())
		case "/healthz":
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte("ok"))
		default:
			http.NotFound(w, r)
		}
	})
	return NewAuth(cfg).Wrap(mux)
}
