package proxy

// 数据面共享常量。原定义在 gateway.go(已删),因 errors.go/usage.go 需要而单独保留。
// translate 包有自己的同名副本(避免 import 环),改动需两侧同步。

const (
	ProtoAnthropic = "anthropic"
	ProtoOpenAI    = "openai"
)

// op 标识入站要执行的操作,由 server 按 path 判定。
const (
	OpMessages    = "messages"
	OpChat        = "chat"
	OpCountTokens = "count_tokens"
)
