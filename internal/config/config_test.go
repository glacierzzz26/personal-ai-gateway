package config

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(write(t, ""))
	if err != nil {
		t.Fatalf("load empty config: %v", err)
	}
	if cfg.Listen != ":8787" {
		t.Errorf("listen default = %q, want :8787", cfg.Listen)
	}
	if cfg.DBDSN != "postgres://gw:gw@127.0.0.1:5432/gateway?sslmode=disable" {
		t.Errorf("db_dsn default = %q, want local PG dsn", cfg.DBDSN)
	}
	if cfg.KeyDir != "/data" {
		t.Errorf("key_dir default = %q, want /data", cfg.KeyDir)
	}
}

func TestLoadOverrides(t *testing.T) {
	// os.ExpandEnv 在解析时执行,因此需先注入 env 再 Load
	t.Setenv("TMP_DSN", "postgres://u:p@h:5432/db")
	cfg, err := Load(write(t, "listen: \":9999\"\ndb_dsn: \"${TMP_DSN}\"\nkey_dir: \"/keys\"\n"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Listen != ":9999" {
		t.Errorf("listen = %q, want :9999", cfg.Listen)
	}
	if cfg.DBDSN != "postgres://u:p@h:5432/db" {
		t.Errorf("db_dsn = %q, want postgres://u:p@h:5432/db", cfg.DBDSN)
	}
	if cfg.KeyDir != "/keys" {
		t.Errorf("key_dir = %q, want /keys", cfg.KeyDir)
	}
}

func TestTLSEnabled(t *testing.T) {
	full := TLSConfig{
		APIListen: ":17080", APICert: "/certs/api/cert.pem", APIKey: "/certs/api/key.pem",
		AdminListen: ":17090", AdminCert: "/certs/admin/cert.pem", AdminKey: "/certs/admin/key.pem",
	}
	if !full.Enabled() {
		t.Fatalf("完整 TLS 配置 Enabled = false, want true")
	}
	for name, mut := range map[string]func(*TLSConfig){
		"缺 api_listen":   func(c *TLSConfig) { c.APIListen = "" },
		"缺 api_key":      func(c *TLSConfig) { c.APIKey = "" },
		"缺 admin_cert":   func(c *TLSConfig) { c.AdminCert = "" },
		"缺 admin_listen": func(c *TLSConfig) { c.AdminListen = "" },
	} {
		c := full
		mut(&c)
		if c.Enabled() {
			t.Errorf("%s:TLS 配置不齐 Enabled = true, want false", name)
		}
	}
	if (TLSConfig{}).Enabled() {
		t.Errorf("空 TLSConfig Enabled = true, want false")
	}
}
