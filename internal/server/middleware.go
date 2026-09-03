package server

import (
	"context"
	"net/http"

	"personal-ai-gateway/internal/auth"
	"personal-ai-gateway/internal/domain"
	"personal-ai-gateway/internal/store"
)

type ctxKey int

const adminKey ctxKey = 1

// session 管理面会话中间件:cookie gw_session → sessions(sha256)→ 管理员进 context。
// 仅 auth/login 与 auth/bootstrap 匿名放行,其余一律要求已登录。
func (s *Server) session(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 匿名端点白名单
		if r.Method == http.MethodPost &&
			(r.URL.Path == "/api/v1/auth/login" || r.URL.Path == "/api/v1/auth/bootstrap") {
			next.ServeHTTP(w, r)
			return
		}
		admin, ok := s.lookupSession(w, r)
		if !ok {
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), adminKey, admin)))
	})
}

func (s *Server) lookupSession(w http.ResponseWriter, r *http.Request) (domain.AdminUser, bool) {
	c, err := r.Cookie(auth.SessionCookie)
	if err != nil || c.Value == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"error": map[string]any{"type": "unauthorized", "message": "not logged in"},
		})
		return domain.AdminUser{}, false
	}
	admin, err := s.st.LookupSession(auth.HashSecret(c.Value))
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"error": map[string]any{"type": "unauthorized", "message": "session expired"},
		})
		return domain.AdminUser{}, false
	}
	return admin, true
}

// currentAdmin 从已登录 context 取管理员(仅 session 保护的 handler 内调用)。
func (s *Server) currentAdmin(r *http.Request) domain.AdminUser {
	a, _ := r.Context().Value(adminKey).(domain.AdminUser)
	return a
}

// paramID 取路径 {id}/{oid} 数值。
func paramID(r *http.Request, name string) (int64, bool) {
	v := r.PathValue(name)
	if v == "" {
		return 0, false
	}
	var id int64
	for _, c := range v {
		if c < '0' || c > '9' {
			return 0, false
		}
		id = id*10 + int64(c-'0')
	}
	return id, true
}

// writeStoreErr 把 store 哨兵错误映射成 HTTP;ErrNotFound/ErrConflict 是常见管理面错误。
func writeStoreErr(w http.ResponseWriter, err error) {
	switch err {
	case store.ErrNotFound:
		writeJSON(w, http.StatusNotFound, map[string]any{
			"error": map[string]any{"type": "not_found", "message": "resource not found"},
		})
	case store.ErrConflict:
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": map[string]any{"type": "conflict", "message": "duplicate name"},
		})
	case store.ErrQuotaExceeded:
		writeJSON(w, http.StatusPaymentRequired, map[string]any{
			"error": map[string]any{"type": "quota_exceeded", "message": "token quota exceeded"},
		})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": map[string]any{"type": "internal", "message": err.Error()},
		})
	}
}
