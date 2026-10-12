package store

import (
	"database/sql"
	"errors"
	"fmt"

	"personal-ai-gateway/internal/domain"
)

// 「渠道 × 模型」成本系数:成本 = 官方价 × ratio(见迁移 v15)。
//
// 与 channel_vendor_costs(「渠道 × 厂商」)的关系 —— 计费三级口径:
//   ① 本表(模型级):订阅计划页自动派生(CC GOAT / opencode Go,ratio = 订阅价 ÷ 该模型额度)或人工覆盖;
//   ② channel_vendor_costs(厂商级):旧口径,兜底;
//   ③ 都无 → 1.0(不折扣)。
// 键 model_name 取 official_prices.model_name(canonical slug),与计费侧 plan.OfficialModelName 对齐。

// ListChannelModelCostRatios 某渠道的全部模型级系数行(含备注/来源,供管理面编辑)。
func (s *Store) ListChannelModelCostRatios(channelID int64) ([]domain.ModelCostRatioRow, error) {
	rows, err := s.db.Query(`SELECT channel_id, model_name, ratio, source, note, updated_at
		FROM channel_model_cost_ratios WHERE channel_id = ? ORDER BY model_name`, channelID)
	if err != nil {
		return nil, fmt.Errorf("list channel model cost ratios: %w", err)
	}
	defer rows.Close()
	out := []domain.ModelCostRatioRow{}
	for rows.Next() {
		var r domain.ModelCostRatioRow
		if err := rows.Scan(&r.ChannelID, &r.ModelName, &r.Ratio, &r.Source, &r.Note, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ChannelModelRatio 计费热路径点查:该渠道消耗该模型的成本系数(模型级)。
// 无行 → 返回 0 与 ErrNotFound(调用方应回落厂商级)。
func (s *Store) ChannelModelRatio(channelID int64, modelName string) (float64, error) {
	var ratio float64
	err := s.db.QueryRow(`SELECT ratio FROM channel_model_cost_ratios WHERE channel_id = ? AND model_name = ?`,
		channelID, modelName).Scan(&ratio)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("channel model ratio: %w", err)
	}
	return ratio, nil
}

// SetChannelModelCostRatio 写入(或更新)一条模型级系数(source 由调用方定,manual 覆盖 plan)。
func (s *Store) SetChannelModelCostRatio(channelID int64, modelName string, ratio float64, source, note string) error {
	if channelID <= 0 {
		return fmt.Errorf("渠道 ID 非法: %d", channelID)
	}
	if modelName == "" {
		return fmt.Errorf("模型名不得为空")
	}
	if ratio <= 0 {
		return ErrInvalidRatio
	}
	if source == "" {
		source = "manual"
	}
	now := nowRFC3339()
	_, err := s.db.Exec(`INSERT INTO channel_model_cost_ratios
		(channel_id, model_name, ratio, source, note, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?)
		ON CONFLICT(channel_id, model_name) DO UPDATE SET
			ratio = excluded.ratio, source = excluded.source, note = excluded.note, updated_at = excluded.updated_at`,
		channelID, modelName, ratio, source, note, now, now)
	if err != nil {
		return fmt.Errorf("set channel model cost ratio: %w", err)
	}
	return nil
}

// DeleteChannelModelCostRatio 删除一条模型级系数(退回厂商级兜底)。目标不存在时返回 ErrNotFound。
func (s *Store) DeleteChannelModelCostRatio(channelID int64, modelName string) error {
	res, err := s.db.Exec(`DELETE FROM channel_model_cost_ratios WHERE channel_id = ? AND model_name = ?`,
		channelID, modelName)
	if err != nil {
		return fmt.Errorf("delete channel model cost ratio: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ReplaceChannelModelCostRatios 全量替换某渠道的模型级系数行(管理面 PUT 语义)。
// 单事务:先清后插,中途失败整体回滚。写入的行一律 source=manual(人工编辑语义)。
func (s *Store) ReplaceChannelModelCostRatios(channelID int64, rows []domain.ModelCostRatioInput) error {
	if channelID <= 0 {
		return fmt.Errorf("渠道 ID 非法: %d", channelID)
	}
	seen := map[string]bool{}
	for _, r := range rows {
		if r.ModelName == "" {
			return fmt.Errorf("模型名不得为空")
		}
		if r.Ratio <= 0 {
			return ErrInvalidRatio
		}
		if seen[r.ModelName] {
			return fmt.Errorf("模型 %s 重复", r.ModelName)
		}
		seen[r.ModelName] = true
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM channel_model_cost_ratios WHERE channel_id = ?`, channelID); err != nil {
		return fmt.Errorf("clear channel model cost ratios: %w", err)
	}
	now := nowRFC3339()
	for _, r := range rows {
		if _, err := tx.Exec(`INSERT INTO channel_model_cost_ratios
			(channel_id, model_name, ratio, source, note, created_at, updated_at) VALUES (?,?,?,?,?,?,?)`,
			channelID, r.ModelName, r.Ratio, "manual", r.Note, now, now); err != nil {
			return fmt.Errorf("insert channel model cost ratio: %w", err)
		}
	}
	return tx.Commit()
}

// UpsertPlanRatios 订阅计划页**批量**写入某渠道的模型级系数(source=plan)。
//
// 人工行(source=manual)不被覆盖:`ON CONFLICT … DO UPDATE … WHERE source <> 'manual'` ——
// 自动派生只是「默认值」,站主手工改过的那几条必须留住(否则每次抓取都会把手填值冲掉)。
// 返回实际写入/更新的行数。
func (s *Store) UpsertPlanRatios(channelID int64, ratios map[string]float64) (int, error) {
	if channelID <= 0 {
		return 0, fmt.Errorf("渠道 ID 非法: %d", channelID)
	}
	if len(ratios) == 0 {
		return 0, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	now := nowRFC3339()
	written := 0
	for model, ratio := range ratios {
		if model == "" || ratio <= 0 {
			continue
		}
		res, err := tx.Exec(`INSERT INTO channel_model_cost_ratios
			(channel_id, model_name, ratio, source, note, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?)
			ON CONFLICT(channel_id, model_name) DO UPDATE SET
				ratio = excluded.ratio, source = excluded.source, note = excluded.note, updated_at = excluded.updated_at
			WHERE channel_model_cost_ratios.source <> 'manual'`,
			channelID, model, ratio, "plan", "订阅计划页自动派生", now, now)
		if err != nil {
			return 0, fmt.Errorf("upsert plan ratio %s: %w", model, err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			written++
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return written, nil
}

// CountChannelModelRatiosBySource 该渠道某来源的系数行数(source=plan 用于行数骤降护栏)。
func (s *Store) CountChannelModelRatiosBySource(channelID int64, source string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM channel_model_cost_ratios WHERE channel_id = ? AND source = ?`,
		channelID, source).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count channel model ratios: %w", err)
	}
	return n, nil
}
