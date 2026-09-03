package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"personal-ai-gateway/internal/config"
	"personal-ai-gateway/internal/proxy"
	"personal-ai-gateway/internal/router"
	"personal-ai-gateway/internal/store"
)

const testKey = "sk-test-gateway-1234567890abcdef"

// fakeOpenAI 造一个本地 openai 型假上游;handler 决定其行为。
func fakeOpenAI(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(handler)
	t.Cleanup(s.Close)
	return s
}

func okOpenAIHandler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 校验出站请求带了 Bearer key
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer up-key-") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var req struct {
			Model  string `json:"model"`
			Stream bool   `json:"stream"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		if req.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"id\":\"c1\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "c1",
			"model":   req.Model,
			"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "hi"}}},
			"usage":   map[string]any{"prompt_tokens": 10, "completion_tokens": 5},
		})
	}
}

func buildServer(t *testing.T, ups []config.Upstream) *httptest.Server {
	t.Helper()
	cfg := config.Config{
		Keys: []config.Key{{Name: "laptop", Secret: testKey}},
	}
	cfg.Upstreams = ups
	cfg.DBPath = t.TempDir() + "/gw.db"
	cfg.Listen = ":0"

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	gw := proxy.New(router.New(ups), st)
	ts := httptest.NewServer(New(&cfg, gw, nil).Handler())
	t.Cleanup(ts.Close)
	return ts
}

func doChat(t *testing.T, base, key, model string, stream bool) *http.Response {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"model":    model,
		"stream":   stream,
		"messages": []any{map[string]any{"role": "user", "content": "ping"}},
	})
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost,
		base+"/v1/chat/completions", bytes.NewReader(body))
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do chat: %v", err)
	}
	return resp
}

func TestAuthRequired(t *testing.T) {
	up := fakeOpenAI(t, okOpenAIHandler(t))
	ts := buildServer(t, []config.Upstream{{
		Name: "a", Type: config.TypeOpenAI, BaseURL: up.URL + "/v1", APIKey: "up-key-a",
	}})

	for name, key := range map[string]string{
		"no key":     "",
		"wrong key":  "sk-wrong",
	} {
		t.Run(name, func(t *testing.T) {
			resp := doChat(t, ts.URL, key, "gpt-4o", false)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("want 401, got %d", resp.StatusCode)
			}
		})
	}
}

func TestOpenAIPassthroughAndLog(t *testing.T) {
	up := fakeOpenAI(t, okOpenAIHandler(t))
	ts := buildServer(t, []config.Upstream{{
		Name: "a", Type: config.TypeOpenAI, BaseURL: up.URL + "/v1", APIKey: "up-key-a",
	}})

	resp := doChat(t, ts.URL, testKey, "gpt-4o", false)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	got, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(got), `"content":"hi"`) {
		t.Fatalf("unexpected relay body: %s", got)
	}

	// x-api-key 也要能用(Claude Code 风格)
	resp2 := doChatWithKeyHeader(t, ts.URL, "gpt-4o", false)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("x-api-key want 200, got %d", resp2.StatusCode)
	}
}

func doChatWithKeyHeader(t *testing.T, base, model string, stream bool) *http.Response {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"model": model, "stream": stream, "messages": []any{}})
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost,
		base+"/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("x-api-key", testKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do chat: %v", err)
	}
	return resp
}

func TestFailoverWhenPrimaryDown(t *testing.T) {
	upA := fakeOpenAI(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":{"message":"boom"}}`)
	})
	upB := fakeOpenAI(t, okOpenAIHandler(t))

	ts := buildServer(t, []config.Upstream{
		{Name: "a", Type: config.TypeOpenAI, BaseURL: upA.URL + "/v1", APIKey: "up-key-a", Priority: 1},
		{Name: "b", Type: config.TypeOpenAI, BaseURL: upB.URL + "/v1", APIKey: "up-key-b", Priority: 2},
	})

	resp := doChat(t, ts.URL, testKey, "gpt-4o", false)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("want 200 after failover, got %d: %s", resp.StatusCode, body)
	}
}

func TestStreamingSSE(t *testing.T) {
	up := fakeOpenAI(t, okOpenAIHandler(t))
	ts := buildServer(t, []config.Upstream{{
		Name: "a", Type: config.TypeOpenAI, BaseURL: up.URL + "/v1", APIKey: "up-key-a",
	}})

	resp := doChat(t, ts.URL, testKey, "gpt-4o", true)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	got, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(got), "data: [DONE]") || !strings.Contains(string(got), "hi") {
		t.Fatalf("stream body unexpected: %q", got)
	}
}

func TestCircuitBreakerBlocks(t *testing.T) {
	cfg := []config.Upstream{
		{Name: "a", Type: config.TypeOpenAI, Priority: 1, MaxFailures: 2, CooldownSec: 60},
		{Name: "b", Type: config.TypeOpenAI, Priority: 2},
	}
	rt := router.New(cfg)
	// 反复 500 后,a 打开熔断;候选里应只剩 b。用 rt.Candidates 拿内部指针,保证作用于同一对象。
	first := rt.Candidates("gpt-4o")
	if len(first) != 2 || first[0].Name != "a" {
		t.Fatalf("want [a b] healthy, got %v", first)
	}
	rt.RecordFailure(first[0])
	rt.RecordFailure(first[0])
	if got := rt.Candidates("gpt-4o"); len(got) != 1 || got[0].Name != "b" {
		t.Fatalf("circuit should keep only b, got %v", got)
	}
}

func TestListModels(t *testing.T) {
	up := fakeOpenAI(t, okOpenAIHandler(t))
	ts := buildServer(t, []config.Upstream{{
		Name: "a", Type: config.TypeOpenAI, BaseURL: up.URL + "/v1", APIKey: "up-key-a",
		Models: []string{"claude-sonnet-4-5", "gpt-4o"},
	}})

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+testKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(got), "claude-sonnet-4-5") || !strings.Contains(string(got), `"object":"list"`) {
		t.Fatalf("models body unexpected: %s", got)
	}
}
