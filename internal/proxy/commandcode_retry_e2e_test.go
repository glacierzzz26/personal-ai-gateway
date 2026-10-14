package proxy

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"personal-ai-gateway/internal/domain"
)

// generic400 是 commandcode 上游并发压力下的泛化瞬态 400(与请求内容无关)。
const generic400 = `{"error":{"message":"{\"message\":\"invalid request error trace_id: abc123\",\"type\":\"invalid_request_error\"}","type":"invalid_request_error"}}`

// genuine400 是带成因的真·坏请求 400(参数非法),绝不该被重试。
const genuine400 = `{"error":{"message":"Too small: expected number to be >0","type":"invalid_request_error","param":"max_tokens"}}`

// TestE2ECommandCodeTransient400Retried commandcode 渠道首次回泛化瞬态 400、次次回 200:
// 网关应重试同渠道,客户端最终拿到 200(此前该 400 直接 break,请求被判硬失败)。
func TestE2ECommandCodeTransient400Retried(t *testing.T) {
	e := newE2E(t)
	var hits int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprint(w, generic400)
			return
		}
		_, _ = fmt.Fprint(w, `{"id":"c","object":"chat.completion","model":"m",`+
			`"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],`+
			`"usage":{"prompt_tokens":3,"completion_tokens":2}}`)
	}))
	t.Cleanup(up.Close)

	chID := e.addChannelType("cc", domain.ProviderOpenAI, up.URL, "sk-cc", domain.ChannelTypeCommandCode)
	model := "m-cc"
	e.addModelOffer(model, chID, 1)
	key := e.addToken("cli", []string{"*"}, 100)

	code, body := e.post("/v1/chat/completions", key, false, fmt.Sprintf(chatBody, model))
	if code != http.StatusOK {
		t.Fatalf("瞬态 400 应重试后成功: status %d body %s", code, body)
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Fatalf("上游应被调用 2 次(首次 400 + 重试),实际 %d", got)
	}
	if got := firstText([]byte(body)); got != "ok" {
		t.Errorf("回包内容 = %q, want ok", got)
	}
}

// TestE2ECommandCodeGenuine400NotRetried 带成因的真·坏请求 400 不应重试:
// 上游只被调用一次,客户端原样拿到 400(不因新增重试逻辑被拖慢/掩盖)。
func TestE2ECommandCodeGenuine400NotRetried(t *testing.T) {
	e := newE2E(t)
	var hits int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprint(w, genuine400)
	}))
	t.Cleanup(up.Close)

	chID := e.addChannelType("cc", domain.ProviderOpenAI, up.URL, "sk-cc", domain.ChannelTypeCommandCode)
	model := "m-cc2"
	e.addModelOffer(model, chID, 1)
	key := e.addToken("cli", []string{"*"}, 100)

	code, body := e.post("/v1/chat/completions", key, false, fmt.Sprintf(chatBody, model))
	if code != http.StatusBadRequest {
		t.Fatalf("真·坏请求应原样 400: got %d body %s", code, body)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("真·坏请求不该重试:上游被调用 %d 次(应 1)", got)
	}
}
