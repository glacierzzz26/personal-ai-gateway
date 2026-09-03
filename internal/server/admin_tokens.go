package server

import (
	"net/http"
	"strings"
	"time"

	"personal-ai-gateway/internal/auth"
	"personal-ai-gateway/internal/domain"
)

// handleTokensList 令牌列表。
func (s *Server) handleTokensList(w http.ResponseWriter, r *http.Request) {
	tokens, err := s.st.ListTokens()
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if tokens == nil {
		tokens = []domain.TokenRead{}
	}
	writeJSON(w, http.StatusOK, tokens)
}

// handleTokensCreate 新建令牌:明文 key 仅此响应出现一次,后续只剩掩码。
func (s *Server) handleTokensCreate(w http.ResponseWriter, r *http.Request) {
	var in domain.TokenInput
	if !decodeBody(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		apiErr(w, http.StatusBadRequest, "validation", "token name is required")
		return
	}
	if in.AllowedModels == nil {
		in.AllowedModels = []string{"*"}
	}
	if in.ExpiresAt != nil && *in.ExpiresAt != "" {
		if _, ok := parseDateLoose(*in.ExpiresAt); !ok {
			apiErr(w, http.StatusBadRequest, "validation", "expiresAt must be RFC3339 or YYYY-MM-DD")
			return
		}
	}
	in.Defaults()
	plain, hashed, err := auth.NewModelKey()
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	tr, err := s.st.CreateToken(in.Name, in.AllowedModels, in.QuotaUsd, in.RpmLimit,
		in.ExpiresAt, hashed, domain.MaskKey(plain))
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"key": plain, "token": tr})
}

// handleTokensUpdate 更新令牌(名称/允许模型/额度/限速/有效期/启停)。
func (s *Server) handleTokensUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := paramID(r, "id")
	if !ok {
		apiErr(w, http.StatusBadRequest, "validation", "bad token id")
		return
	}
	var in domain.TokenInput
	if !decodeBody(w, r, &in) {
		return
	}
	tr, err := s.st.UpdateToken(id, in)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tr)
}

// handleTokensDelete 删除令牌。
func (s *Server) handleTokensDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := paramID(r, "id")
	if !ok {
		apiErr(w, http.StatusBadRequest, "validation", "bad token id")
		return
	}
	if err := s.st.DeleteToken(id); err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// parseDateLoose 宽松日期解析(对齐 gateway 端)。
func parseDateLoose(s string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
