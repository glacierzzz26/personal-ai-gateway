package server

import (
	"net/http"
	"strings"

	"personal-ai-gateway/internal/proxy"
	"personal-ai-gateway/internal/sess"
)

// statusRecorder 记录响应状态码,并保证 SSE 场景下 Flush 仍可达(透传给底层)。
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// protocolOf 从请求特征推断客户端讲哪种协议,用于错误体/模型清单的返回形状。
// anthropic 客户端会带 x-api-key 或 anthropic-version;openai 客户端带 Authorization。
func protocolOf(r *http.Request) string {
	h := r.Header
	if h.Get("x-api-key") != "" || h.Get("anthropic-version") != "" {
		return proxy.ProtoAnthropic
	}
	return proxy.ProtoOpenAI
}

// extractSecret 从 x-api-key 或 Authorization: Bearer 里取统一 key。
func extractSecret(r *http.Request) (string, bool) {
	if v := r.Header.Get("x-api-key"); v != "" {
		return v, true
	}
	if v := r.Header.Get("Authorization"); strings.HasPrefix(v, "Bearer ") {
		return strings.TrimPrefix(v, "Bearer "), true
	}
	return "", false
}

// auth 统一 key 鉴权,通过后把 key 名/工具名放进上下文供 proxy 记账。
// 两层:
//   1. config key(登录/管理 key)→ 全权(Admin,管理面 + 模型面)。
//   2. DB 生成的模型面 key(api_keys 表)→ 仅 /v1/*;访问管理面(/api/* 等)一律 401,
//      与无效 key 同表现,不泄露 key 有效。鉴权失败一律 fail closed。
func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secret, ok := extractSecret(r)
		if !ok {
			unauthorized(w, r)
			return
		}
		if name, ok := s.cfg.FindKey(secret); ok {
			info := sess.Info{KeyName: name, Tool: clip(r.UserAgent(), 100), Admin: true}
			next.ServeHTTP(w, r.WithContext(sess.With(r.Context(), info)))
			return
		}
		if s.gw != nil && s.gw.Store != nil {
			if k, err := s.gw.Store.LookupActiveKey(secret); err == nil {
				if !modelPlanePath(r.URL.Path) {
					unauthorized(w, r) // 模型面 key 不许碰管理面
					return
				}
				info := sess.Info{KeyName: k.Name, Tool: clip(r.UserAgent(), 100), Admin: false}
				next.ServeHTTP(w, r.WithContext(sess.With(r.Context(), info)))
				return
			}
		}
		unauthorized(w, r)
	})
}

// modelPlanePath 判定路径是否属模型面。以 /v1/ 前缀为准,
// 天然兼容未来新增的模型端点(与 inbound 协议无关)。
func modelPlanePath(p string) bool { return strings.HasPrefix(p, "/v1/") }

// accessLog 记录每个 HTTP 请求的方法/路径/状态/耗时(结构化日志)。
func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		s.log.Info("http",
			"method", r.Method,
			"path", r.URL.Path,
			"remote", clip(r.RemoteAddr, 64),
			"status", rec.status,
		)
	})
}

func unauthorized(w http.ResponseWriter, r *http.Request) {
	proxy.WriteError(w, protocolOf(r), http.StatusUnauthorized,
		"authentication_error", "invalid or missing api key")
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
