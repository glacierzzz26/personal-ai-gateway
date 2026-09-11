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
	if cfg.DBPath != "gateway-v2.db" {
		t.Errorf("db_path default = %q, want gateway-v2.db (v2 换新库)", cfg.DBPath)
	}
}

func TestLoadOverrides(t *testing.T) {
	// os.ExpandEnv 在解析时执行,因此需先注入 env 再 Load
	t.Setenv("TMP_DB", "/tmp/x.db")
	cfg, err := Load(write(t, "listen: \":9999\"\ndb_path: \"${TMP_DB}\"\n"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Listen != ":9999" {
		t.Errorf("listen = %q, want :9999", cfg.Listen)
	}
	if cfg.DBPath != "/tmp/x.db" {
		t.Errorf("db_path = %q, want /tmp/x.db", cfg.DBPath)
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
