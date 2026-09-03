package translate

import (
	"encoding/json"
	"strings"
)

// —— Anthropic Messages 请求侧(入站)。content/system/tools 是多态字段,
// 用 json.RawMessage 保原始形态,按块逐条改写 ——

// aMessagesRequest 收敛改写所需的全部顶层字段。
type aMessagesRequest struct {
	Model         string          `json:"model"`
	System        json.RawMessage `json:"system"` // string | text/image block 数组
	Messages      []aMessage      `json:"messages"`
	MaxTokens     int             `json:"max_tokens"`
	Temperature   *float64        `json:"temperature"`
	TopP          *float64        `json:"top_p"`
	TopK          *int            `json:"top_k"`
	StopSequences []string        `json:"stop_sequences"`
	Stream        bool            `json:"stream"`
	Tools         []aTool         `json:"tools"`
	ToolChoice    aToolChoice     `json:"tool_choice"`
}

type aMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"` // string | block 数组
}

// aBlock 覆盖 content 数组里的全部块类型(text/tool_use/tool_result/image)。
type aBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"` // tool_use
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"` // tool_use.input(对象)
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"` // tool_result.content(string | text 块数组)
}

type aTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type aToolChoice struct {
	Type string `json:"type"` // auto | any | tool
	Name string `json:"name"` // tool 时指定
}

// contentText 把 content 的多态形态压成一段文本:字符串原样;数组取各 text 块文本;
// 图片/其它块丢弃。返回的 text 未 trim。
func contentText(raw json.RawMessage) string {
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
	if raw[0] != '[' {
		return ""
	}
	var blocks []aBlock
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var sb strings.Builder
	for _, b := range blocks {
		if b.Type == "text" {
			sb.WriteString(b.Text)
		}
	}
	return sb.String()
}

// systemText 取顶层 system 的纯文本(行为与 contentText 一致,单独命名好读)。
func systemText(raw json.RawMessage) string { return contentText(raw) }

// contentBlocks 解析 content 数组;非数组返回 nil。
func contentBlocks(raw json.RawMessage) []aBlock {
	if len(raw) == 0 || raw[0] != '[' {
		return nil
	}
	var blocks []aBlock
	if json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	return blocks
}
