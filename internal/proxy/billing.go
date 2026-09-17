package proxy

import (
	"time"

	"personal-ai-gateway/internal/domain"
	"personal-ai-gateway/internal/engine"
	"personal-ai-gateway/internal/pricing"
	"personal-ai-gateway/internal/proxy/translate"
)

// 计费定稿:一次请求的成本与售价。
//
// 改造前的病根是**成本与售价各算一套** —— chargeUsd 走官方价换算(RetailPrice),
// settle 又回头读 offer 标量(costUsd)。分时之后两套还会各自选价,必然漂移。
//
// 现在收敛成一个原语(见 pricing.ShapePrice):官方价按 at 时刻选形态、换算到计价币种,然后
//
//	成本   = 生效单价 × 渠道成本系数 ratio
//	本站价 = 生效单价 × 售价倍率     rate
//
// 同一 at、同一行官方价、同一档位判定,所以毛利恒为 官方价 × (rate − ratio),
// 不会出现「按峰价收费、按谷价记成本」的假毛利。

// CostSource 该笔成本的口径,落 request_logs.cost_source 供事后核对账面。
type CostSource string

const (
	// CostFromOfficial 官方价 × 渠道系数 —— 唯一随官方价与分时自动更新、可算毛利的来源。
	CostFromOfficial CostSource = "official"
	// CostFromOffer 回落 model_offers 的手填兜底三价(74 个无官方价来源的模型走这条)。
	CostFromOffer CostSource = "offer"
	// CostUnknown 无任何成本依据(兜底三价全 0)→ 毛利不可计算,展示面必须藏起来。
	CostUnknown CostSource = "unknown"
)

// tokenCost 三价 × token 数(每百万)。与 costUsd 同式,提出单点是让单价口径只有一处。
func tokenCost(in, out, cache float64, tok translate.Usage) float64 {
	pm := func(price float64, n int) float64 { return price * float64(n) / 1e6 }
	return pm(in, tok.Prompt) + pm(out, tok.Completion) + pm(cache, tok.CacheRead)
}

// windowName 档位名,落 request_logs.price_window。
func windowName(isPeak bool) string {
	if isPeak {
		return "peak"
	}
	return "offpeak"
}

// billing 一次请求的计费定稿。
type billing struct {
	At      time.Time  // 基准时刻(见 resolveBilling 说明)
	Cost    float64    // 你付上游
	Charge  float64    // 客户付你
	Wallet  bool       // 是否扣归属客户钱包(仅 role=user 的归属)
	CostSrc CostSource // 成本口径
	IsPeak  bool       // 该时刻是否落在高峰档
	Window  string     // "peak" | "offpeak" | ""
	Warn    string     // 非致命提示(阶梯按首档计 / 系数未设 / 汇率缺失)—— 未落库,由调用方记日志
}

// resolveBilling 一次算清成本与售价。所有成功与失败路径共用此函数。
//
// at 必须由调用方传入(失败路径也一样),**不许在函数内取 now** —— 否则同一请求的不同阶段
// 会落在不同档位,产生「按峰价收费、按谷价记成本」的假毛利,且不可测。
//
// tok 为零值时结果自然为 0,失败路径不必特判。
// 官方价行只查一次,成本与售价共用同一行、同一 at、同一档位判定。
func (g *Gateway) resolveBilling(in *inboundReq, plan *engine.Plan, offer domain.OfferRead,
	at time.Time, settings domain.Settings, tok translate.Usage) billing {
	b := billing{At: at, Wallet: in.token.OwnerRole == domain.RoleUser}

	q, hasOfficial := g.officialFor(plan)

	// —— 成本:官方价 × 渠道系数,取不到则回落手填兜底价 ——
	if hasOfficial {
		ratio := g.costRatio(offer.ChannelID, plan.OfficialVendor)
		cin, cout, cc, _, err := pricing.WholesalePriceAt(q, at, settings.TZOffsetMin,
			settings.DisplayCurrency, settings.USDPerCNY, ratio)
		if err == nil {
			b.Cost = tokenCost(cin, cout, cc, tok)
			b.CostSrc = CostFromOfficial
			if q.BillingShape == domain.ShapeTiered {
				// 阶梯价按首档标量计 —— tiers 数据已知损坏,绝不消费(见 pricing/window.go)。
				b.Warn = "阶梯计价:按首档标量计"
			}
			if ratio == 1.0 {
				b.Warn = joinWarn(b.Warn, "渠道成本系数未设,按 1.0 计")
			}
		} else {
			b.Warn = err.Error() // 通常是 ErrNoExchangeRate
		}
	}
	if b.CostSrc == "" {
		b.Cost = costUsd(offer, tok)
		b.CostSrc = CostFromOffer
		if offer.InputPriceUsd == 0 && offer.OutputPriceUsd == 0 && offer.CacheReadPriceUsd == 0 {
			b.CostSrc = CostUnknown
		} else if b.Warn == "" {
			b.Warn = "未绑定官方价,成本按兜底价计"
		}
	}

	// —— 售价:与成本同源、同 at、同档位 ——
	rate := settings.PriceMultiplier
	if plan != nil && plan.RateOverride != nil {
		rate = *plan.RateOverride
	}
	if rate <= 0 {
		rate = 1.0
	}
	if hasOfficial {
		rin, rout, rc, peak, err := pricing.RetailPriceAt(q, at, settings.TZOffsetMin,
			settings.DisplayCurrency, settings.USDPerCNY, rate)
		if err == nil {
			b.Charge = tokenCost(rin, rout, rc, tok)
			if q.BillingShape == domain.ShapePeakOff {
				b.IsPeak = peak
				b.Window = windowName(peak)
			}
			return b
		}
	}
	// 无官方锚 → 回落「成本 × 倍率」,与改造前逐位一致(见 TestE2ENoOfficialFallsBackToCost)。
	b.Charge = b.Cost * rate
	return b
}

// costRatio 取该渠道对某厂商的成本系数(cost = 官方价 × ratio)。无配置 = 1.0(不折扣)。
//
// 查找键是**厂商**(plan.OfficialVendor),不是渠道自身的 provider —— 聚合中转渠道的
// provider 往往是 OpenAI/Anthropic 之类,而它实际消耗的是 DeepSeek/通义千问的官方价。
// 用 channels.provider 当键会让系数永远查不到,成本静默变 1.0 倍。
func (g *Gateway) costRatio(channelID int64, vendor domain.Provider) float64 {
	if channelID <= 0 || vendor == "" {
		return 1.0
	}
	r, err := g.st.ChannelVendorRatio(channelID, vendor)
	if err != nil || r <= 0 {
		return 1.0
	}
	return r
}

// joinWarn 拼接非致命提示(避免两处提示互相覆盖)。
func joinWarn(a, b string) string {
	if a == "" {
		return b
	}
	return a + ";" + b
}
