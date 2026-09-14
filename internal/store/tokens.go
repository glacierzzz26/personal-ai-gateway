package store

import (
	"database/sql"
	"errors"
	"strconv"
	"time"

	"personal-ai-gateway/internal/domain"
)

// ErrQuotaExceeded 令牌额度不足(扣减被拒)。
var ErrQuotaExceeded = errors.New("token quota exceeded")

// CreateToken 落库新令牌。sha/masked 由调用方(鉴权层)按密钥生成;
// keyCipher 为明文的 AES-GCM 密文(供回显/生成配置;空=不可回显);
// ownerID 为归属账号(nil=全局 key)。status 恒为 active,后续用 SetTokenStatus 停用。
func (s *Store) CreateToken(name string, ownerID *int64, keyCipher string, allowed []string,
	quotaUsd float64, rpm int, expiresAt *string, sha, masked string) (domain.TokenRead, error) {
	now := formatRFC3339(s.nowUTC())
	_, err := s.db.Exec(`INSERT INTO tokens (
		name, sha256, key_masked, allowed_models, quota_usd, used_usd, rpm_limit,
		expires_at, status, created_at, updated_at, owner_id, key_cipher
	) VALUES (?,?,?,?,?,0,?,?, 'active', ?,?,?,?)`,
		name, sha, masked, encodeJSON(allowed), quotaUsd, rpm,
		expiresAt, now, now, ownerID, keyCipher)
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
	row := s.db.QueryRow(tokenCols+` WHERE t.id=?`, id)
	tk, err := scanToken(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.TokenRead{}, ErrNotFound
	}
	return tk, err
}

// ListTokens 令牌列表,新建在前。ownerID=nil 返回全部(管理员);非 nil 只返回该账号名下。
func (s *Store) ListTokens(ownerID *int64) ([]domain.TokenRead, error) {
	q := tokenCols
	args := []any{}
	if ownerID != nil {
		q += ` WHERE t.owner_id = ?`
		args = append(args, *ownerID)
	}
	q += ` ORDER BY t.id DESC`
	rows, err := s.db.Query(q, args...)
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

// TokenKeyCipher 读单条令牌的明文密文(仅生成配置端点使用;不进入列表读结构)。
func (s *Store) TokenKeyCipher(id int64) (string, error) {
	var cipher string
	err := s.db.QueryRow(`SELECT key_cipher FROM tokens WHERE id=?`, id).Scan(&cipher)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return cipher, err
}

// LookupTokenBySHA256 模型面鉴权用:按密钥哈希精确查,并顺带带出归属账号的钱包
// (角色/余额/倍率覆盖)——数据面每请求都要判断余额门禁与算售价,避免再查一次。
func (s *Store) LookupTokenBySHA256(sha string) (domain.TokenRow, error) {
	var tk domain.TokenRow
	var allowed string
	var expires, lastUsed, ownerID sql.NullString
	var rateOverride sql.NullFloat64
	var ownerRole string
	var created, updated string
	err := s.db.QueryRow(`SELECT t.id,t.name,t.sha256,t.key_masked,t.allowed_models,t.quota_usd,t.used_usd,
		t.rpm_limit,t.expires_at,t.status,t.last_used_at,t.owner_id,
		COALESCE(a.role,''), COALESCE(a.balance_usd,0), a.rate_override
		FROM tokens t LEFT JOIN admins a ON a.id = t.owner_id WHERE t.sha256=?`, sha).
		Scan(&tk.ID, &tk.Name, &tk.SHA256, &tk.KeyMasked, &allowed, &tk.QuotaUsd, &tk.UsedUsd,
			&tk.RpmLimit, &expires, &tk.Status, &lastUsed, &ownerID,
			&ownerRole, &tk.OwnerBalance, &rateOverride)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.TokenRow{}, ErrNotFound
	}
	if err != nil {
		return domain.TokenRow{}, err
	}
	tk.AllowedModels = decodeStringList(allowed)
	tk.OwnerID = nullInt64Ptr(ownerID)
	tk.OwnerRole = domain.Role(ownerRole)
	if rateOverride.Valid {
		v := rateOverride.Float64
		tk.OwnerRateOverride = &v
	}
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

// ChargeToken 请求结束累加用量并刷新 last_used_at。quota_usd<=0 视为不限。
//
// 只累加、不设上限:额度是否够由入口预检查(tokenGateErr)判定,结算一律落账。
// 若这里也带上限条件,当「剩余额度 < 一笔成本」时 UPDATE 会命中 0 行 —— 而调用方无法
// 把它转成真正的拒绝(响应已发出),只会让 used_usd 永远不前进,变成无限白跑。故此处
// 允许 used_usd 越过 quota_usd(透支至多一笔),由入口在下一笔请求上稳定返回 402。
func (s *Store) ChargeToken(id int64, costUsd float64) error {
	if costUsd < 0 {
		return nil
	}
	now := formatRFC3339(s.nowUTC())
	res, err := s.db.Exec(`UPDATE tokens SET used_usd = used_usd + ?, last_used_at = ?, updated_at = ?
		WHERE id=? AND status='active'`,
		costUsd, now, now, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
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

// tokenCols 令牌列表/详情读结构:LEFT JOIN 账号取归属名,并以 key_cipher 是否为空
// 派生「可回显」(不把密文本身带出)。
const tokenCols = `SELECT t.id, t.name, t.sha256, t.key_masked, t.allowed_models, t.quota_usd, t.used_usd,
	t.rpm_limit, t.expires_at, t.status, t.last_used_at, t.created_at,
	t.owner_id, COALESCE(a.username, ''), (t.key_cipher <> '')
	FROM tokens t LEFT JOIN admins a ON a.id = t.owner_id`

// scanToken 按 tokenCols(15 列)顺序扫;sha256 不展露,用占位变量丢弃。
func scanToken(row scanner) (domain.TokenRead, error) {
	var tk domain.TokenRead
	var shaIgnored string
	var allowed string
	var expires, lastUsed, ownerID sql.NullString
	var created string
	var retrievable int
	if err := row.Scan(&tk.ID, &tk.Name, &shaIgnored, &tk.KeyMasked, &allowed,
		&tk.QuotaUsd, &tk.UsedUsd, &tk.RpmLimit, &expires, &tk.Status, &lastUsed, &created,
		&ownerID, &tk.OwnerName, &retrievable); err != nil {
		return domain.TokenRead{}, err
	}
	tk.AllowedModels = decodeStringList(allowed)
	tk.OwnerID = nullInt64Ptr(ownerID)
	tk.KeyRetrievable = retrievable == 1
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

func nullInt64Ptr(n sql.NullString) *int64 {
	if !n.Valid {
		return nil
	}
	v, err := strconv.ParseInt(n.String, 10, 64)
	if err != nil {
		return nil
	}
	return &v
}
