package proxy

import (
	"encoding/json"
	"strings"
)

// usage 是归一化后的 token 统计口径:
//
//	prompt     只含按**正常输入价**计费的输入 token(已剔除缓存命中**与缓存写**)
//	completion 输出 token
//	cacheRead  缓存命中 token(Anthropic cache_read / OpenAI cached_tokens),按缓存读价计
//	cacheWrite 缓存写入 token(Anthropic cache_creation),按缓存写价计
//
// ⚠️ 缓存写**不再折进 prompt**(补 m0013 前的旧行为)。厂商对它按更高单价收
// (Anthropic 约 1.25× 输入价),折进 prompt 会系统性低估成本,且正是多轮/长上下文
// 场景偏差最大。价格缺失时由计费回落按 input 价(见 billing.tokenCost)。
type usage struct {
	prompt, completion, cacheRead, cacheWrite int
}

// —— 上游各自的 usage 结构 ——

type anthropicUsageJSON struct {
	InputTokens   int `json:"input_tokens"`
	OutputTokens  int `json:"output_tokens"`
	CacheCreation int `json:"cache_creation_input_tokens"`
	CacheRead     int `json:"cache_read_input_tokens"`
}

type openAIUsageJSON struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	Details          struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

// parseAnthropicUsage 解析"完整 Anthropic 响应体"(usage 在顶层),用于非流式。
func parseAnthropicUsage(raw []byte) usage {
	var body struct {
		Usage anthropicUsageJSON `json:"usage"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return usage{}
	}
	return usage{
		prompt:     body.Usage.InputTokens,
		completion: body.Usage.OutputTokens,
		cacheRead:  body.Usage.CacheRead,
		cacheWrite: body.Usage.CacheCreation,
	}
}

// parseOpenAIUsage 解析"完整 OpenAI 响应体"。缓存命中的 token 不计费,从输入里剔出。
// OpenAI 无缓存写概念(其 input 不含 cache_creation),故 cacheWrite 恒 0。
func parseOpenAIUsage(raw []byte) usage {
	var body struct {
		Usage openAIUsageJSON `json:"usage"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return usage{}
	}
	prompt := body.Usage.PromptTokens - body.Usage.Details.CachedTokens
	if prompt < 0 {
		prompt = 0
	}
	return usage{prompt: prompt, completion: body.Usage.CompletionTokens, cacheRead: body.Usage.Details.CachedTokens}
}

// sniffSSELine 逐行嗅探 SSE 里的 usage,不改动转发字节。
// data 行是单行紧凑 JSON;解析失败就跳过,绝不干扰转发。
func sniffSSELine(proto string, line string, u *usage) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "data:") {
		return
	}
	payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
	if payload == "" || payload == "[DONE]" {
		return
	}
	if proto == ProtoOpenAI {
		if v, ok := sseOpenAIUsage(payload); ok {
			// 兼容服务多把 usage 塞在每块且是"累计值",取 completion 最大的一版(通常是末块)。
			if u.completion == 0 || v.completion >= u.completion {
				*u = v
			}
		}
		return
	}
	sseAnthropicUsage(payload, u)
}

// sseOpenAIUsage 只在该 chunk 确实带 usage 字段时返回(很多兼容服务只在末块给)。
func sseOpenAIUsage(payload string) (usage, bool) {
	var probe struct {
		Usage *json.RawMessage `json:"usage"`
	}
	if json.Unmarshal([]byte(payload), &probe) != nil || probe.Usage == nil {
		return usage{}, false
	}
	var body struct {
		Usage openAIUsageJSON `json:"usage"`
	}
	if json.Unmarshal([]byte(payload), &body) != nil {
		return usage{}, false
	}
	prompt := body.Usage.PromptTokens - body.Usage.Details.CachedTokens
	if prompt < 0 {
		prompt = 0
	}
	return usage{prompt: prompt, completion: body.Usage.CompletionTokens, cacheRead: body.Usage.Details.CachedTokens}, true
}

// sseAnthropicUsage 逐 chunk 收敛 usage。事件载体各家不固定:
// 官方 message_start 带 input/cache、message_delta 带 output;opencode 校准发现它可能
// 全放 message_delta、或 message_start 也带(两种都见过)。故对"任一带 usage 的 chunk"都做:
//
//	输入/缓存  → 取 input+cache_creation 更大的一版(防后面 0 值 chunk 覆盖);
//	输出       → 累计语义下取观测最大值(若某上游按增量下发,校准后改为累加)。
//
// 比较仍用 input+cache_creation 的**合计**(单调、可跨 chunk 判大小),但落地时按口径拆开:
// prompt 只放 input,cache_creation 单列 cacheWrite(见 usage 注释)。
func sseAnthropicUsage(payload string, u *usage) {
	var ev struct {
		Type  string              `json:"type"`
		Usage *anthropicUsageJSON `json:"usage"`
	}
	if json.Unmarshal([]byte(payload), &ev) != nil || ev.Usage == nil {
		return
	}
	total := ev.Usage.InputTokens + ev.Usage.CacheCreation
	if u.prompt+u.cacheWrite == 0 || total >= u.prompt+u.cacheWrite {
		u.prompt = ev.Usage.InputTokens
		u.cacheWrite = ev.Usage.CacheCreation
		u.cacheRead = ev.Usage.CacheRead
	}
	if ev.Usage.OutputTokens > u.completion {
		u.completion = ev.Usage.OutputTokens
	}
}
