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
