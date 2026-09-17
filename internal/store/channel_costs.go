package store

import (
	"database/sql"
	"errors"
	"fmt"

	"personal-ai-gateway/internal/domain"
)

// 「渠道 × 厂商」成本系数:成本 = 厂商官方价 × ratio(见迁移 m0012)。
//
// 键是**厂商**(domain.Provider,取值同 official_prices.provider / models.official_vendor),
// 不是 channels.provider —— 聚合中转渠道的 provider 多为 OpenAI,而它实际消耗的是
// DeepSeek/通义千问的官方价。计费热路径走 ChannelVendorRatio 点查。

// ListChannelCostRatios 某渠道的全部系数行(含备注,供管理面编辑)。
func (s *Store) ListChannelCostRatios(channelID int64) ([]domain.CostRatioRow, error) {
	rows, err := s.db.Query(`SELECT channel_id, vendor, ratio, note, updated_at
		FROM channel_vendor_costs WHERE channel_id = ? ORDER BY vendor`, channelID)
	if err != nil {
		return nil, fmt.Errorf("list channel cost ratios: %w", err)
	}
	defer rows.Close()
	out := []domain.CostRatioRow{}
	for rows.Next() {
		var r domain.CostRatioRow
		if err := rows.Scan(&r.ChannelID, &r.Vendor, &r.Ratio, &r.Note, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ChannelVendorRatio 计费热路径点查:该渠道消耗该厂商模型的成本系数。
// 无行 → 返回 0 与 ErrNotFound(调用方应回落 1.0)。
func (s *Store) ChannelVendorRatio(channelID int64, vendor domain.Provider) (float64, error) {
	var ratio float64
	err := s.db.QueryRow(`SELECT ratio FROM channel_vendor_costs WHERE channel_id = ? AND vendor = ?`,
		channelID, string(vendor)).Scan(&ratio)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("channel vendor ratio: %w", err)
	}
	return ratio, nil
}

// ErrInvalidRatio 系数取值非法(必须 > 0)。
//
// 系数 0 = 上游免费送 —— 若真如此也必须显式删除该行而非填 0:填 0 会让
// 「没配」与「配成免费」不可区分,前者该报「未设系数」,后者是真事。
var ErrInvalidRatio = errors.New("成本系数必须大于 0")

// SetCostRatio 写入(或更新)一条系数。ratio <= 0 拒绝(见 ErrInvalidRatio)。
func (s *Store) SetCostRatio(channelID int64, vendor domain.Provider, ratio float64, note string) error {
	if channelID <= 0 {
		return fmt.Errorf("渠道 ID 非法: %d", channelID)
	}
	if vendor == "" {
		return fmt.Errorf("厂商不得为空")
	}
	if ratio <= 0 {
		return ErrInvalidRatio
	}
	now := nowRFC3339()
	_, err := s.db.Exec(`INSERT INTO channel_vendor_costs (channel_id, vendor, ratio, note, created_at, updated_at)
		VALUES (?,?,?,?,?,?)
		ON CONFLICT(channel_id, vendor) DO UPDATE SET
			ratio = excluded.ratio, note = excluded.note, updated_at = excluded.updated_at`,
		channelID, string(vendor), ratio, note, now, now)
	if err != nil {
		return fmt.Errorf("set cost ratio: %w", err)
	}
	return nil
}

// DeleteCostRatio 删除一条系数(退回默认 1.0)。目标不存在时返回 ErrNotFound。
func (s *Store) DeleteCostRatio(channelID int64, vendor domain.Provider) error {
	res, err := s.db.Exec(`DELETE FROM channel_vendor_costs WHERE channel_id = ? AND vendor = ?`,
		channelID, string(vendor))
	if err != nil {
		return fmt.Errorf("delete cost ratio: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ReplaceChannelCostRatios 全量替换某渠道的系数行(管理面 PUT 语义)。
//
// 单独一张表、独立接口 —— 刻意**不并进 ChannelDraft**:渠道的 PATCH 是整体覆盖语义,
// 系数混进去会让「改个渠道名」顺带清掉没回传的系数行。
//
// 单事务:先清后插,中途失败整体回滚(不会出现「删了旧的、新的没进去」)。
func (s *Store) ReplaceChannelCostRatios(channelID int64, rows []domain.CostRatioInput) error {
	if channelID <= 0 {
		return fmt.Errorf("渠道 ID 非法: %d", channelID)
	}
	seen := map[domain.Provider]bool{}
	for _, r := range rows {
		if r.Vendor == "" {
			return fmt.Errorf("厂商不得为空")
		}
		if r.Ratio <= 0 {
			return ErrInvalidRatio
		}
		if seen[r.Vendor] {
			return fmt.Errorf("厂商 %s 重复", r.Vendor)
		}
		seen[r.Vendor] = true
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM channel_vendor_costs WHERE channel_id = ?`, channelID); err != nil {
		return fmt.Errorf("clear cost ratios: %w", err)
	}
	now := nowRFC3339()
	for _, r := range rows {
		if _, err := tx.Exec(`INSERT INTO channel_vendor_costs
			(channel_id, vendor, ratio, note, created_at, updated_at) VALUES (?,?,?,?,?,?)`,
			channelID, string(r.Vendor), r.Ratio, r.Note, now, now); err != nil {
			return fmt.Errorf("insert cost ratio: %w", err)
		}
	}
	return tx.Commit()
}
