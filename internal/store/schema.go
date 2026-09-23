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
	// v5:供给源级上游模型名(对外统一名 → 各渠道各自真实名)
	m0005OfferUpstreamModel,
	// v6:模型级官方价绑定(厂商官方价行 ↔ 目录模型,供聚合渠道显示厂商官方价)
	m0006ModelOfficialBinding,
	// v7:中转站改造(用户级钱包 + 售价记账 + 日志归属作用域)
	m0007RelayWallet,
	// v8:用户令牌上限(普通用户自助建令牌时不得超过管理员设的天花板)
	m0008UserTokenCeiling,
	// v9:模型级售价倍率覆盖(定价从「按用户」改为「按模型」,全站同模型同价)
	m0009ModelRateOverride,
	// v10:通知/公告(管理员发布,全站可见,「我已知晓」后不再对本人显示)
	m0010Announcements,
	// v11:渠道类型 + 出站协议拆分 + 第三方额度手工配置(见 DESIGN.md §5.3)
	m0011ChannelQuota,
	// v12:渠道 × 厂商成本系数 + 成本口径审计(成本从「手填标量」改为「官方价 × 渠道系数」)
	m0012ChannelVendorCost,
	// v13:缓存写价(官方价/兜底价各一列 + 日志的缓存写 token)—— 补上被折进输入价的 cache_creation
	m0013CacheWritePrice,
}

// m0013CacheWritePrice 给计费链补上「缓存写」(Anthropic cache_creation)这一项。
//
// 为什么必须补:改造前 cache_creation 被折进 prompt 按**普通输入价**计(见 proxy/usage.go),
// 而厂商实际按**更高的缓存写价**收(Anthropic 约 1.25× 输入价)。于是 Claude 系请求的
// `in` 口径系统性**低估** —— 多轮/长上下文的真实场景恰好最依赖缓存写,偏差最大。
//
// 三处新增列,语义各不相同,勿混:
//   - official_prices.cache_write_price  官方价锚点的缓存写单价(CC 单页有该列;缺失记 0)
//   - model_offers.cache_write_price_usd 手填兜底成本的缓存写单价(与既有四价同层)
//   - request_logs.cache_write_tokens    该笔的缓存写 token 数(原被并进 prompt_tokens)
//
// 兼容与取舍(务必写清楚,否则日后对账会踩):
//   - 存量 `official_prices` 行 cache_write 一律 0。CC 抓取会重写这些行并带上真值;
//     在那之前,缓存写 token 按 input 价计(与改造前**逐位一致**,不是回归)。
//   - 存量 `model_offers` 行同理为 0(兜底路径同规则回落 input 价)。
//   - 存量 `request_logs.cache_write_tokens` 一律 0,旧行无法回溯拆分
//     (prompt_tokens 里那部分是 cache_creation 还是真输入,已无从区分)—— 不臆造。
const m0013CacheWritePrice = `
ALTER TABLE official_prices ADD COLUMN cache_write_price REAL NOT NULL DEFAULT 0;
ALTER TABLE model_offers    ADD COLUMN cache_write_price_usd REAL NOT NULL DEFAULT 0;
ALTER TABLE request_logs    ADD COLUMN cache_write_tokens  INTEGER NOT NULL DEFAULT 0;
`

// m0012ChannelVendorCost 把成本从「每供给源手填三个标量」改成「官方价 × 渠道系数」:
//
//	channel_vendor_costs            (渠道 × 厂商) 成本系数
//	request_logs.cost_source        该笔成本的口径:official | offer | unknown
//	request_logs.price_window       该笔落在哪一档:peak | offpeak(非分时为空)
//
// 为什么按厂商而非按模型:credit 型套餐(commandcode 的 $10 买 $60 额度)对所有模型
// 同倍率 —— 按模型填是 O(渠道×模型) 个格子,按厂商是 O(渠道×厂商)。
//
// ⚠️ 查找键是厂商(**models.official_vendor**),不是 channels.provider ——
// 生产里两个聚合渠道的 provider 都是 OpenAI,而它们实际消耗的是 DeepSeek/通义千问的
// 官方价。用 channels.provider 当键会让系数永远查不到,成本静默退回 1.0 倍。
//
// 缺行 = 1.0(不折扣),这是刻意的方向性选择:高估成本只会让毛利看起来偏低(你会去查),
// 低估成本会伪造利润(你不会去查)。UI 必须显式显示「未设系数,按 1.0 计」。
//
// 本迁移**不 seed 业务数据**:channel_id 因环境而异(测试库/生产库不同),且 $10/$60
// 是商业事实不是 schema 事实。commandcode → 1/6 由 UI 侧 SuggestedRatio 预填 + admin 确认。
const m0012ChannelVendorCost = `
CREATE TABLE IF NOT EXISTS channel_vendor_costs (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  channel_id INTEGER NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  vendor     TEXT    NOT NULL,            -- domain.Provider,取值同 official_prices.provider
  ratio      REAL    NOT NULL,            -- 成本 = 厂商官方价 × ratio
  note       TEXT    NOT NULL DEFAULT '',
  created_at TEXT    NOT NULL,
  updated_at TEXT    NOT NULL,
  UNIQUE (channel_id, vendor)
);

CREATE INDEX IF NOT EXISTS idx_channel_vendor_costs_channel ON channel_vendor_costs (channel_id);

-- 成本口径审计:分时之后同一个模型每天有两个成本价,没有这两列就无法回答
-- 「三个月前那笔为什么按这个价记」。历史行保持空串(= 迁移前无此信息),新行必须非空。
ALTER TABLE request_logs ADD COLUMN cost_source  TEXT NOT NULL DEFAULT '';
ALTER TABLE request_logs ADD COLUMN price_window TEXT NOT NULL DEFAULT '';
`

// m0010Announcements 增加「通知/公告」能力(见 issue #10):
//
//	announcements           公告正文 + 级别 + 启停 + 定时发布/过期
//	announcement_dismissals 每个账号对每条公告的「已读」记录(「我已知晓」后的去重依据)
//
// publish_at 空 = 立即发布;expires_at 空 = 永不过期。二者存 UTC RFC3339Nano,
// 生效判定与既有 tokens.expires_at 同口径(字典序即时间序)。
// 已读记录随公告/账号删除级联清除(store.go 已开启 foreign_keys)。
const m0010Announcements = `
CREATE TABLE IF NOT EXISTS announcements (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  title      TEXT    NOT NULL,
  body       TEXT    NOT NULL,
  level      TEXT    NOT NULL DEFAULT 'info',  -- info|warn|danger
  enabled    INTEGER NOT NULL DEFAULT 1,
  publish_at TEXT,                             -- NULL = 立即发布;否则到点才可见
  expires_at TEXT,                             -- NULL = 永不过期
  created_at TEXT    NOT NULL,
  updated_at TEXT    NOT NULL
);

CREATE TABLE IF NOT EXISTS announcement_dismissals (
  announcement_id INTEGER NOT NULL REFERENCES announcements(id) ON DELETE CASCADE,
  admin_id        INTEGER NOT NULL REFERENCES admins(id)        ON DELETE CASCADE,
  dismissed_at    TEXT    NOT NULL,
  PRIMARY KEY (announcement_id, admin_id)
);

CREATE INDEX IF NOT EXISTS idx_announcements_live ON announcements(enabled, publish_at, expires_at);
`

// m0011ChannelQuota 拆开 overloaded 的 channels.provider,并给渠道额度查询留位:
//
//	channel_type  渠道类型:deepseek | commandcode | opencode | thirdparty。
//	              **额度协议由它决定**(各上游问法完全不同),与 provider(卖的是谁的模型)正交。
//	egress_proto  出站协议:anthropic | openai | azure。原先由 provider 反推(OutProto),
//	              但「卖谁的模型」与「怎么连上去」本是两回事 —— 聚合渠道卖别家模型,却走 openai 协议。
//	quota_path    第三方渠道额度查询路径(如 /v1/dashboard/billing/subscription);空 = 未配置。
//	quota_shape   该路径的响应形状(oneapi | newapi);空 = 未配置。仅 thirdparty 用得上。
//
// 回填与收窄同批完成(幂等 UPDATE,可重复执行):
//   - egress_proto 按原 provider 推:Anthropic→anthropic,Azure→azure,其余→openai;
//   - channel_type 按 base_url/provider 认领:commandcode.ai→commandcode,opencode.ai→opencode,
//     provider='DeepSeek'→deepseek,其余→thirdparty;
//   - provider 收窄到真厂商:Azure→OpenAI(协议已由 egress_proto 承载),
//     聚合中转→空串(非单一厂商,官方价靠模型级 official_vendor 绑定)。
//
// commandcode / opencode 这两类上游本身即聚合(卖别家模型),provider 留空、由徽标回落显示渠道类型。
const m0011ChannelQuota = `
ALTER TABLE channels ADD COLUMN channel_type TEXT NOT NULL DEFAULT '';
ALTER TABLE channels ADD COLUMN egress_proto TEXT NOT NULL DEFAULT '';
ALTER TABLE channels ADD COLUMN quota_path   TEXT NOT NULL DEFAULT '';
ALTER TABLE channels ADD COLUMN quota_shape  TEXT NOT NULL DEFAULT '';

UPDATE channels SET egress_proto = CASE provider
  WHEN 'Anthropic' THEN 'anthropic'
  WHEN 'Azure'     THEN 'azure'
  ELSE 'openai'
END WHERE egress_proto = '';

UPDATE channels SET channel_type = CASE
  WHEN base_url LIKE '%commandcode.ai%' THEN 'commandcode'
  WHEN base_url LIKE '%opencode.ai%'    THEN 'opencode'
  WHEN provider = 'DeepSeek'            THEN 'deepseek'
  ELSE 'thirdparty'
END WHERE channel_type = '';

UPDATE channels SET provider = 'OpenAI' WHERE provider = 'Azure';
UPDATE channels SET provider = ''       WHERE provider = '聚合中转';
`

// m0009ModelRateOverride 把售价倍率从「按用户」下沉到「按模型」(见 PLAN.md §2):
//
//	models.rate_override  该模型的售价倍率;NULL = 回落全局 settings.price_multiplier
//
// 语义:本站价 = 官方价 × 倍率,而倍率只由模型决定 —— 同一模型对所有客户同一价。
// 纯附加、默认 NULL,存量库行为不变(全部回落全局倍率)。
//
// 注:迁移 v7 的 admins.rate_override(用户级倍率)已废弃不再读写,列保留不删
// (SQLite 删列代价大且无收益);新库不再写入该列。
const m0009ModelRateOverride = `
ALTER TABLE models ADD COLUMN rate_override REAL;
`

// m0008UserTokenCeiling 给「用户自助建令牌」加天窗,避免客户绕过额度约束:
// 令牌额度只是子预算,但用户自己可以把它设成 0(不限)或极大值,分闸形同虚设。
//
//	admins.token_quota_ceiling  该用户名下令牌的额度上限(0 = 不限;仅约束 role=user)
//	admins.token_rpm_ceiling    该用户名下令牌的 RPM 上限(0 = 不限)
//
// 管理员不受限(管理员建令牌走 admin 分支,不校验此值)。纯附加、默认 0,存量库行为不变。
const m0008UserTokenCeiling = `
ALTER TABLE admins ADD COLUMN token_quota_ceiling REAL    NOT NULL DEFAULT 0;
ALTER TABLE admins ADD COLUMN token_rpm_ceiling   INTEGER NOT NULL DEFAULT 0;
`

// m0007RelayWallet 把网关从「个人自用」推向「中转站」的存储基础(见 PLAN.md §3):
//
//	admins.balance_usd    用户钱包余额(仅 role=user 扣减;admin 即站主自己,不扣)
//	admins.rate_override  【已废弃,见 v9】用户级售价倍率;倍率现按模型存(models.rate_override)
//	balance_logs          账变流水(钱包不能只有当前值,充值/扣费都要可审计)
//	request_logs.charge_usd  该笔「售价」(客户付你);与 cost(你付上游)分离,差额即毛利
//	request_logs.owner_id    归属冗余,免 JOIN 即可按 owner 作用域查询;存量行由 tokens 回填
//
// 金额口径同既有 offers.*_price_usd / logs.cost:字段名带 _usd 是历史命名,装的其实是
// settings.displayCurrency 币种金额(本站为人民币)。
const m0007RelayWallet = `
ALTER TABLE admins ADD COLUMN balance_usd   REAL NOT NULL DEFAULT 0;
ALTER TABLE admins ADD COLUMN rate_override REAL;

CREATE TABLE IF NOT EXISTS balance_logs (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  admin_id      INTEGER NOT NULL REFERENCES admins(id) ON DELETE CASCADE,
  delta         REAL    NOT NULL,              -- 正=充值,负=扣费
  balance_after REAL    NOT NULL,
  reason        TEXT    NOT NULL,              -- charge | topup | adjust
  log_id        INTEGER NOT NULL DEFAULT 0,    -- 关联 request_logs.id(charge 时)
  note          TEXT    NOT NULL DEFAULT '',
  created_at    TEXT    NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_balance_logs_admin ON balance_logs (admin_id, id);

ALTER TABLE request_logs ADD COLUMN charge_usd REAL    NOT NULL DEFAULT 0;
ALTER TABLE request_logs ADD COLUMN owner_id   INTEGER NOT NULL DEFAULT 0;
UPDATE request_logs SET owner_id = COALESCE(
	(SELECT t.owner_id FROM tokens t WHERE t.id = request_logs.token_id), 0);
CREATE INDEX IF NOT EXISTS idx_logs_owner ON request_logs (owner_id, ts);
`

// m0006ModelOfficialBinding 模型级「官方参考价来源」绑定:
// 聚合中转渠道的 provider 不是厂商(多为 OpenAI),模型名(如 deepseek/deepseek-v4.1-flash)
// 也与厂商官网名(如 deepseek-flash)对不上,故需要一个显式的模型 ↔ 官方价行映射。
//
//	official_vendor     官方价所属厂商(domain.Provider 原值,空 = 未绑定)
//	official_model_name 该厂商 official_prices.model_name(空 = 未绑定)
//
// 二者皆空 = 走「provider 直连 / 厂商名推断」自动匹配;非空 = 显式覆盖,优先级最高。
// 纯附加、两列默认空串,存量库无需回填,行为不变。
const m0006ModelOfficialBinding = `
ALTER TABLE models ADD COLUMN official_vendor     TEXT NOT NULL DEFAULT '';
ALTER TABLE models ADD COLUMN official_model_name TEXT NOT NULL DEFAULT '';
`

// m0005OfferUpstreamModel 把「渠道侧真实模型名」从 model 级下沉到 offer(供给源)级:
// 同一对外统一名可为不同渠道配各自的上游真实名,出站按实际命中的候选渠道改写请求体 model。
// 空串 = 未配置,回落 model 级 name(存量数据与旧行为完全一致)。
const m0005OfferUpstreamModel = `
ALTER TABLE model_offers ADD COLUMN upstream_model TEXT NOT NULL DEFAULT '';
`

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
