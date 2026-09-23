package server

import (
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"personal-ai-gateway/internal/config"
	"personal-ai-gateway/internal/domain"
	"personal-ai-gateway/internal/secret"
	"personal-ai-gateway/internal/store"
)

// newTestServerS 同 newTestServer,但额外返回 *Server 以便注入 pricingBase(仅测试用)。
func newTestServerS(t *testing.T) (*httptest.Server, *http.Client, *store.Store, *Server) {
	t.Helper()
	dir := t.TempDir()
	if _, err := secret.BootstrapKey(dir); err != nil {
		t.Fatalf("bootstrap master key: %v", err)
	}
	st, err := store.Open(filepath.Join(dir, "gw.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	s := New(config.Config{}, st)
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	return srv, &http.Client{Jar: jar}, st, s
}

// redirectRT 把出站请求改写到本地假官方页(仅测试)。
// 注意:它被塞进 AllowlistClient 的 base.Transport —— 白名单校验发生在更外层,
// 故「只允许官方域名」在测试注入下依旧被强制,本用例因此同时验证了该保证。
type redirectRT struct{ target string }

func (t *redirectRT) RoundTrip(req *http.Request) (*http.Response, error) {
	u, _ := url.Parse(t.target)
	r2 := req.Clone(req.Context())
	r2.URL.Scheme = u.Scheme
	r2.URL.Host = u.Host
	r2.Host = ""
	return http.DefaultTransport.RoundTrip(r2)
}

// mkChannelOf 建一个指定 provider 的渠道供定价测试。
func mkChannelOf(t *testing.T, c *http.Client, base string, name string, p domain.Provider) int64 {
	t.Helper()
	code, body := doJSON(t, c, http.MethodPost, base+"/api/v1/channels", map[string]any{
		"name": name, "provider": string(p), "baseUrl": "https://upstream.example.com",
	})
	mustStatus(t, code, http.StatusOK, "create channel")
	ch := decode[domain.ChannelRead](t, body)
	return ch.ID
}

// TestFetchPricingManualOnlyProvider 智谱页面为动态渲染,抓取必须显式报错(不得静默成功)。
func TestFetchPricingManualOnlyProvider(t *testing.T) {
	srv, c, _ := newTestServer(t)
	base := srv.URL
	bootstrap(t, c, base)
	zid := mkChannelOf(t, c, base, "zhipu", domain.ProviderZhipu)

	code, body := doJSON(t, c, http.MethodPost, base+"/api/v1/channels/"+itoa(zid)+"/fetch-pricing", nil)
	mustStatus(t, code, http.StatusBadRequest, "zhipu fetch pricing")
	e := decode[struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}](t, body)
	if e.Error.Type != "manual_only" {
		t.Fatalf("type = %q, body %s", e.Error.Type, body)
	}
}

// TestFetchPricingUnsupportedProvider 中转站不是厂商,无官方单价来源 → 400。
// (S4 后 OpenAI/Anthropic 已放开为「仅手工录入」,不再是 unsupported。)
func TestFetchPricingUnsupportedProvider(t *testing.T) {
	srv, c, _ := newTestServer(t)
	base := srv.URL
	bootstrap(t, c, base)
	oid := mkChannelOf(t, c, base, "agg", domain.Provider("示例中转站"))

	code, body := doJSON(t, c, http.MethodPost, base+"/api/v1/channels/"+itoa(oid)+"/fetch-pricing", nil)
	mustStatus(t, code, http.StatusBadRequest, "agg fetch pricing")
	if !strings.Contains(string(body), "无受支持的官方单价页面") {
		t.Fatalf("unexpected body: %s", body)
	}
}

// TestManualPriceEntryAndApply 手工录入官方价 → 应用 → offer 三价 + 来源留证落库,override_price 不动。
func TestManualPriceEntryAndApply(t *testing.T) {
	srv, c, st := newTestServer(t)
	base := srv.URL
	bootstrap(t, c, base)
	chID := mkChannelOf(t, c, base, "zhipu", domain.ProviderZhipu)

	// 手工录入(智谱 GLM,人民币,限时折扣)。
	code, body := doJSON(t, c, http.MethodPost, base+"/api/v1/official-prices/manual", map[string]any{
		"provider": string(domain.ProviderZhipu), "modelName": "glm-4.6",
		"sourceUrl": "https://bigmodel.cn/pricing", "currency": "CNY",
		"inputPrice": 0.1, "outputPrice": 0.2, "nativeText": "限时5折",
	})
	mustStatus(t, code, http.StatusOK, "manual price")
	op := decode[domain.OfficialPriceView](t, body)
	if op.ID == 0 || op.ModelName != "glm-4.6" {
		t.Fatalf("manual row bad: %+v", op)
	}
	// 官方价为人民币、计价币种同为人民币(默认)→ 可直接应用,不需要汇率。
	if !op.RateSet {
		t.Error("rateSet should be true when official currency == display currency")
	}
	if !closeTo(op.InputPriceUsd, 0.1) || !closeTo(op.OutputPriceUsd, 0.2) {
		t.Errorf("same-currency price must pass through untouched: %+v", op)
	}

	// 挂一个手工覆盖价 offer。
	m, err := st.CreateModel(domain.ModelInput{Name: "glm-4.6", ContextWindow: 128000})
	mustNoErrT(t, err, "create model")
	of, err := st.CreateOffer(m.ID, domain.OfferInput{
		ChannelID: chID, InputPriceUsd: 0.05, OutputPriceUsd: 0.1, OverridePrice: true, Note: "手填",
	})
	mustNoErrT(t, err, "create offer")

	// 未确认就应用 → 409 override_required。
	code, _ = doJSON(t, c, http.MethodPost, base+"/api/v1/official-prices/"+itoa(op.ID)+"/apply",
		map[string]any{"offerId": of.ID})
	mustStatus(t, code, http.StatusConflict, "apply without confirm")

	// 确认应用 → 200,人民币原值直接落库(无汇率折算)。
	code, body = doJSON(t, c, http.MethodPost, base+"/api/v1/official-prices/"+itoa(op.ID)+"/apply",
		map[string]any{"offerId": of.ID, "confirmOverride": true})
	mustStatus(t, code, http.StatusOK, "apply same-currency")
	got := decode[domain.OfferRead](t, body)
	if !closeTo(got.InputPriceUsd, 0.1) || !closeTo(got.OutputPriceUsd, 0.2) {
		t.Errorf("cny prices not applied as-is: %+v", got)
	}
	if !got.OverridePrice {
		t.Error("override_price must be untouched")
	}
	if got.Note != "手填" {
		t.Errorf("note must be untouched: %q", got.Note)
	}
	if got.PriceSourceURL != "https://bigmodel.cn/pricing" || got.PriceCurrency != "CNY" {
		t.Errorf("provenance not written: %+v", got)
	}

	// 读回官方价:RateSet=true 且 AppliedOfferIDs 含该 offer。
	code, body = doJSON(t, c, http.MethodGet,
		base+"/api/v1/channels/"+itoa(chID)+"/official-prices", nil)
	mustStatus(t, code, http.StatusOK, "list channel official prices")
	views := decode[[]domain.OfficialPriceView](t, body)
	if len(views) != 1 {
		t.Fatalf("want 1 view, got %d", len(views))
	}
	if !views[0].RateSet {
		t.Error("rateSet should be true when official currency == display currency")
	}
	found := false
	for _, id := range views[0].AppliedOfferIDs {
		if id == of.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("appliedOfferIds = %v, want to contain %d", views[0].AppliedOfferIDs, of.ID)
	}
}

// TestApplyOfficialPriceCrossCurrency 计价币种与官方原币种不一致时:未设汇率必须拒绝(不臆造),
// 设了汇率则按 USDPerCNY 折算后落库。
func TestApplyOfficialPriceCrossCurrency(t *testing.T) {
	srv, c, st := newTestServer(t)
	base := srv.URL
	bootstrap(t, c, base)
	chID := mkChannelOf(t, c, base, "zhipu", domain.ProviderZhipu)

	// 计价币种切美元,汇率留空。
	code, _ := doJSON(t, c, http.MethodPatch, base+"/api/v1/settings", map[string]any{
		"requestTimeoutMs": 60000, "displayCurrency": "USD",
	})
	mustStatus(t, code, http.StatusOK, "set display currency")

	code, body := doJSON(t, c, http.MethodPost, base+"/api/v1/official-prices/manual", map[string]any{
		"provider": string(domain.ProviderZhipu), "modelName": "glm-4.6",
		"sourceUrl": "https://bigmodel.cn/pricing", "currency": "CNY",
		"inputPrice": 0.1, "outputPrice": 0.2,
	})
	mustStatus(t, code, http.StatusOK, "manual price")
	op := decode[domain.OfficialPriceView](t, body)
	// 币种不一致且无汇率 → 金额不可用(三价留 0,不臆造)。
	if op.RateSet {
		t.Error("rateSet should be false when currencies differ and no rate is set")
	}
	if op.InputPriceUsd != 0 || op.OutputPriceUsd != 0 {
		t.Errorf("no conversion without a rate, got %+v", op)
	}

	m, err := st.CreateModel(domain.ModelInput{Name: "glm-4.6", ContextWindow: 128000})
	mustNoErrT(t, err, "create model")
	of, err := st.CreateOffer(m.ID, domain.OfferInput{ChannelID: chID, InputPriceUsd: 0.05, OutputPriceUsd: 0.1})
	mustNoErrT(t, err, "create offer")

	code, body = doJSON(t, c, http.MethodPost, base+"/api/v1/official-prices/"+itoa(op.ID)+"/apply",
		map[string]any{"offerId": of.ID})
	mustStatus(t, code, http.StatusBadRequest, "apply without rate")
	if !strings.Contains(string(body), "汇率") {
		t.Fatalf("unexpected body: %s", body)
	}

	// 设了汇率 → 0.1 CNY × 0.14 = 0.014;0.2 × 0.14 = 0.028。
	code, _ = doJSON(t, c, http.MethodPatch, base+"/api/v1/settings", map[string]any{
		"requestTimeoutMs": 60000, "displayCurrency": "USD", "usdPerCny": 0.14,
	})
	mustStatus(t, code, http.StatusOK, "set rate")

	code, body = doJSON(t, c, http.MethodPost, base+"/api/v1/official-prices/"+itoa(op.ID)+"/apply",
		map[string]any{"offerId": of.ID})
	mustStatus(t, code, http.StatusOK, "apply with rate")
	got := decode[domain.OfferRead](t, body)
	if !closeTo(got.InputPriceUsd, 0.014) || !closeTo(got.OutputPriceUsd, 0.028) {
		t.Errorf("converted prices not applied: %+v", got)
	}
}

// TestSettingsDisplayCurrencyValidation 计价币种只接受 CNY/USD;缺省回落 CNY。
func TestSettingsDisplayCurrencyValidation(t *testing.T) {
	srv, c, _ := newTestServer(t)
	base := srv.URL
	bootstrap(t, c, base)

	code, _ := doJSON(t, c, http.MethodPatch, base+"/api/v1/settings", map[string]any{
		"requestTimeoutMs": 60000, "displayCurrency": "EUR",
	})
	mustStatus(t, code, http.StatusBadRequest, "reject unknown currency")

	code, body := doJSON(t, c, http.MethodPatch, base+"/api/v1/settings", map[string]any{
		"requestTimeoutMs": 60000,
	})
	mustStatus(t, code, http.StatusOK, "omit display currency")
	if got := decode[domain.Settings](t, body); got.DisplayCurrency != domain.CurrencyCNY {
		t.Errorf("displayCurrency = %q, want CNY default", got.DisplayCurrency)
	}
}

// TestFetchPricingFailureWritesNothing 抓取失败时 official_prices 必须为空,且原 offer 报价不变。
func TestFetchPricingFailureWritesNothing(t *testing.T) {
	srv, c, st := newTestServer(t)
	base := srv.URL
	bootstrap(t, c, base)
	oid := mkChannelOf(t, c, base, "openai", domain.ProviderOpenAI)

	m, err := st.CreateModel(domain.ModelInput{Name: "gpt-5", ContextWindow: 400000})
	mustNoErrT(t, err, "create model")
	of, err := st.CreateOffer(m.ID, domain.OfferInput{ChannelID: oid, InputPriceUsd: 1.5, OutputPriceUsd: 6})
	mustNoErrT(t, err, "create offer")

	code, _ := doJSON(t, c, http.MethodPost, base+"/api/v1/channels/"+itoa(oid)+"/fetch-pricing", nil)
	mustStatus(t, code, http.StatusBadRequest, "fetch pricing fails")

	rows, err := st.ListAllOfficialPrices()
	mustNoErrT(t, err, "list all")
	if len(rows) != 0 {
		t.Fatalf("no official price should be written on failure, got %d", len(rows))
	}
	after, err := st.GetOffer(of.ID)
	mustNoErrT(t, err, "get offer")
	if after.InputPriceUsd != 1.5 || after.OutputPriceUsd != 6 {
		t.Errorf("existing offer price must be unchanged: %+v", after)
	}
}

// TestFetchPricingChannelManualOnly 渠道级抓取:逐厂商官网抓取停用后(issue #27),
// 任何 provider 的渠道级抓取都返回 400 manual_only —— 官方价改由 CC 单页锚点写入。
//
// 频道级抓取的「成功路径」已移至 commandcode 端点(见 TestFetchCommandCodeEndToEnd)。
func TestFetchPricingChannelManualOnly(t *testing.T) {
	srv, c, _ := newTestServer(t)
	base := srv.URL
	bootstrap(t, c, base)

	// 即便 provider 是可抓时代的 DeepSeek,现在也只支持手工录入。
	did := mkChannelOf(t, c, base, "deepseek", domain.ProviderDeepSeek)
	code, body := doJSON(t, c, http.MethodPost, base+"/api/v1/channels/"+itoa(did)+"/fetch-pricing", nil)
	mustStatus(t, code, http.StatusBadRequest, "channel fetch manual-only")
	if !strings.Contains(string(body), "手工录入") {
		t.Fatalf("unexpected body: %s", body)
	}
}

// TestManualPriceRequiresSourceURL 手工录入缺来源 URL → 400(手工也要留证)。
func TestManualPriceRequiresSourceURL(t *testing.T) {
	srv, c, _ := newTestServer(t)
	base := srv.URL
	bootstrap(t, c, base)

	code, _ := doJSON(t, c, http.MethodPost, base+"/api/v1/official-prices/manual", map[string]any{
		"provider": string(domain.ProviderZhipu), "modelName": "glm-4.6",
		"currency": "CNY", "inputPrice": 0.1, "outputPrice": 0.2,
	})
	mustStatus(t, code, http.StatusBadRequest, "manual without source url")
}

// TestVendorFetchOfficialPrices 按厂商抓取(无需厂商直连渠道):仅手工 / 无来源两种结局。
//
// issue #27 后逐厂商自动抓取已停用(官方价唯一锚点来源是 commandcode 单页),
// 故 DeepSeek 也归入「仅手工」—— 成功路径改由 TestFetchCommandCodeEndToEnd 覆盖。
func TestVendorFetchOfficialPrices(t *testing.T) {
	srv, c, _, _ := newTestServerS(t)
	base := srv.URL
	bootstrap(t, c, base)

	// 仅手工录入的厂商 → 400 manual_only(不得静默成功)。
	// DeepSeek 亦在此列:抓取停用后它只支持手工录入。
	for _, p := range []domain.Provider{domain.ProviderZhipu, domain.ProviderDeepSeek, domain.ProviderQwen} {
		code, body := doJSON(t, c, http.MethodPost, base+"/api/v1/official-prices/fetch",
			map[string]any{"provider": string(p)})
		mustStatus(t, code, http.StatusBadRequest, "vendor fetch manual-only "+string(p))
		if e := decode[struct {
			Error struct {
				Type string `json:"type"`
			} `json:"error"`
		}](t, body); e.Error.Type != "manual_only" {
			t.Fatalf("%s type = %q, body %s", p, e.Error.Type, body)
		}
	}

	// 无官方来源的厂商(中转站不是厂商)→ 400 unsupported。
	code, body := doJSON(t, c, http.MethodPost, base+"/api/v1/official-prices/fetch",
		map[string]any{"provider": string(domain.Provider("示例中转站"))})
	mustStatus(t, code, http.StatusBadRequest, "vendor fetch unsupported")
	if !strings.Contains(string(body), "无受支持的官方单价页面") {
		t.Fatalf("unexpected body: %s", body)
	}
}

// TestFetchCommandCodeEndToEnd commandcode 单页锚点抓取的端到端(issue #27):
// 假 CC 页 + 出站改写 → 多厂商落库、免费行报告跳过、来源留证齐备、对账按来源作用域。
func TestFetchCommandCodeEndToEnd(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("..", "pricing", "testdata", "commandcode_models.html"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(fixture)
	}))
	t.Cleanup(upstream.Close)

	srv, c, st, s := newTestServerS(t)
	base := srv.URL
	bootstrap(t, c, base)
	s.pricingBaseForURL = func(_ string, _ domain.Settings) *http.Client {
		return &http.Client{Transport: &redirectRT{target: upstream.URL}}
	}

	code, body := doJSON(t, c, http.MethodPost, base+"/api/v1/official-prices/fetch-commandcode", nil)
	mustStatus(t, code, http.StatusOK, "cc fetch")
	res := decode[domain.CommandCodeFetchResult](t, body)
	if res.TotalRows != 81 {
		t.Errorf("totalRows = %d, want 81", res.TotalRows)
	}
	if res.Upserted != 78 {
		t.Errorf("upserted = %d, want 78(81 − 3 免费)", res.Upserted)
	}
	if len(res.FreeSkipped) != 3 {
		t.Errorf("freeSkipped = %v, want 3 条", res.FreeSkipped)
	}
	if res.ContentSHA == "" {
		t.Error("content sha256 must be recorded")
	}
	if !strings.Contains(res.SourceURL, "commandcode.ai") {
		t.Errorf("source url should be CC: %q", res.SourceURL)
	}
	// 多厂商:至少覆盖 OpenAI/Anthropic/DeepSeek/通义。
	got := map[domain.Provider]int{}
	for _, pc := range res.PerVendor {
		got[pc.Provider] = pc.Count
	}
	for p, want := range map[domain.Provider]int{
		domain.ProviderOpenAI: 10, domain.ProviderAnthropic: 9,
		domain.ProviderDeepSeek: 5, domain.ProviderQwen: 10,
	} {
		if got[p] != want {
			t.Errorf("perVendor[%s] = %d, want %d", p, got[p], want)
		}
	}

	// 落库:全部来源为 CC;峰谷行形态正确;缓存写在列。
	rows, err := st.ListOfficialPrices(domain.ProviderDeepSeek)
	mustNoErrT(t, err, "list deepseek official prices")
	var flash *domain.OfficialPriceRow
	for i := range rows {
		if rows[i].ModelName == "deepseek-v4-1-flash" {
			flash = &rows[i]
		}
	}
	if flash == nil {
		t.Fatalf("deepseek-v4-1-flash 未落库: %+v", rows)
	}
	if flash.BillingShape != domain.ShapePeakOff {
		t.Errorf("deepseek-v4-1-flash shape = %q, want peak_offpeak", flash.BillingShape)
	}
	if flash.Currency != domain.CurrencyUSD {
		t.Errorf("currency = %q, want USD", flash.Currency)
	}
	if flash.InputPrice != 0.15 || flash.OutputPrice != 0.60 {
		t.Errorf("deepseek-v4-1-flash 谷价 = %v/%v, want 0.15/0.60", flash.InputPrice, flash.OutputPrice)
	}
	if !strings.Contains(flash.SourceURL, "commandcode.ai") {
		t.Errorf("source url = %q", flash.SourceURL)
	}

	// 幂等:再抓一次,不重复落库、无删除。
	code, body = doJSON(t, c, http.MethodPost, base+"/api/v1/official-prices/fetch-commandcode", nil)
	mustStatus(t, code, http.StatusOK, "cc fetch 2nd")
	res2 := decode[domain.CommandCodeFetchResult](t, body)
	if res2.Upserted != 78 {
		t.Errorf("2nd upserted = %d, want 78", res2.Upserted)
	}
	if res2.Removed != 0 {
		t.Errorf("2nd removed = %d, want 0(同一份页面重抓不该删任何行)", res2.Removed)
	}

	// 对账按来源作用域:手工/旧来源(非 CC)的行不得被 CC 抓取删掉 —— 回归红线。
	if _, err := st.UpsertOfficialPrice(domain.OfficialPriceRow{
		Provider: domain.ProviderDeepSeek, ModelName: "deepseek-flash",
		SourceURL: "https://api-docs.deepseek.com/zh-cn/quick_start/pricing",
		FetchedAt: time.Now().UTC(), Currency: domain.CurrencyCNY,
		BillingShape: domain.ShapeFlat, InputPrice: 1, OutputPrice: 2,
	}); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}
	code, body = doJSON(t, c, http.MethodPost, base+"/api/v1/official-prices/fetch-commandcode", nil)
	mustStatus(t, code, http.StatusOK, "cc fetch 3rd")
	res3 := decode[domain.CommandCodeFetchResult](t, body)
	if res3.Removed != 0 {
		t.Errorf("removed = %d, want 0(非 CC 来源的行不得被删)", res3.Removed)
	}
	if _, err := st.GetOfficialPriceByName(domain.ProviderDeepSeek, "deepseek-flash"); err != nil {
		t.Errorf("旧来源行被误删: %v", err)
	}

	// 换厂商归属:把某 slug 写到别的厂商名下(模拟补全前缀映射前的旧行),
	// 对账必须清掉它(逐厂商对账够不到这一支)。
	if _, err := st.UpsertOfficialPrice(domain.OfficialPriceRow{
		Provider: domain.ProviderMeta, ModelName: "claude-haiku-4-5",
		SourceURL: "https://commandcode.ai/models",
		FetchedAt: time.Now().UTC(), Currency: domain.CurrencyUSD,
		BillingShape: domain.ShapeFlat, InputPrice: 9, OutputPrice: 9,
	}); err != nil {
		t.Fatalf("seed misattributed row: %v", err)
	}
	code, body = doJSON(t, c, http.MethodPost, base+"/api/v1/official-prices/fetch-commandcode", nil)
	mustStatus(t, code, http.StatusOK, "cc fetch 4th")
	res4 := decode[domain.CommandCodeFetchResult](t, body)
	if res4.Removed != 1 {
		t.Errorf("removed = %d, want 1(错厂商的 CC 行应被清)", res4.Removed)
	}
	if _, err := st.GetOfficialPriceByName(domain.ProviderAnthropic, "claude-haiku-4-5"); err != nil {
		t.Errorf("正确厂商的行应存在: %v", err)
	}
}

// TestOfficialVendorsEndpoint 厂商清单:CC 覆盖的全部厂商(issue #27 后 20 家),
// 来源 URL 非空;逐厂商抓取已停用,故全部 manualOnly。
func TestOfficialVendorsEndpoint(t *testing.T) {
	srv, c, _ := newTestServer(t)
	base := srv.URL
	bootstrap(t, c, base)

	code, body := doJSON(t, c, http.MethodGet, base+"/api/v1/official-prices/vendors", nil)
	mustStatus(t, code, http.StatusOK, "list vendors")
	vs := decode[[]map[string]any](t, body)
	if len(vs) != len(domain.Providers) {
		t.Fatalf("vendors len = %d, want %d: %s", len(vs), len(domain.Providers), body)
	}
	seen := map[string]bool{}
	cur := map[string]string{}
	for _, v := range vs {
		p := v["provider"].(string)
		seen[p] = true
		if v["sourceUrl"].(string) == "" {
			t.Errorf("%s: source url empty", p)
		}
		// issue #27:逐厂商官网抓取停用 → 全部厂商仅手工(官方价由 CC 锚点写入)。
		if !v["manualOnly"].(bool) {
			t.Errorf("%s should be manual-only(逐厂商抓取已停用)", p)
		}
		if s, ok := v["manualCurrency"].(string); ok {
			cur[p] = s
		}
	}
	// 厂商清单必须覆盖枚举全集。
	for _, p := range domain.Providers {
		if !seen[string(p)] {
			t.Errorf("vendors 未覆盖 %s", p)
		}
	}
	// 手工录入默认原币:Anthropic 是美元(否则官方价会按 ¥ 错算)。
	if cur[string(domain.ProviderAnthropic)] != "USD" {
		t.Errorf("Anthropic manualCurrency = %q, want USD", cur[string(domain.ProviderAnthropic)])
	}
	if cur[string(domain.ProviderZhipu)] != "CNY" {
		t.Errorf("智谱 manualCurrency = %q, want CNY", cur[string(domain.ProviderZhipu)])
	}
}

// TestModelBindingPatchPersists 模型级官方价绑定:带绑定 PATCH 落库并可读回,缺省绑定时不丢。
func TestModelBindingPatchPersists(t *testing.T) {
	srv, c, _ := newTestServer(t)
	base := srv.URL
	bootstrap(t, c, base)

	code, body := doJSON(t, c, http.MethodPost, base+"/api/v1/models",
		map[string]any{"name": "deepseek/deepseek-v4.1-flash", "contextWindow": 64000})
	mustStatus(t, code, http.StatusOK, "create model")
	m := decode[domain.ModelRead](t, body)

	// 带绑定 PATCH。
	code, body = doJSON(t, c, http.MethodPatch, base+"/api/v1/models/"+itoa(m.ID), map[string]any{
		"name": "deepseek/deepseek-v4.1-flash", "contextWindow": 64000,
		"officialVendor": string(domain.ProviderDeepSeek), "officialModelName": "deepseek-flash",
	})
	mustStatus(t, code, http.StatusOK, "patch binding")
	got := decode[domain.ModelRead](t, body)
	if got.OfficialVendor != domain.ProviderDeepSeek || got.OfficialModelName != "deepseek-flash" {
		t.Fatalf("binding not persisted: %+v", got)
	}
	// 模型名带 deepseek/ 前缀 → 后端应推断出厂商下发,供前端自动匹配。
	if got.InferredVendor != domain.ProviderDeepSeek {
		t.Errorf("inferred vendor = %q, want DeepSeek", got.InferredVendor)
	}

	// 不带绑定的 PATCH → 绑定保留(指针为 nil 表示不改)。
	code, body = doJSON(t, c, http.MethodPatch, base+"/api/v1/models/"+itoa(m.ID), map[string]any{
		"name": "deepseek/deepseek-v4.1-flash", "contextWindow": 64000,
	})
	mustStatus(t, code, http.StatusOK, "patch without binding")
	got2 := decode[domain.ModelRead](t, body)
	if got2.OfficialVendor != domain.ProviderDeepSeek || got2.OfficialModelName != "deepseek-flash" {
		t.Fatalf("binding must persist when omitted: %+v", got2)
	}
}

// ---- 小工具 ----
func mustNoErrT(t *testing.T, err error, msg string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", msg, err)
	}
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

func closeTo(a, b float64) bool {
	d := a - b
	return d < 1e-6 && d > -1e-6
}
