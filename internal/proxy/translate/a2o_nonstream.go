package translate

import (
	"encoding/json"
	"fmt"
	"strings"
)

// convertA2ONonStream 把 openai chat.completion 整包响应转成 anthropic message 响应 JSON。
func convertA2ONonStream(raw []byte) (outBody []byte, tok Usage, err error) {
	var o oChatCompletion
	if err := json.Unmarshal(raw, &o); err != nil {
		return nil, Usage{}, fmt.Errorf("translate a2o: invalid openai response: %w", err)
	}

	content := []anthropicContentBlock{}
	var finish string
	if len(o.Choices) > 0 {
		c := o.Choices[0]
		finish = c.FinishReason
		if c.Message.Content != "" {
			content = append(content, anthropicContentBlock{Type: "text", Text: c.Message.Content})
		}
		for _, tc := range c.Message.ToolCalls {
			content = append(content, anthropicContentBlock{
				Type:  "tool_use",
				ID:    OpenAItoAnthropicToolID(tc.ID),
				Name:  tc.Function.Name,
				Input: toolInput(tc.Function.Arguments),
			})
		}
	}

	u := o.Usage
	in := u.PromptTokens - u.Details.CachedTokens
	if in < 0 {
		in = 0
	}
	out := anthropicMessage{
		ID:           "msg_" + randHex(8),
		Type:         "message",
		Role:         "assistant",
		Model:        o.Model,
		Content:      content,
		StopReason:   mapFinishReason(finish),
		StopSequence: nil,
		Usage: anthropicUsage{
			InputTokens:   in,
			OutputTokens:  u.CompletionTokens,
			CacheCreation: 0,
			CacheRead:     u.Details.CachedTokens,
		},
	}
	outBody, err = json.Marshal(out)
	if err != nil {
		return nil, Usage{}, fmt.Errorf("translate a2o: marshal anthropic response: %w", err)
	}
	return outBody, Usage{Prompt: in, Completion: u.CompletionTokens, CacheRead: u.Details.CachedTokens}, nil
}

// —— anthropic message 响应侧结构 ——

type anthropicMessage struct {
	ID           string                  `json:"id"`
	Type         string                  `json:"type"`
	Role         string                  `json:"role"`
	Model        string                  `json:"model"`
	Content      []anthropicContentBlock `json:"content"`
	StopReason   string                  `json:"stop_reason"`
	StopSequence *string                 `json:"stop_sequence"`
	Usage        anthropicUsage          `json:"usage"`
}

type anthropicContentBlock struct {
	Type  string `json:"type"`
	Text  string `json:"text,omitempty"`
	ID    string `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	Input any    `json:"input,omitempty"`
}

type anthropicUsage struct {
	InputTokens   int `json:"input_tokens"`
	OutputTokens  int `json:"output_tokens"`
	CacheCreation int `json:"cache_creation_input_tokens"`
	CacheRead     int `json:"cache_read_input_tokens"`
}

func mapFinishReason(fr string) string {
	switch fr {
	case "stop":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "tool_calls":
		return "tool_use"
	default: // content_filter 或空 → 归为 end_turn(不阻塞对话继续)
		return "end_turn"
	}
}

// toolInput 把 arguments(JSON 字符串)→ anthropic input(对象)。
// 解析失败塞 {"raw": ...} 保住内容,不因一段坏 JSON 丢整轮。
func toolInput(arguments string) any {
	if strings.TrimSpace(arguments) == "" {
		return map[string]any{}
	}
	var v any
	if json.Unmarshal([]byte(arguments), &v) != nil {
		return map[string]any{"raw": arguments}
	}
	if v == nil {
		return map[string]any{}
	}
	return v
}
