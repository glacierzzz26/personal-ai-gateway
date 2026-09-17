package pricing

import (
	"errors"
	"fmt"

	"personal-ai-gateway/internal/domain"
)

// 官方价(厂商官网挂牌,原币种)与本站价(卖给客户,计价币种)之间的换算。
//
// 定价模型(见 PLAN.md §2):本站价 = 官方价 × 倍率。官方价是「对外价格的锚」,
// 而 model_offers 里的价是**成本**(你付上游的钱),两者不是一回事。
//
// 为什么放这里:计费链路(proxy)与展示链路(server)都要用同一套折算,
// 两处各写一份必然漂移。pricing 只依赖 domain,放在这里不会引入环。

// Round6 四舍五入到 6 位,避免浮点尾数进库。
func Round6(v float64) float64 { return float64(int64(v*1e6+0.5)) / 1e6 }

// ErrNoExchangeRate 币种不一致且未设汇率。调用方应据此提示补汇率,而不是臆造 1:1。
var ErrNoExchangeRate = errors.New("币种不一致且未设置 USD/CNY 汇率")

// Convert 官方原币价 → 计价币种金额(每百万 token)。
//
// 原币种与计价币种一致时原样返回(不需要汇率);不一致时按 usdPerCNY 折算,
// usdPerCNY <= 0 则返回 ErrNoExchangeRate(不臆造汇率)。
// usdPerCNY 语义 = 1 元人民币折合的美元数(如 0.139):CNY→USD 乘,USD→CNY 除。
func Convert(price float64, from, to domain.Currency, usdPerCNY float64) (float64, error) {
	if !to.Valid() {
		return 0, errors.New("计价币种未设置(应为 CNY 或 USD)")
	}
	if from == to {
		return price, nil
	}
	if usdPerCNY <= 0 {
		return 0, ErrNoExchangeRate
	}
	switch {
	case from == domain.CurrencyCNY && to == domain.CurrencyUSD:
		return Round6(price * usdPerCNY), nil
	case from == domain.CurrencyUSD && to == domain.CurrencyCNY:
		return Round6(price / usdPerCNY), nil
	default:
		return 0, fmt.Errorf("未知币种: %s→%s", from, to)
	}
}

// 注:改造前这里还有一个 RetailPrice(不带 at 的旧签名),计费与展示两处都调它。
// 分时之后「按哪个时刻取价」成了必须显式回答的问题,两个调用点已各自收敛:
//   - 计费 → RetailPriceAt(q, at, ...)(at 由请求入口取一次,见 proxy.resolveBilling)
//   - 展示 → 显式分档展示,不再按时刻取单值(见 server/userModelsList 的峰谷并列)
// 旧签名因此失去调用者被删除,避免有人图省事又调回「按当前时钟取价」。
