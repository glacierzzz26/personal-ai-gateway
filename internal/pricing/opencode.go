package pricing

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"

	"personal-ai-gateway/internal/domain"
)

// opencode.go —— opencode zen 定价页:官方价的**第二个来源**(issue #27 后 CC 之外新增)。
//
// 与 commandcode.go 的关系:CC 仍是主力锚点(一页覆盖多厂商);opencode 是**另一来源**,
// 与 CC **并存**(同一 (厂商, 模型) 各存一行,仅 source 不同,见迁移 m0014)。
//
// 页面形态(实测 2026-10-08, https://opencode.ai/docs/zen/, 真静态 HTML,3 张表):
//   - 清单表  Model | Model ID | Endpoint | AI SDK Package —— Model ID 即模型 slug;
//   - 定价表  Model | Input | Output | Cached Read | Cached Write —— 价按**显示名**给出;
//   - 弃用表  Model | Deprecation date —— 本抓取不使用。
//
// 与 CC 的三处关键差异(务必据此理解解析器):
//   ① 定价表**不带 slug**,须先解析清单表得「显示名 → slug」再回填;定价行的显示名会带
//      「(≤ 200K tokens)」「(50% off)」后缀,须剥括号后与清单表对齐;
//   ② 折扣用 `<del>` 标删除线原价(CC 用活动按钮);
//   ③ 免费行混杂付费行之间,且免费行显示名多带 "Free"(Big Pickle 等无厂商前缀的昵称模型),
//      故**免费剔除必须在厂商归属之前**,否则昵称模型会被判「未登记厂商」而误报硬错。

// openCodeURL opencode 定价页(见 opencode.go 头注)。
const openCodeURL = "https://opencode.ai/docs/zen/"

// openCodeHosts opencode 域名白名单(传输层强制)。
var openCodeHosts = []string{"opencode.ai"}

// openCodeMinRows 绝对行数地板:低于此值判定页面改版,拒绝落库(实测 2026-10-08 定价表 102 行)。
const openCodeMinRows = 40

// openCodeFloorRatio 与上次成功抓取的行数比,低于此视为半张表(与 CC 同护栏)。
const openCodeFloorRatio = 0.8

// OpenCodeURL opencode 定价页 URL(供 server 层展示/留证)。
func OpenCodeURL() string { return openCodeURL }

// OpenCodeHosts opencode 域名白名单(供 server 层构造传输层 AllowlistClient)。
func OpenCodeHosts() []string { return openCodeHosts }

// OpenCodeReport 一次 opencode 抓取的统计。免费行/分档行/折扣行必须显式上报 ——
// 它们被剔除或合并,不报就是静默丢信息。
type OpenCodeReport struct {
	SourceURL     string                  `json:"sourceUrl"`
	ContentSHA    string                  `json:"contentSha256"`
	TotalRows     int                     `json:"totalRows"` // 定价表数据行数(含免费行与各分档行)
	Priced        int                     `json:"priced"`    // 落库行数(免费剔除、分档合并后)
	PerVendor     map[domain.Provider]int `json:"perVendor"`
	FreeSkipped   []string                `json:"freeSkipped"`   // 免费行 "显示名(slug)"
	TieredSlugs   []string                `json:"tieredSlugs"`   // 合并了多档的 slug
	DiscountSlugs []string                `json:"discountSlugs"` // 含折扣(<del> 原价)的 slug
}

// ---------- 解析中间结构 ----------

// ocParsed 定价表的一行(未合并)。免费行 Free=true。
type ocParsed struct {
	Display string
	Slug    string // 由清单表 join 得到的 Model ID;免费行未 join 到时可空
	Base    string // 剥括号后缀后的显示名
	Free    bool
	// 付费行才有意义:
	Provider      domain.Provider
	BandLabel     string  // 括号后缀内容(如 "≤ 200K tokens"),""=无分档
	BandMax       float64 // 分档 token 阈值(K→×1000),0=未知
	In, Out       float64
	CacheRead     float64
	CacheWrite    float64
	CacheWriteOff bool // 缓存写列缺失(`-`)
	HasOrig       bool // 含 <del> 删除线原价(折扣行)
	OrigIn        float64
	OrigOut       float64
	OrigCR        float64
	OrigCW        float64
}

var (
	// ocParenRe 显示名尾部的括号后缀(分档 "… (≤ 200K tokens)" / 折扣 "… (50% off)")。
	ocParenRe = regexp.MustCompile(`^(.*?)\s*\(([^)]*)\)\s*$`)
	// ocTokensRe 从分档标签解析 token 阈值("200K tokens" / "272k tokens")。
	ocTokensRe = regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?)\s*([km])?\s*tokens`)
)

// ocSplitSuffix 拆出显示名主体与括号后缀。
func ocSplitSuffix(display string) (base, label string) {
	if m := ocParenRe.FindStringSubmatch(display); m != nil {
		return strings.TrimSpace(m[1]), strings.TrimSpace(m[2])
	}
	return display, ""
}

// ocBandThreshold 从分档标签解析 token 阈值("≤ 200K tokens" → 200000)。解析不出返回 0。
func ocBandThreshold(label string) float64 {
	m := ocTokensRe.FindStringSubmatch(strings.ToLower(label))
	if m == nil {
		return 0
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0
	}
	switch m[2] {
	case "k":
		v *= 1000
	case "m":
		v *= 1_000_000
	}
	return v
}

// ocIsFreeCell 该价格单元格是否显式 "Free"(归一后)。
func ocIsFreeCell(s string) bool {
	switch strings.ToLower(normalizeText(s)) {
	case "free", "免费":
		return true
	}
	return false
}

// parseOpenCode 解析 opencode 定价页正文,返回定价表全部数据行(**含免费行**)。
//
// 任一结构异常(找不到表/清单表/列数对不上/付费行归不出厂商或 join 不到 slug)一律硬错,
// 绝不回退估算值或静默跳过 —— 「失败即失败」是 pricing 包的红线。
// 免费行例外:免费行允许不 join 到 slug(仍会记录,由调用方上报)。
func parseOpenCode(body []byte) ([]ocParsed, error) {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	// ① 清单表:显示名 → slug。
	mtab, mlabels, err := ccFindTableHaving(doc, "Model", "Model ID")
	if err != nil {
		return nil, fmt.Errorf("opencode 清单表: %w", err)
	}
	iMModel, iMSlug := ccIndex(mlabels, "Model"), ccIndex(mlabels, "Model ID")
	slugByName := map[string]string{}
	for _, tr := range ccRowNodes(mtab)[1:] {
		cells := ccCellNodes(tr)
		if len(cells) != len(mlabels) {
			return nil, fmt.Errorf("opencode 清单表行列数为 %d,与表头 %d 不一致(页面结构可能已变更)", len(cells), len(mlabels))
		}
		name := normalizeText(nodeText(cells[iMModel]))
		slug := normalizeText(nodeText(cells[iMSlug]))
		if name == "" || slug == "" {
			return nil, fmt.Errorf("opencode 清单表存在空显示名或空 Model ID(页面结构可能已变更)")
		}
		slugByName[name] = slug
	}
	if len(slugByName) == 0 {
		return nil, fmt.Errorf("opencode 清单表未解析出任何模型(页面结构可能已变更)")
	}

	// ② 定价表。
	tbl, labels, err := ccFindTableHaving(doc, "Model", "Input", "Output")
	if err != nil {
		return nil, fmt.Errorf("opencode 定价表: %w", err)
	}
	iModel := ccIndex(labels, "Model")
	iInput := ccIndex(labels, "Input")
	iOutput := ccIndex(labels, "Output")
	iCacheRead := ccIndex(labels, "Cached Read")
	iCacheWrite := ccIndex(labels, "Cached Write") // 允许缺失
	width := len(labels)

	trs := ccRowNodes(tbl)
	if len(trs) < 2 {
		return nil, fmt.Errorf("opencode 定价表无数据行(页面结构可能已变更)")
	}
	var out []ocParsed
	var unmapped []string
	for _, tr := range trs[1:] {
		cells := ccCellNodes(tr)
		if len(cells) != width {
			return nil, fmt.Errorf("opencode 定价表第 %d 数据行列数为 %d,与表头 %d 不一致(页面结构可能已变更)",
				len(out)+1, len(cells), width)
		}
		r, err := ocParseRow(cells, iModel, iInput, iOutput, iCacheRead, iCacheWrite, slugByName)
		if err != nil {
			return nil, err
		}
		if !r.Free {
			if r.Provider == "" {
				unmapped = append(unmapped, r.Display)
				continue // 汇总为硬错
			}
		}
		out = append(out, r)
	}
	if len(unmapped) > 0 {
		// 不得静默落库:未登记的厂商前缀必须补映射表,而不是丢掉或猜一个。
		return nil, fmt.Errorf("opencode 出现未登记厂商前缀:%s(须补 ccVendorPrefixes,不得静默落库)",
			strings.Join(unmapped, "、"))
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("opencode 定价表未解析出任何数据行(页面结构可能已变更)")
	}
	return out, nil
}

// ocParseRow 解析一行。cells 已按表头列对齐。slugByName 来自清单表。
func ocParseRow(cells []*html.Node, iModel, iInput, iOutput, iCacheRead, iCacheWrite int, slugByName map[string]string) (ocParsed, error) {
	var r ocParsed
	display := normalizeText(nodeText(cells[iModel]))
	if display == "" {
		return r, fmt.Errorf("opencode 定价行缺少模型名(页面结构可能已变更)")
	}
	r.Display = display
	base, label := ocSplitSuffix(display)
	r.Base, r.BandLabel = base, label
	r.BandMax = ocBandThreshold(label)

	// slug:优先用「剥括号后的显示名」在清单表 join;免费行 join 不到可容忍。
	if slug, ok := slugByName[base]; ok {
		r.Slug = slug
	}

	inC := ccParsePrice(cells[iInput])
	outC := ccParsePrice(cells[iOutput])
	crC := ccParsePrice(cells[iCacheRead])
	var cwC ccPrice
	if iCacheWrite >= 0 {
		cwC = ccParsePrice(cells[iCacheWrite])
	}

	// 免费判定:输入与输出**都**为 Free 才算整行免费(opencode 页把全部免费的昵称模型
	// 如此标注)。仅单侧 Free(如 Jev 1.13 输出 Free、输入有价)不算免费 —— 保留该行,
	// 免费侧按 0 计,否则会误把有输入价的模型整行丢掉。
	inFree, outFree := ocIsFreeCell(inC.Current), ocIsFreeCell(outC.Current)
	if inFree && outFree {
		r.Free = true
		return r, nil
	}

	if r.Slug == "" {
		return r, fmt.Errorf("opencode 付费行 %q 未能在清单表 join 到 Model ID(页面结构可能已变更)", display)
	}
	if p, brand := ccVendorOf(base); p != "" {
		r.Provider = p
		_ = brand
	} else {
		return r, nil // 由调用方汇总为「未归属」硬错
	}

	in, _, err := ccMoney(inC.Current)
	if err != nil {
		return r, fmt.Errorf("%s 输入价解析失败: %w", display, err)
	}
	outV, _, err := ccMoney(outC.Current)
	if err != nil {
		return r, fmt.Errorf("%s 输出价解析失败: %w", display, err)
	}
	cr, _, err := ccMoney(crC.Current)
	if err != nil {
		return r, fmt.Errorf("%s 缓存读价解析失败: %w", display, err)
	}
	r.In, r.Out, r.CacheRead = in, outV, cr
	if iCacheWrite < 0 || cwC.Current == "" || cwC.Current == "—" || cwC.Current == "-" {
		r.CacheWriteOff = true
	} else {
		cw, _, err := ccMoney(cwC.Current)
		if err != nil {
			return r, fmt.Errorf("%s 缓存写价解析失败: %w", display, err)
		}
		r.CacheWrite = cw
	}

	// 折扣:任一价格列含 <del> 原价即折扣行。
	if v, ok := ocOriginal(inC); ok {
		r.HasOrig, r.OrigIn = true, v
	}
	if v, ok := ocOriginal(outC); ok {
		r.HasOrig, r.OrigOut = true, v
	}
	if v, ok := ocOriginal(crC); ok {
		r.HasOrig, r.OrigCR = true, v
	}
	if v, ok := ocOriginal(cwC); ok {
		r.HasOrig, r.OrigCW = true, v
	}
	return r, nil
}

// ocOriginal 取 <del>/<s> 删除线原价(存在且可解析时)。
func ocOriginal(c ccPrice) (float64, bool) {
	if c.Original == "" || c.Original == "—" {
		return 0, false
	}
	v, free, err := ccMoney(c.Original)
	if err != nil || free {
		return 0, false
	}
	return v, true
}

// ---------- 合并 + 落库 ----------

// ocMerged 同一 slug 合并后的落库行。
type ocMerged struct {
	Slug, Base string
	Provider   domain.Provider
	Shape      domain.BillingShape
	In, Out    float64
	CacheRead  float64
	CacheWrite float64
	CacheWriteOff bool
	Bands      []ocParsed
	Discount   bool
}

// quote 转成通用 quote 复用 validate 的区间/非空/非全零校验。
func (m ocMerged) quote() quote {
	return quote{ModelName: m.Slug, In: m.In, Out: m.Out, CacheRead: m.CacheRead}
}

// ocMerge 把同 slug 的多行(分档)合并为一行。顺序:首个出现即基准档。
func ocMerge(rows []ocParsed) []ocMerged {
	idx := map[string]int{}
	var out []ocMerged
	for _, r := range rows {
		if r.Free {
			continue
		}
		if i, ok := idx[r.Slug]; ok {
			out[i].Bands = append(out[i].Bands, r)
			continue
		}
		idx[r.Slug] = len(out)
		out = append(out, ocMerged{
			Slug: r.Slug, Base: r.Base, Provider: r.Provider,
			In: r.In, Out: r.Out, CacheRead: r.CacheRead, CacheWrite: r.CacheWrite,
			CacheWriteOff: r.CacheWriteOff, Discount: r.HasOrig,
			Bands: []ocParsed{r},
		})
	}
	for i := range out {
		// 分档按阈值升序(基准档在前;无阈值视为最大),使标量与分档明细统一从 Bands[0] 取。
		sort.SliceStable(out[i].Bands, func(a, b int) bool {
			return ocBandKey(out[i].Bands[a]) < ocBandKey(out[i].Bands[b])
		})
		base := out[i].Bands[0]
		out[i].Base = base.Base
		out[i].In, out[i].Out, out[i].CacheRead, out[i].CacheWrite = base.In, base.Out, base.CacheRead, base.CacheWrite
		out[i].CacheWriteOff = base.CacheWriteOff
		switch {
		case len(out[i].Bands) > 1:
			out[i].Shape = domain.ShapeTiered
		case out[i].Discount:
			out[i].Shape = domain.ShapeDiscount
		default:
			out[i].Shape = domain.ShapeFlat
		}
	}
	return out
}

// ocBandKey 分档排序键:阈值>0 用阈值,否则视为最大(排在最后)。
func ocBandKey(b ocParsed) float64 {
	if b.BandMax > 0 {
		return b.BandMax
	}
	return math.MaxFloat64
}

// ocDetail 组装 Detail。分档明细写 Detail["ocTiers"],**绝不写 Detail["tiers"]**
// (该键在旧库已坏,window.go 明令禁读)。
func ocDetail(m ocMerged) map[string]any {
	d := map[string]any{
		"ocSlug":      m.Slug,
		"displayName": m.Base,
		"source":      string(domain.PriceSourceOpenCode),
		"note":        "opencode zen 定价页锚点价",
	}
	if m.CacheWriteOff {
		d["cacheWriteAbsent"] = true
	} else {
		d["cacheWrite"] = m.CacheWrite
	}
	if len(m.Bands) > 1 {
		tiers := make([]map[string]any, 0, len(m.Bands))
		for _, b := range m.Bands {
			t := map[string]any{"label": b.BandLabel, "in": b.In, "out": b.Out, "cacheRead": b.CacheRead}
			if b.BandMax > 0 {
				t["maxTokens"] = b.BandMax
			}
			if !b.CacheWriteOff {
				t["cacheWrite"] = b.CacheWrite
			}
			tiers = append(tiers, t)
		}
		d["tierBands"] = len(m.Bands)
		d["ocTiers"] = tiers
		d["tiersNote"] = "opencode 分档按 use 上下文长度计价;标量取基准档(阈值最小档)"
	}
	if m.Discount {
		first := m.Bands[0]
		deal := map[string]any{
			"original": map[string]any{"in": first.OrigIn, "out": first.OrigOut, "cacheRead": first.OrigCR},
		}
		if first.OrigIn > 0 && first.In > 0 {
			deal["percent"] = int(math.Round((1 - first.In/first.OrigIn) * 100))
		}
		d["deal"] = deal
	}
	return d
}

// ocNativeText 重建人读文本(管理台逐字核对 opencode 页面用)。
func ocNativeText(m ocMerged) string {
	var b strings.Builder
	fmt.Fprintf(&b, "输入 %s | 输出 %s | 缓存读 %s", ccMoneyText(m.In), ccMoneyText(m.Out), ccMoneyText(m.CacheRead))
	if !m.CacheWriteOff {
		fmt.Fprintf(&b, " | 缓存写 %s", ccMoneyText(m.CacheWrite))
	}
	if m.Discount {
		first := m.Bands[0]
		fmt.Fprintf(&b, "; 原价 输入 %s", ccMoneyText(first.OrigIn))
	}
	if len(m.Bands) > 1 {
		fmt.Fprintf(&b, "; %d 档(基准 %s)", len(m.Bands), m.Bands[0].BandLabel)
	}
	return b.String()
}

// FetchOpenCode 抓取并解析 opencode 定价页,返回可落库的官方价行(**多厂商**,source=opencode)与统计。
//
// previousRows 为上次成功抓取的落库行数(<=0 表示首次,跳过比例检查),用于行数骤降护栏。
// 失败即失败:网络/解析/校验/行数骤降/厂商归属缺失 任一不满足即返回错误,不落可疑值。
func FetchOpenCode(ctx context.Context, client *http.Client, previousRows int) ([]domain.OfficialPriceRow, OpenCodeReport, error) {
	rep := OpenCodeReport{SourceURL: openCodeURL, PerVendor: map[domain.Provider]int{}}

	body, sha, err := fetchPageURL(ctx, client, openCodeURL, openCodeHosts)
	if err != nil {
		return nil, rep, err
	}
	rep.ContentSHA = sha

	rows, err := parseOpenCode(body)
	if err != nil {
		return nil, rep, fmt.Errorf("解析 opencode 定价页失败(页面结构可能已变更): %w", err)
	}
	rep.TotalRows = len(rows)
	if len(rows) < openCodeMinRows {
		return nil, rep, fmt.Errorf("opencode 页面仅解析出 %d 行(地板 %d),判定页面改版,拒绝落库",
			len(rows), openCodeMinRows)
	}

	// 免费行排除在落库之外,但必须上报(否则是静默丢数据)。
	// 排除必须在 validate 之前:validate 会因 in/out 全 0 拒绝**整批**。
	priced := make([]ocParsed, 0, len(rows))
	for _, r := range rows {
		if r.Free {
			rep.FreeSkipped = append(rep.FreeSkipped, fmt.Sprintf("%s(%s)", r.Display, r.Slug))
			continue
		}
		priced = append(priced, r)
	}

	merged := ocMerge(priced)
	rep.Priced = len(merged)
	if previousRows > 0 && float64(len(merged)) < openCodeFloorRatio*float64(previousRows) {
		return nil, rep, fmt.Errorf("opencode 页面行数骤降:%d 行 < 上次 %d 行的 %.0f%%,拒绝落库",
			len(merged), previousRows, openCodeFloorRatio*100)
	}

	// 按 (厂商, 形态) 分组复用 validate 的区间/非空/非全零校验(Currency 恒为 USD)。
	type gkey struct {
		p domain.Provider
		s domain.BillingShape
	}
	groups := map[gkey][]ocMerged{}
	var order []gkey
	for _, m := range merged {
		if m.Shape == domain.ShapeTiered {
			rep.TieredSlugs = append(rep.TieredSlugs, m.Slug)
		}
		if m.Discount {
			rep.DiscountSlugs = append(rep.DiscountSlugs, m.Slug)
		}
		k := gkey{m.Provider, m.Shape}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], m)
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].p != order[j].p {
			return order[i].p < order[j].p
		}
		return order[i].s < order[j].s
	})

	at := time.Now().UTC()
	out := make([]domain.OfficialPriceRow, 0, len(merged))
	for _, k := range order {
		g := groups[k]
		quotes := make([]quote, 0, len(g))
		for _, m := range g {
			quotes = append(quotes, m.quote())
		}
		if err := validate(quotes, domain.CurrencyUSD, k.s); err != nil {
			return nil, rep, fmt.Errorf("opencode 解析结果校验失败: %w", err)
		}
		for _, m := range g {
			out = append(out, domain.OfficialPriceRow{
				Provider:        k.p,
				ModelName:       m.Slug, // 落 slug(与 canonicalModelKey 一致,便于回填绑定)
				Source:          domain.PriceSourceOpenCode,
				SourceURL:       openCodeURL,
				FetchedAt:       at,
				Currency:        domain.CurrencyUSD,
				BillingShape:    k.s,
				InputPrice:      m.In,
				OutputPrice:     m.Out,
				CacheReadPrice:  m.CacheRead,
				CacheWritePrice: m.CacheWrite, // 列缺失(`-`)时为 0 = 无依据,计费回落 input 价
				NativeText:      ocNativeText(m),
				Detail:          ocDetail(m),
				ContentSHA256:   sha,
			})
			rep.PerVendor[k.p]++
		}
	}
	return out, rep, nil
}
