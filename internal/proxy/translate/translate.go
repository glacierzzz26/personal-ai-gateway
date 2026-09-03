// Package translate 跨协议翻译:把客户端讲的协议换成上游讲的协议,并把响应翻回来。
//
// 本期实现 a2o(Anthropic 入站 → OpenAI 兼容上游),方向函数按 [2]string{inProto,outProto}
// 查表调度,给将来的 o2a(codex/pi 类 OpenAI 入站 → anthropic 上游)预留对称位置。
//
// 关键约束:本包不得 import internal/proxy(否则成环)。因此:
//   - 协议/操作字符串常量在这里复刻 proxy 的同值常量(比较/组装用,不跨包共享)。
//   - 上游错误体的「重编码」只在这里做分类(typ+msg),落 HTTP 响应仍由 proxy.WriteError 负责。
package translate

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// 复刻 proxy 常量(同值,避免 import 环)。改动时两边必须同步。
const (
	ProtoAnthropic = "anthropic"
	ProtoOpenAI    = "openai"

	OpMessages    = "messages"
	OpChat        = "chat"
	OpCountTokens = "count_tokens"
)

// Usage 归一化 token 口径,与 proxy.usage 一致但独立定义(避免 import 环):
// Prompt 已剔除缓存命中(按正常价计费的输入);Completion 输出;CacheRead 缓存命中。
type Usage struct {
	Prompt     int
	Completion int
	CacheRead  int
}

// Supported 是否实现 inProto→outProto 翻译(仅跨协议需要;同协议 fast path 透传)。
func Supported(inProto, outProto string) bool {
	return (inProto == ProtoAnthropic && outProto == ProtoOpenAI) ||
		(inProto == ProtoOpenAI && outProto == ProtoAnthropic)
}

// BuildRequest 把入站体改写成上游协议。
//   - 返回改写的 outOp(anthropic messages → openai chat / openai chat → anthropic messages,
//     供 outbound 取路径);
//   - outBody 是重写的 JSON;estIn 是本地估算的输入 token(仅 a2o 的 message_start 展示用)。
//   - 返回的错误是「入站体无法翻译」→ 上层按客户端 400 处理,不回退。
func BuildRequest(inProto, outProto, op string, body []byte, stream bool) (outOp string, outBody []byte, estIn int, err error) {
	switch {
	case inProto == ProtoAnthropic && outProto == ProtoOpenAI:
		if op != OpMessages {
			return "", nil, 0, fmt.Errorf("translate: op %q has no a2o outbound (only messages)", op)
		}
		return buildA2ORequest(body, stream)
	case inProto == ProtoOpenAI && outProto == ProtoAnthropic:
		if op != OpChat {
			return "", nil, 0, fmt.Errorf("translate: op %q has no o2a outbound (only chat)", op)
		}
		return buildO2ARequest(body, stream)
	}
	return "", nil, 0, fmt.Errorf("translate: unsupported %s→%s", inProto, outProto)
}

// ConvertNonStream 把整段上游非流响应转成入站协议响应。
func ConvertNonStream(inProto, outProto string, raw []byte) (outBody []byte, tok Usage, err error) {
	switch {
	case inProto == ProtoAnthropic && outProto == ProtoOpenAI:
		return convertA2ONonStream(raw)
	case inProto == ProtoOpenAI && outProto == ProtoAnthropic:
		return convertO2ANonStream(raw)
	}
	return nil, Usage{}, fmt.Errorf("translate: unsupported %s→%s", inProto, outProto)
}

// ConvertStream 把上游 SSE 流边读边转成入站协议 SSE 事件流,直接写到 w。
// model/estIn 来自入站解析(同协议 fast path 不经过这里)。
func ConvertStream(inProto, outProto string, src io.Reader, w http.ResponseWriter, model string, estIn int) (Usage, error) {
	switch {
	case inProto == ProtoAnthropic && outProto == ProtoOpenAI:
		return convertA2OStream(src, w, model, estIn)
	case inProto == ProtoOpenAI && outProto == ProtoAnthropic:
		return convertO2AStream(src, w, model)
	}
	return Usage{}, fmt.Errorf("translate: unsupported %s→%s", inProto, outProto)
}

// A2OError 把上游非 2xx 错误体分类成入站(anthropic)协议的错误 type+message。
// 调用方(proxy)负责 retryable(429/5xx)先行 failover,这里只收不可重试的 4xx。
func A2OError(status int, errBody []byte) (typ, msg string) {
	var e struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	providerMsg := ""
	if json.Unmarshal(errBody, &e) == nil {
		providerMsg = e.Error.Message
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

// brief 截断错误体,压成单行可读。
func brief(b []byte) string {
	s := strings.Join(strings.Fields(string(b)), " ")
	if len(s) > 400 {
		s = s[:400] + "…"
	}
	if s == "" {
		return "(empty body)"
	}
	return s
}
