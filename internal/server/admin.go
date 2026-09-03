package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"personal-ai-gateway/internal/proxy"
	"personal-ai-gateway/internal/store"
)

// /api/v1/usage 查询接口。当前 P2 只做查询,统一 key 鉴权,
// 供后续 Web 端与脚本使用;分页用 limit/offset(个人规模足够,见 DESIGN)。
func (s *Server) apiUsageRequests(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	f, err := parseFilter(q)
	if err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	sort := q.Get("sort")
	if sort == "" {
		sort = "id"
	}
	if !store.ValidSort(sort) {
		apiError(w, http.StatusBadRequest, fmt.Sprintf("invalid sort %q (allowed: id, ts, status, latency_ms, prompt_tokens, completion_tokens, cache_read_tokens, cost, model, upstream, client_key, protocol)", sort))
		return
	}
	order := strings.ToLower(q.Get("order"))
	if order == "" {
		order = "desc"
	}
	if order != "asc" && order != "desc" {
		apiError(w, http.StatusBadRequest, "invalid order: must be asc or desc")
		return
	}
	limit, offset, err := parseLimit(q)
	if err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}

	total, err := s.gw.Store.CountRequests(f)
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	rows, err := s.gw.Store.ListRequests(f, limit, offset, sort, order)
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}

	data := make([]map[string]any, 0, len(rows))
	for _, e := range rows {
		data = append(data, map[string]any{
			"id":               e.ID,
			"ts":               e.TS.UTC().Format(time.RFC3339Nano),
			"client_key":       e.ClientKey,
			"client_tool":      e.ClientTool,
			"protocol":         e.Protocol,
			"model":            e.Model,
			"upstream":         e.Upstream,
			"stream":           e.Stream,
			"status":           e.Status,
			"prompt_tokens":    e.PromptTokens,
			"completion_tokens": e.CompletionTokens,
			"cache_read_tokens": e.CacheReadTokens,
			"cost":             e.Cost,
			"latency_ms":       e.LatencyMs,
			"error":            e.Err,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"meta": map[string]any{
			"limit":    limit,
			"offset":   offset,
			"total":    total,
			"returned": len(rows),
		},
		"data": data,
	})
}

func (s *Server) apiUsageSummary(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	f, err := parseFilter(q)
	if err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	// summary 默认取最近 24h,否则请求极多时统计很重;可通过 from/to 覆盖。
	now := time.Now().UTC()
	if f.From == nil && f.To == nil {
		f.From = ptrTime(now.Add(-24 * time.Hour))
	}
	if f.To == nil {
		f.To = ptrTime(now)
	}

	groupBy := parseList(q.Get("group_by"))
	for _, g := range groupBy {
		if !store.ValidGroup(g) {
			apiError(w, http.StatusBadRequest,
				fmt.Sprintf("invalid group %q (allowed: model, upstream, protocol, client_key)", g))
			return
		}
	}
	bucket := q.Get("bucket")
	switch bucket {
	case "", "hour", "day":
	default:
		apiError(w, http.StatusBadRequest, "invalid bucket: must be hour or day")
		return
	}

	rows, err := s.gw.Store.UsageSummary(f, groupBy, bucket)
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	totals, err := s.gw.Store.UsageSummary(f, nil, "")
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// data:每行 = 分组维度 + 指标;未选的分组维度不出现
	data := make([]map[string]any, 0, len(rows))
	for _, rw := range rows {
		row := map[string]any{}
		if bucket != "" {
			row["bucket"] = rw.Bucket
		}
		for k, v := range rw.Groups {
			row[k] = v
		}
		addMetrics(row, rw)
		data = append(data, row)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"meta": map[string]any{
			"from":     tsOrNil(f.From),
			"to":       tsOrNil(f.To),
			"group_by": groupBy,
			"bucket":   bucket,
		},
		"totals": summaryTotals(totals),
		"data":   data,
	})
}

// apiQuota 返回各上游配额感知选路当前状态(诊断/未来 Web 用量页用)。
func (s *Server) apiQuota(w http.ResponseWriter, r *http.Request) {
	rows := make([]map[string]any, 0)
	for _, up := range s.gw.Router.All() {
		q := up.Quota
		enabled := q != nil && q.Enabled
		row := map[string]any{
			"upstream": up.Name,
			"type":     up.Type,
			"enabled":  enabled,
		}
		if !enabled {
			rows = append(rows, row)
			continue
		}
		row["window"] = q.Window
		row["warn_used_pct"] = q.WarnUsedPct
		row["hard_used_pct"] = q.HardUsedPct
		if st, ok := s.gw.Router.QuotaState(up.Name); ok {
			row["used_pct"] = st.UsedPct
			row["status"] = st.Status
			row["hard"] = st.Hard
			if !st.ResetsAt.IsZero() {
				row["resets_at"] = st.ResetsAt.UTC().Format(time.RFC3339)
			}
		} else {
			row["used_pct"] = nil
			row["status"] = nil
			row["hard"] = false
			row["resets_at"] = nil
		}
		rows = append(rows, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": rows})
}

// —— 参数解析与响应工具 ——

func parseFilter(q map[string][]string) (store.ReqFilter, error) {
	get := func(k string) string { return first(q[k]) }
	var f store.ReqFilter
	var err error

	if v := get("from"); v != "" {
		if f.From, err = parseTime(v); err != nil {
			return f, fmt.Errorf("invalid from: %v", err)
		}
	}
	if v := get("to"); v != "" {
		if f.To, err = parseTime(v); err != nil {
			return f, fmt.Errorf("invalid to: %v", err)
		}
	}
	f.Protocol, f.Model, f.Upstream, f.ClientKey = get("protocol"), get("model"), get("upstream"), get("client_key")
	if f.Protocol != "" && f.Protocol != proxy.ProtoOpenAI && f.Protocol != proxy.ProtoAnthropic {
		return f, fmt.Errorf("invalid protocol %q (allowed: openai, anthropic)", f.Protocol)
	}
	if v := get("stream"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return f, fmt.Errorf("invalid stream: must be true or false")
		}
		f.Stream = &b
	}
	if v := get("status"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 100 || n > 599 {
			return f, fmt.Errorf("invalid status %q: must be an HTTP status code", v)
		}
		f.Status = n
	}
	switch b := get("status_bucket"); b {
	case "", "2xx", "3xx", "4xx", "5xx":
		f.StatusBucket = b
	default:
		return f, fmt.Errorf("invalid status_bucket %q (allowed: 2xx, 3xx, 4xx, 5xx)", b)
	}
	return f, nil
}

func parseLimit(q map[string][]string) (limit, offset int, err error) {
	limit = 50
	offset = 0
	if v := first(q["limit"]); v != "" {
		n, e := strconv.Atoi(v)
		if e != nil || n < 1 || n > 200 {
			return 0, 0, fmt.Errorf("invalid limit: must be 1..200")
		}
		limit = n
	}
	if v := first(q["offset"]); v != "" {
		n, e := strconv.Atoi(v)
		if e != nil || n < 0 {
			return 0, 0, fmt.Errorf("invalid offset: must be >= 0")
		}
		offset = n
	}
	return limit, offset, nil
}

// parseList 解析逗号分隔列表,去重并保序。
func parseList(s string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

func parseTime(s string) (*time.Time, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil, fmt.Errorf("use RFC3339 (e.g. 2026-09-03T00:00:00Z)")
	}
	utc := t.UTC()
	return &utc, nil
}

func addMetrics(dst map[string]any, rw store.SummaryRow) {
	dst["requests"] = rw.Requests
	dst["errors"] = rw.Errors
	dst["prompt_tokens"] = rw.PromptTokens
	dst["completion_tokens"] = rw.Completion
	dst["cache_read_tokens"] = rw.CacheRead
	dst["cost"] = rw.Cost
	dst["avg_latency_ms"] = rw.AvgLatencyMs
}

func summaryTotals(rows []store.SummaryRow) map[string]any {
	z := store.SummaryRow{}
	if len(rows) > 0 {
		z = rows[0]
	}
	m := map[string]any{}
	addMetrics(m, z)
	return m
}

func tsOrNil(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}

func ptrTime(t time.Time) *time.Time { return &t }

func first(v []string) string {
	if len(v) == 0 {
		return ""
	}
	return v[0]
}

func apiError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{"type": "api_error", "message": msg},
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
