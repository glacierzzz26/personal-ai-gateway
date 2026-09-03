package store

import (
	"database/sql"
	"errors"
	"fmt"

	"personal-ai-gateway/internal/domain"
)

// ListRules 全部规则,自上而下匹配顺序(sort 升序)。Routing 页拖拽即该序。
func (s *Store) ListRules() ([]domain.RuleRead, error) {
	rows, err := s.db.Query(`SELECT id,name,enabled,match_mode,pattern,strategy,channel_ids,
		weights,fallback_channel_id,retry,timeout_ms,sort,hit
		FROM rules ORDER BY sort ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.RuleRead
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CreateRule 追加到规则表尾部(sort = 当前最大 +1)。
func (s *Store) CreateRule(in domain.RuleInput) (domain.RuleRead, error) {
	in.Defaults()
	var maxSort int
	_ = s.db.QueryRow(`SELECT COALESCE(MAX(sort),0) FROM rules`).Scan(&maxSort)
	now := formatRFC3339(s.nowUTC())
	res, err := s.db.Exec(`INSERT INTO rules (
		name, enabled, match_mode, pattern, strategy, channel_ids, weights,
		fallback_channel_id, retry, timeout_ms, sort, hit, created_at, updated_at
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,0,?,?)`,
		in.Name, b2i(*in.Enabled), in.MatchMode, in.Pattern, in.Strategy,
		encodeJSON(in.ChannelIDs), encodeWeightMap(in.Weights),
		nullFK(in.FallbackChannelID), in.Retry, in.TimeoutMs, maxSort+1, now, now)
	if err != nil {
		return domain.RuleRead{}, fmt.Errorf("insert rule: %w", err)
	}
	id, _ := res.LastInsertId()
	return s.GetRule(id)
}

func nullFK(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

// GetRule 取单条。
func (s *Store) GetRule(id int64) (domain.RuleRead, error) {
	row := s.db.QueryRow(`SELECT id,name,enabled,match_mode,pattern,strategy,channel_ids,
		weights,fallback_channel_id,retry,timeout_ms,sort,hit
		FROM rules WHERE id=?`, id)
	r, err := scanRule(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.RuleRead{}, ErrNotFound
	}
	return r, err
}

// UpdateRule 更新规则体(hit/sort 不受影响)。
func (s *Store) UpdateRule(id int64, in domain.RuleInput) (domain.RuleRead, error) {
	in.Defaults()
	res, err := s.db.Exec(`UPDATE rules SET
		name=?, enabled=?, match_mode=?, pattern=?, strategy=?, channel_ids=?, weights=?,
		fallback_channel_id=?, retry=?, timeout_ms=?, updated_at=?
		WHERE id=?`,
		in.Name, b2i(*in.Enabled), in.MatchMode, in.Pattern, in.Strategy,
		encodeJSON(in.ChannelIDs), encodeWeightMap(in.Weights),
		nullFK(in.FallbackChannelID), in.Retry, in.TimeoutMs,
		formatRFC3339(s.nowUTC()), id)
	if err != nil {
		return domain.RuleRead{}, fmt.Errorf("update rule %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.RuleRead{}, ErrNotFound
	}
	return s.GetRule(id)
}

// SetRuleEnabled 启停。
func (s *Store) SetRuleEnabled(id int64, enabled bool) error {
	res, err := s.db.Exec(`UPDATE rules SET enabled=?, updated_at=? WHERE id=?`,
		b2i(enabled), formatRFC3339(s.nowUTC()), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteRule 删除规则并重排剩余 sort。目标不存在返回 ErrNotFound。
func (s *Store) DeleteRule(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`DELETE FROM rules WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if err := renumberRules(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// MoveRule 把 fromIdx(当前显示序)的规则移到 insertAt,重排 sort=1..N。
func (s *Store) MoveRule(fromIdx, insertAt int) ([]domain.RuleRead, error) {
	rules, err := s.ListRules()
	if err != nil {
		return nil, err
	}
	if fromIdx < 0 || fromIdx >= len(rules) {
		return nil, fmt.Errorf("move from=%d out of range (len %d)", fromIdx, len(rules))
	}
	ids := make([]int64, 0, len(rules))
	for _, r := range rules {
		ids = append(ids, r.ID)
	}
	moved := ids[fromIdx]
	ids = append(ids[:fromIdx], ids[fromIdx+1:]...)
	if insertAt < 0 {
		insertAt = 0
	}
	if insertAt > len(ids) {
		insertAt = len(ids)
	}
	ids = append(ids[:insertAt], append([]int64{moved}, ids[insertAt:]...)...)
	return s.applyRuleOrder(ids)
}

// applyRuleOrder 按传入 id 序列重写 sort=1..N。
func (s *Store) applyRuleOrder(ids []int64) ([]domain.RuleRead, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	for i, id := range ids {
		if _, err := tx.Exec(`UPDATE rules SET sort=? WHERE id=?`, i+1, id); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.ListRules()
}

// HitRule 规则命中计数 +1(引擎选路命中时调用)。
func (s *Store) HitRule(id int64) error {
	_, err := s.db.Exec(`UPDATE rules SET hit=hit+1 WHERE id=?`, id)
	return err
}

func renumberRules(q *sql.Tx) error {
	rows, err := q.Query(`SELECT id FROM rules ORDER BY sort ASC, id ASC`)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for i, id := range ids {
		if _, err := q.Exec(`UPDATE rules SET sort=? WHERE id=?`, i+1, id); err != nil {
			return err
		}
	}
	return nil
}

func scanRule(row scanner) (domain.RuleRead, error) {
	var r domain.RuleRead
	var enabled int
	var channelIDs string
	var weights sql.NullString
	var fallback sql.NullInt64
	if err := row.Scan(&r.ID, &r.Name, &enabled, &r.MatchMode, &r.Pattern, &r.Strategy,
		&channelIDs, &weights, &fallback, &r.Retry, &r.TimeoutMs, &r.Sort, &r.Hit); err != nil {
		return domain.RuleRead{}, err
	}
	r.Enabled = enabled == 1
	r.ChannelIDs = decodeInt64List(channelIDs)
	if weights.Valid {
		r.Weights = decodeWeightMap(weights.String)
	} else {
		r.Weights = map[int64]int{}
	}
	if fallback.Valid {
		id := fallback.Int64
		r.FallbackChannelID = &id
	}
	return r, nil
}
