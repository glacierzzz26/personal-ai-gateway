package engine

import (
	"errors"
	"math/rand"
	"path/filepath"
	"testing"

	"personal-ai-gateway/internal/domain"
	"personal-ai-gateway/internal/secret"
	"personal-ai-gateway/internal/store"
)

func testStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	if _, err := secret.BootstrapKey(dir); err != nil {
		t.Fatalf("bootstrap master key: %v", err)
	}
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func addChannel(t *testing.T, st *store.Store, name string, priority, weight int) int64 {
	t.Helper()
	ch, err := st.CreateChannel(domain.ChannelInput{Name: name, BaseURL: "http://x", APIKey: "sk-test", Priority: priority, Weight: weight})
	if err != nil {
		t.Fatalf("create channel %s: %v", name, err)
	}
	return ch.ID
}

func addModel(t *testing.T, st *store.Store, name string) int64 {
	t.Helper()
	m, err := st.CreateModel(domain.ModelInput{Name: name})
	if err != nil {
		t.Fatalf("create model %s: %v", name, err)
	}
	return m.ID
}

func addOffer(t *testing.T, st *store.Store, modelID, chID int64) {
	t.Helper()
	if _, err := st.CreateOffer(modelID, domain.OfferInput{ChannelID: chID}); err != nil {
		t.Fatalf("create offer model=%d ch=%d: %v", modelID, chID, err)
	}
}

func TestEvaluatePriority(t *testing.T) {
	st := testStore(t)
	e := New(st)
	a := addChannel(t, st, "A", 1, 1)
	b := addChannel(t, st, "B", 5, 1)
	m := addModel(t, st, "m1")
	addOffer(t, st, m, a)
	addOffer(t, st, m, b)

	plan, err := e.Evaluate("m1")
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if len(plan.Attempts) != 2 {
		t.Fatalf("want 2 attempts, got %d", len(plan.Attempts))
	}
	if plan.Attempts[0].Offer.ChannelID != a {
		t.Fatalf("priority: want channel A first, got %d", plan.Attempts[0].Offer.ChannelID)
	}
	if plan.MatchedID != 0 {
		t.Fatalf("no rule should match, got %d", plan.MatchedID)
	}
}

// TestEvaluateByDisplayName 重命名后选路用统一名解析供应商,Plan 带对外名与真实名;
// 路由规则按对外名匹配(即便客户端仍用真实名)。
func TestEvaluateByDisplayName(t *testing.T) {
	st := testStore(t)
	e := New(st)
	a := addChannel(t, st, "A", 1, 1)
	// 目录真实名 deepseek-chat,统一名 deepseek-v3
	m, err := st.CreateModel(domain.ModelInput{Name: "deepseek-chat", DisplayName: strPtr("deepseek-v3")})
	if err != nil {
		t.Fatalf("create model: %v", err)
	}
	addOffer(t, st, m.ID, a)

	plan, err := e.Evaluate("deepseek-v3")
	if err != nil {
		t.Fatalf("evaluate by display name: %v", err)
	}
	if plan.PublicName != "deepseek-v3" || plan.OriginName != "deepseek-chat" {
		t.Fatalf("plan names = %q/%q, want deepseek-v3/deepseek-chat", plan.PublicName, plan.OriginName)
	}

	// 规则以统一名匹配:客户端用真实名请求也应命中
	_, err = st.CreateRule(domain.RuleInput{
		Name: "v3", MatchMode: domain.ModePrefix, Pattern: "deepseek-v3",
		Strategy: domain.StrategyPriority, ChannelIDs: []int64{a},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err = e.Evaluate("deepseek-chat")
	if err != nil {
		t.Fatalf("evaluate by origin name: %v", err)
	}
	if plan.MatchedID == 0 || plan.PublicName != "deepseek-v3" {
		t.Fatalf("rule should match on display name: matched=%d public=%q", plan.MatchedID, plan.PublicName)
	}
}

func strPtr(s string) *string { return &s }

func TestEvaluateDisabledModel(t *testing.T) {
	st := testStore(t)
	e := New(st)
	m := addModel(t, st, "off")
	if err := st.SetModelEnabled(m, false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Evaluate("off"); !errors.Is(err, ErrModelUnavailable) {
		t.Fatalf("want ErrModelUnavailable, got %v", err)
	}
}

func TestEvaluateRuleContractionAndFallback(t *testing.T) {
	st := testStore(t)
	e := New(st)
	a := addChannel(t, st, "A", 1, 1)
	b := addChannel(t, st, "B", 2, 1)
	c := addChannel(t, st, "C", 9, 1)
	m := addModel(t, st, "gpt-4o")
	addOffer(t, st, m, a)
	addOffer(t, st, m, b)
	addOffer(t, st, m, c)

	rule, err := st.CreateRule(domain.RuleInput{
		Name:       "gpt",
		MatchMode:  domain.ModePrefix,
		Pattern:    "gpt-",
		Strategy:   domain.StrategyPriority,
		ChannelIDs: []int64{b, a},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := e.Evaluate("gpt-4o")
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if plan.MatchedID != rule.ID {
		t.Fatalf("want matched rule %d, got %d", rule.ID, plan.MatchedID)
	}
	// C 被规则收缩剔除;A/B 按 priority 升序
	if len(plan.Attempts) != 2 {
		t.Fatalf("want 2 attempts (A,B), got %d", len(plan.Attempts))
	}
	for _, at := range plan.Attempts {
		if at.Offer.ChannelID == c {
			t.Fatalf("channel C must be excluded by rule")
		}
	}
	if plan.Attempts[0].Offer.ChannelID != a {
		t.Fatalf("within rule want A first, got %d", plan.Attempts[0].Offer.ChannelID)
	}
}

func TestEvaluateFallbackAppend(t *testing.T) {
	st := testStore(t)
	e := New(st)
	a := addChannel(t, st, "A", 1, 1)
	c := addChannel(t, st, "C", 9, 1)
	m := addModel(t, st, "gpt-4o")
	addOffer(t, st, m, a)
	addOffer(t, st, m, c)

	fb := a
	_, err := st.CreateRule(domain.RuleInput{
		Name:              "gpt",
		MatchMode:         domain.ModePrefix,
		Pattern:           "gpt-",
		Strategy:          domain.StrategyPriority,
		ChannelIDs:        []int64{c},
		FallbackChannelID: &fb,
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := e.Evaluate("gpt-4o")
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	// 规则只在 C;fallback=A 追加末尾
	if len(plan.Attempts) != 2 {
		t.Fatalf("want C then A fallback, got %d attempts", len(plan.Attempts))
	}
	if plan.Attempts[0].Offer.ChannelID != c || plan.Attempts[1].Offer.ChannelID != a {
		t.Fatalf("want order [C, A], got [%d, %d]", plan.Attempts[0].Offer.ChannelID, plan.Attempts[1].Offer.ChannelID)
	}
}

func TestCircuitOpenSkipsChannel(t *testing.T) {
	st := testStore(t)
	e := New(st)
	a := addChannel(t, st, "A", 1, 1)
	m := addModel(t, st, "m1")
	addOffer(t, st, m, a)

	if _, err := e.Evaluate("m1"); err != nil {
		t.Fatalf("healthy evaluate: %v", err)
	}
	e.RecordFailure(a, 2, 600)
	e.RecordFailure(a, 2, 600) // 达到 2 次阈值 → 熔断
	plan, err := e.Evaluate("m1")
	if err == nil {
		t.Fatalf("want error after circuit open, got plan with %d attempts", len(plan.Attempts))
	}
	if !errors.Is(err, ErrModelUnavailable) {
		t.Fatalf("want ErrModelUnavailable, got %v", err)
	}
}

func TestSupportsModelWildcard(t *testing.T) {
	cases := []struct {
		allowed []string
		model   string
		want    bool
	}{
		{[]string{"*"}, "gpt-4", true},
		{[]string{"gpt-*"}, "gpt-4o", true},
		{[]string{"gpt-*"}, "claude-3", false},
		{[]string{"claude-3-5-sonnet-*"}, "claude-3-5-sonnet-20241022", true},
		{[]string{"deepseek-chat", "deepseek-reasoner"}, "deepseek-chat", true},
	}
	for _, c := range cases {
		if got := SupportsModel(c.allowed, c.model); got != c.want {
			t.Errorf("SupportsModel(%v,%q)=%v want %v", c.allowed, c.model, got, c.want)
		}
	}
}

// TestWeightedOrderSplitsByWeight 验证 weight 策略真的按权重分流:首选候选 ~90% 落在高权重渠道。
// 旧实现「权重降序 + 平手抛硬币」恒把高权重排第一,占比会接近 100%。
func TestWeightedOrderSplitsByWeight(t *testing.T) {
	pool := []domain.OfferRead{{ChannelID: 1, Priority: 1}, {ChannelID: 2, Priority: 1}}
	w := map[int64]int{1: 9, 2: 1}
	rnd := rand.New(rand.NewSource(42))
	counts := map[int64]int{}
	const n = 10000
	for i := 0; i < n; i++ {
		out := weightedOrder(pool, w, rnd)
		if len(out) != 2 {
			t.Fatalf("weightedOrder 长度 = %d, want 2", len(out))
		}
		counts[out[0].ChannelID]++
	}
	got := float64(counts[1]) / n
	if got < 0.85 || got > 0.95 {
		t.Errorf("高权重渠道为首选占比 = %.3f, want ~0.90", got)
	}
}

// TestWeightedOrderKeepsAllOffersAndWithinChannelOrder 每个 offer 都出现,同渠道内保持 priority 序。
func TestWeightedOrderKeepsAllOffersAndWithinChannelOrder(t *testing.T) {
	pool := []domain.OfferRead{
		{ID: 11, ChannelID: 1, Priority: 1},
		{ID: 12, ChannelID: 1, Priority: 2},
		{ID: 21, ChannelID: 2, Priority: 1},
	}
	rnd := rand.New(rand.NewSource(1))
	for i := 0; i < 100; i++ {
		out := weightedOrder(pool, map[int64]int{1: 1, 2: 1}, rnd)
		if len(out) != 3 {
			t.Fatalf("len = %d, want 3", len(out))
		}
		var ch1 []int64
		for _, o := range out {
			if o.ChannelID == 1 {
				ch1 = append(ch1, o.ID)
			}
		}
		if len(ch1) != 2 || ch1[0] != 11 || ch1[1] != 12 {
			t.Fatalf("渠道 1 内 offer 序被打乱: %v", ch1)
		}
	}
}
