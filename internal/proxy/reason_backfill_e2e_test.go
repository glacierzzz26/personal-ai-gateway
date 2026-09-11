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

// TestE2EThinkingReasoningBackfill 复现线上故障:thinking 模型跑工具轮次时,
// 第 2 轮必须把上一轮的 reasoning_content 带回去,否则上游回
//   400 The `reasoning_content` in the thinking mode must be passed back to the API.
//
// anthropic 客户端根本没有这个字段可回传,只能靠网关记着再补回(见 reasoncache)。
// 这个测试走完整网关链路:上游 SSE → a2o 翻译 → 客户端 anthropic 事件 →
// 客户端下一轮请求 → 回填 → 断言到达上游的请求体。
func TestE2EThinkingReasoningBackfill(t *testing.T) {
	e := newE2E(t)

	var (
		mu     sync.Mutex
		bodies []string
	)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		n := len(bodies)
		mu.Unlock()

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
		chunk := func(delta map[string]any) {
			sse(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta}}})
		}
		usage := func(p, c int) {
			sse(map[string]any{"choices": []any{}, "usage": map[string]any{"prompt_tokens": p, "completion_tokens": c}})
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
			if fl != nil {
				fl.Flush()
			}
		}

		if n == 1 {
			// 第 1 轮:上游先吐 reasoning_content,再给工具调用
			chunk(map[string]any{"role": "assistant", "reasoning_content": "让我想想"})
			chunk(map[string]any{"reasoning_content": "该查天气"})
			chunk(map[string]any{"tool_calls": []any{map[string]any{
				"index": 0, "id": "call_1", "type": "function",
				"function": map[string]any{"name": "get_weather", "arguments": `{"city":"北京"}`},
			}}})
			sse(map[string]any{"choices": []any{map[string]any{"index": 0, "finish_reason": "tool_calls"}}})
			usage(10, 5)
			return
		}
		// 第 2 轮:正常文本收尾
		chunk(map[string]any{"content": "晴"})
		sse(map[string]any{"choices": []any{map[string]any{"index": 0, "finish_reason": "stop"}}})
		usage(12, 2)
	})
	up := httptest.NewServer(h)
	t.Cleanup(up.Close)

	chID := e.addChannel("cc", domain.ProviderOpenAI, up.URL, "sk-up", 1)
	model := "deepseek/deepseek-v4.1-flash"
	e.addModelOffer(model, chID, 1)
	key := e.addToken("claude", []string{"*"}, 100)

	// —— 第 1 轮:用户提问 ——
	body1 := fmt.Sprintf(`{"model":%q,"max_tokens":64,"stream":true,
		"messages":[{"role":"user","content":"北京天气如何?"}]}`, model)
	code, resp1 := e.post("/v1/messages", key, true, body1)
	if code != http.StatusOK {
		t.Fatalf("turn1 status %d body %s", code, resp1)
	}
	// 客户端形状不变:reasoning 绝不能泄漏成 anthropic 事件
	if strings.Contains(resp1, "reasoning") {
		t.Fatalf("reasoning leaked to client: %s", resp1)
	}
	encID := extractToolUseID(t, resp1)
	if encID == "" {
		t.Fatalf("no tool_use id in turn1 response: %s", resp1)
	}

	// —— 第 2 轮:客户端回带 assistant(tool_use) + tool_result ——
	// 注意:anthropic 客户端回传不了 reasoning_content,这里刻意不带。
	body2 := fmt.Sprintf(`{"model":%q,"max_tokens":64,"stream":true,"messages":[
		{"role":"user","content":"北京天气如何?"},
		{"role":"assistant","content":[{"type":"tool_use","id":%q,"name":"get_weather","input":{"city":"北京"}}]},
		{"role":"user","content":[{"type":"tool_result","tool_use_id":%q,"content":"晴,25 度"}]}
	]}`, model, encID, encID)
	code, resp2 := e.post("/v1/messages", key, true, body2)
	if code != http.StatusOK {
		t.Fatalf("turn2 status %d body %s", code, resp2)
	}

	// —— 断言:第 2 轮到达上游的请求体里,assistant 消息带回了 reasoning_content ——
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		t.Fatalf("上游收到 %d 次请求, want 2", len(bodies))
	}
	if !strings.Contains(bodies[0], "北京天气如何?") {
		t.Fatalf("turn1 body unexpected: %s", bodies[0])
	}
	// 第 1 轮客户端没给过 reasoning → 上游也收不到(回填只发生在下一轮)
	if strings.Contains(bodies[0], "reasoning_content") {
		t.Fatalf("turn1 不该有 reasoning_content: %s", bodies[0])
	}
	if !strings.Contains(bodies[1], `"reasoning_content":"让我想想该查天气"`) {
		t.Fatalf("第 2 轮未回填 reasoning_content(线上就是这里 400):\n%s", bodies[1])
	}
	// 回填的必须是纯净字符串,不能把 assistant 的 tool_calls 结构弄丢
	if !strings.Contains(bodies[1], "tool_calls") || !strings.Contains(bodies[1], "call_1") {
		t.Fatalf("第 2 轮 assistant 消息丢了 tool_calls:\n%s", bodies[1])
	}
}

// TestE2ENoReasoningWhenUpstreamSilent 上游不用 reasoning_content 时(如 OpenAI 官方),
// 不得凭空注入该字段。
func TestE2ENoReasoningWhenUpstreamSilent(t *testing.T) {
	e := newE2E(t)
	var (
		mu     sync.Mutex
		bodies []string
	)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		n := len(bodies)
		mu.Unlock()
		_ = n
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "c", "object": "chat.completion", "model": "m",
			"choices": []any{map[string]any{"index": 0, "finish_reason": "stop",
				"message": map[string]any{"role": "assistant", "content": "ok"}}},
			"usage": map[string]any{"prompt_tokens": 3, "completion_tokens": 1},
		})
	})
	up := httptest.NewServer(h)
	t.Cleanup(up.Close)
	chID := e.addChannel("oa", domain.ProviderOpenAI, up.URL, "sk-up", 1)
	model := "gpt-5.5"
	e.addModelOffer(model, chID, 1)
	key := e.addToken("cli", []string{"*"}, 100)

	// 先来一轮普通对话,再看后续带 assistant 历史的请求有没有被注入字段
	b1 := fmt.Sprintf(`{"model":%q,"max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`, model)
	if code, body := e.post("/v1/messages", key, true, b1); code != http.StatusOK {
		t.Fatalf("turn1 %d %s", code, body)
	}
	b2 := fmt.Sprintf(`{"model":%q,"max_tokens":16,"messages":[
		{"role":"user","content":"hi"},
		{"role":"assistant","content":"ok"},
		{"role":"user","content":"再来"}
	]}`, model)
	if code, body := e.post("/v1/messages", key, true, b2); code != http.StatusOK {
		t.Fatalf("turn2 %d %s", code, body)
	}

	mu.Lock()
	defer mu.Unlock()
	if strings.Contains(bodies[1], "reasoning_content") {
		t.Fatalf("上游没给过 reasoning,不该注入:\n%s", bodies[1])
	}
}

// extractToolUseID 从 anthropic SSE 里取出 tool_use 块的 id。
func extractToolUseID(t *testing.T, sse string) string {
	t.Helper()
	for _, line := range strings.Split(sse, "\n") {
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var ev struct {
			Type         string `json:"type"`
			ContentBlock struct {
				Type string `json:"type"`
				ID   string `json:"id"`
			} `json:"content_block"`
		}
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data:")), &ev) != nil {
			continue
		}
		if ev.Type == "content_block_start" && ev.ContentBlock.Type == "tool_use" {
			return ev.ContentBlock.ID
		}
	}
	return ""
}
