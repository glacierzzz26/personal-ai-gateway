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
		// S4:厂商白名单放开后,Claude/GPT 名也参与推断(聚合渠道直接沿用厂商名)。
		{"claude-sonnet-5", domain.ProviderAnthropic},
		{"claude-3-5-sonnet", domain.ProviderAnthropic},
		{"gpt-4o", domain.ProviderOpenAI},
		{"gpt-5.1-mini", domain.ProviderOpenAI},
		// 判不出 / 无官方来源 → 空(交由模型级显式绑定兜底)。
		{"", ""},
		{"moonshot-v1-8k", ""},
		{"openai/deepseek-chat", ""}, // 只看首段:聚合商前缀优先,刻意保守
		{"glmx", ""},                 // 匹配词后紧跟字母不算命中
		{"gptx", ""},                 // 同理,防 "gpt" 命中 "gptx"
	}
	for _, c := range cases {
		if got := InferVendor(c.model); got != c.want {
			t.Errorf("InferVendor(%q) = %q, want %q", c.model, got, c.want)
		}
	}
}

func TestVendors(t *testing.T) {
	vs := Vendors()
	// S4 后:DeepSeek/通义(可抓)+ 智谱/Anthropic/OpenAI/Moonshot(仅手工)= 6。
	// (Azure 已从 provider 枚举移除 —— 按区域部署定价,不是厂商。)
	if len(vs) != 6 {
		t.Fatalf("Vendors() len = %d, want 6: %+v", len(vs), vs)
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
	// 仅手工厂商:抓不了,但支持手工录入,且带出默认原币。
	for p, cur := range map[domain.Provider]domain.Currency{
		domain.ProviderZhipu:     domain.CurrencyCNY,
		domain.ProviderAnthropic: domain.CurrencyUSD,
		domain.ProviderOpenAI:    domain.CurrencyUSD,
		domain.ProviderMoonshot:  domain.CurrencyCNY,
	} {
		if !byP[p].ManualOnly {
			t.Errorf("%s should be manual-only", p)
		}
		if byP[p].ManualCurrency != cur {
			t.Errorf("%s manualCurrency = %q, want %q", p, byP[p].ManualCurrency, cur)
		}
	}
	for _, p := range []domain.Provider{domain.ProviderDeepSeek, domain.ProviderQwen} {
		if byP[p].ManualOnly {
			t.Errorf("%s should be fetchable", p)
		}
	}
}
