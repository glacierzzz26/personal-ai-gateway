package pricing

import (
	"errors"
	"fmt"
	"time"

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

// RetailPrice 本站价 = 官方价 × 倍率(计价币种,每百万 token)。
//
// 倍率 <= 0 视为未设,回落 1.0(不加价)。官方价币种与计价币种不一致且未设汇率时,
// 按 ErrNoExchangeRate 失败 —— 调用方应把「没有可用官方价」当作可降级的信号。
//
// 取价按 at 时刻的形态生效(峰谷分时会选峰/谷价,见 window.go)。at 为零值或
// 官方价非分时形态时,行为与改造前的单一价口径一致。
func RetailPrice(q domain.OfficialPriceRow, at time.Time, tzOffsetMin int,
	display domain.Currency, usdPerCNY, multiplier float64) (in, out, cache float64, err error) {
	in, out, cache, _, err = RetailPriceAt(q, at, tzOffsetMin, display, usdPerCNY, multiplier)
	return in, out, cache, err
}
