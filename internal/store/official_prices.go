package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"personal-ai-gateway/internal/domain"
)

// ---------------- 官方参考价(official_prices) ----------------

// UpsertOfficialPrice 写入/更新一条官方参考价,唯一键 (provider, model_name)。
// 抓取成功的新价覆盖旧价,但 created_at 保留首次写入时间。
func (s *Store) UpsertOfficialPrice(q domain.OfficialPriceRow) (domain.OfficialPriceRow, error) {
	if q.Provider == "" || q.ModelName == "" {
		return domain.OfficialPriceRow{}, fmt.Errorf("provider/model_name 必填")
	}
	now := formatRFC3339(s.nowUTC())
	detail := encodeJSON(orEmptyMap(q.Detail))
	fetched := formatRFC3339(q.FetchedAt)
	res, err := s.db.Exec(`INSERT INTO official_prices (
		provider, model_name, source_url, fetched_at, currency, billing_shape,
		in_price, out_price, cache_read_price, cache_derived, native_text,
		detail_json, content_sha256, created_at, updated_at
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	ON CONFLICT(provider, model_name) DO UPDATE SET
		source_url=excluded.source_url, fetched_at=excluded.fetched_at,
		currency=excluded.currency, billing_shape=excluded.billing_shape,
		in_price=excluded.in_price, out_price=excluded.out_price,
		cache_read_price=excluded.cache_read_price, cache_derived=excluded.cache_derived,
		native_text=excluded.native_text, detail_json=excluded.detail_json,
		content_sha256=excluded.content_sha256, updated_at=excluded.updated_at`,
		string(q.Provider), q.ModelName, q.SourceURL, fetched, string(q.Currency), string(q.BillingShape),
		q.InputPrice, q.OutputPrice, q.CacheReadPrice, b2i(q.CacheDerived), q.NativeText,
		detail, q.ContentSHA256, now, now)
	if err != nil {
		return domain.OfficialPriceRow{}, fmt.Errorf("upsert official price: %w", err)
	}
	_ = res
	row, err := s.GetOfficialPriceByName(q.Provider, q.ModelName)
	if err != nil {
		return domain.OfficialPriceRow{}, err
	}
	return row, nil
}

// GetOfficialPriceByName 按 (provider, model_name) 取一条。
func (s *Store) GetOfficialPriceByName(p domain.Provider, model string) (domain.OfficialPriceRow, error) {
	row := s.db.QueryRow(officialPriceSelect+` WHERE provider=? AND model_name=?`, string(p), model)
	q, err := scanOfficialPrice(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.OfficialPriceRow{}, ErrNotFound
	}
	return q, err
}

// GetOfficialPrice 按 id 取一条。
func (s *Store) GetOfficialPrice(id int64) (domain.OfficialPriceRow, error) {
	row := s.db.QueryRow(officialPriceSelect+` WHERE id=?`, id)
	q, err := scanOfficialPrice(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.OfficialPriceRow{}, ErrNotFound
	}
	return q, err
}

// ListOfficialPrices 某 provider 的官方参考价(模型名升序)。
func (s *Store) ListOfficialPrices(p domain.Provider) ([]domain.OfficialPriceRow, error) {
	return s.queryOfficialPrices(` WHERE provider=? ORDER BY model_name ASC`, string(p))
}

// ListAllOfficialPrices 全部官方参考价(模型广场「查看官方参考价」用)。
func (s *Store) ListAllOfficialPrices() ([]domain.OfficialPriceRow, error) {
	return s.queryOfficialPrices(` ORDER BY provider ASC, model_name ASC`)
}

func (s *Store) queryOfficialPrices(where string, args ...any) ([]domain.OfficialPriceRow, error) {
	rows, err := s.db.Query(officialPriceSelect+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.OfficialPriceRow
	for rows.Next() {
		q, err := scanOfficialPrice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// DeleteOfficialPrice 删除一条官方参考价(不影响已应用到 offer 的价与来源留证)。
func (s *Store) DeleteOfficialPrice(id int64) error {
	res, err := s.db.Exec(`DELETE FROM official_prices WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteOfficialPricesNotIn 删除该厂商「来源为 sourceURL 且模型名不在 keep 中」的官方价行,
// 返回删除条数。抓取对账用:页面已不再列出的模型视为下架,清掉陈旧行,避免解析器修正后
// 旧错误行(如曾被误并入通义千问的第三方模型)永远留在库里。keep 为空时不删(防御:宁留不误删)。
// 只按 model_name 过滤 — 模型名不会在厂商间碰撞;已应用到 offer 的价与来源留证不受影响。
func (s *Store) DeleteOfficialPricesNotIn(p domain.Provider, sourceURL string, keep []string) (int64, error) {
	if len(keep) == 0 {
		return 0, nil
	}
	ph := make([]string, len(keep))
	args := make([]any, 0, len(keep)+2)
	args = append(args, string(p), sourceURL)
	for i, name := range keep {
		ph[i] = "?"
		args = append(args, name)
	}
	q := `DELETE FROM official_prices WHERE provider=? AND source_url=? AND model_name NOT IN (` +
		strings.Join(ph, ",") + `)`
	res, err := s.db.Exec(q, args...)
	if err != nil {
		return 0, fmt.Errorf("reconcile official prices: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// ApplyOfficialPrice 把官方价应用到某 offer:写三价 + 来源留证四字段。
//
// 只动价与 provenance,不改 override_price、不改启停 —— 「手工覆盖价优先」由上层
// 依据 offer.OverridePrice 决定是否放行(需二次确认),store 层不做策略判断。
// usd 三参为换汇后的 USD 价(原币为 USD 时等于原价;CNY 无汇率时由上层拒绝应用)。
func (s *Store) ApplyOfficialPrice(offerID int64, q domain.OfficialPriceRow, usdIn, usdOut, usdCache float64) error {
	res, err := s.db.Exec(`UPDATE model_offers SET
		input_price_usd=?, output_price_usd=?, cache_read_price_usd=?,
		price_source_url=?, price_fetched_at=?, price_currency=?, price_native_text=?
		WHERE id=?`,
		usdIn, usdOut, usdCache,
		q.SourceURL, formatRFC3339(q.FetchedAt), string(q.Currency), q.NativeText, offerID)
	if err != nil {
		return fmt.Errorf("apply official price to offer %d: %w", offerID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// OfferPriceSource 读某 offer 的来源留证(判断「是否已应用该官方价」)。
func (s *Store) OfferPriceSource(offerID int64) (url, fetchedAt, currency string, err error) {
	err = s.db.QueryRow(`SELECT price_source_url, price_fetched_at, price_currency
		FROM model_offers WHERE id=?`, offerID).Scan(&url, &fetchedAt, &currency)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", "", ErrNotFound
	}
	return url, fetchedAt, currency, err
}

const officialPriceSelect = `SELECT id, provider, model_name, source_url, fetched_at,
	currency, billing_shape, in_price, out_price, cache_read_price, cache_derived,
	native_text, detail_json, content_sha256, created_at, updated_at
	FROM official_prices`

func scanOfficialPrice(row scanner) (domain.OfficialPriceRow, error) {
	var q domain.OfficialPriceRow
	var provider, currency, shape, fetched, detail, created, updated string
	var derived int
	if err := row.Scan(&q.ID, &provider, &q.ModelName, &q.SourceURL, &fetched,
		&currency, &shape, &q.InputPrice, &q.OutputPrice, &q.CacheReadPrice, &derived,
		&q.NativeText, &detail, &q.ContentSHA256, &created, &updated); err != nil {
		return domain.OfficialPriceRow{}, err
	}
	q.Provider = domain.Provider(provider)
	q.Currency = domain.Currency(currency)
	q.BillingShape = domain.BillingShape(shape)
	q.CacheDerived = derived == 1
	q.Detail = decodeAnyMap(detail)
	q.FetchedAt, _ = parseTime(fetched)
	q.CreatedAt, _ = parseTime(created)
	q.UpdatedAt, _ = parseTime(updated)
	return q, nil
}

// orEmptyMap 避免把 nil map 编成 "null"(统一 "{}")。
func orEmptyMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}
