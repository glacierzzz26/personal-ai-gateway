// Package pricing 提供"模型 → 每百万 token 单价"的解析,供入库时折算成本。
package pricing

import (
	"strings"

	"personal-ai-gateway/internal/config"
)

type Price struct {
	PromptPerM     float64
	CompletionPerM float64
	CacheReadPerM  float64
	Known          bool // 是否命中过任何规则;未收录的模型成本按 0 计
}

type Resolver struct {
	rules []config.PriceRule
}

func New(rules []config.PriceRule) *Resolver {
	return &Resolver{rules: rules}
}

// Price 返回 model 的第一条命中单价(声明顺序);都不命中则返回零价。
func (r *Resolver) Price(model string) Price {
	for _, rule := range r.rules {
		if matchModel(rule.Model, model) {
			return Price{
				PromptPerM:     rule.PromptPerM,
				CompletionPerM: rule.CompletionPerM,
				CacheReadPerM:  rule.CacheReadPerM,
				Known:          true,
			}
		}
	}
	return Price{}
}

// matchModel 判断模式是否能命中模型名:"*"=全部,"claude-*"=前缀通配,否则精确匹配。
func matchModel(pattern, model string) bool {
	switch {
	case pattern == "*" || pattern == model:
		return true
	case strings.HasSuffix(pattern, "*"):
		return strings.HasPrefix(model, strings.TrimSuffix(pattern, "*"))
	}
	return false
}

// Cost 按归一化后的 token 数折算美元成本(每百万)。prompt 已是"剔除缓存命中"的计费输入。
func (p Price) Cost(prompt, completion, cacheRead int) float64 {
	return (float64(prompt)*p.PromptPerM +
		float64(completion)*p.CompletionPerM +
		float64(cacheRead)*p.CacheReadPerM) / 1e6
}
