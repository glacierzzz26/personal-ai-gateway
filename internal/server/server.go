// Package server 承载 v2 网关的全部 HTTP 面。
//
//   - 管理面 /api/v1:会话鉴权(cookie),账号引导 bootstrap/login/logout/me,
//     渠道/模型/供给源/规则/令牌/日志/用量/概览/设置 全 CRUD(展示字段现算);
//   - 数据面 /v1:令牌鉴权 + engine 选路 + relay 真转发(由 proxy.Gateway 实现);
//   - 健康检查 /healthz 与静态托管(web-v2 构建产物)。
package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"personal-ai-gateway/internal/config"
	"personal-ai-gateway/internal/engine"
	"personal-ai-gateway/internal/proxy"
	"personal-ai-gateway/internal/store"
)

type Server struct {
	cfg config.Config
	st  *store.Store
	log *slog.Logger

	eng *engine.Engine
	gw  *proxy.Gateway
	rl  *proxy.Relay
}

func New(cfg config.Config, st *store.Store) *Server {
	eng := engine.New(st)
	rl := proxy.NewRelay(st)
	gw := proxy.NewGateway(st, eng, rl)
	return &Server{cfg: cfg, st: st, log: slog.Default(), eng: eng, gw: gw, rl: rl}
}

// Handler 组装根路由。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)

	// 数据面 /v1
	mux.Handle("/v1/", s.gw)

	// 管理面 /api/v1
	mux.Handle("/api/v1/", s.session(s.apiMux()))

	// 静态托管 web-v2/dist(SPA 回退 index.html);dist 不存在时给 404 提示。
	mux.Handle("/", s.static())
	return mux
}

// static 托管管理台构建产物。distDir=<webDir>/dist;文件命中即吐,其余回退 index.html。
func (s *Server) static() http.Handler {
	dist := s.distDir()
	fileServer := http.FileServer(http.Dir(dist))
	index := filepath.Join(dist, "index.html")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 只回退前端路由式的路径;API/模型面已在更具体的 pattern 命中,到不了这里。
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.NotFound(w, r)
			return
		}
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p != "" {
			if f, err := os.Stat(filepath.Join(dist, filepath.FromSlash(p))); err == nil && !f.IsDir() {
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		if _, err := os.Stat(index); err == nil {
			http.ServeFile(w, r, index)
			return
		}
		s.handleNotFound(w, r)
	})
}

// distDir 解析前端构建产物目录(相对 cwd 的 <webDir>/dist)。
func (s *Server) distDir() string {
	web := s.cfg.WebDir
	if web == "" {
		web = "web-v2"
	}
	return filepath.Join(web, "dist")
}

// apiMux 管理面全部子路由(仍受会话中间件约束)。
func (s *Server) apiMux() *http.ServeMux {
	m := http.NewServeMux()

	m.HandleFunc("POST /api/v1/auth/bootstrap", s.handleBootstrap)
	m.HandleFunc("POST /api/v1/auth/login", s.handleLogin)
	m.HandleFunc("POST /api/v1/auth/logout", s.handleLogout)
	m.HandleFunc("GET /api/v1/auth/me", s.handleMe)
	m.HandleFunc("GET /api/v1/auth/state", s.handleAuthState)

	m.HandleFunc("GET /api/v1/overview", s.handleOverview)
	m.HandleFunc("GET /api/v1/usage", s.handleUsage)

	m.HandleFunc("GET /api/v1/channels", s.handleChannelsList)
	m.HandleFunc("POST /api/v1/channels", s.handleChannelsCreate)
	m.HandleFunc("PATCH /api/v1/channels/{id}", s.handleChannelsUpdate)
	m.HandleFunc("DELETE /api/v1/channels/{id}", s.handleChannelsDelete)
	m.HandleFunc("POST /api/v1/channels/{id}/test", s.handleChannelTest)
	m.HandleFunc("GET /api/v1/channels/{id}/quota", s.handleChannelQuota)
	m.HandleFunc("POST /api/v1/channels/{id}/sync-models", s.handleChannelSyncModels)

	m.HandleFunc("GET /api/v1/models", s.handleModelsList)
	m.HandleFunc("POST /api/v1/models", s.handleModelsCreate)
	m.HandleFunc("PATCH /api/v1/models/{id}", s.handleModelsUpdate)
	m.HandleFunc("DELETE /api/v1/models/{id}", s.handleModelsDelete)
	m.HandleFunc("GET /api/v1/models/{id}/usage", s.handleModelUsage)
	m.HandleFunc("POST /api/v1/models/{id}/offers", s.handleOffersCreate)
	m.HandleFunc("PUT /api/v1/models/{id}/offers/order", s.handleOffersReorder)
	m.HandleFunc("PATCH /api/v1/offers/{oid}", s.handleOffersUpdate)
	m.HandleFunc("DELETE /api/v1/offers/{oid}", s.handleOffersDelete)

	m.HandleFunc("GET /api/v1/rules", s.handleRulesList)
	m.HandleFunc("POST /api/v1/rules", s.handleRulesCreate)
	m.HandleFunc("PATCH /api/v1/rules/{id}", s.handleRulesUpdate)
	m.HandleFunc("DELETE /api/v1/rules/{id}", s.handleRulesDelete)
	m.HandleFunc("PUT /api/v1/rules/order", s.handleRulesReorder)

	m.HandleFunc("GET /api/v1/tokens", s.handleTokensList)
	m.HandleFunc("POST /api/v1/tokens", s.handleTokensCreate)
	m.HandleFunc("PATCH /api/v1/tokens/{id}", s.handleTokensUpdate)
	m.HandleFunc("DELETE /api/v1/tokens/{id}", s.handleTokensDelete)

	m.HandleFunc("GET /api/v1/logs", s.handleLogsList)
	m.HandleFunc("DELETE /api/v1/logs", s.handleLogsClear)

	m.HandleFunc("GET /api/v1/settings", s.handleSettingsGet)
	m.HandleFunc("PATCH /api/v1/settings", s.handleSettingsPatch)

	return m
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "store": "up"})
}

func (s *Server) handleNotFound(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusNotFound, map[string]any{
		"error": map[string]any{"type": "not_found", "message": "no such route"},
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
