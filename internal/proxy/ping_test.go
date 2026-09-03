package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"personal-ai-gateway/internal/config"
)

func TestPingUpstream(t *testing.T) {
	// openai 型:GET {base 含 /v1}/models,Bearer
	openaiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("openai path=%q want /v1/models", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-abc" {
			t.Errorf("openai auth=%q", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer openaiSrv.Close()

	// anthropic 型:GET 根/v1/models,x-api-key
	anthSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("anthropic path=%q want /v1/models", r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "sk-xyz" {
			t.Errorf("anthropic x-api-key=%q", got)
		}
		if got := r.Header.Get("anthropic-version"); got == "" {
			t.Error("missing anthropic-version")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer anthSrv.Close()

	denied := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer denied.Close()

	cases := []struct {
		name string
		up   config.Upstream
		want bool // reachable
	}{
		{"openai ok", config.Upstream{Type: config.TypeOpenAI, BaseURL: openaiSrv.URL + "/v1", APIKey: "sk-abc"}, true},
		{"anthropic ok", config.Upstream{Type: config.TypeAnthropic, BaseURL: anthSrv.URL, APIKey: "sk-xyz"}, true},
		{"key denied", config.Upstream{Type: config.TypeOpenAI, BaseURL: denied.URL + "/v1", APIKey: "bad"}, true},
	}
	for _, c := range cases {
		got := PingUpstream(context.Background(), c.up, 3*time.Second)
		if got.Reachable != c.want {
			t.Errorf("%s: reachable=%v want %v (%s)", c.name, got.Reachable, c.want, got.Message)
		}
	}
	if got := PingUpstream(context.Background(),
		config.Upstream{Type: config.TypeOpenAI, BaseURL: "http://127.0.0.1:1/v1", APIKey: "k"}, time.Second); got.Reachable {
		t.Errorf("unreachable should report not reachable: %+v", got)
	}
}
