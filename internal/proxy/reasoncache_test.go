package proxy

import (
	"testing"
	"time"

	"personal-ai-gateway/internal/proxy/translate"
)

// fakeClock 让 TTL 可被确定性拨动。
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func newTestCache(ttl time.Duration, max int) (*reasonCache, *fakeClock) {
	c := newReasonCache(ttl, max)
	clk := &fakeClock{t: time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)}
	c.now = clk.now
	return c, clk
}

func TestReasonCacheToolKeyRoundTrip(t *testing.T) {
	c, _ := newTestCache(time.Minute, 16)
	c.Put(7, translate.Capture{
		Reasoning:  "先看天气",
		ToolUseIDs: []string{"toolu_gw_abc"},
		Text:       "",
	})
	// 下一轮请求带回同一个 tool_use id → 命中
	if got := c.Lookup(7, []string{"toolu_gw_abc"}, ""); got != "先看天气" {
		t.Fatalf("lookup = %q, want 先看天气", got)
	}
}

func TestReasonCacheTextKeyFallback(t *testing.T) {
	c, _ := newTestCache(time.Minute, 16)
	// 纯文本轮:没有 tool_use id,只能靠文本匹配
	c.Put(7, translate.Capture{Reasoning: "推一下", Text: "答案是 4"})
	if got := c.Lookup(7, nil, "答案是 4"); got != "推一下" {
		t.Fatalf("text lookup = %q, want 推一下", got)
	}
	// 文本不同 → 不命中(不给无关轮次塞 reasoning)
	if got := c.Lookup(7, nil, "答案是 5"); got != "" {
		t.Fatalf("mismatched text should miss, got %q", got)
	}
}

func TestReasonCacheMissWithoutReasoning(t *testing.T) {
	c, _ := newTestCache(time.Minute, 16)
	// 上游没用 reasoning_content:不该占容量,也不该被回填
	c.Put(7, translate.Capture{ToolUseIDs: []string{"toolu_gw_x"}, Text: "hi"})
	if got := c.Lookup(7, []string{"toolu_gw_x"}, "hi"); got != "" {
		t.Fatalf("empty reasoning must not be cached, got %q", got)
	}
	c.mu.Lock()
	n := len(c.m)
	c.mu.Unlock()
	if n != 0 {
		t.Fatalf("cache len = %d, want 0", n)
	}
}

func TestReasonCacheExpires(t *testing.T) {
	c, clk := newTestCache(30*time.Minute, 16)
	c.Put(7, translate.Capture{Reasoning: "想", ToolUseIDs: []string{"id1"}})
	clk.t = clk.t.Add(29 * time.Minute)
	if got := c.Lookup(7, []string{"id1"}, ""); got != "想" {
		t.Fatalf("before TTL: got %q", got)
	}
	clk.t = clk.t.Add(2 * time.Minute) // 共 31min > 30min
	if got := c.Lookup(7, []string{"id1"}, ""); got != "" {
		t.Fatalf("after TTL should miss, got %q", got)
	}
}

func TestReasonCacheScopedByToken(t *testing.T) {
	c, _ := newTestCache(time.Minute, 16)
	c.Put(7, translate.Capture{Reasoning: "甲的思考", ToolUseIDs: []string{"shared_id"}})
	// 同 id 但不同令牌 → 绝不串味
	if got := c.Lookup(8, []string{"shared_id"}, ""); got != "" {
		t.Fatalf("token 8 must not read token 7's reasoning, got %q", got)
	}
	if got := c.Lookup(7, []string{"shared_id"}, ""); got != "甲的思考" {
		t.Fatalf("token 7 lookup = %q", got)
	}
}

func TestReasonCacheEvictsOverCapacity(t *testing.T) {
	c, clk := newTestCache(time.Hour, 4)
	for i := 0; i < 6; i++ {
		clk.t = clk.t.Add(time.Second) // 让到期时间有先后
		c.Put(int64(i), translate.Capture{Reasoning: "r", ToolUseIDs: []string{"id" + string(rune('a'+i))}})
	}
	c.mu.Lock()
	n := len(c.m)
	c.mu.Unlock()
	if n > 4 {
		t.Fatalf("cache len = %d, want <= 4", n)
	}
	// 最旧的两条(0、1)应被淘汰
	if got := c.Lookup(0, []string{"ida"}, ""); got != "" {
		t.Fatalf("oldest entry should be evicted, got %q", got)
	}
	if got := c.Lookup(5, []string{"idf"}, ""); got != "r" {
		t.Fatalf("newest entry should survive, got %q", got)
	}
}

// 过期项是惰性回收:Lookup 按到期时间判 miss,回收只在容量吃紧时发生(见 evictLocked)。
// 关键是「过期后读不到」且「map 不会无限增长」——容量上限本身封住了增长。
func TestReasonCacheExpiredNotReturnedAndBounded(t *testing.T) {
	c, clk := newTestCache(time.Minute, 16)
	c.Put(1, translate.Capture{Reasoning: "旧", ToolUseIDs: []string{"old"}})
	clk.t = clk.t.Add(2 * time.Minute)
	if got := c.Lookup(1, []string{"old"}, ""); got != "" {
		t.Fatalf("expired entry returned %q", got)
	}
	c.Put(2, translate.Capture{Reasoning: "新", ToolUseIDs: []string{"new"}})
	if got := c.Lookup(2, []string{"new"}, ""); got != "新" {
		t.Fatalf("fresh entry returned %q", got)
	}
	// 之后的 Put 一旦触到容量上限,过期项会被清掉
	clk.t = clk.t.Add(2 * time.Minute) // 连"新"也过期
	for i := 3; i < 40; i++ {
		c.Put(int64(i), translate.Capture{Reasoning: "r", ToolUseIDs: []string{"id" + string(rune('a'+i%26))}})
	}
	c.mu.Lock()
	n := len(c.m)
	c.mu.Unlock()
	if n > 16 {
		t.Fatalf("cache len = %d, want <= 16", n)
	}
}

func TestReasonCacheNilSafe(t *testing.T) {
	var c *reasonCache
	if got := c.Lookup(1, []string{"x"}, "y"); got != "" {
		t.Fatalf("nil cache lookup = %q", got)
	}
	c.Put(1, translate.Capture{Reasoning: "r"}) // 不应 panic
	if l := c.ForToken(1); l != nil {
		t.Fatalf("nil cache ForToken = %v, want nil", l)
	}
}
