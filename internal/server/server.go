// Package server 承载 v2 网关的全部 HTTP 面。
//
// M1 先落健康检查与 JSON 404 骨架;M2 注入管理 REST(会话鉴权)+ 账号引导,
// M3 注入 /v1 模型面(令牌鉴权 + engine 转发),M4 增加静态托管。
package server

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"personal-ai-gateway/internal/config"
	"personal-ai-gateway/internal/store"
)

type Server struct {
	cfg config.Config
	st  *store.Store
	log *slog.Logger
}

func New(cfg config.Config, st *store.Store) *Server {
	return &Server{cfg: cfg, st: st, log: slog.Default()}
}

// Handler 返回根路由 mux。子面(/api /v1)在各自里程碑挂载。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("/", s.handleNotFound)
	return mux
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":    true,
		"store": "up",
	})
}

func (s *Server) handleNotFound(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusNotFound, map[string]any{
		"error": map[string]any{
			"type":    "not_found",
			"message": "no such route",
		},
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
