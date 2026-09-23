package pricing

import (
	"time"

	"personal-ai-gateway/internal/domain"
)

// 零价供给源判定(issue #26)。
//
// 一条供给源的成本与售价**都派生自官方价**,取不到才回落到 model_offers 手填四价。
// 当两条链都拿不出任何非 0 数字时,这条供给源的 cost 与 charge 恒为 0 —— 启用它等于
// 对外免费放流量:既漏收钱(charge=0),账面毛利也失真(记成零成本)。
//
// 系统早已知道这个状态(计费侧 domain.CostUnknown、展示侧隐藏毛利列),本次把
// 「仅展示面隐藏」升级为「禁止启用」。判定必须落在**两条链都拿不到数字**的交集上:
//   - 缓存读价为 0 是合法形态,不得误伤;
//   - 生产里 74 个无官方价来源的模型走手填兜底价那一档,把它们一并拦死会打断现网选路。
//
// 判据统一为「最终算出来的成本是否可能非 0」——**复用计费同一条换算链**
// (WholesalePriceAt),不另写判据。两处各写一套正是 billing.go 记录过的漂移病根。

// OfferHasNoPrice 该供给源手填四价是否全为 0(兜底链拿不出任何数字)。
//
// 缓存写价为 0 有「按输入价回落」的合法语义,故这里判的是**四价全 0**,
// 而不是「任一价为 0」—— 只有全 0 才是「一点依据都没有」。
func OfferHasNoPrice(offer domain.OfferRead) bool {
	return offer.InputPriceUsd == 0 && offer.OutputPriceUsd == 0 &&
		offer.CacheReadPriceUsd == 0 && offer.CacheWritePriceUsd == 0
}

// ZeroPriced 判定一条供给源是否为「零价」(启用即免费放流量)。返回 (true, 成因) 时必须拒绝启用。
//
// q 为该模型绑定官方价的行;nil = 未绑定或按绑定查不到行。调用方负责查行(store 在 server/proxy 侧),
// 本函数只管判定 —— 由此保证计费、启停闸门、读接口三处同源。
//
// at 取「此刻」(与展示面 CostQuote 同口径):零价判定答的是「这条供给源现在有没有价」。
func ZeroPriced(q *domain.OfficialPriceRow, offer domain.OfferRead, settings domain.Settings, at time.Time) (bool, string) {
	if q != nil {
		// 官方价行存在:看该时刻能否换算出**非 0** 单价。
		// ratio 传 1.0 —— 系数只是乘数,不影响「是否为 0」。
		in, out, cr, cw, _, err := WholesalePriceAt(*q, at, settings.TZOffsetMin,
			settings.DisplayCurrency, settings.USDPerCNY, 1.0)
		switch {
		case err == nil && !fourZeros(in, out, cr, cw):
			return false, ""
		case err == nil:
			// 官方价行可换算但单价全 0(厂商标 0)→ 视同无官方价。
			if !OfferHasNoPrice(offer) {
				return false, ""
			}
			return true, "官方价单价为 0,且该供给源兜底四价全为 0"
		default:
			// 官方价在,但换算不出来(通常是 ErrNoExchangeRate:币种不一致且未设汇率)
			// → 回落兜底。这是预期行为:拿不出价就是拿不出价。成因里区分出这一种。
			if !OfferHasNoPrice(offer) {
				return false, ""
			}
			return true, "官方价不可用(" + err.Error() + "),且该供给源兜底四价全为 0"
		}
	}
	// 未绑定官方价(或按绑定查不到行)。
	if !OfferHasNoPrice(offer) {
		return false, ""
	}
	return true, "未绑定官方价,且该供给源兜底四价全为 0"
}

// fourZeros 一组单价是否全为 0。
func fourZeros(a, b, c, d float64) bool { return a == 0 && b == 0 && c == 0 && d == 0 }
