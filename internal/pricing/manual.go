package pricing

import (
	"fmt"
	"strings"
	"time"

	"personal-ai-gateway/internal/domain"
)

// BuildManual 把管理台手工录入的官方价构造成可落库的 OfficialPriceRow。
//
// 用于官方页面为动态渲染、无法稳定抓取的厂商(智谱)。与管理台自动抓取共用同一张
// official_prices 表与同一套「来源 URL + 抓取时间 + 原币种 + 原始文本」留证字段,
// 区别仅在于 FetchedAt 是录入时刻、ContentSHA256 为空(无页面指纹)。
func BuildManual(in domain.OfficialPriceInput) (domain.OfficialPriceRow, error) {
	if in.Provider == "" {
		return domain.OfficialPriceRow{}, fmt.Errorf("provider 必填")
	}
	if !Supports(in.Provider) {
		return domain.OfficialPriceRow{}, fmt.Errorf("%w: %s", ErrNoOfficialSource, in.Provider)
	}
	in.ModelName = strings.TrimSpace(in.ModelName)
	if in.ModelName == "" {
		return domain.OfficialPriceRow{}, fmt.Errorf("modelName 必填")
	}
	in.SourceURL = strings.TrimSpace(in.SourceURL)
	if in.SourceURL == "" {
		return domain.OfficialPriceRow{}, fmt.Errorf("sourceUrl 必填(手工录入也必须留来源,否则无法核对)")
	}
	if !strings.HasPrefix(in.SourceURL, "http://") && !strings.HasPrefix(in.SourceURL, "https://") {
		return domain.OfficialPriceRow{}, fmt.Errorf("sourceUrl 必须是完整 http(s) URL")
	}
	if in.Currency == "" {
		in.Currency = domain.CurrencyCNY // 智谱等国内厂商默认人民币
	}
	if !in.Currency.Valid() {
		return domain.OfficialPriceRow{}, fmt.Errorf("currency 必须是 CNY 或 USD")
	}
	if in.InputPrice < 0 || in.OutputPrice < 0 || in.CacheReadPrice < 0 {
		return domain.OfficialPriceRow{}, fmt.Errorf("单价不能为负")
	}
	if in.InputPrice == 0 && in.OutputPrice == 0 {
		return domain.OfficialPriceRow{}, fmt.Errorf("输入与输出单价不能同时为 0")
	}
	shape := domain.ShapeFlat
	if in.Note != "" {
		shape = domain.ShapeDiscount // 手工录入常用于「限时折扣」等带说明的形态
	}
	return domain.OfficialPriceRow{
		Provider:       in.Provider,
		ModelName:      in.ModelName,
		SourceURL:      in.SourceURL,
		FetchedAt:      time.Now().UTC(),
		Currency:       in.Currency,
		BillingShape:   shape,
		InputPrice:     in.InputPrice,
		OutputPrice:    in.OutputPrice,
		CacheReadPrice: in.CacheReadPrice,
		NativeText:     strings.TrimSpace(in.NativeText),
		Detail:         map[string]any{"manual": true, "note": in.Note},
		Note:           in.Note,
	}, nil
}
