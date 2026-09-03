// Package server 负责 HTTP 路由与中间件组装。
// 端点全部挂在统一 key 鉴权之后;/healthz 例外。/api 命名空间为未来 Web 管理端预留。
package server

import (
	"log/slog"
	"net/http"

	"personal-ai-gateway/internal/config"
	"personal-ai-gateway/internal/proxy"
	"personal-ai-gateway/internal/quota"
)

type Server struct {
	cfg *config.Config
	gw  *proxy.Gateway
	log *slog.Logger
	qm  *quota.Manager // 上游增删改后同步配额轮询;nil(测试)= 跳过
}

func New(cfg *config.Config, gw *proxy.Gateway, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{cfg: cfg, gw: gw, log: log}
}

// SetQuotaManager 注入配额管理器(main 在 server.New 后调用;测试可省略)。
func (s *Server) SetQuotaManager(qm *quota.Manager) { s.qm = qm }

// Handler 组装完整路由。对外只放行 /healthz,其余一律过鉴权。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", s.handleHealth)

	// —— 以下全部需要统一 key 鉴权 ——
	mux.Handle("POST /v1/messages", s.auth(http.HandlerFunc(s.hMessages)))
	mux.Handle("POST /v1/messages/count_tokens", s.auth(http.HandlerFunc(s.hCountTokens)))
	mux.Handle("POST /v1/chat/completions", s.auth(http.HandlerFunc(s.hChat)))
	mux.Handle("GET /v1/models", s.auth(http.HandlerFunc(s.hModels)))

	// —— /api 管理接口(Web 端与脚本用),与模型端点共用统一 key 鉴权 ——
	mux.Handle("GET /api/v1/usage/requests", s.auth(http.HandlerFunc(s.apiUsageRequests)))
	mux.Handle("GET /api/v1/usage/summary", s.auth(http.HandlerFunc(s.apiUsageSummary)))
	mux.Handle("GET /api/v1/quota", s.auth(http.HandlerFunc(s.apiQuota)))
	mux.Handle("GET /api/v1/upstreams", s.auth(http.HandlerFunc(s.apiUpstreamList)))
	mux.Handle("POST /api/v1/upstreams", s.auth(http.HandlerFunc(s.apiUpstreamCreate)))
	mux.Handle("PUT /api/v1/upstreams/{name}", s.auth(http.HandlerFunc(s.apiUpstreamUpdate)))
	mux.Handle("DELETE /api/v1/upstreams/{name}", s.auth(http.HandlerFunc(s.apiUpstreamDelete)))
	mux.Handle("POST /api/v1/upstreams/{name}/test", s.auth(http.HandlerFunc(s.apiUpstreamTest)))
	mux.Handle("GET /api/v1/keys", s.auth(http.HandlerFunc(s.apiKeyList)))
	mux.Handle("POST /api/v1/keys", s.auth(http.HandlerFunc(s.apiKeyCreate)))
	mux.Handle("POST /api/v1/keys/{name}/revoke", s.auth(http.HandlerFunc(s.apiKeyRevoke)))

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
