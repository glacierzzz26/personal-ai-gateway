package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"personal-ai-gateway/internal/domain"
	"personal-ai-gateway/internal/secret"
)

// cipherOf 用临时主密钥把明文 key 加密为渠道密文(与 store.CreateChannel 同一实现)。
func cipherOf(t *testing.T, plain string) string {
	t.Helper()
	if _, err := secret.BootstrapKey(t.TempDir()); err != nil {
		t.Fatalf("bootstrap master key: %v", err)
	}
	cipher, err := secret.Encrypt(plain)
	if err != nil {
		t.Fatalf("encrypt key: %v", err)
	}
	return cipher
}

// --- 第三方(one-api 形状):既有 /v1/usage 解析路径的回归覆盖 ---

// TestFetchChannelQuota_OK 覆盖 one-api 形状的三窗口 status=ok。
func TestFetchChannelQuota_OK(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"planName": "Pro",
			"usage": {
				"rolling": {"status": "ok", "percent": 12},
				"weekly":  {"status": "ok", "percent": 34},
				"monthly": {"status": "ok", "percent": 56}
			}
		}`))
	}))
	defer srv.Close()

	rl := &Relay{}
	res, err := rl.FetchChannelQuota(context.Background(), srv.Client(), domain.ChannelRow{
		BaseURL:      srv.URL,
		ChannelType:  domain.ChannelTypeThirdParty,
		QuotaPath:    "/v1/usage",
		QuotaShape:   string(domain.ShapeUsage),
		APIKeyCipher: cipherOf(t, "sk-test"),
	})
	if err != nil {
		t.Fatalf("FetchChannelQuota: %v", err)
	}
	if gotPath != "/v1/usage" {
		t.Errorf("path = %q, want /v1/usage", gotPath)
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("authorization = %q, want Bearer sk-test", gotAuth)
	}
	if res.PlanName != "Pro" {
		t.Errorf("planName = %q, want Pro", res.PlanName)
	}
	want := map[string]float64{"rolling": 12, "weekly": 34, "monthly": 56}
	for k, p := range want {
		w, ok := res.Windows[k]
		if !ok {
			t.Errorf("window %q missing", k)
			continue
		}
		if w.Status != "ok" || w.Percent != p {
			t.Errorf("window %q = %+v, want status=ok percent=%v", k, w, p)
		}
	}
	if len(res.Windows) != 3 {
		t.Errorf("windows = %v, want exactly 3 ok windows", res.Windows)
	}
}

// TestFetchChannelQuota_SkipsNonOK 非 ok / 字段缺失的窗口应被略去。
func TestFetchChannelQuota_SkipsNonOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"usage": {
				"rolling": {"status": "error"},
				"weekly":  {"status": "ok", "percent": 20},
				"monthly": {}
			}
		}`))
	}))
	defer srv.Close()

	rl := &Relay{}
	res, err := rl.FetchChannelQuota(context.Background(), srv.Client(), domain.ChannelRow{
		BaseURL:     srv.URL,
		ChannelType: domain.ChannelTypeThirdParty,
		QuotaPath:   "/v1/usage",
	})
	if err != nil {
		t.Fatalf("FetchChannelQuota: %v", err)
	}
	if len(res.Windows) != 1 {
		t.Fatalf("windows = %v, want only weekly", res.Windows)
	}
	if w, ok := res.Windows["weekly"]; !ok || w.Percent != 20 {
		t.Errorf("windows = %v, want weekly.ok 20", res.Windows)
	}
}

// TestFetchChannelQuota_Non2xx 上游非 2xx 应报错(UI 侧判"不支持/失败")。
func TestFetchChannelQuota_Non2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	rl := &Relay{}
	_, err := rl.FetchChannelQuota(context.Background(), srv.Client(), domain.ChannelRow{
		BaseURL:     srv.URL,
		ChannelType: domain.ChannelTypeThirdParty,
		QuotaPath:   "/v1/usage",
	})
	if err == nil {
		t.Fatal("want error for non-2xx upstream")
	}
}

// TestFetchChannelQuota_ThirdPartyUnconfigured 第三方渠道未填额度路径 → 明确提示去配置。
func TestFetchChannelQuota_ThirdPartyUnconfigured(t *testing.T) {
	rl := &Relay{}
	_, err := rl.FetchChannelQuota(context.Background(), http.DefaultClient, domain.ChannelRow{
		BaseURL:     "https://us.example.com",
		ChannelType: domain.ChannelTypeThirdParty,
	})
	if !errors.Is(err, ErrQuotaNotConfigured) {
		t.Fatalf("err = %v, want ErrQuotaNotConfigured", err)
	}
}

// TestFetchChannelQuota_ThirdPartyHTMLIsError 路由不存在时 SPA 回 200 HTML,不能当成功解析。
func TestFetchChannelQuota_ThirdPartyHTMLIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html><html><body>app</body></html>`))
	}))
	defer srv.Close()

	rl := &Relay{}
	_, err := rl.FetchChannelQuota(context.Background(), srv.Client(), domain.ChannelRow{
		BaseURL:      srv.URL,
		ChannelType:  domain.ChannelTypeThirdParty,
		QuotaPath:    "/v1/dashboard/billing/subscription",
		APIKeyCipher: cipherOf(t, "sk-x"),
	})
	if err == nil {
		t.Fatal("want error for HTML body")
	}
}

// TestFetchChannelQuota_ThirdParty200ErrorBody 中转失败回 200 + {"error":...} 也要判失败。
func TestFetchChannelQuota_ThirdParty200ErrorBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"error":{"message":"无效的令牌","type":"invalid_request_error"}}`))
	}))
	defer srv.Close()

	rl := &Relay{}
	_, err := rl.FetchChannelQuota(context.Background(), srv.Client(), domain.ChannelRow{
		BaseURL:      srv.URL,
		ChannelType:  domain.ChannelTypeThirdParty,
		QuotaPath:    "/api/user/self",
		QuotaShape:   string(domain.ShapeNewAPIUser),
		APIKeyCipher: cipherOf(t, "sk-x"),
	})
	if err == nil {
		t.Fatal("want error for 200-with-error body")
	}
}

// TestFetchChannelQuota_OneAPIBalance 一次订阅+用量两个请求,算出余额与月已用%。
func TestFetchChannelQuota_OneAPIBalance(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/dashboard/billing/subscription":
			_, _ = w.Write([]byte(`{"hard_limit_usd": 20}`))
		case "/v1/dashboard/billing/usage":
			_, _ = w.Write([]byte(`{"total_usage": 500}`)) // 500 美分 = $5
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	rl := &Relay{}
	res, err := rl.FetchChannelQuota(context.Background(), srv.Client(), domain.ChannelRow{
		BaseURL:     srv.URL,
		ChannelType: domain.ChannelTypeThirdParty,
		QuotaPath:   "/v1/dashboard/billing/subscription",
		QuotaShape:  string(domain.ShapeOneAPI),
	})
	if err != nil {
		t.Fatalf("FetchChannelQuota: %v", err)
	}
	if res.Balance == nil || res.Balance.Amount != 15 {
		t.Fatalf("balance = %+v, want 15 USD", res.Balance)
	}
	if w := res.Windows["monthly"]; w.Percent != 25 {
		t.Errorf("monthly percent = %v, want 25", w.Percent)
	}
}

// --- deepseek 官方:只有绝对余额,没有窗口 ---

// TestFetchChannelQuota_DeepSeekBalance 余额接口:apiRoot(base_url) + /user/balance,解析字符串金额。
func TestFetchChannelQuota_DeepSeekBalance(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user/balance" {
			t.Errorf("path = %q, want /user/balance", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"110.00","granted_balance":"10.00","topped_up_balance":"100.00"}]}`))
	}))
	defer srv.Close()

	rl := &Relay{}
	res, err := rl.FetchChannelQuota(context.Background(), srv.Client(), domain.ChannelRow{
		ChannelType: domain.ChannelTypeDeepSeek,
		BaseURL:     srv.URL + "/v1", // 用户按惯例带 /v1,apiRoot 应把它去掉
	})
	if err != nil {
		t.Fatalf("FetchChannelQuota: %v", err)
	}
	if res.Balance == nil || res.Balance.Amount != 110 || res.Balance.Currency != "CNY" {
		t.Fatalf("balance = %+v, want 110 CNY", res.Balance)
	}
	if len(res.Windows) != 0 {
		t.Errorf("windows = %v, want none for balance-only upstream", res.Windows)
	}
}

// --- command code:窗口 used/cap + 剩余额度 ---

// TestFetchChannelQuota_CommandCode 窗口百分比由 used/cap 推出,剩余额度为 credits 之和。
func TestFetchChannelQuota_CommandCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/alpha/billing/credits" {
			t.Errorf("path = %q, want /alpha/billing/credits", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{
			"credits": {"monthlyCredits": 53.78, "purchasedCredits": 0, "freeCredits": 0},
			"windowLimits": {
				"limited": true,
				"fiveHour": {"used": 1.24, "cap": 14, "exceeded": false, "resetAt": 1789455020098},
				"weekly":   {"used": 16.21, "cap": 35, "resetAt": 1789455020098}
			}
		}`))
	}))
	defer srv.Close()

	old := commandCodeHost
	commandCodeHost = srv.URL
	defer func() { commandCodeHost = old }()

	rl := &Relay{}
	res, err := rl.FetchChannelQuota(context.Background(), srv.Client(), domain.ChannelRow{
		ChannelType:  domain.ChannelTypeCommandCode,
		APIKeyCipher: cipherOf(t, "user_test"),
	})
	if err != nil {
		t.Fatalf("FetchChannelQuota: %v", err)
	}
	if res.Balance == nil || res.Balance.Amount != 53.78 {
		t.Fatalf("balance = %+v, want 53.78", res.Balance)
	}
	rolling, ok := res.Windows["rolling"]
	if !ok {
		t.Fatalf("rolling window missing: %v", res.Windows)
	}
	if rolling.Used != 1.24 || rolling.Cap != 14 {
		t.Errorf("rolling = %+v, want used=1.24 cap=14", rolling)
	}
	if rolling.Percent < 8.8 || rolling.Percent > 8.9 { // 1.24/14 = 8.857%
		t.Errorf("rolling percent = %v, want ≈8.86", rolling.Percent)
	}
	if rolling.ResetAt == "" {
		t.Error("resetAt should be normalized from epoch ms")
	}
}

// --- opencode:与 /v1/usage 同形状,只换 host 与路径 ---

// TestFetchChannelQuota_OpenCode opencode zen 复用 usage 解析器,resetsAt 归一为 RFC3339。
func TestFetchChannelQuota_OpenCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/zen/go/v1/usage" {
			t.Errorf("path = %q, want /zen/go/v1/usage", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{
			"usage": {
				"rolling": {"status": "ok", "percent": 10, "resetsAt": "2026-09-15T12:00:00Z"},
				"weekly":  {"status": "rate-limited", "percent": 99}
			}
		}`))
	}))
	defer srv.Close()

	rl := &Relay{}
	res, err := rl.FetchChannelQuota(context.Background(), srv.Client(), domain.ChannelRow{
		ChannelType:  domain.ChannelTypeOpenCode,
		BaseURL:      srv.URL + "/zen/go/v1", // 归一后拼回 /zen/go/v1/usage
		APIKeyCipher: cipherOf(t, "sk-oc"),
	})
	if err != nil {
		t.Fatalf("FetchChannelQuota: %v", err)
	}
	rolling, ok := res.Windows["rolling"]
	if !ok || rolling.Percent != 10 {
		t.Fatalf("rolling = %+v, want ok 10", rolling)
	}
	if rolling.ResetAt != "2026-09-15T12:00:00Z" {
		t.Errorf("resetAt = %q, want normalized RFC3339", rolling.ResetAt)
	}
	if _, ok := res.Windows["weekly"]; ok {
		t.Errorf("rate-limited window should be dropped, got %v", res.Windows)
	}
}

// TestParseQuotaWindow_NonJSON 不可解析的窗口体不应崩(当作无窗口)。
func TestParseQuotaWindow_NonJSON(t *testing.T) {
	if _, ok := parseQuotaWindow(json.RawMessage(`not-json`)); ok {
		t.Fatal("want invalid raw to be dropped")
	}
	if _, ok := parseQuotaWindow(json.RawMessage(`{"status":"ok","percent":"oops"}`)); ok {
		t.Fatal("want non-numeric percent to be dropped")
	}
}
