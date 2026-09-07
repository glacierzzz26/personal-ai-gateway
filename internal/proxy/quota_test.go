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

// TestFetchChannelQuota_OK 覆盖 issue 示例 /v1/usage body:三个窗口 status=ok。
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
	plan, windows, _, err := rl.FetchChannelQuota(context.Background(), srv.Client(), domain.ChannelRow{
		BaseURL:      srv.URL,
		Provider:     domain.ProviderOpenAI,
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
	if plan != "Pro" {
		t.Errorf("planName = %q, want Pro", plan)
	}
	want := map[string]float64{"rolling": 12, "weekly": 34, "monthly": 56}
	for k, p := range want {
		w, ok := windows[k]
		if !ok {
			t.Errorf("window %q missing", k)
			continue
		}
		if w.Status != "ok" || w.Percent != p {
			t.Errorf("window %q = %+v, want status=ok percent=%v", k, w, p)
		}
	}
	if len(windows) != 3 {
		t.Errorf("windows = %v, want exactly 3 ok windows", windows)
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
	_, windows, _, err := rl.FetchChannelQuota(context.Background(), srv.Client(), domain.ChannelRow{
		BaseURL:  srv.URL,
		Provider: domain.ProviderOpenAI,
	})
	if err != nil {
		t.Fatalf("FetchChannelQuota: %v", err)
	}
	if len(windows) != 1 {
		t.Fatalf("windows = %v, want only weekly", windows)
	}
	if w, ok := windows["weekly"]; !ok || w.Percent != 20 {
		t.Errorf("windows = %v, want weekly.ok 20", windows)
	}
}

// TestFetchChannelQuota_Non2xx 上游非 2xx 应报错(UI 侧判"不支持/失败")。
func TestFetchChannelQuota_Non2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	rl := &Relay{}
	_, _, _, err := rl.FetchChannelQuota(context.Background(), srv.Client(), domain.ChannelRow{
		BaseURL:  srv.URL,
		Provider: domain.ProviderOpenAI,
	})
	if err == nil {
		t.Fatal("want error for non-2xx upstream")
	}
}

// TestFetchChannelQuota_AnthropicUnsupported Anthropic 无额度接口。
func TestFetchChannelQuota_AnthropicUnsupported(t *testing.T) {
	rl := &Relay{}
	_, _, _, err := rl.FetchChannelQuota(context.Background(), http.DefaultClient, domain.ChannelRow{
		BaseURL:  "https://api.anthropic.com",
		Provider: domain.ProviderAnthropic,
	})
	if !errors.Is(err, ErrQuotaUnsupported) {
		t.Fatalf("err = %v, want ErrQuotaUnsupported", err)
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
