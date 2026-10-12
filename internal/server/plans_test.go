package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"personal-ai-gateway/internal/domain"
	"personal-ai-gateway/internal/store"
)

// ---- 订阅计划页(CC GOAT / opencode Go)抓取 → 按模型派生成本系数的端到端 ----

// planFixtureServer 一个按**请求路径**分派夹具的假上游。redirectRT 只改 host、保留 path,
// 故同一起服务可同时满足四个来源:CC 模型页、opencode zen、CC GOAT 计划页、opencode Go 计划页。
//
// failPaths 里的路径返回 500(用于验证「某来源失败不影响另一个」)。
func planFixtureServer(t *testing.T, failPaths map[string]bool) *httptest.Server {
	t.Helper()
	dir := filepath.Join("..", "pricing", "testdata")
	files := map[string]string{
		"/models":          "commandcode_models.html",
		"/docs/zen":        "opencode_zen.html",
		"/docs/zen/":       "opencode_zen.html",
		"/docs/plans/goat": "commandcode_goat.html",
		"/docs/go":         "opencode_go.html",
		"/docs/go/":        "opencode_go.html",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failPaths[r.URL.Path] {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		name, ok := files[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// mkChanType 建一个指定 channel_type 的渠道(计划页只写给匹配类型的渠道)。
func mkChanType(t *testing.T, st *store.Store, name string, ct domain.ChannelType) int64 {
	t.Helper()
	en := true
	ch, err := st.CreateChannel(domain.ChannelInput{
		Name: name, Provider: domain.ProviderOpenAI, BaseURL: "https://upstream.example.com",
		ChannelType: ct, Enabled: &en,
	})
	if err != nil {
		t.Fatalf("create channel %s: %v", name, err)
	}
	return ch.ID
}

// TestFetchPlansEndToEnd 端到端:CC GOAT / opencode Go 两来源各自按模型派生系数,
// 只写给**匹配 channel_type** 的渠道;已存在的人工覆盖行(source=manual)不被覆盖。
func TestFetchPlansEndToEnd(t *testing.T) {
	srvUp := planFixtureServer(t, nil)

	srv, c, st, s := newTestServerS(t)
	base := srv.URL
	bootstrap(t, c, base)
	s.pricingBaseForURL = func(_ string, _ domain.Settings) *http.Client {
		return &http.Client{Transport: &redirectRT{target: srvUp.URL}}
	}

	ccID := mkChanType(t, st, "cc", domain.ChannelTypeCommandCode)
	ocID := mkChanType(t, st, "oc", domain.ChannelTypeOpenCode)

	// 预置一条人工覆盖行(CC 渠道,deepseek-v4-1-flash):抓取后不得被 plan 行覆盖。
	mustNoErrT(t, st.SetChannelModelCostRatio(ccID, "deepseek-v4-1-flash", 0.05, "manual", "手改"), "seed manual")

	code, body := doJSON(t, c, http.MethodPost, base+"/api/v1/official-prices/fetch-plans", nil)
	mustStatus(t, code, http.StatusOK, "fetch plans")
	res := decode[domain.PlanFetchResult](t, body)
	if len(res.Sources) != 2 {
		t.Fatalf("sources = %d, want 2: %s", len(res.Sources), body)
	}
	cc, oc := res.Sources[0], res.Sources[1]
	if cc.ChannelType != domain.ChannelTypeCommandCode || oc.ChannelType != domain.ChannelTypeOpenCode {
		t.Fatalf("来源顺序应 CC→opencode: %+v", res.Sources)
	}
	for _, r := range res.Sources {
		if r.Error != "" {
			t.Errorf("%s 抓取失败: %s", r.ChannelType, r.Error)
		}
		if r.PlanPriceUSD != 10 {
			t.Errorf("%s planPrice = %v, want 10", r.ChannelType, r.PlanPriceUSD)
		}
		if r.ContentSHA == "" {
			t.Errorf("%s content sha256 缺失", r.ChannelType)
		}
		if r.ChannelsTouched != 1 {
			t.Errorf("%s channelsTouched = %d, want 1", r.ChannelType, r.ChannelsTouched)
		}
		if !strings.Contains(r.SourceURL, "ai") {
			t.Errorf("%s source url = %q", r.ChannelType, r.SourceURL)
		}
	}
	// CC:53 行可落库,其中 1 行被人工作废 → 写入 52。
	if cc.Priced != 53 || cc.Written != 52 {
		t.Errorf("CC priced/written = %d/%d, want 53/52", cc.Priced, cc.Written)
	}
	// opencode:42 行 → 去重 30 个 slug,全写入。
	if oc.Priced != 30 || oc.Written != 30 {
		t.Errorf("opencode priced/written = %d/%d, want 30/30", oc.Priced, oc.Written)
	}

	// CC 渠道:plan 52 行 + manual 1 行;人工行系数保持 0.05。
	nPlan, _ := st.CountChannelModelRatiosBySource(ccID, "plan")
	mustEqualT(t, nPlan, 52, "CC plan 行数")
	nManual, _ := st.CountChannelModelRatiosBySource(ccID, "manual")
	mustEqualT(t, nManual, 1, "CC manual 行数")
	if got, _ := st.ChannelModelRatio(ccID, "deepseek-v4-1-flash"); got != 0.05 {
		t.Errorf("人工行被覆盖: %v, want 0.05", got)
	}
	// CC 派生值:10/60。
	if got, _ := st.ChannelModelRatio(ccID, "gpt-5-6-sol"); !closeTo(got, 10.0/70.0) {
		t.Errorf("gpt-5-6-sol ratio = %v, want 0.1428", got)
	}
	// opencode 渠道:30 行,连字符/点号 slug 按各自来源键控。
	ocRows, err := st.ListChannelModelCostRatios(ocID)
	mustNoErrT(t, err, "list oc ratios")
	mustEqualT(t, len(ocRows), 30, "opencode 写入行数")
	if got, _ := st.ChannelModelRatio(ocID, "deepseek-v4.1-flash"); !closeTo(got, 0.125) {
		t.Errorf("opencode deepseek ratio = %v, want 0.125", got)
	}
	// 只写给匹配类型:CC 渠道不该有 opencode 的点号 slug,反之亦然。
	if _, err := st.ChannelModelRatio(ccID, "deepseek-v4.1-flash"); err == nil {
		t.Error("点号 slug 不该出现在 CC 渠道")
	}
}

// TestFetchPlansSourceIndependence 一来源失败不影响另一来源(逐来源独立成败):
// opencode Go 页 500 → CC 照常写入,响应里 opencode 源带 error、CC 源正常。
func TestFetchPlansSourceIndependence(t *testing.T) {
	srvUp := planFixtureServer(t, map[string]bool{"/docs/go": true, "/docs/go/": true})

	srv, c, st, s := newTestServerS(t)
	base := srv.URL
	bootstrap(t, c, base)
	s.pricingBaseForURL = func(_ string, _ domain.Settings) *http.Client {
		return &http.Client{Transport: &redirectRT{target: srvUp.URL}}
	}
	ccID := mkChanType(t, st, "cc", domain.ChannelTypeCommandCode)
	ocID := mkChanType(t, st, "oc", domain.ChannelTypeOpenCode)

	code, body := doJSON(t, c, http.MethodPost, base+"/api/v1/official-prices/fetch-plans", nil)
	mustStatus(t, code, http.StatusOK, "partial failure still 200")
	res := decode[domain.PlanFetchResult](t, body)
	if len(res.Sources) != 2 {
		t.Fatalf("sources = %d, want 2", len(res.Sources))
	}
	cc, oc := res.Sources[0], res.Sources[1]
	if cc.Error != "" || cc.Written == 0 {
		t.Errorf("CC 源应成功且写入: %+v", cc)
	}
	if oc.Error == "" || oc.ErrorType != "fetch_failed" {
		t.Errorf("opencode 源应报 fetch_failed: %+v", oc)
	}
	// CC 渠道写入、opencode 渠道为空(其来源失败)。
	nPlan, _ := st.CountChannelModelRatiosBySource(ccID, "plan")
	if nPlan == 0 {
		t.Error("CC 渠道应有 plan 行")
	}
	ocRows, _ := st.ListChannelModelCostRatios(ocID)
	mustEqualT(t, len(ocRows), 0, "opencode 渠道不该有行(其来源失败)")
}

// TestRefreshPlansBlockIndependent 批量刷新的三块独立:订阅计划全部失败 → PlansError 置位、
// Plans 为空,但 CC / opencode 官方价抓取照常成功(计划页失败不淹没官方价)。
func TestRefreshPlansBlockIndependent(t *testing.T) {
	// 计划页两条路径 500;模型/zen 定价页正常。
	srvUp := planFixtureServer(t, map[string]bool{"/docs/plans/goat": true, "/docs/go": true, "/docs/go/": true})

	srv, c, st, s := newTestServerS(t)
	base := srv.URL
	bootstrap(t, c, base)
	s.pricingBaseForURL = func(_ string, _ domain.Settings) *http.Client {
		return &http.Client{Transport: &redirectRT{target: srvUp.URL}}
	}
	mkChanType(t, st, "cc", domain.ChannelTypeCommandCode)

	code, body := doJSON(t, c, http.MethodPost, base+"/api/v1/official-prices/refresh", map[string]any{})
	mustStatus(t, code, http.StatusOK, "refresh 200")
	res := decode[domain.RefreshPricingResp](t, body)
	if res.CommandCode == nil {
		t.Errorf("CC 官方价应仍成功: %+v", res.CommandCodeError)
	}
	if res.OpenCode == nil {
		t.Errorf("opencode 官方价应仍成功: %+v", res.OpenCodeError)
	}
	if res.Plans != nil {
		t.Errorf("订阅计划全失败时 Plans 应为空, got %+v", res.Plans)
	}
	if res.PlansError == "" {
		t.Error("订阅计划失败应置 PlansError")
	}
	// 官方价确实落库(证明 CC 抓取未被计划失败连累)。
	rows, err := st.ListAllOfficialPrices()
	mustNoErrT(t, err, "list official prices")
	if len(rows) == 0 {
		t.Error("官方价应已落库")
	}
}

// mustEqualT 断言语标量相等。
func mustEqualT(t *testing.T, got, want any, msg string) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %v, want %v", msg, got, want)
	}
}
