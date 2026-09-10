package server

import (
	"net/http"
	"strings"
	"time"

	"personal-ai-gateway/internal/auth"
	"personal-ai-gateway/internal/domain"
	"personal-ai-gateway/internal/secret"
	"personal-ai-gateway/internal/store"
)

// handleTokensList 令牌列表。user 只见自己名下;admin 见全部。
func (s *Server) handleTokensList(w http.ResponseWriter, r *http.Request) {
	me := s.currentAdmin(r)
	var owner *int64
	if me.Role == domain.RoleUser {
		id := me.ID
		owner = &id
	}
	tokens, err := s.st.ListTokens(owner)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if tokens == nil {
		tokens = []domain.TokenRead{}
	}
	writeJSON(w, http.StatusOK, tokens)
}

// loadManageableToken 取该账号有权管理的令牌:admin 任意;user 仅自己名下。
// 越权一律按「不存在」返回 404,避免泄露他人 key 的存在。
func (s *Server) loadManageableToken(w http.ResponseWriter, r *http.Request, param string) (domain.TokenRead, bool) {
	id, ok := paramID(r, param)
	if !ok {
		apiErr(w, http.StatusBadRequest, "validation", "bad token id")
		return domain.TokenRead{}, false
	}
	tr, err := s.st.GetToken(id)
	if err != nil {
		writeStoreErr(w, err)
		return domain.TokenRead{}, false
	}
	me := s.currentAdmin(r)
	if me.Role != domain.RoleAdmin && (tr.OwnerID == nil || *tr.OwnerID != me.ID) {
		writeStoreErr(w, store.ErrNotFound)
		return domain.TokenRead{}, false
	}
	return tr, true
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
	ne, ok := normalizeExpiresAt(in.ExpiresAt)
	if !ok {
		apiErr(w, http.StatusBadRequest, "validation", "expiresAt must be RFC3339 or YYYY-MM-DD")
		return
	}
	in.ExpiresAt = ne
	in.Defaults()
	// 归属:user 强制自己;admin 可用 ownerId 指定(校验存在),缺省为全局(NULL)。
	me := s.currentAdmin(r)
	var ownerID *int64
	if me.Role == domain.RoleUser {
		id := me.ID
		ownerID = &id
	} else if in.OwnerID != nil {
		if _, _, err := s.st.AdminByID(*in.OwnerID); err != nil {
			apiErr(w, http.StatusBadRequest, "validation", "owner does not exist")
			return
		}
		ownerID = in.OwnerID
	}
	plain, hashed, err := auth.NewModelKey()
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	cipher, err := secret.Encrypt(plain)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	tr, err := s.st.CreateToken(in.Name, ownerID, cipher, in.AllowedModels, in.QuotaUsd, in.RpmLimit,
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
	if _, ok := s.loadManageableToken(w, r, "id"); !ok {
		return
	}
	var in domain.TokenInput
	if !decodeBody(w, r, &in) {
		return
	}
	ne, ok := normalizeExpiresAt(in.ExpiresAt)
	if !ok {
		apiErr(w, http.StatusBadRequest, "validation", "expiresAt must be RFC3339 or YYYY-MM-DD")
		return
	}
	in.ExpiresAt = ne
	tr, err := s.st.UpdateToken(id, in)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tr)
}

// handleTokensDelete 删除令牌。
func (s *Server) handleTokensDelete(w http.ResponseWriter, r *http.Request) {
	tr, ok := s.loadManageableToken(w, r, "id")
	if !ok {
		return
	}
	if err := s.st.DeleteToken(tr.ID); err != nil {
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

// normalizeExpiresAt 把用户输入的宽松日期规范成库内统一口径 UTC RFC3339Nano。
// 否则管理台可存「2000-01-01」而 store 端 parseTime(RFC3339Nano)解不出 → 列表恒显 active,
// 数据面却按宽松格式判为过期,同一令牌两处结论矛盾。nil / 空串表「无有效期」,原样返回;
// 无法解析返回 ok=false,由调用方回 400。
func normalizeExpiresAt(p *string) (*string, bool) {
	if p == nil || *p == "" {
		return p, true
	}
	t, ok := parseDateLoose(*p)
	if !ok {
		return nil, false
	}
	s := t.UTC().Format(time.RFC3339Nano)
	return &s, true
}
