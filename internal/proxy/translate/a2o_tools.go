package translate

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"strings"
)

// randHex 返回 n 字节随机数的十六进制(用于合成 msg_/兜底 toolu_ id)。
func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// tool_call_id ↔ tool_use id 的可逆映射。
//
// 为什么必须无状态:客户端第 N 轮收到 tool_use id,第 N+1 轮在 tool_result 里回带它;
// HTTP 无状态,映射必须在两次请求间可逆 —— 方案是把 openai 的 call id 编码进
// anthropic 的 tool_use id 本身:toolu_<base64url(provider call id)>。跨轮、跨 failover 都成立。

const toolIDPrefix = "toolu_"

// OpenAItoAnthropicToolID 出站编码:call_xxx → toolu_<b64url>。
// 解码失败的原始 id(如手工造的)无法被反向还原,见 AnthropicToOpenAIToolID。
func OpenAItoAnthropicToolID(id string) string {
	if id == "" {
		return ""
	}
	return toolIDPrefix + base64.RawURLEncoding.EncodeToString([]byte(id))
}

// AnthropicToOpenAIToolID 入站解码:toolu_<b64url> → call_xxx。
// 不是我们编码的(前缀不符或解码失败)→ 原样透传:请求仍良构,
// 上游会因 tool_call_id 对不上而拒绝 —— 对"别处来的 id"这是正确的失败,而非静默错配。
func AnthropicToOpenAIToolID(id string) string {
	if !strings.HasPrefix(id, toolIDPrefix) {
		return id
	}
	raw := strings.TrimPrefix(id, toolIDPrefix)
	if raw == "" {
		return id
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(b) == 0 {
		return id
	}
	return string(b)
}
