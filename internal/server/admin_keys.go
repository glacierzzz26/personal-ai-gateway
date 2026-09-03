// 运行时模型面 API key 管理:GET/POST /api/v1/keys + POST /api/v1/keys/{name}/revoke。
// 与 config 登录/管理 key 分开 —— 生成的 key 只能走模型面(/v1/*),访问 /api 一律 401。
// 明文 secret 仅在创建(201)响应出现一次;DB 只存 sha256,这里也绝不落日志。
package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"personal-ai-gateway/internal/store"
)

const maxKeyBody = 64 << 10

var keyNameRe = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

func (s *Server) apiKeyList(w http.ResponseWriter, r *http.Request) {
	rows, err := s.gw.Store.ListKeys()
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	data := make([]map[string]any, 0, len(rows))
	for _, k := range rows {
		data = append(data, keyRow(k))
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data})
}

func (s *Server) apiKeyCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
		Note string `json:"note"`
	}
	if !decodeKeyBody(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Note = strings.TrimSpace(req.Note)
	switch {
	case req.Name == "":
		apiError(w, http.StatusBadRequest, "missing required field name")
		return
	case len(req.Name) > 64:
		apiError(w, http.StatusBadRequest, "name must be at most 64 characters")
		return
	case !keyNameRe.MatchString(req.Name):
		apiError(w, http.StatusBadRequest, "name may only contain a-z A-Z 0-9 . _ -")
		return
	case len(req.Note) > 200:
		apiError(w, http.StatusBadRequest, "note must be at most 200 characters")
		return
	}
	for _, k := range s.cfg.Keys {
		if k.Name == req.Name {
			apiError(w, http.StatusConflict, "key name already exists (config): "+req.Name)
			return
		}
	}
	rows, err := s.gw.Store.ListKeys()
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, k := range rows {
		if k.Name == req.Name {
			apiError(w, http.StatusConflict, "key name already exists: "+req.Name)
			return
		}
	}

	secret := newSecret()
	k, err := s.gw.Store.CreateKey(req.Name, req.Note, secret)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			apiError(w, http.StatusConflict, "key name already exists: "+req.Name)
			return
		}
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	row := keyRow(k)
	row["secret"] = secret // 仅此一次
	writeJSON(w, http.StatusCreated, row)
}

func (s *Server) apiKeyRevoke(w http.ResponseWriter, r *http.Request) {
	k, err := s.gw.Store.RevokeKey(r.PathValue("name"), time.Now().UTC())
	if err != nil {
		if err == store.ErrKeyNotFound {
			apiError(w, http.StatusNotFound, "no such key: "+r.PathValue("name"))
			return
		}
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, keyRow(k))
}

func newSecret() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand: " + err.Error()) // 个人网关,rand 失败即环境性问题
	}
	return "sk-gw-" + hex.EncodeToString(b)
}

func keyRow(k store.ApiKey) map[string]any {
	row := map[string]any{
		"id":         k.ID,
		"name":       k.Name,
		"prefix":     k.Prefix,
		"note":       k.Note,
		"revoked":    k.Revoked,
		"created_at": k.CreatedAt.UTC().Format(time.RFC3339Nano),
		"revoked_at": nil,
	}
	if k.RevokedAt != nil {
		row["revoked_at"] = k.RevokedAt.UTC().Format(time.RFC3339Nano)
	}
	return row
}

func decodeKeyBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxKeyBody))
	if err != nil || len(body) == 0 {
		apiError(w, http.StatusBadRequest, "invalid or empty JSON body")
		return false
	}
	if err := json.Unmarshal(body, dst); err != nil {
		apiError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}
