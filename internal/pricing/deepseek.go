package pricing

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"personal-ai-gateway/internal/domain"
)

// parseDeepSeek 解析 DeepSeek「模型 & 价格」页。
//
// 页面形态(实测 2026-09-13):Docusaurus 静态 HTML,价格区是真 <table>,
// 属性行式布局(模型为列,属性为行),价格 6 行为「缓存命中/未命中 × 输入」「输出」
// 各再分「空闲时段 / 高峰时段」。币种人民币(元),单位百万 tokens。
//
// 生效默认取「空闲时段」价(高峰为空闲的 2 倍,且非工作时段占多数);
// 两档明细全部记入 Detail,由管理端明示「该来源为峰谷分时」。
func parseDeepSeek(body []byte) ([]quote, domain.Currency, domain.BillingShape, error) {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, "", "", err
	}
	if !bytes.Contains(body, []byte("百万")) {
		return nil, "", "", fmt.Errorf("页面未出现「百万」计价单位说明")
	}
	tables, _ := parseTables(doc)
	tbl, ok := findByHeaderCell(tables, "模型")
	if !ok {
		return nil, "", "", fmt.Errorf("未找到含「模型」表头的价格表")
	}

	header := tbl.grid[0]
	// 模型列 = 表头行里非「模型」且非空的列(表头首格 colspan=3 占位即标签列)。
	var modelCols []int
	var modelNames []string
	for c, v := range header {
		if v == "" || v == "模型" {
			continue
		}
		modelCols = append(modelCols, c)
		modelNames = append(modelNames, stripFootnote(v))
	}
	if len(modelCols) == 0 {
		return nil, "", "", fmt.Errorf("未从表头解析出模型列")
	}
	labelEnd := modelCols[0]

	type priceSet struct {
		inCache, in, out float64
		currency         domain.Currency
	}
	// 每个模型列一份:off(空闲)/ peak(高峰)。
	off := make([]priceSet, len(modelCols))
	peak := make([]priceSet, len(modelCols))
	nativeOff := make([]string, len(modelCols))
	nativePeak := make([]string, len(modelCols))
	found := make([]bool, len(modelCols))

	for _, row := range tbl.grid {
		if len(row) <= labelEnd {
			continue
		}
		label := strings.Join(row[:labelEnd], " ")
		isOff := strings.Contains(label, "空闲时段")
		isPeak := strings.Contains(label, "高峰时段")
		if !isOff && !isPeak {
			continue
		}
		var kind string
		switch {
		case strings.Contains(label, "输出"):
			kind = "out"
		case strings.Contains(label, "缓存命中"):
			kind = "cache"
		case strings.Contains(label, "未命中") || strings.Contains(label, "输入"):
			kind = "in"
		default:
			continue
		}
		for i, c := range modelCols {
			if c >= len(row) {
				continue
			}
			v, cur, ok := parseMoney(row[c])
			if !ok {
				continue
			}
			dst := &off[i]
			if isPeak {
				dst = &peak[i]
			}
			dst.currency = cur
			switch kind {
			case "in":
				dst.in = v
			case "out":
				dst.out = v
			case "cache":
				dst.inCache = v
			}
			found[i] = true
			// 原始文本留证
			if isPeak {
				nativePeak[i] = strings.TrimSpace(nativePeak[i] + " " + row[c])
			} else {
				nativeOff[i] = strings.TrimSpace(nativeOff[i] + " " + row[c])
			}
		}
	}
	// 逐模型列收集。DeepSeek 价格恒为 CNY(单元带「元」);校验兜底。
	var out []quote
	for i, name := range modelNames {
		if !found[i] || (off[i].in == 0 && off[i].out == 0) {
			continue
		}
		cur := off[i].currency
		if cur == "" {
			cur = peak[i].currency
		}
		if cur == "" {
			cur = domain.CurrencyCNY
		}
		if cur != domain.CurrencyCNY {
			// DeepSeek 价格带「元」;非 CNY 说明解析错位,宁缺勿假。
			return nil, "", "", fmt.Errorf("%s 解析出非人民币单价(%s),页面结构可能已变更", name, cur)
		}
		out = append(out, quote{
			ModelName:  name,
			In:         off[i].in,
			Out:        off[i].out,
			CacheRead:  off[i].inCache,
			NativeText: fmt.Sprintf("空闲 %s | 高峰 %s", nativeOff[i], nativePeak[i]),
			Detail: map[string]any{
				"peakHours": legacyDeepSeekPeakHours,
				"peak": map[string]any{
					"in": peak[i].in, "out": peak[i].out, "cacheRead": peak[i].inCache,
				},
				"offpeak": map[string]any{
					"in": off[i].in, "out": off[i].out, "cacheRead": off[i].inCache,
				},
				"effectiveDefault": "offpeak",
				"note":             "峰谷分时:计费按请求时刻自动选峰/谷价(成本与售价同步浮动)",
				// windows 是 peakHours 的机器可读版本,供计费分时选价(见 window.go)。
				// peakHours 中文串保留不动:展示面仍用它,且旧库行的迁移靠它精确匹配。
				"windows": windowsToAny(legacyDeepSeekWindows()),
			},
		})
	}
	return out, domain.CurrencyCNY, domain.ShapePeakOff, nil
}

// findByHeaderCell 找到含指定表头文本的表。
func findByHeaderCell(tables []table, header string) (table, bool) {
	for _, t := range tables {
		if len(t.grid) == 0 {
			continue
		}
		for _, v := range t.grid[0] {
			if v == header {
				return t, true
			}
		}
	}
	return table{}, false
}

// stripFootnote 去掉模型名后附的脚注标记,如 "deepseek-flash(1)" → "deepseek-flash"。
func stripFootnote(s string) string {
	s = strings.TrimSpace(s)
	for {
		i := strings.LastIndex(s, "(")
		if i < 0 || !strings.HasSuffix(s, ")") {
			break
		}
		inner := s[i+1 : len(s)-1]
		if inner == "" || strings.Trim(inner, "0123456789") != "" {
			break
		}
		s = strings.TrimSpace(s[:i])
	}
	return s
}

// parseMoney 从 "0.02元" / "$2" / "4.5" 解析出数值与原币种。
// 无币种标记时返回空币种(由调用方按页面兜底)。
func parseMoney(s string) (float64, domain.Currency, bool) {
	s = normalizeText(s)
	if s == "" {
		return 0, "", false
	}
	cur := domain.Currency("")
	switch {
	case strings.Contains(s, "元") || strings.Contains(s, "¥") || strings.Contains(s, "￥"):
		cur = domain.CurrencyCNY
	case strings.Contains(s, "$") || strings.Contains(s, "USD"):
		cur = domain.CurrencyUSD
	}
	// 取第一段类数字子串(支持小数、逗号分隔)。
	var b strings.Builder
	seenDigit := false
	for _, ch := range s {
		if ch >= '0' && ch <= '9' {
			b.WriteRune(ch)
			seenDigit = true
			continue
		}
		if ch == '.' && seenDigit && !strings.Contains(b.String(), ".") {
			b.WriteRune(ch)
			continue
		}
		if ch == ',' && seenDigit {
			continue
		}
		if seenDigit {
			break
		}
	}
	if !seenDigit {
		return 0, "", false
	}
	v, err := strconv.ParseFloat(strings.TrimSuffix(b.String(), "."), 64)
	if err != nil {
		return 0, "", false
	}
	return v, cur, true
}
