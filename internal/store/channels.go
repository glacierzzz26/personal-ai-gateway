package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"personal-ai-gateway/internal/domain"
	"personal-ai-gateway/internal/secret"
)

func isUniqueErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique") || strings.Contains(msg, "2067")
}

// CreateChannel 新建渠道。apiKey 非空则加密落库,明文不落盘。
func (s *Store) CreateChannel(in domain.ChannelInput) (domain.ChannelRow, error) {
	in.Defaults()
	cipher, err := secret.Encrypt(in.APIKey)
	if err != nil {
		return domain.ChannelRow{}, err
	}
	now := formatRFC3339(s.nowUTC())
	res, err := s.db.Exec(`INSERT INTO channels (
		name, provider, base_url, api_key_cipher, key_masked,
		priority, weight, timeout_ms, tags, enabled, max_failures, cooldown_sec, note,
		created_at, updated_at
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		in.Name, in.Provider, in.BaseURL, cipher, domain.MaskKey(in.APIKey),
		in.Priority, in.Weight, in.TimeoutMs, encodeJSON(in.Tags), b2i(*in.Enabled),
		in.MaxFailures, in.CooldownSec, in.Note, now, now)
	if err != nil {
		if isUniqueErr(err) {
			return domain.ChannelRow{}, ErrConflict
		}
		return domain.ChannelRow{}, fmt.Errorf("insert channel: %w", err)
	}
	id, _ := res.LastInsertId()
	return s.GetChannel(id)
}

// UpdateChannel 整体更新渠道可编辑字段。apiKey 为空 = 保留原密文(不改密钥);
// 非空 = 换新密钥。改名撞重返回 ErrConflict。
func (s *Store) UpdateChannel(id int64, in domain.ChannelInput) (domain.ChannelRow, error) {
	in.Defaults()
	cur, err := s.GetChannel(id)
	if err != nil {
		return domain.ChannelRow{}, err
	}
	cipher, masked := cur.APIKeyCipher, cur.KeyMasked
	if in.APIKey != "" {
		cipher, err = secret.Encrypt(in.APIKey)
		if err != nil {
			return domain.ChannelRow{}, err
		}
		masked = domain.MaskKey(in.APIKey)
	}
	now := formatRFC3339(s.nowUTC())
	res, err := s.db.Exec(`UPDATE channels SET
		name=?, provider=?, base_url=?, api_key_cipher=?, key_masked=?,
		priority=?, weight=?, timeout_ms=?, tags=?, enabled=?, max_failures=?, cooldown_sec=?, note=?, updated_at=?
		WHERE id=?`,
		in.Name, in.Provider, in.BaseURL, cipher, masked,
		in.Priority, in.Weight, in.TimeoutMs, encodeJSON(in.Tags), b2i(*in.Enabled),
		in.MaxFailures, in.CooldownSec, in.Note, now, id)
	if err != nil {
		if isUniqueErr(err) {
			return domain.ChannelRow{}, ErrConflict
		}
		return domain.ChannelRow{}, fmt.Errorf("update channel %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ChannelRow{}, ErrNotFound
	}
	return s.GetChannel(id)
}

// SetChannelEnabled 快速启停(不改其余字段)。
func (s *Store) SetChannelEnabled(id int64, enabled bool) error {
	res, err := s.db.Exec(`UPDATE channels SET enabled=?, updated_at=? WHERE id=?`,
		b2i(enabled), formatRFC3339(s.nowUTC()), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetChannel 返回渠道整行(含密文;仅引擎/管理回显内部使用)。
func (s *Store) GetChannel(id int64) (domain.ChannelRow, error) {
	row := s.db.QueryRow(`SELECT id,name,provider,base_url,api_key_cipher,key_masked,
		priority,weight,timeout_ms,tags,enabled,max_failures,cooldown_sec,note,created_at,updated_at
		FROM channels WHERE id=?`, id)
	ch, err := scanChannel(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ChannelRow{}, ErrNotFound
	}
	return ch, err
}

// GetChannelByName 按唯一名查渠道。
func (s *Store) GetChannelByName(name string) (domain.ChannelRow, error) {
	row := s.db.QueryRow(`SELECT id,name,provider,base_url,api_key_cipher,key_masked,
		priority,weight,timeout_ms,tags,enabled,max_failures,cooldown_sec,note,created_at,updated_at
		FROM channels WHERE name=?`, name)
	ch, err := scanChannel(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ChannelRow{}, ErrNotFound
	}
	return ch, err
}

// ListChannels 返回全部渠道,priority 升序、同优后创建在前。
func (s *Store) ListChannels() ([]domain.ChannelRow, error) {
	rows, err := s.db.Query(`SELECT id,name,provider,base_url,api_key_cipher,key_masked,
		priority,weight,timeout_ms,tags,enabled,max_failures,cooldown_sec,note,created_at,updated_at
		FROM channels ORDER BY priority ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ChannelRow
	for rows.Next() {
		ch, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ch)
	}
	return out, rows.Err()
}

// DeleteChannel 删除渠道(其 model_offers 级联删除)。
func (s *Store) DeleteChannel(id int64) error {
	res, err := s.db.Exec(`DELETE FROM channels WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ChannelModelCounts 每个渠道挂载的供给源数量(map channelID→count),供列表回显。
func (s *Store) ChannelModelCounts() (map[int64]int, error) {
	rows, err := s.db.Query(`SELECT channel_id, COUNT(*) FROM model_offers GROUP BY channel_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int{}
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

type scanner interface{ Scan(dest ...any) error }

func scanChannel(row scanner) (domain.ChannelRow, error) {
	var ch domain.ChannelRow
	var tags string
	var enabled int
	var created, updated string
	if err := row.Scan(&ch.ID, &ch.Name, &ch.Provider, &ch.BaseURL, &ch.APIKeyCipher,
		&ch.KeyMasked, &ch.Priority, &ch.Weight, &ch.TimeoutMs, &tags, &enabled,
		&ch.MaxFailures, &ch.CooldownSec, &ch.Note, &created, &updated); err != nil {
		return domain.ChannelRow{}, err
	}
	ch.Tags = decodeStringList(tags)
	ch.Enabled = enabled == 1
	ch.CreatedAt, _ = parseTime(created)
	ch.UpdatedAt, _ = parseTime(updated)
	return ch, nil
}
