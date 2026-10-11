package server

import (
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"

	"personal-ai-gateway/internal/domain"
)

// TestModelsListRoleConvergence 管理员看全量;普通用户看收敛清单 ——
// 无渠道名、无上游真实名、无官方价来源 URL、无成本、无全站用量。
func TestModelsListRoleConvergence(t *testing.T) {
	srv, admin, st := newTestServer(t)
	base := srv.URL
	bootstrap(t, admin, base)

	// 造一个渠道 + 模型 + 供给源,并绑官方价。
	// 渠道名与上游真实名刻意取得「一眼能认出」,便于断言用户面不泄漏。
	en := true
	ch, err := st.CreateChannel(domain.ChannelInput{
		Name: "内部聚合渠道", Provider: domain.ProviderOpenAI, BaseURL: "http://up.example/v1",
		APIKey: "sk-secret", Priority: 1, Enabled: &en, TimeoutMs: 30000, MaxFailures: 2, CooldownSec: 10,
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	m, err := st.CreateModel(domain.ModelInput{Name: "claude-sonnet-5", ContextWindow: 200000})
	if err != nil {
		t.Fatalf("create model: %v", err)
	}
	if _, err := st.CreateOffer(m.ID, domain.OfferInput{
		ChannelID: ch.ID, InputPriceUsd: 8.4, OutputPriceUsd: 42,
		UpstreamModel: "upstream-secret-name", OverridePrice: true,
		PriceSourceURL: "https://anthropic.com/pricing", Enabled: boolPtrT(true),
	}); err != nil {
		t.Fatalf("create offer: %v", err)
	}
	vendor, oname := string(domain.ProviderAnthropic), "claude-sonnet-5-official"
	if _, err := st.UpdateModel(m.ID, domain.ModelInput{OfficialVendor: &vendor, OfficialModelName: &oname}); err != nil {
		t.Fatalf("bind official: %v", err)
	}
	if _, err := st.UpsertOfficialPrice(domain.OfficialPriceRow{
		Provider: domain.ProviderAnthropic, ModelName: oname,
		Currency: domain.CurrencyUSD, BillingShape: domain.ShapeFlat,
		InputPrice: 3, OutputPrice: 15, SourceURL: "https://anthropic.com/pricing",
	}); err != nil {
		t.Fatalf("upsert official: %v", err)
	}
	// 计价 CNY + 汇率 0.1 → 官方 ¥30/¥150;倍率 0.5 → 本站价 ¥15/¥75。
	settings, _ := st.GetSettings()
	settings.DisplayCurrency = domain.CurrencyCNY
	settings.USDPerCNY = 0.1
	settings.PriceMultiplier = 0.5
	if err := st.SaveSettings(settings); err != nil {
		t.Fatalf("save settings: %v", err)
	}

	// 建客户账号并登录(独立 cookie jar,不串 admin 会话)。
	code, body := doJSON(t, admin, http.MethodPost, base+"/api/v1/users",
		map[string]any{"username": "cust", "password": "password123", "role": "user"})
	mustStatus(t, code, http.StatusOK, "create user: "+string(body))
	jar, _ := cookiejar.New(nil)
	cust := &http.Client{Jar: jar}
	code, body = doJSON(t, cust, http.MethodPost, base+"/api/v1/auth/login",
		map[string]any{"username": "cust", "password": "password123"})
	mustStatus(t, code, http.StatusOK, "cust login: "+string(body))

	// —— 客户视角:收敛清单 ——
	raw := mustGet(t, cust, base+"/api/v1/models")
	views := decode[[]map[string]any](t, raw)
	if len(views) != 1 {
		t.Fatalf("user models = %+v", views)
	}
	v := views[0]
	if v["name"] != "claude-sonnet-5" {
		t.Fatalf("name = %v", v["name"])
	}
	// 两行价格:官方价(划线 ¥30/¥150)+ 本站价(¥15/¥75 = 官方 × 0.5)。
	off := v["official"].(map[string]any)
	ret := v["retail"].(map[string]any)
	approx(t, "official.input", off["input"].(float64), 30)
	approx(t, "official.output", off["output"].(float64), 150)
	approx(t, "retail.input", ret["input"].(float64), 15)
	approx(t, "retail.output", ret["output"].(float64), 75)

	// 泄漏断言:整段 JSON 不得出现渠道名/上游真实名/来源 URL/成本字段与数字。
	s := string(raw)
	for _, leak := range []string{
		"内部聚合渠道", "upstream-secret-name", "anthropic.com/pricing",
		"channelName", "upstreamModel", "priceSourceUrl", "costUsd",
		"8.4", "42", // 成本价(输入 8.4 / 输出 42)
	} {
		if strings.Contains(s, leak) {
			t.Fatalf("用户面 /models 泄漏 %q:\n%s", leak, s)
		}
	}

	// —— 管理员视角仍是全量(含 offers:渠道名/成本) ——
	adm := decode[[]map[string]any](t, mustGet(t, admin, base+"/api/v1/models"))
	if len(adm) != 1 {
		t.Fatalf("admin models = %+v", adm)
	}
	if adm[0]["offers"] == nil {
		t.Fatalf("管理员应看到 offers(含渠道/成本),实际 %+v", adm[0])
	}
}

func approx(t *testing.T, label string, got, want float64) {
	t.Helper()
	if got < want-1e-6 || got > want+1e-6 {
		t.Fatalf("%s = %v, want %v", label, got, want)
	}
}

func boolPtrT(b bool) *bool { return &b }

// TestModelsListPeakPrices 分时模型:官方价分档时,官方与本站两行**都**拆高峰/空闲两档
// (official=空闲档,peakOfficial=高峰档;retail/peakRetail 各自 = 对应官方档 × 倍率)。
// 官方不分档(上面的 flat 用例)时 peakOfficial/peakVaries 皆**不出现** —— 「官方不区分则本站不区分」。
func TestModelsListPeakPrices(t *testing.T) {
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
	m, err := st.CreateModel(domain.ModelInput{Name: "deepseek/deepseek-v4-pro", ContextWindow: 128000})
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
	// 官方分时价(CNY):谷 ¥1/¥4,峰 ¥2/¥8。
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
			"peakHours": "北京时间周一至周五 9:00-12:00",
		},
	}); err != nil {
		t.Fatalf("upsert official: %v", err)
	}
	// 同币种(CNY)、倍率 2 → 本站价 = 官方价 × 2。
	settings, _ := st.GetSettings()
	settings.DisplayCurrency = domain.CurrencyCNY
	settings.PriceMultiplier = 2
	if err := st.SaveSettings(settings); err != nil {
		t.Fatalf("save settings: %v", err)
	}

	cust := loginAs(t, admin, base, "cust", "password123")
	raw := mustGet(t, cust, base+"/api/v1/models")
	views := decode[[]map[string]any](t, raw)
	if len(views) != 1 {
		t.Fatalf("user models = %+v", views)
	}
	v := views[0]
	if v["peakVaries"] != true {
		t.Fatalf("peakVaries = %v, want true", v["peakVaries"])
	}
	off := v["official"].(map[string]any)
	pk := v["peakOfficial"].(map[string]any) // 官方高峰档必须有值
	ret := v["retail"].(map[string]any)
	pret := v["peakRetail"].(map[string]any)
	approx(t, "official.input(空闲)", off["input"].(float64), 1)
	approx(t, "official.output(空闲)", off["output"].(float64), 4)
	approx(t, "peakOfficial.input(高峰)", pk["input"].(float64), 2)
	approx(t, "peakOfficial.output(高峰)", pk["output"].(float64), 8)
	approx(t, "retail.input(空闲)=官方谷×2", ret["input"].(float64), 2)
	approx(t, "retail.output(空闲)", ret["output"].(float64), 8)
	approx(t, "peakRetail.input(高峰)=官方峰×2", pret["input"].(float64), 4)
	approx(t, "peakRetail.output(高峰)", pret["output"].(float64), 16)
}

// loginAs 建一个指定角色的账号并以其 cookie jar 登录,返回该客户端。
func loginAs(t *testing.T, admin *http.Client, base, username, password string) *http.Client {
	t.Helper()
	code, body := doJSON(t, admin, http.MethodPost, base+"/api/v1/users",
		map[string]any{"username": username, "password": password, "role": "user"})
	mustStatus(t, code, http.StatusOK, "create user: "+string(body))
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	code, body = doJSON(t, c, http.MethodPost, base+"/api/v1/auth/login",
		map[string]any{"username": username, "password": password})
	mustStatus(t, code, http.StatusOK, "login: "+string(body))
	return c
}
