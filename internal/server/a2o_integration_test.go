package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"personal-ai-gateway/internal/config"
	"personal-ai-gateway/internal/proxy/translate"
)

// fakeOpenAIUpstream 记录收到的出站请求(供断言改写),并按脚本返回响应。
type fakeOpenAIUpstream struct {
	srv     *httptest.Server
	gotPath string
	gotAuth string
	gotBody []byte
	respFn  func(w http.ResponseWriter)
}

func newFakeOpenAI(t *testing.T, respFn func(w http.ResponseWriter)) *fakeOpenAIUpstream {
	t.Helper()
	f := &fakeOpenAIUpstream{respFn: respFn}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.gotPath = r.URL.Path
		f.gotAuth = r.Header.Get("Authorization")
		f.gotBody, _ = io.ReadAll(r.Body)
		f.respFn(w)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeOpenAIUpstream) url() string {
	return f.srv.URL
}

// —— anthropic /v1/messages 非流请求 → openai 上游 → anthropic 形状响应 ——

func TestA2ONonStreamToolUse(t *testing.T) {
	const openaiBody = `{
		"id": "chatcmpl-a2o1", "object": "chat.completion", "model": "deepseek-chat",
		"choices": [{"index": 0, "message": {"role": "assistant",
			"content": "查一下",
			"tool_calls": [{"id": "call_xyz", "type": "function",
				"function": {"name": "get_weather", "arguments": "{\"city\": \"北京\"}"}}]},
			"finish_reason": "tool_calls"}],
		"usage": {"prompt_tokens": 30, "completion_tokens": 9,
			"prompt_tokens_details": {"cached_tokens": 5}}
	}`
	fake := newFakeOpenAI(t, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(openaiBody))
	})

	seed := []config.Upstream{{
		Name: "fake", Type: config.TypeOpenAI, BaseURL: fake.url() + "/v1", APIKey: "sk-upstream",
		Priority: 1, Models: []string{"*"},
	}}
	ts, _, st := newUpstreamAPI(t, seed)

	// 一整个工具轮:system 数组、assistant 带 text+tool_use、user 回 tool_result
	code, body := apiKeyReq(t, ts.URL, "POST", "/v1/messages", "", `{
		"model": "deepseek-chat",
		"system": [{"type": "text", "text": "你是助手"}],
		"max_tokens": 256,
		"tools": [{"name": "get_weather", "description": "查天气",
			"input_schema": {"type": "object", "properties": {"city": {"type": "string"}}}}],
		"messages": [
			{"role": "user", "content": "北京天气?"},
			{"role": "assistant", "content": [
				{"type": "text", "text": "我来查"},
				{"type": "tool_use", "id": "`+encToolID("call_inbound")+`", "name": "get_weather", "input": {"city": "北京"}}
			]},
			{"role": "user", "content": [
				{"type": "tool_result", "tool_use_id": "`+encToolID("call_inbound")+`", "content": "晴 25 度"}
			]}
		]
	}`)
	if code != 200 {
		t.Fatalf("a2o non-stream: %d %s", code, body)
	}

	// 出站:改写成 openai chat/completions,鉴权 Bearer,路径拼上 /v1
	if fake.gotPath != "/v1/chat/completions" {
		t.Errorf("outbound path = %q, want /v1/chat/completions", fake.gotPath)
	}
	if fake.gotAuth != "Bearer sk-upstream" {
		t.Errorf("outbound auth = %q", fake.gotAuth)
	}
	var outb struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Stream bool `json:"stream"`
	}
	if err := json.Unmarshal(fake.gotBody, &outb); err != nil {
		t.Fatalf("outbound not json: %v (%s)", err, fake.gotBody)
	}
	if outb.Model != "deepseek-chat" || outb.Stream {
		t.Errorf("outbound model/stream = %q/%v", outb.Model, outb.Stream)
	}
	// tool_result 所在 user 消息无残余文本 → 只出一条 tool 消息
	if len(outb.Messages) != 4 {
		t.Fatalf("outbound messages = %d, want 4 (system + user + assistant + tool): %s",
			len(outb.Messages), fake.gotBody)
	}
	if outb.Messages[0].Role != "system" || outb.Messages[0].Content != "你是助手" {
		t.Errorf("system not flattened: %+v", outb.Messages[0])
	}
	roles := []string{}
	for _, m := range outb.Messages {
		roles = append(roles, m.Role)
	}
	if strings.Join(roles, ",") != "system,user,assistant,tool" {
		t.Errorf("message roles = %v", roles)
	}

	// 入站:anthropic message 形状
	var msg struct {
		Type       string `json:"type"`
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type string          `json:"type"`
			Text string          `json:"text"`
			ID   string          `json:"id"`
			Name string          `json:"name"`
			In   json.RawMessage `json:"input"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
			CacheRead    int `json:"cache_read_input_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &msg); err != nil {
		t.Fatalf("inbound not anthropic json: %v (%s)", err, body)
	}
	if msg.Type != "message" || msg.StopReason != "tool_use" {
		t.Errorf("type/stop_reason = %q/%q", msg.Type, msg.StopReason)
	}
	if len(msg.Content) != 2 {
		t.Fatalf("content len = %d, want text + tool_use", len(msg.Content))
	}
	if msg.Content[1].Type != "tool_use" || msg.Content[1].Name != "get_weather" {
		t.Errorf("tool_use block = %+v", msg.Content[1])
	}
	// 上游给的 tool_use id 编码进 anthropic 块 id
	if msg.Content[1].ID != encToolID("call_xyz") {
		t.Errorf("tool_use id = %q, want encoded", msg.Content[1].ID)
	}
	var in map[string]string
	if err := json.Unmarshal(msg.Content[1].In, &in); err != nil || in["city"] != "北京" {
		t.Errorf("tool input = %s", msg.Content[1].In)
	}
	if msg.Usage.InputTokens != 25 || msg.Usage.OutputTokens != 9 || msg.Usage.CacheRead != 5 {
		t.Errorf("usage = %+v, want input 25 output 9 cache 5", msg.Usage)
	}

	// request_log:usage 对齐(prompt 25 = 30 − cached 5)
	recent, err := st.Recent(3)
	if err != nil || len(recent) == 0 {
		t.Fatalf("recent: %v", err)
	}
	top := recent[0]
	if top.Protocol != "anthropic" || top.Upstream != "fake" || top.Status != 200 {
		t.Errorf("request_log = %+v", top)
	}
	if top.PromptTokens != 25 || top.CompletionTokens != 9 || top.CacheReadTokens != 5 {
		t.Errorf("request_log tokens = p%d/c%d/cr%d, want 25/9/5",
			top.PromptTokens, top.CompletionTokens, top.CacheReadTokens)
	}
}

// —— 上游 400 → anthropic 错误信封(修掉"原样透传 openai 4xx"的问题)——

func TestA2OUpstream4xxReencoded(t *testing.T) {
	fake := newFakeOpenAI(t, func(w http.ResponseWriter) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"messages.0: unexpected role 'tool'","type":"invalid_request_error"}}`))
	})
	seed := []config.Upstream{{
		Name: "fake", Type: config.TypeOpenAI, BaseURL: fake.url() + "/v1", APIKey: "sk", Priority: 1, Models: []string{"*"},
	}}
	ts, _, _ := newUpstreamAPI(t, seed)

	code, body := apiKeyReq(t, ts.URL, "POST", "/v1/messages", "",
		`{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	if code != 400 {
		t.Fatalf("want 400 got %d: %s", code, body)
	}
	var env struct {
		Type  string `json:"type"`
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("not anthropic error envelope: %v (%s)", err, body)
	}
	if env.Type != "error" || env.Error.Type != "invalid_request_error" {
		t.Errorf("envelope = %+v", env)
	}
	if !strings.Contains(env.Error.Message, "unexpected role 'tool'") {
		t.Errorf("message should carry upstream detail, got %q", env.Error.Message)
	}
}

// —— 跨协议 failover:第一个 openai 上游 429 → 换第二个成功,响应仍是 anthropic 形状 ——

func TestA2OFailoverAcrossUpstreams(t *testing.T) {
	good := newFakeOpenAI(t, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"ok","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"兜底成功"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":2}}`))
	})
	bad := newFakeOpenAI(t, func(w http.ResponseWriter) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"overloaded","type":"rate_limit_error"}}`))
	})

	seed := []config.Upstream{
		{Name: "bad", Type: config.TypeOpenAI, BaseURL: bad.url() + "/v1", APIKey: "sk", Priority: 1, Models: []string{"*"}},
		{Name: "good", Type: config.TypeOpenAI, BaseURL: good.url() + "/v1", APIKey: "sk", Priority: 2, Models: []string{"*"}},
	}
	ts, _, _ := newUpstreamAPI(t, seed)

	code, body := apiKeyReq(t, ts.URL, "POST", "/v1/messages", "",
		`{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	if code != 200 {
		t.Fatalf("failover: want 200 got %d: %s", code, body)
	}
	if !strings.Contains(string(body), `"content":[{"type":"text","text":"兜底成功"}]`) {
		t.Errorf("response not translated from second upstream: %s", body)
	}
}

// encToolID 编码 provider call id 成 anthropic toolu_ id(测试里复现同一往返)。
func encToolID(providerID string) string {
	return translate.OpenAItoAnthropicToolID(providerID)
}

// —— anthropic /v1/messages 流式请求 → openai SSE → anthropic SSE 事件序 + 用量 ——

func TestA2OStream(t *testing.T) {
	// 模拟真实上游布局:内容块 → 分段 tool delta → finish → usage 末块 → [DONE]
	upstreamSSE := "data: " + `{"choices":[{"index":0,"delta":{"role":"assistant","content":"查找"}}]}` + "\n\n" +
		"data: " + `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_live","type":"function","function":{"name":"web_search","arguments":""}}]}}]}` + "\n\n" +
		"data: " + `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"q\":"}}]}}]}` + "\n\n" +
		"data: " + `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"天气\""}}]}}]}` + "\n\n" +
		"data: " + `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"}"}}]}}]}` + "\n\n" +
		"data: " + `{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n" +
		"data: " + `{"choices":[],"usage":{"prompt_tokens":40,"completion_tokens":18,"prompt_tokens_details":{"cached_tokens":3}}}` + "\n\n" +
		"data: [DONE]\n\n"

	fake := newFakeOpenAI(t, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(upstreamSSE))
	})
	seed := []config.Upstream{{
		Name: "fake", Type: config.TypeOpenAI, BaseURL: fake.url() + "/v1", APIKey: "sk-upstream",
		Priority: 1, Models: []string{"*"},
	}}
	ts, _, st := newUpstreamAPI(t, seed)

	code, body := apiKeyReq(t, ts.URL, "POST", "/v1/messages", "", `{
		"model": "deepseek-chat",
		"stream": true,
		"max_tokens": 64,
		"tools": [{"name": "web_search", "description": "联网", "input_schema": {"type": "object"}}],
		"messages": [{"role": "user", "content": "天气?"}]
	}`)
	if code != 200 {
		t.Fatalf("stream: %d %s", code, body)
	}
	if !strings.Contains(string(body), "event: message_start") {
		t.Fatalf("response not anthropic SSE:\n%s", body)
	}

	evs := parseA2OSSE(t, string(body))
	names := []string{}
	for _, e := range evs {
		names = append(names, e.name)
	}
	// message_start 恒最先、恰一次 message_stop;内容/工具块全在唯一 message_delta 之前
	if names[0] != "message_start" {
		t.Fatalf("first event %q, want message_start (%v)", names[0], names)
	}
	if names[len(names)-1] != "message_stop" {
		t.Fatalf("last event %q, want message_stop (%v)", names[len(names)-1], names)
	}
	var nDelta int
	for _, n := range names {
		if n == "message_delta" {
			nDelta++
		}
	}
	if nDelta != 1 {
		t.Errorf("message_delta count = %d, want exactly 1", nDelta)
	}
	lastBlockStop := -1
	firstDelta := -1
	for i, e := range evs {
		if e.name == "content_block_stop" {
			lastBlockStop = i
		}
		if e.name == "message_delta" && firstDelta < 0 {
			firstDelta = i
		}
	}
	if lastBlockStop < 0 || lastBlockStop > firstDelta {
		t.Errorf("content_block_stop(#%d) must precede message_delta(#%d)", lastBlockStop, firstDelta)
	}

	// tool_use 块:id 逆解回 provider id;名称正确
	var toolName, toolID string
	for _, e := range evs {
		if e.name == "content_block_start" {
			if cb, ok := e.body["content_block"].(map[string]any); ok {
				if cb["type"] == "tool_use" {
					toolName, _ = cb["name"].(string)
					toolID, _ = cb["id"].(string)
				}
			}
		}
	}
	if toolName != "web_search" || toolID != encToolID("call_live") {
		t.Errorf("tool block name=%q id=%q, want web_search/%q", toolName, toolID, encToolID("call_live"))
	}

	// message_delta:stop_reason tool_use;usage 权威数字 input 37 = 40 − cached 3
	var deltaStop string
	var outTok, inTok, cacheTok float64
	for _, e := range evs {
		if e.name == "message_delta" {
			if d, ok := e.body["delta"].(map[string]any); ok {
				deltaStop, _ = d["stop_reason"].(string)
			}
			if u, ok := e.body["usage"].(map[string]any); ok {
				outTok, _ = u["output_tokens"].(float64)
				inTok, _ = u["input_tokens"].(float64)
				cacheTok, _ = u["cache_read_input_tokens"].(float64)
			}
		}
	}
	if deltaStop != "tool_use" {
		t.Errorf("stop_reason = %q, want tool_use", deltaStop)
	}
	if outTok != 18 || inTok != 37 || cacheTok != 3 {
		t.Errorf("message_delta usage = out %v in %v cache %v, want 18/37/3", outTok, inTok, cacheTok)
	}

	// request_log:usage 对齐 37/18/3,且出站请求确实开了 stream(include_usage)
	if !strings.Contains(string(fake.gotBody), `"stream":true`) ||
		!strings.Contains(string(fake.gotBody), `"include_usage":true`) {
		t.Errorf("outbound body missing stream/include_usage: %s", fake.gotBody)
	}
	recent, err := st.Recent(3)
	if err != nil || len(recent) == 0 {
		t.Fatalf("recent: %v", err)
	}
	top := recent[0]
	if top.Protocol != "anthropic" || top.Status != 200 || top.Stream != true {
		t.Errorf("request_log = %+v", top)
	}
	if top.PromptTokens != 37 || top.CompletionTokens != 18 || top.CacheReadTokens != 3 {
		t.Errorf("request_log tokens = p%d/c%d/cr%d, want 37/18/3",
			top.PromptTokens, top.CompletionTokens, top.CacheReadTokens)
	}
}

// —— server 侧 SSE 解析(与 translate 包单测的 parseSSEEvents 同款,跨包复制最小版)——

type a2oSse struct {
	name string
	body map[string]any
}

func parseA2OSSE(t *testing.T, raw string) []a2oSse {
	t.Helper()
	var out []a2oSse
	var cur a2oSse
	flush := func() {
		if cur.name != "" {
			out = append(out, cur)
		}
		cur = a2oSse{}
	}
	for _, line := range strings.Split(raw, "\n") {
		switch {
		case strings.HasPrefix(line, "event: "):
			cur.name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			_ = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &cur.body)
		case line == "":
			flush()
		}
	}
	flush()
	return out
}
