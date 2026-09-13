package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"personal-ai-gateway/internal/domain"
)

// ---------------- 模型目录 ----------------

// CreateModel 新建目录模型(模型名唯一)。
func (s *Store) CreateModel(in domain.ModelInput) (domain.ModelRow, error) {
	in.Defaults()
	now := formatRFC3339(s.nowUTC())
	display := ""
	if in.DisplayName != nil {
		display = strings.TrimSpace(*in.DisplayName)
	}
	if taken, err := s.publicNameTaken(in.Name, display, 0); err != nil {
		return domain.ModelRow{}, err
	} else if taken {
		return domain.ModelRow{}, ErrConflict
	}
	res, err := s.db.Exec(`INSERT INTO models (name, display_name, context_window, capabilities, enabled, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?)`,
		in.Name, display, in.ContextWindow, encodeJSON(in.Capabilities), b2i(*in.Enabled), now, now)
	if err != nil {
		if isUniqueErr(err) {
			return domain.ModelRow{}, ErrConflict
		}
		return domain.ModelRow{}, fmt.Errorf("insert model: %w", err)
	}
	id, _ := res.LastInsertId()
	return s.GetModel(id)
}

func (s *Store) GetModel(id int64) (domain.ModelRow, error) {
	row := s.db.QueryRow(`SELECT id,name,display_name,context_window,capabilities,enabled,created_at,updated_at
		FROM models WHERE id=?`, id)
	m, err := scanModel(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ModelRow{}, ErrNotFound
	}
	return m, err
}

// GetModelByName 供同步与去重(按渠道侧真实模型名)。
func (s *Store) GetModelByName(name string) (domain.ModelRow, error) {
	row := s.db.QueryRow(`SELECT id,name,display_name,context_window,capabilities,enabled,created_at,updated_at
		FROM models WHERE name=?`, name)
	m, err := scanModel(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ModelRow{}, ErrNotFound
	}
	return m, err
}

// GetModelByPublicName 模型面/选路按对外统一名解析:先查重命名后的 display_name,
// 未重命名时回落真实模型名,name 命中同样返回。供 engine 选路与令牌 allowed_models 校验。
func (s *Store) GetModelByPublicName(name string) (domain.ModelRow, error) {
	row := s.db.QueryRow(`SELECT id,name,display_name,context_window,capabilities,enabled,created_at,updated_at
		FROM models WHERE (display_name <> '' AND display_name = ?) OR name = ?
		ORDER BY CASE WHEN display_name = ? THEN 0 ELSE 1 END LIMIT 1`, name, name, name)
	m, err := scanModel(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ModelRow{}, ErrNotFound
	}
	return m, err
}

// canonicalModelKey 归并用的规范化模型名:取最后一个 '/' 之后的段,去空白,大小写归一。
// 用于把 `deepseek/deepseek-v4.1-flash` 与 `deepseek-v4.1-flash` 视为同一模型。
func canonicalModelKey(s string) string {
	if i := strings.LastIndexByte(s, '/'); i >= 0 {
		s = s[i+1:]
	}
	return strings.ToLower(strings.TrimSpace(s))
}

// GetModelByCanonical 按「规范名」解析模型,供渠道同步自动归并:
// 同一模型被不同渠道以带前缀/裸名上报时归到一行,而不是各建一行。
//
// 保守闸门:仅当「一方本就是无前缀裸名」才跨后缀归并 —— 这样
//   deepseek/deepseek-v4.1-flash 与 deepseek-v4.1-flash 归并(正中用户场景),
//   而 openai/gpt-4o 与 anthropic/gpt-4o(两边都带前缀)不会误合并,交给手工合并。
func (s *Store) GetModelByCanonical(raw string) (domain.ModelRow, error) {
	if m, err := s.GetModelByName(raw); err == nil {
		return m, nil
	} else if !errors.Is(err, ErrNotFound) {
		return domain.ModelRow{}, err
	}
	key := canonicalModelKey(raw)
	if m, err := s.GetModelByName(key); err == nil {
		return m, nil
	} else if !errors.Is(err, ErrNotFound) {
		return domain.ModelRow{}, err
	}
	models, err := s.ListModels()
	if err != nil {
		return domain.ModelRow{}, err
	}
	rawBare := raw == canonicalModelKey(raw)
	var hits []domain.ModelRow
	for _, m := range models {
		if canonicalModelKey(m.Name) != key {
			continue
		}
		if rawBare || m.Name == canonicalModelKey(m.Name) {
			hits = append(hits, m)
		}
	}
	// 含糊(多个同后缀候选)时不猜:返回未命中,让调用方新建一行 —— 归并成一行但归错模型
	// 比不归并更糟(历史/规则/令牌都挂在那一行上)。交由手工合并处理。
	if len(hits) == 1 {
		return hits[0], nil
	}
	return domain.ModelRow{}, ErrNotFound
}

// publicNameTaken 校验 name/display 是否会与别的模型在「对外名解析」上歧义:
// 统一名不得等于自身外的任何模型对外名(含其他模型真实名),否则按名解析会命中错模型。
// excludeID 为更新时的自身 id(新建传 0)。name 自身的重名由 models.name UNIQUE 兜底。
func (s *Store) publicNameTaken(name, display string, excludeID int64) (bool, error) {
	var n int
	if display != "" {
		err := s.db.QueryRow(`SELECT COUNT(*) FROM models
			WHERE id <> ? AND (display_name = ? OR name = ?)`, excludeID, display, display).Scan(&n)
		if err != nil {
			return false, err
		}
		if n > 0 {
			return true, nil
		}
	}
	err := s.db.QueryRow(`SELECT COUNT(*) FROM models
		WHERE id <> ? AND display_name <> '' AND display_name = ?`, excludeID, name).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// ListModels 全部目录模型,最新创建在前。
func (s *Store) ListModels() ([]domain.ModelRow, error) {
	rows, err := s.db.Query(`SELECT id,name,display_name,context_window,capabilities,enabled,created_at,updated_at
		FROM models ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ModelRow
	for rows.Next() {
		m, err := scanModel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// UpdateModel 更新名称/统一名/上下文/能力/启停。DisplayName 非 nil 才改动(空串=取消重命名)。
func (s *Store) UpdateModel(id int64, in domain.ModelInput) (domain.ModelRow, error) {
	in.Defaults()
	cur, err := s.GetModel(id)
	if err != nil {
		return domain.ModelRow{}, err
	}
	if in.Name == "" {
		in.Name = cur.Name
	}
	display := cur.DisplayName
	if in.DisplayName != nil {
		display = strings.TrimSpace(*in.DisplayName)
	}
	if taken, err := s.publicNameTaken(in.Name, display, id); err != nil {
		return domain.ModelRow{}, err
	} else if taken {
		return domain.ModelRow{}, ErrConflict
	}
	res, err := s.db.Exec(`UPDATE models SET name=?, display_name=?, context_window=?, capabilities=?, enabled=?, updated_at=?
		WHERE id=?`,
		in.Name, display, in.ContextWindow, encodeJSON(in.Capabilities), b2i(*in.Enabled),
		formatRFC3339(s.nowUTC()), id)
	if err != nil {
		if isUniqueErr(err) {
			return domain.ModelRow{}, ErrConflict
		}
		return domain.ModelRow{}, fmt.Errorf("update model %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ModelRow{}, ErrNotFound
	}
	return s.GetModel(id)
}

// SetModelEnabled 目录启停。
func (s *Store) SetModelEnabled(id int64, enabled bool) error {
	res, err := s.db.Exec(`UPDATE models SET enabled=?, updated_at=? WHERE id=?`,
		b2i(enabled), formatRFC3339(s.nowUTC()), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteModel(id int64) error {
	res, err := s.db.Exec(`DELETE FROM models WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// MergeModels 把 fromID 模型的全部供给源并入 intoID 模型,并删除 from 行(手工合并重复模型)。
// 单事务完成;同渠道冲突(两模型在同一 channel 都有 offer)返回 ErrConflict,由用户先删其一。
func (s *Store) MergeModels(fromID, intoID int64) (domain.ModelRow, error) {
	if fromID == intoID {
		return domain.ModelRow{}, ErrConflict
	}
	from, err := s.GetModel(fromID)
	if err != nil {
		return domain.ModelRow{}, err
	}
	if _, err := s.GetModel(intoID); err != nil {
		return domain.ModelRow{}, err
	}
	// 同渠道冲突预检:UNIQUE(model_id, channel_id) 会在搬移时撞键,先给明确错误。
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM model_offers a
		JOIN model_offers b ON a.channel_id = b.channel_id
		WHERE a.model_id=? AND b.model_id=?`, fromID, intoID).Scan(&n); err != nil {
		return domain.ModelRow{}, err
	}
	if n > 0 {
		return domain.ModelRow{}, ErrConflict
	}
	tx, err := s.db.Begin()
	if err != nil {
		return domain.ModelRow{}, err
	}
	defer tx.Rollback()
	// 关键:未配置上游名的 offer 先回填 from 的真实名,否则搬入后会用 into 的 name 路由到错模型。
	if _, err := tx.Exec(`UPDATE model_offers SET upstream_model=? WHERE model_id=? AND upstream_model=''`,
		from.Name, fromID); err != nil {
		return domain.ModelRow{}, err
	}
	if _, err := tx.Exec(`UPDATE model_offers SET model_id=? WHERE model_id=?`, intoID, fromID); err != nil {
		return domain.ModelRow{}, err
	}
	// 重排目标模型 priority=1..N(按原 priority,id 序),抽屉序不塌。
	rows, err := tx.Query(`SELECT id FROM model_offers WHERE model_id=? ORDER BY priority ASC, id ASC`, intoID)
	if err != nil {
		return domain.ModelRow{}, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return domain.ModelRow{}, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return domain.ModelRow{}, err
	}
	for i, oid := range ids {
		if _, err := tx.Exec(`UPDATE model_offers SET priority=? WHERE id=?`, i+1, oid); err != nil {
			return domain.ModelRow{}, err
		}
	}
	if _, err := tx.Exec(`DELETE FROM models WHERE id=?`, fromID); err != nil {
		return domain.ModelRow{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.ModelRow{}, err
	}
	return s.GetModel(intoID)
}

// CountModels 目录规模(供首启/空态判断)。
func (s *Store) CountModels() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM models`).Scan(&n)
	return n, err
}

func scanModel(row scanner) (domain.ModelRow, error) {
	var m domain.ModelRow
	var caps string
	var enabled int
	var created, updated string
	if err := row.Scan(&m.ID, &m.Name, &m.DisplayName, &m.ContextWindow, &caps, &enabled, &created, &updated); err != nil {
		return domain.ModelRow{}, err
	}
	m.Capabilities = decodeCaps(caps)
	m.Enabled = enabled == 1
	m.CreatedAt, _ = parseTime(created)
	m.UpdatedAt, _ = parseTime(updated)
	return m, nil
}

// decodeCaps "[]string" → []domain.Capability。
func decodeCaps(raw string) []domain.Capability {
	strs := decodeStringList(raw)
	out := make([]domain.Capability, 0, len(strs))
	for _, s := range strs {
		out = append(out, domain.Capability(s))
	}
	return out
}

// ---------------- 供给源(model_offers) ----------------

// CreateOffer 为模型挂载一个渠道供给源。(model_id, channel_id) 唯一,重复返回 ErrConflict。
// priority 为空时追加到队尾(max+1)。
func (s *Store) CreateOffer(modelID int64, in domain.OfferInput) (domain.OfferRead, error) {
	in.Defaults()
	if in.Priority == nil {
		var maxP int
		_ = s.db.QueryRow(`SELECT COALESCE(MAX(priority),0) FROM model_offers WHERE model_id=?`, modelID).Scan(&maxP)
		p := maxP + 1
		in.Priority = &p
	}
	res, err := s.db.Exec(`INSERT INTO model_offers (
		model_id, channel_id, input_price_usd, output_price_usd, cache_read_price_usd,
		override_price, priority, enabled, rate_limit_rpm, timeout_ms, note,
		price_source_url, price_fetched_at, price_currency, price_native_text, upstream_model
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		modelID, in.ChannelID, in.InputPriceUsd, in.OutputPriceUsd, in.CacheReadPriceUsd,
		b2i(in.OverridePrice), *in.Priority, b2i(*in.Enabled), in.RateLimitRpm, in.TimeoutMs, in.Note,
		in.PriceSourceURL, in.PriceFetchedAt, in.PriceCurrency, in.PriceNativeText, in.UpstreamModel)
	if err != nil {
		if isUniqueErr(err) {
			return domain.OfferRead{}, ErrConflict
		}
		return domain.OfferRead{}, fmt.Errorf("insert offer: %w", err)
	}
	id, _ := res.LastInsertId()
	return s.GetOffer(id)
}

// GetOffer 返回带渠道名/供应商/模型上下文的供给源读结构(动态字段待上层填充)。
func (s *Store) GetOffer(id int64) (domain.OfferRead, error) {
	row := s.db.QueryRow(offerSelect+` WHERE o.id=?`, id)
	of, err := scanOffer(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.OfferRead{}, ErrNotFound
	}
	return of, err
}

// ListModelOffers 某模型的全部供给源,priority 升序(即抽屉拖拽序)。
func (s *Store) ListModelOffers(modelID int64) ([]domain.OfferRead, error) {
	rows, err := s.db.Query(offerSelect+` WHERE o.model_id=? ORDER BY o.priority ASC, o.id ASC`, modelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.OfferRead
	for rows.Next() {
		of, err := scanOffer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, of)
	}
	return out, rows.Err()
}

// ListEnabledOffersForModel 只取启用供给源(引擎用),按 priority 升序。
func (s *Store) ListEnabledOffersForModel(modelID int64) ([]domain.OfferRead, error) {
	rows, err := s.db.Query(offerSelect+
		` WHERE o.model_id=? AND o.enabled=1 AND c.enabled=1 ORDER BY o.priority ASC, o.id ASC`, modelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.OfferRead
	for rows.Next() {
		of, err := scanOffer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, of)
	}
	return out, rows.Err()
}

// UpdateOffer 更新供给源(报价/启停/限流/备注等;priority 空=不动)。
func (s *Store) UpdateOffer(id int64, in domain.OfferInput) (domain.OfferRead, error) {
	in.Defaults()
	cur, err := s.GetOffer(id)
	if err != nil {
		return domain.OfferRead{}, err
	}
	if in.Priority == nil {
		p := cur.Priority
		in.Priority = &p
	}
	_, err = s.db.Exec(`UPDATE model_offers SET
		input_price_usd=?, output_price_usd=?, cache_read_price_usd=?, override_price=?,
		priority=?, enabled=?, rate_limit_rpm=?, timeout_ms=?, note=?,
		price_source_url=?, price_fetched_at=?, price_currency=?, price_native_text=?, upstream_model=?
		WHERE id=?`,
		in.InputPriceUsd, in.OutputPriceUsd, in.CacheReadPriceUsd, b2i(in.OverridePrice),
		*in.Priority, b2i(*in.Enabled), in.RateLimitRpm, in.TimeoutMs, in.Note,
		in.PriceSourceURL, in.PriceFetchedAt, in.PriceCurrency, in.PriceNativeText, in.UpstreamModel, id)
	if err != nil {
		return domain.OfferRead{}, fmt.Errorf("update offer %d: %w", id, err)
	}
	return s.GetOffer(id)
}

// SetModelOffersEnabled 批量启停某模型下全部供给源(启用模型时联动开启)。
func (s *Store) SetModelOffersEnabled(modelID int64, enabled bool) error {
	_, err := s.db.Exec(`UPDATE model_offers SET enabled=? WHERE model_id=?`, b2i(enabled), modelID)
	return err
}

// SetOfferEnabled 供给源启停(不改报价)。
func (s *Store) SetOfferEnabled(id int64, enabled bool) error {
	res, err := s.db.Exec(`UPDATE model_offers SET enabled=? WHERE id=?`, b2i(enabled), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetOfferUpstreamModel 只改上游真实模型名(同步回写用;不动报价/启停,避免全量替换误清字段)。
func (s *Store) SetOfferUpstreamModel(id int64, upstream string) error {
	res, err := s.db.Exec(`UPDATE model_offers SET upstream_model=? WHERE id=?`, upstream, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteOffer 移除供给源。
func (s *Store) DeleteOffer(id int64) error {
	res, err := s.db.Exec(`DELETE FROM model_offers WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ReorderOffers 将某模型供给源 fromIdx(当前序)移到 insertAt 并重排 priority=1..N。
// 用于抽屉拖拽与「设为首选」。返回重排后的供给源列表。
func (s *Store) ReorderOffers(modelID int64, fromIdx, insertAt int) ([]domain.OfferRead, error) {
	offers, err := s.ListModelOffers(modelID)
	if err != nil {
		return nil, err
	}
	if fromIdx < 0 || fromIdx >= len(offers) {
		return nil, fmt.Errorf("reorder from=%d out of range (len %d)", fromIdx, len(offers))
	}
	if insertAt < 0 {
		insertAt = 0
	}
	if insertAt > len(offers) {
		insertAt = len(offers)
	}
	// 切分索引序列做 move
	ids := make([]int64, 0, len(offers))
	for _, o := range offers {
		ids = append(ids, o.ID)
	}
	moved := ids[fromIdx]
	ids = append(ids[:fromIdx], ids[fromIdx+1:]...)
	// 目标位换算:若 insertAt 在被移除元素之后需 -1?调用方按「老列表位次」给值
	// 语义与 ModelDrawer(v.from, v.insertAt)一致:insertAt 是移动后的插入位(0 基)
	if insertAt > len(ids) {
		insertAt = len(ids)
	}
	ids = append(ids[:insertAt], append([]int64{moved}, ids[insertAt:]...)...)

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	for i, oid := range ids {
		if _, err := tx.Exec(`UPDATE model_offers SET priority=? WHERE id=?`, i+1, oid); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.ListModelOffers(modelID)
}

const offerSelect = `SELECT o.id, o.model_id, o.channel_id, c.name, c.provider,
	o.input_price_usd, o.output_price_usd, o.cache_read_price_usd, o.override_price,
	o.priority, o.enabled, o.rate_limit_rpm, o.timeout_ms, o.note,
	m.context_window, c.enabled,
	o.price_source_url, o.price_fetched_at, o.price_currency, o.price_native_text, o.upstream_model
	FROM model_offers o
	JOIN channels c ON c.id = o.channel_id
	JOIN models   m ON m.id = o.model_id`

func scanOffer(row scanner) (domain.OfferRead, error) {
	var of domain.OfferRead
	var provider domain.Provider
	var overridePrice, enabled, channelEnabled int
	var timeout sql.NullInt64
	if err := row.Scan(&of.ID, &of.ModelID, &of.ChannelID, &of.ChannelName, &provider,
		&of.InputPriceUsd, &of.OutputPriceUsd, &of.CacheReadPriceUsd, &overridePrice,
		&of.Priority, &enabled, &of.RateLimitRpm, &timeout, &of.Note,
		&of.ContextWindow, &channelEnabled,
		&of.PriceSourceURL, &of.PriceFetchedAt, &of.PriceCurrency, &of.PriceNativeText, &of.UpstreamModel); err != nil {
		return domain.OfferRead{}, err
	}
	of.Provider = provider
	of.OverridePrice = overridePrice == 1
	of.Enabled = enabled == 1
	if timeout.Valid {
		v := int(timeout.Int64)
		of.TimeoutMs = &v
	}
	// 动态展示字段(延迟/成功率/状态)由上层实时填充;此处给安全默认
	of.Status = domain.StatusUnknown
	if channelEnabled == 0 {
		of.Status = domain.StatusDisabled
	}
	return of, nil
}

// ChannelEnabled 校验渠道存在且启用(供引擎)。
func (s *Store) ChannelEnabled(id int64) (bool, error) {
	var e int
	err := s.db.QueryRow(`SELECT enabled FROM channels WHERE id=?`, id).Scan(&e)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, err
	}
	return e == 1, nil
}

// EnabledModelsWithOffers 目录中「enabled 且有启用 offer」的模型(驱动 GET /v1/models)。
func (s *Store) EnabledModelsWithOffers() ([]domain.ModelRead, error) {
	rows, err := s.db.Query(`SELECT DISTINCT m.id, m.name, m.display_name, m.context_window, m.capabilities, m.enabled
		FROM models m
		JOIN model_offers o ON o.model_id = m.id
		JOIN channels c ON c.id = o.channel_id
		WHERE m.enabled=1 AND o.enabled=1 AND c.enabled=1
		ORDER BY m.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ModelRead
	for rows.Next() {
		var mr domain.ModelRead
		var name, display, caps string
		var enabled int
		if err := rows.Scan(&mr.ID, &name, &display, &mr.ContextWindow, &caps, &enabled); err != nil {
			return nil, err
		}
		mr.OriginalName = name
		mr.Name = name
		if display != "" {
			mr.DisplayName = display
			mr.Name = display
		}
		mr.Capabilities = decodeCaps(caps)
		mr.Enabled = enabled == 1
		mr.Offers, err = s.ListModelOffers(mr.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, mr)
	}
	return out, rows.Err()
}
