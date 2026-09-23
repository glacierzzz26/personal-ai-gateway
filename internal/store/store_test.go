package store

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"personal-ai-gateway/internal/secret"
)

// newTestStore 打开一个临时库;首次会引导全局主密钥(密文/解密同进程一致)。
func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	if _, err := secret.BootstrapKey(dir); err != nil {
		t.Fatalf("bootstrap master key: %v", err)
	}
	st, err := Open(filepath.Join(dir, "test.db"))
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

func TestOpenMigratesAndIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.db")
	st, err := Open(path)
	mustNoErr(t, err, "first open")
	st.Close()

	st2, err := Open(path) // 重复打开:迁移幂等
	mustNoErr(t, err, "second open")
	defer st2.Close()

	var version int
	mustNoErr(t, st2.db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version), "read schema_migrations")
	if version != len(migrations) {
		t.Errorf("migration version = %d, want %d", version, len(migrations))
	}
	// SchemaVersion(/healthz 的 schema 字段来源)应与迁移条数一致 —— 升级脚本据此判降级。
	sv, err := st2.SchemaVersion()
	mustNoErr(t, err, "SchemaVersion")
	mustEqual(t, sv, len(migrations), "SchemaVersion == len(migrations)")
	// 业务表应就绪(抽查几张三件套)
	for _, table := range []string{"channels", "models", "model_offers", "rules", "tokens", "request_logs", "settings", "admins", "official_prices", "announcements", "announcement_dismissals"} {
		var n int
		err := st2.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n)
		mustNoErr(t, err, "sqlite_master")
		mustEqual(t, n, 1, "table "+table+" exists")
	}
	// m0002 后 sessions 已退役(JWT 无状态)
	var gone int
	mustNoErr(t, st2.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='sessions'`).Scan(&gone), "check sessions gone")
	mustEqual(t, gone, 0, "sessions table dropped")
}

// TestM0011BackfillsLegacyChannels 迁移回填:老库的 Azure/聚合中转 渠道应被
// 拆成 egress_proto + 空 provider,commandcode/opencode 按 base_url 认领渠道类型。
// 这些行在迁移前 provider 是唯一线索,回填错了会静默改变出站协议 —— 故逐条断言。
func TestM0011BackfillsLegacyChannels(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")

	// 建一个「迁移前」形态的库:跑到 m0010 为止(手工插入 m0011 之前的渠道老行),
	// 再由 Open 跑完 m0011 及之后各步。
	//
	// ⚠️ 必须按**内容**定位 m0011,不能用 len(migrations)-1 —— 那只在「m0011 恰好是最后一步」
	// 时成立,追加任何新迁移都会让这条用例静默地测错版本(表现为回填断言全部落空)。
	const wantThrough = m0011ChannelQuota
	nThrough := 0
	for i, step := range migrations {
		if step == wantThrough {
			nThrough = i // 0-based:第 i 步(含)之前 = 只跑前 i 步
			break
		}
	}
	if nThrough == 0 {
		t.Fatal("未在 migrations 中找到 m0011 —— 断言过时,请更新本用例")
	}
	dsn, err := sqliteDSN(path)
	mustNoErr(t, err, "dsn")
	db, err := sql.Open("sqlite", dsn)
	mustNoErr(t, err, "open raw")
	_, err = db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`)
	mustNoErr(t, err, "create schema_migrations")
	for i, step := range migrations[:nThrough] {
		_, err = db.Exec(step)
		mustNoErr(t, err, fmt.Sprintf("apply migration %d", i+1))
		_, err = db.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`, i+1, nowRFC3339())
		mustNoErr(t, err, "record migration")
	}

	now := nowRFC3339()
	legacy := []struct{ name, provider, baseURL string }{
		{"old-azure", "Azure", "https://myres.openai.azure.com"},
		{"old-agg", "聚合中转", "https://relay.example.com"},
		{"old-cc", "OpenAI", "https://api.commandcode.ai/provider/v1"},
		{"old-ant", "Anthropic", "https://api.anthropic.com"},
		{"old-ds", "DeepSeek", "https://api.deepseek.com/v1"},
	}
	for _, c := range legacy {
		_, err := db.Exec(`INSERT INTO channels (name, provider, base_url, api_key_cipher, key_masked,
			priority, weight, timeout_ms, tags, enabled, max_failures, cooldown_sec, note, created_at, updated_at)
			VALUES (?,?,?,'','',0,0,60000,'[]',1,3,60,'',?,?)`, c.name, c.provider, c.baseURL, now, now)
		mustNoErr(t, err, "insert legacy "+c.name)
	}
	db.Close()

	st, err := Open(path)
	mustNoErr(t, err, "reopen to apply m0011")
	defer st.Close()

	want := map[string]struct{ provider, egress, ctype string }{
		"old-azure": {"OpenAI", "azure", "thirdparty"},
		"old-agg":   {"", "openai", "thirdparty"},
		"old-cc":    {"OpenAI", "openai", "commandcode"},
		"old-ant":   {"Anthropic", "anthropic", "thirdparty"},
		"old-ds":    {"DeepSeek", "openai", "deepseek"},
	}
	for _, c := range legacy {
		ch, err := st.GetChannelByName(c.name)
		mustNoErr(t, err, "get "+c.name)
		w := want[c.name]
		if string(ch.Provider) != w.provider {
			t.Errorf("%s provider = %q, want %q", c.name, ch.Provider, w.provider)
		}
		if string(ch.EgressProto) != w.egress {
			t.Errorf("%s egress_proto = %q, want %q", c.name, ch.EgressProto, w.egress)
		}
		if string(ch.ChannelType) != w.ctype {
			t.Errorf("%s channel_type = %q, want %q", c.name, ch.ChannelType, w.ctype)
		}
	}
}
