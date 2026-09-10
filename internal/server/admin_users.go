package server

import (
	"net/http"
	"strings"
	"time"

	"personal-ai-gateway/internal/auth"
	"personal-ai-gateway/internal/domain"
	"personal-ai-gateway/internal/store"
)

// handleUsersList 账号列表(管理员)。
func (s *Server) handleUsersList(w http.ResponseWriter, r *http.Request) {
	users, err := s.st.ListUsers()
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if users == nil {
		users = []domain.UserRead{}
	}
	writeJSON(w, http.StatusOK, users)
}

// handleUsersCreate 新建账号(管理员设初始密码,role 默认 user)。
func (s *Server) handleUsersCreate(w http.ResponseWriter, r *http.Request) {
	var req domain.UserCreateReq
	if !decodeBody(w, r, &req) {
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" || len(req.Password) < minPasswordLen {
		apiErr(w, http.StatusBadRequest, "validation", "username required and password must be at least 8 chars")
		return
	}
	if req.Role == "" {
		req.Role = domain.RoleUser
	}
	if !req.Role.Valid() {
		apiErr(w, http.StatusBadRequest, "validation", "role must be admin or user")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	u, err := s.st.CreateAdmin(req.Username, hash, req.Role)
	if err != nil {
		writeStoreErr(w, err) // ErrConflict → 409
		return
	}
	writeJSON(w, http.StatusOK, domain.UserRead{
		ID: u.ID, Username: u.Username, Role: u.Role, KeyCount: 0, CreatedAt: u.CreatedAt,
	})
}

// handleUserResetPassword 管理员重置他人密码(无需旧密码)。
func (s *Server) handleUserResetPassword(w http.ResponseWriter, r *http.Request) {
	id, ok := paramID(r, "id")
	if !ok {
		apiErr(w, http.StatusBadRequest, "validation", "bad user id")
		return
	}
	if id == s.currentAdmin(r).ID {
		apiErr(w, http.StatusBadRequest, "validation", "use change-password for your own account")
		return
	}
	var req domain.PasswordResetReq
	if !decodeBody(w, r, &req) {
		return
	}
	if len(req.NewPassword) < minPasswordLen {
		apiErr(w, http.StatusBadRequest, "validation", "password must be at least 8 chars")
		return
	}
	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if err := s.st.UpdateAdminPassword(id, hash); err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleUserDelete 删除账号(其令牌级联删除)。守卫:不能删自己 / 不能删最后一个管理员。
func (s *Server) handleUserDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := paramID(r, "id")
	if !ok {
		apiErr(w, http.StatusBadRequest, "validation", "bad user id")
		return
	}
	if id == s.currentAdmin(r).ID {
		apiErr(w, http.StatusBadRequest, "validation", "cannot delete yourself")
		return
	}
	target, _, err := s.st.AdminByID(id)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if target.Role == domain.RoleAdmin {
		n, err := s.st.CountAdminsByRole(domain.RoleAdmin)
		if err != nil {
			writeStoreErr(w, err)
			return
		}
		if n <= 1 {
			apiErr(w, http.StatusConflict, "conflict", "cannot delete the last admin")
			return
		}
	}
	if err := s.st.DeleteUser(id); err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleChangeOwnPassword 改自己密码(验旧密码);成功后重签 cookie 免得本机掉线。
func (s *Server) handleChangeOwnPassword(w http.ResponseWriter, r *http.Request) {
	me := s.currentAdmin(r)
	admin, hash, err := s.st.AdminByID(me.ID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	var req domain.PasswordChangeReq
	if !decodeBody(w, r, &req) {
		return
	}
	if !auth.CheckPassword(hash, req.OldPassword) {
		writeStoreErr(w, store.ErrUnauthorized)
		return
	}
	if len(req.NewPassword) < minPasswordLen {
		apiErr(w, http.StatusBadRequest, "validation", "password must be at least 8 chars")
		return
	}
	newHash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if err := s.st.UpdateAdminPassword(admin.ID, newHash); err != nil {
		writeStoreErr(w, err)
		return
	}
	// 密码已变 → pv 变 → 旧 JWT 全失效;给当前设备重签一张,免得自己被动登出。
	token, err := auth.IssueSession(admin.ID, admin.Username, admin.Role, newHash)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	setSessionCookie(w, r, token, int(auth.SessionTTL/time.Second))
	writeJSON(w, http.StatusOK, domain.MeResp{
		ID: admin.ID, Username: admin.Username, Role: admin.Role, CreatedAt: admin.CreatedAt,
	})
}
