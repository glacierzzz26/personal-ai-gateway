package pricing

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/net/html"

	"personal-ai-gateway/internal/domain"
)

// plans.go —— 订阅计划页:成本系数的**自动来源**。
//
// 商业事实(站主已确认):站主每月花 $10 订阅 CC GOAT / opencode Go,买到**每模型一个**
// 月度额度(Monthly credits / Monthly limit)。故某模型的**真实成本系数** = 订阅价 ÷ 该模型额度:
//
//	CC  DeepSeek V4.1 Flash:$60  → 10/60 ≈ 0.1667
//	OC  DeepSeek V4.1 Flash:$80  → 10/80 = 0.125
//
// 每个模型一个值,不再「一厂商同倍率」手填(旧口径见 channel_vendor_costs)。
//
// 只抓 **$10 档**(CC GOAT / opencode Go);更贵档(如 OC Go Plus $40)不抓 —— 站主只订 $10。
//
// 页面形态(实测 2026-10-12,真静态 HTML):
//   - CC GOAT `commandcode.ai/docs/plans/goat`:估算表 `Model|Context|Intelligence|…` 提供
//     `/models/<slug>` href + truncate span 显示名;另有三张 `Model|Input|Output|Cache Read|
//     Cache Write|Monthly credits` 表(主力 22 行 + 高价值 2 行 + 新模型 29 行,同属 $10 GOAT)。
//   - opencode Go `opencode.ai/docs/go/`(**英文页**:中文页表头是「模型/输入/每月限额」,
//     表头定位会失配):计划表 `Plan|Price|Included usage` 取 Go → $10/month;`Model|Model ID|
//     Endpoint` 表提供 slug;第一张 `Model|Input|Output|Cached Read|Cached Write|Monthly limit`
//     即 Go 档($40 Go Plus 在其后)。
//
// 容错红线(照搬 commandcode.go / opencode.go):免费/不限额行跳过并上报;显示名映射不到
// slug 跳过并上报;行数绝对地板 + 相对地板;可落库行数为 0 硬失败(不得静默清空)。

// ccGoatURL CC GOAT 订阅计划页($10 档)。
const ccGoatURL = "https://commandcode.ai/docs/plans/goat"

// ocGoURL opencode Go 订阅计划页(英文;$10 档)。
const ocGoURL = "https://opencode.ai/docs/go/"

// planMinRows 绝对行数地板:低于此值判定页面改版,拒绝落库(宁缺勿假)。
// 实测 CC GOAT 53 行 / OC Go 42 行;取 10 既留余量又挡「解析到半张表/抓错表」。
const planMinRows = 10

// planFloorRatio 与上一次成功抓取的行数比,低于此视为半张表。
const planFloorRatio = 0.8

// planDefaultPriceUSD 订阅价兜底(页面解析不出时)。两档均为 $10/月。
const planDefaultPriceUSD = 10

// ccGoatPriceRe 从正文取 CC GOAT 订阅价("…the $10/mo GOAT plan…")。
var ccGoatPriceRe = regexp.MustCompile(`\$\s*([0-9]+(?:\.[0-9]+)?)\s*/\s*(?:mo|month)\b`)

// planPriceRe 从单格文本取金额("$10/month" → 10)。
var planPriceRe = regexp.MustCompile(`\$\s*([0-9]+(?:\.[0-9]+)?)`)

// ---------- 类型 ----------

// PlanTarget 一个订阅计划来源(供 server 层构造抓取循环,不暴露解析实现)。
type PlanTarget struct {
	ChannelType domain.ChannelType `json:"channelType"`
	URL         string             `json:"url"`
	Hosts       []string           `json:"-"`
}

// planTarget 内部登记项:复用既有抓取/解析形态(CC/opencode 同构)。
type planTarget struct {
	ChannelType     domain.ChannelType
	URL             string
	Hosts           []string
	DefaultPriceUSD float64
	Parse           func([]byte) ([]planRow, float64, error)
}

// planRow 一行解析结果(内部)。免费/不限额行 Free=true;显示名映射不到 slug 的行 Unmapped=true。
type planRow struct {
	Display    string
	Slug       string
	CreditsUSD float64
	Free       bool
	Unmapped   bool
}

// PlanAllowance 一个模型在订阅计划下的成本系数(落库/计费用)。
//
// ModelName 落 slug(= official_prices.model_name,可被 canonicalModelKey 绑定,与 CC/opencode 抓取一致)。
type PlanAllowance struct {
	ChannelType    domain.ChannelType `json:"channelType"`
	ModelName      string             `json:"modelName"`
	Display        string             `json:"display"`
	MonthlyCredits float64            `json:"monthlyCredits"`
	PlanPriceUSD   float64            `json:"planPriceUsd"`
	Ratio          float64            `json:"ratio"` // = PlanPriceUSD / MonthlyCredits
}

// PlanReport 一次订阅计划抓取的统计。免费/未映射行必须显式上报(否则是静默丢数据)。
type PlanReport struct {
	ChannelType     domain.ChannelType `json:"channelType"`
	SourceURL       string             `json:"sourceUrl"`
	ContentSHA      string             `json:"contentSha256"`
	PlanPriceUSD    float64            `json:"planPriceUsd"`
	TotalRows       int                `json:"totalRows"`
	Priced          int                `json:"priced"`
	SkippedFree     []string           `json:"skippedFree"`
	SkippedUnmapped []string           `json:"skippedUnmapped"`
}

// planRegistry 已登记的订阅计划来源(顺序即 PlanTargets 的返回顺序)。
var planRegistry = []planTarget{
	{
		ChannelType:     domain.ChannelTypeCommandCode,
		URL:             ccGoatURL,
		Hosts:           []string{"commandcode.ai"},
		DefaultPriceUSD: planDefaultPriceUSD,
		Parse:           parseCCGoat,
	},
	{
		ChannelType:     domain.ChannelTypeOpenCode,
		URL:             ocGoURL,
		Hosts:           []string{"opencode.ai"},
		DefaultPriceUSD: planDefaultPriceUSD,
		Parse:           parseOCGo,
	},
}

// PlanTargets 返回全部订阅计划来源(供 server 层遍历抓取)。
func PlanTargets() []PlanTarget {
	out := make([]PlanTarget, 0, len(planRegistry))
	for _, t := range planRegistry {
		out = append(out, PlanTarget{ChannelType: t.ChannelType, URL: t.URL, Hosts: t.Hosts})
	}
	return out
}

// PlanTargetFor 按渠道类型取来源(未登记返回 false)。
func PlanTargetFor(ct domain.ChannelType) (PlanTarget, bool) {
	for _, t := range planRegistry {
		if t.ChannelType == ct {
			return PlanTarget{ChannelType: t.ChannelType, URL: t.URL, Hosts: t.Hosts}, true
		}
	}
	return PlanTarget{}, false
}

// ---------- HTML 辅助 ----------

// planTable 一张命中表头的 <table>。
type planTable struct {
	tbl    *html.Node
	labels []string
}

// planFindTables 返回表头同时含全部 need 列的**所有** <table>(按文档顺序)。
// 与 ccFindTableHaving 同规则,但取全部 —— CC GOAT 的 Monthly credits 分散在三张表,只取首张会漏。
func planFindTables(doc *html.Node, need ...string) []planTable {
	var out []planTable
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "table" {
			l := ccHeaderLabels(n)
			ok := true
			for _, name := range need {
				if ccIndex(l, name) < 0 {
					ok = false
					break
				}
			}
			if ok {
				out = append(out, planTable{tbl: n, labels: l})
				return // 命中即不再递归进表体
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return out
}

// ccGoatSlugByName 从 CC GOAT 估算表建「显示名 → slug」。
// 显示名取 truncate span(整格文本会被徽标/峰谷提示污染,如 "Glyph ClusterFree"、
// "DeepSeek V4.1 Flash FastOff-peak shown (…)");slug 取 `/models/<slug>` href。
func ccGoatSlugByName(doc *html.Node) (map[string]string, error) {
	tbl, labels, err := ccFindTableHaving(doc, "Model", "Context", "Intelligence")
	if err != nil {
		return nil, fmt.Errorf("CC GOAT 模型表: %w", err)
	}
	iModel := ccIndex(labels, "Model")
	m := map[string]string{}
	for _, tr := range ccRowNodes(tbl)[1:] {
		cells := ccCellNodes(tr)
		if len(cells) != len(labels) {
			return nil, fmt.Errorf("CC GOAT 模型表行列数为 %d,与表头 %d 不一致(页面结构可能已变更)", len(cells), len(labels))
		}
		slug, display, _, _ := ccParseNameCell(cells[iModel])
		if slug == "" || display == "" {
			continue // 空行/无 href 的行(如占位),跳过
		}
		if _, dup := m[display]; !dup {
			m[display] = slug
		}
	}
	if len(m) == 0 {
		return nil, fmt.Errorf("CC GOAT 模型表未解析出任何模型(页面结构可能已变更)")
	}
	return m, nil
}

// planCreditsOfCell 取额度单元格的**现价**金额。免费/不限额/破折号/无法解析 → ok=false。
//
// 必须按节点走并**跳过删除线**:CC 折扣行额度形如
// `<button><s>$20</s><span>$40</span><span>through Oct 20th</span></button>` ——
// 删除线 $20 是原值、$40 才是现额度(留证见页面 JSON `"credits":"$40"`)。而 ccParsePrice
// 刻意不递归进 `<button>`(它把按钮当峰谷/分档元数据载体),故此处不能复用它,须自行下钻。
func planCreditsOfCell(cell *html.Node) (float64, bool) {
	var b strings.Builder
	var walk func(n *html.Node, strike bool)
	walk = func(n *html.Node, strike bool) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			switch {
			case c.Type == html.TextNode:
				if !strike {
					b.WriteString(c.Data)
				}
			case c.Type == html.ElementNode && (c.Data == "s" || c.Data == "del" || c.Data == "strike"):
				walk(c, true)
			case c.Type == html.ElementNode:
				walk(c, strike)
			}
		}
	}
	walk(cell, false)
	amt, free, err := ccMoney(normalizeText(b.String()))
	if err != nil || free || amt <= 0 {
		return 0, false
	}
	return amt, true
}

// ---------- CC GOAT 解析 ----------

// parseCCGoat 解析 CC GOAT 页,返回额度行(**含免费行**)与订阅价(USD)。
func parseCCGoat(body []byte) ([]planRow, float64, error) {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}

	price := float64(planDefaultPriceUSD)
	if m := ccGoatPriceRe.FindSubmatch(body); m != nil {
		if v, _, ok := parseMoney("$" + string(m[1])); ok && v > 0 {
			price = v
		}
	}

	slugByName, err := ccGoatSlugByName(doc)
	if err != nil {
		return nil, 0, err
	}

	tables := planFindTables(doc, "Model", "Input", "Monthly credits")
	if len(tables) == 0 {
		return nil, 0, fmt.Errorf("CC GOAT 页未找到 Monthly credits 表(页面结构可能已变更)")
	}
	var out []planRow
	for _, t := range tables {
		iModel := ccIndex(t.labels, "Model")
		iCred := ccIndex(t.labels, "Monthly credits")
		for _, tr := range ccRowNodes(t.tbl)[1:] {
			cells := ccCellNodes(tr)
			if len(cells) != len(t.labels) {
				return nil, 0, fmt.Errorf("CC GOAT 额度表行列数为 %d,与表头 %d 不一致(页面结构可能已变更)", len(cells), len(t.labels))
			}
			out = append(out, planRowOf(cells, iModel, iCred, slugByName))
		}
	}
	if len(out) == 0 {
		return nil, 0, fmt.Errorf("CC GOAT 页未解析出任何额度行(页面结构可能已变更)")
	}
	return out, price, nil
}

// ---------- opencode Go 解析 ----------

// parseOCGo 解析 opencode Go 页,返回额度行(**含免费行**)与订阅价(USD)。
func parseOCGo(body []byte) ([]planRow, float64, error) {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}

	price, err := ocPlanPrice(doc)
	if err != nil {
		return nil, 0, err
	}
	slugByName, err := ocSlugByName(doc)
	if err != nil {
		return nil, 0, err
	}

	// 只取第一张 Monthly limit 表(= Go 档;$40 Go Plus 在其后)。
	tbl, labels, err := ccFindTableHaving(doc, "Model", "Input", "Monthly limit")
	if err != nil {
		return nil, 0, fmt.Errorf("opencode Go 额度表: %w", err)
	}
	iModel := ccIndex(labels, "Model")
	iLimit := ccIndex(labels, "Monthly limit")
	var out []planRow
	for _, tr := range ccRowNodes(tbl)[1:] {
		cells := ccCellNodes(tr)
		if len(cells) != len(labels) {
			return nil, 0, fmt.Errorf("opencode Go 额度表行列数为 %d,与表头 %d 不一致(页面结构可能已变更)", len(cells), len(labels))
		}
		out = append(out, planRowOf(cells, iModel, iLimit, slugByName))
	}
	if len(out) == 0 {
		return nil, 0, fmt.Errorf("opencode Go 页未解析出任何额度行(页面结构可能已变更)")
	}
	return out, price, nil
}

// ocPlanPrice 从计划表取 Go 档订阅价("Go | $10/month")。
func ocPlanPrice(doc *html.Node) (float64, error) {
	tbl, labels, err := ccFindTableHaving(doc, "Plan", "Price", "Included usage")
	if err != nil {
		return 0, fmt.Errorf("opencode Go 计划表: %w", err)
	}
	iPlan := ccIndex(labels, "Plan")
	iPrice := ccIndex(labels, "Price")
	for _, tr := range ccRowNodes(tbl)[1:] {
		cells := ccCellNodes(tr)
		if len(cells) != len(labels) {
			continue
		}
		if normalizeText(nodeText(cells[iPlan])) != "Go" {
			continue
		}
		if m := planPriceRe.FindStringSubmatch(normalizeText(nodeText(cells[iPrice]))); m != nil {
			if v, _, ok := parseMoney("$" + m[1]); ok && v > 0 {
				return v, nil
			}
		}
	}
	return planDefaultPriceUSD, nil
}

// ocSlugByName 从 opencode「Model | Model ID」表建「显示名 → Model ID」。
func ocSlugByName(doc *html.Node) (map[string]string, error) {
	tbl, labels, err := ccFindTableHaving(doc, "Model", "Model ID")
	if err != nil {
		return nil, fmt.Errorf("opencode Go 清单表: %w", err)
	}
	iModel := ccIndex(labels, "Model")
	iSlug := ccIndex(labels, "Model ID")
	m := map[string]string{}
	for _, tr := range ccRowNodes(tbl)[1:] {
		cells := ccCellNodes(tr)
		if len(cells) != len(labels) {
			return nil, fmt.Errorf("opencode Go 清单表行列数为 %d,与表头 %d 不一致(页面结构可能已变更)", len(cells), len(labels))
		}
		name := normalizeText(nodeText(cells[iModel]))
		slug := normalizeText(nodeText(cells[iSlug]))
		if name == "" || slug == "" {
			return nil, fmt.Errorf("opencode Go 清单表存在空显示名或空 Model ID(页面结构可能已变更)")
		}
		if _, dup := m[name]; !dup {
			m[name] = slug
		}
	}
	if len(m) == 0 {
		return nil, fmt.Errorf("opencode Go 清单表未解析出任何模型(页面结构可能已变更)")
	}
	return m, nil
}

// ---------- 共享行解析 ----------

// planRowOf 解析一行额度表。display 为空的行(占位)跳过(返回零值 planRow,Display=="")。
// 显示名会剥括号后缀(opencode 的 "(Off-Peak)"/"(≤ 256K tokens)")再映射 slug。
func planRowOf(cells []*html.Node, iModel, iCredits int, slugByName map[string]string) planRow {
	display := normalizeText(nodeText(cells[iModel]))
	if display == "" {
		return planRow{}
	}
	r := planRow{Display: display}
	amt, ok := planCreditsOfCell(cells[iCredits])
	if !ok {
		r.Free = true // 免费/不限额/破折号 → 跳过并上报
		return r
	}
	r.CreditsUSD = amt
	// slug 映射:先原样,再剥括号后缀(opencode 分档/峰谷名)。
	slug := ""
	if s, ok := slugByName[display]; ok {
		slug = s
	} else if base, _ := ocSplitSuffix(display); base != display {
		if s, ok := slugByName[base]; ok {
			slug = s
		}
	}
	if slug == "" {
		r.Unmapped = true // 显示名映射不到 slug:由调用方跳过并上报
		return r
	}
	r.Slug = slug
	return r
}

// ---------- 抓取入口 ----------

// FetchPlanAllowances 抓取并解析一个订阅计划来源,返回各模型的成本系数与统计。
//
// previousRows 为上次成功抓取的落库行数(<=0 表示首次,跳过比例检查),用于行数骤降护栏。
// 失败即失败:网络/解析/行数地板/行数骤降/可落库行为 0 任一不满足即返回错误,不落可疑值。
func FetchPlanAllowances(ctx context.Context, client *http.Client, target PlanTarget, previousRows int) ([]PlanAllowance, PlanReport, error) {
	var tgt planTarget
	found := false
	for _, t := range planRegistry {
		if t.ChannelType == target.ChannelType {
			tgt, found = t, true
			break
		}
	}
	if !found {
		return nil, PlanReport{}, fmt.Errorf("未登记的订阅计划来源: %s", target.ChannelType)
	}
	rep := PlanReport{ChannelType: tgt.ChannelType, SourceURL: tgt.URL}

	body, sha, err := fetchPageURL(ctx, client, tgt.URL, tgt.Hosts)
	if err != nil {
		return nil, rep, err
	}
	rep.ContentSHA = sha

	rows, price, err := tgt.Parse(body)
	if err != nil {
		return nil, rep, fmt.Errorf("解析订阅计划页失败(页面结构可能已变更): %w", err)
	}
	if price <= 0 {
		price = tgt.DefaultPriceUSD
	}
	rep.PlanPriceUSD = price
	rep.TotalRows = len(rows)
	if len(rows) < planMinRows {
		return nil, rep, fmt.Errorf("订阅计划页仅解析出 %d 行(地板 %d),判定页面改版,拒绝落库", len(rows), planMinRows)
	}

	priced := make([]planRow, 0, len(rows))
	seen := map[string]bool{}
	for _, r := range rows {
		switch {
		case r.Display == "":
			continue
		case r.Free:
			rep.SkippedFree = append(rep.SkippedFree, r.Display)
		case r.Unmapped:
			rep.SkippedUnmapped = append(rep.SkippedUnmapped, r.Display)
		case seen[r.Slug]:
			continue // 同 slug 在多张表重复出现 → 只取首次
		default:
			seen[r.Slug] = true
			priced = append(priced, r)
		}
	}
	rep.Priced = len(priced)
	if len(priced) == 0 {
		return nil, rep, fmt.Errorf("订阅计划页解析出 %d 行但无一可落库(免费/不限额 %d,未映射 %d),拒绝落库",
			len(rows), len(rep.SkippedFree), len(rep.SkippedUnmapped))
	}
	if previousRows > 0 && float64(len(priced)) < planFloorRatio*float64(previousRows) {
		return nil, rep, fmt.Errorf("订阅计划页行数骤降:%d 行 < 上次 %d 行的 %.0f%%,拒绝落库",
			len(priced), previousRows, planFloorRatio*100)
	}

	out := make([]PlanAllowance, 0, len(priced))
	for _, r := range priced {
		out = append(out, PlanAllowance{
			ChannelType:    tgt.ChannelType,
			ModelName:      r.Slug,
			Display:        r.Display,
			MonthlyCredits: r.CreditsUSD,
			PlanPriceUSD:   price,
			Ratio:          price / r.CreditsUSD,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ModelName < out[j].ModelName })
	return out, rep, nil
}

// PlanAllowanceMap 把额度行折成「slug → ratio」,供 store 批量 upsert。
func PlanAllowanceMap(rows []PlanAllowance) map[string]float64 {
	m := make(map[string]float64, len(rows))
	for _, r := range rows {
		if r.ModelName == "" || r.Ratio <= 0 {
			continue
		}
		m[r.ModelName] = r.Ratio
	}
	return m
}
