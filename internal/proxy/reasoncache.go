package proxy

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"personal-ai-gateway/internal/proxy/translate"
)

// reasonCache 暂存上游上一轮给出的 reasoning_content,供下一轮 a2o 请求回填。
//
// DeepSeek 等 thinking 模型在带 tool_calls 的 assistant 消息后会要求把 reasoning_content
// 原样带回,而 anthropic 协议里没有这个字段(客户端也无从回传),所以只能网关自己记。
// 详见 internal/proxy/translate/reasoning.go 与 DESIGN §5.2。
//
// 有界 + TTL:对话轮次间隔通常几秒到几分钟,30min 足够覆盖;超出即丢,不会无限增长。
// 缓存丢失(进程重启 / 超 TTL / 网关没见过的历史)时只是退化成「不回填」——
// 该轮仍会被上游以 400 拒绝,与回填上线前一致,不会更糟。
type reasonCache struct {
	mu  sync.Mutex
	m   map[string]reasonEntry
	ttl time.Duration
	max int
	now func() time.Time
}

const (
	reasonCacheTTL = 30 * time.Minute
	reasonCacheMax = 1024
)

type reasonEntry struct {
	reasoning string
	expires   time.Time
}

func newReasonCache(ttl time.Duration, max int) *reasonCache {
	if ttl <= 0 {
		ttl = reasonCacheTTL
	}
	if max <= 0 {
		max = reasonCacheMax
	}
	return &reasonCache{m: map[string]reasonEntry{}, ttl: ttl, max: max, now: time.Now}
}

// 键带 token id 前缀:不同令牌(不同人)的会话互不串味。
func reasonToolKey(tokenID int64, toolUseID string) string {
	return "t|" + strconv.FormatInt(tokenID, 10) + "|" + toolUseID
}

// 纯文本轮(无 tool_use)没有 id 可用,退回文本内容匹配;sha256 取前 16 字节足够。
func reasonTextKey(tokenID int64, text string) string {
	sum := sha256.Sum256([]byte(text))
	return "x|" + strconv.FormatInt(tokenID, 10) + "|" + hex.EncodeToString(sum[:16])
}

// Put 记下本轮结果。reasoning 为空(上游没在用该字段)则整体跳过,不占容量。
func (c *reasonCache) Put(tokenID int64, cap translate.Capture) {
	if c == nil || cap.Reasoning == "" {
		return
	}
	exp := c.now().Add(c.ttl)
	e := reasonEntry{reasoning: cap.Reasoning, expires: exp}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, id := range cap.ToolUseIDs {
		if id != "" {
			c.m[reasonToolKey(tokenID, id)] = e
		}
	}
	if t := strings.TrimSpace(cap.Text); t != "" {
		c.m[reasonTextKey(tokenID, t)] = e
	}
	c.evictLocked()
}

// Lookup 先按 tool_use id(精确),再按文本(兜底);未命中回 ""。
func (c *reasonCache) Lookup(tokenID int64, toolUseIDs []string, text string) string {
	if c == nil {
		return ""
	}
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, id := range toolUseIDs {
		if id == "" {
			continue
		}
		if e, ok := c.m[reasonToolKey(tokenID, id)]; ok && now.Before(e.expires) {
			return e.reasoning
		}
	}
	if t := strings.TrimSpace(text); t != "" {
		if e, ok := c.m[reasonTextKey(tokenID, t)]; ok && now.Before(e.expires) {
			return e.reasoning
		}
	}
	return ""
}

// evictLocked 先清过期,再按到期时间(≈写入序)丢最旧,直到回到容量内。
func (c *reasonCache) evictLocked() {
	if len(c.m) <= c.max {
		return
	}
	now := c.now()
	for k, e := range c.m {
		if !now.Before(e.expires) {
			delete(c.m, k)
		}
	}
	if len(c.m) <= c.max {
		return
	}
	keys := make([]string, 0, len(c.m))
	for k := range c.m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return c.m[keys[i]].expires.Before(c.m[keys[j]].expires) })
	for _, k := range keys[:len(c.m)-c.max] {
		delete(c.m, k)
	}
}

// ForToken 返回绑定到某个令牌的窄视图,交给翻译层按 assistant 消息查回填。
func (c *reasonCache) ForToken(tokenID int64) translate.ReasoningLookup {
	if c == nil {
		return nil
	}
	return tokenReasonLookup{c: c, tokenID: tokenID}
}

type tokenReasonLookup struct {
	c       *reasonCache
	tokenID int64
}

func (l tokenReasonLookup) Lookup(toolUseIDs []string, text string) string {
	return l.c.Lookup(l.tokenID, toolUseIDs, text)
}
