package store

import (
	"database/sql"
	"errors"
	"time"

	"personal-ai-gateway/internal/domain"
)

// ErrQuotaExceeded 令牌额度不足(扣减被拒)。
var ErrQuotaExceeded = errors.New("token quota exceeded")

// CreateToken 落库新令牌。sha/masked 由调用方(鉴权层)按密钥生成;
// status 恒为 active,后续用 SetTokenStatus 停用。返回带派生状态的读结构。
func (s *Store) CreateToken(name string, allowed []string, quotaUsd float64, rpm int,
	expiresAt *string, sha, masked string) (domain.TokenRead, error) {
	now := formatRFC3339(s.nowUTC())
	_, err := s.db.Exec(`INSERT INTO tokens (
		name, sha256, key_masked, allowed_models, quota_usd, used_usd, rpm_limit,
		expires_at, status, created_at, updated_at
	) VALUES (?,?,?,?,?,0,?,?, 'active', ?,?)`,
		name, sha, masked, encodeJSON(allowed), quotaUsd, rpm,
		expiresAt, now, now)
	if err != nil {
		if isUniqueErr(err) {
			return domain.TokenRead{}, ErrConflict
		}
		return domain.TokenRead{}, err
	}
	var id int64
	if err := s.db.QueryRow(`SELECT id FROM tokens WHERE sha256=?`, sha).Scan(&id); err != nil {
		return domain.TokenRead{}, err
	}
	return s.GetToken(id)
}

// GetToken 读单条令牌(派生 expired 状态)。
func (s *Store) GetToken(id int64) (domain.TokenRead, error) {
	row := s.db.QueryRow(tokenCols+` WHERE id=?`, id)
	tk, err := scanToken(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.TokenRead{}, ErrNotFound
	}
	return tk, err
}

// ListTokens 全部令牌,新建在前。
func (s *Store) ListTokens() ([]domain.TokenRead, error) {
	rows, err := s.db.Query(tokenCols + ` ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.TokenRead
	for rows.Next() {
		tk, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, tk)
	}
	return out, rows.Err()
}

// LookupTokenBySHA256 模型面鉴权用:按密钥哈希精确查。
func (s *Store) LookupTokenBySHA256(sha string) (domain.TokenRow, error) {
	var tk domain.TokenRow
	var allowed string
	var expires, lastUsed sql.NullString
	var created, updated string
	err := s.db.QueryRow(`SELECT id,name,sha256,key_masked,allowed_models,quota_usd,used_usd,
		rpm_limit,expires_at,status,last_used_at FROM tokens WHERE sha256=?`, sha).
		Scan(&tk.ID, &tk.Name, &tk.SHA256, &tk.KeyMasked, &allowed, &tk.QuotaUsd, &tk.UsedUsd,
			&tk.RpmLimit, &expires, &tk.Status, &lastUsed)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.TokenRow{}, ErrNotFound
	}
	if err != nil {
		return domain.TokenRow{}, err
	}
	tk.AllowedModels = decodeStringList(allowed)
	if expires.Valid {
		v := expires.String
		tk.ExpiresAt = &v
	}
	if lastUsed.Valid {
		v := lastUsed.String
		tk.LastUsedAt = &v
	}
	// 派生状态:存库只留 active/disabled,"expired" 每次读取现算
	if tk.Status == domain.TokenActive && isExpired(expires) {
		tk.Status = domain.TokenExpired
	}
	_ = created
	_ = updated
	return tk, nil
}

// UpdateToken 更新令牌可编辑字段。quota 变化不清 used_usd。
func (s *Store) UpdateToken(id int64, in domain.TokenInput) (domain.TokenRead, error) {
	if in.Status == nil {
		s := domain.TokenActive
		in.Status = &s
	}
	now := formatRFC3339(s.nowUTC())
	res, err := s.db.Exec(`UPDATE tokens SET
		name=?, allowed_models=?, quota_usd=?, rpm_limit=?, expires_at=?, status=?, updated_at=?
		WHERE id=?`,
		in.Name, encodeJSON(in.AllowedModels), in.QuotaUsd, in.RpmLimit,
		in.ExpiresAt, *in.Status, now, id)
	if err != nil {
		return domain.TokenRead{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.TokenRead{}, ErrNotFound
	}
	return s.GetToken(id)
}

// SetTokenStatus 快速启停令牌。
func (s *Store) SetTokenStatus(id int64, status domain.TokenStatus) error {
	res, err := s.db.Exec(`UPDATE tokens SET status=?, updated_at=? WHERE id=?`,
		status, formatRFC3339(s.nowUTC()), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteToken 删除令牌(历史日志里的名称留档)。
func (s *Store) DeleteToken(id int64) error {
	res, err := s.db.Exec(`DELETE FROM tokens WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ChargeToken 请求结束扣减额度并刷新 last_used_at。quota_usd<=0 视为不限。
// 原子条件更新,避免并发超扣;超出返回 ErrQuotaExceeded。
func (s *Store) ChargeToken(id int64, costUsd float64) error {
	if costUsd < 0 {
		return nil
	}
	now := formatRFC3339(s.nowUTC())
	res, err := s.db.Exec(`UPDATE tokens SET used_usd = used_usd + ?, last_used_at = ?, updated_at = ?
		WHERE id=? AND status='active'
		  AND (quota_usd <= 0 OR used_usd + ? <= quota_usd + 0.0000001)`,
		costUsd, now, now, id, costUsd)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrQuotaExceeded
	}
	return nil
}

// parseExpires 解析 tokens.expires_at:库内新写入为 UTC RFC3339Nano,但历史/直连测试
// 可能落宽松格式(YYYY-MM-DD、带空格的本地时间)。统一在此容错,避免与数据面 parseDate
// 口径分裂 —— 管理台显示状态与网关放行判定必须一致。
func parseExpires(s string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func isExpired(e sql.NullString) bool {
	if !e.Valid || e.String == "" {
		return false
	}
	t, ok := parseExpires(e.String)
	if !ok {
		return false
	}
	return !t.After(time.Now().UTC())
}

const tokenCols = `SELECT id,name,sha256,key_masked,allowed_models,quota_usd,used_usd,
	rpm_limit,expires_at,status,last_used_at,created_at FROM tokens`

// scanToken 按 tokenCols(12 列)顺序扫;sha256 不展露,用占位变量丢弃。
func scanToken(row scanner) (domain.TokenRead, error) {
	var tk domain.TokenRead
	var shaIgnored string
	var allowed string
	var expires, lastUsed sql.NullString
	var created string
	if err := row.Scan(&tk.ID, &tk.Name, &shaIgnored, &tk.KeyMasked, &allowed,
		&tk.QuotaUsd, &tk.UsedUsd, &tk.RpmLimit, &expires, &tk.Status, &lastUsed, &created); err != nil {
		return domain.TokenRead{}, err
	}
	tk.AllowedModels = decodeStringList(allowed)
	if expires.Valid {
		v := expires.String
		tk.ExpiresAt = &v
	}
	if lastUsed.Valid {
		v := lastUsed.String
		tk.LastUsedAt = &v
	}
	if tk.Status == domain.TokenActive && isExpired(expires) {
		tk.Status = domain.TokenExpired
	}
	tk.CreatedAt, _ = parseTime(created)
	return tk, nil
}
