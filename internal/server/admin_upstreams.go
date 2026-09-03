// 订阅源(上游)管理:GET/POST/PUT/DELETE /api/v1/upstreams + 连通测试。
// 运行时唯一来源是 DB 的 upstreams 表(store.ReplaceUpstreams 全量落盘),
// 每次变更后把 resolved 态推给 router(Apply)与 quota manager(Apply+RefreshAll)。
package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"personal-ai-gateway/internal/config"
	"personal-ai-gateway/internal/proxy"
)

const maxUpstreamBody = 1 << 20 // 1MB,足够一条上游配置

// commitUpstreams 把整份上游列表落盘并热应用到选路/配额。
// 约定调用方已保证唯一性(重复名在 handler 里按 409 处理);返回的 error 多为校验错误(→400)。
func (s *Server) commitUpstreams(ctx context.Context, next []config.Upstream) error {
	config.ApplyUpstreamDefaults(next)
	if err := config.ValidateUpstreams(next); err != nil {
		return err
	}
	if err := s.gw.Store.ReplaceUpstreams(next); err != nil {
		return err
	}
	resolved := config.ResolveUpstreams(next)
	s.gw.Router.Apply(resolved)
	if s.qm != nil {
		s.qm.Apply(resolved)
		s.qm.RefreshAll(ctx)
	}
	return nil
}

// apiUpstreamList 列出全部订阅源;api_key 永不回显明文(env 引用除外,见 maskKey)。
func (s *Server) apiUpstreamList(w http.ResponseWriter, r *http.Request) {
	ups, err := s.gw.Store.LoadUpstreams()
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	rows := make([]map[string]any, 0, len(ups))
	for _, u := range ups {
		rows = append(rows, upstreamRow(u))
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": rows})
}

// apiUpstreamCreate 新增一个订阅源。body = 一条 config.Upstream 的 JSON(与 config.yaml 块同字段)。
func (s *Server) apiUpstreamCreate(w http.ResponseWriter, r *http.Request) {
	var req config.Upstream
	if !decodeUpstreamBody(w, r, &req) {
		return
	}
	cur, err := s.gw.Store.LoadUpstreams()
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if req.Name == "" {
		apiError(w, http.StatusBadRequest, "missing required field name")
		return
	}
	for _, u := range cur {
		if u.Name == req.Name {
			apiError(w, http.StatusConflict, "upstream name already exists: "+req.Name)
			return
		}
	}
	next := append(cur, req)
	if err := s.commitUpstreams(r.Context(), next); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	row, _ := upstreamRowByStore(s, req.Name)
	writeJSON(w, http.StatusCreated, row)
}

// apiUpstreamUpdate 修改一个订阅源(name 不可变,来自路径)。
// body = 期望的完整配置块;api_key 留空表示保持不变(不然每次改都要重贴密钥)。
func (s *Server) apiUpstreamUpdate(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var req config.Upstream
	if !decodeUpstreamBody(w, r, &req) {
		return
	}
	req.Name = name // 路径为准,忽略 body 里的 name

	cur, err := s.gw.Store.LoadUpstreams()
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	idx := -1
	for i, u := range cur {
		if u.Name == name {
			idx = i
			break
		}
	}
	if idx < 0 {
		apiError(w, http.StatusNotFound, "no such upstream: "+name)
		return
	}
	if req.APIKey == "" {
		req.APIKey = cur[idx].APIKey // 保持原密钥
	}

	next := append([]config.Upstream(nil), cur...)
	next[idx] = req
	if err := s.commitUpstreams(r.Context(), next); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	row, _ := upstreamRowByStore(s, name)
	writeJSON(w, http.StatusOK, row)
}

// apiUpstreamDelete 删除一个订阅源;最后一个不允许删(网关至少要有一个源)。
func (s *Server) apiUpstreamDelete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	cur, err := s.gw.Store.LoadUpstreams()
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	idx := -1
	for i, u := range cur {
		if u.Name == name {
			idx = i
			break
		}
	}
	if idx < 0 {
		apiError(w, http.StatusNotFound, "no such upstream: "+name)
		return
	}
	if len(cur) == 1 {
		apiError(w, http.StatusConflict, "cannot remove the last upstream")
		return
	}
	next := append(cur[:idx], cur[idx+1:]...)
	if err := s.commitUpstreams(r.Context(), next); err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// apiUpstreamTest 对某上游做连通性探测(GET /models,不打模型请求不耗配额)。
// 用 resolved 态(真实 key)而非 DB raw,才能验证密钥真的有效。
func (s *Server) apiUpstreamTest(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	for _, u := range s.gw.Router.All() {
		if u.Name == name {
			res := proxy.PingUpstream(r.Context(), *u, 5*time.Second)
			writeJSON(w, http.StatusOK, res)
			return
		}
	}
	apiError(w, http.StatusNotFound, "no such upstream: "+name)
}

func decodeUpstreamBody(w http.ResponseWriter, r *http.Request, dst *config.Upstream) bool {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxUpstreamBody))
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

func upstreamRowByStore(s *Server, name string) (map[string]any, bool) {
	ups, err := s.gw.Store.LoadUpstreams()
	if err != nil {
		return nil, false
	}
	for _, u := range ups {
		if u.Name == name {
			return upstreamRow(u), true
		}
	}
	return nil, false
}

// upstreamRow 输出单条上游;api_key 用 maskKey 掩码。
func upstreamRow(u config.Upstream) map[string]any {
	var m map[string]any
	if b, err := json.Marshal(u); err == nil {
		_ = json.Unmarshal(b, &m) // 统一经一次 JSON 往返,保证与响应字段一致
	}
	if m == nil {
		m = map[string]any{}
	}
	m["api_key"] = maskKey(u.APIKey)
	return m
}

// maskKey 回显规则:${ENV} 引用原样(env 名非敏感);字面密钥只露头尾。
func maskKey(s string) string {
	if strings.HasPrefix(s, "${") && strings.HasSuffix(s, "}") {
		return s
	}
	if len(s) <= 8 {
		return "••••"
	}
	return s[:3] + "…" + s[len(s)-4:]
}
