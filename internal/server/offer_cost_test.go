package server

import (
	"net/http"
	"net/http/cookiejar"
	"testing"

	"personal-ai-gateway/internal/domain"
)

// TestOfferCostQuoteDerivedFromOfficial 「每渠道每模型单独核算」的管理面验收:
// 同一模型挂两个渠道,渠道系数不同 → 两个供给源的成本不同,而**售价相同**。
//
// 这正是改造前做不到的事:成本挂在 offer 标量上,同一模型两条渠道得手工填两遍,
// 官方价一变还要再填一遍。
func TestOfferCostQuoteDerivedFromOfficial(t *testing.T) {
	srv, admin, st := newTestServer(t)
	base := srv.URL
	bootstrap(t, admin, base)

	en := true
	// 两个渠道的 provider 都写成 OpenAI(聚合中转渠道的真实形态)——
	// 系数查找键若取 channels.provider,两个渠道都会查不到系数、成本静默变 1.0 倍。
	chA, err := st.CreateChannel(domain.ChannelInput{
		Name: "commandcode", Provider: domain.ProviderOpenAI, BaseURL: "http://a.example/v1",
		APIKey: "sk-a", Priority: 1, Enabled: &en, TimeoutMs: 30000, MaxFailures: 2, CooldownSec: 10,
	})
	if err != nil {
		t.Fatalf("create channel A: %v", err)
	}
	chB, err := st.CreateChannel(domain.ChannelInput{
		Name: "xsjiang", Provider: domain.ProviderOpenAI, BaseURL: "http://b.example/v1",
		APIKey: "sk-b", Priority: 2, Enabled: &en, TimeoutMs: 30000, MaxFailures: 2, CooldownSec: 10,
	})
	if err != nil {
		t.Fatalf("create channel B: %v", err)
	}
	m, err := st.CreateModel(domain.ModelInput{Name: "deepseek/deepseek-v4-pro", ContextWindow: 128000})
	if err != nil {
		t.Fatalf("create model: %v", err)
	}
	for _, chID := range []int64{chA.ID, chB.ID} {
		if _, err := st.CreateOffer(m.ID, domain.OfferInput{
			ChannelID: chID, Enabled: boolPtrT(true),
		}); err != nil {
			t.Fatalf("create offer: %v", err)
		}
	}
	vendor, oname := string(domain.ProviderDeepSeek), "deepseek-v4-pro"
	if _, err := st.UpdateModel(m.ID, domain.ModelInput{OfficialVendor: &vendor, OfficialModelName: &oname}); err != nil {
		t.Fatalf("bind official: %v", err)
	}
	// 官方分时价:谷 ¥1/¥4,峰 ¥2/¥8(detail 带机器可读 windows)。
	if _, err := st.UpsertOfficialPrice(domain.OfficialPriceRow{
		Provider: domain.ProviderDeepSeek, ModelName: oname,
		Currency: domain.CurrencyCNY, BillingShape: domain.ShapePeakOff,
		InputPrice: 1, OutputPrice: 4, CacheReadPrice: 0.02,
		Detail: map[string]any{
			"offpeak": map[string]any{"in": 1.0, "out": 4.0, "cacheRead": 0.02},
			"peak":    map[string]any{"in": 2.0, "out": 8.0, "cacheRead": 0.04},
			"windows": []any{map[string]any{
				"days": []any{1, 2, 3, 4, 5}, "start": "09:00", "end": "12:00", "tzOffsetMin": 480,
			}},
		},
	}); err != nil {
		t.Fatalf("upsert official: %v", err)
	}
	// chA 的 DeepSeek credit 套餐:$10 买 $60 → 系数 1/6。chB 不配 → 1.0。
	if err := st.SetCostRatio(chA.ID, domain.ProviderDeepSeek, 1.0/6.0, "$10→$60"); err != nil {
		t.Fatalf("set ratio: %v", err)
	}
	// 倍率 1.0:售价 = 官方价原价。
	settings, _ := st.GetSettings()
	settings.DisplayCurrency = domain.CurrencyCNY
	settings.PriceMultiplier = 1.0
	if err := st.SaveSettings(settings); err != nil {
		t.Fatalf("save settings: %v", err)
	}

	adm := decode[[]map[string]any](t, mustGet(t, admin, base+"/api/v1/models"))
	if len(adm) != 1 {
		t.Fatalf("models = %+v", adm)
	}
	offers := adm[0]["offers"].([]any)
	if len(offers) != 2 {
		t.Fatalf("offers = %+v", offers)
	}
	byChannel := map[string]map[string]any{}
	for _, o := range offers {
		om := o.(map[string]any)
		byChannel[om["channelName"].(string)] = om["cost"].(map[string]any)
	}
	a, b := byChannel["commandcode"], byChannel["xsjiang"]
	if a == nil || b == nil {
		t.Fatalf("cost 缺失: %+v", byChannel)
	}
	// chA:官方谷价 × 1/6 = 0.1667/0.6667/0.0033。
	approx(t, "chA.cost.in", a["in"].(float64), 1.0/6.0)
	approx(t, "chA.cost.out", a["out"].(float64), 4.0/6.0)
	approx(t, "chA.cost.ratio", a["ratio"].(float64), 1.0/6.0)
	// chB:未配系数 → 1.0,成本等于官方价,且**必须带 warn** 说明这是未设而非真按 1.0 谈的。
	approx(t, "chB.cost.in", b["in"].(float64), 1)
	approx(t, "chB.cost.ratio", b["ratio"].(float64), 1.0)
	if w, _ := b["warn"].(string); w == "" {
		t.Errorf("未设系数的渠道必须带 warn,实际 %+v", b)
	}
	if a["source"] != "official" || b["source"] != "official" {
		t.Errorf("source = %v/%v, want official", a["source"], b["source"])
	}
}

// TestOfferCostQuoteUnknownShowsNoMargin 无任何成本依据的模型(**生产里 91 条 offer 的绝大多数**)
// 必须给出 source=unknown 且三价全 0 —— 前端据此隐藏毛利列。
//
// 若这里返回 0 成本而 source 是 offer/official,页面就会显示「成本 0、毛利 100%」,
// 那正是改造前那个假 100% 毛利的来源。
func TestOfferCostQuoteUnknownShowsNoMargin(t *testing.T) {
	srv, admin, st := newTestServer(t)
	base := srv.URL
	bootstrap(t, admin, base)

	en := true
	ch, err := st.CreateChannel(domain.ChannelInput{
		Name: "ch", Provider: domain.ProviderOpenAI, BaseURL: "http://u.example/v1",
		APIKey: "sk", Priority: 1, Enabled: &en, TimeoutMs: 30000, MaxFailures: 2, CooldownSec: 10,
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	m, err := st.CreateModel(domain.ModelInput{Name: "gpt-5.5", ContextWindow: 128000})
	if err != nil {
		t.Fatalf("create model: %v", err)
	}
	// 三价全 0 —— 生产快照里 91/95 条 offer 就是这个样子。
	if _, err := st.CreateOffer(m.ID, domain.OfferInput{ChannelID: ch.ID, Enabled: boolPtrT(true)}); err != nil {
		t.Fatalf("create offer: %v", err)
	}

	adm := decode[[]map[string]any](t, mustGet(t, admin, base+"/api/v1/models"))
	offers := adm[0]["offers"].([]any)
	c := offers[0].(map[string]any)["cost"].(map[string]any)
	if c["source"] != "unknown" {
		t.Errorf("source = %v, want unknown", c["source"])
	}
	for _, k := range []string{"in", "out", "cacheRead"} {
		if v, ok := c[k].(float64); ok && v != 0 {
			t.Errorf("unknown 成本必须全 0,%s = %v", k, v)
		}
	}
}

// TestUserModelsShowPeakBothTiers 客户面分时模型必须**并列**列出谷/峰两价:
// 报价是「现在买多少钱」,而计费按请求时刻选档 —— 只给单值会让客户看到谷价、
// 恰在峰时段请求被按峰价收费,事后无法解释。
func TestUserModelsShowPeakBothTiers(t *testing.T) {
	srv, admin, st := newTestServer(t)
	base := srv.URL
	bootstrap(t, admin, base)

	en := true
	ch, err := st.CreateChannel(domain.ChannelInput{
		Name: "ch", Provider: domain.ProviderDeepSeek, BaseURL: "http://u.example/v1",
		APIKey: "sk", Priority: 1, Enabled: &en, TimeoutMs: 30000, MaxFailures: 2, CooldownSec: 10,
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	m, err := st.CreateModel(domain.ModelInput{Name: "deepseek-v4-pro", ContextWindow: 128000})
	if err != nil {
		t.Fatalf("create model: %v", err)
	}
	if _, err := st.CreateOffer(m.ID, domain.OfferInput{ChannelID: ch.ID, Enabled: boolPtrT(true)}); err != nil {
		t.Fatalf("create offer: %v", err)
	}
	vendor, oname := string(domain.ProviderDeepSeek), "deepseek-v4-pro"
	if _, err := st.UpdateModel(m.ID, domain.ModelInput{OfficialVendor: &vendor, OfficialModelName: &oname}); err != nil {
		t.Fatalf("bind official: %v", err)
	}
	if _, err := st.UpsertOfficialPrice(domain.OfficialPriceRow{
		Provider: domain.ProviderDeepSeek, ModelName: oname,
		Currency: domain.CurrencyCNY, BillingShape: domain.ShapePeakOff,
		InputPrice: 1, OutputPrice: 4, CacheReadPrice: 0.02,
		Detail: map[string]any{
			"offpeak":   map[string]any{"in": 1.0, "out": 4.0, "cacheRead": 0.02},
			"peak":      map[string]any{"in": 2.0, "out": 8.0, "cacheRead": 0.04},
			"peakHours": "北京时间周一至周五 9:00-12:00、14:00-18:00(其余为空闲时段)",
		},
	}); err != nil {
		t.Fatalf("upsert official: %v", err)
	}
	settings, _ := st.GetSettings()
	settings.DisplayCurrency = domain.CurrencyCNY
	settings.PriceMultiplier = 1.0
	if err := st.SaveSettings(settings); err != nil {
		t.Fatalf("save settings: %v", err)
	}

	createUser(t, admin, base, "cust2", "password123", "user")
	jar, _ := cookiejar.New(nil)
	cust := &http.Client{Jar: jar}
	loginAsUser(t, cust, base, "cust2", "password123")

	views := decode[[]map[string]any](t, mustGet(t, cust, base+"/api/v1/models"))
	if len(views) != 1 {
		t.Fatalf("user models = %+v", views)
	}
	v := views[0]
	if v["peakVaries"] != true {
		t.Fatalf("分时模型应标 peakVaries,实际 %+v", v)
	}
	ret := v["retail"].(map[string]any)
	peak := v["peakRetail"].(map[string]any)
	// 谷价 ¥1/¥4,峰价 ¥2/¥8 —— 并列展示,客户两档都能看到。
	approx(t, "retail.input", ret["input"].(float64), 1)
	approx(t, "peakRetail.input", peak["input"].(float64), 2)
	approx(t, "peakRetail.output", peak["output"].(float64), 8)
	if v["peakHours"] == nil || v["peakHours"] == "" {
		t.Errorf("峰时段说明应当透出,实际 %+v", v["peakHours"])
	}
}
