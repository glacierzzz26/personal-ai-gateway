package config

import (
	"crypto/sha256"
	"crypto/subtle"
)

// constantTimeEqual 比较两个 secret:先各自取 sha256 摘要(固定 32 字节),
// 再做常量时间比较,避免长度差异带来的时序侧信道。
func constantTimeEqual(a, b string) bool {
	ha := sha256.Sum256([]byte(a))
	hb := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ha[:], hb[:]) == 1
}
