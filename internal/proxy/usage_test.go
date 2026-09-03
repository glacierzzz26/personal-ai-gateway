package proxy

import "testing"

// sseAnthropicUsage 对 usage 载体事件的位置不敏感:
// 官方把 input 放 message_start、output 放 message_delta;opencode 校准发现它也可能全放 message_delta。
func TestSSEAnthropicUsagePlacement(t *testing.T) {
	cases := []struct {
		name  string
		lines []string // SSE data 载荷(不含 "data:" 前缀)
		want  usage
	}{
		{
			name:  "官方:start 带 input,delta 带 output",
			lines: []string{`{"type":"message_start","usage":{"input_tokens":85,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}`, `{"type":"message_delta","usage":{"output_tokens":12}}`},
			want:  usage{prompt: 85, completion: 12},
		},
		{
			name:  "opencode:全放 message_delta",
			lines: []string{`{"type":"message_start"}`, `{"type":"message_delta","usage":{"input_tokens":85,"output_tokens":8,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}`},
			want:  usage{prompt: 85, completion: 8},
		},
		{
			name:  "带缓存:input 计费,缓存命中单独记",
			lines: []string{`{"type":"message_start","usage":{"input_tokens":20,"cache_creation_input_tokens":0,"cache_read_input_tokens":500}}`, `{"type":"message_delta","usage":{"output_tokens":30}}`},
			want:  usage{prompt: 20, completion: 30, cacheRead: 500},
		},
		{
			name:  "cache_creation 计入计费 input",
			lines: []string{`{"type":"message_start","usage":{"input_tokens":85,"cache_creation_input_tokens":40,"cache_read_input_tokens":0}}`},
			want:  usage{prompt: 125},
		},
		{
			name:  "后续 0-input chunk 不得覆盖已收敛的 input",
			lines: []string{`{"type":"message_start","usage":{"input_tokens":85,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}`, `{"type":"message_delta","usage":{"output_tokens":0,"input_tokens":0}}`},
			want:  usage{prompt: 85},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var u usage
			for _, l := range c.lines {
				sseAnthropicUsage(l, &u)
			}
			if u != c.want {
				t.Errorf("got %+v want %+v", u, c.want)
			}
		})
	}
}

func TestSSEOpenAIUsageCumulative(t *testing.T) {
	var u usage
	// 兼容服务每块带"累计 usage",取 completion 最大的一版
	for _, payload := range []string{
		`{"choices":[{"delta":{"content":"p"}}],"usage":{"prompt_tokens":89,"completion_tokens":1}}`,
		`{"choices":[{"delta":{"content":"ong"}}],"usage":{"prompt_tokens":89,"completion_tokens":4}}`,
		`{"choices":[],"usage":{"prompt_tokens":89,"completion_tokens":31,"prompt_tokens_details":{}}}`,
	} {
		sniffSSELine(ProtoOpenAI, "data: "+payload, &u)
	}
	if u.completion != 31 || u.prompt != 89 {
		t.Errorf("got %+v want completion=31 prompt=89", u)
	}
}

func TestParseOpenAISubtractsCached(t *testing.T) {
	raw := []byte(`{"usage":{"prompt_tokens":100,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":80}}}`)
	u := parseOpenAIUsage(raw)
	if u.prompt != 20 || u.cacheRead != 80 {
		t.Errorf("got %+v want prompt=20 cacheRead=80", u)
	}
}
