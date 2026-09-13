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
	// v2:多用户(账号角色 + 令牌归属 + 可回放密文)
	m0002MultiUser,
	// v3:模型统一名称(网关侧对外名,与渠道侧真实模型名解耦)
	m0003ModelDisplayName,
	// v4:厂商官方定价(来源留证 + 官方参考价与手工报价分离)
	m0004OfficialPricing,
}

const m0004OfficialPricing = `
-- 报价来源留证:应用官方价时写入。四字段空串 = 从未从官方来源应用过。
--   price_source_url  来源官方页面 URL(可点击核对)
--   price_fetched_at  抓取时间(UTC RFC3339),不是「改动时间」
--   price_currency    原币种(CNY|USD);offer 计价恒为 USD
--   price_native_text 原始单价文本(如 "高峰 8元 / 空闲 4元"),便于人工核对
ALTER TABLE model_offers ADD COLUMN price_source_url  TEXT NOT NULL DEFAULT '';
ALTER TABLE model_offers ADD COLUMN price_fetched_at  TEXT NOT NULL DEFAULT '';
ALTER TABLE model_offers ADD COLUMN price_currency    TEXT NOT NULL DEFAULT '';
ALTER TABLE model_offers ADD COLUMN price_native_text TEXT NOT NULL DEFAULT '';

-- 官方参考价:与手工报价(model_offers)分离存放。手工价优先,官方价作默认值与比对源。
-- 价格一律以「原币种 / 百万 token」存储;分时类取空闲价作生效默认(见 detail_json)。
CREATE TABLE IF NOT EXISTS official_prices (
  id               INTEGER PRIMARY KEY AUTOINCREMENT,
  provider         TEXT    NOT NULL,              -- domain.Provider 原值
  model_name       TEXT    NOT NULL,              -- 渠道侧真实模型名
  source_url       TEXT    NOT NULL,              -- 官方域名页面
  fetched_at       TEXT    NOT NULL,              -- 抓取时间 UTC RFC3339
  currency         TEXT    NOT NULL,              -- CNY | USD
  billing_shape    TEXT    NOT NULL DEFAULT 'flat', -- flat|peak_offpeak|tiered|discount
  in_price         REAL    NOT NULL,              -- 原币种/百万 token
  out_price        REAL    NOT NULL,
  cache_read_price REAL    NOT NULL DEFAULT 0,
  cache_derived    INTEGER NOT NULL DEFAULT 0,    -- 缓存价是否由官方规则推导(非官方列)
  native_text      TEXT    NOT NULL DEFAULT '',   -- 原始单价文本
  detail_json      TEXT    NOT NULL DEFAULT '{}', -- 分时/阶梯/折扣明细
  content_sha256   TEXT    NOT NULL DEFAULT '',   -- 官方页面内容指纹
  created_at       TEXT    NOT NULL,
  updated_at       TEXT    NOT NULL,
  UNIQUE (provider, model_name)
);
CREATE INDEX IF NOT EXISTS idx_official_prices_provider ON official_prices (provider);
`

const m0003ModelDisplayName = `
-- 统一名称:空串表示未重命名(对外回落为真实模型名 name)。
ALTER TABLE models ADD COLUMN display_name TEXT NOT NULL DEFAULT '';

-- 已重命名的统一名唯一;未重命名的空串不参与约束(部分索引)。
CREATE UNIQUE INDEX IF NOT EXISTS idx_models_display_name
	ON models (display_name) WHERE display_name <> '';
`

const m0002MultiUser = `
-- 账号角色。常量默认值直接回填既有管理员行为 admin,无需额外 UPDATE。
ALTER TABLE admins ADD COLUMN role TEXT NOT NULL DEFAULT 'admin';

-- 令牌归属(NULL=历史/全局 key)与可回放密文(供显示 key / 生成 Claude 配置)。
ALTER TABLE tokens ADD COLUMN owner_id  INTEGER REFERENCES admins(id) ON DELETE CASCADE;
ALTER TABLE tokens ADD COLUMN key_cipher TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_tokens_owner ON tokens (owner_id);

-- 会话改为无状态 JWT,cookie 里不再落库,sessions 表退役。
DROP TABLE IF EXISTS sessions;
`

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
