package translate

import (
	"encoding/json"
	"math"
)

// EstimateMessagesInput 按 Anthropic /v1/messages 请求体估算输入 token。
// 只用于两处:anthropic message_start 展示用;count_tokens 在无 anthropic 型上游时本地兜底。
// 这是启发式(非计量),见 DESIGN「token 估算」注记。返回至少反映文本体量的正整数。
func EstimateMessagesInput(body []byte) int {
	var in aMessagesRequest
	if json.Unmarshal(body, &in) != nil {
		// 解析不了就当普通文本数一下,别让 count_tokens 报错
		return ceilTokens(string(body))
	}
	return estimateRequest(&in)
}

// estimateRequest 复用已解析的请求结构做估算(与 BuildRequest 同一份 parse)。
func estimateRequest(in *aMessagesRequest) int {
	n := 0.0
	n += tokensOfText(systemText(in.System))
	if in.Model != "" {
		n += tokensOfText(in.Model)
	}
	for _, m := range in.Messages {
		// 数组里的 text / tool_use.input / tool_result.content 都算文本
		if s, ok := asTextString(m.Content); ok {
			n += tokensOfText(s)
			continue
		}
		for _, b := range contentBlocks(m.Content) {
			switch b.Type {
			case "text":
				n += tokensOfText(b.Text)
			case "tool_use":
				n += tokensOfText(b.Name) + tokensOfText(string(b.Input))
			case "tool_result":
				n += tokensOfText(toolResultText(b.Content))
			}
		}
	}
	for _, t := range in.Tools {
		n += tokensOfText(t.Name + " " + t.Description)
		if len(t.InputSchema) > 0 {
			n += tokensOfText(string(t.InputSchema))
		}
	}
	return int(math.Ceil(n))
}

// tokensOfText 启发式:ASCII 4 字符≈1 token,CJK 等每字≈1 token。
func tokensOfText(s string) float64 {
	n := 0.0
	for _, r := range s {
		if isCJKish(r) {
			n += 1
		} else {
			n += 0.25
		}
	}
	return n
}

func ceilTokens(s string) int {
	return int(math.Ceil(tokensOfText(s)))
}

func isCJKish(r rune) bool {
	switch {
	case r >= 0x4E00 && r <= 0x9FFF, // CJK 统一汉字
		r >= 0x3400 && r <= 0x4DBF,
		r >= 0xF900 && r <= 0xFAFF,
		r >= 0x3040 && r <= 0x30FF, // 平假名/片假名
		r >= 0xAC00 && r <= 0xD7AF, // 谚文音节
		r >= 0x1100 && r <= 0x11FF:
		return true
	}
	return false
}
