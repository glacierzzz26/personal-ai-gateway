package store

import (
	"database/sql"
	"fmt"
)

// schema 版本化迁移:每个元素 = 一步;执行过的版本记入 schema_migrations。
// 只允许追加新步骤,禁止改动已发布的步骤 —— 否则已有库会漂移。
var migrations = []string{
	// v1:全量业务表(新库首装,无历史包袱)
	m0001Init,
}

const m0001Init = `
CREATE TABLE IF NOT EXISTS channels (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  name           TEXT    NOT NULL UNIQUE,
  provider       TEXT    NOT NULL,
  base_url       TEXT    NOT NULL,
  api_key_cipher TEXT    NOT NULL DEFAULT '',
  key_masked     TEXT    NOT NULL DEFAULT '',
  priority       INTEGER NOT NULL DEFAULT 0,
  weight         INTEGER NOT NULL DEFAULT 0,
  timeout_ms     INTEGER NOT NULL DEFAULT 60000,
  tags           TEXT    NOT NULL DEFAULT '[]',
  enabled        INTEGER NOT NULL DEFAULT 1,
  max_failures   INTEGER NOT NULL DEFAULT 3,
  cooldown_sec   INTEGER NOT NULL DEFAULT 60,
  note           TEXT    NOT NULL DEFAULT '',
  created_at     TEXT    NOT NULL,
  updated_at     TEXT    NOT NULL
);

CREATE TABLE IF NOT EXISTS models (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  name           TEXT    NOT NULL UNIQUE,
  context_window INTEGER NOT NULL DEFAULT 0,
  capabilities   TEXT    NOT NULL DEFAULT '[]',
  enabled        INTEGER NOT NULL DEFAULT 1,
  created_at     TEXT    NOT NULL,
  updated_at     TEXT    NOT NULL
);

CREATE TABLE IF NOT EXISTS model_offers (
  id                   INTEGER PRIMARY KEY AUTOINCREMENT,
  model_id             INTEGER NOT NULL REFERENCES models(id)   ON DELETE CASCADE,
  channel_id           INTEGER NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  input_price_usd      REAL    NOT NULL DEFAULT 0,
  output_price_usd     REAL    NOT NULL DEFAULT 0,
  cache_read_price_usd REAL    NOT NULL DEFAULT 0,
  override_price       INTEGER NOT NULL DEFAULT 0,
  priority             INTEGER NOT NULL DEFAULT 0,
  enabled              INTEGER NOT NULL DEFAULT 1,
  rate_limit_rpm       INTEGER NOT NULL DEFAULT 60,
  timeout_ms           INTEGER,
  note                 TEXT    NOT NULL DEFAULT '',
  UNIQUE (model_id, channel_id)
);

CREATE TABLE IF NOT EXISTS rules (
  id                  INTEGER PRIMARY KEY AUTOINCREMENT,
  name                TEXT    NOT NULL,
  enabled             INTEGER NOT NULL DEFAULT 1,
  match_mode          TEXT    NOT NULL,
  pattern             TEXT    NOT NULL,
  strategy            TEXT    NOT NULL,
  channel_ids         TEXT    NOT NULL DEFAULT '[]',
  weights             TEXT,
  fallback_channel_id INTEGER,
  retry               INTEGER NOT NULL DEFAULT 1,
  timeout_ms          INTEGER NOT NULL DEFAULT 120000,
  sort                INTEGER NOT NULL DEFAULT 0,
  hit                 INTEGER NOT NULL DEFAULT 0,
  created_at          TEXT    NOT NULL,
  updated_at          TEXT    NOT NULL
);

CREATE TABLE IF NOT EXISTS tokens (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  name           TEXT    NOT NULL,
  sha256         TEXT    NOT NULL UNIQUE,
  key_masked     TEXT    NOT NULL,
  allowed_models TEXT    NOT NULL DEFAULT '[]',
  quota_usd      REAL    NOT NULL DEFAULT 0,
  used_usd       REAL    NOT NULL DEFAULT 0,
  rpm_limit      INTEGER NOT NULL DEFAULT 60,
  expires_at     TEXT,
  status         TEXT    NOT NULL DEFAULT 'active',
  last_used_at   TEXT,
  created_at     TEXT    NOT NULL,
  updated_at     TEXT    NOT NULL
);

CREATE TABLE IF NOT EXISTS request_logs (
  id                 INTEGER PRIMARY KEY AUTOINCREMENT,
  ts                 TEXT    NOT NULL,
  model              TEXT    NOT NULL DEFAULT '',
  channel_id         INTEGER NOT NULL DEFAULT 0,
  channel_name       TEXT    NOT NULL DEFAULT '',
  token_id           INTEGER NOT NULL DEFAULT 0,
  token_name         TEXT    NOT NULL DEFAULT '',
  client_tool        TEXT    NOT NULL DEFAULT '',
  protocol           TEXT    NOT NULL DEFAULT '',
  stream             INTEGER NOT NULL DEFAULT 0,
  status             INTEGER NOT NULL DEFAULT 0,
  prompt_tokens      INTEGER NOT NULL DEFAULT 0,
  completion_tokens  INTEGER NOT NULL DEFAULT 0,
  cache_read_tokens  INTEGER NOT NULL DEFAULT 0,
  cost               REAL    NOT NULL DEFAULT 0,
  first_token_ms     INTEGER NOT NULL DEFAULT 0,
  total_ms           INTEGER NOT NULL DEFAULT 0,
  ip                 TEXT    NOT NULL DEFAULT '',
  err                TEXT
);
CREATE INDEX IF NOT EXISTS idx_logs_ts         ON request_logs (ts);
CREATE INDEX IF NOT EXISTS idx_logs_model      ON request_logs (model);
CREATE INDEX IF NOT EXISTS idx_logs_channel    ON request_logs (channel_name);
CREATE INDEX IF NOT EXISTS idx_logs_token      ON request_logs (token_name);
CREATE INDEX IF NOT EXISTS idx_logs_channel_id ON request_logs (channel_id);
CREATE INDEX IF NOT EXISTS idx_logs_token_id   ON request_logs (token_id);

CREATE TABLE IF NOT EXISTS settings (
  k TEXT PRIMARY KEY,
  v TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS admins (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  username        TEXT NOT NULL UNIQUE,
  password_bcrypt TEXT NOT NULL,
  created_at      TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  token      TEXT NOT NULL UNIQUE,
  admin_id   INTEGER NOT NULL REFERENCES admins(id) ON DELETE CASCADE,
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL
);
`

// migrate 按版本顺序执行未应用的步骤,每步一个事务。
func migrate(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return err
	}
	for i, step := range migrations {
		version := i + 1
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, version).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			continue
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(step); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply migration %d: %w", version, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
			version, nowRFC3339()); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
