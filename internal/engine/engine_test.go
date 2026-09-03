package engine

import (
	"errors"
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
