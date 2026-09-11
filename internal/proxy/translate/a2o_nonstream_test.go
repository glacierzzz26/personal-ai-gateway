package translate

import (
	"encoding/json"
	"strings"
	"testing"
)

// —— ConvertNonStream:openai chat.completion → anthropic message ——

func TestConvertNonStreamTextAndUsage(t *testing.T) {
	raw := `{
		"id": "chatcmpl-1",
		"model": "deepseek-chat",
		"choices": [{"message": {"role": "assistant", "content": "你好"}, "finish_reason": "stop"}],
		"usage": {"prompt_tokens": 20, "completion_tokens": 7, "prompt_tokens_details": {"cached_tokens": 5}}
	}`
	out, tok, _, err := ConvertNonStream(ProtoAnthropic, ProtoOpenAI, []byte(raw))
	if err != nil {
		t.Fatalf("ConvertNonStream: %v", err)
	}
	var msg struct {
		ID         string `json:"id"`
		Type       string `json:"type"`
		Role       string `json:"role"`
		Model      string `json:"model"`
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
			CacheRead    int `json:"cache_read_input_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(out, &msg); err != nil {
		t.Fatalf("unmarshal anthropic body: %v", err)
	}
	if msg.Type != "message" || msg.Role != "assistant" || msg.Model != "deepseek-chat" {
		t.Errorf("id/type/role/model = %q/%q/%q/%q", msg.ID, msg.Type, msg.Role, msg.Model)
	}
	if msg.StopReason != "end_turn" {
		t.Errorf("stop_reason = %q, want end_turn", msg.StopReason)
	}
	if len(msg.Content) != 1 || msg.Content[0].Type != "text" || msg.Content[0].Text != "你好" {
		t.Errorf("content = %+v", msg.Content)
	}
	// prompt 减缓存命中 → anthropic input_tokens 语义
	if msg.Usage.InputTokens != 15 || msg.Usage.OutputTokens != 7 || msg.Usage.CacheRead != 5 {
		t.Errorf("usage = %+v, want input 15 output 7 cache 5", msg.Usage)
	}
	if tok.Prompt != 15 || tok.Completion != 7 || tok.CacheRead != 5 {
		t.Errorf("Usage = %+v", tok)
	}
}

func TestConvertNonStreamToolUse(t *testing.T) {
	raw := `{
		"id": "chatcmpl-2",
		"model": "deepseek-chat",
		"choices": [{"message": {"role": "assistant",
			"content": "查一下",
			"tool_calls": [{"id": "call_xyz", "type": "function",
				"function": {"name": "get_weather", "arguments": "{\"city\": \"北京\"}"}}]},
			"finish_reason": "tool_calls"}],
		"usage": {"prompt_tokens": 10, "completion_tokens": 5, "prompt_tokens_details": {"cached_tokens": 0}}
	}`
	out, _, _, err := ConvertNonStream(ProtoAnthropic, ProtoOpenAI, []byte(raw))
	if err != nil {
		t.Fatalf("ConvertNonStream: %v", err)
	}
	var msg struct {
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
	}
	if err := json.Unmarshal(out, &msg); err != nil {
		t.Fatal(err)
	}
	if msg.StopReason != "tool_use" {
		t.Errorf("stop_reason = %q, want tool_use", msg.StopReason)
	}
	if len(msg.Content) != 2 {
		t.Fatalf("content len = %d, want 2 (text + tool_use)", len(msg.Content))
	}
	tu := msg.Content[1]
	if tu.Type != "tool_use" || tu.Name != "get_weather" {
		t.Errorf("tool_use block = %+v", tu)
	}
	if tu.ID != OpenAItoAnthropicToolID("call_xyz") {
		t.Errorf("tool_use id = %q, want encoded", tu.ID)
	}
	var input map[string]string
	if err := json.Unmarshal(tu.Input, &input); err != nil || input["city"] != "北京" {
		t.Errorf("tool_use input = %s, want parsed object", tu.Input)
	}
}

func TestConvertNonStreamBadArgumentsFallsBackToRaw(t *testing.T) {
	// arguments 解析失败 → {"raw": ...},不丢整轮
	raw := `{
		"model": "deepseek-chat",
		"choices": [{"message": {"role": "assistant",
			"tool_calls": [{"id": "call_1", "type": "function", "function": {"name": "f", "arguments": "{not json"}}]},
			"finish_reason": "tool_calls"}],
		"usage": {"prompt_tokens": 1, "completion_tokens": 1}
	}`
	out, _, _, err := ConvertNonStream(ProtoAnthropic, ProtoOpenAI, []byte(raw))
	if err != nil {
		t.Fatalf("ConvertNonStream: %v", err)
	}
	var msg struct {
		Content []struct {
			Type  string          `json:"type"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
	}
	if err := json.Unmarshal(out, &msg); err != nil {
		t.Fatal(err)
	}
	if len(msg.Content) != 1 {
		t.Fatalf("content len = %d", len(msg.Content))
	}
	var rawMap map[string]any
	if err := json.Unmarshal(msg.Content[0].Input, &rawMap); err != nil {
		t.Fatalf("input = %s: %v", msg.Content[0].Input, err)
	}
	if rawMap["raw"] != "{not json" {
		t.Errorf("input = %v, want {\"raw\": \"{not json\"}", rawMap)
	}
}

func TestConvertNonStreamReasoningCapturedNotLeaked(t *testing.T) {
	// DeepSeek-reasoner 的 reasoning_content 不进 anthropic body(客户端只见最终 content),
	// 但要被 Capture 记下来供下一轮回填(否则 thinking 模式多轮必 400)。
	raw := `{
		"model": "deepseek-reasoner",
		"choices": [{"message": {"role": "assistant",
			"content": "答案是 4", "reasoning_content": "思考过程……"}, "finish_reason": "stop"}],
		"usage": {"prompt_tokens": 3, "completion_tokens": 2}
	}`
	out, _, cap, err := ConvertNonStream(ProtoAnthropic, ProtoOpenAI, []byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if cap.Reasoning != "思考过程……" {
		t.Fatalf("capture.reasoning = %q, want 思考过程……", cap.Reasoning)
	}
	if cap.Text != "答案是 4" {
		t.Fatalf("capture.text = %q", cap.Text)
	}
	if !json.Valid(out) {
		t.Fatalf("out not valid json")
	}
	// 响应体里绝不能出现 reasoning(客户端形状不变)
	if strings.Contains(string(out), "reasoning") {
		t.Fatalf("reasoning leaked into anthropic response: %s", out)
	}
	// 不要有 reasoning 字段泄漏;content 只有 text 块
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	content := m["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("content len = %d, want 1", len(content))
	}
	blk := content[0].(map[string]any)
	if blk["text"] != "答案是 4" {
		t.Errorf("text = %v", blk["text"])
	}
}

// —— 上游错误分类(A2OError)——

func TestA2OErrorClassification(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   string
	}{
		{400, `{"error":{"message":"bad"}}`, "invalid_request_error"},
		{422, `{"error":{"message":"bad"}}`, "invalid_request_error"},
		{401, `{"error":{"message":"auth"}}`, "authentication_error"},
		{403, `{"error":{"message":"no"}}`, "permission_error"},
		{404, `{"error":{"message":"gone"}}`, "not_found_error"},
		{429, `{"error":{"message":"slow"}}`, "rate_limit_error"},
		{500, `not json at all`, "api_error"},
	}
	for _, c := range cases {
		typ, msg := A2OError(c.status, []byte(c.body))
		if typ != c.want {
			t.Errorf("status %d: type = %q, want %q", c.status, typ, c.want)
		}
		if msg == "" {
			t.Errorf("status %d: empty message", c.status)
		}
	}
}

func TestA2OErrorMessageExtracted(t *testing.T) {
	typ, msg := A2OError(400, []byte(`{"error":{"message":"insufficient balance","type":"insufficient_quota"}}`))
	if typ != "invalid_request_error" {
		t.Errorf("type = %q", typ)
	}
	if msg != "insufficient balance" {
		t.Errorf("msg = %q, want provider message, not raw body", msg)
	}
}
