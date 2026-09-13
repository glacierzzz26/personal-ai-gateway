package pricing

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/net/html"
	"personal-ai-gateway/internal/domain"
)

// cacheHitRatio 通义官方正文声明:上下文缓存「命中」按标准输入单价的 10% 计费。
// 官方表格没有独立缓存价列,故缓存价是**推导值**,必须 CacheDerived=true 标注。
const cacheHitRatio = 0.10

var (
	// 中文/国际 tab 页里「每百萬Token」「每百万Token」两种写法。
	reInputHeader = regexp.MustCompile(`輸入單價|输入单价`)
	reModelIDHead = regexp.MustCompile(`模型\s*ID|模型ID`)
	// 区间描述形如 "0<Token≤1M" / "32K<Token≤128K"。
	reTierBound = regexp.MustCompile(`([0-9.]+)\s*([KkMm]?)`)
)

// parseQwen 解析阿里云百炼「模型计费」页。
//
// 页面形态(实测 2026-09-13):静态 HTML,真 <table>(251 个),列头形如
// 「模型 ID / 服務部署範圍 / 模式 / 單次請求的輸入Token數 / 輸入單價（每百萬Token） / 輸出單價（每百萬Token）」。
// 币种美元($),按「单次请求输入 token 区间」阶梯计价;无缓存价列(由正文规则推导)。
//
// 表格按地区分块(國際/北京/…),同一模型可出现多次。取首个出现的档位(國際优先,页面顺序),
// 并把全部档位记入 Detail。
func parseQwen(body []byte) ([]quote, domain.Currency, domain.BillingShape, error) {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, "", "", err
	}
	// 单位/缓存规则必须在正文出现,否则说明页面改版,直接失败。
	if !bytes.Contains(body, []byte("百萬Token")) && !bytes.Contains(body, []byte("百万Token")) {
		return nil, "", "", fmt.Errorf("页面未出现「每百萬Token」计价单位说明")
	}
	tables, _ := parseTables(doc)

	// 收集所有「模型 ID」表,按出现顺序遍历。
	type hit struct {
		model    string
		tierText string
		in, out  float64
	}
	var hits []hit
	seenShape := false
	for _, t := range tables {
		if !hasHeader(t, reModelIDHead) || !hasHeader(t, reInputHeader) {
			continue
		}
		colModel, colTier, colIn, colOut := locateCols(t)
		if colModel < 0 || colIn < 0 || colOut < 0 {
			continue
		}
		for _, row := range t.grid[1:] {
			name := cellAt(row, colModel)
			name = cleanModelName(name)
			if name == "" {
				continue
			}
			inV, _, ok1 := parseMoney(cellAt(row, colIn))
			outV, _, ok2 := parseMoney(cellAt(row, colOut))
			if !ok1 || !ok2 {
				continue
			}
			tier := cellAt(row, colTier)
			if colTier < 0 {
				tier = ""
			}
			if strings.Contains(tier, "<") || strings.Contains(tier, "≤") || strings.Contains(tier, "≤") {
				seenShape = true
			}
			hits = append(hits, hit{model: name, tierText: normalizeText(tier), in: inV, out: outV})
		}
	}
	if len(hits) == 0 {
		return nil, "", "", fmt.Errorf("未找到含「模型 ID / 輸入單價」的计费表")
	}

	// 按模型归并:首个出现的档位为「生效默认」,其余进 tiers。
	type agg struct {
		quote
		tiers    []map[string]any
		currency domain.Currency
	}
	order := []string{}
	byModel := map[string]*agg{}
	for _, h := range hits {
		a, ok := byModel[h.model]
		if !ok {
			a = &agg{currency: domain.CurrencyUSD}
			a.ModelName = h.model
			a.In, a.Out = h.in, h.out
			a.CacheRead = round6(h.in * cacheHitRatio)
			a.CacheDerived = true
			a.currency = domain.CurrencyUSD
			byModel[h.model] = a
			order = append(order, h.model)
		}
		a.tiers = append(a.tiers, map[string]any{
			"range": h.tierText, "in": h.in, "out": h.out,
		})
	}

	shape := domain.ShapeFlat
	if seenShape {
		shape = domain.ShapeTiered
	}
	out := make([]quote, 0, len(order))
	for _, name := range order {
		a := byModel[name]
		native := fmt.Sprintf("$%v / $%v (每百萬Token)", a.In, a.Out)
		if len(a.tiers) > 1 {
			native = fmt.Sprintf("阶梯计价,共 %d 档;首档 $%v / $%v", len(a.tiers), a.In, a.Out)
		}
		detail := map[string]any{
			"tiers":        a.tiers,
			"cacheRule":    "缓存命中按标准输入价 10% 计费(官方正文),缓存价为本系统按该规则推导,非官方列",
			"cacheDerived": true,
		}
		if len(a.tiers) > 1 {
			detail["effectiveDefault"] = "first-tier"
			detail["note"] = "按单次请求输入 token 区间阶梯计价;网关按单一价计费,应用时取首档"
		}
		out = append(out, quote{
			ModelName:    a.ModelName,
			In:           a.In,
			Out:          a.Out,
			CacheRead:    a.CacheRead,
			CacheDerived: true,
			NativeText:   native,
			Detail:       detail,
		})
	}
	return out, domain.CurrencyUSD, shape, nil
}

// locateCols 按列头文本定位「模型/区间/输入价/输出价」列。
func locateCols(t table) (model, tier, in, out int) {
	model, tier, in, out = -1, -1, -1, -1
	for c, v := range t.grid[0] {
		switch {
		case model < 0 && reModelIDHead.MatchString(v):
			model = c
		case tier < 0 && (strings.Contains(v, "輸入Token數") || strings.Contains(v, "输入Token数") || strings.Contains(v, "區間") || strings.Contains(v, "区间")):
			tier = c
		case in < 0 && reInputHeader.MatchString(v):
			in = c
		case out < 0 && (strings.Contains(v, "輸出單價") || strings.Contains(v, "输出单价")):
			out = c
		}
	}
	return
}

func hasHeader(t table, re *regexp.Regexp) bool {
	if len(t.grid) == 0 {
		return false
	}
	for _, v := range t.grid[0] {
		if re.MatchString(v) {
			return true
		}
	}
	return false
}

func cellAt(row []string, i int) string {
	if i < 0 || i >= len(row) {
		return ""
	}
	return row[i]
}

// cleanModelName 从模型 ID 单元格取纯模型名:去掉「上下文緩衝 享有折扣」等附注与 tag 标记。
func cleanModelName(s string) string {
	s = normalizeText(s)
	for _, cut := range []string{"上下文", "上下文緩衝", "上下文缓存", "當前", "当前", "（", "(", " ", "　"} {
		if i := strings.Index(s, cut); i > 0 {
			s = s[:i]
		}
	}
	return strings.TrimSpace(s)
}

// round6 保留 6 位小数,避免浮点尾数进入留证文本。
func round6(v float64) float64 {
	return float64(int64(v*1e6+0.5)) / 1e6
}
