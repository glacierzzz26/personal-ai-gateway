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

// auth 统一 key 鉴权:通过后把 key 名/工具名放进上下文供 proxy 记账。
func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secret, ok := extractSecret(r)
		if !ok {
			unauthorized(w, r)
			return
		}
		name, ok := s.cfg.FindKey(secret)
		if !ok {
			unauthorized(w, r)
			return
		}
		info := sess.Info{KeyName: name, Tool: clip(r.UserAgent(), 100)}
		next.ServeHTTP(w, r.WithContext(sess.With(r.Context(), info)))
	})
}

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
