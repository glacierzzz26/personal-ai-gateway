package pricing

import (
	"sort"
	"strings"
	"unicode"

	"personal-ai-gateway/internal/domain"
)

// vendorToken 模型名 → 厂商的匹配词。顺序即优先级(长的在前,避免 "glm" 抢在 "chatglm" 前)。
var vendorTokens = []struct {
	tok string
	p   domain.Provider
}{
	{"deepseek", domain.ProviderDeepSeek},
	{"chatglm", domain.ProviderZhipu},
	{"zhipu", domain.ProviderZhipu},
	{"glm", domain.ProviderZhipu},
	{"tongyi", domain.ProviderQwen},
	{"qwen", domain.ProviderQwen},
	// S4:聚合中转渠道常直接沿用厂商模型名(claude-sonnet-5 / gpt-4o),放开厂商白名单后
	// 也纳入推断,使这类模型能自动命中厂商官方价(否则得逐个手工绑定)。
	{"claude", domain.ProviderAnthropic},
	{"gpt", domain.ProviderOpenAI},
}

// InferVendor 从模型名保守推断厂商,供聚合中转等「provider 非厂商」的渠道匹配厂商官方价。
//
// 规则:取首个 '/' 之前的段作候选(无 '/' 取整串,大小写归一);候选等于匹配词,
// 或以匹配词开头且紧随其后不是字母(如 -、.、_、数字)即命中。
// 只看首个段是刻意的保守取舍:openai/deepseek-chat 返回 ""(聚合商前缀优先),
// 此类命名歧义场景交由模型级显式绑定(internal/store models.official_vendor)兜底。
//
// 仅返回 scrapers 表内有官方来源的厂商;判不出返回 ""。
func InferVendor(model string) domain.Provider {
	seg := strings.TrimSpace(model)
	if i := strings.IndexByte(seg, '/'); i >= 0 {
		seg = seg[:i]
	}
	seg = strings.ToLower(strings.TrimSpace(seg))
	if seg == "" {
		return ""
	}
	for _, vt := range vendorTokens {
		if !strings.HasPrefix(seg, vt.tok) {
			continue
		}
		rest := seg[len(vt.tok):]
		if rest == "" || !unicode.IsLetter(rune(rest[0])) {
			return vt.p
		}
	}
	return ""
}

// VendorInfo 一个「有官方来源」的厂商及其可抓取性,供管理台驱动抓取/手工录入入口。
type VendorInfo struct {
	Provider   domain.Provider `json:"provider"`
	SourceURL  string          `json:"sourceUrl"`
	ManualOnly bool            `json:"manualOnly"`
	// ManualCurrency 手工录入的默认原币种(CNY/USD)。仅 ManualOnly 有意义。
	ManualCurrency domain.Currency `json:"manualCurrency,omitempty"`
}

// Vendors 全部有官方来源的厂商(provider 字典序稳定输出)。
func Vendors() []VendorInfo {
	out := make([]VendorInfo, 0, len(scrapers))
	for p, s := range scrapers {
		cur := s.ManualCurrency
		if s.ManualOnly && cur == "" {
			cur = domain.CurrencyCNY // 未显式标注的仅手工厂商按国内站默认人民币
		}
		out = append(out, VendorInfo{
			Provider: p, SourceURL: s.URL, ManualOnly: s.ManualOnly, ManualCurrency: cur,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Provider < out[j].Provider })
	return out
}

// ManualDefaultCurrency 手工录入某厂商官方价时的默认原币种(CNY/USD)。
// 非仅手工厂商返回空 —— 它们的币种由抓取解析结果决定。
func ManualDefaultCurrency(p domain.Provider) domain.Currency {
	s, ok := scrapers[p]
	if !ok || !s.ManualOnly {
		return ""
	}
	if s.ManualCurrency == "" {
		return domain.CurrencyCNY
	}
	return s.ManualCurrency
}
