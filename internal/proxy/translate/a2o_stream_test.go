package translate

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// —— convertA2OStream:openai SSE → anthropic SSE 事件序 ——

// 单测驱动:把一个 openai chunk 数组拼成上游 SSE 源,喂给状态机,解析写出的 anthropic 事件。
func runStream(t *testing.T, chunks ...string) ([]sseEvent, Usage) {
	t.Helper()
	var src strings.Builder
	for _, c := range chunks {
		src.WriteString("data: " + c + "\n\n")
	}
	src.WriteString("data: [DONE]\n\n")

	rec := httptest.NewRecorder()
	u, err := convertA2OStream(strings.NewReader(src.String()), rec, "deepseek-chat", 7)
	if err != nil {
		t.Fatalf("convertA2OStream: %v", err)
	}
	return parseSSEEvents(t, rec.Body.String()), u
}

type sseEvent struct {
	event string
	body  map[string]any
}

func parseSSEEvents(t *testing.T, raw string) []sseEvent {
	t.Helper()
	var out []sseEvent
	var cur sseEvent
	flush := func() {
		if cur.event != "" {
			out = append(out, cur)
		}
		cur = sseEvent{}
	}
	for _, line := range strings.Split(raw, "\n") {
		switch {
		case strings.HasPrefix(line, "event: "):
			cur.event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			_ = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &cur.body)
		case line == "":
			flush()
		}
	}
	flush()
	return out
}

func eventNames(evs []sseEvent) []string {
	var names []string
	for _, e := range evs {
		names = append(names, e.event)
	}
	return names
}

// contentBlockIndexes 收集每个 content_block_start/delta/stop 的 index(校验块序)。
func contentBlockIndexes(evs []sseEvent) []any {
	var idx []any
	for _, e := range evs {
		if e.body == nil {
			continue
		}
		switch e.event {
		case "content_block_start", "content_block_delta", "content_block_stop":
			idx = append(idx, e.body["index"])
		}
	}
	return idx
}

func TestStreamPureTextWithUsage(t *testing.T) {
	evs, u := runStream(t,
		`{"choices":[{"index":0,"delta":{"role":"assistant","content":"你"}}]}`,
		`{"choices":[{"index":0,"delta":{"content":"好"}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":16,"completion_tokens":12,"prompt_tokens_details":{"cached_tokens":5}}}`,
	)
	names := eventNames(evs)
	// message_start 恒最先,message_stop 恰一次收尾;内容块全部先于 message_delta
	if names[0] != "message_start" {
		t.Fatalf("first event = %q, want message_start (%v)", names[0], names)
	}
	if names[len(names)-1] != "message_stop" {
		t.Fatalf("last event = %q, want message_stop (%v)", names[len(names)-1], names)
	}
	start := evs[0].body["message"].(map[string]any)
	if start["model"] != "deepseek-chat" {
		t.Errorf("message_start.model = %v", start["model"])
	}
	u0 := start["usage"].(map[string]any)
	if fnum(u0["input_tokens"]) != 7 {
		t.Errorf("message_start usage.input_tokens = %v, want estIn 7", u0["input_tokens"])
	}

	// 期望:start, block_start, text_delta, text_delta, block_stop, message_delta, stop
	got := strings.Join(names, ",")
	want := "message_start,content_block_start,content_block_delta,content_block_delta,content_block_stop,message_delta,message_stop"
	if got != want {
		t.Fatalf("event sequence:\n got %s\nwant %s", got, want)
	}
	// 只开了一个 text 块,两次 delta 命中同一 index
	if idx := contentBlockIndexes(evs); strings.Join(anyStrs(idx), ",") != "0,0,0,0" {
		t.Errorf("block indexes = %v", idx)
	}
	// message_delta:stop_reason end_turn;usage 带权威数字(input 11 = 16−5)
	md := evs[len(evs)-2].body
	if md["delta"].(map[string]any)["stop_reason"] != "end_turn" {
		t.Errorf("message_delta stop_reason = %v", md["delta"])
	}
	mu := md["usage"].(map[string]any)
	if fnum(mu["output_tokens"]) != 12 || fnum(mu["input_tokens"]) != 11 || fnum(mu["cache_read_input_tokens"]) != 5 {
		t.Errorf("message_delta usage = %v, want output 12 input 11 cache 5", mu)
	}
	// 返回的权威 Usage
	if u.Prompt != 11 || u.Completion != 12 || u.CacheRead != 5 {
		t.Errorf("Usage = %+v, want prompt 11 completion 12 cache 5", u)
	}
}

func TestStreamToolCallSplitArguments(t *testing.T) {
	// 无 usage 末块 → output_tokens 0;tool 参数分三段 input_json_delta;stop_reason tool_use
	evs, u := runStream(t,
		`{"choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":""}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"北京\"}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	)
	names := eventNames(evs)
	joined := strings.Join(names, ",")
	// tool 首见开 tool_use 块(id/name 在首块),随后两段参数各一条 input_json_delta
	want := "message_start,content_block_start,content_block_delta,content_block_delta,content_block_stop,message_delta,message_stop"
	if joined != want {
		t.Fatalf("event sequence:\n got %s\nwant %s", joined, want)
	}
	bs := evs[1].body["content_block"].(map[string]any)
	if bs["type"] != "tool_use" || bs["name"] != "get_weather" {
		t.Errorf("tool_use block = %v", bs)
	}
	if bs["id"] != OpenAItoAnthropicToolID("call_1") {
		t.Errorf("tool_use id = %v", bs["id"])
	}
	d0 := evs[2].body["delta"].(map[string]any)
	if d0["type"] != "input_json_delta" || d0["partial_json"] != `{"city":` {
		t.Errorf("delta[0] = %v", d0)
	}
	d1 := evs[3].body["delta"].(map[string]any)
	if d1["partial_json"] != `"北京"}` {
		t.Errorf("delta[1] = %v", d1)
	}
	md := evs[5].body
	if md["delta"].(map[string]any)["stop_reason"] != "tool_use" {
		t.Errorf("stop_reason = %v", md["delta"])
	}
	if fnum(md["usage"].(map[string]any)["output_tokens"]) != 0 {
		t.Errorf("usage should be zero when no usage chunk; got %v", md["usage"])
	}
	if u != (Usage{}) {
		t.Errorf("Usage = %+v, want zero (usage missing)", u)
	}
}

func TestStreamTextThenToolClosesTextBlock(t *testing.T) {
	// 文本块先开,第一个 tool delta 必须先把文本块关掉;块 index 按序 0(text)、1(tool)
	evs, _ := runStream(t,
		`{"choices":[{"index":0,"delta":{"role":"assistant","content":"结果:"}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_x","type":"function","function":{"name":"f","arguments":"{}"}}]}}]}`,
	)
	// 期望顺序:start, text block_start, text_delta, text block_stop, tool block_start,
	//          tool delta(args), tool block_stop, message_delta, message_stop
	got := strings.Join(eventNames(evs), ",")
	want := "message_start,content_block_start,content_block_delta,content_block_stop," +
		"content_block_start,content_block_delta,content_block_stop,message_delta,message_stop"
	if got != want {
		t.Fatalf("sequence:\n got %s\nwant %s", got, want)
	}
	if idx := contentBlockIndexes(evs); strings.Join(anyStrs(idx), ",") != "0,0,0,1,1,1" {
		t.Errorf("block indexes = %v", idx)
	}
}

func TestStreamMultipleToolCalls(t *testing.T) {
	// 两个并行 tool_call(index 0/1),各自独立开块;usage 后到也不丢。
	// 首块带 arguments:"{}",故各有一条 input_json_delta。
	evs, u := runStream(t,
		`{"choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_a","function":{"name":"fa","arguments":"{}"}},{"index":1,"id":"call_b","function":{"name":"fb","arguments":"{}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":9,"completion_tokens":6,"prompt_tokens_details":{"cached_tokens":0}}}`,
	)
	got := strings.Join(eventNames(evs), ",")
	want := "message_start,content_block_start,content_block_delta," +
		"content_block_start,content_block_delta,content_block_stop," +
		"content_block_stop,message_delta,message_stop"
	if got != want {
		t.Fatalf("sequence:\n got %s\nwant %s", got, want)
	}
	// 两块 type 均为 tool_use,index 0/1
	if evs[1].body["content_block"].(map[string]any)["name"] != "fa" ||
		evs[3].body["content_block"].(map[string]any)["name"] != "fb" {
		t.Errorf("tool blocks = %v %v", evs[1].body["content_block"], evs[3].body["content_block"])
	}
	// 事件:start(0),delta(0),start(1),delta(1),stop(0),stop(1)
	if idx := contentBlockIndexes(evs); strings.Join(anyStrs(idx), ",") != "0,0,1,1,0,1" {
		t.Errorf("block indexes = %v", idx)
	}
	if u.Prompt != 9 || u.Completion != 6 {
		t.Errorf("Usage = %+v", u)
	}
	md := evs[7].body["usage"].(map[string]any)
	if fnum(md["output_tokens"]) != 6 {
		t.Errorf("message_delta output_tokens = %v", md["output_tokens"])
	}
}

func TestStreamNoDONEStillFinalizes(t *testing.T) {
	// 上游断流(没发 [DONE])也要正常收尾成完整事件序
	var src strings.Builder
	src.WriteString("data: " + `{"choices":[{"index":0,"delta":{"role":"assistant","content":"hi"}}]}` + "\n\n")
	rec := httptest.NewRecorder()
	u, err := convertA2OStream(strings.NewReader(src.String()), rec, "m", 1)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	evs := parseSSEEvents(t, rec.Body.String())
	if evs[len(evs)-1].event != "message_stop" {
		t.Errorf("last event = %v, want message_stop even without [DONE]", eventNames(evs))
	}
	if u != (Usage{}) {
		t.Errorf("Usage = %+v, want zero", u)
	}
}

func anyStrs(v []any) []string {
	var out []string
	for _, x := range v {
		b, _ := json.Marshal(x)
		out = append(out, string(b))
	}
	return out
}

// fnum:JSON 数字解码进 any 是 float64;统一取数用。
func fnum(v any) float64 {
	if f, ok := v.(float64); ok {
		return f
	}
	return 0
}
