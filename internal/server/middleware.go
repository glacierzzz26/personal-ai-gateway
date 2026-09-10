package server

import (
	"context"
	"net/http"
	"strconv"

	"personal-ai-gateway/internal/auth"
	"personal-ai-gateway/internal/domain"
	"personal-ai-gateway/internal/store"
)

type ctxKey int

const adminKey ctxKey = 1

// session 管理面会话中间件:cookie gw_session(JWT)→ 复核账号 → 进 context。
// 仅 auth/login 与 auth/bootstrap 匿名放行,其余一律要求已登录。
func (s *Server) session(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 匿名端点白名单:登录/引导,以及登录页判定用 auth/state
		if r.URL.Path == "/api/v1/auth/state" ||
			(r.Method == http.MethodPost &&
				(r.URL.Path == "/api/v1/auth/login" || r.URL.Path == "/api/v1/auth/bootstrap")) {
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

// lookupSession 校验会话 JWT,并用 AdminByID 复核角色与密码版本(pv):
// 角色变更、删号、改密码都会让旧 JWT 立即失效(登出=客户端清 cookie)。
func (s *Server) lookupSession(w http.ResponseWriter, r *http.Request) (domain.AdminUser, bool) {
	unauthorized := func(msg string) (domain.AdminUser, bool) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"error": map[string]any{"type": "unauthorized", "message": msg},
		})
		return domain.AdminUser{}, false
	}
	c, err := r.Cookie(auth.SessionCookie)
	if err != nil || c.Value == "" {
		return unauthorized("not logged in")
	}
	claims, err := auth.ParseSession(c.Value)
	if err != nil {
		return unauthorized("session expired")
	}
	id, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil {
		return unauthorized("session expired")
	}
	admin, hash, err := s.st.AdminByID(id)
	if err != nil {
		return unauthorized("session expired")
	}
	if admin.Role != claims.Role || auth.PasswordVersion(hash) != claims.PV {
		return unauthorized("session expired")
	}
	return admin, true
}

// requireAdmin 管理员专用路由闸门(须在 session 中间件之内使用)。
func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.currentAdmin(r).Role != domain.RoleAdmin {
			writeJSON(w, http.StatusForbidden, map[string]any{
				"error": map[string]any{"type": "forbidden", "message": "admin only"},
			})
			return
		}
		next(w, r)
	}
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
	case store.ErrUnauthorized:
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"error": map[string]any{"type": "unauthorized", "message": "invalid credentials"},
		})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": map[string]any{"type": "internal", "message": err.Error()},
		})
	}
}
