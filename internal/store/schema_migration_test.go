package store

import (
	"testing"

	"personal-ai-gateway/internal/domain"
	"personal-ai-gateway/internal/pgtest"
)

// TestMigrateFromV14AddsModelRatioTable 已存在的 v14 库打开后应**走增量步骤**升到当前版本:
// 建出 channel_model_cost_ratios 表(v15)、跑官方价来源收敛(v16),且旧数据不丢、不重跑基线
// (重跑会 CREATE TABLE 冲突)。
//
// 复现方式:先 Open 一次(拿到 v15 全表),再手工把它打回「v14 的样子」——
//   - DROP 掉 v15 才有的表(连带其索引);
//   - schema_migrations 版本改回 14;
//
// 然后重开。若 migrate 还按「版本行缺失就重跑基线」的老逻辑,会在 CREATE TABLE admins 处爆炸;
// 新逻辑应只跑 schemaSteps[15]。
func TestMigrateFromV14AddsModelRatioTable(t *testing.T) {
	dsn := pgtest.SchemaDSN(t)

	st, err := Open(dsn)
	mustNoErr(t, err, "first open")
	// 播一条业务数据(旧库本就该有),升级后必须原样保留。
	chID := mkChannel(t, st, "legacy-cc")

	// 打回 v14:删掉 v15 才有的表 + 版本号回退。
	if _, err := st.db.Exec(`DROP TABLE channel_model_cost_ratios`); err != nil {
		t.Fatalf("drop new table: %v", err)
	}
	if _, err := st.db.Exec(`DELETE FROM schema_migrations`); err != nil {
		t.Fatalf("clear version: %v", err)
	}
	if _, err := st.db.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (14, ?)`, nowRFC3339()); err != nil {
		t.Fatalf("set v14: %v", err)
	}
	mustNoErr(t, st.Close(), "close")

	// 重开 → 只跑 v15 增量步骤。
	st2, err := Open(dsn)
	mustNoErr(t, err, "reopen")
	defer st2.Close()

	sv, err := st2.SchemaVersion()
	mustNoErr(t, err, "schema version")
	mustEqual(t, sv, schemaVersion, "升级后版本 = schemaVersion")

	// 新表存在(未随 DROP 缺失)。
	var n int
	mustNoErr(t, st2.db.QueryRow(`SELECT COUNT(*) FROM information_schema.tables
		WHERE table_schema = current_schema() AND table_name = 'channel_model_cost_ratios'`).Scan(&n), "table exists")
	mustEqual(t, n, 1, "channel_model_cost_ratios 表由增量步骤建出")

	// 旧数据不丢:渠道还在,且新表可写(UNIQUE(channel_id, model_name) 生效)。
	got, err := st2.GetChannel(chID)
	mustNoErr(t, err, "legacy channel intact")
	mustEqual(t, got.Name, "legacy-cc", "渠道名原样")
	mustNoErr(t, st2.SetChannelModelCostRatio(chID, "y", 0.25, "manual", ""), "new table writable")

	// 幂等:再开一次不再动任何东西。
	mustNoErr(t, st2.Close(), "close2")
	st3, err := Open(dsn)
	mustNoErr(t, err, "third open")
	defer st3.Close()
	sv3, _ := st3.SchemaVersion()
	mustEqual(t, sv3, schemaVersion, "重复打开版本稳定")
}

// TestMigrateFromV15RebindsZenAndDropsZenRows v15 → v16 的官方价来源收敛:
// 绑定在 opencode(zen)点号 slug 上的模型**改绑**到 CC 连字符孪生(仅当 CC 确有该行),随后删光 zen 行。
// 三条绑定覆盖三种情形:①点号且 CC 有连字符孪生 → 改绑;②点号但 CC 本身用点号(孪生不存在)→ 不动;
// ③点号但 CC 无任何孪生 → 不动。
func TestMigrateFromV15RebindsZenAndDropsZenRows(t *testing.T) {
	dsn := pgtest.SchemaDSN(t)

	st, err := Open(dsn)
	mustNoErr(t, err, "first open")

	putPrice := func(p domain.Provider, model string, src domain.PriceSource) {
		t.Helper()
		q := mkOfficialPrice(p, model, "https://example.test/p", 1, 2)
		q.Source, q.Currency, q.BillingShape = src, domain.CurrencyUSD, domain.ShapeFlat
		_, uerr := st.UpsertOfficialPrice(q)
		mustNoErr(t, uerr, "upsert price "+model+"/"+string(src))
	}
	// zen 来源字面量(opencode 常量已随来源收敛删除;库里历史行仍是 'opencode')。
	const zenSrc = domain.PriceSource("opencode")
	// CC 行(连字符)+ zen 行(点号),同一模型 OpenAI/gpt-5.6-sol。
	putPrice(domain.ProviderOpenAI, "gpt-5-6-sol", domain.PriceSourceCommandCode)
	putPrice(domain.ProviderOpenAI, "gpt-5.6-sol", zenSrc)
	// CC 本身用点号的 qwen:连字符孪生(qwen3-6-plus)在 CC 不存在 → 不该被改绑。
	putPrice(domain.ProviderQwen, "qwen3.6-plus", domain.PriceSourceCommandCode)

	insModel := func(name, vendor, off string) {
		t.Helper()
		now := nowRFC3339()
		_, e := st.db.Exec(`INSERT INTO models (name, official_vendor, official_model_name, created_at, updated_at)
			VALUES (?,?,?,?,?)`, name, vendor, off, now, now)
		mustNoErr(t, e, "insert model "+name)
	}
	insModel("gpt-5.6-sol", string(domain.ProviderOpenAI), "gpt-5.6-sol")      // ①改绑
	insModel("Qwen/Qwen3.6-Plus", string(domain.ProviderQwen), "qwen3.6-plus") // ②不动
	insModel("orphan-1", string(domain.ProviderOpenAI), "gpt-9.9-x")           // ③不动

	// 打回 v15:让 step 16 重跑。
	if _, err := st.db.Exec(`DELETE FROM schema_migrations`); err != nil {
		t.Fatalf("clear version: %v", err)
	}
	if _, err := st.db.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (15, ?)`, nowRFC3339()); err != nil {
		t.Fatalf("set v15: %v", err)
	}
	mustNoErr(t, st.Close(), "close")

	st2, err := Open(dsn)
	mustNoErr(t, err, "reopen")
	defer st2.Close()
	sv, _ := st2.SchemaVersion()
	mustEqual(t, sv, schemaVersion, "升级后 = schemaVersion")

	modelOff := func(name string) string {
		var off string
		mustNoErr(t, st2.db.QueryRow(`SELECT official_model_name FROM models WHERE name=?`, name).Scan(&off), "read binding "+name)
		return off
	}
	mustEqual(t, modelOff("gpt-5.6-sol"), "gpt-5-6-sol", "①点号+CC 连字符孪生 → 改绑")
	mustEqual(t, modelOff("Qwen/Qwen3.6-Plus"), "qwen3.6-plus", "②CC 自身用点号 → 不动")
	mustEqual(t, modelOff("orphan-1"), "gpt-9.9-x", "③无孪生 → 不动")

	// zen 行全删,CC 行保留。
	var zen, cc int
	mustNoErr(t, st2.db.QueryRow(`SELECT COUNT(*) FROM official_prices WHERE source='opencode'`).Scan(&zen), "count zen")
	mustNoErr(t, st2.db.QueryRow(`SELECT COUNT(*) FROM official_prices WHERE source='commandcode'`).Scan(&cc), "count cc")
	mustEqual(t, zen, 0, "opencode 行被清")
	mustEqual(t, cc, 2, "commandcode 行保留")
}

// TestMigrateFromV16NormalizesModelRatioSlugs v16 → v17 的模型级系数键归一(点号 → 连字符):
// 遗留的点号 model_name 与官方价绑定口径对齐;已成连字符孪生的点号行被删(保留权威连字符行);
// 无点号的行不动。这是「opencode 渠道的派生系数因点号键点查不到模型、成本静默按 1.0 计」的数据修复。
func TestMigrateFromV16NormalizesModelRatioSlugs(t *testing.T) {
	dsn := pgtest.SchemaDSN(t)

	st, err := Open(dsn)
	mustNoErr(t, err, "first open")
	chID := mkChannel(t, st, "oc")

	// ① 遗留点号行,无连字符孪生 → 应被改名为连字符。
	mustNoErr(t, st.SetChannelModelCostRatio(chID, "deepseek-v4.1-flash", 0.125, "plan", ""), "seed dot")
	// ② 点号行 + 已存在的连字符孪生 → 点号行应被删,连字符行保留。
	mustNoErr(t, st.SetChannelModelCostRatio(chID, "glm-5.3", 0.2, "plan", ""), "seed dot twin")
	mustNoErr(t, st.SetChannelModelCostRatio(chID, "glm-5-3", 0.3, "plan", ""), "seed hyphen twin")
	// ③ 无点号行 → 不动。
	mustNoErr(t, st.SetChannelModelCostRatio(chID, "kimi-k3", 0.5, "plan", ""), "seed plain")

	// 打回 v16:让 step 17 重跑。
	if _, err := st.db.Exec(`DELETE FROM schema_migrations`); err != nil {
		t.Fatalf("clear version: %v", err)
	}
	if _, err := st.db.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (16, ?)`, nowRFC3339()); err != nil {
		t.Fatalf("set v16: %v", err)
	}
	mustNoErr(t, st.Close(), "close")

	st2, err := Open(dsn)
	mustNoErr(t, err, "reopen")
	defer st2.Close()
	sv, _ := st2.SchemaVersion()
	mustEqual(t, sv, schemaVersion, "升级后 = schemaVersion")

	// ① 点号 → 连字符(值保留)。
	if got, e := st2.ChannelModelRatio(chID, "deepseek-v4-1-flash"); e != nil || got != 0.125 {
		t.Errorf("① 点号行应改名为连字符且值不变: got %v, err %v", got, e)
	}
	if _, e := st2.ChannelModelRatio(chID, "deepseek-v4.1-flash"); e == nil {
		t.Error("① 旧点号键应已不存在")
	}
	// ② 点号孪生被删,连字符行保留。
	if _, e := st2.ChannelModelRatio(chID, "glm-5.3"); e == nil {
		t.Error("② 点号孪生应被删")
	}
	if got, e := st2.ChannelModelRatio(chID, "glm-5-3"); e != nil || got != 0.3 {
		t.Errorf("② 连字符行应保留: got %v, err %v", got, e)
	}
	// ③ 无点号行不动。
	if got, e := st2.ChannelModelRatio(chID, "kimi-k3"); e != nil || got != 0.5 {
		t.Errorf("③ 无点号行不该被改: got %v, err %v", got, e)
	}
	// 幂等:再开一次不再动。
	mustNoErr(t, st2.Close(), "close2")
	st3, err := Open(dsn)
	mustNoErr(t, err, "third open")
	defer st3.Close()
	if _, e := st3.ChannelModelRatio(chID, "deepseek-v4-1-flash"); e != nil {
		t.Errorf("重开后连字符键应仍在: err %v", e)
	}
}

// TestMigrateFromV17RebindsFoldedOfficialBindings v17 → v18 的官方价绑定归位:
// 模型名末段带点号者折点号后唯一命中一条 commandcode 官方价行 → 改绑(含补绑双空行);
// 已绑到陈旧 slug 的同样改绑;已正确的 / 无 CC 孪生的 / 同 slug 多厂商歧义的不动。
// 这是「点号名模型永远绑不上连字符官方价 → 成本系数/官方价点查全落空」的数据修复。
func TestMigrateFromV17RebindsFoldedOfficialBindings(t *testing.T) {
	dsn := pgtest.SchemaDSN(t)

	st, err := Open(dsn)
	mustNoErr(t, err, "first open")

	putCC := func(p domain.Provider, model string) {
		t.Helper()
		q := mkOfficialPrice(p, model, "https://example.test/c", 1, 2)
		q.Source, q.Currency, q.BillingShape = domain.PriceSourceCommandCode, domain.CurrencyUSD, domain.ShapeFlat
		_, e := st.UpsertOfficialPrice(q)
		mustNoErr(t, e, "upsert cc "+model)
	}
	putCC(domain.ProviderDeepSeek, "deepseek-v4-flash")
	putCC(domain.ProviderDeepSeek, "deepseek-v4-1-flash")
	putCC(domain.ProviderZhipu, "glm-5-3-flash")
	putCC(domain.ProviderQwen, "flash-x")  // 同 slug 两厂商 → 歧义
	putCC(domain.ProviderZhipu, "flash-x") //

	insModel := func(name, vendor, off string) {
		t.Helper()
		now := nowRFC3339()
		_, e := st.db.Exec(`INSERT INTO models (name, official_vendor, official_model_name, created_at, updated_at)
			VALUES (?,?,?,?,?)`, name, vendor, off, now, now)
		mustNoErr(t, e, "insert model "+name)
	}
	insModel("deepseek/deepseek-v4.1-flash", string(domain.ProviderDeepSeek), "deepseek-flash") // 陈旧 → 改绑
	insModel("deepseek/deepseek-v4-flash", string(domain.ProviderDeepSeek), "deepseek-flash")   // 陈旧 → 改绑
	insModel("deepseek-flash", string(domain.ProviderDeepSeek), "deepseek-flash")               // 无 CC 孪生 → 不动
	insModel("x/deepseek-v4-flash", string(domain.ProviderDeepSeek), "deepseek-v4-flash")       // 已正确 → 不动
	insModel("z-ai/glm-5.3-flash", "", "")                                                      // 双空 → 补绑
	insModel("vendor/flash-x", string(domain.ProviderQwen), "old-slug")                         // 歧义 → 不动

	// 打回 v17:让 step 18 重跑。
	if _, err := st.db.Exec(`DELETE FROM schema_migrations`); err != nil {
		t.Fatalf("clear version: %v", err)
	}
	if _, err := st.db.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (17, ?)`, nowRFC3339()); err != nil {
		t.Fatalf("set v17: %v", err)
	}
	mustNoErr(t, st.Close(), "close")

	st2, err := Open(dsn)
	mustNoErr(t, err, "reopen")
	defer st2.Close()
	sv, _ := st2.SchemaVersion()
	mustEqual(t, sv, schemaVersion, "升级后 = schemaVersion")

	binding := func(s *Store, name string) (string, string) {
		t.Helper()
		var v, o string
		mustNoErr(t, s.db.QueryRow(`SELECT official_vendor, official_model_name FROM models WHERE name=?`, name).Scan(&v, &o), "read binding "+name)
		return v, o
	}
	if _, o := binding(st2, "deepseek/deepseek-v4.1-flash"); o != "deepseek-v4-1-flash" {
		t.Errorf("点号名+陈旧 slug 应改绑 deepseek-v4-1-flash: got %q", o)
	}
	if _, o := binding(st2, "deepseek/deepseek-v4-flash"); o != "deepseek-v4-flash" {
		t.Errorf("点号名+陈旧 slug 应改绑 deepseek-v4-flash: got %q", o)
	}
	if _, o := binding(st2, "deepseek-flash"); o != "deepseek-flash" {
		t.Errorf("无 CC 孪生的行不该被动: got %q", o)
	}
	if _, o := binding(st2, "x/deepseek-v4-flash"); o != "deepseek-v4-flash" {
		t.Errorf("已正确的行不该被动: got %q", o)
	}
	if v, o := binding(st2, "z-ai/glm-5.3-flash"); o != "glm-5-3-flash" || v != string(domain.ProviderZhipu) {
		t.Errorf("双空行应补绑 glm-5-3-flash/智谱: got %q/%q", v, o)
	}
	if _, o := binding(st2, "vendor/flash-x"); o != "old-slug" {
		t.Errorf("同 slug 多厂商歧义不该被动: got %q", o)
	}
	// 无点号、无 CC 孪生的模型 deepseek-flash:再确认 vendor 也没被改。
	if v, _ := binding(st2, "deepseek-flash"); v != string(domain.ProviderDeepSeek) {
		t.Errorf("deepseek-flash 的 vendor 不该被动: got %q", v)
	}

	// 幂等:再开一次不再动。
	mustNoErr(t, st2.Close(), "close2")
	st3, err := Open(dsn)
	mustNoErr(t, err, "third open")
	defer st3.Close()
	if _, o := binding(st3, "deepseek/deepseek-v4.1-flash"); o != "deepseek-v4-1-flash" {
		t.Errorf("重开后仍应保持 deepseek-v4-1-flash: got %q", o)
	}
	if _, o := binding(st3, "z-ai/glm-5.3-flash"); o != "glm-5-3-flash" {
		t.Errorf("重开后仍应保持 glm-5-3-flash: got %q", o)
	}
}
