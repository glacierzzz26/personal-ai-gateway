package store

import (
	"database/sql"
	"time"

	"personal-ai-gateway/internal/domain"
)

// AvgFirstTokenMsSince 窗口内成功请求首 token 延迟均值(无样本返回 0)。
func (s *Store) AvgFirstTokenMsSince(sinceUTC time.Time) (float64, error) {
	var avg sql.NullFloat64
	err := s.db.QueryRow(`SELECT AVG(first_token_ms) FROM request_logs
		WHERE ts >= ? AND status BETWEEN 100 AND 399 AND first_token_ms > 0`,
		formatRFC3339(sinceUTC)).Scan(&avg)
	if err != nil {
		return 0, err
	}
	if !avg.Valid {
		return 0, nil
	}
	return avg.Float64, nil
}

// QueryModelSeries 单一模型的按桶用量曲线(过滤 model = ?)。
func (s *Store) QueryModelSeries(model, bucket string, fromUTC, toUTC time.Time, tzOffMin int) ([]domain.MetricPoint, error) {
	n := MetricBucket(bucket)
	rows, err := s.db.Query(`SELECT substr(datetime(ts, ?), 1, ?) AS bkt,
			COUNT(*),
			SUM(CASE WHEN status >= 400 OR err IS NOT NULL THEN 1 ELSE 0 END),
			COALESCE(SUM(cost), 0)
		FROM request_logs
		WHERE model = ? AND ts >= ? AND ts < ?
		GROUP BY bkt ORDER BY bkt ASC`,
		tzMod(tzOffMin), n, model, formatRFC3339(fromUTC), formatRFC3339(toUTC))
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

// QueryModelChannels 单一模型窗口内按渠道聚合(模型抽屉「用量」byChannel)。
func (s *Store) QueryModelChannels(model string, fromUTC, toUTC time.Time) ([]domain.ModelChannelUsage, error) {
	rows, err := s.db.Query(`SELECT channel_name,
			COUNT(*),
			COALESCE(SUM(cost), 0)
		FROM request_logs
		WHERE model = ? AND ts >= ? AND ts < ?
		GROUP BY channel_name ORDER BY COUNT(*) DESC`,
		model, formatRFC3339(fromUTC), formatRFC3339(toUTC))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ModelChannelUsage
	for rows.Next() {
		var c domain.ModelChannelUsage
		if err := rows.Scan(&c.ChannelName, &c.Requests, &c.CostUsd); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// WindowTotals 窗口内请求/错误/成本合计。
func (s *Store) WindowTotals(fromUTC, toUTC time.Time) (reqs, errs int, cost float64, err error) {
	var e sql.NullInt64
	err = s.db.QueryRow(`SELECT COUNT(*),
			COALESCE(SUM(CASE WHEN status >= 400 OR err IS NOT NULL THEN 1 ELSE 0 END),0),
			COALESCE(SUM(cost),0)
		FROM request_logs WHERE ts >= ? AND ts < ?`,
		formatRFC3339(fromUTC), formatRFC3339(toUTC)).Scan(&reqs, &e, &cost)
	errs = int(e.Int64)
	return
}
