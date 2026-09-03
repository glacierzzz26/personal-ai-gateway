package store

import (
	"testing"

	"personal-ai-gateway/internal/domain"
)

func TestRulesCRUDMoveHit(t *testing.T) {
	st := newTestStore(t)
	fb := int64(9)
	r1, err := st.CreateRule(domain.RuleInput{
		Name: "Claude 主路由", MatchMode: domain.ModePrefix, Pattern: "claude-",
		Strategy: domain.StrategyPriority, ChannelIDs: []int64{1, 2},
		Weights: map[int64]int{1: 70, 2: 30}, FallbackChannelID: &fb, Retry: 2, TimeoutMs: 90000,
	})
	mustNoErr(t, err, "create rule1")
	_, _ = st.CreateRule(domain.RuleInput{Name: "r2", MatchMode: domain.ModeWildcard, Pattern: "*"})
	_, _ = st.CreateRule(domain.RuleInput{Name: "r3", MatchMode: domain.ModeRegex, Pattern: "^gpt-"})

	// 读回完整字段
	got, _ := st.GetRule(r1.ID)
	if got.Name != "Claude 主路由" || got.MatchMode != domain.ModePrefix ||
		got.Strategy != domain.StrategyPriority || len(got.ChannelIDs) != 2 ||
		got.Weights[1] != 70 || got.Retry != 2 || got.TimeoutMs != 90000 ||
		got.FallbackChannelID == nil || *got.FallbackChannelID != fb {
		t.Errorf("rule read mismatch: %+v", got)
	}

	// 顺序 & 移动(0→2)
	list, _ := st.ListRules()
	if len(list) != 3 || list[0].Name != "Claude 主路由" {
		t.Fatalf("list order wrong: %+v", list)
	}
	moved, err := st.MoveRule(0, 2)
	mustNoErr(t, err, "move rule")
	if moved[0].Name != "r2" || moved[2].Name != "Claude 主路由" {
		t.Errorf("move result wrong: %+v", moved)
	}

	// 启停 + 命中计数
	mustNoErr(t, st.SetRuleEnabled(r1.ID, false), "disable rule")
	r, _ := st.GetRule(r1.ID)
	if r.Enabled {
		t.Error("rule should be disabled")
	}
	mustNoErr(t, st.HitRule(r1.ID), "hit rule")
	r, _ = st.GetRule(r1.ID)
	if r.Hit != 1 {
		t.Errorf("hit = %d, want 1", r.Hit)
	}

	// 更新
	up, err := st.UpdateRule(r1.ID, domain.RuleInput{
		Name: "Claude 路由2", MatchMode: domain.ModePrefix, Pattern: "claude-3",
		Strategy: domain.StrategyLatency, ChannelIDs: []int64{3},
	})
	mustNoErr(t, err, "update rule")
	if up.Name != "Claude 路由2" || up.Strategy != domain.StrategyLatency ||
		up.Hit != 1 || up.Sort == 0 {
		t.Errorf("update rule mismatch: %+v", up)
	}

	// 删除后剩余重排(sort 连续无洞)
	mustNoErr(t, st.DeleteRule(up.ID), "delete rule")
	rest, _ := st.ListRules()
	for i, rr := range rest {
		if rr.Sort != i+1 {
			t.Errorf("sort not renumbered: rule %d sort=%d", rr.ID, rr.Sort)
		}
	}
}

func TestDeleteMissingRule(t *testing.T) {
	st := newTestStore(t)
	err := st.DeleteRule(12345)
	mustErrIs(t, err, ErrNotFound, "delete missing rule")
}
