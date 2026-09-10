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
// anthropic 的 tool_use id 本身:toolu_gw_<base64url(provider call id)>。跨轮、跨 failover 都成立。
//
// 前缀必须与上游自己产生的 tool_use id 可区分:anthropic 原生 id 形如 toolu_01.../toolu_vrtx_,
// 也以 toolu_ 开头且 base64url 可解 —— 若只判 toolu_,原生 id 会被误当自编码去 base64 解码,
// 解成非法 UTF-8 乱码再回给客户端。故额外加 gw 段做标记,只有 toolu_gw_ 前缀才走解码。

const toolIDPrefix = "toolu_gw_"

// OpenAItoAnthropicToolID 出站编码:call_xxx → toolu_gw_<b64url>。
func OpenAItoAnthropicToolID(id string) string {
	if id == "" {
		return ""
	}
	return toolIDPrefix + base64.RawURLEncoding.EncodeToString([]byte(id))
}

// AnthropicToOpenAIToolID 入站解码:toolu_gw_<b64url> → call_xxx。
// 非我们编码的(前缀不符;含 anthropic 原生 toolu_...)→ 原样透传,保持可读与合法 UTF-8。
// 自编码但解不开(理论不可达)→ 亦原样透传,不产出乱码。
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
