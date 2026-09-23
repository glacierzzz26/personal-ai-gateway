package pricing

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"

	"personal-ai-gateway/internal/domain"
)

// commandcode.go —— commandcode(CC)模型列表页:本站官方价的**唯一锚点来源**(issue #27)。
//
// 为什么单独一个入口:scrapers(pricing.go)是 map[domain.Provider]scraper,键是**厂商**。
// CC 不是厂商(它是中转渠道,Provider 为空),且一页覆盖 20 家厂商,所以塞不进该表 ——
// 它是「来源」,不是「厂商」。故本文件独立成源,由 server 层直接调用 FetchCommandCode。
//
// 商业事实(务必区分):CC 页面单价是**本站采购锚点**,不总是厂商挂牌价
// (CC 靠高缓存命中率把单价压得比挂牌价低)。将来若要对外展示厂商挂牌价,需另存一列区分。

// commandCodeURL CC 模型列表页(一次抓取覆盖全部厂商)。
const commandCodeURL = "https://commandcode.ai/models"

// commandCodeHosts CC 域名白名单(传输层强制,非白名单 host 直接拒绝)。
var commandCodeHosts = []string{"commandcode.ai"}

// commandCodeMinRows 绝对行数地板:低于此值判定页面改版,拒绝落库(宁缺勿假)。
// 实测 2026-09-23 为 81 行;取 40 既留出正常增减余量,又能挡住「解析到半张表/抓错表」。
const commandCodeMinRows = 40

// commandCodeFloorRatio 与上一次成功抓取的行数比。低于此比例视为半张表(issue §六.3)。
const commandCodeFloorRatio = 0.8

// CommandCodeURL CC 列表页 URL(供 server 层展示/留证,避免各处硬编码)。
func CommandCodeURL() string { return commandCodeURL }

// CommandCodeHosts CC 域名白名单(供 server 层构造传输层 AllowlistClient)。
func CommandCodeHosts() []string { return commandCodeHosts }

// CommandCodeReport 一次 CC 抓取的统计。免费行/分档行必须显式上报 ——
// 它们被排除在落库之外,若不报告就成了「静默丢数据」。
type CommandCodeReport struct {
	SourceURL   string                  `json:"sourceUrl"`
	ContentSHA  string                  `json:"contentSha256"`
	TotalRows   int                     `json:"totalRows"`   // 表内数据行数(含免费行)
	Priced      int                     `json:"priced"`      // 落库行数(= TotalRows − 免费行)
	PerVendor   map[domain.Provider]int `json:"perVendor"`   // 各厂商落库行数
	FreeSkipped []string                `json:"freeSkipped"` // 免费行 "显示名(slug)"
}

// ccVendorPrefixes CC 显示名首词 → 真实厂商。顺序即优先级(长前缀在前)。
//
// 依据:2026-09-23 逐模型核对 CC 详情页 JSON-LD 的 "brand"(81/81 命中,零冲突)。
// 为什么不用 brand 字段做运行时来源:那需要 81 次详情页请求。前缀表是 brand 的离线固化,
// brand 值随行记入 Detail["brand"] 作为留证,便于日后复核。
var ccVendorPrefixes = []struct {
	prefix string
	p      domain.Provider
	brand  string
}{
	// 注意 LongCat 的 brand 是 Meituan(不是 ByteDance);Ling 的 brand 是 inclusionAI。
	{"DeepSeek", domain.ProviderDeepSeek, "DeepSeek"},
	{"Claude", domain.ProviderAnthropic, "Anthropic"},
	{"GPT", domain.ProviderOpenAI, "OpenAI"},
	{"Qwen", domain.ProviderQwen, "Alibaba"},
	{"GLM", domain.ProviderZhipu, "Z AI"},
	{"Kimi", domain.ProviderMoonshot, "Moonshot AI"},
	{"Gemini", domain.ProviderGoogle, "Google"},
	{"Grok", domain.ProviderXAI, "xAI"},
	{"MiMo", domain.ProviderXiaomi, "Xiaomi"},
	{"MiniMax", domain.ProviderMiniMax, "MiniMax"},
	{"Muse Spark", domain.ProviderMeta, "Meta"},
	{"Nemotron", domain.ProviderNVIDIA, "NVIDIA"},
	{"Step", domain.ProviderStepFun, "StepFun"},
	{"Tencent Hy", domain.ProviderTencent, "Tencent"},
	{"LongCat", domain.ProviderMeituan, "Meituan"},
	{"Inkling", domain.ProviderThinking, "Thinking Machines"},
	{"Fugu", domain.ProviderSakana, "Sakana AI"},
	{"Laguna", domain.ProviderPoolside, "Poolside"},
	{"Ling", domain.ProviderInclusion, "inclusionAI"},
	{"Jev", domain.ProviderJev, "TypeSafe"},
}

// ccVendorOf 按显示名前缀归厂商。匹配:大小写归一的**前缀** + 边界(紧随其后不是字母),
// 与 InferVendor 同规则,防止 "Ling" 吞掉 "Lingua…"、"Step" 吞掉 "Stepper…"。
// 判不出返回 ("", "")。**刻意不做模糊猜测** —— 猜错会把成本记到别的厂商名下。
func ccVendorOf(display string) (domain.Provider, string) {
	seg := strings.ToLower(strings.TrimSpace(display))
	if seg == "" {
		return "", ""
	}
	for _, vp := range ccVendorPrefixes {
		pfx := strings.ToLower(vp.prefix)
		if !strings.HasPrefix(seg, pfx) {
			continue
		}
		rest := seg[len(pfx):]
		if rest == "" {
			return vp.p, vp.brand
		}
		r := []rune(rest)[0]
		if !(r >= 'a' && r <= 'z') {
			return vp.p, vp.brand
		}
	}
	return "", ""
}

// ---------- 页面解析 ----------

// ccDeal 活动(折扣)信息。删除线原价 + 折扣 + 到期日。
type ccDeal struct {
	Percent int    // 折扣百分比(40 = 40% off)
	Expires string // 到期日原文("September 27, 2026"),可空
	Title   string // 厂商原文标题
	URL     string // CC 活动详情页(留证)
	Free    bool   // 该「活动」实为免费(Free through …)
}

// ccRow 一行解析结果(内部)。刻意不复用 pricing.quote:quote 没有 Provider/Shape,
// 而 CC 每行的厂商与计费形态都不同(Fetch/toRows 的「单一 provider + 单一 shape」前提不成立)。
type ccRow struct {
	Slug, Display string
	Provider      domain.Provider
	Brand         string
	Shape         domain.BillingShape
	In, Out       float64
	CacheRead     float64
	CacheWrite    float64
	CacheWriteOff bool // 缓存写列缺失(页面为 `—`)
	Free          bool
	Deal          *ccDeal
	NativeText    string
	Detail        map[string]any
}

// quote 转成通用 quote 以便复用 validate 的区间/非空/非全零校验。
func (r ccRow) quote() quote {
	return quote{
		ModelName:  r.Slug,
		In:         r.In,
		Out:        r.Out,
		CacheRead:  r.CacheRead,
		NativeText: r.NativeText,
		Detail:     r.Detail,
	}
}

// 价格单元格内的按钮 aria-label 形态(实测 2026-09-23):
//
//	"DeepSeek V4.1 Flash input: $0.30 during peak hours, 01–04 & 06–10 UTC, Mon–Fri"  → 峰值 + 时段
//	"GPT-6 Luna: 2 context price bands"                                                → 分档数
var (
	ccPeakRe   = regexp.MustCompile(`\$([0-9.]+)\s+during peak hours,\s*(.+)$`)
	ccBandsRe  = regexp.MustCompile(`(\d+)\s+context price bands?`)
	ccPctRe    = regexp.MustCompile(`(\d+)\s*%\s*off`)
	ccExpiryRe = regexp.MustCompile(`(?:through|Ends)\s+([A-Z][a-z]+\s+\d{1,2},\s+\d{4})`)
	ccRangeRe  = regexp.MustCompile(`(\d{1,2})\s*[–—-]\s*(\d{1,2})`)
)

// ccPrice 一个价格单元格的解析结果。
type ccPrice struct {
	Current  string // 单元格裸文本:"$1.20" / "Free" / "—" / ""
	Original string // <s> 删除线原价(活动行才有)
	Peak     string // 峰值(峰谷行才有)
	PeakSpec string // 峰值时段说明原文
	Bands    int    // 分档数(分档行才有)
}

// parseCommandCode 解析 CC 模型列表页正文。返回全部数据行(**含免费行**,由调用方过滤)。
//
// 任一结构异常(找不到表、行数/列数对不上、厂商归不出、价格解析不出)一律硬错,
// 绝不回退估算值或静默跳过 —— 「失败即失败」是 pricing 包的红线(见 pricing.go 头注)。
func parseCommandCode(body []byte) ([]ccRow, error) {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	tbl, labels, err := ccFindTable(doc)
	if err != nil {
		return nil, err
	}
	iModel := ccIndex(labels, "Model")
	iInput := ccIndex(labels, "Input")
	iOutput := ccIndex(labels, "Output")
	iCacheRead := ccIndex(labels, "Cache read")
	iCacheWrite := ccIndex(labels, "Cache write") // 允许缺失
	width := len(labels)

	trs := ccRowNodes(tbl)
	if len(trs) < 2 {
		return nil, fmt.Errorf("模型表无数据行(页面结构可能已变更)")
	}
	var out []ccRow
	var unmapped []string
	for _, tr := range trs[1:] {
		cells := ccCellNodes(tr)
		if len(cells) != width {
			return nil, fmt.Errorf("模型表第 %d 数据行列数为 %d,与表头 %d 不一致(页面结构可能已变更)",
				len(out)+1, len(cells), width)
		}
		r, err := ccParseRow(cells, iModel, iInput, iOutput, iCacheRead, iCacheWrite)
		if err != nil {
			return nil, err
		}
		if r.Provider == "" {
			unmapped = append(unmapped, r.Display)
			continue
		}
		out = append(out, r)
	}
	if len(unmapped) > 0 {
		// 不得静默落库:未登记的厂商前缀必须补映射表,而不是丢掉或猜一个。
		return nil, fmt.Errorf("CC 出现未登记厂商前缀:%s(须补 ccVendorPrefixes,不得静默落库)",
			strings.Join(unmapped, "、"))
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("模型表未解析出任何数据行(页面结构可能已变更)")
	}
	return out, nil
}

// ccParseRow 解析一行。cells 已按表头列对齐。
func ccParseRow(cells []*html.Node, iModel, iInput, iOutput, iCacheRead, iCacheWrite int) (ccRow, error) {
	var r ccRow

	// --- 首格:slug / 显示名 / 活动信息 ---
	slug, display, deal, peakTitle := ccParseNameCell(cells[iModel])
	if slug == "" || display == "" {
		return r, fmt.Errorf("模型行缺少 <a href=\"/models/<slug>\"> 或显示名(页面结构可能已变更)")
	}
	r.Slug, r.Display, r.Deal = slug, display, deal
	if p, brand := ccVendorOf(display); p != "" {
		r.Provider, r.Brand = p, brand
	} else {
		return r, nil // 由调用方汇总为「未归属」硬错
	}

	// --- 价格列 ---
	inC := ccParsePrice(cells[iInput])
	outC := ccParsePrice(cells[iOutput])
	crC := ccParsePrice(cells[iCacheRead])
	var cwC ccPrice
	if iCacheWrite >= 0 {
		cwC = ccParsePrice(cells[iCacheWrite])
	}

	in, inFree, err := ccMoney(inC.Current)
	if err != nil {
		return r, fmt.Errorf("%s 输入价解析失败: %w", display, err)
	}
	outV, outFree, err := ccMoney(outC.Current)
	if err != nil {
		return r, fmt.Errorf("%s 输出价解析失败: %w", display, err)
	}
	if inFree || outFree {
		r.Free = true
		return r, nil // 免费行:不落库,由调用方上报
	}
	cr, _, err := ccMoney(crC.Current)
	if err != nil {
		return r, fmt.Errorf("%s 缓存读价解析失败: %w", display, err)
	}
	r.In, r.Out, r.CacheRead = in, outV, cr
	if iCacheWrite < 0 || cwC.Current == "" || cwC.Current == "—" {
		r.CacheWriteOff = true
	} else {
		cw, _, err := ccMoney(cwC.Current)
		if err != nil {
			return r, fmt.Errorf("%s 缓存写价解析失败: %w", display, err)
		}
		r.CacheWrite = cw
	}

	// --- 计费形态:峰谷 > 折扣 > 分档 > 单一价 ---
	// 分档与折扣会**并存**(grok-4-7、minimax-m3 既有多档又有折扣),故分档只作 Detail 留证
	// (tierBands),不抢 Shape —— Shape 取对计价语义更「显著」的那个:
	// 分时影响每笔计费,折扣代表这个锚点价的时效性,分档明细本页拿不到(只在详情页)。
	detail := map[string]any{
		"ccSlug":      slug,
		"displayName": display,
		"brand":       r.Brand,
		"note":        "commandcode 单页锚点价(采购锚点,不总是厂商挂牌价)",
	}
	if inC.Bands > 0 {
		detail["tierBands"] = inC.Bands
		detail["tiersNote"] = "全档明细在 CC 详情页,本行只取标准档(绝不写 Detail[\"tiers\"],该键在旧库已坏)"
	}
	switch {
	case inC.Peak != "":
		r.Shape = domain.ShapePeakOff
		peakIn, _, _ := ccMoney("$" + inC.Peak)
		peakOut, _, _ := ccMoney("$" + outC.Peak)
		peakCR, _, _ := ccMoney("$" + crC.Peak)
		detail["peak"] = map[string]any{"in": peakIn, "out": peakOut, "cacheRead": peakCR}
		detail["offpeak"] = map[string]any{"in": r.In, "out": r.Out, "cacheRead": r.CacheRead}
		detail["effectiveDefault"] = "offpeak"
		if peakTitle != "" {
			detail["peakHours"] = peakTitle
		}
		ws, err := ccPeakWindows(inC.PeakSpec)
		if err != nil {
			// 绝不回落成「只有标量价」——那会把高峰流量按谷价计费,正是 window.go 头注写的那个 bug。
			return r, fmt.Errorf("%s 峰谷时段无法解析: %w", display, err)
		}
		detail["windows"] = windowsToAny(ws)
		detail["note"] = "峰谷分时:计费按请求时刻自动选峰/谷价(成本与售价同步浮动);页面显示值为谷价"
	case deal != nil:
		r.Shape = domain.ShapeDiscount
	default:
		r.Shape = domain.ShapeFlat
	}
	if r.Free {
		r.Shape = domain.ShapeFlat
	}
	if r.CacheWriteOff {
		detail["cacheWriteAbsent"] = true
	} else {
		detail["cacheWrite"] = r.CacheWrite
	}
	if deal != nil && !deal.Free {
		d := map[string]any{
			"percent": deal.Percent,
			"title":   deal.Title,
		}
		if deal.URL != "" {
			d["url"] = deal.URL
		}
		if deal.Expires != "" {
			d["expiresAt"] = deal.Expires
		}
		orig := map[string]any{"in": r.In, "out": r.Out, "cacheRead": r.CacheRead}
		if v, ok := ccOriginal(inC); ok {
			orig["in"] = v
		}
		if v, ok := ccOriginal(outC); ok {
			orig["out"] = v
		}
		if v, ok := ccOriginal(crC); ok {
			orig["cacheRead"] = v
		}
		d["original"] = orig
		detail["deal"] = d
	}
	r.Detail = detail
	r.NativeText = ccNativeText(r, inC, outC, crC)
	return r, nil
}

// ccOriginal 取 <s> 删除线原价(存在且可解析时)。
func ccOriginal(c ccPrice) (float64, bool) {
	if c.Original == "" || c.Original == "—" {
		return 0, false
	}
	v, free, err := ccMoney(c.Original)
	if err != nil || free {
		return 0, false
	}
	return v, true
}

// ccParseNameCell 从首格取 slug、显示名、活动信息与峰谷提示原文。
//
// 刻意**不使用整格文本**:它会把徽标/按钮文字拼进来
// (如 "Grok 4.7-40%Ends September 27, 2026"、"DeepSeek V4.1 FlashOff-peak shown (17h/day) · …")。
func ccParseNameCell(cell *html.Node) (slug, display string, deal *ccDeal, peakTitle string) {
	var d ccDeal
	var haveDeal bool
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			if h := ccAttr(n, "href"); strings.HasPrefix(h, "/models/") && slug == "" {
				slug = strings.TrimPrefix(h, "/models/")
				display = ccTruncateText(n)
			}
		}
		if n.Type == html.ElementNode && (n.Data == "a" || n.Data == "button") {
			title := ccAttr(n, "title")
			label := ccAttr(n, "aria-label")
			if strings.Contains(title, "See the deal") || strings.Contains(label, "view deal") {
				haveDeal = true
				if d.Title == "" {
					d.Title = title
				}
				if h := ccAttr(n, "href"); h != "" && d.URL == "" {
					d.URL = h
				}
				if strings.Contains(title, "Free") || strings.Contains(label, "Free") {
					d.Free = true
				}
				if m := ccPctRe.FindStringSubmatch(title + " " + label); m != nil {
					d.Percent, _ = strconv.Atoi(m[1])
				}
				if m := ccExpiryRe.FindStringSubmatch(title); m != nil {
					d.Expires = m[1]
				}
			}
			// 到期日有时只挂在旁挂按钮上(title="… · Ends September 27, 2026")。
			if d.Expires == "" && strings.Contains(title, "Ends ") {
				if m := ccExpiryRe.FindStringSubmatch(title); m != nil {
					d.Expires = m[1]
				}
			}
			if peakTitle == "" && strings.HasPrefix(title, "Off-peak shown") {
				peakTitle = title
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(cell)
	if haveDeal {
		deal = &d
	}
	return slug, display, deal, peakTitle
}

// ccTruncateText 取模型名 <span class="truncate …"> 的文本(CC 的显示名载体)。
func ccTruncateText(a *html.Node) string {
	var out string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if out != "" {
			return
		}
		if n.Type == html.ElementNode && n.Data == "span" {
			if strings.Contains(ccAttr(n, "class"), "truncate") {
				out = ccText(n)
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(a)
	if out == "" {
		out = ccText(a)
	}
	return out
}

// ccParsePrice 解析一个价格单元格。按节点走,区分:
//   - <s> 内文本 → 删除线原价
//   - <button> 的 aria-label/title → 峰值 / 分档数 / 脚注角标
//   - 其余文本 → 现价
//
// **必须按节点走,不能对整格文本调 parseMoney**:整格文本 "$2.00$1.20" 会取到删除线原价 2.00,
// 把现价与折扣价弄反。
func ccParsePrice(cell *html.Node) ccPrice {
	var out ccPrice
	var walk func(n *html.Node, inStrike bool)
	walk = func(n *html.Node, inStrike bool) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			switch {
			case c.Type == html.TextNode:
				if inStrike {
					out.Original += c.Data
				} else {
					out.Current += c.Data
				}
			case c.Type == html.ElementNode && (c.Data == "s" || c.Data == "del"):
				walk(c, true)
			case c.Type == html.ElementNode && c.Data == "button":
				label := ccAttr(c, "aria-label")
				if label == "" {
					label = ccAttr(c, "title")
				}
				if m := ccPeakRe.FindStringSubmatch(label); m != nil {
					out.Peak = m[1]
					out.PeakSpec = normalizeText(m[2])
				}
				if m := ccBandsRe.FindStringSubmatch(label); m != nil {
					out.Bands, _ = strconv.Atoi(m[1])
				}
			case c.Type == html.ElementNode:
				walk(c, inStrike)
			}
		}
	}
	walk(cell, false)
	out.Current = normalizeText(out.Current)
	out.Original = normalizeText(out.Original)
	return out
}

// ccMoney 解析 "$1.20" / "1.20" 为数值。免费/缺失返回 (0, true/false, nil)。
func ccMoney(s string) (float64, bool, error) {
	s = normalizeText(s)
	switch s {
	case "":
		return 0, false, fmt.Errorf("价格单元格为空")
	case "—", "-", "–":
		return 0, false, nil // 该列缺失(页面用破折号表示)
	case "Free", "free", "免费":
		return 0, true, nil
	}
	v, _, ok := parseMoney(s)
	if !ok {
		return 0, false, fmt.Errorf("无法从 %q 解析金额", s)
	}
	return v, false, nil
}

// ccPeakWindows 从 "01–04 & 06–10 UTC, Mon–Fri" 解析峰时段窗口。
// CC 的时段是 **UTC**,故 TZOffsetMin=0 且 TZSet=true(显式,区别于「未设置」)。
func ccPeakWindows(spec string) ([]PriceWindow, error) {
	up := strings.ToUpper(spec)
	ui := strings.Index(up, "UTC")
	if ui < 0 {
		return nil, fmt.Errorf("峰谷时段未标注 UTC,无法确定时区: %q", spec)
	}
	timesPart := spec[:ui]
	daysPart := strings.TrimSpace(strings.TrimLeft(spec[ui+len("UTC"):], " ,"))
	if daysPart == "" {
		return nil, fmt.Errorf("峰谷时段未标注生效星期: %q", spec)
	}
	days, err := ccWeekdays(daysPart)
	if err != nil {
		return nil, err
	}
	ms := ccRangeRe.FindAllStringSubmatch(timesPart, -1)
	if len(ms) == 0 {
		return nil, fmt.Errorf("峰谷时段未解析出时间区间: %q", spec)
	}
	out := make([]PriceWindow, 0, len(ms))
	for _, m := range ms {
		sh, _ := strconv.Atoi(m[1])
		eh, _ := strconv.Atoi(m[2])
		if sh > 23 || eh > 24 || eh <= sh {
			return nil, fmt.Errorf("峰谷时段区间非法(%s→%s,不支持跨零点): %q", m[1], m[2], spec)
		}
		out = append(out, PriceWindow{
			Days:        days,
			Start:       fmt.Sprintf("%02d:00", sh),
			End:         fmt.Sprintf("%02d:00", eh),
			TZOffsetMin: 0,
			TZSet:       true, // UTC:0 是**真实偏移**,不是「未设置」
		})
	}
	return out, nil
}

// ccWeekdays 解析生效星期。只认已知写法,不猜。
func ccWeekdays(s string) ([]int, error) {
	s = strings.ToLower(normalizeText(s))
	s = strings.NewReplacer("–", "-", "—", "-").Replace(s)
	switch s {
	case "mon-fri", "monday-friday", "mon to fri", "weekdays":
		return []int{1, 2, 3, 4, 5}, nil
	case "sat-sun", "saturday-sunday", "weekends":
		return []int{6, 7}, nil
	case "mon-sun", "monday-sunday", "every day", "daily":
		return []int{1, 2, 3, 4, 5, 6, 7}, nil
	}
	return nil, fmt.Errorf("峰谷时段的生效星期无法识别: %q", s)
}

// ccNativeText 重建人读文本(管理台逐字核对 CC 页面用)。由解析值拼出,不用整格文本。
func ccNativeText(r ccRow, inC, outC, crC ccPrice) string {
	var b strings.Builder
	fmt.Fprintf(&b, "输入 %s | 输出 %s | 缓存读 %s", ccMoneyText(r.In), ccMoneyText(r.Out), ccMoneyText(r.CacheRead))
	if !r.CacheWriteOff {
		fmt.Fprintf(&b, " | 缓存写 %s", ccMoneyText(r.CacheWrite))
	}
	if r.Deal != nil && !r.Deal.Free {
		if v, ok := ccOriginal(inC); ok {
			fmt.Fprintf(&b, "; 原价 输入 %s(现价 %s, -%d%%", ccMoneyText(v), ccMoneyText(r.In), r.Deal.Percent)
			if outV, ok := ccOriginal(outC); ok {
				fmt.Fprintf(&b, ", 输出原价 %s", ccMoneyText(outV))
			}
			if crV, ok := ccOriginal(crC); ok {
				fmt.Fprintf(&b, ", 缓存读原价 %s", ccMoneyText(crV))
			}
			b.WriteString(")")
			if r.Deal.Expires != "" {
				fmt.Fprintf(&b, " 到期 %s", r.Deal.Expires)
			}
		}
	}
	if r.Shape == domain.ShapePeakOff {
		if pk, ok := r.Detail["peak"].(map[string]any); ok {
			fmt.Fprintf(&b, "; 峰价 输入 %s / 输出 %s / 缓存读 %s | 时段 %s",
				ccMoneyText(ccAnyFloat(pk["in"])), ccMoneyText(ccAnyFloat(pk["out"])),
				ccMoneyText(ccAnyFloat(pk["cacheRead"])), inC.PeakSpec)
		}
	}
	return b.String()
}

// ccAnyFloat 从 Detail 的 any 取值(仅用于重建展示文本)。
func ccAnyFloat(v any) float64 {
	f, _ := toFloat(v)
	return f
}

// ccMoneyText 金额展示(每百万 token,USD)。
func ccMoneyText(v float64) string { return "$" + strconv.FormatFloat(v, 'f', -1, 64) }

// ---------- HTML 结构辅助 ----------

// ccFindTable 找到含 Model/Input/Output 表头的那张 <table>。按**表头特征**定位而非位置,
// 以免 CC 日后在表格前后插别的表时错抓。
// 注意:CC 表头首格是英文 "Model"(不是「模型」),故不能复用 findByHeaderCell。
func ccFindTable(doc *html.Node) (*html.Node, []string, error) {
	var (
		found  *html.Node
		labels []string
	)
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if found != nil {
			return
		}
		if n.Type == html.ElementNode && n.Data == "table" {
			l := ccHeaderLabels(n)
			if ccIndex(l, "Model") >= 0 && ccIndex(l, "Input") >= 0 && ccIndex(l, "Output") >= 0 {
				found, labels = n, l
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if found == nil {
		return nil, nil, fmt.Errorf("未找到含 Model/Input/Output 表头的模型表(页面结构可能已变更)")
	}
	return found, labels, nil
}

// ccHeaderLabels 取表头行各列标签(去掉排序字形 ↕ 与空白)。
func ccHeaderLabels(tbl *html.Node) []string {
	var tr *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if tr != nil {
			return
		}
		if n.Type == html.ElementNode && n.Data == "tr" {
			tr = n
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(tbl)
	if tr == nil {
		return nil
	}
	var out []string
	for _, c := range ccCellNodes(tr) {
		out = append(out, strings.TrimRight(normalizeText(nodeText(c)), "↕ ⇅ ▲▼"))
	}
	return out
}

// ccRowNodes 表内全部 <tr>(不递归进嵌套表)。
func ccRowNodes(tbl *html.Node) []*html.Node {
	var out []*html.Node
	var walk func(*html.Node, bool)
	walk = func(n *html.Node, root bool) {
		if n.Type == html.ElementNode && n.Data == "table" && !root {
			return
		}
		if n.Type == html.ElementNode && n.Data == "tr" {
			out = append(out, n)
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, false)
		}
	}
	walk(tbl, true)
	return out
}

// ccCellNodes 一行的 <td>/<th> 列表。
func ccCellNodes(tr *html.Node) []*html.Node {
	var out []*html.Node
	for c := tr.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && (c.Data == "td" || c.Data == "th") {
			out = append(out, c)
		}
	}
	return out
}

// ccIndex 标签在表头中的下标,缺返回 -1。
func ccIndex(labels []string, name string) int {
	for i, l := range labels {
		if l == name {
			return i
		}
	}
	return -1
}

func ccAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func ccText(n *html.Node) string { return normalizeText(nodeText(n)) }

// ---------- 抓取入口 ----------

// FetchCommandCode 抓取并解析 CC 列表页,返回可落库的官方价行(**多厂商**)与统计。
//
// previousRows 为上次成功抓取的落库行数(<=0 表示首次,跳过比例检查),用于行数骤降护栏。
// 失败即失败:网络/解析/校验/行数骤降/厂商归属缺失 任一不满足即返回错误,不落可疑值。
func FetchCommandCode(ctx context.Context, client *http.Client, previousRows int) ([]domain.OfficialPriceRow, CommandCodeReport, error) {
	rep := CommandCodeReport{SourceURL: commandCodeURL, PerVendor: map[domain.Provider]int{}}

	body, sha, err := fetchPageURL(ctx, client, commandCodeURL, commandCodeHosts)
	if err != nil {
		return nil, rep, err
	}
	rep.ContentSHA = sha

	rows, err := parseCommandCode(body)
	if err != nil {
		return nil, rep, fmt.Errorf("解析 commandcode 列表页失败(页面结构可能已变更): %w", err)
	}
	rep.TotalRows = len(rows)
	if len(rows) < commandCodeMinRows {
		return nil, rep, fmt.Errorf("commandcode 页面仅解析出 %d 行(地板 %d),判定页面改版,拒绝落库",
			len(rows), commandCodeMinRows)
	}
	if previousRows > 0 && float64(len(rows)) < commandCodeFloorRatio*float64(previousRows) {
		return nil, rep, fmt.Errorf("commandcode 页面行数骤降:%d 行 < 上次 %d 行的 %.0f%%,拒绝落库",
			len(rows), previousRows, commandCodeFloorRatio*100)
	}

	// 免费行排除在落库之外,但必须上报(否则是静默丢数据)。
	// 排除还必须在 validate 之前:validate 会因 in/out 全 0 拒绝**整批**。
	priced := make([]ccRow, 0, len(rows))
	for _, r := range rows {
		if r.Free {
			rep.FreeSkipped = append(rep.FreeSkipped, fmt.Sprintf("%s(%s)", r.Display, r.Slug))
			continue
		}
		priced = append(priced, r)
	}
	rep.Priced = len(priced)

	// 按 (厂商, 形态) 分组:同一个 CC 页里各厂商、各形态的合法性约束不同,
	// 复用 validate 的区间/非空/非全零校验,但分组调用(Currency 恒为 USD)。
	type gkey struct {
		p domain.Provider
		s domain.BillingShape
	}
	groups := map[gkey][]ccRow{}
	var order []gkey
	for _, r := range priced {
		k := gkey{r.Provider, r.Shape}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], r)
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].p != order[j].p {
			return order[i].p < order[j].p
		}
		return order[i].s < order[j].s
	})

	at := time.Now().UTC()
	out := make([]domain.OfficialPriceRow, 0, len(priced))
	for _, k := range order {
		g := groups[k]
		quotes := make([]quote, 0, len(g))
		for _, r := range g {
			quotes = append(quotes, r.quote())
		}
		if err := validate(quotes, domain.CurrencyUSD, k.s); err != nil {
			return nil, rep, fmt.Errorf("commandcode 解析结果校验失败: %w", err)
		}
		for _, r := range g {
			out = append(out, domain.OfficialPriceRow{
				Provider:       k.p,
				ModelName:      r.Slug, // 落 slug(见 DESIGN:与 canonicalModelKey 一致,便于回填绑定)
				SourceURL:      commandCodeURL,
				FetchedAt:      at,
				Currency:       domain.CurrencyUSD,
				BillingShape:   k.s,
				InputPrice:     r.In,
				OutputPrice:    r.Out,
				CacheReadPrice: r.CacheRead,
				NativeText:     r.NativeText,
				Detail:         r.Detail,
				ContentSHA256:  sha,
			})
			rep.PerVendor[k.p]++
		}
	}
	return out, rep, nil
}
