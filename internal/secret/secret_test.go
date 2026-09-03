package secret

import (
	"os"
	"path/filepath"
	"testing"
)

func tmpDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "data")
}

// resetKey 清空单例,让每个测试独立引导自己的主密钥。
func resetKey() {
	current = nil
	loaded = false
}

func TestBootstrapFileRoundTrip(t *testing.T) {
	resetKey()
	dir := tmpDir(t)
	k1, err := BootstrapKey(dir)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	// 幂等:第二次返回同一把钥匙,且文件存在
	k2, err := BootstrapKey(dir)
	if err != nil {
		t.Fatalf("bootstrap again: %v", err)
	}
	if string(k1) != string(k2) {
		t.Error("second bootstrap returned different key")
	}
	fi, err := os.Stat(filepath.Join(dir, KeyFile))
	if err != nil {
		t.Fatalf("master key file missing: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("key file mode = %o, want 600", fi.Mode().Perm())
	}

	enc, err := Encrypt("sk-secret-abc")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if enc == "" {
		t.Fatal("encrypt returned empty for non-empty plaintext")
	}
	got, err := Decrypt(enc)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if got != "sk-secret-abc" {
		t.Errorf("roundtrip = %q, want original", got)
	}
}

func TestEncryptEmptyPlaintext(t *testing.T) {
	resetKey()
	dir := tmpDir(t)
	if _, err := BootstrapKey(dir); err != nil {
		t.Fatal(err)
	}
	enc, err := Encrypt("")
	if err != nil {
		t.Fatalf("encrypt empty: %v", err)
	}
	if enc != "" {
		t.Errorf("empty plaintext should not be stored, got %q", enc)
	}
}

func TestEnvKeyWinsOverFile(t *testing.T) {
	resetKey()
	t.Setenv(EnvKey, "a long-ish env passphrase that gets hashed")
	dir := tmpDir(t)
	k, err := BootstrapKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, KeyFile)); !os.IsNotExist(err) {
		t.Error("should not write key file when env is set")
	}
	// env 密钥必须可用
	enc, _ := Encrypt("x")
	got, err := Decrypt(enc)
	if err != nil || got != "x" {
		t.Errorf("env-key roundtrip failed: %v %q", err, got)
	}
	_ = k
}

func TestWrongKeyFails(t *testing.T) {
	resetKey()
	dir := tmpDir(t)
	if _, err := BootstrapKey(dir); err != nil {
		t.Fatal(err)
	}
	enc, _ := Encrypt("secret")

	// 模拟密钥轮换:直接替换包内 current(生产上等同换 GW_MASTER_KEY/删除 key 文件)
	current = make([]byte, 32)
	for i := range current {
		current[i] = byte(i + 1)
	}
	if got, err := Decrypt(enc); err == nil {
		t.Errorf("decrypt with rotated key succeeded: %q", got)
	}
	resetKey()
}
