package proxy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"personal-ai-gateway/internal/domain"
)

// 本文件:为 opencode 渠道出站定一个「每会话稳定」的 `x-opencode-session`。
//
// opencode Go 文档要求客户端**每个对话**在 `x-opencode-session` 里带一个稳定会话 ID
// (供上游路由 + 提示词缓存命中),并明确要求中转「转发时保留会话头」。本网关出站只发固定
// 几个头(见 relay.go 的 setOutboundHeaders),会丢掉客户端会话头 → 上游 400
// 「Request is missing x-opencode-session and cannot be routed efficiently」。
// 故在入站解析阶段把会话标识定出来,出站时(仅 opencode 渠道)写进该头。

// resolveSessionID 定出站 opencode 的会话标识,优先级:
//  1. 入站 `x-opencode-session` —— 调用方本就来自 opencode 生态,原样透传;
//  2. 入站 `X-Claude-Code-Session-Id` —— Claude Code 每会话稳定、跨会话互异的 UUID;
//  3. 入站体 `metadata.user_id` 内嵌 JSON 的 `session_id`;
//  4. 兜底:由令牌 sha256 派生 —— 客户端什么都没给也要能路由,避免上游 400。
//
// 前三级任一命中即返回,故 Claude Code 场景不做多余的 body 解析。
func resolveSessionID(r *http.Request, body []byte, t domain.TokenRow) string {
	if v := strings.TrimSpace(r.Header.Get(headerOpenCodeSession)); v != "" {
		return v
	}
	if v := strings.TrimSpace(r.Header.Get(headerClaudeSession)); v != "" {
		return v
	}
	if v := sessionIDFromMetadata(body); v != "" {
		return v
	}
	return fallbackSessionID(t)
}

// sessionIDFromMetadata 从入站体 `metadata.user_id` 抽 `session_id`。
//
// Claude Code 把 `user_id` 发成一个**内嵌 JSON 字符串**(形如
// `{"device_id":"…","account_uuid":"…","session_id":"<uuid>"}`),故需二次 Unmarshal。
// 缺失 / 类型不符 / 任一层解析失败一律返回空串,绝不因它让请求失败。
func sessionIDFromMetadata(body []byte) string {
	var outer struct {
		Metadata struct {
			UserID string `json:"user_id"`
		} `json:"metadata"`
	}
	if len(body) == 0 || json.Unmarshal(body, &outer) != nil || outer.Metadata.UserID == "" {
		return ""
	}
	var inner struct {
		SessionID string `json:"session_id"`
	}
	if json.Unmarshal([]byte(outer.Metadata.UserID), &inner) != nil {
		return ""
	}
	return strings.TrimSpace(inner.SessionID)
}

// fallbackSessionID 客户端未提供任何会话标识时的兜底:由令牌 sha256 派生一个确定性的
// UUID 形状串。令牌不变则值稳定(满足「同一会话稳定」的路由要求),且派生自库内已有的
// sha256 十六进制(**不含明文密钥、不外泄**)。刻意做成 UUID 形状,以防上游对格式有要求。
//
// 局限:这是 per-token 而非 per-conversation —— 同一令牌的多个对话会共用一条路由。
// 这是「宁可粗也别上游 400」的刻意取舍;能拿到真实会话标识的客户端(如 Claude Code)
// 恒命中优先级 2/3,兜底极少触发。
func fallbackSessionID(t domain.TokenRow) string {
	h := sha256.Sum256([]byte("opencode-session:" + t.SHA256))
	s := hex.EncodeToString(h[:16]) // 32 hex 字符
	return s[0:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:32]
}
