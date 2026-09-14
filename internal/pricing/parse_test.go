package pricing

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"personal-ai-gateway/internal/domain"
)

// loadFixture 读 testdata 里的官方页面样本。
func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

// 官方样本必须完整解析,数值与页面一致(防解析器静默退化)。
func TestParseDeepSeekOfficialSample(t *testing.T) {
	quotes, cur, shape, err := parseDeepSeek(loadFixture(t, "deepseek_pricing.html"))
	if err != nil {
		t.Fatalf("parseDeepSeek: %v", err)
	}
	if cur != domain.CurrencyCNY {
		t.Errorf("currency = %q, want CNY", cur)
	}
	if shape != domain.ShapePeakOff {
		t.Errorf("shape = %q, want peak_offpeak", shape)
	}
	byName := map[string]quote{}
	for _, q := range quotes {
		byName[q.ModelName] = q
	}
	if len(byName) < 2 {
		t.Fatalf("got %d models, want >= 2: %+v", len(byName), byName)
	}
	// 页面实测:deepseek-flash 空闲 输入(未命中)1 / 输出 4 / 缓存命中 0.02;
	// 高峰为其 2 倍。生效默认取空闲价。
	flash, ok := byName["deepseek-flash"]
	if !ok {
		t.Fatalf("deepseek-flash missing; got %v", keys(byName))
	}
	mustClose(t, "flash.In", flash.In, 1)
	mustClose(t, "flash.Out", flash.Out, 4)
	mustClose(t, "flash.CacheRead", flash.CacheRead, 0.02)
	// 明细须含峰谷两档,且不得把分时价当单一价静默丢弃。
	off, _ := flash.Detail["offpeak"].(map[string]any)
	peak, _ := flash.Detail["peak"].(map[string]any)
	if off == nil || peak == nil {
		t.Fatalf("detail 缺少峰谷两档: %+v", flash.Detail)
	}
	mustClose(t, "peak.out", toF(peak["out"]), 8)
	if flash.Detail["effectiveDefault"] != "offpeak" {
		t.Errorf("effectiveDefault = %v, want offpeak", flash.Detail["effectiveDefault"])
	}
}

func TestParseQwenOfficialSample(t *testing.T) {
	quotes, cur, shape, err := parseQwen(loadFixture(t, "qwen_pricing.html"))
	if err != nil {
		t.Fatalf("parseQwen: %v", err)
	}
	// 国内站为人民币原生价(计价币种为 CNY,故不走汇率折算)。
	if cur != domain.CurrencyCNY {
		t.Errorf("currency = %q, want CNY", cur)
	}
	if shape != domain.ShapeTiered {
		t.Errorf("shape = %q, want tiered", shape)
	}
	byName := map[string]quote{}
	for _, q := range quotes {
		byName[q.ModelName] = q
	}
	// 验收标准点名 qwen3.8 系列。
	mx, ok := byName["qwen3.8-max"]
	if !ok {
		t.Fatalf("qwen3.8-max missing; got %d models e.g. %v", len(byName), sampleNames(byName))
	}
	// 页面实测(北京首档):qwen3.8-max 12 元 / 36 元。
	mustClose(t, "qwen3.8-max.In", mx.In, 12)
	mustClose(t, "qwen3.8-max.Out", mx.Out, 36)
	// 缓存价是推导值,必须标注(不能冒充官方列)。
	if !mx.CacheDerived {
		t.Errorf("qwen cache price must be marked derived")
	}
	mustClose(t, "qwen3.8-max.CacheRead", mx.CacheRead, 1.2)
	// 阶梯档位须完整留证,不能只留首档。
	tiers, _ := mx.Detail["tiers"].([]map[string]any)
	if len(tiers) < 2 {
		t.Errorf("tiers = %d, want >= 2", len(tiers))
	}
	if mx.Detail["effectiveDefault"] != "first-tier" {
		t.Errorf("effectiveDefault = %v, want first-tier", mx.Detail["effectiveDefault"])
	}
	// 第三方转售小节必须剔除:百炼同页转售 glm/deepseek/kimi/minimax 等,模型名与
	// 通义无关、价格也是阿里转售价 —— 并入会把「glm-4.5」错记成通义千问官方价。
	for _, foreign := range []string{"glm-4.5", "glm-4.6", "deepseek-v3", "kimi-k2.5", "MiniMax-M2.1", "ZHIPU/GLM-5"} {
		if _, leaked := byName[foreign]; leaked {
			t.Errorf("第三方模型 %q 不应出现在通义千问官方价里", foreign)
		}
	}
	// 通义自家(含 farui/gui 等)必须保留,不能因过滤误伤。
	for _, own := range []string{"qwen3.8-max", "qwen-max"} {
		if _, ok := byName[own]; !ok {
			t.Errorf("通义自家模型 %q 被误删", own)
		}
	}
}

// 页面改版(结构变化)必须解析失败,绝不能静默给出错误值。
func TestParseFailsLoudOnMalformed(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"empty", ""},
		{"no-unit", "<html><body><table><tr><th>模型</th><th>deepseek-flash</th></tr></table></body></html>"},
		{"renamed-headers", `<html><body>百万 tokens<table><tr><th>Whatever</th><th>Other</th></tr><tr><td>1</td><td>2</td></tr></table></body></html>`},
	}
	for _, tc := range cases {
		if _, _, _, err := parseDeepSeek([]byte(tc.body)); err == nil {
			t.Errorf("deepseek %s: want error, got nil", tc.name)
		}
	}

	// 通义:有单位但表头改名 → 必须失败,不得返回空集成功。
	bad := `<html><body>每百萬Token<table><tr><th>名字改了</th><th>另一个</th></tr><tr><td>x</td><td>1</td></tr></table></body></html>`
	if _, _, _, err := parseQwen([]byte(bad)); err == nil {
		t.Errorf("qwen renamed headers: want error, got nil")
	}
}

// validate 必须拒绝可疑值(宁缺勿假)。
func TestValidateRejectsSuspicious(t *testing.T) {
	if err := validate(nil, domain.CurrencyCNY, domain.ShapeFlat); err == nil {
		t.Error("empty quotes should fail")
	}
	if err := validate([]quote{{ModelName: "m", In: 0, Out: 0}}, domain.CurrencyCNY, domain.ShapeFlat); err == nil {
		t.Error("all-zero price should fail")
	}
	if err := validate([]quote{{ModelName: "m", In: 1, Out: 2}}, domain.Currency("EUR"), domain.ShapeFlat); err == nil {
		t.Error("unknown currency should fail")
	}
	if err := validate([]quote{{ModelName: "m", In: 2e6, Out: 2}}, domain.CurrencyCNY, domain.ShapeFlat); err == nil {
		t.Error("out-of-range price should fail")
	}
	if err := validate([]quote{{ModelName: "m", In: 1, Out: 2}}, domain.CurrencyCNY, domain.ShapeFlat); err != nil {
		t.Errorf("valid quote should pass: %v", err)
	}
}

// —— 传输层白名单:非官方 host 必须发不出去 ——

type fakeRT struct {
	got string
}

func (f *fakeRT) RoundTrip(req *http.Request) (*http.Response, error) {
	f.got = req.URL.Hostname()
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok"))}, nil
}

func TestAllowlistBlocksNonOfficialHost(t *testing.T) {
	base := http.Client{Transport: &fakeRT{}}
	c := AllowlistClient(base, []string{"api-docs.deepseek.com"})

	// 非白名单 host → 拒绝,且底层 RoundTripper 不得被调用。
	req, _ := http.NewRequest(http.MethodGet, "https://evil.example.com/pricing", nil)
	if _, err := c.Do(req); err == nil {
		t.Fatal("non-allowlisted host should be rejected")
	}

	// 白名单 host → 放行。
	req2, _ := http.NewRequest(http.MethodGet, "https://api-docs.deepseek.com/x", nil)
	if _, err := c.Do(req2); err != nil {
		t.Fatalf("allowlisted host should pass: %v", err)
	}
}

func TestManualOnlyProviders(t *testing.T) {
	if !ManualOnly(domain.ProviderZhipu) {
		t.Error("智谱应标记为仅手工录入")
	}
	if ManualOnly(domain.ProviderDeepSeek) || ManualOnly(domain.ProviderQwen) {
		t.Error("DeepSeek/通义 不应是仅手工")
	}
	// S4:Claude/GPT 官方价录不进 → 放开为「仅手工录入」(issue #8)。页面 JS 渲染,抓取不做。
	if !Supports(domain.ProviderAnthropic) || !ManualOnly(domain.ProviderAnthropic) {
		t.Error("Anthropic 应为仅手工录入(官方价需手工录入才能用上)")
	}
	if !Supports(domain.ProviderOpenAI) || !ManualOnly(domain.ProviderOpenAI) {
		t.Error("OpenAI 应为仅手工录入")
	}
	// 聚合中转不是厂商,始终无官方来源。
	if Supports(domain.ProviderOpenRouter) {
		t.Error("聚合中转无官方来源")
	}
}

func TestManualDefaultCurrency(t *testing.T) {
	if c := ManualDefaultCurrency(domain.ProviderAnthropic); c != domain.CurrencyUSD {
		t.Errorf("Anthropic 手工录入默认原币应为 USD, got %q", c)
	}
	if c := ManualDefaultCurrency(domain.ProviderOpenAI); c != domain.CurrencyUSD {
		t.Errorf("OpenAI 手工录入默认原币应为 USD, got %q", c)
	}
	if c := ManualDefaultCurrency(domain.ProviderZhipu); c != domain.CurrencyCNY {
		t.Errorf("智谱手工录入默认原币应为 CNY, got %q", c)
	}
	// 非仅手工厂商无默认(币种由抓取解析决定)。
	if c := ManualDefaultCurrency(domain.ProviderDeepSeek); c != "" {
		t.Errorf("DeepSeek 非仅手工,不应有默认原币, got %q", c)
	}
	// 缺省币种按厂商落地:Anthropic 录入不填币种 → USD 而非 CNY。
	row, err := BuildManual(domain.OfficialPriceInput{
		Provider: domain.ProviderAnthropic, ModelName: "claude-sonnet-5",
		SourceURL: "https://www.anthropic.com/pricing", InputPrice: 3, OutputPrice: 15,
	})
	if err != nil {
		t.Fatalf("manual anthropic: %v", err)
	}
	if row.Currency != domain.CurrencyUSD {
		t.Errorf("缺省币种应为 USD, got %q", row.Currency)
	}
}

func TestBuildManualValidates(t *testing.T) {
	// 缺来源 URL → 必须拒绝(手工录入也要留证)。
	if _, err := BuildManual(domain.OfficialPriceInput{
		Provider: domain.ProviderZhipu, ModelName: "glm-4.6", InputPrice: 1, OutputPrice: 2,
	}); err == nil {
		t.Error("missing sourceUrl should fail")
	}
	// 无官方来源的 provider → 拒绝(聚合中转不是厂商,其价只能来自所转厂商)。
	if _, err := BuildManual(domain.OfficialPriceInput{
		Provider: domain.ProviderOpenRouter, ModelName: "gpt", SourceURL: "https://openai.com/pricing", InputPrice: 1, OutputPrice: 2,
	}); err == nil {
		t.Error("unsupported provider should fail")
	}
	// 正常录入。
	row, err := BuildManual(domain.OfficialPriceInput{
		Provider: domain.ProviderZhipu, ModelName: "glm-4.6",
		SourceURL: "https://bigmodel.cn/pricing", Currency: domain.CurrencyCNY,
		InputPrice: 0.1, OutputPrice: 0.2, NativeText: "限时5折",
	})
	if err != nil {
		t.Fatalf("valid manual: %v", err)
	}
	if row.Provider != domain.ProviderZhipu || row.ModelName != "glm-4.6" || row.InputPrice != 0.1 {
		t.Errorf("unexpected row: %+v", row)
	}
}

// —— 小工具 ——

func mustClose(t *testing.T, what string, got, want float64) {
	t.Helper()
	if d := got - want; d > 1e-6 || d < -1e-6 {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

func toF(v any) float64 {
	f, _ := v.(float64)
	return f
}

func keys(m map[string]quote) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func sampleNames(m map[string]quote) []string {
	var out []string
	for k := range m {
		out = append(out, k)
		if len(out) >= 8 {
			break
		}
	}
	return out
}
