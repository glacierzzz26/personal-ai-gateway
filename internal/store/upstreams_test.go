package store

import (
	"testing"

	"personal-ai-gateway/internal/config"
)

// 替换→读回:顺序、字段、${ENV} 引用都原样保持。
func TestUpstreamsRoundtrip(t *testing.T) {
	st, err := Open(t.TempDir() + "/gw.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	in := []config.Upstream{
		{Name: "a", Type: config.TypeAnthropic, BaseURL: "${ANTHROPIC_BASE_URL}", APIKey: "${ANTHROPIC_API_KEY}",
			Priority: 1, Models: []string{"*"}, CooldownSec: 5, MaxFailures: 2,
			Quota: &config.QuotaConfig{Enabled: true, Window: "monthly", WarnUsedPct: 80, HardUsedPct: 95, CacheTTLSec: 60}},
		{Name: "b", Type: config.TypeOpenAI, BaseURL: "https://x.example/v1", APIKey: "literal-key", Priority: 2},
	}
	if err := st.ReplaceUpstreams(in); err != nil {
		t.Fatal(err)
	}

	got, err := st.LoadUpstreams()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "a" || got[1].Name != "b" {
		t.Fatalf("order/len wrong: %+v", got)
	}
	a := got[0]
	if a.BaseURL != "${ANTHROPIC_BASE_URL}" || a.APIKey != "${ANTHROPIC_API_KEY}" {
		t.Errorf("env ref not preserved: %+v", a)
	}
	if a.CooldownSec != 5 || a.MaxFailures != 2 || a.Priority != 1 {
		t.Errorf("fields lost: %+v", a)
	}
	if a.Quota == nil || a.Quota.HardUsedPct != 95 {
		t.Errorf("quota lost: %+v", a.Quota)
	}
	if got[1].APIKey != "literal-key" {
		t.Errorf("literal key changed: %q", got[1].APIKey)
	}

	// 整体替换为一条 → 旧的全部消失
	if err := st.ReplaceUpstreams(in[:1]); err != nil {
		t.Fatal(err)
	}
	got2, _ := st.LoadUpstreams()
	if len(got2) != 1 || got2[0].Name != "a" {
		t.Fatalf("replace to one failed: %+v", got2)
	}
}
