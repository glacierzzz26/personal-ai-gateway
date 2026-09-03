package domain

import "strings"

// MaskKey 展示用密钥掩码:保留头尾,中间打点。
// 渠道密钥创建后明文即弃;令牌密钥仅在创建响应里完整返回一次,列表一律走这里。
func MaskKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 8 {
		return strings.Repeat("*", len(key))
	}
	return key[:4] + "••••" + key[len(key)-4:]
}
