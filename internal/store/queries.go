package store

import (
	"fmt"
	"strings"
	"time"
)

// ReqFilter 是 request_log 的通用过滤条件。零值字段表示"不过滤"。
type ReqFilter struct {
	From, To     *time.Time
	Protocol     string // anthropic | openai
	Model        string
	Upstream     string
	ClientKey    string
	Stream       *bool
	Status       int    // >0 时精确匹配状态码
	StatusBucket string // "2xx"|"3xx"|"4xx"|"5xx";与 Status 二选一
}

var sortColumns = map[string]string{
	"ts": "ts", "id": "id", "status": "status", "latency_ms": "latency_ms",
	"prompt_tokens": "prompt_tokens", "completion_tokens": "completion_tokens",
	"cache_read_tokens": "cache_read_tokens", "cost": "cost",
	"model": "model", "upstream": "upstream", "client_key": "client_key", "protocol": "protocol",
}

var groupColumns = map[string]string{
	"model": "model", "upstream": "upstream", "protocol": "protocol", "client_key": "client_key",
}

const reqCols = "id, ts, client_key, client_tool, protocol, model, upstream, stream, status, " +
	"prompt_tokens, completion_tokens, cache_read_tokens, cost, latency_ms, err"

func (f ReqFilter) where() (string, []any) {
	var conds []string
	var args []any
	// 时间过滤:CAST(strftime('%s', ts) AS INTEGER) 再比。
	// 直接拿 strftime 的 TEXT 结果与整数比,在 modernc 下 <= 恒假(隐式 TEXT→INT 转换有 bug)。
	if f.From != nil {
		conds = append(conds, "CAST(strftime('%s', ts) AS INTEGER) >= ?")
		args = append(args, f.From.Unix())
	}
	if f.To != nil {
		conds = append(conds, "CAST(strftime('%s', ts) AS INTEGER) <= ?")
		args = append(args, f.To.Unix())
	}
	eq := func(col, val string) {
		if val != "" {
			conds = append(conds, col+" = ?")
			args = append(args, val)
		}
	}
	eq("protocol", f.Protocol)
	eq("model", f.Model)
	eq("upstream", f.Upstream)
	eq("client_key", f.ClientKey)
	if f.Stream != nil {
		conds = append(conds, "stream = ?")
		args = append(args, boolInt(*f.Stream))
	}
	if f.Status > 0 {
		conds = append(conds, "status = ?")
		args = append(args, f.Status)
	}
	switch f.StatusBucket {
	case "2xx":
		conds = append(conds, "status >= 200 AND status < 300")
	case "3xx":
		conds = append(conds, "status >= 300 AND status < 400")
	case "4xx":
		conds = append(conds, "status >= 400 AND status < 500")
	case "5xx":
		conds = append(conds, "status >= 500")
	}
	if len(conds) == 0 {
		return "1=1", args
	}
	return strings.Join(conds, " AND "), args
}

// ListRequests 返回一页请求日志,支持白名单内排序(列名由调用方校验后传入,这里再兜底)。
func (s *Store) ListRequests(f ReqFilter, limit, offset int, sort, order string) ([]LogEntry, error) {
	col, ok := sortColumns[sort]
	if !ok {
		return nil, fmt.Errorf("store: invalid sort column %q", sort)
	}
	order = strings.ToLower(order)
	if order != "asc" {
		order = "desc"
	}
	where, args := f.where()
	q := fmt.Sprintf("SELECT %s FROM request_log WHERE %s ORDER BY %s %s LIMIT ? OFFSET ?",
		reqCols, where, col, strings.ToUpper(order))
	args = append(args, limit, offset)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list: %w", err)
	}
	defer rows.Close()
	return scanLogs(rows)
}

// CountRequests 返回同一过滤条件下的总条数,供分页 total。
func (s *Store) CountRequests(f ReqFilter) (int, error) {
	where, args := f.where()
	var n int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM request_log WHERE "+where, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: count: %w", err)
	}
	return n, nil
}

// SummaryRow 是一行聚合结果;Groups 只含本次 groupBy 请求的维度。
type SummaryRow struct {
	Bucket        string
	Groups        map[string]string
	Requests      int
	Errors        int
	PromptTokens  int64
	Completion    int64
	CacheRead     int64
	Cost          float64
	AvgLatencyMs  float64
}

// UsageSummary 按 groupBy(白名单内)+ 时间桶(hour|day,空=不拆)做聚合。
// groupBy 为空且 bucket 为空 → 返回单行整体汇总。
func (s *Store) UsageSummary(f ReqFilter, groupBy []string, bucket string) ([]SummaryRow, error) {
	var groupCols []string
	for _, g := range groupBy {
		col, ok := groupColumns[g]
		if !ok {
			return nil, fmt.Errorf("store: invalid group %q", g)
		}
		groupCols = append(groupCols, col)
	}

	bucketSel, bucketCol := "", ""
	switch bucket {
	case "hour":
		bucketSel, bucketCol = "substr(ts,1,13) || ':00:00'", "substr(ts,1,13) || ':00:00'"
	case "day":
		bucketSel, bucketCol = "substr(ts,1,10)", "substr(ts,1,10)"
	}

	// SELECT 里除了分组列和桶,还需要 group col 原始值,用于回填 Groups
	selects := []string{}
	orderBy := []string{}
	if bucketSel != "" {
		selects = append(selects, bucketSel+" AS __bucket")
		orderBy = append(orderBy, "__bucket")
	}
	selNames := make([]string, 0, len(groupCols)) // 每个分组的列别名(保持去重)
	for _, col := range groupCols {
		selNames = append(selNames, col)
		selects = append(selects, col)
		orderBy = append(orderBy, col)
	}
	selects = append(selects,
		"COUNT(*)", "COALESCE(SUM(CASE WHEN status >= 400 THEN 1 ELSE 0 END),0)",
		"COALESCE(SUM(prompt_tokens),0)", "COALESCE(SUM(completion_tokens),0)",
		"COALESCE(SUM(cache_read_tokens),0)", "COALESCE(SUM(cost),0)", "COALESCE(AVG(latency_ms),0)")

	where, args := f.where()
	q := fmt.Sprintf("SELECT %s FROM request_log WHERE %s", strings.Join(selects, ", "), where)
	if len(groupCols) > 0 || bucketCol != "" {
		all := append([]string{}, groupCols...)
		if bucketCol != "" {
			all = append([]string{bucketCol}, all...)
		}
		q += " GROUP BY " + strings.Join(all, ", ")
	}
	if len(orderBy) > 0 {
		q += " ORDER BY " + strings.Join(orderBy, ", ")
	}

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: summary: %w", err)
	}
	defer rows.Close()

	var out []SummaryRow
	for rows.Next() {
		row := SummaryRow{Groups: map[string]string{}}
		scan := []any{}
		if bucketSel != "" {
			scan = append(scan, &row.Bucket)
		}
		var selVals = make([]any, 0, len(selNames))
		dest := make([]string, len(selNames))
		for i := range selNames {
			selVals = append(selVals, &dest[i])
		}
		scan = append(scan, selVals...)
		scan = append(scan,
			&row.Requests, &row.Errors,
			&row.PromptTokens, &row.Completion, &row.CacheRead,
			&row.Cost, &row.AvgLatencyMs)

		if err := rows.Scan(scan...); err != nil {
			return nil, fmt.Errorf("store: summary scan: %w", err)
		}
		for i, name := range selNames {
			row.Groups[name] = dest[i]
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// ValidSort / ValidGroup 供 HTTP 层校验参数;查询方法内部仍会二次校验。
func ValidSort(s string) bool {
	_, ok := sortColumns[s]
	return ok
}

func ValidGroup(g string) bool {
	_, ok := groupColumns[g]
	return ok
}

func scanLogs(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]LogEntry, error) {
	var out []LogEntry
	for rows.Next() {
		var e LogEntry
		var ts string
		var stream int
		if err := rows.Scan(&e.ID, &ts, &e.ClientKey, &e.ClientTool, &e.Protocol, &e.Model, &e.Upstream,
			&stream, &e.Status, &e.PromptTokens, &e.CompletionTokens, &e.CacheReadTokens,
			&e.Cost, &e.LatencyMs, &e.Err); err != nil {
			return nil, err
		}
		e.Stream = stream == 1
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			e.TS = t
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
