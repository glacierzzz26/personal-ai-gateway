package translate

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// convertA2OStream 把 openai 的 SSE 流逐 chunk 转成 anthropic SSE 事件流,直接写给客户端。
// 上游典型布局:内容/tool 增量块 →(finish chunk)→ usage 末块(choices 空)→ [DONE]。
// 因此 message_delta 先按住不发,等 usage 块到了(或流结束)再收尾,保证:
//   - message_start 恒最先;message_stop 恰一次收尾;
//   - 所有 content_block_stop 先于 message_delta;
//   - message_delta.usage.output_tokens 用真实 completion 而非 0。
func convertA2OStream(src io.Reader, w http.ResponseWriter, model string, estIn int) (Usage, error) {
	st := &a2oStream{
		w:       w,
		model:   model,
		msgID:   "msg_" + randHex(8),
		estIn:   estIn,
		toolBlk: map[int]int{},
		toolID:  map[int]string{},
	}
	if err := st.emitMessageStart(); err != nil {
		return Usage{}, err
	}

	br := bufio.NewReaderSize(src, 32<<10)
	var data strings.Builder
	for {
		line, err := br.ReadString('\n')
		line = strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(line, "data:"):
			data.WriteString(strings.TrimPrefix(line, "data:"))
			data.WriteByte('\n')
		case line == "":
			if data.Len() > 0 {
				if ferr := st.feed(data.String()); ferr != nil {
					return st.usage, ferr
				}
				data.Reset()
			}
		}
		if err == io.EOF {
			if data.Len() > 0 { // 上游没发空行就 EOF:把残余事件喂完
				if ferr := st.feed(data.String()); ferr != nil {
					return st.usage, ferr
				}
			}
			st.finish()
			return st.usage, nil
		}
		if err != nil {
			return st.usage, err
		}
	}
}

type a2oStream struct {
	w        http.ResponseWriter
	model    string
	msgID    string
	estIn    int
	nextBlk  int  // 下一个 anthropic content-block index
	openText bool // 一个 text content_block 正开着

	toolBlk   map[int]int // openai tool_call index → anthropic block index
	toolID    map[int]string
	toolOrder []int // openai tool_call index 首见顺序(finalize 按块序关)

	stopReason string
	gotUsage   bool
	usage      Usage
	finished   bool
}

func (s *a2oStream) emit(event string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(s.w, "event: %s\n", event); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(s.w, "data: %s\n\n", b); err != nil {
		return err
	}
	if f, ok := s.w.(http.Flusher); ok {
		f.Flush()
	}
	return nil
}

func (s *a2oStream) emitMessageStart() error {
	return s.emit("message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            s.msgID,
			"type":          "message",
			"role":          "assistant",
			"model":         s.model,
			"content":       []any{},
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage": map[string]any{
				"input_tokens":                s.estIn, // 本地估算,展示用;权威数字来自 usage 末块
				"cache_creation_input_tokens": 0,
				"cache_read_input_tokens":     0,
			},
		},
	})
}

// feed 处理一个 SSE 事件体(data: 后内容)。
func (s *a2oStream) feed(payload string) error {
	if strings.TrimSpace(payload) == "[DONE]" {
		s.finish()
		return nil
	}
	var c oChunk
	if json.Unmarshal([]byte(payload), &c) != nil {
		return nil // 一行解析失败就跳过,别打断整个流
	}
	if c.Usage != nil {
		s.gotUsage = true
		prompt := c.Usage.PromptTokens - c.Usage.Details.CachedTokens
		if prompt < 0 {
			prompt = 0
		}
		s.usage = Usage{Prompt: prompt, Completion: c.Usage.CompletionTokens, CacheRead: c.Usage.Details.CachedTokens}
	}
	for _, ch := range c.Choices {
		if ch.FinishReason != "" {
			s.stopReason = mapFinishReason(ch.FinishReason) // 按住,收尾时再用
		}
		if ch.Delta.Content != "" {
			if err := s.textDelta(ch.Delta.Content); err != nil {
				return err
			}
		}
		for _, tc := range ch.Delta.ToolCalls {
			if err := s.toolDelta(tc); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *a2oStream) textDelta(text string) error {
	if !s.openText {
		idx := s.nextBlk
		if err := s.emit("content_block_start", map[string]any{
			"type":          "content_block_start",
			"index":         idx,
			"content_block": map[string]any{"type": "text", "text": ""},
		}); err != nil {
			return err
		}
		s.openText = true
	}
	return s.emit("content_block_delta", map[string]any{
		"type":  "content_block_delta",
		"index": s.nextBlk, // openText 期间 text 块 index == nextBlk(未自增)
		"delta": map[string]any{"type": "text_delta", "text": text},
	})
}

func (s *a2oStream) toolDelta(tc oToolDelta) error {
	idx := tc.Index
	blk, known := s.toolBlk[idx]
	if !known {
		if s.openText {
			if err := s.emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": s.nextBlk}); err != nil {
				return err
			}
			s.nextBlk++
			s.openText = false
		}
		// 首见:拿 provider id 编码成 anthropic tool_use id;没有 id 就合成兜底(回程解码会失败,见 DESIGN)
		aid := OpenAItoAnthropicToolID(tc.ID)
		if tc.ID == "" {
			aid = "toolu_" + randHex(8)
		}
		s.toolID[idx] = aid
		s.toolBlk[idx] = s.nextBlk
		s.toolOrder = append(s.toolOrder, idx)
		blk = s.nextBlk
		if err := s.emit("content_block_start", map[string]any{
			"type":          "content_block_start",
			"index":         blk,
			"content_block": map[string]any{"type": "tool_use", "id": aid, "name": tc.Function.Name, "input": map[string]any{}},
		}); err != nil {
			return err
		}
		s.nextBlk++
	}
	if tc.Function.Arguments == "" {
		return nil
	}
	return s.emit("content_block_delta", map[string]any{
		"type":  "content_block_delta",
		"index": blk,
		"delta": map[string]any{"type": "input_json_delta", "partial_json": tc.Function.Arguments},
	})
}

// finish 恰一次:关块 → message_delta → message_stop。
func (s *a2oStream) finish() {
	if s.finished {
		return
	}
	s.finished = true
	if s.openText {
		_ = s.emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": s.nextBlk})
		s.nextBlk++
		s.openText = false
	}
	for _, idx := range s.toolOrder {
		_ = s.emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": s.toolBlk[idx]})
	}
	usage := map[string]any{"output_tokens": 0}
	if s.gotUsage {
		// opencode 同款:把 input/cache 一并放 message_delta(官方只定义 output_tokens,
		// Claude Code 实测可容;校验点见 DESIGN)。这些是权威数字,覆盖 message_start 的估算。
		usage["output_tokens"] = s.usage.Completion
		usage["input_tokens"] = s.usage.Prompt
		usage["cache_read_input_tokens"] = s.usage.CacheRead
	}
	stop := s.stopReason
	if stop == "" {
		stop = "end_turn"
	}
	_ = s.emit("message_delta", map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": stop, "stop_sequence": nil},
		"usage": usage,
	})
	_ = s.emit("message_stop", map[string]any{"type": "message_stop"})
}
