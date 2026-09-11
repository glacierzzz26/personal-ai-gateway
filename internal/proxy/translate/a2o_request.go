package translate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// buildA2ORequest 把 anthropic /v1/messages 请求体改写为 openai /chat/completions 请求体。
// 返回 outOp="chat"(供 outboundPath),改写后的 JSON,以及本地估算的输入 token。
// look 命中时给 assistant 消息补回 reasoning_content(见 ReasoningLookup)。
func buildA2ORequest(body []byte, stream bool, look ReasoningLookup) (outOp string, outBody []byte, estIn int, err error) {
	var in aMessagesRequest
	if err := json.Unmarshal(body, &in); err != nil {
		return "", nil, 0, fmt.Errorf("translate a2o: invalid anthropic request: %w", err)
	}
	if in.Model == "" {
		return "", nil, 0, fmt.Errorf("translate a2o: request must include a model")
	}

	out := map[string]any{"model": in.Model}

	// 顶层 system(string|text 块)→ 开头一条 system 消息
	sys := systemText(in.System)
	var msgs []map[string]any
	for _, m := range in.Messages {
		msgs = append(msgs, rewriteMessage(m, look)...)
	}
	if sys != "" {
		msgs = append([]map[string]any{{"role": "system", "content": sys}}, msgs...)
	}
	out["messages"] = msgs

	if in.MaxTokens > 0 {
		out["max_tokens"] = in.MaxTokens // deepseek/glm 均要求显式 max_tokens
	}
	if in.Temperature != nil {
		out["temperature"] = *in.Temperature
	}
	if in.TopP != nil {
		out["top_p"] = *in.TopP
	}
	if len(in.StopSequences) > 0 {
		out["stop"] = in.StopSequences
	}
	if ts := toolsToOpenAI(in.Tools); len(ts) > 0 {
		out["tools"] = ts
	}
	if tc := toolChoiceToOpenAI(in.ToolChoice); tc != nil {
		out["tool_choice"] = tc
	}
	out["stream"] = stream
	if stream {
		// 让末块带 usage(DeepSeek/GLM 均 OpenAI chat 兼容;个别不认则 usage 缺失,见 DESIGN 风险)
		out["stream_options"] = map[string]any{"include_usage": true}
	}

	outBody, err = json.Marshal(out)
	if err != nil {
		return "", nil, 0, fmt.Errorf("translate a2o: marshal openai request: %w", err)
	}
	return "chat", outBody, estimateRequest(&in), nil
}

// rewriteMessage 把一条 anthropic message 改成 0..n 条 openai message:
//
//	user(纯文本)→ user;user(带 tool_result)→ 各 tool 消息(+残余文本置后为 user);
//	assistant(text+tool_use)→ 单条 assistant(content+tool_calls)。
func rewriteMessage(m aMessage, look ReasoningLookup) []map[string]any {
	switch m.Role {
	case "user":
		return userToOpenAI(m.Content)
	case "assistant":
		if o := assistantToOpenAI(m.Content, look); o != nil {
			return []map[string]any{o}
		}
	case "system": // 容忍罕见的 messages 内 system:并入 system 语义(这里只兜底,正常走顶层)
		if s := contentText(m.Content); s != "" {
			return []map[string]any{{"role": "system", "content": s}}
		}
	}
	return nil
}

func userToOpenAI(raw json.RawMessage) []map[string]any {
	if s, ok := asTextString(raw); ok {
		if strings.TrimSpace(s) == "" {
			return nil
		}
		return []map[string]any{{"role": "user", "content": s}}
	}
	blocks := contentBlocks(raw)
	if blocks == nil {
		return nil
	}
	var out []map[string]any
	var text strings.Builder
	for _, b := range blocks {
		switch b.Type {
		case "tool_result":
			// openai 硬约束:tool 消息紧跟它对应的 assistant tool_calls 消息。
			// Claude Code 的工具轮次通常是纯 tool_result 的 user 消息 → 直接对应成立。
			out = append(out, map[string]any{
				"role":         "tool",
				"tool_call_id": AnthropicToOpenAIToolID(b.ToolUseID),
				"content":      toolResultText(b.Content),
			})
		case "text":
			text.WriteString(b.Text)
		default:
			// image 等块:v1 文本模型为主,丢弃
		}
	}
	if text.Len() > 0 {
		out = append(out, map[string]any{"role": "user", "content": text.String()})
	}
	return out
}

func assistantToOpenAI(raw json.RawMessage, look ReasoningLookup) map[string]any {
	if s, ok := asTextString(raw); ok {
		o := map[string]any{"role": "assistant", "content": s}
		backfillReasoning(o, nil, s, look)
		return o
	}
	blocks := contentBlocks(raw)
	if blocks == nil {
		return nil
	}
	var text strings.Builder
	var calls []map[string]any
	var aIDs []string // anthropic 侧 tool_use id(缓存键用的就是这一形态)
	for _, b := range blocks {
		switch b.Type {
		case "text":
			text.WriteString(b.Text)
		case "tool_use":
			aIDs = append(aIDs, b.ID)
			calls = append(calls, map[string]any{
				"id":   AnthropicToOpenAIToolID(b.ID),
				"type": "function",
				"function": map[string]any{
					"name":      b.Name,
					"arguments": argumentsString(b.Input),
				},
			})
		}
	}
	if text.Len() == 0 && len(calls) == 0 {
		return nil
	}
	o := map[string]any{"role": "assistant"}
	if text.Len() > 0 {
		o["content"] = text.String()
	}
	if len(calls) > 0 {
		o["tool_calls"] = calls
	}
	backfillReasoning(o, aIDs, text.String(), look)
	return o
}

// backfillReasoning 命中上一轮的 reasoning 缓存时补回 reasoning_content。
//
// 只在命中时补:命中本身就证明「这个上游确实在用该字段」,因此不必按 provider 白名单,
// 也不会把该字段塞给不认识它的上游(OpenAI 官方 / Azure 等)。
func backfillReasoning(o map[string]any, toolUseIDs []string, text string, look ReasoningLookup) {
	if look == nil {
		return
	}
	if r := look.Lookup(toolUseIDs, text); r != "" {
		o["reasoning_content"] = r
	}
}

// toolResultText 把 tool_result.content(string|text 块数组)压成字符串;图片丢弃。
func toolResultText(raw json.RawMessage) string {
	if s, ok := asTextString(raw); ok {
		return s
	}
	var sb strings.Builder
	for _, b := range contentBlocks(raw) {
		if b.Type == "text" {
			sb.WriteString(b.Text)
		}
	}
	return sb.String()
}

// argumentsString:anthropic tool_use.input 是对象,openai arguments 必须是「JSON 字符串」。
func argumentsString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil || buf.Len() == 0 {
		return string(raw)
	}
	return buf.String()
}

// toolsToOpenAI:anthropic tools[] → openai {type:function,function:{...}}[]。
func toolsToOpenAI(in []aTool) []map[string]any {
	var out []map[string]any
	for _, t := range in {
		fn := map[string]any{"name": t.Name}
		if t.Description != "" {
			fn["description"] = t.Description
		}
		var params any
		if len(t.InputSchema) > 0 {
			_ = json.Unmarshal(t.InputSchema, &params) // 原样透传(对象)
		}
		if params == nil {
			params = map[string]any{}
		}
		fn["parameters"] = params
		out = append(out, map[string]any{"type": "function", "function": fn})
	}
	return out
}

// toolChoiceToOpenAI:anthropic {auto|any|tool,name} → openai 形状。
func toolChoiceToOpenAI(in aToolChoice) any {
	switch in.Type {
	case "", "auto":
		return nil // 缺省即为 auto,不显式给
	case "any":
		return "required"
	case "tool":
		return map[string]any{"type": "function", "function": map[string]any{"name": in.Name}}
	default:
		return nil
	}
}

// asTextString:content 是纯字符串则返回(true,文本)。
func asTextString(raw json.RawMessage) (string, bool) {
	if len(raw) > 0 && raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s, true
		}
	}
	return "", false
}
