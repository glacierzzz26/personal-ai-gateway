package pricing

import (
	"net/http"
	"strings"
	"testing"

	"personal-ai-gateway/internal/domain"
)

// 夹具:2026-10-08 从 https://opencode.ai/docs/zen/ 实存(109648 字节)。
// 3 张表:清单表(Model|Model ID|…)、定价表(Model|Input|Output|Cached Read|Cached Write)、弃用表。
const ocFixture = "opencode_zen.html"

// ocParseFixture 解析夹具,返回定价表全部数据行(**含免费行**)。
func ocParseFixture(t *testing.T) []ocParsed {
	t.Helper()
	rows, err := parseOpenCode(loadFixture(t, ocFixture))
	if err != nil {
		t.Fatalf("parseOpenCode: %v", err)
	}
	return rows
}

// 页面结构指纹:行数/免费数/合并数/分档数/折扣数任一漂移即失败 —— 让 opencode 改版在此处炸,
// 而不是在生产里静默记错价。
func TestParseOpenCodeCounts(t *testing.T) {
	rows := ocParseFixture(t)
	if len(rows) != 102 {
		t.Fatalf("总行数 = %d, want 102", len(rows))
	}
	var free int
	priced := make([]ocParsed, 0, len(rows))
	for _, r := range rows {
		if r.Free {
			free++
			continue
		}
		priced = append(priced, r)
	}
	if free != 13 {
		t.Errorf("免费行 = %d, want 13", free)
	}
	merged := ocMerge(priced)
	if len(merged) != 74 {
		t.Errorf("合并后行数 = %d, want 74(15 个 slug 各有 2 档)", len(merged))
	}
	var tiered, discount int
	for _, m := range merged {
		if m.Shape == domain.ShapeTiered {
			tiered++
		}
		if m.Discount {
			discount++
		}
	}
	if tiered != 15 {
		t.Errorf("分档 slug = %d, want 15", tiered)
	}
	if discount != 1 {
		t.Errorf("折扣 slug = %d, want 1(Mistral Large 4)", discount)
	}
}

// 厂商归属必须覆盖全部可计价行,且分布与实测一致;Mistral 为新登记厂商。
func TestParseOpenCodeVendorDistribution(t *testing.T) {
	rows := ocParseFixture(t)
	got := map[domain.Provider]int{}
	for _, r := range rows {
		if r.Free {
			continue
		}
		if r.Provider == "" {
			t.Fatalf("模型 %q(%s)未归属厂商", r.Display, r.Slug)
		}
		got[r.Provider]++
	}
	// 分档 slug 会出现两次,故按厂商计数含分档重复(89 = 74 + 15)。
	want := map[domain.Provider]int{
		domain.ProviderOpenAI: 24 + 9, domain.ProviderAnthropic: 13 + 2, domain.ProviderGoogle: 7 + 1,
		domain.ProviderQwen: 6, domain.ProviderDeepSeek: 4, domain.ProviderMoonshot: 4,
		domain.ProviderXAI: 4 + 3, domain.ProviderZhipu: 5, domain.ProviderMiniMax: 3,
		domain.ProviderMeta: 2, domain.ProviderJev: 1, domain.ProviderMistral: 1,
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

// slug 必须来自清单表 Model ID(落库 model_name),且分档合并后每 slug 唯一。
func TestParseOpenCodeSlugAndMerge(t *testing.T) {
	rows := ocParseFixture(t)
	// Claude 系 slug 与 CC 完全相同(这正是需要 source 维度的原因)。
	byDisplay := map[string]ocParsed{}
	for _, r := range rows {
		byDisplay[r.Display] = r
	}
	if s := byDisplay["Claude Sonnet 5"].Slug; s != "claude-sonnet-5" {
		t.Errorf("Claude Sonnet 5 slug = %q, want claude-sonnet-5", s)
	}
	// 分档行剥括号后仍 join 到同一 slug。
	if s := byDisplay["Claude Sonnet 4.5 (> 200K tokens)"].Slug; s != "claude-sonnet-4-5" {
		t.Errorf("分档行 slug = %q, want claude-sonnet-4-5", s)
	}
	merged := ocMerge(rows)
	seen := map[string]int{}
	for _, m := range merged {
		seen[m.Slug]++
		if strings.ContainsAny(m.Slug, " \t/") {
			t.Errorf("slug %q 含空白或 '/'", m.Slug)
		}
	}
	for s, n := range seen {
		if n != 1 {
			t.Errorf("slug %q 出现 %d 次,合并后应唯一", s, n)
		}
	}
}

// 分档:标量取基准档(阈值最小档),明细写 Detail["ocTiers"],**绝不**写 Detail["tiers"]。
func TestParseOpenCodeTiered(t *testing.T) {
	merged := ocMerge(ocParseFixture(t))
	bySlug := map[string]ocMerged{}
	for _, m := range merged {
		bySlug[m.Slug] = m
	}
	m, ok := bySlug["claude-sonnet-4-5"]
	if !ok {
		t.Fatal("缺少 claude-sonnet-4-5")
	}
	if m.Shape != domain.ShapeTiered {
		t.Errorf("claude-sonnet-4-5 shape = %q, want tiered", m.Shape)
	}
	// 基准档 = ≤ 200K:$3.00/$15.00。
	if m.In != 3.00 || m.Out != 15.00 {
		t.Errorf("claude-sonnet-4-5 基准档 = %v/%v, want 3.00/15.00", m.In, m.Out)
	}
	d := ocDetail(m)
	if v, _ := toFloat(d["tierBands"]); v != 2 {
		t.Errorf("tierBands = %v, want 2", d["tierBands"])
	}
	tiers, ok := d["ocTiers"].([]map[string]any)
	if !ok || len(tiers) != 2 {
		t.Fatalf("ocTiers 缺失或档数不对: %v", d["ocTiers"])
	}
	if v, _ := toFloat(tiers[0]["maxTokens"]); v != 200000 {
		t.Errorf("首档 maxTokens = %v, want 200000", tiers[0]["maxTokens"])
	}
	if _, bad := d["tiers"]; bad {
		t.Error("不得写入 Detail[tiers](该键在旧库已坏)")
	}
}

// 折扣:Mistral Large 4 用 <del> 标原价,取**现价**作标量,原价进 Detail["deal"]。
func TestParseOpenCodeDiscount(t *testing.T) {
	merged := ocMerge(ocParseFixture(t))
	bySlug := map[string]ocMerged{}
	for _, m := range merged {
		bySlug[m.Slug] = m
	}
	m, ok := bySlug["mistral-large-4"]
	if !ok {
		t.Fatal("缺少 mistral-large-4")
	}
	if m.Shape != domain.ShapeDiscount {
		t.Errorf("mistral-large-4 shape = %q, want discount", m.Shape)
	}
	if m.In != 0.68 || m.Out != 2.09 || m.CacheRead != 0.07 {
		t.Errorf("mistral-large-4 现价 = %v/%v/%v, want 0.68/2.09/0.07", m.In, m.Out, m.CacheRead)
	}
	if m.In == 1.36 {
		t.Error("mistral-large-4 取了删除线原价(1.36)—— 现价应是 0.68")
	}
	if m.Provider != domain.ProviderMistral {
		t.Errorf("mistral-large-4 厂商 = %q, want Mistral", m.Provider)
	}
	d, _ := ocDetail(m)["deal"].(map[string]any)
	if d["percent"] != 50 {
		t.Errorf("deal.percent = %v, want 50", d["percent"])
	}
	orig, _ := d["original"].(map[string]any)
	if v, _ := toFloat(orig["in"]); v != 1.36 {
		t.Errorf("deal.original.in = %v, want 1.36", orig["in"])
	}
}

// 免费剔除必须在厂商归属之前:无厂商前缀的昵称模型(Big Pickle 等)应作为免费行上报,
// 而**不**触发「未登记厂商」硬错。
func TestParseOpenCodeFreeNicknames(t *testing.T) {
	rows := ocParseFixture(t) // parseOpenCode 内部若把免费行当付费行归属,这里会直接 fatal
	var free []string
	for _, r := range rows {
		if r.Free {
			free = append(free, r.Display)
		}
	}
	for _, want := range []string{"Big Pickle", "Space Bunny Free", "Exo Free", "Fledge Alpha Free"} {
		found := false
		for _, f := range free {
			if f == want {
				found = true
			}
		}
		if !found {
			t.Errorf("免费行 %q 未识别", want)
		}
	}
}

// 单侧 Free(输入有价、输出 Free)不整行判免费 —— Jev 1.13 应保留,输出按 0 计。
func TestParseOpenCodePartialFreeKept(t *testing.T) {
	rows := ocParseFixture(t)
	var jev *ocParsed
	for i := range rows {
		if rows[i].Slug == "jev-1.13" {
			jev = &rows[i]
		}
	}
	if jev == nil {
		t.Fatal("缺少 jev-1.13")
	}
	if jev.Free {
		t.Error("jev-1.13 输入有价,不应整行判免费")
	}
	if jev.In != 0.042 || jev.Out != 0 {
		t.Errorf("jev-1.13 = %v/%v, want 0.042/0", jev.In, jev.Out)
	}
}

// 缓存写列缺失(`-`)→ CacheWriteOff,计费回落 input 价。
func TestParseOpenCodeCacheWrite(t *testing.T) {
	merged := ocMerge(ocParseFixture(t))
	bySlug := map[string]ocMerged{}
	for _, m := range merged {
		bySlug[m.Slug] = m
	}
	// Claude Sonnet 5 有缓存写 2.50(输入 2.00 的 1.25 倍)。
	s := bySlug["claude-sonnet-5"]
	if s.CacheWriteOff {
		t.Fatal("claude-sonnet-5 不应缺缓存写")
	}
	if s.CacheWrite != 2.50 {
		t.Errorf("claude-sonnet-5 cacheWrite = %v, want 2.50", s.CacheWrite)
	}
	// DeepSeek 缓存写为 `-`。
	d := bySlug["deepseek-v4.1-flash"]
	if !d.CacheWriteOff {
		t.Error("deepseek-v4.1-flash 缓存写应为缺失")
	}
	if dd := ocDetail(d); dd["cacheWriteAbsent"] != true {
		t.Errorf("应标 cacheWriteAbsent: %v", dd)
	}
}

// 未登记厂商前缀必须硬错(付费行);免费昵称行不在此列。
func TestParseOpenCodeUnattributedFails(t *testing.T) {
	page := `<!doctype html><html><body>
<table><tr><th>Model</th><th>Model ID</th><th>Endpoint</th><th>AI SDK Package</th></tr>
<tr><td>Zzz 1</td><td>zzz-1</td><td>/x</td><td>x</td></tr></table>
<table><tr><th>Model</th><th>Input</th><th>Output</th><th>Cached Read</th><th>Cached Write</th></tr>
<tr><td>Zzz 1</td><td>$1.00</td><td>$2.00</td><td>$0.10</td><td>-</td></tr></table>
</body></html>`
	_, err := parseOpenCode([]byte(page))
	if err == nil {
		t.Fatal("未登记厂商前缀应报错")
	}
	if !strings.Contains(err.Error(), "未登记厂商前缀") || !strings.Contains(err.Error(), "Zzz 1") {
		t.Errorf("错误应点名未归属模型,得到: %v", err)
	}
}

// opencode 域名白名单:非白名单 host 直接拒绝(传输层)。
func TestOpenCodeAllowlist(t *testing.T) {
	base := http.Client{Transport: &fakeRT{}}
	c := AllowlistClient(base, OpenCodeHosts())
	req, _ := http.NewRequest(http.MethodGet, "https://opencode.ai/docs/zen/", nil)
	if _, err := c.Do(req); err != nil {
		t.Fatalf("opencode 域名应放行: %v", err)
	}
	req2, _ := http.NewRequest(http.MethodGet, "https://evil.example.com/zen", nil)
	if _, err := c.Do(req2); err == nil {
		t.Fatal("非白名单域名应拒绝")
	}
}
