// api_keys 表:运行时生成的模型面 API key(与 config 登录 key 分开)。
// 密钥只存 sha256,明文只出现一次(创建响应),绝不落库/日志;prefix 仅供列表展示。
// 吊销是软删(revoked=1,保留行做审计),幂等。
package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// ErrKeyNotFound:按 name/secret 查不到(或已吊销的 secret 也归它,见 LookupActiveKey)。
var ErrKeyNotFound = errors.New("store: key not found")

// ApiKey 是 api_keys 表的一行。哈希永远不离开 store —— 对外只给展示字段。
type ApiKey struct {
	ID        int64
	Name      string
	Prefix    string // 展示用,secret 前 12 字符
	Note      string
	Revoked   bool
	CreatedAt time.Time
	RevokedAt *time.Time // nil = 激活
}

const keyCols = "id, name, prefix, note, revoked, created_at, revoked_at"

// HashSecret 计算存储用哈希(sha256 hex)。导出供测试与外部比对。
func HashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// CreateKey 落一条 key;hash/prefix 在此边界计算,handler 只负责生成明文 secret。
// name 重复(或 sha256 重复)→ 返回带 "UNIQUE" 的包装错误,由上层映射成 409。
func (s *Store) CreateKey(name, note, secret string) (ApiKey, error) {
	if s == nil || s.db == nil {
		return ApiKey{}, errors.New("store: not open")
	}
	if name == "" || secret == "" {
		return ApiKey{}, errors.New("store: create key: name and secret required")
	}
	now := time.Now().UTC()
	prefix := secret
	if len(prefix) > 12 {
		prefix = secret[:12]
	}
	_, err := s.db.Exec(`INSERT INTO api_keys (name, prefix, sha256, note, created_at) VALUES (?, ?, ?, ?, ?)`,
		name, prefix, HashSecret(secret), note, now.Format(time.RFC3339Nano))
	if err != nil {
		return ApiKey{}, fmt.Errorf("store: create key: %w", err)
	}
	return fetchKeyBy(s, "name", name)
}

// ListKeys 按 id 升序返回全部行(含已吊销),供管理列表与名称查重。
func (s *Store) ListKeys() ([]ApiKey, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("store: not open")
	}
	rows, err := s.db.Query(`SELECT ` + keyCols + ` FROM api_keys ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("store: list keys: %w", err)
	}
	defer rows.Close()
	var out []ApiKey
	for rows.Next() {
		k, err := scanKey(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan key: %w", err)
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// LookupActiveKey 按明文 secret 找一条未吊销的 key(鉴权用)。
// 哈希比对走索引列;查不到或已吊销 → ErrKeyNotFound(不区分,对外不泄露)。
func (s *Store) LookupActiveKey(secret string) (ApiKey, error) {
	if s == nil || s.db == nil {
		return ApiKey{}, errors.New("store: not open")
	}
	return fetchKeyBy(s, "sha256", HashSecret(secret), "revoked = 0")
}

// RevokeKey 吊销(幂等):已吊销的行返回原状,revoked_at 保持首次吊销时间。
// name 不存在 → ErrKeyNotFound。
func (s *Store) RevokeKey(name string, at time.Time) (ApiKey, error) {
	if s == nil || s.db == nil {
		return ApiKey{}, errors.New("store: not open")
	}
	if _, err := s.db.Exec(`UPDATE api_keys SET revoked = 1, revoked_at = ? WHERE name = ? AND revoked = 0`,
		at.UTC().Format(time.RFC3339Nano), name); err != nil {
		return ApiKey{}, fmt.Errorf("store: revoke key: %w", err)
	}
	return fetchKeyBy(s, "name", name)
}

// fetchKeyBy 按列取一行(可带附加条件),NotFound → ErrKeyNotFound。
func fetchKeyBy(s *Store, col, val string, extra ...string) (ApiKey, error) {
	where := col + " = ?"
	args := []any{val}
	if len(extra) > 0 {
		where += " AND " + extra[0]
	}
	row := s.db.QueryRow(`SELECT `+keyCols+` FROM api_keys WHERE `+where, args...)
	k, err := scanKey(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ApiKey{}, ErrKeyNotFound
	}
	return k, err
}

type scanner interface{ Scan(dest ...any) error }

func scanKey(sc scanner) (ApiKey, error) {
	var (
		k         ApiKey
		revoked   int
		createdAt string
		revokedAt string
	)
	if err := sc.Scan(&k.ID, &k.Name, &k.Prefix, &k.Note, &revoked, &createdAt, &revokedAt); err != nil {
		return ApiKey{}, err
	}
	k.Revoked = revoked != 0
	if t, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
		k.CreatedAt = t
	}
	if revokedAt != "" {
		if t, err := time.Parse(time.RFC3339Nano, revokedAt); err == nil {
			k.RevokedAt = &t
		}
	}
	return k, nil
}
