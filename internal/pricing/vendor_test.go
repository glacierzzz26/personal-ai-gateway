package pricing

import (
	"testing"

	"personal-ai-gateway/internal/domain"
)

func TestInferVendor(t *testing.T) {
	cases := []struct {
		model string
		want  domain.Provider
	}{
		{"deepseek/deepseek-v4.1-flash", domain.ProviderDeepSeek},
		{"deepseek-v4.1-flash", domain.ProviderDeepSeek},
		{"deepseek-chat", domain.ProviderDeepSeek},
		{"DeepSeek-V3", domain.ProviderDeepSeek},
		{"qwen3.8-max", domain.ProviderQwen},
		{"qwen/qwen-max", domain.ProviderQwen},
		{"tongyi-qwen-max", domain.ProviderQwen},
		{"glm-4.6", domain.ProviderZhipu},
		{"chatglm3", domain.ProviderZhipu},
		{"zhipu/glm-4", domain.ProviderZhipu},
		// 判不出 / 无官方来源 → 空(交由模型级显式绑定兜底)。
		{"", ""},
		{"gpt-4o", ""},
		{"claude-3-5-sonnet", ""},
		{"moonshot-v1-8k", ""},
		{"openai/deepseek-chat", ""}, // 只看首段:聚合商前缀优先,刻意保守
		{"glmx", ""},                 // 匹配词后紧跟字母不算命中
	}
	for _, c := range cases {
		if got := InferVendor(c.model); got != c.want {
			t.Errorf("InferVendor(%q) = %q, want %q", c.model, got, c.want)
		}
	}
}

func TestVendors(t *testing.T) {
	vs := Vendors()
	if len(vs) != 3 {
		t.Fatalf("Vendors() len = %d, want 3: %+v", len(vs), vs)
	}
	// 字典序稳定输出。
	for i := 1; i < len(vs); i++ {
		if vs[i-1].Provider >= vs[i].Provider {
			t.Errorf("Vendors() not sorted: %q >= %q", vs[i-1].Provider, vs[i].Provider)
		}
	}
	byP := map[domain.Provider]VendorInfo{}
	for _, v := range vs {
		if v.SourceURL == "" {
			t.Errorf("%s: source url empty", v.Provider)
		}
		byP[v.Provider] = v
	}
	if !byP[domain.ProviderZhipu].ManualOnly {
		t.Error("智谱 should be manual-only")
	}
	for _, p := range []domain.Provider{domain.ProviderDeepSeek, domain.ProviderQwen} {
		if byP[p].ManualOnly {
			t.Errorf("%s should be fetchable", p)
		}
	}
}
