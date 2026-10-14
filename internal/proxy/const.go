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

// opencode 出站会话头相关名。http.Header.Set 会把名规范成 MIME 形态
// (X-Opencode-Session),HTTP 头名大小写不敏感,上游按名取不受影响。
const (
	// headerOpenCodeSession opencode Go 出站必带:上游据此路由 + 命中提示词缓存,缺失即 400。
	headerOpenCodeSession = "x-opencode-session"
	// headerClaudeSession Claude Code 每会话稳定、跨会话互异的 UUID(v4)。
	headerClaudeSession = "X-Claude-Code-Session-Id"
)
