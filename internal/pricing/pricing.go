// Package pricing 从厂商「官方计费页面」抓取模型单价表。
//
// 设计红线(issue #3):
//   - 只采信官方域名 —— 域名白名单在传输层强制(RoundTripper),非白名单 host 直接拒绝;
//   - 每条价格留证 —— 来源 URL + 抓取时间(UTC) + 页面内容 sha256;
//   - 失败即失败 —— 抓不到/解析不出/页面改版一律显式报错,绝不回退内置表或估算值;
//   - 分时/阶梯/折扣价不得当单一价静默落库 —— 记入 Detail,InPrice/OutPrice 取「生效默认」
//     (分时取空闲价),由管理端展示时明示。
//
// 页面形态实测(2026-09-13):
//   - DeepSeek api-docs.deepseek.com/.../pricing  静态 HTML,真 <table>(rowspan/colspan),CNY,峰谷分时
//   - 通义千问 help.aliyun.com/zh/model-studio/model-pricing  国内站静态 HTML,真 <table>,CNY,阶梯
//     (国际站 www.alibabacloud.com 为美元原币;网关计价币种为人民币,故取国内站,避免汇率折算)
//   - 智谱 GLM bigmodel.cn/pricing  Vue SPA,正文无价格 → 不可抓,走手工录入(manual.go)
package pricing

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"personal-ai-gateway/internal/domain"
)

// ErrManualOnly 该 provider 无稳定可抓的官方页面,只能手工录入(智谱)。
// 触发抓取时显式返回此错误,绝不悄悄跳过或返回空集冒充成功。
var ErrManualOnly = errors.New("该厂商官方计费页为动态渲染,无法稳定抓取;请在管理台手工录入官方参考价")

// ErrNoOfficialSource 该 provider 根本没有官方单价来源(OpenAI/Anthropic/Azure/聚合中转 等)。
var ErrNoOfficialSource = errors.New("该厂商无受支持的官方单价页面")

// quote 一条解析结果(内部;原币种 / 百万 token)。
type quote struct {
	ModelName    string
	In, Out      float64
	CacheRead    float64
	CacheDerived bool
	NativeText   string
	Detail       map[string]any
}

// scraper 一个 provider 的官方来源与解析器。
type scraper struct {
	// Hosts 官方域名白名单(传输层强制)。列出该 provider 允许访问的全部 host。
	Hosts []string
	// URL 官方计费页面。
	URL string
	// ManualOnly 该 provider 无稳定可抓页面,只能手工录入。
	ManualOnly bool
	// ManualCurrency 手工录入时的默认原币种(空 = CNY)。抓取路径由 parse 返回的币种决定,
	// 与这里无关;此字段只影响 ManualOnly 条目的录入默认值(Anthropic/OpenAI 官网以美元标价)。
	ManualCurrency domain.Currency
	// parse 从页面正文解析。空 → ManualOnly。
	parse func(body []byte) ([]quote, domain.Currency, domain.BillingShape, error)
}

// scrapers provider → 官方来源。未列出的 provider 视为无官方来源。
//
// ManualOnly 条目(Hosts 为空、parse 为空)表示「有官方价、但页面不可稳定抓取,只能手工录入」:
// 它们不进抓取路径(Fetch 在 ManualOnly 分支直接返回 ErrManualOnly,不会用到 Hosts),
// 仅用于让 Supports 放行、让管理台出现手工录入入口(issue #8:Claude/GPT 官方价录不进)。
var scrapers = map[domain.Provider]scraper{
	domain.ProviderDeepSeek: {
		Hosts: []string{"api-docs.deepseek.com"},
		URL:   "https://api-docs.deepseek.com/zh-cn/quick_start/pricing",
		parse: parseDeepSeek,
	},
	domain.ProviderQwen: {
		Hosts: []string{"help.aliyun.com"},
		URL:   "https://help.aliyun.com/zh/model-studio/model-pricing",
		parse: parseQwen,
	},
	domain.ProviderZhipu: {
		Hosts:      []string{"bigmodel.cn"},
		URL:        "https://bigmodel.cn/pricing",
		ManualOnly: true,
	},
	// Anthropic / OpenAI 官方定价页为 JS 渲染(Anthropic 实测,见 PLAN.md §8),抓取大概率白做;
	// 官方价靠手工录入 + 来源 URL 留证。这是 S3「官方价 × 倍率」对 Claude/GPT 生效的前置。
	domain.ProviderAnthropic: {
		URL:            "https://www.anthropic.com/pricing",
		ManualOnly:     true,
		ManualCurrency: domain.CurrencyUSD,
	},
	domain.ProviderOpenAI: {
		URL:            "https://openai.com/api/pricing/",
		ManualOnly:     true,
		ManualCurrency: domain.CurrencyUSD,
	},
	// Moonshot 官网定价为动态渲染;Azure 按区域/部署定价无单一官方页 —— 均只能手工录入。
	domain.ProviderMoonshot: {
		URL:            "https://platform.moonshot.cn/docs/pricing",
		ManualOnly:     true,
		ManualCurrency: domain.CurrencyCNY,
	},
	domain.ProviderAzure: {
		URL:            "https://azure.microsoft.com/en-us/pricing/details/cognitive-services/openai-service/",
		ManualOnly:     true,
		ManualCurrency: domain.CurrencyUSD,
	},
}

// Supports 该 provider 是否有受支持的官方来源(可抓或可手工)。
func Supports(p domain.Provider) bool {
	_, ok := scrapers[p]
	return ok
}

// ManualOnly 该 provider 是否只能手工录入。
func ManualOnly(p domain.Provider) bool {
	s, ok := scrapers[p]
	return ok && s.ManualOnly
}

// SourceURL 该 provider 的官方计费页面(供管理端展示/跳转核对)。
func SourceURL(p domain.Provider) string {
	return scrapers[p].URL
}

// Hosts 该 provider 的官方域名白名单(供调用方构造传输层 AllowlistClient)。
func Hosts(p domain.Provider) []string {
	return scrapers[p].Hosts
}

// Fetch 抓取并解析 provider 的官方单价表。
//
// 任一环节失败(不支持 / 只能手工 / 网络失败 / 解析不出 / 校验不过)都返回错误,
// 调用方据此保持原报价不变。返回的 []domain.OfficialPriceRow 已是可落库形态。
func Fetch(ctx context.Context, client *http.Client, p domain.Provider) ([]domain.OfficialPriceRow, string, error) {
	s, ok := scrapers[p]
	if !ok {
		return nil, "", fmt.Errorf("%w: %s", ErrNoOfficialSource, p)
	}
	if s.ManualOnly {
		return nil, s.URL, fmt.Errorf("%w: %s", ErrManualOnly, p)
	}
	body, sha, err := fetchPage(ctx, client, s)
	if err != nil {
		return nil, s.URL, err
	}
	quotes, cur, shape, err := s.parse(body)
	if err != nil {
		return nil, s.URL, fmt.Errorf("解析官方页面失败(页面结构可能已变更): %w", err)
	}
	if err := validate(quotes, cur, shape); err != nil {
		return nil, s.URL, err
	}
	return toRows(p, s.URL, sha, cur, shape, quotes, time.Now().UTC()), s.URL, nil
}

// validate 解析后校验:币种/单位/数值区间/模型名/页面覆盖率。
// 校验不通过 → 判定抓取失败,不把可疑值写进报价。
func validate(quotes []quote, cur domain.Currency, shape domain.BillingShape) error {
	if !cur.Valid() {
		return fmt.Errorf("官方页面币种不可识别: %q", cur)
	}
	switch shape {
	case domain.ShapeFlat, domain.ShapePeakOff, domain.ShapeTiered, domain.ShapeDiscount:
	default:
		return fmt.Errorf("官方页面计费形态不可识别: %q", shape)
	}
	if len(quotes) == 0 {
		return fmt.Errorf("官方页面未解析出任何模型(结构可能已变更)")
	}
	for _, q := range quotes {
		if q.ModelName == "" {
			return fmt.Errorf("解析出空模型名")
		}
		for _, v := range []float64{q.In, q.Out, q.CacheRead} {
			if v < 0 || v > 1e6 {
				return fmt.Errorf("%s 单价超出合理区间: %v", q.ModelName, v)
			}
		}
		// 官方价不可能 input/output 都是 0(全免费需要人工确认,不自动落库)。
		if q.In == 0 && q.Out == 0 {
			return fmt.Errorf("%s 解析出的单价均为 0,拒绝写入(宁缺勿假)", q.ModelName)
		}
	}
	return nil
}

// toRows 把内部 quote 转成可落库的 OfficialPriceRow。
func toRows(p domain.Provider, url, sha string, cur domain.Currency, shape domain.BillingShape, quotes []quote, at time.Time) []domain.OfficialPriceRow {
	out := make([]domain.OfficialPriceRow, 0, len(quotes))
	for _, q := range quotes {
		out = append(out, domain.OfficialPriceRow{
			Provider:       p,
			ModelName:      q.ModelName,
			SourceURL:      url,
			FetchedAt:      at,
			Currency:       cur,
			BillingShape:   shape,
			InputPrice:     q.In,
			OutputPrice:    q.Out,
			CacheReadPrice: q.CacheRead,
			CacheDerived:   q.CacheDerived,
			NativeText:     q.NativeText,
			Detail:         q.Detail,
			ContentSHA256:  sha,
		})
	}
	return out
}
