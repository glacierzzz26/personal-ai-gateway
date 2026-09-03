// Package store 提供 v2 网关的全部持久化:版本化 schema 迁移 + 各业务仓库。
//
// 时间口径:request_logs.ts / 各 *_at 一律存 UTC RFC3339Nano 文本;
// 展示与聚合按 settings.tz_offset_min(默认 480)换算,见 logs.go 桶查询。
// 渠道 api_key 只存密文(secret 包 AES-GCM),明文不落库。
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// ErrNotFound 记录不存在(查询/更新/删除目标缺失)。
var ErrNotFound = errors.New("record not found")

// ErrConflict 唯一性冲突(名称/模型-渠道 组合已存在)。
var ErrConflict = errors.New("record conflict")

// Store 网关数据访问门面。并发安全(sql.DB 自带池)。
type Store struct {
	db  *sql.DB
	now func() time.Time
}

// Open 打开(必要时创建)数据库并执行版本化迁移。
func Open(path string) (*Store, error) {
	if path == "" {
		path = "gateway-v2.db"
	}
	dsn, err := sqliteDSN(path)
	if err != nil {
		return nil, fmt.Errorf("store dsn: %w", err)
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	// busy_timeout 由 pragma 保证;WAL 下读写可并行
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate %s: %w", path, err)
	}
	return &Store{db: db, now: time.Now}, nil
}

// Close 关闭底层连接。
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) nowUTC() time.Time { return s.now().UTC() }

// sqliteDSN 把路径转成带 pragma 的 file: DSN(modernc.org/sqlite)。
// 相对路径先转绝对,避免 URI 语义歧义;WAL + 外键 + 忙等待。
func sqliteDSN(path string) (string, error) {
	if path == ":memory:" {
		return "file:gwmem?mode=memory&cache=shared", nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	u := url.URL{Scheme: "file", Path: abs}
	q := url.Values{}
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "journal_mode(WAL)")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// parseTime 解析库内时间文本(UTC RFC3339Nano)。
func parseTime(s string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, s)
}
