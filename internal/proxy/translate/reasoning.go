package translate

// —— a2o 推理回填:上游 thinking 模式要求把上一轮的 reasoning_content 原样带回 ——
//
// DeepSeek 等 thinking 模型在「带 tool_calls 的 assistant 消息」后继续对话时,
// 要求该消息仍带 reasoning_content,否则回 400:
//   The `reasoning_content` in the thinking mode must be passed back to the API.
//
// 但 anthropic 协议里没有这个字段(思考以 thinking 块表达,且客户端未必回传),
// 所以网关自己记住上一轮上游吐出的 reasoning,下一轮重建 assistant 消息时按需补回。
// 见 proxy 侧的 reasoncache(有界内存缓存)。

// ReasoningLookup 按「上一轮 assistant 消息」取回其 reasoning_content;"" = 无缓存。
// 命中即证明该上游确实在用这个字段,故无需按 provider 白名单判断。
type ReasoningLookup interface {
	Lookup(toolUseIDs []string, text string) string
}

// Capture 一次响应转换中「值得回填给下一轮」的东西,由上层缓存。
// o2a 方向恒为零值(openai 入站没有 reasoning_content 概念)。
type Capture struct {
	Reasoning  string   // 上游给出的思维链(reasoning_content);空 = 上游没在用
	ToolUseIDs []string // 本轮 assistant 消息里的工具调用 id(anthropic 侧形态)
	Text       string   // 本轮 assistant 文本,用于纯文本轮次的兜底匹配
}
