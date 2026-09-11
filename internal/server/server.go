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

// Handler 组装根路由(管理面 + 数据面 + 静态托管合并于一个 mux,dev/测试/明文口用)。
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

// HandlerAPI 数据面单独入口(TLS 数据面口用):只暴露 /healthz 与 /v1/*,其余一律 404。
// 与管理面物理隔离——数据面口永不托管管理台,管理面口永不接 /v1。
func (s *Server) HandlerAPI() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.Handle("/v1/", s.gw)
	mux.Handle("/", http.HandlerFunc(s.handleNotFound))
	return mux
}

// HandlerAdmin 管理台单独入口(TLS 管理台口用):/healthz + /api/v1/* + 静态 SPA。
// 显式把 /v1/ 指到 404——否则 GET /v1/xxx 会落到静态回退、拿 index.html 冒充 200。
func (s *Server) HandlerAdmin() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.Handle("/api/v1/", s.session(s.apiMux()))
	mux.Handle("/v1/", http.HandlerFunc(s.handleNotFound))
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
	adm := s.requireAdmin // 管理员专用;其余为「已登录即可」

	// 会话/账号:登录态即可
	m.HandleFunc("POST /api/v1/auth/bootstrap", s.handleBootstrap)
	m.HandleFunc("POST /api/v1/auth/login", s.handleLogin)
	m.HandleFunc("POST /api/v1/auth/logout", s.handleLogout)
	m.HandleFunc("GET /api/v1/auth/me", s.handleMe)
	m.HandleFunc("GET /api/v1/auth/state", s.handleAuthState)
	m.HandleFunc("POST /api/v1/auth/password", s.handleChangeOwnPassword)

	// 管理台数据:管理员专用
	m.HandleFunc("GET /api/v1/overview", adm(s.handleOverview))
	m.HandleFunc("GET /api/v1/usage", adm(s.handleUsage))

	m.HandleFunc("GET /api/v1/channels", adm(s.handleChannelsList))
	m.HandleFunc("POST /api/v1/channels", adm(s.handleChannelsCreate))
	m.HandleFunc("PATCH /api/v1/channels/{id}", adm(s.handleChannelsUpdate))
	m.HandleFunc("DELETE /api/v1/channels/{id}", adm(s.handleChannelsDelete))
	m.HandleFunc("POST /api/v1/channels/{id}/test", adm(s.handleChannelTest))
	m.HandleFunc("GET /api/v1/channels/{id}/quota", adm(s.handleChannelQuota))
	m.HandleFunc("POST /api/v1/channels/{id}/sync-models", adm(s.handleChannelSyncModels))

	// 模型目录:GET 全站可读(用户建令牌需选模型);写操作管理员专用
	m.HandleFunc("GET /api/v1/models", s.handleModelsList)
	m.HandleFunc("POST /api/v1/models", adm(s.handleModelsCreate))
	m.HandleFunc("PATCH /api/v1/models/{id}", adm(s.handleModelsUpdate))
	m.HandleFunc("DELETE /api/v1/models/{id}", adm(s.handleModelsDelete))
	m.HandleFunc("GET /api/v1/models/{id}/usage", adm(s.handleModelUsage))
	m.HandleFunc("POST /api/v1/models/{id}/offers", adm(s.handleOffersCreate))
	m.HandleFunc("PUT /api/v1/models/{id}/offers/order", adm(s.handleOffersReorder))
	m.HandleFunc("PATCH /api/v1/offers/{oid}", adm(s.handleOffersUpdate))
	m.HandleFunc("DELETE /api/v1/offers/{oid}", adm(s.handleOffersDelete))

	m.HandleFunc("GET /api/v1/rules", adm(s.handleRulesList))
	m.HandleFunc("POST /api/v1/rules", adm(s.handleRulesCreate))
	m.HandleFunc("PATCH /api/v1/rules/{id}", adm(s.handleRulesUpdate))
	m.HandleFunc("DELETE /api/v1/rules/{id}", adm(s.handleRulesDelete))
	m.HandleFunc("PUT /api/v1/rules/order", adm(s.handleRulesReorder))

	// 访问令牌:登录态即可,作用域在 handler 内按角色收束(user 只见自己名下)
	m.HandleFunc("GET /api/v1/tokens", s.handleTokensList)
	m.HandleFunc("POST /api/v1/tokens", s.handleTokensCreate)
	m.HandleFunc("PATCH /api/v1/tokens/{id}", s.handleTokensUpdate)
	m.HandleFunc("DELETE /api/v1/tokens/{id}", s.handleTokensDelete)
	m.HandleFunc("GET /api/v1/tokens/{id}/claude-config", s.handleTokenClaudeConfig)

	m.HandleFunc("GET /api/v1/logs", adm(s.handleLogsList))
	m.HandleFunc("DELETE /api/v1/logs", adm(s.handleLogsClear))

	m.HandleFunc("GET /api/v1/settings", adm(s.handleSettingsGet))
	m.HandleFunc("PATCH /api/v1/settings", adm(s.handleSettingsPatch))

	// 用户管理:管理员专用
	m.HandleFunc("GET /api/v1/users", adm(s.handleUsersList))
	m.HandleFunc("POST /api/v1/users", adm(s.handleUsersCreate))
	m.HandleFunc("PATCH /api/v1/users/{id}/password", adm(s.handleUserResetPassword))
	m.HandleFunc("DELETE /api/v1/users/{id}", adm(s.handleUserDelete))

	return m
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "store": "up", "version": s.cfg.Version})
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
