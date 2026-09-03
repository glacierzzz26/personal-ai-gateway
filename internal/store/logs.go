package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"personal-ai-gateway/internal/domain"
)

// LogFilter 列表查询条件(空值 = 不过滤)。
type LogFilter struct {
	Model   string // 精确
	Channel string // 精确 channel_name
	Token   string // 精确 token_name
	Status  string // ok|error|""
	Keyword string // LIKE 命中 model/channel/token/ip/err
	From    *time.Time
	To      *time.Time
	Limit   int
	Offset  int
}

// InsertLog 落一条请求日志(记账最终态)。
func (s *Store) InsertLog(l domain.LogRow) error {
	_, err := s.db.Exec(`INSERT INTO request_logs (
		ts, model, channel_id, channel_name, token_id, token_name,
		client_tool, protocol, stream, status,
		prompt_tokens, completion_tokens, cache_read_tokens, cost,
		first_token_ms, total_ms, ip, err
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		formatRFC3339(l.TS), l.Model, l.ChannelID, l.ChannelName, l.TokenID, l.TokenName,
		l.ClientTool, l.Protocol, b2i(l.Stream), l.Status,
		l.PromptTokens, l.Completion, l.CacheRead, l.CostUsd,
		l.FirstTokenMs, l.TotalMs, l.IP, l.Err)
	return err
}

// ListLogs 分页返回日志(新→旧)与总数,用于 Logs 页列表与筛选。
func (s *Store) ListLogs(f LogFilter, tzOffMin int) ([]domain.LogItem, int, error) {
	where, args := logWhere(f)
	limit, offset := f.Limit, f.Offset
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM request_logs`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	sqlStr := `SELECT id, ts, model, channel_name, token_name,
		prompt_tokens, completion_tokens, cache_read_tokens, cost,
		first_token_ms, total_ms, status, ip, err
		FROM request_logs` + where + ` ORDER BY id DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	rows, err := s.db.Query(sqlStr, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	off := time.Duration(tzOffMin) * time.Minute
	var items []domain.LogItem
	for rows.Next() {
		item, err := scanLog(rows, off)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	return items, total, rows.Err()
}

func logWhere(f LogFilter) (string, []any) {
	var conds []string
	var args []any
	if f.Model != "" {
		conds = append(conds, "model = ?")
		args = append(args, f.Model)
	}
	if f.Channel != "" {
		conds = append(conds, "channel_name = ?")
		args = append(args, f.Channel)
	}
	if f.Token != "" {
		conds = append(conds, "token_name = ?")
		args = append(args, f.Token)
	}
	switch f.Status {
	case "ok":
		conds = append(conds, "status BETWEEN 100 AND 399")
	case "error":
		conds = append(conds, "(status >= 400 OR err IS NOT NULL)")
	}
	if f.Keyword != "" {
		kw := "%" + lower(f.Keyword) + "%"
		conds = append(conds, `(lower(model) LIKE ? OR lower(channel_name) LIKE ?
			OR lower(token_name) LIKE ? OR lower(ip) LIKE ? OR lower(coalesce(err,'')) LIKE ?)`)
		args = append(args, kw, kw, kw, kw, kw)
	}
	if f.From != nil {
		conds = append(conds, "ts >= ?")
		args = append(args, formatRFC3339(*f.From))
	}
	if f.To != nil {
		conds = append(conds, "ts < ?")
		args = append(args, formatRFC3339(*f.To))
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

func scanLog(row scanner, off time.Duration) (domain.LogItem, error) {
	var it domain.LogItem
	var ts, errText sql.NullString
	if err := row.Scan(&it.ID, &ts, &it.Model, &it.ChannelName, &it.TokenName,
		&it.InTokens, &it.OutTokens, &it.CacheRead, &it.CostUsd,
		&it.FirstTokenMs, &it.TotalMs, &it.StatusCode, &it.IP, &errText); err != nil {
		return domain.LogItem{}, err
	}
	if ts.Valid {
		if t, err := parseTime(ts.String); err == nil {
			it.TS = t.Add(off).Format("2006-01-02 15:04")
		}
	}
	if errText.Valid {
		it.Error = &errText.String
	}
	return it, nil
}

// ClearLogs 清空全部日志(危险操作,管理端需二次确认)。
func (s *Store) ClearLogs() error {
	_, err := s.db.Exec(`DELETE FROM request_logs`)
	return err
}

// PruneLogs 按设置保留天数删除过期日志(<=0 跳过)。
func (s *Store) PruneLogs(days int) error {
	if days <= 0 {
		return nil
	}
	cut := s.nowUTC().AddDate(0, 0, -days)
	_, err := s.db.Exec(`DELETE FROM request_logs WHERE ts < ?`, formatRFC3339(cut))
	return err
}

// ------------- 时区换算与桶 -------------

// tzMod 生成 SQLite datetime 修饰符(如 "+480 minutes" / "-120 minutes")。
func tzMod(offMin int) string { return fmt.Sprintf("%+d minutes", offMin) }

// LocalDayWindowUTC 给定时区偏移与时刻,返回该「本地自然日」对应的 UTC 起止。
func LocalDayWindowUTC(tzOffMin int, at time.Time) (time.Time, time.Time) {
	u := at.UTC()
	local := u.Add(time.Duration(tzOffMin) * time.Minute)
	localMidnight := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
	return localMidnight.Add(-time.Duration(tzOffMin) * time.Minute),
		localMidnight.Add(24 * time.Hour).Add(-time.Duration(tzOffMin) * time.Minute)
}

// MetricBucket 返回 (bucketKey长度, 桶式) 供 series 用;hour=13,day=10。
func MetricBucket(bucket string) int {
	switch bucket {
	case "hour":
		return 13 // "YYYY-MM-DD HH"
	default:
		return 10 // "YYYY-MM-DD"
	}
}

// QuerySeries 时间桶聚合(hour/day),桶内 request/error/cost。
// fromUTC/toUTC 已换算好;tzOff 只影响桶归属。
func (s *Store) QuerySeries(bucket string, fromUTC, toUTC time.Time, tzOffMin int) ([]domain.MetricPoint, error) {
	n := MetricBucket(bucket)
	rows, err := s.db.Query(`SELECT substr(datetime(ts, ?), 1, ?) AS bkt,
			COUNT(*),
			SUM(CASE WHEN status >= 400 OR err IS NOT NULL THEN 1 ELSE 0 END),
			COALESCE(SUM(cost), 0)
		FROM request_logs
		WHERE ts >= ? AND ts < ?
		GROUP BY bkt ORDER BY bkt ASC`,
		tzMod(tzOffMin), n, formatRFC3339(fromUTC), formatRFC3339(toUTC))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.MetricPoint
	for rows.Next() {
		var p domain.MetricPoint
		var errs sql.NullInt64
		if err := rows.Scan(&p.TS, &p.Requests, &errs, &p.CostUsd); err != nil {
			return nil, err
		}
		p.Errors = int(errs.Int64)
		out = append(out, p)
	}
	return out, rows.Err()
}

// dimWhitelist 维度 → 实际列(防注入)。
func dimCol(dim string) string {
	switch dim {
	case "model":
		return "model"
	case "channel":
		return "channel_name"
	case "token":
		return "token_name"
	}
	return ""
}

// QueryDimSummary 按 模型/渠道/令牌 维度聚合窗口内的用量行。
func (s *Store) QueryDimSummary(dim string, fromUTC, toUTC time.Time, limit int) ([]domain.UsageRow, error) {
	col := dimCol(dim)
	if col == "" {
		return nil, fmt.Errorf("invalid dim: %q", dim)
	}
	sqlStr := `SELECT ` + col + ` AS g,
			COUNT(*),
			COALESCE(SUM(prompt_tokens),0),
			COALESCE(SUM(completion_tokens),0),
			COALESCE(SUM(cost),0),
			SUM(CASE WHEN status >= 400 OR err IS NOT NULL THEN 1 ELSE 0 END)
		FROM request_logs
		WHERE ts >= ? AND ts < ?
		GROUP BY g ORDER BY COUNT(*) DESC`
	if limit > 0 {
		sqlStr += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := s.db.Query(sqlStr, formatRFC3339(fromUTC), formatRFC3339(toUTC))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.UsageRow
	for rows.Next() {
		var u domain.UsageRow
		var errs sql.NullInt64
		if err := rows.Scan(&u.Name, &u.Requests, &u.InTokens, &u.OutTokens, &u.CostUsd, &errs); err != nil {
			return nil, err
		}
		u.ErrorRate = pctErrors(errs.Int64, u.Requests)
		out = append(out, u)
	}
	return out, rows.Err()
}

func pctErrors(errs int64, reqs int) float64 {
	if reqs == 0 {
		return 0
	}
	return float64(errs) / float64(reqs)
}

// ChannelStat 渠道在窗口内的健康/用量汇总。
type ChannelStat struct {
	Requests   int     // 窗口内请求数
	Errors     int     // 其中错误数
	LatencySum int64   // total_ms 累加(算均值)
	Tokens     int64   // prompt+completion+cache
	CostUsd    float64 // 成本
}

// ChannelStatsSince 统计每个渠道自 sinceUTC 起的流量。无流量渠道不出现在 map。
func (s *Store) ChannelStatsSince(sinceUTC time.Time) (map[int64]ChannelStat, error) {
	rows, err := s.db.Query(`SELECT channel_id,
			COUNT(*),
			SUM(CASE WHEN status >= 400 OR err IS NOT NULL THEN 1 ELSE 0 END),
			COALESCE(SUM(total_ms),0),
			COALESCE(SUM(prompt_tokens + completion_tokens + cache_read_tokens),0),
			COALESCE(SUM(cost),0)
		FROM request_logs
		WHERE channel_id > 0 AND ts >= ?
		GROUP BY channel_id`,
		formatRFC3339(sinceUTC))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]ChannelStat{}
	for rows.Next() {
		var id int64
		var st ChannelStat
		var errs sql.NullInt64
		if err := rows.Scan(&id, &st.Requests, &errs, &st.LatencySum, &st.Tokens, &st.CostUsd); err != nil {
			return nil, err
		}
		st.Errors = int(errs.Int64)
		out[id] = st
	}
	return out, rows.Err()
}
