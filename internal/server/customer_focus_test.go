package server

import (
	"net/http"
	"net/http/cookiejar"
	"testing"
	"time"

	"personal-ai-gateway/internal/domain"
)

/*
客户经营面(issue #13):营收/毛利 + 客户关注区。

关键不变量:
  - /overview 带出客户口径的营收/成本/毛利,且旧日志(无 charge_usd)不污染毛利;
  - 默认窗口由前端负责,后端吃 days/from&to;
  - 普通用户拿不到 /customers/focus,也拿不到成本/毛利 —— 不因重排引入越权;
  - /customers/focus 的欠费线:余额 ≤0 已欠费,低于窗口消耗则预警。
*/
func TestOverviewMarginTotals(t *testing.T) {
	srv, admin, st := newTestServer(t)
	base := srv.URL
	bootstrap(t, admin, base)

	code, _ := doJSON(t, admin, http.MethodPost, base+"/api/v1/users",
		map[string]any{"username": "cust", "password": "password123", "role": "user"})
	mustStatus(t, code, http.StatusOK, "create user")
	users := decode[[]map[string]any](t, mustGet(t, admin, base+"/api/v1/users"))
	var custID int64
	for _, u := range users {
		if u["username"] == "cust" {
			custID = int64(u["id"].(float64))
		}
	}

	now := time.Now().UTC()
	// 客户流量:成本 1 / 售价 3。
	seedLogOwner(t, st, "m", now.Add(-1*time.Hour), custID)
	// 改造前的旧日志:有成本、无 charge_usd、无归属 —— 不得把毛利率抬到 100%。
	seedLogOwner(t, st, "legacy", now.Add(-2*time.Hour), 0)

	ov := decode[map[string]any](t, mustGet(t, admin, base+"/api/v1/overview?days=1"))
	totals, ok := ov["totals"].(map[string]any)
	if !ok {
		t.Fatalf("overview 缺 totals 字段: %v", ov)
	}
	if got := int(totals["requests"].(float64)); got != 1 {
		t.Errorf("客户请求数 = %d, want 1(旧日志无归属,应排除)", got)
	}
	if got := totals["revenueUsd"].(float64); got != 0.02 {
		t.Errorf("营收 = %v, want 0.02", got)
	}
	if got := totals["costUsd"].(float64); got != 0.01 {
		t.Errorf("成本 = %v, want 0.01(只算客户流量)", got)
	}
	if got := totals["marginUsd"].(float64); got < 0.0099 || got > 0.0101 {
		t.Errorf("毛利 = %v, want 0.01", got)
	}
	if got := totals["marginRate"].(float64); got < 0.49 || got > 0.51 {
		t.Errorf("毛利率 = %v, want 0.5", got)
	}

	// 客户口径曲线与合计同源:逐桶营收求和 = 合计营收。
	custPts, ok := ov["customerPoints"].([]any)
	if !ok || len(custPts) == 0 {
		t.Fatalf("overview 缺 customerPoints: %v", ov["customerPoints"])
	}
	var sumCharge float64
	for _, p := range custPts {
		sumCharge += p.(map[string]any)["chargeUsd"].(float64)
	}
	if sumCharge < 0.0199 || sumCharge > 0.0201 {
		t.Errorf("客户曲线营收求和 = %v, want 0.02(与合计一致)", sumCharge)
	}

	// 预设窗口回填环比基准;自定义区间为 null。
	if ov["prev"] == nil {
		t.Error("days=1 应回填 prev(昨日整日)")
	}
	from := now.AddDate(0, 0, -2).Format("2006-01-02")
	to := now.Format("2006-01-02")
	ov2 := decode[map[string]any](t, mustGet(t, admin, base+"/api/v1/overview?from="+from+"&to="+to))
	if ov2["prev"] != nil {
		t.Error("自定义区间不该有 prev(无自然对齐的上一区间)")
	}
}

// TestCustomerFocusEndpoint 客户关注区:欠费/低余额判定 + 消耗排行。
func TestCustomerFocusEndpoint(t *testing.T) {
	srv, admin, st := newTestServer(t)
	base := srv.URL
	bootstrap(t, admin, base)

	// 三个客户:欠费(余额-5)、低余额(余额 0.05)、健康(余额 100)。
	for _, u := range []struct {
		name string
		bal  float64
	}{{"broke", -5}, {"low", 0.05}, {"rich", 100}} {
		code, _ := doJSON(t, admin, http.MethodPost, base+"/api/v1/users",
			map[string]any{"username": u.name, "password": "password123", "role": "user"})
		mustStatus(t, code, http.StatusOK, "create "+u.name)
	}
	users := decode[[]map[string]any](t, mustGet(t, admin, base+"/api/v1/users"))
	idOf := map[string]int64{}
	for _, u := range users {
		idOf[u["username"].(string)] = int64(u["id"].(float64))
	}
	// 余额:调整到目标值(topup 正=充值,负=扣减)。
	doJSON(t, admin, http.MethodPost, base+"/api/v1/users/"+itoa(idOf["broke"])+"/topup", map[string]any{"amount": -5, "note": "seed"})
	doJSON(t, admin, http.MethodPost, base+"/api/v1/users/"+itoa(idOf["low"])+"/topup", map[string]any{"amount": 0.05, "note": "seed"})
	doJSON(t, admin, http.MethodPost, base+"/api/v1/users/"+itoa(idOf["rich"])+"/topup", map[string]any{"amount": 100, "note": "seed"})

	// 消耗:low 今天花了 0.10(> 余额 0.05 → 预警),rich 花了 1.00(余额充足 → 正常)。
	now := time.Now().UTC()
	seedCharge(t, st, now.Add(-1*time.Hour), idOf["low"], 0.05, 0.10)
	seedCharge(t, st, now.Add(-1*time.Hour), idOf["rich"], 0.40, 1.00)
	// 站主自用:不该出现在客户名单里。
	seedCharge(t, st, now.Add(-1*time.Hour), 0, 5, 0)

	d := decode[map[string]any](t, mustGet(t, admin, base+"/api/v1/customers/focus?days=1"))

	atRisk := d["atRisk"].([]any)
	if len(atRisk) != 2 {
		t.Fatalf("告警数 = %d, want 2(已欠费 + 低余额): %v", len(atRisk), atRisk)
	}
	// 余额升序:最危险在前。
	first := atRisk[0].(map[string]any)
	if first["username"].(string) != "broke" || first["risk"].(string) != "depleted" {
		t.Errorf("首行 = %v, want broke/depleted", first)
	}
	second := atRisk[1].(map[string]any)
	if second["username"].(string) != "low" || second["risk"].(string) != "low" {
		t.Errorf("次行 = %v, want low/low", second)
	}

	top := d["top"].([]any)
	if len(top) != 2 {
		t.Fatalf("消耗排行 = %d, want 2(站主自用不得出现)", len(top))
	}
	if r := top[0].(map[string]any); r["username"].(string) != "rich" || r["spendUsd"].(float64) != 1.0 {
		t.Errorf("消耗榜首 = %v, want rich/1.0", r)
	}
}

// TestCustomerFocusForbiddenForUser 普通用户拿不到客户关注区(成本口径不得下发)。
func TestCustomerFocusForbiddenForUser(t *testing.T) {
	srv, admin, _ := newTestServer(t)
	base := srv.URL
	bootstrap(t, admin, base)

	code, _ := doJSON(t, admin, http.MethodPost, base+"/api/v1/users",
		map[string]any{"username": "cust", "password": "password123", "role": "user"})
	mustStatus(t, code, http.StatusOK, "create user")

	jar, _ := cookiejar.New(nil)
	cust := &http.Client{Jar: jar}
	code, body := doJSON(t, cust, http.MethodPost, base+"/api/v1/auth/login",
		map[string]any{"username": "cust", "password": "password123"})
	mustStatus(t, code, http.StatusOK, "cust login: "+string(body))

	for _, path := range []string{"/api/v1/customers/focus", "/api/v1/overview"} {
		code, _ := doJSON(t, cust, http.MethodGet, base+path, nil)
		if code != http.StatusForbidden {
			t.Errorf("cust → %s: status %d, want 403", path, code)
		}
	}
}

// seedCharge 落一条带成本/售价的客户日志。
func seedCharge(t *testing.T, st interface {
	InsertLog(domain.LogRow) error
}, at time.Time, ownerID int64, cost, charge float64) {
	t.Helper()
	if err := st.InsertLog(domain.LogRow{
		TS: at, Model: "m", ChannelName: "ch", TokenName: "tk", OwnerID: ownerID,
		Status: 200, PromptTokens: 10, CostUsd: cost, ChargeUsd: charge,
	}); err != nil {
		t.Fatalf("seed charge: %v", err)
	}
}
