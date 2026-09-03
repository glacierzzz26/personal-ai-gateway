package translate

// —— OpenAI Chat Completions 侧(上游)结构 ——

// oChatCompletion 是非流式 chat.completion 响应的收敛字段。
type oChatCompletion struct {
	ID      string      `json:"id"`
	Model   string      `json:"model"`
	Choices []oChoiceNS `json:"choices"`
	Usage   oUsage      `json:"usage"`
}

type oChoiceNS struct {
	Message      oMessage `json:"message"`
	FinishReason string   `json:"finish_reason"`
}

type oMessage struct {
	Content          string      `json:"content"`
	ToolCalls        []oToolCall `json:"tool_calls"`
	ReasoningContent string      `json:"reasoning_content"` // DeepSeek-reasoner 等,v1 丢弃
}

type oToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type oUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	Details          struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

// oChunk 是流式 chat.completion.chunk 的收敛字段。
type oChunk struct {
	Choices []struct {
		Index        int         `json:"index"`
		Delta        oChunkDelta `json:"delta"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
	Usage *oUsage `json:"usage"` // 兼容服务常在末块带;部分服务把它与 usage 一起给
}

type oChunkDelta struct {
	Role             string       `json:"role"`
	Content          string       `json:"content"`
	ReasoningContent string       `json:"reasoning_content"`
	ToolCalls        []oToolDelta `json:"tool_calls"`
}

// oToolDelta 是流式 tool_call 增量(带 index,同一 id 分多块推)。
type oToolDelta struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}
