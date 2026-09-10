package translate

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// —— o2a 计费口径回归:缓存命中不得既进 Prompt(按输入价)又进 CacheRead(按缓存价)——
//
// anthropic 的 input_tokens 不含缓存;归一化 Usage.Prompt 必须只含按输入价计费的部分,
// 否则 costUsd 会对缓存命中重复计费。openai 客户端要看的 prompt_tokens(含缓存)由
// o2aUsageJSON 用 Prompt+CacheRead 还原。

func TestO2AUsageCacheExcludedFromPrompt(t *testing.T) {
	raw := `{
		"id": "msg_1", "type": "message", "role": "assistant", "model": "claude-sonnet-4-5",
		"stop_reason": "end_turn",
		"content": [{"type": "text", "text": "hi"}],
		"usage": {"input_tokens": 100, "output_tokens": 50, "cache_creation_input_tokens": 10, "cache_read_input_tokens": 900}
	}`
	out, tok, err := ConvertNonStream(ProtoOpenAI, ProtoAnthropic, []byte(raw))
	if err != nil {
		t.Fatalf("ConvertNonStream: %v", err)
	}
	var resp struct {
		Usage struct {
			PromptTokens int `json:"prompt_tokens"`
			Completion   int `json:"completion_tokens"`
			Details      struct {
				Cached int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("unmarshal openai body: %v", err)
	}
	// openai 客户端口径:prompt_tokens 含全部输入(100+10+900)
	if resp.Usage.PromptTokens != 1010 || resp.Usage.Completion != 50 || resp.Usage.Details.Cached != 900 {
		t.Errorf("openai usage = %+v, want prompt 1010 completion 50 cached 900", resp.Usage)
	}
	// 计费口径:Prompt 剔缓存(100+10),CacheRead 单列(900)
	if tok.Prompt != 110 || tok.Completion != 50 || tok.CacheRead != 900 {
		t.Errorf("Usage = %+v, want prompt 110 completion 50 cacheRead 900", tok)
	}
}

func TestO2AStreamUsageCacheExcludedFromPrompt(t *testing.T) {
	src := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_1","model":"claude-sonnet-4-5","usage":{"input_tokens":100,"output_tokens":1,"cache_creation_input_tokens":10,"cache_read_input_tokens":900}}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":50}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")

	rec := httptest.NewRecorder()
	u, err := convertO2AStream(strings.NewReader(src), rec, "claude-sonnet-4-5")
	if err != nil {
		t.Fatalf("convertO2AStream: %v", err)
	}
	if u.Prompt != 110 || u.Completion != 50 || u.CacheRead != 900 {
		t.Errorf("Usage = %+v, want prompt 110 completion 50 cacheRead 900", u)
	}
	usage := lastStreamUsage(t, rec.Body.String())
	if fnum(usage["prompt_tokens"]) != 1010 || fnum(usage["completion_tokens"]) != 50 {
		t.Errorf("openai stream usage = %+v, want prompt 1010 completion 50", usage)
	}
	if det, ok := usage["prompt_tokens_details"].(map[string]any); !ok || fnum(det["cached_tokens"]) != 900 {
		t.Errorf("openai stream cached_tokens = %+v, want 900", usage["prompt_tokens_details"])
	}
}

// lastStreamUsage 取 o2a 流里最后一个带 usage 的 chunk(收尾 chunk)。
func lastStreamUsage(t *testing.T, raw string) map[string]any {
	t.Helper()
	var last map[string]any
	for _, line := range strings.Split(raw, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			continue
		}
		var m map[string]any
		if json.Unmarshal([]byte(payload), &m) != nil {
			continue
		}
		if u, ok := m["usage"].(map[string]any); ok {
			last = u
		}
	}
	if last == nil {
		t.Fatalf("no usage chunk in stream output:\n%s", raw)
	}
	return last
}
