package store

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// 创建→列/查/吊销的完整生命周期;哈希与明文永不混存。
func TestKeysLifecycle(t *testing.T) {
	st, err := Open(t.TempDir() + "/gw.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	secret := "sk-gw-" + strings.Repeat("ab12", 8) // 36 chars
	k, err := st.CreateKey("ci-runner", "用于 CI", secret)
	if err != nil {
		t.Fatal(err)
	}
	if k.Name != "ci-runner" || k.Note != "用于 CI" || k.Revoked || k.RevokedAt != nil {
		t.Fatalf("create returned wrong row: %+v", k)
	}
	if k.Prefix != secret[:12] {
		t.Errorf("prefix=%q want first 12 chars", k.Prefix)
	}
	if k.ID == 0 || k.CreatedAt.IsZero() {
		t.Errorf("id/created_at missing: %+v", k)
	}

	// 列表:不回显 secret/哈希(结构里根本没有这两个字段)
	rows, err := st.ListKeys()
	if err != nil || len(rows) != 1 {
		t.Fatalf("list: %d rows err=%v", len(rows), err)
	}
	if rows[0].Prefix != secret[:12] {
		t.Errorf("list prefix mismatch: %q", rows[0].Prefix)
	}

	// 按明文查激活 key 命中
	got, err := st.LookupActiveKey(secret)
	if err != nil || got.Name != "ci-runner" {
		t.Fatalf("lookup active: %+v err=%v", got, err)
	}
	// 错误 secret → ErrKeyNotFound
	if _, err := st.LookupActiveKey("sk-gw-wrong-secret"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("lookup wrong secret want ErrKeyNotFound got %v", err)
	}

	// 吊销 → 激活查询不再命中;再吊销幂等且 revoked_at 保持首次
	at := time.Now().UTC()
	rev, err := st.RevokeKey("ci-runner", at)
	if err != nil || !rev.Revoked || rev.RevokedAt == nil {
		t.Fatalf("revoke: %+v err=%v", rev, err)
	}
	if _, err := st.LookupActiveKey(secret); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("revoked key still active: %v", err)
	}
	rev2, err := st.RevokeKey("ci-runner", at.Add(time.Hour))
	if err != nil || !rev2.Revoked {
		t.Fatalf("re-revoke: %+v err=%v", rev2, err)
	}
	if !rev2.RevokedAt.Equal(*rev.RevokedAt) {
		t.Errorf("re-revoke changed revoked_at: %v → %v", *rev.RevokedAt, *rev2.RevokedAt)
	}
	// 吊销不存在的 → ErrKeyNotFound
	if _, err := st.RevokeKey("nope", at); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("revoke missing want ErrKeyNotFound got %v", err)
	}
}

// 重名/重复 secret 由 UNIQUE 约束挡下;哈希不与明文相等。
func TestKeyUniqueness(t *testing.T) {
	st, err := Open(t.TempDir() + "/gw.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	secret := "sk-gw-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err := st.CreateKey("a", "", secret); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateKey("a", "", "sk-gw-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"); err == nil ||
		!strings.Contains(err.Error(), "UNIQUE") {
		t.Fatalf("duplicate name want UNIQUE error got %v", err)
	}
	if _, err := st.CreateKey("b", "", secret); err == nil || !strings.Contains(err.Error(), "UNIQUE") {
		t.Fatalf("duplicate secret want UNIQUE error got %v", err)
	}

	if HashSecret(secret) == secret || len(HashSecret(secret)) != 64 {
		t.Errorf("HashSecret should be 64-hex and never equal plaintext")
	}
}
