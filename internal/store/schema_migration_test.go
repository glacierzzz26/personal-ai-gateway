package store

import (
	"testing"

	"personal-ai-gateway/internal/pgtest"
)

// TestMigrateFromV14AddsModelRatioTable 已存在的 v14 库打开后应**走增量步骤**升到 v15:
// 建出 channel_model_cost_ratios 表,且旧数据不丢、不重跑基线(重跑会 CREATE TABLE 冲突)。
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
	mustEqual(t, sv, schemaVersion, "升级后版本 = schemaVersion(15)")

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
