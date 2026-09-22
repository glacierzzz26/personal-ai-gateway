// Package engine 把「模型目录+供给源+路由规则+渠道健康」编译成一次转发决策:
// 产出一份有序候选(Attempts)、重试/超时参数与是否命中规则;并在请求失败/成功后
// 更新渠道熔断与 EWMA 延迟(影响 latency 策略与展示健康态)。
package engine

import (
	"errors"
	"math/rand"
	"regexp"
	"strings"
	"sync"
	"time"

	"personal-ai-gateway/internal/domain"
	"personal-ai-gateway/internal/store"
)

// ErrModelUnavailable 模型不在目录 / 未启用 / 无可用供给源。
var ErrModelUnavailable = errors.New("model unavailable")

type Engine struct {
	st *store.Store

	mu       sync.Mutex
	circuits map[int64]*circuit
	ewma     map[int64]*ewma
	rnd      *rand.Rand
}

func New(st *store.Store) *Engine {
	return &Engine{
		st:       st,
		circuits: map[int64]*circuit{},
		ewma:     map[int64]*ewma{},
		rnd:      rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

type circuit struct {
	failures  int
	openUntil time.Time // 零值 = 未熔断
	lastOK    time.Time // 最近一次成功(真实转发或管理台探测),供「无流量但有复检证据」判健康
}

// CircuitStateT 熔断展示态(Claim/CircuitState 共用)。
type CircuitStateT string

const (
	CircuitClosed  CircuitStateT = "closed"  // 未熔断
	CircuitDown    CircuitStateT = "down"    // 冷却中,剔除
	CircuitProbing CircuitStateT = "probing" // 冷却已过、待复检(半开)
)

type ewma struct {
	val float64 // 毫秒
	n   int
}

// Attempt 一次可执行的转发候选(含报价,engine 不碰密钥)。
type Attempt struct {
	Offer domain.OfferRead
	// 有效超时(毫秒):offer 覆盖 > 规则/全局,调用前定稿
	TimeoutMs int
}

// Plan 一次模型请求的转发方案。
type Plan struct {
	ModelID int64
	// PublicName 客户端请求命中的对外统一名(重命名后即新名);OriginName 渠道侧真实模型名。
	// 出站转发与日志归因分别用这两个名字。
	PublicName string
	OriginName string
	// OfficialVendor/OfficialModelName 模型级官方价绑定(空 = 未绑定)。计费按官方零售价
	// 而非上游成本时,由 proxy 据此取 official_prices 的一行。
	OfficialVendor    domain.Provider
	OfficialModelName string
	// RateOverride 模型级售价倍率;nil = 回落全局 settings.price_multiplier。
	// 倍率按模型定(全站同模型同价),计费与展示都从这里取,避免各自查库漂移。
	RateOverride *float64
	Attempts     []Attempt
	Retry        int   // 追加的重试轮数(候选失败后在剩余候选里重来)
	TimeoutMs    int   // 单候选超时
	MatchedID    int64 // 命中的规则 id(0=无)
	FallbackID   int64
}

// Evaluate 按给定模型名产出有序候选。model 可为渠道侧真实名或网关统一名(display_name)。
func (e *Engine) Evaluate(model string) (*Plan, error) {
	m, err := e.st.GetModelByPublicName(model)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrModelUnavailable
		}
		return nil, err
	}
	if !m.Enabled {
		return nil, ErrModelUnavailable
	}

	channels, err := e.st.ListChannels()
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]domain.ChannelRow, len(channels))
	for _, c := range channels {
		byID[c.ID] = c
	}
	offers, err := e.st.ListEnabledOffersForModel(m.ID)
	if err != nil {
		return nil, err
	}
	cands := make([]domain.OfferRead, 0, len(offers)) // 新底层数组,勿复用 offers(后续会原地收缩)
	// 同一渠道可能挂多个 offer,Claim 是「消费半开机会」的写操作,同一渠道只算一次,
	// 否则一条渠道的多个 offer 会把一次探测机会重复放行。
	claimed := make(map[int64]bool, len(offers))
	for _, o := range offers {
		ch, ok := byID[o.ChannelID]
		if !ok || !ch.Enabled {
			continue
		}
		if !claimed[o.ChannelID] {
			if !e.Claim(o.ChannelID, ch.CooldownSec) {
				continue
			}
			claimed[o.ChannelID] = true
		}
		cands = append(cands, o)
	}
	if len(cands) == 0 {
		return nil, ErrModelUnavailable
	}

	// 默认参数(settings)
	settings, err := e.st.GetSettings()
	if err != nil {
		return nil, err
	}
	retry := settings.MaxRetries
	timeout := settings.RequestTimeoutMs

	// 命中第一个 enabled 匹配规则 → 按策略重排;未命中走 offer 优先级序
	rules, err := e.st.ListRules()
	if err != nil {
		return nil, err
	}
	// 规则按对外统一名匹配:重命名后即便客户端仍用原真实名,路由规则也照统一名生效。
	publicName := m.PublicName()
	var matched *domain.RuleRead
	for i := range rules {
		if !rules[i].Enabled {
			continue
		}
		if ruleMatch(rules[i].MatchMode, rules[i].Pattern, publicName) {
			matched = &rules[i]
			break
		}
	}

	order := cands
	if matched != nil {
		retry = matched.Retry
		timeout = matched.TimeoutMs
		order = e.applyRule(matched, cands, byID)
	}
	// 规则命中把候选收缩到 channel_ids 后,再追加 fallback 渠道上的启用 offer
	order = appendFallback(order, matched, offers, byID)

	attempts := make([]Attempt, 0, len(order))
	for _, o := range order {
		tm := timeout
		if o.TimeoutMs != nil && *o.TimeoutMs > 0 {
			tm = *o.TimeoutMs
		}
		attempts = append(attempts, Attempt{Offer: o, TimeoutMs: tm})
	}

	p := &Plan{ModelID: m.ID, PublicName: publicName, OriginName: m.Name,
		OfficialVendor: m.OfficialVendor, OfficialModelName: m.OfficialModelName,
		RateOverride: m.RateOverride,
		Attempts:     attempts, Retry: retry, TimeoutMs: timeout}
	if matched != nil {
		p.MatchedID = matched.ID
		if matched.FallbackChannelID != nil {
			p.FallbackID = *matched.FallbackChannelID
		}
	}
	return p, nil
}

// applyRule 将候选收缩到规则渠道并按策略排序。
func (e *Engine) applyRule(r *domain.RuleRead, cands []domain.OfferRead, byID map[int64]domain.ChannelRow) []domain.OfferRead {
	want := map[int64]bool{}
	for _, id := range r.ChannelIDs {
		want[id] = true
	}
	pool := make([]domain.OfferRead, 0, len(cands))
	for _, o := range cands {
		if want[o.ChannelID] {
			pool = append(pool, o)
		}
	}
	if len(pool) == 0 {
		return cands
	}
	switch r.Strategy {
	case domain.StrategyWeight:
		// 规则显式给出的权重(含显式 0)优先;未列出的渠道回落到渠道 weight。
		w := map[int64]int{}
		explicit := map[int64]bool{}
		for _, id := range r.ChannelIDs {
			if v, ok := r.Weights[id]; ok {
				w[id] = v
				explicit[id] = true
			}
		}
		for _, o := range pool {
			if !explicit[o.ChannelID] {
				w[o.ChannelID] = byID[o.ChannelID].Weight
			}
		}
		e.mu.Lock()
		pool = weightedOrder(pool, w, e.rnd)
		e.mu.Unlock()
	case domain.StrategyLatency:
		e.mu.Lock()
		sortByLatency(pool, e.ewma)
		e.mu.Unlock()
	default: // priority: 渠道 priority 小者优先,同渠道内 offer.priority 升序
		sortByChannelPriority(pool, byID)
	}
	return pool
}

// appendFallback 在规则收缩后再把 fallback 渠道上该模型的启用 offer 追加到候选末尾。
// offers 是本模型全量启用 offer(收缩前),保证兜底渠道即使不在 rule.channel_ids 也能入选。
func appendFallback(order []domain.OfferRead, r *domain.RuleRead, offers []domain.OfferRead, byID map[int64]domain.ChannelRow) []domain.OfferRead {
	if r == nil || r.FallbackChannelID == nil {
		return order
	}
	fb := *r.FallbackChannelID
	inOrder := false
	for _, o := range order {
		if o.ChannelID == fb {
			inOrder = true
			break
		}
	}
	if inOrder {
		return order // 已在候选内,无需追加
	}
	ch, ok := byID[fb]
	if !ok || !ch.Enabled {
		return order
	}
	var fbOffers []domain.OfferRead
	for _, o := range offers {
		if o.ChannelID == fb {
			fbOffers = append(fbOffers, o)
		}
	}
	sortSlice(fbOffers, func(a, b domain.OfferRead) bool { return a.Priority < b.Priority })
	return append(order, fbOffers...)
}

func sortByChannelPriority(pool []domain.OfferRead, byID map[int64]domain.ChannelRow) {
	sortSlice(pool, func(a, b domain.OfferRead) bool {
		ca, cb := byID[a.ChannelID], byID[b.ChannelID]
		if ca.Priority != cb.Priority {
			return ca.Priority < cb.Priority
		}
		if a.ChannelID != b.ChannelID {
			return a.ChannelID < b.ChannelID
		}
		return a.Priority < b.Priority
	})
}

// weightedOrder 按渠道权重做「不放回加权随机排列」:首个候选按权重概率选出(实现流量分流),
// 其余为失败兜底序;同渠道内保留 offer.priority 序。
// 原实现是「权重降序 + 平手抛硬币」,高权重渠道恒排第一 —— weight 策略退化成 priority,不分流。
func weightedOrder(pool []domain.OfferRead, w map[int64]int, rnd *rand.Rand) []domain.OfferRead {
	type grp struct {
		ch     int64
		offers []domain.OfferRead
		weight int
	}
	var groups []*grp
	idx := map[int64]*grp{}
	for _, o := range pool {
		g, ok := idx[o.ChannelID]
		if !ok {
			g = &grp{ch: o.ChannelID, weight: w[o.ChannelID]}
			groups = append(groups, g)
			idx[o.ChannelID] = g
		}
		g.offers = append(g.offers, o)
	}

	var out []domain.OfferRead
	for len(groups) > 0 {
		total := 0
		for _, g := range groups {
			if g.weight > 0 {
				total += g.weight
			}
		}
		pick := 0
		if total > 0 {
			r := rnd.Intn(total)
			acc := 0
			for i, g := range groups {
				if g.weight <= 0 {
					continue
				}
				acc += g.weight
				if r < acc {
					pick = i
					break
				}
			}
		} else {
			// 全为 0/负权重:退化为均匀随机,仍保证都被排入兜底序。
			pick = rnd.Intn(len(groups))
		}
		out = append(out, groups[pick].offers...)
		groups = append(groups[:pick], groups[pick+1:]...)
	}
	return out
}

// RecordSuccess 成功:复位熔断与延迟计入。
func (e *Engine) RecordSuccess(chID int64, latencyMs int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	c, ok := e.circuits[chID]
	if !ok {
		c = &circuit{}
		e.circuits[chID] = c
	}
	c.failures = 0
	c.openUntil = time.Time{}
	// 记下成功时刻:管理台「测试」走的是同一原语,故探测成功同样算复检证据,
	// 让无流量的渠道也能凭一次真实成功判健康(而非只能永远 unknown)。
	c.lastOK = time.Now()
	ew := e.ewma[chID]
	if ew == nil {
		ew = &ewma{val: float64(latencyMs)}
		e.ewma[chID] = ew
		ew.n = 1
		return
	}
	ew.val = ew.val*0.7 + float64(latencyMs)*0.3
	ew.n++
}

// RecordFailure 失败:累计到 maxFailures 则熔断 cooldown。
func (e *Engine) RecordFailure(chID int64, maxFailures int, cooldownSec int) {
	if maxFailures <= 0 {
		maxFailures = 3
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	c, ok := e.circuits[chID]
	if !ok {
		c = &circuit{}
		e.circuits[chID] = c
	}
	c.failures++
	if c.failures >= maxFailures {
		c.openUntil = time.Now().Add(time.Duration(cooldownSec) * time.Second)
	}
}

// Claim 判定渠道此刻能否被选中,并在放行时**原子地**占住这次机会。
//
// 三个态:
//   - 未熔断/成功已复位 → 直接放行(不占位);
//   - 冷却中(now < openUntil) → 拒绝;
//   - 冷却已过但尚未复检 → 半开:只放行**一次**探测,并把 `openUntil` 往后延
//     (cooldownSec≤0 回落到 30s),使并发请求不会一起涌入。
//     放行后**不阻塞**:若这次探测因客户端 499 中断(不记成功也不记失败),
//     `openUntil` 停在延后的时刻,下一个请求在下一个冷却边界会再试一次,
//     最坏只是把一个「待复检」的渠道多晾一个冷却,漏判一个空闲渠道而已。
//
// 取代了原先的 available()+CircuitOpen():此前「判定是否可选」与「消费半开机会」
// 是两步,读接口的 CircuitOpen 又只报「冷却中」,**冷却已过但从未复检**这一态两端都
// 读成「健康」,于是列表显示健康、实际仍在被剔除。合并成一个原语后不存在这个缝。
func (e *Engine) Claim(chID int64, cooldownSec int) bool {
	if cooldownSec <= 0 {
		cooldownSec = 30
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	c, exists := e.circuits[chID]
	if !exists || c.openUntil.IsZero() {
		return true
	}
	now := time.Now()
	if !now.After(c.openUntil) {
		return false // 冷却中:剔除
	}
	// 冷却已过:开一次半开探测,把窗口推后,防并发涌入。
	c.openUntil = now.Add(time.Duration(cooldownSec) * time.Second)
	return true
}

// CircuitState 渠道熔断展示态(供读接口)。
//
//   - down: 冷却中(now < openUntil),开放时间即 availableFrom;
//   - probing: 曾熔断、冷却已过,但**没有任何近期成功证据**(lastOK 不存在或已超出
//     一次冷却);此时半开探测还没落地,既不能说它坏,也不能说它好 —— 这才是
//     「无流量不等于健康」的落点,列表敢显示它,也敢在首页提示;
//   - closed: 未熔断。
func (e *Engine) CircuitState(chID int64) (state CircuitStateT, availableFrom time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	c, ok := e.circuits[chID]
	if !ok || c.openUntil.IsZero() {
		return CircuitClosed, time.Time{}
	}
	now := time.Now()
	if !now.After(c.openUntil) {
		return CircuitDown, c.openUntil
	}
	if !c.lastOK.IsZero() && now.Sub(c.lastOK) <= c.verifiedFor() {
		return CircuitClosed, time.Time{} // 冷却后确有一次成功(真实转发或探测)→ 健康
	}
	return CircuitProbing, time.Time{}
}

// LastSuccessAt 最近一次成功时刻(零值 = 从未成功过)。
func (e *Engine) LastSuccessAt(chID int64) time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	if c, ok := e.circuits[chID]; ok {
		return c.lastOK
	}
	return time.Time{}
}

// verifiedFor 一次成功能被采信多久。冷却期就是「这条渠道上次坏掉后要等多久」,
// 拿它当证据有效期最自然:成功若比这还久远,就当没验过。
func (c *circuit) verifiedFor() time.Duration {
	if c.failures <= 0 {
		return 15 * time.Minute
	}
	return 30 * time.Second
}

// LatencyMS EWMA 延迟(未知返回 0)。
func (e *Engine) LatencyMS(chID int64) int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	if ew, ok := e.ewma[chID]; ok {
		return int64(ew.val)
	}
	return 0
}

// Reset 清空熔断与延迟(测试/设置变更用)。
func (e *Engine) Reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.circuits = map[int64]*circuit{}
	e.ewma = map[int64]*ewma{}
}

// ---------- 匹配工具(导出,供令牌 allowed_models 复用) ----------

// SupportsModel 判断 allowedModels(可为 "*")是否允许 model(前缀/通配/精确)。
func SupportsModel(allowed []string, model string) bool {
	for _, pat := range allowed {
		if pat == "*" || pat == model || WildcardMatch(pat, model) {
			return true
		}
	}
	return false
}

// WildcardMatch "*" 通配匹配("gpt-*"→"gpt-4o")。
func WildcardMatch(pattern, s string) bool {
	re, err := regexp.Compile("^" + wildcardToRegex(pattern) + "$")
	if err != nil {
		return false
	}
	return re.MatchString(s)
}

func wildcardToRegex(p string) string {
	var b strings.Builder
	for _, c := range p {
		switch c {
		case '*':
			b.WriteString(".*")
		case '.', '^', '$', '(', ')', '[', ']', '{', '}', '|', '+', '?', '\\':
			b.WriteByte('\\')
			b.WriteRune(c)
		default:
			b.WriteRune(c)
		}
	}
	return b.String()
}

func ruleMatch(mode domain.MatchMode, pattern, model string) bool {
	switch mode {
	case domain.ModePrefix:
		return strings.HasPrefix(model, pattern)
	case domain.ModeWildcard:
		return WildcardMatch(pattern, model)
	case domain.ModeRegex:
		re, err := regexp.Compile(pattern)
		if err != nil {
			return false
		}
		return re.MatchString(model)
	}
	return false
}

// sortSlice 泛型稳定排序兜底(避免额外依赖)。
func sortSlice[T any](s []T, less func(a, b T) bool) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && less(s[j], s[j-1]); j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func sortByLatency(pool []domain.OfferRead, ewma map[int64]*ewma) {
	sortSlice(pool, func(a, b domain.OfferRead) bool {
		la, okA := ewma[a.ChannelID]
		lb, okB := ewma[b.ChannelID]
		switch {
		case !okA && !okB:
			return a.Priority < b.Priority
		case !okA:
			return false // 未知延迟放最后
		case !okB:
			return true
		default:
			return la.val < lb.val
		}
	})
}
