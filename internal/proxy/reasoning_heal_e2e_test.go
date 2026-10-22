package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"personal-ai-gateway/internal/domain"
)

const reasoning400Body = `{"error":{"message":"Upstream request failed: [invalid_request_error] ` +
	`The reasoning_content in the thinking mode must be passed back to the API. ` +
	`(request_id: test)","type":"invalid_request_error"}}`

// missingReasoning 模拟上游校验:带 tool_calls 的 assistant 消息必须存在
// reasoning_content 字段(空串也算存在)。
func missingReasoning(b []byte) bool {
	var req struct {
		Messages []map[string]any `json:"messages"`
	}
	if json.Unmarshal(b, &req) != nil {
		return false
	}
	for _, m := range req.Messages {
		if m["role"] != "assistant" {
			continue
		}
		if _, ok := m["tool_calls"]; !ok {
			continue
		}
		if _, ok := m["reasoning_content"]; !ok {
			return true
		}
	}
	return false
}

// writeHealJSON 回一个成功的 openai chat.completion(非流)。
func writeHealJSON(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": "chatcmpl-heal", "object": "chat.completion", "model": "m",
		"choices": []any{map[string]any{"index": 0, "finish_reason": "stop",
			"message": map[string]any{"role": "assistant", "content": text}}},
		"usage": map[string]any{"prompt_tokens": 3, "completion_tokens": 1},
	})
}

// writeHealSSE 回一段成功的 openai SSE(流式)。
func writeHealSSE(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	fl, _ := w.(http.Flusher)
	sse := func(v any) {
		raw, _ := json.Marshal(v)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
		if fl != nil {
			fl.Flush()
		}
	}
	sse(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": text}}}})
	sse(map[string]any{"choices": []any{map[string]any{"index": 0, "finish_reason": "stop"}}})
	sse(map[string]any{"choices": []any{}, "usage": map[string]any{"prompt_tokens": 3, "completion_tokens": 1}})
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
	if fl != nil {
		fl.Flush()
	}
}

func reasoningHealAnthropicBody(model string, stream bool) string {
	return fmt.Sprintf(`{
		"model": %q,
		"max_tokens": 32,
		"stream": %t,
		"tools": [{"name": "get_weather", "description": "d",
			"input_schema": {"type": "object", "properties": {"city": {"type": "string"}}}}],
		"messages": [
			{"role": "user", "content": "查天气"},
			{"role": "assistant", "content": [
				{"type": "tool_use", "id": "toolu_gw_Y2FsbF9mYWtlX2lk", "name": "get_weather", "input": {"city": "北京"}}
			]},
			{"role": "user", "content": [
				{"type": "tool_result", "tool_use_id": "toolu_gw_Y2FsbF9mYWtlX2lk", "content": "晴"}
			]}
		]
	}`, model, stream)
}

// TestE2EReasoningHealNonStream 缓存未命中被上游 400 后,网关同渠道补空串重发,
// 客户端拿到成功响应;上游恰好收到 2 次请求,第二次带 reasoning_content:""。
func TestE2EReasoningHealNonStream(t *testing.T) {
	e := newE2E(t)
	var (
		mu     sync.Mutex
		bodies []string
	)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
		if missingReasoning(b) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, reasoning400Body)
			return
		}
		writeHealJSON(w, "ok")
	}))
	t.Cleanup(up.Close)

	chID := e.addChannel("heal", domain.ProviderOpenAI, up.URL, "sk-up", 1)
	model := "m-heal"
	e.addModelOffer(model, chID, 1)
	key := e.addToken("cli", []string{"*"}, 100)

	code, out := e.post("/v1/messages", key, true, reasoningHealAnthropicBody(model, false))
	if code != http.StatusOK {
		t.Fatalf("自愈后应成功: code=%d body=%s", code, out)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		t.Fatalf("上游请求次数 = %d, want 2(先失败再自愈)", len(bodies))
	}
	if !missingReasoning([]byte(bodies[0])) {
		t.Fatalf("第 1 次本应缺 reasoning_content: %s", bodies[0])
	}
	if missingReasoning([]byte(bodies[1])) {
		t.Fatalf("第 2 次仍缺 reasoning_content: %s", bodies[1])
	}
	if !strings.Contains(bodies[1], `"reasoning_content":""`) {
		t.Fatalf("第 2 次应补空串: %s", bodies[1])
	}
}

// TestE2EReasoningHealStream 流式路径同样自愈:首次 400(JSON),重发拿到 SSE。
func TestE2EReasoningHealStream(t *testing.T) {
	e := newE2E(t)
	var (
		mu     sync.Mutex
		bodies []string
	)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		n := len(bodies)
		mu.Unlock()
		if n == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, reasoning400Body)
			return
		}
		writeHealSSE(w, "ok")
	}))
	t.Cleanup(up.Close)

	chID := e.addChannel("heal-stream", domain.ProviderOpenAI, up.URL, "sk-up", 1)
	model := "m-heal-stream"
	e.addModelOffer(model, chID, 1)
	key := e.addToken("cli", []string{"*"}, 100)

	code, out := e.post("/v1/messages", key, true, reasoningHealAnthropicBody(model, true))
	if code != http.StatusOK {
		t.Fatalf("流式自愈后应成功: code=%d body=%s", code, out)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		t.Fatalf("上游请求次数 = %d, want 2", len(bodies))
	}
	if !strings.Contains(bodies[1], `"reasoning_content":""`) {
		t.Fatalf("流式重发未补空串: %s", bodies[1])
	}
}

// TestE2EReasoningHealOnlyOnce 补了空串仍失败时只重发一次,不再循环,原错误照常返回。
func TestE2EReasoningHealOnlyOnce(t *testing.T) {
	e := newE2E(t)
	var hits int
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, reasoning400Body)
	}))
	t.Cleanup(up.Close)

	chID := e.addChannel("heal-twice", domain.ProviderOpenAI, up.URL, "sk-up", 1)
	model := "m-heal-twice"
	e.addModelOffer(model, chID, 1)
	key := e.addToken("cli", []string{"*"}, 100)

	code, _ := e.post("/v1/messages", key, true, reasoningHealAnthropicBody(model, false))
	if code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", code)
	}
	if hits != 2 {
		t.Fatalf("上游请求次数 = %d, want 2(只自愈一次)", hits)
	}
}

// TestE2EOther400NoHeal 其它确定性 400 不触发自愈重发。
func TestE2EOther400NoHeal(t *testing.T) {
	e := newE2E(t)
	var hits int
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"Too small: expected number to be >0","type":"invalid_request_error"}}`)
	}))
	t.Cleanup(up.Close)

	chID := e.addChannel("other-400", domain.ProviderOpenAI, up.URL, "sk-up", 1)
	model := "m-other-400"
	e.addModelOffer(model, chID, 1)
	key := e.addToken("cli", []string{"*"}, 100)

	if code, _ := e.post("/v1/messages", key, true, reasoningHealAnthropicBody(model, false)); code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", code)
	}
	if hits != 1 {
		t.Fatalf("上游请求次数 = %d, want 1(不该自愈)", hits)
	}
}
