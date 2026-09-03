// Package server 负责 HTTP 路由与中间件组装。
// 端点全部挂在统一 key 鉴权之后;/healthz 例外。/api 命名空间为未来 Web 管理端预留。
package server

import (
	"log/slog"
	"net/http"

	"personal-ai-gateway/internal/config"
	"personal-ai-gateway/internal/proxy"
)

type Server struct {
	cfg *config.Config
	gw  *proxy.Gateway
	log *slog.Logger
}

func New(cfg *config.Config, gw *proxy.Gateway, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{cfg: cfg, gw: gw, log: log}
}

// Handler 组装完整路由。对外只放行 /healthz,其余一律过鉴权。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", s.handleHealth)

	// —— 以下全部需要统一 key 鉴权 ——
	mux.Handle("POST /v1/messages", s.auth(http.HandlerFunc(s.hMessages)))
	mux.Handle("POST /v1/messages/count_tokens", s.auth(http.HandlerFunc(s.hCountTokens)))
	mux.Handle("POST /v1/chat/completions", s.auth(http.HandlerFunc(s.hChat)))
	mux.Handle("GET /v1/models", s.auth(http.HandlerFunc(s.hModels)))
	// 兜底:未知路径给 JSON 404(协议形状按请求特征推断)
	mux.Handle("/", s.auth(http.HandlerFunc(s.handleRoot)))

	return s.accessLog(mux)
}

func (s *Server) hMessages(w http.ResponseWriter, r *http.Request) {
	s.gw.Relay(w, r, proxy.ProtoAnthropic, proxy.OpMessages)
}

func (s *Server) hCountTokens(w http.ResponseWriter, r *http.Request) {
	s.gw.Relay(w, r, proxy.ProtoAnthropic, proxy.OpCountTokens)
}

func (s *Server) hChat(w http.ResponseWriter, r *http.Request) {
	s.gw.Relay(w, r, proxy.ProtoOpenAI, proxy.OpChat)
}

func (s *Server) hModels(w http.ResponseWriter, r *http.Request) {
	s.gw.ListModels(w, protocolOf(r))
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"ok":true}`))
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	proxy.WriteError(w, protocolOf(r), http.StatusNotFound, "not_found",
		"no such endpoint: "+r.Method+" "+r.URL.Path)
}
