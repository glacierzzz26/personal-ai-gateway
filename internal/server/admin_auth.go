package server

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"personal-ai-gateway/internal/auth"
	"personal-ai-gateway/internal/domain"
	"personal-ai-gateway/internal/store"
)

const minPasswordLen = 8

// handleBootstrap 首启引导:库中无管理员时创建首个管理员并直接登录(建会话 cookie)。
// 已有管理员 → 409(登录页应只显示登录)。
func (s *Server) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	n, err := s.st.CountAdmins()
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if n > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": map[string]any{"type": "conflict", "message": "admin already exists, please login"},
		})
		return
	}
	var req domain.LoginReq
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Username == "" || len(req.Password) < minPasswordLen {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]any{"type": "validation", "message": "username required and password must be at least 8 chars"},
		})
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if _, err := s.st.CreateAdmin(strings.TrimSpace(req.Username), hash, domain.RoleAdmin); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": map[string]any{"type": "conflict", "message": "username taken"},
			})
			return
		}
		writeStoreErr(w, err)
		return
	}
	s.loginAs(w, r, strings.TrimSpace(req.Username), req.Password, true)
}

// handleLogin 会话登录:校验密码后签发会话 JWT。
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req domain.LoginReq
	if !decodeBody(w, r, &req) {
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" || req.Password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]any{"type": "validation", "message": "username and password required"},
		})
		return
	}
	s.loginAs(w, r, req.Username, req.Password, false)
}

func (s *Server) loginAs(w http.ResponseWriter, r *http.Request, username, password string, expectBootstrap bool) {
	admin, hash, err := s.st.AdminByUsername(username)
	if err != nil || !auth.CheckPassword(hash, password) {
		if expectBootstrap {
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"error": map[string]any{"type": "internal", "message": "bootstrap failed"},
			})
			return
		}
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"error": map[string]any{"type": "unauthorized", "message": "invalid username or password"},
		})
		return
	}
	token, err := auth.IssueSession(admin.ID, admin.Username, admin.Role, hash)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	setSessionCookie(w, r, token, int(auth.SessionTTL/time.Second))
	writeJSON(w, http.StatusOK, domain.MeResp{ID: admin.ID, Username: admin.Username, Role: admin.Role, CreatedAt: admin.CreatedAt})
}

// handleAuthState 匿名可访问:登录页需区分「首启建管理员」与「普通登录」。
func (s *Server) handleAuthState(w http.ResponseWriter, r *http.Request) {
	n, err := s.st.CountAdmins()
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"adminExists": n > 0})
}

// handleLogout 清当前会话 cookie(JWT 无状态,登出即客户端丢弃)。
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	setSessionCookie(w, r, "", -1)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	a := s.currentAdmin(r)
	writeJSON(w, http.StatusOK, domain.MeResp{ID: a.ID, Username: a.Username, Role: a.Role, CreatedAt: a.CreatedAt})
}

// setSessionCookie 写会话 cookie。Secure 依请求是否 HTTPS(TLS 直连或反代 X-Forwarded-Proto)。
func setSessionCookie(w http.ResponseWriter, r *http.Request, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookie,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   requestIsHTTPS(r),
		MaxAge:   maxAge,
	})
}

// requestIsHTTPS 判断原始请求是否经 HTTPS(TLS 直连,或反代在 X-Forwarded-Proto 声明)。
func requestIsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	proto := r.Header.Get("X-Forwarded-Proto")
	if i := strings.IndexByte(proto, ','); i >= 0 {
		proto = proto[:i]
	}
	return strings.EqualFold(strings.TrimSpace(proto), "https")
}
