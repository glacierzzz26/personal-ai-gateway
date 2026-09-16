package store

import (
	"database/sql"
	"errors"

	"personal-ai-gateway/internal/domain"
)

// 钱包与结算(中转站改造,见 PLAN.md §3/§4)。
//
// 口径:金额一律是 settings.displayCurrency 币种金额(本站为人民币);
// cost = 你付上游,charge = 客户付你,差额即毛利。只有 role=user 的归属会被扣钱包,
// admin(站主自己)与无归属(owner_id=0,历史全局 key)只记账不扣钱。

// SettleRequest 一次请求结算:落账 + 令牌用量累加 +(客户)扣钱包,单事务完成。
//
//   - log.TokenID > 0 时累加 tokens.used_usd 与 last_used_at(纯累加,不设上限 —— 额度是否够
//     由入口预检查判定,见 ChargeToken 注释);
//   - chargeWallet 为 true 且 log.OwnerID > 0 且 log.ChargeUsd != 0 时,扣该用户钱包并记一条
//     balance_logs(允许扣成负数:透支至多一笔,入口下一笔即 402)。
//
// 落账与扣费必须在同一事务:否则客户端拿到的成功响应背后可能「记了账没扣钱」或反之。
func (s *Store) SettleRequest(log domain.LogRow, chargeWallet bool) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var errField *string = log.Err
	res, err := tx.Exec(`INSERT INTO request_logs (
		ts, model, channel_id, channel_name, token_id, token_name, owner_id,
		client_tool, protocol, stream, status,
		prompt_tokens, completion_tokens, cache_read_tokens, cost, charge_usd,
		first_token_ms, total_ms, ip, err
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		formatRFC3339(log.TS), log.Model, log.ChannelID, log.ChannelName, log.TokenID, log.TokenName, log.OwnerID,
		log.ClientTool, log.Protocol, b2i(log.Stream), log.Status,
		log.PromptTokens, log.Completion, log.CacheRead, log.CostUsd, log.ChargeUsd,
		log.FirstTokenMs, log.TotalMs, log.IP, errField)
	if err != nil {
		return err
	}
	logID, _ := res.LastInsertId()

	if log.TokenID > 0 {
		if _, err := tx.Exec(`UPDATE tokens SET used_usd = used_usd + ?, last_used_at = ?, updated_at = ?
			WHERE id=?`, log.ChargeUsd, formatRFC3339(s.nowUTC()), formatRFC3339(s.nowUTC()), log.TokenID); err != nil {
			return err
		}
	}
	if chargeWallet && log.OwnerID > 0 && log.ChargeUsd != 0 {
		if err := applyBalance(tx, log.OwnerID, -log.ChargeUsd, "charge", logID, "", s); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// TopupBalance 充值/调整钱包(正=充值,负=扣减),返回变动后余额与流水行。
func (s *Store) TopupBalance(adminID int64, amount float64, note string) (domain.BalanceLogItem, error) {
	reason := "topup"
	if amount < 0 {
		reason = "adjust"
	}
	var out domain.BalanceLogItem
	var createdAt string
	tx, err := s.db.Begin()
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err := applyBalance(tx, adminID, amount, reason, 0, note, s); err != nil {
		return out, err
	}
	row := tx.QueryRow(`SELECT id, admin_id, delta, balance_after, reason, log_id, note, created_at
		FROM balance_logs WHERE admin_id=? ORDER BY id DESC LIMIT 1`, adminID)
	var rec domain.BalanceLogItem
	if err := row.Scan(&rec.ID, new(int64), &rec.Delta, &rec.BalanceAfter, &rec.Reason, &rec.LogID, &rec.Note, &createdAt); err != nil {
		return out, err
	}
	if rec.CreatedAt, err = parseTime(createdAt); err != nil {
		return out, err
	}
	if err := tx.Commit(); err != nil {
		return out, err
	}
	return rec, nil
}

// applyBalance 在一个已有事务内改动余额并记流水。余额可为负(透支)。
func applyBalance(tx *sql.Tx, adminID int64, delta float64, reason string, logID int64, note string, s *Store) error {
	res, err := tx.Exec(`UPDATE admins SET balance_usd = balance_usd + ? WHERE id=?`, delta, adminID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	var after float64
	if err := tx.QueryRow(`SELECT balance_usd FROM admins WHERE id=?`, adminID).Scan(&after); err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO balance_logs (admin_id, delta, balance_after, reason, log_id, note, created_at)
		VALUES (?,?,?,?,?,?,?)`,
		adminID, delta, after, reason, logID, note, formatRFC3339(s.nowUTC()))
	return err
}

// GetBalance 读某账号余额。
func (s *Store) GetBalance(adminID int64) (float64, error) {
	var b float64
	err := s.db.QueryRow(`SELECT balance_usd FROM admins WHERE id=?`, adminID).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return b, err
}

// ListBalanceLogs 某账号的账变流水(新→旧),limit<=0 用默认 50。
func (s *Store) ListBalanceLogs(adminID int64, limit int) ([]domain.BalanceLogItem, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`SELECT id, delta, balance_after, reason, log_id, note, created_at
		FROM balance_logs WHERE admin_id=? ORDER BY id DESC LIMIT ?`, adminID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.BalanceLogItem{}
	for rows.Next() {
		var it domain.BalanceLogItem
		var created string
		if err := rows.Scan(&it.ID, &it.Delta, &it.BalanceAfter, &it.Reason, &it.LogID, &it.Note, &created); err != nil {
			return nil, err
		}
		it.CreatedAt, _ = parseTime(created)
		out = append(out, it)
	}
	return out, rows.Err()
}

// SetTokenCeiling 设某账号名下令牌的额度/RPM 上限(0 = 不限)。
func (s *Store) SetTokenCeiling(adminID int64, quotaUsd float64, rpm int) error {
	res, err := s.db.Exec(`UPDATE admins SET token_quota_ceiling=?, token_rpm_ceiling=? WHERE id=?`,
		quotaUsd, rpm, adminID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// TokenCeiling 读某账号名下令牌的额度/RPM 上限(0,0 = 不限)。
func (s *Store) TokenCeiling(adminID int64) (quotaUsd float64, rpm int, err error) {
	err = s.db.QueryRow(`SELECT token_quota_ceiling, token_rpm_ceiling FROM admins WHERE id=?`, adminID).
		Scan(&quotaUsd, &rpm)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, ErrNotFound
	}
	return quotaUsd, rpm, err
}
