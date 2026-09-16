package server

import (
	"net/http"
	"net/http/cookiejar"
	"strconv"
	"testing"

	"personal-ai-gateway/internal/domain"
)

// TestTokenProbe 自检不访问上游、不计费:逐项回答「这个 key 能不能用某模型」。
func TestTokenProbe(t *testing.T) {
	srv, admin, st := newTestServer(t)
	base := srv.URL
	bootstrap(t, admin, base)

	// 造一个可路由的模型(启用渠道 + 启用供给源)。
	en := true
	ch, err := st.CreateChannel(domain.ChannelInput{
		Name: "up", Provider: domain.ProviderOpenAI, BaseURL: "http://up.example/v1",
		APIKey: "sk-x", Priority: 1, Enabled: &en, TimeoutMs: 30000, MaxFailures: 2, CooldownSec: 10,
	})
	if err != nil {
		t.Fatalf("channel: %v", err)
	}
	m, err := st.CreateModel(domain.ModelInput{Name: "claude-sonnet-5", ContextWindow: 200000})
	if err != nil {
		t.Fatalf("model: %v", err)
	}
	if _, err := st.CreateOffer(m.ID, domain.OfferInput{
		ChannelID: ch.ID, InputPriceUsd: 1, OutputPriceUsd: 2, Enabled: &en,
	}); err != nil {
		t.Fatalf("offer: %v", err)
	}

	// 客户 A:有余额,令牌限 claude-*。
	code, body := doJSON(t, admin, http.MethodPost, base+"/api/v1/users",
		map[string]any{"username": "cust", "password": "password123", "role": "user"})
	mustStatus(t, code, http.StatusOK, "create user: "+string(body))
	users := decode[[]map[string]any](t, mustGet(t, admin, base+"/api/v1/users"))
	var custID int64
	for _, u := range users {
		if u["username"] == "cust" {
			custID = int64(u["id"].(float64))
		}
	}
	if _, err := st.TopupBalance(custID, 50, "seed"); err != nil {
		t.Fatalf("topup: %v", err)
	}

	jar, _ := cookiejar.New(nil)
	cust := &http.Client{Jar: jar}
	code, body = doJSON(t, cust, http.MethodPost, base+"/api/v1/auth/login",
		map[string]any{"username": "cust", "password": "password123"})
	mustStatus(t, code, http.StatusOK, "cust login: "+string(body))

	// 建一个只允许 claude-* 的令牌。
	code, body = doJSON(t, cust, http.MethodPost, base+"/api/v1/tokens", map[string]any{
		"name": "k", "allowedModels": []string{"claude-*"},
		"quotaUsd": 10, "rpmLimit": 60, "expiresAt": nil,
	})
	mustStatus(t, code, http.StatusOK, "create token: "+string(body))
	tokID := int64(decode[map[string]any](t, body)["token"].(map[string]any)["id"].(float64))
	probe := func(model string) domain.TokenProbeResp {
		c, b := doJSON(t, cust, http.MethodPost,
			base+"/api/v1/tokens/"+strconv.FormatInt(tokID, 10)+"/probe",
			map[string]any{"model": model})
		mustStatus(t, c, http.StatusOK, "probe: "+string(b))
		return decode[domain.TokenProbeResp](t, b)
	}

	// 允许的模型且可路由 → 全通过。
	if r := probe("claude-sonnet-5"); !r.Ok {
		t.Fatalf("allowed model should pass: %+v", r.Checks)
	}
	// 未授权模型 → 模型授权项失败。
	r := probe("gpt-4o")
	if r.Ok {
		t.Fatal("unauthorized model should fail")
	}
	var authCheck *domain.ProbeCheck
	for i := range r.Checks {
		if r.Checks[i].Name == "模型授权" {
			authCheck = &r.Checks[i]
		}
	}
	if authCheck == nil || authCheck.Ok {
		t.Fatalf("模型授权应失败: %+v", r.Checks)
	}

	// 未知模型 → 模型可用性失败。
	if r := probe("no-such-model"); r.Ok {
		t.Fatal("unknown model should fail")
	}

	// 余额耗尽后,账户余额项失败(钱包是主闸)。
	if _, err := st.TopupBalance(custID, -50, "drain"); err != nil {
		t.Fatalf("drain: %v", err)
	}
	r = probe("claude-sonnet-5")
	if r.Ok {
		t.Fatal("drained balance should fail probe")
	}
	found := false
	for _, c := range r.Checks {
		if c.Name == "账户余额" && !c.Ok {
			found = true
		}
	}
	if !found {
		t.Fatalf("账户余额应失败: %+v", r.Checks)
	}

	// 越权:别人的令牌 id 探测 → 404(不泄露存在性)。
	code, _ = doJSON(t, cust, http.MethodPost, base+"/api/v1/tokens/99999/probe",
		map[string]any{"model": "claude-sonnet-5"})
	mustStatus(t, code, http.StatusNotFound, "probe unknown token")
}
