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

// TestFetchPricingUnsupportedProvider OpenAI 无受支持官方单价来源 → 400。
func TestFetchPricingUnsupportedProvider(t *testing.T) {
	srv, c, _ := newTestServer(t)
	base := srv.URL
	bootstrap(t, c, base)
	oid := mkChannelOf(t, c, base, "openai", domain.ProviderOpenAI)

	code, body := doJSON(t, c, http.MethodPost, base+"/api/v1/channels/"+itoa(oid)+"/fetch-pricing", nil)
	mustStatus(t, code, http.StatusBadRequest, "openai fetch pricing")
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

// TestFetchPricingSuccessPath 走真解析器(绑官方样本)的端到端抓取:
// 假官方页 + 出站改写 → DeepSeek 峰谷两档落库、来源 URL/时间/sha256 齐备。
func TestFetchPricingSuccessPath(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "pricing", "testdata", "deepseek_pricing.html"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(body)
	}))
	t.Cleanup(upstream.Close)

	srv, c, st, s := newTestServerS(t)
	base := srv.URL
	bootstrap(t, c, base)
	// 注入:所有官方域出站改写本地假页。
	s.pricingBase = func(p domain.Provider, _ domain.Settings) *http.Client {
		return &http.Client{Transport: &redirectRT{target: upstream.URL}}
	}
	did := mkChannelOf(t, c, base, "deepseek", domain.ProviderDeepSeek)

	code, body2 := doJSON(t, c, http.MethodPost, base+"/api/v1/channels/"+itoa(did)+"/fetch-pricing", nil)
	mustStatus(t, code, http.StatusOK, "deepseek fetch pricing")
	res := decode[domain.FetchPricingResult](t, body2)
	if res.Upserted == 0 || len(res.Models) == 0 {
		t.Fatalf("nothing upserted: %+v", res)
	}
	if res.ContentSHA == "" {
		t.Error("content sha256 must be recorded")
	}
	if !strings.Contains(res.SourceURL, "api-docs.deepseek.com") {
		t.Errorf("source url should be official domain: %q", res.SourceURL)
	}

	// 落库的官方价:CNY + 峰谷形态 + 来源可追溯。
	rows, err := st.ListOfficialPrices(domain.ProviderDeepSeek)
	mustNoErrT(t, err, "list official prices")
	if len(rows) == 0 {
		t.Fatal("official prices not persisted")
	}
	if rows[0].Currency != domain.CurrencyCNY || rows[0].BillingShape != domain.ShapePeakOff {
		t.Errorf("bad row: %+v", rows[0])
	}
	if rows[0].SourceURL == "" || rows[0].FetchedAt.IsZero() || rows[0].ContentSHA256 == "" {
		t.Errorf("provenance incomplete: %+v", rows[0])
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

// TestVendorFetchOfficialPrices 按厂商抓取(无需厂商直连渠道):成功 / 仅手工 / 无来源三种结局。
func TestVendorFetchOfficialPrices(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("..", "pricing", "testdata", "deepseek_pricing.html"))
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
	s.pricingBase = func(p domain.Provider, _ domain.Settings) *http.Client {
		return &http.Client{Transport: &redirectRT{target: upstream.URL}}
	}

	// 不建任何渠道,直接按厂商抓取(聚合中转场景)。
	code, body := doJSON(t, c, http.MethodPost, base+"/api/v1/official-prices/fetch",
		map[string]any{"provider": string(domain.ProviderDeepSeek)})
	mustStatus(t, code, http.StatusOK, "vendor fetch")
	res := decode[domain.FetchPricingResult](t, body)
	if res.Upserted == 0 || len(res.Models) == 0 {
		t.Fatalf("nothing upserted: %+v", res)
	}
	if !strings.Contains(res.SourceURL, "api-docs.deepseek.com") {
		t.Errorf("source url should be official domain: %q", res.SourceURL)
	}
	rows, err := st.ListOfficialPrices(domain.ProviderDeepSeek)
	mustNoErrT(t, err, "list official prices")
	if len(rows) == 0 {
		t.Fatal("official prices not persisted")
	}

	// 仅手工录入的厂商 → 400 manual_only(不得静默成功)。
	code, body = doJSON(t, c, http.MethodPost, base+"/api/v1/official-prices/fetch",
		map[string]any{"provider": string(domain.ProviderZhipu)})
	mustStatus(t, code, http.StatusBadRequest, "vendor fetch manual-only")
	if e := decode[struct {
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}](t, body); e.Error.Type != "manual_only" {
		t.Fatalf("type = %q, body %s", e.Error.Type, body)
	}

	// 无官方来源的厂商 → 400 unsupported。
	code, body = doJSON(t, c, http.MethodPost, base+"/api/v1/official-prices/fetch",
		map[string]any{"provider": string(domain.ProviderOpenAI)})
	mustStatus(t, code, http.StatusBadRequest, "vendor fetch unsupported")
	if !strings.Contains(string(body), "无受支持的官方单价页面") {
		t.Fatalf("unexpected body: %s", body)
	}
}

// TestOfficialVendorsEndpoint 厂商清单:3 个厂商,仅智谱标「仅手工」,来源 URL 非空。
func TestOfficialVendorsEndpoint(t *testing.T) {
	srv, c, _ := newTestServer(t)
	base := srv.URL
	bootstrap(t, c, base)

	code, body := doJSON(t, c, http.MethodGet, base+"/api/v1/official-prices/vendors", nil)
	mustStatus(t, code, http.StatusOK, "list vendors")
	vs := decode[[]map[string]any](t, body)
	if len(vs) != 3 {
		t.Fatalf("vendors len = %d: %s", len(vs), body)
	}
	manual := map[string]bool{}
	for _, v := range vs {
		p := v["provider"].(string)
		if v["sourceUrl"].(string) == "" {
			t.Errorf("%s: source url empty", p)
		}
		manual[p] = v["manualOnly"].(bool)
	}
	if !manual[string(domain.ProviderZhipu)] {
		t.Error("智谱 should be manual-only")
	}
	if manual[string(domain.ProviderDeepSeek)] || manual[string(domain.ProviderQwen)] {
		t.Error("DeepSeek/通义千问 should be fetchable")
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
