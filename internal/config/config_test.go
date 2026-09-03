package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveUpstreamsExpandsAndDefaults(t *testing.T) {
	t.Setenv("UP_KEY", "sk-secret")
	t.Setenv("UP_BASE", "https://opencode.ai/zen/go")
	raw := []Upstream{
		{Name: "a", Type: TypeAnthropic, BaseURL: "${UP_BASE}", APIKey: "${UP_KEY}", Priority: 1},
		{Name: "b", Type: TypeOpenAI, BaseURL: "https://x/v1", APIKey: "plain", CooldownSec: 5},
	}
	got := ResolveUpstreams(raw)

	// raw 不被原地改(拷贝语义):仍保留 ${ENV} 引用
	if raw[0].APIKey != "${UP_KEY}" {
		t.Errorf("raw mutated: %q", raw[0].APIKey)
	}
	if got[0].APIKey != "sk-secret" || got[0].BaseURL != "https://opencode.ai/zen/go" {
		t.Errorf("expand failed: %+v", got[0])
	}
	// 默认值:cooldown/max_failures;quota.Enabled 时才补窗口/阈值
	if got[1].CooldownSec != 5 || got[1].MaxFailures != 3 {
		t.Errorf("defaults wrong: %+v", got[1])
	}
	q := &QuotaConfig{Enabled: true, CacheTTLSec: 10}
	raw2 := []Upstream{{Name: "c", Type: TypeOpenAI, BaseURL: "https://x/v1", APIKey: "k", Quota: q}}
	r2 := ResolveUpstreams(raw2)
	if r2[0].Quota.Window != "monthly" || r2[0].Quota.WarnUsedPct != 80 || r2[0].Quota.HardUsedPct != 95 || r2[0].Quota.CacheTTLSec != 10 {
		t.Errorf("quota defaults wrong: %+v", r2[0].Quota)
	}
}

func TestValidateUpstreams(t *testing.T) {
	base := Upstream{Name: "a", Type: TypeOpenAI, BaseURL: "https://x/v1", APIKey: "k"}
	if err := ValidateUpstreams([]Upstream{base}); err != nil {
		t.Fatalf("valid rejected: %v", err)
	}
	dup := base
	dup.Name = "b"
	dup2 := base
	dup2.Name = "b"
	if err := ValidateUpstreams([]Upstream{dup, dup2}); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("want duplicate error, got %v", err)
	}
	bad := base
	bad.Type = "grpc"
	if err := ValidateUpstreams([]Upstream{bad}); err == nil || !strings.Contains(err.Error(), "type") {
		t.Errorf("want type error, got %v", err)
	}
	nokey := base
	nokey.APIKey = ""
	if err := ValidateUpstreams([]Upstream{nokey}); err == nil {
		t.Error("want missing api_key error")
	}
	badq := base
	badq.Quota = &QuotaConfig{Enabled: true, Window: "monthly", WarnUsedPct: 90, HardUsedPct: 50}
	if err := ValidateUpstreams([]Upstream{badq}); err == nil {
		t.Error("want quota range error")
	}
}

// LoadSeedUpstreams 不展开 ${ENV},原样返回 raw。
func TestLoadSeedUpstreamsKeepsEnvRefs(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	content := `upstreams:
  - name: a
    type: anthropic
    base_url: ${ANTHROPIC_BASE_URL}
    api_key: ${ANTHROPIC_API_KEY}
    priority: 1
`
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	ups, err := LoadSeedUpstreams(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(ups) != 1 || ups[0].APIKey != "${ANTHROPIC_API_KEY}" || ups[0].BaseURL != "${ANTHROPIC_BASE_URL}" {
		t.Fatalf("seed not raw: %+v", ups)
	}
}
