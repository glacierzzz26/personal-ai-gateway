package pricing

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strings"
	"testing"

	"personal-ai-gateway/internal/domain"
)

// bodyClient 返回固定正文的 client(bodyRT 见 commandcode_test.go)。
func bodyClient(t *testing.T, body []byte) *http.Client {
	t.Helper()
	return &http.Client{Transport: bodyRT{body: string(body)}}
}

// 夹具:2026-10-12 从订阅计划页实存(站点改版后重新取:opencode 旧址 docs/go/ 已 404)。
//
//	commandcode_goat.html —— CC GOAT($10 档)页(789626 字节):估算表 + 三张 Monthly credits 表(53 行)。
//	opencode_go.html      —— opencode Go($10 档)英文页(54552 字节,https://opencode.ai/v2/docs/console/go/):
//	                         计划表 + 两张 Monthly limit 表(Go 43 行 / Go Plus 43 行)+ 清单表。
const (
	ccGoatFixture = "commandcode_goat.html"
	ocGoFixture   = "opencode_go.html"
)

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// planMap 解析夹具为 slug→ratio。
func planBySlug(t *testing.T, fixture string, parse func([]byte) ([]planRow, float64, error)) (map[string]planRow, float64) {
	t.Helper()
	rows, price, err := parse(loadFixture(t, fixture))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	m := map[string]planRow{}
	for _, r := range rows {
		if r.Slug != "" {
			m[r.Slug] = r
		}
	}
	return m, price
}

// CC GOAT:三张 credits 表都属 $10 GOAT,合并 53 行;订阅价 $10;DeepSeek V4.1 Flash $60 → ratio≈0.1667。
func TestParseCCGoat(t *testing.T) {
	bySlug, price := planBySlug(t, ccGoatFixture, parseCCGoat)
	if price != 10 {
		t.Errorf("plan price = %v, want 10", price)
	}
	// 53 行全部映射到 slug(0 未映射)。
	if len(bySlug) != 53 {
		t.Errorf("mapped rows = %d, want 53", len(bySlug))
	}
	for _, tc := range []struct {
		slug    string
		credits float64
	}{
		{"deepseek-v4-1-flash", 60},
		{"gpt-5-6-sol", 70},
		{"claude-sonnet-5-5", 10},
		{"claude-haiku-5-5", 20},
		{"gpt-6-luna", 20},
		{"mistral-large-4", 40}, // 折扣行单元格 <button><s>$20</s>$40…</button> → 取现额度 $40(非原值 $20)
		{"mimo-v2-5", 30},
	} {
		r, ok := bySlug[tc.slug]
		if !ok {
			t.Errorf("缺 slug %s", tc.slug)
			continue
		}
		if !approx(r.CreditsUSD, tc.credits) {
			t.Errorf("%s credits = %v, want %v", tc.slug, r.CreditsUSD, tc.credits)
		}
	}
	// ratio = 10 / credits
	if r := bySlug["deepseek-v4-1-flash"]; !approx(10/r.CreditsUSD, 10.0/60.0) {
		t.Errorf("deepseek ratio = %v, want 0.1667", 10/r.CreditsUSD)
	}
}

// opencode Go:第一张 Monthly limit(=Go 档)43 行,其中若干模型峰谷/分档各占两行(额度相同)
// → 去重后 30 个 slug;2 个 Free 行。订阅价 $10;DeepSeek V4.1 Flash $80 → ratio 0.125。
//
// 注意 opencode 的 Model ID 用点("deepseek-v4.1-flash"),与 CC 的连字符 slug("deepseek-v4-1-flash")
// 不同 —— 各渠道按其自身官方价来源的 slug 键控系数,不跨源混用。
func TestParseOCGo(t *testing.T) {
	bySlug, price := planBySlug(t, ocGoFixture, parseOCGo)
	if price != 10 {
		t.Errorf("plan price = %v, want 10", price)
	}
	if len(bySlug) != 30 {
		t.Errorf("unique slugs = %d, want 30", len(bySlug))
	}
	for _, tc := range []struct {
		slug    string
		credits float64
	}{
		{"deepseek-v4.1-flash", 80},
		{"glm-5.3-flash", 60},
		{"glm-5.3", 15},
		{"grok-4.7", 15}, // 峰谷分档两行同名同额度 → 去重后仍在
	} {
		r, ok := bySlug[tc.slug]
		if !ok {
			t.Errorf("缺 slug %s", tc.slug)
			continue
		}
		if !approx(r.CreditsUSD, tc.credits) {
			t.Errorf("%s credits = %v, want %v", tc.slug, r.CreditsUSD, tc.credits)
		}
	}
	if r := bySlug["deepseek-v4.1-flash"]; !approx(10/r.CreditsUSD, 0.125) {
		t.Errorf("deepseek ratio = %v, want 0.125", 10/r.CreditsUSD)
	}
}

// 免费/不限额行(OC 的 "Unlimited"、CC 的 "Free")必须跳过并上报,不得落库。
func TestParsePlanSkipsFreeLabelled(t *testing.T) {
	// OC 页两张 "…Free" 行额度为 "Unlimitedlimited time" → Free。
	rows, _, err := parseOCGo(loadFixture(t, ocGoFixture))
	if err != nil {
		t.Fatalf("parseOCGo: %v", err)
	}
	var free int
	for _, r := range rows {
		if r.Free {
			free++
			if !strings.Contains(strings.ToLower(r.Display), "free") {
				t.Errorf("被判免费但显示名不含 Free: %q", r.Display)
			}
		}
	}
	if free != 2 {
		t.Errorf("opencode 免费行 = %d, want 2", free)
	}
}

// FetchPlanAllowances:端到端(不走网络),行数地板/可落库数/免费·未映射上报。
func TestFetchPlanAllowancesCC(t *testing.T) {
	client := bodyClient(t, loadFixture(t, ccGoatFixture))
	target, ok := PlanTargetFor(domain.ChannelTypeCommandCode)
	if !ok {
		t.Fatal("未登记 commandcode 订阅计划来源")
	}
	allows, rep, err := FetchPlanAllowances(context.Background(), client, target, 0)
	if err != nil {
		t.Fatalf("FetchPlanAllowances: %v", err)
	}
	if rep.PlanPriceUSD != 10 {
		t.Errorf("planPrice = %v, want 10", rep.PlanPriceUSD)
	}
	if rep.TotalRows != 53 {
		t.Errorf("totalRows = %d, want 53", rep.TotalRows)
	}
	if rep.Priced != 53 || len(allows) != 53 {
		t.Errorf("priced = %d/%d, want 53", rep.Priced, len(allows))
	}
	for _, a := range allows {
		if a.Ratio <= 0 || a.ModelName == "" {
			t.Errorf("非法额度行: %+v", a)
		}
		if !approx(a.Ratio, a.PlanPriceUSD/a.MonthlyCredits) {
			t.Errorf("%s ratio 计算不符: %+v", a.ModelName, a)
		}
	}
	m := PlanAllowanceMap(allows)
	if !approx(m["deepseek-v4-1-flash"], 10.0/60.0) {
		t.Errorf("deepseek ratio = %v, want 0.1667", m["deepseek-v4-1-flash"])
	}
}

// 未登记来源硬错。
func TestFetchPlanAllowancesUnknownTarget(t *testing.T) {
	client := bodyClient(t, []byte("<html></html>"))
	if _, _, err := FetchPlanAllowances(context.Background(), client, PlanTarget{ChannelType: domain.ChannelTypeThirdParty}, 0); err == nil {
		t.Error("未登记来源应硬错")
	}
}

// ccGoatSynthetic 生成一张合成 CC GOAT 页:n 行模型(估算表 + 额度表各 n 行)。
// freeAll 时额度全部标 Free(用于 priced==0 分支)。
func ccGoatSynthetic(n int, freeAll bool) []byte {
	var b strings.Builder
	b.WriteString(`<!doctype html><html><body>`)
	// 估算表:Model / Context / Intelligence 表头 + /models/<slug> href(供 slug 映射)。
	b.WriteString(`<table><tr><th>Model</th><th>Context</th><th>Intelligence</th><th>Tok/s</th></tr>`)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, `<tr><td><a href="/models/model-%d"><span class="truncate">Model %d</span></a></td><td>1M</td><td>-</td><td>-</td></tr>`, i, i)
	}
	b.WriteString(`</table>`)
	// 额度表:Model / Input / … / Monthly credits。
	b.WriteString(`<table><tr><th>Model</th><th>Input</th><th>Output</th><th>Cache Read</th><th>Cache Write</th><th>Monthly credits</th></tr>`)
	for i := 0; i < n; i++ {
		cred := "$60"
		if freeAll {
			cred = "Free"
		}
		fmt.Fprintf(&b, `<tr><td>Model %d</td><td>$0.10</td><td>$0.20</td><td>$0.01</td><td>$0.10</td><td>%s</td></tr>`, i, cred)
	}
	b.WriteString(`</table></body></html>`)
	return []byte(b.String())
}

// 合成页自证:正常页可落库、行数地板拦截、全免费拦截、行数骤降护栏。
func TestFetchPlanAllowancesSyntheticGuards(t *testing.T) {
	target, ok := PlanTargetFor(domain.ChannelTypeCommandCode)
	if !ok {
		t.Fatal("未登记 commandcode 订阅计划来源")
	}

	// ① 正常页(12 行,10 地板之上):全部可落库。
	allows, rep, err := FetchPlanAllowances(context.Background(), bodyClient(t, ccGoatSynthetic(12, false)), target, 0)
	if err != nil {
		t.Fatalf("synthetic ok page: %v", err)
	}
	if len(allows) != 12 || rep.Priced != 12 {
		t.Errorf("synthetic priced = %d/%d, want 12", len(allows), rep.Priced)
	}

	// ② 行数地板:5 行 < 地板 10 → 拒绝落库。
	if _, _, err := FetchPlanAllowances(context.Background(), bodyClient(t, ccGoatSynthetic(5, false)), target, 0); err == nil ||
		!strings.Contains(err.Error(), "地板") {
		t.Errorf("地板护栏失效: err = %v", err)
	}

	// ③ 全免费:12 行但无一可落库 → 拒绝。
	if _, _, err := FetchPlanAllowances(context.Background(), bodyClient(t, ccGoatSynthetic(12, true)), target, 0); err == nil ||
		!strings.Contains(err.Error(), "无一可落库") {
		t.Errorf("全免费护栏失效: err = %v", err)
	}

	// ④ 行数骤降:12 行 < 上次 20 行的 80%(16)→ 拒绝;门槛内(上次 14 → 11.2)放行。
	if _, _, err := FetchPlanAllowances(context.Background(), bodyClient(t, ccGoatSynthetic(12, false)), target, 20); err == nil ||
		!strings.Contains(err.Error(), "骤降") {
		t.Errorf("骤降护栏失效: err = %v", err)
	}
	if _, _, err := FetchPlanAllowances(context.Background(), bodyClient(t, ccGoatSynthetic(12, false)), target, 14); err != nil {
		t.Errorf("门槛内不该拦: %v", err)
	}
}
