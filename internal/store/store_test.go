package store

import (
	"errors"
	"testing"
	"time"

	"personal-ai-gateway/internal/domain"
	"personal-ai-gateway/internal/pgtest"
	"personal-ai-gateway/internal/secret"
)

// newTestStore 在独立 schema 中打开一个已迁移的库;引导全局主密钥(密文/解密同进程一致)。
func newTestStore(t *testing.T) *Store {
	t.Helper()
	if _, err := secret.BootstrapKey(t.TempDir()); err != nil {
		t.Fatalf("bootstrap master key: %v", err)
	}
	st, err := Open(pgtest.SchemaDSN(t))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func mustEqual(t *testing.T, got, want any, msg string) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %v, want %v", msg, got, want)
	}
}

func mustNoErr(t *testing.T, err error, msg string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", msg, err)
	}
}

// mustErrIs 断言 err 与 target 匹配(errors.Is 语义)。
func mustErrIs(t *testing.T, err, target error, msg string) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Errorf("%s: err = %v, want %v", msg, err, target)
	}
}

// TestOpenMigratesAndIdempotent 基线迁移:schema 版本 = schemaVersion(14)、
// 业务表齐备、sessions 退役(历史步骤的终态体现在基线上),重复打开幂等。
func TestOpenMigratesAndIdempotent(t *testing.T) {
	dsn := pgtest.SchemaDSN(t)
	st, err := Open(dsn)
	mustNoErr(t, err, "first open")
	sv, err := st.SchemaVersion()
	mustNoErr(t, err, "SchemaVersion")
	mustEqual(t, sv, schemaVersion, "SchemaVersion == schemaVersion")
	st.Close()

	st2, err := Open(dsn) // 重复打开:迁移幂等
	mustNoErr(t, err, "second open")
	defer st2.Close()
	sv2, err := st2.SchemaVersion()
	mustNoErr(t, err, "SchemaVersion(reopen)")
	mustEqual(t, sv2, schemaVersion, "SchemaVersion stable on reopen")

	// 业务表应就绪(抽查几张三件套)。information_schema 限定当前 schema。
	for _, table := range []string{"channels", "models", "model_offers", "rules", "tokens", "request_logs", "settings", "admins", "official_prices", "announcements", "announcement_dismissals", "balance_logs", "channel_vendor_costs"} {
		var n int
		mustNoErr(t, st2.db.QueryRow(`SELECT COUNT(*) FROM information_schema.tables
			WHERE table_schema = current_schema() AND table_name = ?`, table).Scan(&n), "information_schema")
		mustEqual(t, n, 1, "table "+table+" exists")
	}
	// sessions 已退役(JWT 无状态)。
	var gone int
	mustNoErr(t, st2.db.QueryRow(`SELECT COUNT(*) FROM information_schema.tables
		WHERE table_schema = current_schema() AND table_name = 'sessions'`).Scan(&gone), "check sessions gone")
	mustEqual(t, gone, 0, "sessions table absent")
}

// TestBaselineColumns 基线含历史迁移的终态列(m0011 的 egress_proto/channel_type、
// m0014 的 official_prices.source)。取代原先逐条回放 SQLite 迁移的用例 ——
// PG 基线一次建出终态,这里只断言这些关键列存在。
func TestBaselineColumns(t *testing.T) {
	st := newTestStore(t)
	cases := map[string][]string{
		"channels":        {"egress_proto", "channel_type", "quota_path", "quota_shape"},
		"official_prices": {"source", "cache_write_price", "content_sha256"},
		"models":          {"display_name", "official_vendor", "rate_override"},
		"tokens":          {"owner_id", "key_cipher"},
	}
	for table, cols := range cases {
		for _, col := range cols {
			var n int
			mustNoErr(t, st.db.QueryRow(`SELECT COUNT(*) FROM information_schema.columns
				WHERE table_schema = current_schema() AND table_name = ? AND column_name = ?`,
				table, col).Scan(&n), "information_schema.columns")
			mustEqual(t, n, 1, "column "+table+"."+col+" exists")
		}
	}
}

// TestOfficialPriceSourceDualSource 基线唯一键带 source 维度:同 (厂商, 模型)
// 允许 CC 与 opencode 双来源并存,且单行读回四价无损。
func TestOfficialPriceSourceDualSource(t *testing.T) {
	st := newTestStore(t)

	cc, err := st.UpsertOfficialPrice(domain.OfficialPriceRow{
		Provider: domain.ProviderAnthropic, ModelName: "claude-sonnet-5",
		Source: domain.PriceSourceCommandCode, SourceURL: "https://commandcode.ai/models",
		FetchedAt: time.Now().UTC(), Currency: domain.CurrencyUSD, BillingShape: domain.ShapeFlat,
		InputPrice: 2, OutputPrice: 10, CacheReadPrice: 0.2, CacheWritePrice: 2.5,
	})
	mustNoErr(t, err, "upsert commandcode row")
	oc, err := st.UpsertOfficialPrice(domain.OfficialPriceRow{
		Provider: domain.ProviderAnthropic, ModelName: "claude-sonnet-5",
		Source: domain.PriceSourceOpenCode, SourceURL: "https://opencode.ai/docs/zen/",
		FetchedAt: time.Now().UTC(), Currency: domain.CurrencyUSD, BillingShape: domain.ShapeFlat,
		InputPrice: 3, OutputPrice: 15,
	})
	mustNoErr(t, err, "upsert opencode row")
	if oc.Source != domain.PriceSourceOpenCode {
		t.Errorf("source = %q, want opencode", oc.Source)
	}

	got, err := st.GetOfficialPriceBySource(domain.ProviderAnthropic, "claude-sonnet-5", domain.PriceSourceCommandCode)
	mustNoErr(t, err, "read commandcode row")
	if got.InputPrice != 2 || got.OutputPrice != 10 || got.CacheReadPrice != 0.2 || got.CacheWritePrice != 2.5 {
		t.Errorf("四价读回失真: %+v", got)
	}
	_ = cc

	rows, err := st.ListOfficialPrices(domain.ProviderAnthropic)
	mustNoErr(t, err, "list anthropic")
	if len(rows) != 2 {
		t.Fatalf("双来源应存 2 行(CC + opencode), got %d: %+v", len(rows), rows)
	}
}
