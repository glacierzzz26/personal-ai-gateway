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
	if _, err := s.st.CreateAdmin(strings.TrimSpace(req.Username), hash); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": map[string]any{"type": "conflict", "message": "username taken"},
			})
			return
		}
		writeStoreErr(w, err)
		return
	}
	s.loginAs(w, strings.TrimSpace(req.Username), req.Password, true)
}

// handleLogin 会话登录:校验密码后建会话。
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
	_, _, err := s.st.AdminByUsername(req.Username)
	if errors.Is(err, store.ErrUnauthorized) || err != nil {
		// 统一提示,避免枚举用户名
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"error": map[string]any{"type": "unauthorized", "message": "invalid username or password"},
		})
		return
	}
	s.loginAs(w, req.Username, req.Password, false)
}

func (s *Server) loginAs(w http.ResponseWriter, username, password string, expectBootstrap bool) {
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
	raw, hashed, err := auth.NewSessionToken()
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if _, err := s.st.CreateSession(admin.ID, hashed, auth.SessionTTL); err != nil {
		writeStoreErr(w, err)
		return
	}
	setSessionCookie(w, raw, int(auth.SessionTTL/time.Second))
	writeJSON(w, http.StatusOK, domain.MeResp{ID: admin.ID, Username: admin.Username, CreatedAt: admin.CreatedAt})
}

// handleLogout 删除当前会话并清 cookie(失败也照清)。
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(auth.SessionCookie); err == nil && c.Value != "" {
		_ = s.st.DeleteSession(auth.HashSecret(c.Value))
	}
	setSessionCookie(w, "", -1)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.currentAdmin(r))
}

func setSessionCookie(w http.ResponseWriter, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookie,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   maxAge,
	})
}
