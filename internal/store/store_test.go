package store

import (
	"errors"
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
	// 业务表应就绪(抽查几张三件套)
	for _, table := range []string{"channels", "models", "model_offers", "rules", "tokens", "request_logs", "settings", "admins"} {
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
