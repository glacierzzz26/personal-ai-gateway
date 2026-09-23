package pricing

import (
	"net/http"
	"strings"
	"testing"

	"personal-ai-gateway/internal/domain"
)

// 夹具:2026-09-23 从 https://commandcode.ai/models 实存(595908 字节)。
// 82 行 = 1 表头 + 81 模型;9 列。页脚标注 Per 1M tokens · USD。
const ccFixture = "commandcode_models.html"

// ccParseFixture 解析夹具并返回按 slug 索引的行。
func ccParseFixture(t *testing.T) map[string]ccRow {
	t.Helper()
	rows, err := parseCommandCode(loadFixture(t, ccFixture))
	if err != nil {
		t.Fatalf("parseCommandCode: %v", err)
	}
	bySlug := map[string]ccRow{}
	for _, r := range rows {
		bySlug[r.Slug] = r
	}
	return bySlug
}

// 页面结构指纹:行数/免费数/峰谷数/折扣数/分档数任一漂移即失败 —— 让 CC 改版在此处炸,
// 而不是在生产里静默记错价。
func TestParseCommandCodeCounts(t *testing.T) {
	rows, err := parseCommandCode(loadFixture(t, ccFixture))
	if err != nil {
		t.Fatalf("parseCommandCode: %v", err)
	}
	if len(rows) != 81 {
		t.Fatalf("rows = %d, want 81", len(rows))
	}
	var free, peak, deal, tiered, cacheWriteAbsent int
	for _, r := range rows {
		if r.Free {
			free++
		}
		if r.Shape == domain.ShapePeakOff {
			peak++
		}
		if r.Deal != nil && !r.Deal.Free {
			deal++
		}
		// 分档是 Detail 留证(与折扣可能并存),不再是独立 Shape —— 按 tierBands 键计数。
		if _, ok := r.Detail["tierBands"]; ok {
			tiered++
		}
		if r.CacheWriteOff {
			cacheWriteAbsent++
		}
	}
	for _, tc := range []struct {
		name      string
		got, want int
	}{
		{"免费行", free, 3},
		{"峰谷行", peak, 4},
		{"折扣行", deal, 4}, // Grok 4.7 / MiniMax M3 / MiMo V2.5 / MiMo V2.5 Pro(Jev 免费另计)
		{"分档行", tiered, 12},
		// 60 个 `—` 里有 3 个是免费行(免费行整体不落库,本计数只针对可计价行的缓存写缺失)。
		{"缓存写缺失行", cacheWriteAbsent, 57},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %d, want %d", tc.name, tc.got, tc.want)
		}
	}
}

// 厂商归属必须覆盖全部 81 行,且分布与实测(逐详情页 brand)一致。
func TestParseCommandCodeVendorDistribution(t *testing.T) {
	rows, err := parseCommandCode(loadFixture(t, ccFixture))
	if err != nil {
		t.Fatalf("parseCommandCode: %v", err)
	}
	got := map[domain.Provider]int{}
	for _, r := range rows {
		if r.Provider == "" {
			t.Fatalf("模型 %q(%s)未归属厂商", r.Display, r.Slug)
		}
		if r.Brand == "" {
			t.Errorf("模型 %q 缺少 brand 留证", r.Display)
		}
		got[r.Provider]++
	}
	want := map[domain.Provider]int{
		domain.ProviderOpenAI: 10, domain.ProviderQwen: 10, domain.ProviderAnthropic: 9,
		domain.ProviderZhipu: 7, domain.ProviderGoogle: 6, domain.ProviderXiaomi: 5,
		domain.ProviderDeepSeek: 5, domain.ProviderMeta: 5, domain.ProviderMoonshot: 5,
		domain.ProviderXAI: 3, domain.ProviderStepFun: 3, domain.ProviderMiniMax: 3,
		domain.ProviderTencent: 2, domain.ProviderThinking: 2, domain.ProviderJev: 1,
		domain.ProviderInclusion: 1, domain.ProviderMeituan: 1, domain.ProviderPoolside: 1,
		domain.ProviderSakana: 1, domain.ProviderNVIDIA: 1,
	}
	for p, n := range want {
		if got[p] != n {
			t.Errorf("%s = %d 行, want %d", p, got[p], n)
		}
	}
	if len(got) != len(want) {
		t.Errorf("厂商数 = %d, want %d", len(got), len(want))
	}
}

// LongCat 的 brand 是 Meituan(不是 ByteDance)、Ling 是 inclusionAI —— 实测核对过的易错点。
func TestCommandCodeVendorEdgeCases(t *testing.T) {
	for _, tc := range []struct {
		display string
		want    domain.Provider
	}{
		{"LongCat 2.0", domain.ProviderMeituan},
		{"Ling 3.0 Flash Sante", domain.ProviderInclusion},
		{"Tencent Hy4 Preview", domain.ProviderTencent},
		{"Inkling Small", domain.ProviderThinking},
		{"Nemotron 3 Ultra", domain.ProviderNVIDIA},
		{"Muse Spark 1.3", domain.ProviderMeta},
		{"Fugu Ultra", domain.ProviderSakana},
		{"Laguna S 2.1", domain.ProviderPoolside},
		{"Jev", domain.ProviderJev},
	} {
		if p, _ := ccVendorOf(tc.display); p != tc.want {
			t.Errorf("ccVendorOf(%q) = %q, want %q", tc.display, p, tc.want)
		}
	}
	// 边界:前缀后紧接字母不得命中(防 "Ling" 吞 "Lingua…"、"Step" 吞 "Stepper…")。
	for _, s := range []string{"Lingua Max", "Stepper 2", "Grokling", "QwenX"} {
		if p, _ := ccVendorOf(s); p != "" {
			t.Errorf("ccVendorOf(%q) = %q, want 空(边界应为字母)", s, p)
		}
	}
}

// 折扣行取**现价**作标量,原价进 Detail["deal"](决策 2)。parseMoney 对整格会取到原价,故必须在格内按节点走。
func TestParseCommandCodeDeal(t *testing.T) {
	bySlug := ccParseFixture(t)

	// Grok 4.7 -40%,有明确到期日。
	g := bySlug["grok-4-7"]
	if g.Shape != domain.ShapeDiscount {
		t.Errorf("grok-4-7 shape = %q, want discount", g.Shape)
	}
	if g.In != 1.20 || g.Out != 3.60 || g.CacheRead != 0.30 {
		t.Errorf("grok-4-7 现价 = %v/%v/%v, want 1.20/3.60/0.30", g.In, g.Out, g.CacheRead)
	}
	if g.In == 2.00 {
		t.Error("grok-4-7 取了删除线原价(2.00)—— 现价应是 1.20")
	}
	d, ok := g.Detail["deal"].(map[string]any)
	if !ok {
		t.Fatalf("grok-4-7 缺少 Detail[deal]: %v", g.Detail)
	}
	if d["percent"] != 40 {
		t.Errorf("deal.percent = %v, want 40", d["percent"])
	}
	if s, _ := d["expiresAt"].(string); !strings.Contains(s, "September 27, 2026") {
		t.Errorf("deal.expiresAt = %q, want 含 September 27, 2026", s)
	}
	orig, _ := d["original"].(map[string]any)
	if v, _ := toFloat(orig["in"]); v != 2.00 {
		t.Errorf("deal.original.in = %v, want 2.00", orig["in"])
	}
	if v, _ := toFloat(orig["out"]); v != 6.00 {
		t.Errorf("deal.original.out = %v, want 6.00", orig["out"])
	}

	// MiMo V2.5 -98%,无到期日 → expiresAt 省略。
	m := bySlug["mimo-v2-5"]
	if m.In != 0.14 || m.Out != 0.28 {
		t.Errorf("mimo-v2-5 现价 = %v/%v, want 0.14/0.28", m.In, m.Out)
	}
	md, _ := m.Detail["deal"].(map[string]any)
	if _, present := md["expiresAt"]; present {
		t.Errorf("mimo-v2-5 不应有 expiresAt(页面未给): %v", md)
	}
}

// 峰谷行:显示值为谷价,峰值来自每格的 aria-label(含缓存读,故**不需要**按倍率推导);
// 时段为 UTC(01–04 & 06–10, Mon–Fri)。
func TestParseCommandCodePeak(t *testing.T) {
	bySlug := ccParseFixture(t)

	for _, tc := range []struct {
		slug                    string
		offIn, offOut, offCache float64
		pkIn, pkOut, pkCache    float64
	}{
		{"deepseek-v4-1-flash", 0.15, 0.60, 0.003, 0.30, 1.20, 0.006},
		{"deepseek-v4-pro", 0.66, 1.98, 0.022, 1.32, 3.96, 0.044},
	} {
		r, ok := bySlug[tc.slug]
		if !ok {
			t.Fatalf("缺少 %s", tc.slug)
		}
		if r.Shape != domain.ShapePeakOff {
			t.Errorf("%s shape = %q, want peak_offpeak", tc.slug, r.Shape)
		}
		if r.In != tc.offIn || r.Out != tc.offOut || r.CacheRead != tc.offCache {
			t.Errorf("%s 谷价 = %v/%v/%v, want %v/%v/%v",
				tc.slug, r.In, r.Out, r.CacheRead, tc.offIn, tc.offOut, tc.offCache)
		}
		pk, _ := r.Detail["peak"].(map[string]any)
		if v, _ := toFloat(pk["in"]); v != tc.pkIn {
			t.Errorf("%s peak.in = %v, want %v", tc.slug, pk["in"], tc.pkIn)
		}
		if v, _ := toFloat(pk["out"]); v != tc.pkOut {
			t.Errorf("%s peak.out = %v, want %v", tc.slug, pk["out"], tc.pkOut)
		}
		if v, _ := toFloat(pk["cacheRead"]); v != tc.pkCache {
			t.Errorf("%s peak.cacheRead = %v, want %v", tc.slug, pk["cacheRead"], tc.pkCache)
		}

		// 窗口必须能解码且在 UTC(若 TZSet 丢失会回退 +480,这里就是那条防线的落点)。
		ws, ok := WindowsFromDetail(r.Detail, r.Shape)
		if !ok {
			t.Fatalf("%s 的 windows 无法从 Detail 解码", tc.slug)
		}
		if len(ws) != 2 {
			t.Fatalf("%s windows = %d 段, want 2", tc.slug, len(ws))
		}
		for _, w := range ws {
			if !w.TZSet || w.TZOffsetMin != 0 {
				t.Errorf("%s 窗口时区应为显式 UTC(0, TZSet): %+v", tc.slug, w)
			}
			if len(w.Days) != 5 {
				t.Errorf("%s 窗口星期应为 Mon–Fri: %+v", tc.slug, w)
			}
		}
		if ws[0].Start != "01:00" || ws[0].End != "04:00" {
			t.Errorf("%s 首段 = %s→%s, want 01:00→04:00", tc.slug, ws[0].Start, ws[0].End)
		}
		if ws[1].Start != "06:00" || ws[1].End != "10:00" {
			t.Errorf("%s 次段 = %s→%s, want 06:00→10:00", tc.slug, ws[1].Start, ws[1].End)
		}
	}
}

// 缓存写:CC 页面有的行必须落到 CacheWrite;"—" 行值为 0 且 Detail 标 cacheWriteAbsent。
func TestParseCommandCodeCacheWrite(t *testing.T) {
	bySlug := ccParseFixture(t)

	// Claude Haiku 4.5 缓存写 1.25(输入 1.00 的 1.25 倍)。
	h := bySlug["claude-haiku-4-5"]
	if h.CacheWriteOff {
		t.Fatal("claude-haiku-4-5 不应缺缓存写")
	}
	if h.CacheWrite != 1.25 {
		t.Errorf("claude-haiku-4-5 cacheWrite = %v, want 1.25", h.CacheWrite)
	}
	if v, _ := toFloat(h.Detail["cacheWrite"]); v != 1.25 {
		t.Errorf("Detail.cacheWrite = %v, want 1.25", h.Detail["cacheWrite"])
	}

	// MiniMax M3 的缓存写是 "—"。
	m := bySlug["minimax-m3"]
	if !m.CacheWriteOff {
		t.Error("minimax-m3 缓存写应为缺失")
	}
	if m.CacheWrite != 0 {
		t.Errorf("minimax-m3 cacheWrite = %v, want 0", m.CacheWrite)
	}
	if m.Detail["cacheWriteAbsent"] != true {
		t.Errorf("minimax-m3 应标 cacheWriteAbsent: %v", m.Detail)
	}
	if _, present := m.Detail["cacheWrite"]; present {
		t.Error("缺失缓存写不应同时写 Detail[cacheWrite]")
	}
}

// 分档行:记 tierBands 计数,但**绝不**写 Detail["tiers"](该键在旧库已坏,window.go 明令禁读)。
// 分档不占 Shape(Shape 让给更「显著」的语义);分档与折扣可并存。
func TestParseCommandCodeTiered(t *testing.T) {
	bySlug := ccParseFixture(t)
	r := bySlug["qwen3-7-flash"] // 唯一 3 档行
	if r.Shape == domain.ShapeTiered {
		t.Errorf("qwen3-7-flash Shape 不应为 tiered(分档只作 Detail 留证)")
	}
	if v, _ := toFloat(r.Detail["tierBands"]); v != 3 {
		t.Errorf("qwen3-7-flash tierBands = %v, want 3", r.Detail["tierBands"])
	}
	if _, bad := r.Detail["tiers"]; bad {
		t.Error("不得写入 Detail[tiers](该键在旧库已坏)")
	}

	// grok-4-7 既 2 档又折扣:两者都要留住 —— Shape 取折扣,tierBands 留在 Detail。
	g := bySlug["grok-4-7"]
	if g.Shape != domain.ShapeDiscount {
		t.Errorf("grok-4-7 Shape = %q, want discount(折扣优先于分档)", g.Shape)
	}
	if v, _ := toFloat(g.Detail["tierBands"]); v != 2 {
		t.Errorf("grok-4-7 tierBands = %v, want 2(分档信息不得丢)", g.Detail["tierBands"])
	}
}

// model_name 必须落 slug(小写连字符),不能落显示名(含空格,永远匹配不上 canonicalModelKey)。
func TestCommandCodePersistsSlug(t *testing.T) {
	rows, err := parseCommandCode(loadFixture(t, ccFixture))
	if err != nil {
		t.Fatalf("parseCommandCode: %v", err)
	}
	for _, r := range rows {
		if strings.ContainsAny(r.Slug, " \t") {
			t.Errorf("slug %q 含空白", r.Slug)
		}
		if strings.Contains(r.Slug, "/") {
			t.Errorf("slug %q 不应含 '/'", r.Slug)
		}
		if r.Display == "" {
			t.Errorf("slug %q 缺显示名(Detail 留证需要)", r.Slug)
		}
	}
}

// 免费行被排除在落库之外,但必须在报告里可见(不得静默丢数据)。
func TestFetchCommandCodeExcludesFreeAndReports(t *testing.T) {
	// 直接测「解析 → 过滤免费 → 分组校验」这条不依赖网络的主干:
	rows, err := parseCommandCode(loadFixture(t, ccFixture))
	if err != nil {
		t.Fatalf("parseCommandCode: %v", err)
	}
	var free, priced int
	for _, r := range rows {
		if r.Free {
			free++
			// 免费行必须 in/out 都为 0 —— 否则过滤逻辑与判定不符。
			if r.In != 0 || r.Out != 0 {
				t.Errorf("免费行 %s 不应带价: %v/%v", r.Slug, r.In, r.Out)
			}
			continue
		}
		priced++
		// 落库行必须过一次 validate(免费行若混入会因全 0 拒绝整批)。
		if err := validate([]quote{r.quote()}, domain.CurrencyUSD, r.Shape); err != nil {
			t.Errorf("%s 未通过 validate: %v", r.Slug, err)
		}
	}
	if free != 3 {
		t.Errorf("免费行 = %d, want 3", free)
	}
	if priced != 78 {
		t.Errorf("可计价行 = %d, want 78", priced)
	}
}

// 未登记厂商前缀必须硬错(不得静默丢弃或猜测)。
func TestParseCommandCodeUnattributedFails(t *testing.T) {
	page := `<!doctype html><html><body><table>
<tr><th>Model</th><th>Context</th><th>Intelligence</th><th>Tok/s</th><th>Input</th><th>Output</th><th>Cache read</th><th>Cache write</th><th>Caps</th></tr>
<tr><td><a href="/models/zzz-1"><span class="truncate">Zzz 1</span></a></td><td>1M</td><td>-</td><td>-</td>
<td><span>$1.00</span></td><td><span>$2.00</span></td><td><span>$0.10</span></td><td><span>$1.25</span></td><td></td></tr>
</table></body></html>`
	_, err := parseCommandCode([]byte(page))
	if err == nil {
		t.Fatal("未登记厂商前缀应报错")
	}
	if !strings.Contains(err.Error(), "未登记厂商前缀") || !strings.Contains(err.Error(), "Zzz 1") {
		t.Errorf("错误应点名未归属模型,得到: %v", err)
	}
}

// 表头列缺失/列数不符必须硬错,不得错位取值。
func TestParseCommandCodeBadShapeFails(t *testing.T) {
	// 无 Model/Input/Output 表头。
	if _, err := parseCommandCode([]byte(`<html><body><table><tr><th>x</th></tr></table></body></html>`)); err == nil {
		t.Error("无模型表应报错")
	}
	// 数据行列数与表头不一致。
	bad := `<html><body><table>
<tr><th>Model</th><th>Input</th><th>Output</th></tr>
<tr><td><a href="/models/gpt-1"><span class="truncate">GPT-1</span></a></td><td>$1</td></tr>
</table></body></html>`
	if _, err := parseCommandCode([]byte(bad)); err == nil {
		t.Error("列数不一致应报错")
	}
}

// 峰谷时段解析的纯函数单测(含 UTC 与不支持的写法)。
func TestCCPeakWindows(t *testing.T) {
	ws, err := ccPeakWindows("01–04 & 06–10 UTC, Mon–Fri")
	if err != nil {
		t.Fatalf("ccPeakWindows: %v", err)
	}
	if len(ws) != 2 || !ws[0].TZSet || ws[0].TZOffsetMin != 0 {
		t.Fatalf("窗口 = %+v", ws)
	}
	// 无 UTC 标注 → 硬错(不猜时区)。
	if _, err := ccPeakWindows("01–04, Mon–Fri"); err == nil {
		t.Error("无 UTC 标注应报错")
	}
	// 未知星期 → 硬错。
	if _, err := ccPeakWindows("01–04 UTC, Mon-Fridayish"); err == nil {
		t.Error("未知星期应报错")
	}
	// 跨零点区间 → 硬错。
	if _, err := ccPeakWindows("22–02 UTC, Mon–Fri"); err == nil {
		t.Error("跨零点区间应报错")
	}
}

// CC 域名白名单:非白名单 host 直接拒绝(传输层)。
func TestCommandCodeAllowlist(t *testing.T) {
	base := http.Client{Transport: &fakeRT{}}
	c := AllowlistClient(base, CommandCodeHosts())
	req, _ := http.NewRequest(http.MethodGet, "https://commandcode.ai/models", nil)
	if _, err := c.Do(req); err != nil {
		t.Fatalf("CC 域名应放行: %v", err)
	}
	req2, _ := http.NewRequest(http.MethodGet, "https://evil.example.com/models", nil)
	if _, err := c.Do(req2); err == nil {
		t.Fatal("非白名单域名应拒绝")
	}
}
