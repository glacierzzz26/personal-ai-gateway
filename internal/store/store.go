// Package store 提供 SQLite 持久化(modernc.org/sqlite,纯 Go 无 cgo)。
// P1 用途:request_log 请求日志,为后续用量统计与 Web 管理端提供可查询数据源。
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

type LogEntry struct {
	ID               int64
	TS               time.Time
	ClientKey        string
	ClientTool       string
	Protocol         string // anthropic | openai(入站)
	Model            string
	Upstream         string
	Stream           bool
	Status           int
	PromptTokens     int
	CompletionTokens int
	CacheReadTokens  int
	Cost             float64
	LatencyMs        int64
	Err              string
}

const schema = `
CREATE TABLE IF NOT EXISTS request_log (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  ts            TEXT    NOT NULL,
  client_key    TEXT    NOT NULL DEFAULT '',
  client_tool   TEXT    NOT NULL DEFAULT '',
  protocol      TEXT    NOT NULL,
  model         TEXT    NOT NULL,
  upstream      TEXT    NOT NULL DEFAULT '',
  stream        INTEGER NOT NULL DEFAULT 0,
  status        INTEGER NOT NULL,
  prompt_tokens INTEGER NOT NULL DEFAULT 0,
  completion_tokens INTEGER NOT NULL DEFAULT 0,
  cache_read_tokens  INTEGER NOT NULL DEFAULT 0,
  cost          REAL    NOT NULL DEFAULT 0,
  latency_ms    INTEGER NOT NULL DEFAULT 0,
  err           TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_reqlog_ts    ON request_log(ts);
CREATE INDEX IF NOT EXISTS idx_reqlog_model ON request_log(model);

CREATE TABLE IF NOT EXISTS upstreams (
  name TEXT PRIMARY KEY,
  doc  TEXT NOT NULL,      -- 整条上游配置,存 YAML raw 形式(${ENV} 引用原样保留)
  ord  INTEGER NOT NULL    -- 列表顺序,与 ord 序一致地回读
);
`

// Open 打开(必要时创建)SQLite 库并建表。父目录不存在会自动创建。
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("store: create dir: %w", err)
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	if _, err := db.Exec("PRAGMA journal_mode=WAL;"); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: wal: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: migrate: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Log 写入一条请求日志。写失败只返回错误,不 panic;个人网关里日志丢一两行可接受。
func (s *Store) Log(e LogEntry) error {
	if s == nil || s.db == nil {
		return nil
	}
	const q = `INSERT INTO request_log
	(ts, client_key, client_tool, protocol, model, upstream, stream, status,
	 prompt_tokens, completion_tokens, cache_read_tokens, cost, latency_ms, err)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := s.db.Exec(q,
		e.TS.UTC().Format(time.RFC3339Nano),
		e.ClientKey, e.ClientTool, e.Protocol, e.Model, e.Upstream, boolInt(e.Stream), e.Status,
		e.PromptTokens, e.CompletionTokens, e.CacheReadTokens, e.Cost, e.LatencyMs, e.Err,
	)
	if err != nil {
		return fmt.Errorf("store: log: %w", err)
	}
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Recent 返回最近 n 条记录(新→旧),供调试/后续 /api 使用。
func (s *Store) Recent(n int) ([]LogEntry, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("store: not open")
	}
	if n <= 0 {
		n = 50
	}
	rows, err := s.db.Query(`SELECT `+reqCols+` FROM request_log ORDER BY id DESC LIMIT ?`, n)
	if err != nil {
		return nil, fmt.Errorf("store: recent: %w", err)
	}
	defer rows.Close()
	return scanLogs(rows)
}
