package server

import (
	"net/http"
	"strings"

	"personal-ai-gateway/internal/domain"
)

// 钱包管理面(admin):给用户充值 / 调整余额 / 设售价倍率覆盖。
// 用户自助面(/me/*)见 me.go。

// handleUserTopup 给指定用户充值(正)/扣减(负)。
func (s *Server) handleUserTopup(w http.ResponseWriter, r *http.Request) {
	id, ok := paramID(r, "id")
	if !ok {
		apiErr(w, http.StatusBadRequest, "validation", "bad user id")
		return
	}
	u, _, err := s.st.AdminByID(id)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if u.Role != domain.RoleUser {
		apiErr(w, http.StatusBadRequest, "validation", "only customer accounts have a wallet")
		return
	}
	var req domain.TopupReq
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Amount == 0 {
		apiErr(w, http.StatusBadRequest, "validation", "amount must be non-zero")
		return
	}
	rec, err := s.st.TopupBalance(id, req.Amount, strings.TrimSpace(req.Note))
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// handleUserBalanceLogs 某用户的账变流水(管理员审计)。
func (s *Server) handleUserBalanceLogs(w http.ResponseWriter, r *http.Request) {
	id, ok := paramID(r, "id")
	if !ok {
		apiErr(w, http.StatusBadRequest, "validation", "bad user id")
		return
	}
	logs, err := s.st.ListBalanceLogs(id, queryInt(r, "limit", 50))
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, logs)
}

// handleUserCeiling 设某用户名下令牌的额度/RPM 上限(0 = 不限)。
// 普通用户自助建/改令牌时不得超过此值,避免把子预算设成「不限」绕开约束。
func (s *Server) handleUserCeiling(w http.ResponseWriter, r *http.Request) {
	id, ok := paramID(r, "id")
	if !ok {
		apiErr(w, http.StatusBadRequest, "validation", "bad user id")
		return
	}
	u, _, err := s.st.AdminByID(id)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if u.Role != domain.RoleUser {
		apiErr(w, http.StatusBadRequest, "validation", "only customer accounts have a token ceiling")
		return
	}
	var req domain.CeilingInput
	if !decodeBody(w, r, &req) {
		return
	}
	if req.QuotaUsd < 0 || req.RpmLimit < 0 {
		apiErr(w, http.StatusBadRequest, "validation", "ceiling must be non-negative (0 = unlimited)")
		return
	}
	if err := s.st.SetTokenCeiling(id, req.QuotaUsd, req.RpmLimit); err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "quotaUsd": req.QuotaUsd, "rpmLimit": req.RpmLimit})
}
