package translate

import (
	"encoding/json"
	"strings"
	"testing"
)

// —— BuildRequest:anthropic 请求体 → openai chat/completions 请求体 ——

func TestBuildRequestSystemArrayToSystemMessage(t *testing.T) {
	body := `{
		"model": "deepseek-chat",
		"system": [{"type": "text", "text": "你是翻译助手"}, {"type": "text", "text": "保持简洁"}],
		"max_tokens": 64,
		"messages": [{"role": "user", "content": "你好"}]
	}`
	outOp, out, estIn, err := BuildRequest(ProtoAnthropic, ProtoOpenAI, OpMessages, []byte(body), false)
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	if outOp != "chat" {
		t.Errorf("outOp = %q, want chat", outOp)
	}
	if estIn <= 0 {
		t.Errorf("estIn = %d, want > 0", estIn)
	}
	var got struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Stream bool `json:"stream"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal out: %v", err)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("messages len = %d, want 2 (system + user)", len(got.Messages))
	}
	if got.Messages[0].Role != "system" {
		t.Errorf("messages[0].role = %q, want system", got.Messages[0].Role)
	}
	// 数组里两块 text 要拼成一条 system 字符串
	want := "你是翻译助手保持简洁"
	if got.Messages[0].Content != want {
		t.Errorf("system content = %q, want %q", got.Messages[0].Content, want)
	}
	if got.Stream {
		t.Errorf("stream should be false for non-stream")
	}
}

func TestBuildRequestStreamSetsIncludeUsage(t *testing.T) {
	body := `{"model": "glm-4-flash", "messages": [{"role": "user", "content": "hi"}]}`
	_, out, _, err := BuildRequest(ProtoAnthropic, ProtoOpenAI, OpMessages, []byte(body), true)
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	var got struct {
		Stream        bool `json:"stream"`
		StreamOptions struct {
			IncludeUsage bool `json:"include_usage"`
		} `json:"stream_options"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.Stream {
		t.Errorf("stream = false, want true")
	}
	if !got.StreamOptions.IncludeUsage {
		t.Errorf("stream_options.include_usage = false, want true (usage 末块)")
	}
}

func TestBuildRequestToolUseToToolCalls(t *testing.T) {
	// 完整的工具轮:assistant 带 text + tool_use,user 带 tool_result → 双向改写。
	// 客户端回带的是我们上一轮编码后的 id(toolu_+b64url),这里走真实往返。
	providerID := "call_abc123"
	encID := OpenAItoAnthropicToolID(providerID)
	body := `{
		"model": "deepseek-chat",
		"max_tokens": 128,
		"tools": [{"name": "get_weather", "description": "查天气", "input_schema": {"type": "object", "properties": {"city": {"type": "string"}}}}],
		"tool_choice": {"type": "auto"},
		"messages": [
			{"role": "user", "content": "北京天气如何?"},
			{"role": "assistant", "content": [
				{"type": "text", "text": "我来查"},
				{"type": "tool_use", "id": "` + encID + `", "name": "get_weather", "input": {"city": "北京"}}
			]},
			{"role": "user", "content": [
				{"type": "tool_result", "tool_use_id": "` + encID + `", "content": "晴,25 度"}
			]}
		]
	}`
	_, out, _, err := BuildRequest(ProtoAnthropic, ProtoOpenAI, OpMessages, []byte(body), false)
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	var got struct {
		Messages   []json.RawMessage `json:"messages"`
		Tools      []any             `json:"tools"`
		ToolChoice any               `json:"tool_choice"`
		MaxTokens  int               `json:"max_tokens"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.MaxTokens != 128 {
		t.Errorf("max_tokens = %d, want 128 (deepseek/glm 需要显式)", got.MaxTokens)
	}
	if len(got.Messages) != 3 {
		t.Fatalf("messages len = %d, want 3 (user, assistant, tool)", len(got.Messages))
	}

	var user0 map[string]any
	if err := json.Unmarshal(got.Messages[0], &user0); err != nil {
		t.Fatal(err)
	}
	if user0["role"] != "user" || user0["content"] != "北京天气如何?" {
		t.Errorf("messages[0] = %v, want user text", user0)
	}

	var asst map[string]any
	if err := json.Unmarshal(got.Messages[1], &asst); err != nil {
		t.Fatal(err)
	}
	if asst["role"] != "assistant" {
		t.Fatalf("messages[1].role = %v, want assistant", asst["role"])
	}
	if asst["content"] != "我来查" {
		t.Errorf("assistant content = %v, want 拼接文本 我来查", asst["content"])
	}
	calls, ok := asst["tool_calls"].([]any)
	if !ok || len(calls) != 1 {
		t.Fatalf("assistant tool_calls = %v, want 1", asst["tool_calls"])
	}
	call := calls[0].(map[string]any)
	if call["id"] != providerID {
		t.Errorf("tool_call id = %v, want decoded provider id %q", call["id"], providerID)
	}
	fn := call["function"].(map[string]any)
	if fn["name"] != "get_weather" {
		t.Errorf("tool_call function.name = %v", fn["name"])
	}
	args, ok := fn["arguments"].(string)
	if !ok {
		t.Fatalf("arguments type = %T, want JSON string", fn["arguments"])
	}
	// anthropic input 是对象 → openai arguments 必须是字符串;压缩后应无多余空白
	if args != `{"city":"北京"}` {
		t.Errorf("arguments = %q, want compact JSON string", args)
	}

	var toolMsg map[string]any
	if err := json.Unmarshal(got.Messages[2], &toolMsg); err != nil {
		t.Fatal(err)
	}
	if toolMsg["role"] != "tool" {
		t.Fatalf("messages[2].role = %v, want tool", toolMsg["role"])
	}
	if toolMsg["tool_call_id"] != providerID {
		t.Errorf("tool_call_id = %v, want %q", toolMsg["tool_call_id"], providerID)
	}
	if toolMsg["content"] != "晴,25 度" {
		t.Errorf("tool content = %v", toolMsg["content"])
	}
}

func TestBuildRequestToolResultFollowedByResidualText(t *testing.T) {
	// 同一条 user 消息里 tool_result 和 text 并存 → tool 消息在前,残余文本后置为一条 user
	body := `{
		"model": "glm-4-flash",
		"messages": [{"role": "user", "content": [
			{"type": "tool_result", "tool_use_id": "toolu_A", "content": "42"},
			{"type": "text", "text": "那它的平方呢"}
		]}]
	}`
	_, out, _, err := BuildRequest(ProtoAnthropic, ProtoOpenAI, OpMessages, []byte(body), false)
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	var got struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("len = %d, want 2 (tool + user)", len(got.Messages))
	}
	if got.Messages[0]["role"] != "tool" {
		t.Errorf("messages[0].role = %v, want tool (先于 user 残余文本)", got.Messages[0]["role"])
	}
	if got.Messages[1]["role"] != "user" || got.Messages[1]["content"] != "那它的平方呢" {
		t.Errorf("messages[1] = %v, want user residual text", got.Messages[1])
	}
}

func TestBuildRequestTopKAndThinkingDropped(t *testing.T) {
	// top_k / metadata / thinking 等 v1 不支持的字段要静默丢弃
	body := `{
		"model": "deepseek-chat",
		"top_k": 5,
		"metadata": {"user_id": "x"},
		"messages": [{"role": "user", "content": "hi"}]
	}`
	_, out, _, err := BuildRequest(ProtoAnthropic, ProtoOpenAI, OpMessages, []byte(body), false)
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"top_k", "metadata", "thinking", "anthropic_version"} {
		if _, ok := got[k]; ok {
			t.Errorf("openai request should not contain %q", k)
		}
	}
	if got["model"] != "deepseek-chat" {
		t.Errorf("model = %v", got["model"])
	}
}

// —— tool id 可逆往返 ——

func TestToolIDRoundTrip(t *testing.T) {
	orig := "call_20260903_a1b2c3"
	enc := OpenAItoAnthropicToolID(orig)
	if !strings.HasPrefix(enc, toolIDPrefix) {
		t.Fatalf("encoded = %q, want toolu_ prefix", enc)
	}
	if dec := AnthropicToOpenAIToolID(enc); dec != orig {
		t.Errorf("round-trip = %q, want %q", dec, orig)
	}
}

func TestAnthropicToOpenAIToolIDForeignPassthrough(t *testing.T) {
	// 不是我们编码的 id(前缀不符/解不开)→ 原样透传,让上游正确拒绝,而非静默错配
	for _, id := range []string{"", "toolu_", "call_x", "toolu_!!!not-base64", "some-other-tool-use"} {
		if got := AnthropicToOpenAIToolID(id); got != id {
			t.Errorf("AnthropicToOpenAIToolID(%q) = %q, want passthrough", id, got)
		}
	}
}

// —— 本地 token 估算 ——

func TestEstimateMessagesInput(t *testing.T) {
	body := `{
		"model": "deepseek-chat",
		"system": "你是个助手",
		"max_tokens": 32,
		"tools": [{"name": "f", "description": "工具描述", "input_schema": {"type": "object"}}],
		"messages": [
			{"role": "user", "content": "Hello, world!"},
			{"role": "assistant", "content": [{"type": "text", "text": "好的"}, {"type": "tool_use", "id": "toolu_1", "name": "f", "input": {"x": 1}}]},
			{"role": "user", "content": [{"type": "tool_result", "tool_use_id": "toolu_1", "content": "42"}]}
		]
	}`
	n := EstimateMessagesInput([]byte(body))
	if n <= 0 {
		t.Errorf("Estimate = %d, want > 0", n)
	}
	// 至少要比纯 ASCII 文本大(有 CJK 参与)
	asciiOnly := EstimateMessagesInput([]byte(`{"model":"m","messages":[{"role":"user","content":"aaaaaaaa"}]}`))
	if n <= asciiOnly {
		t.Errorf("CJK-heavy estimate %d should exceed ascii-only %d", n, asciiOnly)
	}
}

func TestSupportedMatrix(t *testing.T) {
	if !Supported(ProtoAnthropic, ProtoOpenAI) {
		t.Error("anthropic→openai should be supported")
	}
	for _, p := range [][2]string{
		{ProtoOpenAI, ProtoAnthropic},
		{ProtoAnthropic, ProtoAnthropic},
		{ProtoOpenAI, ProtoOpenAI},
	} {
		if Supported(p[0], p[1]) {
			t.Errorf("Supported(%s→%s) should be false", p[0], p[1])
		}
	}
}
