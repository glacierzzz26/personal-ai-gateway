package translate

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// —— O2A:把 OpenAI 兼容客户端(openai SDK / codex / pi)的 chat.completions 请求
// 翻译成 Anthropic /v1/messages,并把 anthropic 响应翻回 chat.completion 形状。
// 与 a2o 互逆;a2o 已稳定的可逆 id 编码、token 口径在此复用。

type oReq struct {
	Model    string          `json:"model"`
	Messages []oMsg          `json:"messages"`
	MaxToks  *int            `json:"max_tokens"`
	MaxCompl *int            `json:"max_completion_tokens"`
	Temp     *float64        `json:"temperature"`
	TopP     *float64        `json:"top_p"`
	Stop     any             `json:"stop"`
	Tools    []oReqTool      `json:"tools"`
	Choice   json.RawMessage `json:"tool_choice"`
	Stream   bool            `json:"stream"`
}

type oMsg struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	ToolCalls  []oMsgToolCall  `json:"tool_calls"`
	ToolCallID string          `json:"tool_call_id"`
}

type oMsgToolCall struct {
	ID       string `json:"id"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type oReqTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

// buildO2ARequest 把 openai /chat/completions 请求改写为 anthropic /v1/messages。
func buildO2ARequest(body []byte, stream bool) (outOp string, outBody []byte, estIn int, err error) {
	var in oReq
	if err := json.Unmarshal(body, &in); err != nil {
		return "", nil, 0, fmt.Errorf("translate o2a: invalid openai request: %w", err)
	}
	if in.Model == "" {
		return "", nil, 0, fmt.Errorf("translate o2a: request must include a model")
	}
	mt := 4096
	if in.MaxToks != nil && *in.MaxToks > 0 {
		mt = *in.MaxToks
	} else if in.MaxCompl != nil && *in.MaxCompl > 0 {
		mt = *in.MaxCompl
	}

	var sys []string
	var msgs []map[string]any
	for _, m := range in.Messages {
		switch m.Role {
		case "system":
			if t := oText(m.Content); t != "" {
				sys = append(sys, t)
			}
		case "user":
			if t := oText(m.Content); strings.TrimSpace(t) != "" {
				msgs = append(msgs, map[string]any{"role": "user", "content": t})
			}
		case "assistant":
			if a := o2aAssistant(m); a != nil {
				msgs = append(msgs, a)
			}
		case "tool":
			// 优先并入相邻的 user(同轮多次 tool 调用连续出现);否则新开一条 user
			if n := len(msgs); n > 0 {
				if last, ok := (any(msgs[n-1])).(map[string]any); ok && last["role"] == "user" {
					if blocks, ok := last["content"].([]any); ok {
						last["content"] = append(blocks, toolResultBlock(m))
						continue
					}
				}
			}
			msgs = append(msgs, map[string]any{"role": "user", "content": []any{toolResultBlock(m)}})
		}
	}

	out := map[string]any{"model": in.Model, "max_tokens": mt, "stream": stream}
	if len(sys) > 0 {
		out["system"] = strings.Join(sys, "\n\n")
	}
	out["messages"] = msgs
	if in.Temp != nil {
		out["temperature"] = *in.Temp
	}
	if in.TopP != nil {
		out["top_p"] = *in.TopP
	}
	if s, ok := stopToSeq(in.Stop); ok {
		out["stop_sequences"] = s
	}
	if ts := o2aTools(in.Tools); len(ts) > 0 {
		out["tools"] = ts
		if tc := o2aToolChoice(in.Choice); tc != nil {
			out["tool_choice"] = tc
		}
	}

	outBody, err = json.Marshal(out)
	if err != nil {
		return "", nil, 0, fmt.Errorf("translate o2a: marshal anthropic request: %w", err)
	}
	return "messages", outBody, o2aEstimate(&in), nil
}

func toolResultBlock(m oMsg) map[string]any {
	return map[string]any{
		"type":        "tool_result",
		"tool_use_id": OpenAItoAnthropicToolID(m.ToolCallID),
		"content":     oText(m.Content),
	}
}

// o2aAssistant 把 assistant 消息(文本 + tool_calls)改写成 anthropic content 块数组。
func o2aAssistant(m oMsg) map[string]any {
	text := oText(m.Content)
	if len(m.ToolCalls) == 0 {
		if text == "" {
			return nil
		}
		return map[string]any{"role": "assistant", "content": text}
	}
	var blocks []any
	if text != "" {
		blocks = append(blocks, map[string]any{"type": "text", "text": text})
	}
	for _, tc := range m.ToolCalls {
		blocks = append(blocks, map[string]any{
			"type":  "tool_use",
			"id":    OpenAItoAnthropicToolID(tc.ID),
			"name":  tc.Function.Name,
			"input": toolInput(tc.Function.Arguments),
		})
	}
	return map[string]any{"role": "assistant", "content": blocks}
}

// oText 把 openai content(string | parts 数组)压成文本;image 等块丢弃。
func oText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
		return ""
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var sb strings.Builder
	for _, p := range parts {
		if p.Type == "text" || p.Type == "" {
			sb.WriteString(p.Text)
		}
	}
	return sb.String()
}

// stopToSeq 把 openai stop(string|[]string) 转成 anthropic stop_sequences。
func stopToSeq(stop any) ([]string, bool) {
	switch v := stop.(type) {
	case string:
		if v == "" {
			return nil, false
		}
		return []string{v}, true
	case []any:
		var out []string
		for _, e := range v {
			if s, ok := e.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out, len(out) > 0
	}
	return nil, false
}

// o2aTools:openai tools → anthropic tools(input_schema=parameters)。
func o2aTools(in []oReqTool) []map[string]any {
	var out []map[string]any
	for _, t := range in {
		fn := map[string]any{"name": t.Function.Name}
		if t.Function.Description != "" {
			fn["description"] = t.Function.Description
		}
		params := map[string]any{"type": "object", "properties": map[string]any{}}
		if len(t.Function.Parameters) > 0 {
			var p map[string]any
			if json.Unmarshal(t.Function.Parameters, &p) == nil && p != nil {
				params = p
			}
		}
		fn["input_schema"] = params
		out = append(out, map[string]any{"type": "function", "function": fn})
	}
	return out
}

// o2aToolChoice:openai tool_choice → anthropic(auto 省略/required→any/tool 指名)。
func o2aToolChoice(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		switch s {
		case "auto", "none":
			return nil // anthropic 无 none;不发 tool_choice 即 auto
		case "required":
			return "any"
		}
		return nil
	}
	var obj struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if json.Unmarshal(raw, &obj) == nil && obj.Function.Name != "" {
		return map[string]any{"type": "tool", "name": obj.Function.Name}
	}
	return nil
}

// o2aEstimate 粗略估算 openai 入站文本 token(仅用于 message_start 展示估算,计费走真实 usage)。
func o2aEstimate(in *oReq) int {
	tk := func(s string) int { return int(tokensOfText(s) + 0.999) }
	n := 1
	for _, m := range in.Messages {
		n += tk(oText(m.Content))
		for _, tc := range m.ToolCalls {
			n += tk(tc.Function.Name) + tk(tc.Function.Arguments)
		}
	}
	for _, t := range in.Tools {
		n += tk(t.Function.Name) + tk(t.Function.Description)
	}
	return n
}

// finishReasonO2A anthropic stop_reason → openai finish_reason。
func finishReasonO2A(fr string) string {
	switch fr {
	case "end_turn", "":
		return "stop"
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	default:
		return "stop"
	}
}

// —— 非流式 anthropic message → openai chat.completion ——

func convertO2ANonStream(raw []byte) (outBody []byte, tok Usage, err error) {
	var a anthropicMessage
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, Usage{}, fmt.Errorf("translate o2a: invalid anthropic response: %w", err)
	}
	var text strings.Builder
	var calls []map[string]any
	for _, b := range a.Content {
		switch b.Type {
		case "text":
			text.WriteString(b.Text)
		case "tool_use":
			args := []byte("{}")
			if b.Input != nil {
				args, _ = json.Marshal(b.Input)
			}
			calls = append(calls, map[string]any{
				"id":   AnthropicToOpenAIToolID(b.ID),
				"type": "function",
				"function": map[string]any{
					"name":      b.Name,
					"arguments": string(args),
				},
			})
		}
	}
	msg := map[string]any{"role": "assistant"}
	if text.Len() > 0 {
		msg["content"] = text.String()
	}
	if len(calls) > 0 {
		msg["tool_calls"] = calls
	}
	u := anthropicToOUsage(&a.Usage)
	out := map[string]any{
		"id":      a.ID,
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   a.Model,
		"choices": []any{map[string]any{
			"index": 0, "message": msg, "finish_reason": finishReasonO2A(a.StopReason),
		}},
		"usage": o2aUsageJSON(u),
	}
	outBody, err = json.Marshal(out)
	if err != nil {
		return nil, Usage{}, fmt.Errorf("translate o2a: marshal openai response: %w", err)
	}
	return outBody, Usage{Prompt: u.Prompt, Completion: u.Completion, CacheRead: u.CacheRead}, nil
}

// anthropicToOUsage:anthropic usage(input 不含缓存)→ 归一化 Usage。
// 遵守 Usage 不变式:Prompt 只含按正常输入价计费的部分(InputTokens + CacheCreation),
// 缓存命中单列 CacheRead —— 否则 costUsd 会对缓存命中既按输入价、又按缓存价各收一次。
// openai 客户端要看的 prompt_tokens(含全部输入)在 o2aUsageJSON 里由 Prompt+CacheRead 还原。
func anthropicToOUsage(a *anthropicUsage) Usage {
	return Usage{
		Prompt:     a.InputTokens + a.CacheCreation,
		Completion: a.OutputTokens,
		CacheRead:  a.CacheRead,
	}
}

func o2aUsageJSON(u Usage) map[string]any {
	// openai 口径:prompt_tokens 含全部输入(含缓存命中),cached_tokens 单列明细。
	return map[string]any{
		"prompt_tokens":         u.Prompt + u.CacheRead,
		"completion_tokens":     u.Completion,
		"prompt_tokens_details": map[string]any{"cached_tokens": u.CacheRead},
	}
}

// —— 流式 anthropic SSE → openai SSE chunk 流 ——

func convertO2AStream(src io.Reader, w http.ResponseWriter, model string) (Usage, error) {
	st := &o2aStream{w: w, model: model}
	st.chunk(map[string]any{
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant"}, "finish_reason": nil}},
	})

	br := bufio.NewReaderSize(src, 32<<10)
	var data strings.Builder
	for {
		line, err := br.ReadString('\n')
		line = strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(line, "data:"):
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		case line == "":
			if data.Len() > 0 {
				if ferr := st.feed(data.String()); ferr != nil {
					return st.usage, ferr
				}
				data.Reset()
			}
		}
		if err == io.EOF {
			if data.Len() > 0 {
				if ferr := st.feed(data.String()); ferr != nil {
					return st.usage, ferr
				}
			}
			st.finish()
			return st.usage, nil
		}
		if err != nil {
			return st.usage, err
		}
	}
}

type o2aStream struct {
	w     http.ResponseWriter
	model string
	usage Usage

	toolIdx map[int]int // anthropic block idx → openai tool idx
	toolID  map[int]string
	toolFn  map[int]string
	emitted map[int]bool

	finishSent bool
	done       bool
}

func (s *o2aStream) chunk(payload map[string]any) {
	payload["id"] = "chatcmpl-" + randHex(8)
	payload["object"] = "chat.completion.chunk"
	payload["model"] = s.model
	b, _ := json.Marshal(payload)
	fmt.Fprintf(s.w, "data: %s\n\n", b)
	if f, ok := s.w.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *o2aStream) feed(payload string) error {
	if strings.TrimSpace(payload) == "" {
		return nil
	}
	var ev struct {
		Type    string `json:"type"`
		Message *struct {
			Model string          `json:"model"`
			Usage *anthropicUsage `json:"usage"`
		} `json:"message"`
		Index int `json:"index"`
		Delta *struct {
			Type        string  `json:"type"`
			Text        string  `json:"text"`
			PartialJSON string  `json:"partial_json"`
			StopReason  *string `json:"stop_reason"`
		} `json:"delta"`
		Usage *struct {
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
		ContentBlock *struct {
			Type string `json:"type"`
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"content_block"`
	}
	if json.Unmarshal([]byte(payload), &ev) != nil {
		return nil
	}
	switch ev.Type {
	case "message_start":
		if ev.Message != nil && ev.Message.Usage != nil {
			u := anthropicToOUsage(ev.Message.Usage)
			s.usage.Prompt = u.Prompt
			s.usage.CacheRead = u.CacheRead
		}
	case "content_block_start":
		if ev.ContentBlock != nil && ev.ContentBlock.Type == "tool_use" {
			if s.toolIdx == nil {
				s.toolIdx = map[int]int{}
				s.toolID = map[int]string{}
				s.toolFn = map[int]string{}
				s.emitted = map[int]bool{}
			}
			oi := len(s.toolIdx)
			s.toolIdx[ev.Index] = oi
			s.toolID[ev.Index] = AnthropicToOpenAIToolID(ev.ContentBlock.ID)
			if s.toolID[ev.Index] == "" {
				s.toolID[ev.Index] = "call_" + randHex(8)
			}
			s.toolFn[ev.Index] = ev.ContentBlock.Name
		}
	case "content_block_delta":
		if ev.Delta == nil {
			return nil
		}
		switch ev.Delta.Type {
		case "text_delta":
			s.chunk(map[string]any{
				"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": ev.Delta.Text}, "finish_reason": nil}},
			})
		case "input_json_delta":
			oi, ok := s.toolIdx[ev.Index]
			if !ok || ev.Delta.PartialJSON == "" {
				return nil
			}
			if !s.emitted[oi] {
				s.emitted[oi] = true
				s.chunk(map[string]any{
					"choices": []any{map[string]any{"index": 0, "delta": map[string]any{
						"tool_calls": []any{map[string]any{
							"index": oi, "id": s.toolID[ev.Index],
							"function": map[string]any{"name": s.toolFn[ev.Index], "arguments": ""},
						}},
					}, "finish_reason": nil}},
				})
			}
			s.chunk(map[string]any{
				"choices": []any{map[string]any{"index": 0, "delta": map[string]any{
					"tool_calls": []any{map[string]any{
						"index": oi, "function": map[string]any{"arguments": ev.Delta.PartialJSON},
					}},
				}, "finish_reason": nil}},
			})
		}
	case "message_delta":
		if ev.Usage != nil {
			s.usage.Completion = ev.Usage.OutputTokens
		}
		if ev.Delta != nil && ev.Delta.StopReason != nil {
			s.chunk(map[string]any{
				"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": finishReasonO2A(*ev.Delta.StopReason)}},
			})
		}
	case "message_stop":
		s.finish()
	}
	return nil
}

func (s *o2aStream) finish() {
	if s.done {
		return
	}
	s.done = true
	s.chunk(map[string]any{"choices": []any{}, "usage": o2aUsageJSON(s.usage)})
	fmt.Fprint(s.w, "data: [DONE]\n\n")
	if f, ok := s.w.(http.Flusher); ok {
		f.Flush()
	}
}

// O2AError 把 anthropic 上游错误体分类成 openai {type,message}。
func O2AError(status int, errBody []byte) (typ, msg string) {
	var e struct {
		Type  string `json:"type"`
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	providerMsg := ""
	if json.Unmarshal(errBody, &e) == nil {
		providerMsg = e.Error.Message
		if providerMsg == "" {
			providerMsg = e.Error.Type
		}
	}
	if providerMsg == "" {
		providerMsg = brief(errBody)
	}
	switch {
	case status == http.StatusBadRequest || status == http.StatusUnprocessableEntity:
		typ = "invalid_request_error"
	case status == http.StatusUnauthorized:
		typ = "authentication_error"
	case status == http.StatusForbidden:
		typ = "permission_error"
	case status == http.StatusNotFound:
		typ = "not_found_error"
	case status == http.StatusTooManyRequests:
		typ = "rate_limit_error"
	default:
		typ = "api_error"
	}
	return typ, providerMsg
}
