package proxy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"personal-ai-gateway/internal/domain"
)

// captureUpstream 假 OpenAI:记录收到的出站 model 名;status!=200 时返回错误。
func captureUpstream(t *testing.T, got *string, status int) *httptest.Server {
	t.Helper()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		*got = req.Model
		w.Header().Set("Content-Type", "application/json")
		if status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = fmt.Fprint(w, `{"error":{"message":"boom","type":"server_error"}}`)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-x", "object": "chat.completion", "model": req.Model,
			"choices": []any{map[string]any{"index": 0,
				"message":       map[string]any{"role": "assistant", "content": "ok"},
				"finish_reason": "stop"}},
			"usage": map[string]any{"prompt_tokens": 5, "completion_tokens": 3},
		})
	})
	up := httptest.NewServer(h)
	t.Cleanup(up.Close)
	return up
}

// TestE2EPerOfferUpstreamModelSelectsPerChannel 用户核心诉求:
// 一个对外统一名,按实际命中的候选渠道,出站改写为该渠道各自的真实名。
// 首选渠道 500 → 降级到次选渠道时,必须发次选渠道的真实名(而非首选的真实名 → 静默 404)。
func TestE2EPerOfferUpstreamModelSelectsPerChannel(t *testing.T) {
	e := newE2E(t)
	var gotA, gotB string
	upA := captureUpstream(t, &gotA, http.StatusInternalServerError) // 首选挂掉
	upB := captureUpstream(t, &gotB, http.StatusOK)

	chA := e.addChannel("A", domain.ProviderOpenAI, upA.URL, "sk-a", 1)
	chB := e.addChannel("B", domain.ProviderOpenAI, upB.URL, "sk-b", 2)

	// 单一公开模型名;两条 offer 各配自己的上游真实名。
	en := true
	m, err := e.st.CreateModel(domain.ModelInput{Name: "deepseek-v4.1-flash", Enabled: &en})
	if err != nil {
		t.Fatalf("create model: %v", err)
	}
	pA, pB := 1, 2
	if _, err := e.st.CreateOffer(m.ID, domain.OfferInput{
		ChannelID: chA, Enabled: &en, Priority: &pA, RateLimitRpm: 1000,
		UpstreamModel: "deepseek/deepseek-v4.1-flash",
	}); err != nil {
		t.Fatalf("offer A: %v", err)
	}
	if _, err := e.st.CreateOffer(m.ID, domain.OfferInput{
		ChannelID: chB, Enabled: &en, Priority: &pB, RateLimitRpm: 1000,
		UpstreamModel: "deepseek-v4.1-flash-0902",
	}); err != nil {
		t.Fatalf("offer B: %v", err)
	}
	key := e.addToken("cli", []string{"*"}, 100)

	code, body := e.post("/v1/chat/completions", key, false, fmt.Sprintf(chatBody, "deepseek-v4.1-flash"))
	if code != http.StatusOK {
		t.Fatalf("status %d body %s", code, body)
	}
	if gotA != "deepseek/deepseek-v4.1-flash" {
		t.Errorf("channel A outbound model = %q, want its own upstream", gotA)
	}
	if gotB != "deepseek-v4.1-flash-0902" {
		t.Errorf("channel B (failover) outbound model = %q, want its own upstream name", gotB)
	}
}

// TestE2EUpstreamModelEmptyFallsBackToOriginName 未配上游名时,重命名模型仍用模型级真实名。
func TestE2EUpstreamModelEmptyFallsBackToOriginName(t *testing.T) {
	e := newE2E(t)
	var got string
	up := captureUpstream(t, &got, http.StatusOK)
	ch := e.addChannel("A", domain.ProviderOpenAI, up.URL, "sk-a", 1)

	en := true
	origin, display := "deepseek-chat", "deepseek-v3"
	if _, err := e.st.CreateModel(domain.ModelInput{Name: origin, DisplayName: &display, Enabled: &en}); err != nil {
		t.Fatalf("create model: %v", err)
	}
	m, _ := e.st.GetModelByName(origin)
	if _, err := e.st.CreateOffer(m.ID, domain.OfferInput{ChannelID: ch, Enabled: &en, RateLimitRpm: 1000}); err != nil {
		t.Fatalf("offer: %v", err)
	}
	key := e.addToken("cli", []string{"*"}, 100)

	code, body := e.post("/v1/chat/completions", key, false, fmt.Sprintf(chatBody, display))
	if code != http.StatusOK {
		t.Fatalf("status %d body %s", code, body)
	}
	if got != origin {
		t.Errorf("outbound model = %q, want fallback origin %q", got, origin)
	}
}

// TestE2EPerOfferUpstreamModelOnStream 流式路径同样按候选改写(独立调用点)。
func TestE2EPerOfferUpstreamModelOnStream(t *testing.T) {
	e := newE2E(t)
	var got string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		got = req.Model
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(up.Close)

	ch := e.addChannel("A", domain.ProviderOpenAI, up.URL, "sk-a", 1)
	en := true
	m, _ := e.st.CreateModel(domain.ModelInput{Name: "deepseek-v4.1-flash", Enabled: &en})
	if _, err := e.st.CreateOffer(m.ID, domain.OfferInput{
		ChannelID: ch, Enabled: &en, RateLimitRpm: 1000,
		UpstreamModel: "deepseek/deepseek-v4.1-flash",
	}); err != nil {
		t.Fatalf("offer: %v", err)
	}
	key := e.addToken("cli", []string{"*"}, 100)

	code, body := e.post("/v1/chat/completions", key, false,
		fmt.Sprintf(`{"model":"deepseek-v4.1-flash","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	if code != http.StatusOK {
		t.Fatalf("status %d body %s", code, body)
	}
	if got != "deepseek/deepseek-v4.1-flash" {
		t.Errorf("stream outbound model = %q, want per-offer upstream", got)
	}
}
