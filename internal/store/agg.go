package store

import (
	"database/sql"
	"time"

	"personal-ai-gateway/internal/domain"
)

// AvgFirstTokenMsSince 窗口内成功流式请求首 token 延迟均值(无样本返回 0)。
//
// 只统计 stream=1:首字延迟这个词只对流式有意义(非流式一次返回,「首字」等于整程)。
// 此前不过滤 stream,把非流式的整程耗时也平均进来 —— 非流式实测均值约为流式的 2 倍,
// 混算会系统性抬高这个数字,且同一列在不同流式占比的窗口间不可比。
func (s *Store) AvgFirstTokenMsSince(sinceUTC time.Time) (float64, error) {
	return s.avgFirstTokenMsSince(sinceUTC, 0)
}

// AvgFirstTokenMsSinceOwner 同上,仅统计某归属账号。
func (s *Store) AvgFirstTokenMsSinceOwner(sinceUTC time.Time, ownerID int64) (float64, error) {
	return s.avgFirstTokenMsSince(sinceUTC, ownerID)
}

func (s *Store) avgFirstTokenMsSince(sinceUTC time.Time, ownerID int64) (float64, error) {
	cond, args := ownerCond(ownerID)
	var avg sql.NullFloat64
	err := s.db.QueryRow(`SELECT AVG(first_token_ms) FROM request_logs
		WHERE ts >= ? AND status BETWEEN 100 AND 399 AND first_token_ms > 0 AND stream = 1`+cond,
		append([]any{formatRFC3339(sinceUTC)}, args...)...).Scan(&avg)
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
			SUM(CASE WHEN `+errCond+` THEN 1 ELSE 0 END),
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

// WindowTotals 窗口内请求/错误/成本合计(全站口径)。
func (s *Store) WindowTotals(fromUTC, toUTC time.Time) (reqs, errs int, cost float64, err error) {
	var e sql.NullInt64
	err = s.db.QueryRow(`SELECT COUNT(*),
			COALESCE(SUM(CASE WHEN `+errCond+` THEN 1 ELSE 0 END),0),
			COALESCE(SUM(cost),0)
		FROM request_logs WHERE ts >= ? AND ts < ?`,
		formatRFC3339(fromUTC), formatRFC3339(toUTC)).Scan(&reqs, &e, &cost)
	errs = int(e.Int64)
	return
}

// WindowTotalsCustomers 窗口内「客户归属」的营收/成本/请求合计(经营口径)。
//
// 口径见 customerCond:只算归属为 role=user 的请求。返回 charge=客户付你的钱(营收)、
// cost=你付上游的钱 —— 差额即毛利,由调用方算(口径只在展示层表述,不在这层臆造)。
// 旧日志(改造前,owner_id=0)与站主自用流量都不进来。
func (s *Store) WindowTotalsCustomers(fromUTC, toUTC time.Time) (reqs int, charge, cost float64, err error) {
	err = s.db.QueryRow(`SELECT COUNT(*),
			COALESCE(SUM(charge_usd),0),
			COALESCE(SUM(cost),0)
		FROM request_logs WHERE ts >= ? AND ts < ?`+customerCond,
		formatRFC3339(fromUTC), formatRFC3339(toUTC)).Scan(&reqs, &charge, &cost)
	return
}

// CustomerDist 客户归属在窗口内的消耗分布(按 owner 聚合)。
type CustomerDist struct {
	OwnerID   int64
	Requests  int
	ChargeUsd float64 // 营收(客户付你)
	CostUsd   float64 // 成本(你付上游),其与 ChargeUsd 的差即该客户的毛利
}

// QueryCustomerDist 窗口内按客户聚合的营收/成本/请求数,消耗高→低(经营「谁在涨」用)。
// 无消耗客户不会出现在结果里 —— 与「余额预警」名单合并的事交给上层。
func (s *Store) QueryCustomerDist(fromUTC, toUTC time.Time) ([]CustomerDist, error) {
	rows, err := s.db.Query(`SELECT owner_id, COUNT(*),
			COALESCE(SUM(charge_usd),0),
			COALESCE(SUM(cost),0)
		FROM request_logs WHERE ts >= ? AND ts < ?`+customerCond+`
		GROUP BY owner_id ORDER BY SUM(charge_usd) DESC`,
		formatRFC3339(fromUTC), formatRFC3339(toUTC))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CustomerDist
	for rows.Next() {
		var d CustomerDist
		if err := rows.Scan(&d.OwnerID, &d.Requests, &d.ChargeUsd, &d.CostUsd); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// CustomerWallet 客户钱包快照(仅 role=user,余额高→低)。
type CustomerWallet struct {
	ID       int64
	Username string
	Balance  float64
}

// ListCustomerWallets 列出全部客户账号及其余额(供「欠费/低余额」告警合并消耗数据)。
// 只取 role=user —— 站主账号是经营主体,不是客户,不该出现在欠费名单里。
func (s *Store) ListCustomerWallets() ([]CustomerWallet, error) {
	rows, err := s.db.Query(`SELECT id, username, balance_usd FROM admins
		WHERE role = 'user' ORDER BY balance_usd ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CustomerWallet
	for rows.Next() {
		var w CustomerWallet
		if err := rows.Scan(&w.ID, &w.Username, &w.Balance); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}
