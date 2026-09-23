package pricing

import (
	"strings"
	"testing"
	"time"

	"personal-ai-gateway/internal/domain"
)

// TestZeroPriced 零价判定(issue #26):只有「官方价与兜底四价都拿不出非 0 数字」才算零价。
//
// 判定必须是**交集**而非并集 —— 缓存读价为 0、仅无官方价但手填了兜底价,都要放行,
// 否则会误伤生产里 74 个走兜底价的模型(见 issue 正文的回归红线)。
func TestZeroPriced(t *testing.T) {
	at := beijing(2026, time.September, 23, 10, 0)
	cnSettings := domain.Settings{DisplayCurrency: domain.CurrencyCNY, TZOffsetMin: 480}

	deepseek := deepseekRow(false) // CNY 峰谷:谷 1/4、峰 2/8

	cases := []struct {
		name   string
		q      *domain.OfficialPriceRow
		offer  domain.OfferRead
		want   bool
		reason string // 期望成因里的关键子串(空 = 不校验)
	}{
		{
			name:  "无官方价 + 兜底四价全 0 → 零价",
			q:     nil,
			offer: domain.OfferRead{},
			want:  true, reason: "未绑定官方价",
		},
		{
			name: "无官方价 + 只填了输入价 → 非零价",
			q:    nil,
			// 只填输入价也是有效依据(缓存写价 0 有「按输入价回落」的语义)。
			offer: domain.OfferRead{InputPriceUsd: 3},
			want:  false,
		},
		{
			name:  "无官方价 + 缓存读价为 0 但 in/out 非 0 → 非零价(回归红线)",
			q:     nil,
			offer: domain.OfferRead{InputPriceUsd: 3, OutputPriceUsd: 15, CacheReadPriceUsd: 0},
			want:  false,
		},
		{
			name:  "有官方价 → 非零价(与之无关的兜底空值不影响)",
			q:     &deepseek,
			offer: domain.OfferRead{},
			want:  false,
		},
		{
			name:  "官方价行存在但单价全 0 + 兜底也全 0 → 零价",
			q:     &domain.OfficialPriceRow{Currency: domain.CurrencyCNY},
			offer: domain.OfferRead{},
			want:  true, reason: "官方价单价为 0",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, reason := ZeroPriced(c.q, c.offer, cnSettings, at)
			if got != c.want {
				t.Fatalf("ZeroPriced = %v (reason %q), want %v", got, reason, c.want)
			}
			if c.reason != "" && !contains(reason, c.reason) {
				t.Errorf("成因 %q 不含 %q", reason, c.reason)
			}
		})
	}
}

// TestZeroPricedExchangeRateMissing 官方价存在、币种不一致且未设汇率 → 换算失败回落兜底;
// 兜底也全 0 时判零价,且成因须区分出「汇率缺失」这一种。
func TestZeroPricedExchangeRateMissing(t *testing.T) {
	at := beijing(2026, time.September, 23, 10, 0)
	// 计价币种 USD,官方价 CNY,未设 USDPerCNY → Convert 报 ErrNoExchangeRate。
	settings := domain.Settings{DisplayCurrency: domain.CurrencyUSD, TZOffsetMin: 480}
	q := deepseekRow(false)

	zero, reason := ZeroPriced(&q, domain.OfferRead{}, settings, at)
	if !zero {
		t.Fatalf("汇率缺失 + 兜底全 0 应判零价,got false")
	}
	if !contains(reason, "汇率") {
		t.Errorf("成因 %q 应点出汇率缺失", reason)
	}

	// 兜底有价时,汇率缺失不该导致零价(有手填依据)。
	zero2, _ := ZeroPriced(&q, domain.OfferRead{InputPriceUsd: 3}, settings, at)
	if zero2 {
		t.Error("汇率缺失但有兜底价,不应判零价")
	}
}

// TestOfferHasNoPrice 四价全 0 判据(缓存读/写为 0 单独不构成「无价」)。
func TestOfferHasNoPrice(t *testing.T) {
	if !OfferHasNoPrice(domain.OfferRead{}) {
		t.Error("空四价应判无价")
	}
	if OfferHasNoPrice(domain.OfferRead{InputPriceUsd: 1}) {
		t.Error("有输入价不应判无价")
	}
	// 只有缓存写价非 0:也是依据(缓存写 token 会按它计),不算无价。
	if OfferHasNoPrice(domain.OfferRead{CacheWritePriceUsd: 3.75}) {
		t.Error("有缓存写价不应判无价")
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
