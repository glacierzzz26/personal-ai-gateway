package router

import (
	"testing"
	"time"

	"personal-ai-gateway/internal/config"
	"personal-ai-gateway/internal/quota"
)

func TestSupports(t *testing.T) {
	cases := []struct {
		models []string
		model  string
		want   bool
	}{
		{nil, "claude-sonnet-4-5", true},
		{[]string{"*"}, "anything", true},
		{[]string{"claude-sonnet-4-5"}, "claude-sonnet-4-5", true},
		{[]string{"claude-sonnet-4-5"}, "claude-opus-4-5", false},
		{[]string{"claude-*"}, "claude-opus-4-5", true},
		{[]string{"claude-*"}, "gpt-4o", false},
	}
	for _, c := range cases {
		u := &config.Upstream{Models: c.models}
		if got := Supports(u, c.model); got != c.want {
			t.Errorf("Supports(%v, %q) = %v, want %v", c.models, c.model, got, c.want)
		}
	}
}

func TestCandidatesOrdering(t *testing.T) {
	r := New([]config.Upstream{
		{Name: "a", Type: config.TypeOpenAI, Priority: 5},
		{Name: "b", Type: config.TypeOpenAI, Priority: 1},
		{Name: "c", Type: config.TypeOpenAI, Priority: 3, Models: []string{"claude-*"}},
	})
	got := r.Candidates("claude-sonnet-4-5")
	want := []string{"b", "c", "a"} // priority 升序(1→3→5),全部命中该模型
	if len(got) != len(want) {
		t.Fatalf("len=%d want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Name != want[i] {
			t.Errorf("candidate[%d]=%q want %q", i, got[i].Name, want[i])
		}
	}
}

func TestCandidateFilterByModels(t *testing.T) {
	r := New([]config.Upstream{
		{Name: "claude-only", Type: config.TypeAnthropic, Models: []string{"claude-*"}},
		{Name: "all", Type: config.TypeOpenAI},
	})
	if got := r.Candidates("gpt-4o"); len(got) != 1 || got[0].Name != "all" {
		t.Fatalf("gpt-4o candidates = %v, want [all]", got)
	}
	if got := r.Candidates("claude-sonnet-4-5"); len(got) != 2 {
		t.Fatalf("claude candidates = %v, want 2", got)
	}
}

func TestCircuitOpenAndRecover(t *testing.T) {
	up := &config.Upstream{Name: "a", Type: config.TypeOpenAI, MaxFailures: 2, CooldownSec: 1}
	r := New([]config.Upstream{*up})

	if got := len(r.Candidates("m")); got != 1 {
		t.Fatalf("expected healthy initially, got %d", got)
	}
	r.RecordFailure(up)
	r.RecordFailure(up) // 达 max_failures → 打开
	if got := len(r.Candidates("m")); got != 0 {
		t.Fatalf("expected circuit open, got %d candidates", got)
	}

	// 冷却期走完自动恢复
	time.Sleep(1100 * time.Millisecond)
	if got := len(r.Candidates("m")); got != 1 {
		t.Fatalf("expected recovered after cooldown, got %d", got)
	}

	r.RecordSuccess(up.Name)
	if got := r.healthy(up.Name); !got {
		t.Fatal("expected healthy after success")
	}
}

// Apply 整体换列表:同名熔断状态保留、删除的名字不再被选路、stale 配额快照被剪。
func TestApplyCarriesCircuit(t *testing.T) {
	a := config.Upstream{Name: "a", Type: config.TypeOpenAI, Priority: 1, MaxFailures: 1, CooldownSec: 60}
	b := config.Upstream{Name: "b", Type: config.TypeOpenAI, Priority: 2, MaxFailures: 1, CooldownSec: 60}
	r := New([]config.Upstream{a, b})

	// 把 a 打开熔断
	r.RecordFailure(&a)
	if len(r.Candidates("m")) != 1 || r.Candidates("m")[0].Name != "b" {
		t.Fatalf("expected only b after a trips")
	}
	r.SetQuota("a", quota.Snapshot{UsedPct: 99, Hard: true, Status: "ok", Window: "monthly"})

	// 无关编辑 b(c 加进来、b 改 priority):a 的熔断与配额状态都应原样保留
	r.Apply([]config.Upstream{
		{Name: "a", Type: config.TypeOpenAI, Priority: 1, MaxFailures: 1, CooldownSec: 60},
		{Name: "b", Type: config.TypeOpenAI, Priority: 9, MaxFailures: 1, CooldownSec: 60},
		{Name: "c", Type: config.TypeOpenAI, Priority: 2},
	})
	if got := r.Candidates("m"); len(got) != 2 || got[0].Name != "c" || got[1].Name != "b" {
		// a 仍熔断打开被排除;c(prio 2)、b(prio 9)按 priority 排
		t.Fatalf("after apply candidates = %v", got)
	}
	if _, ok := r.QuotaState("a"); !ok {
		t.Error("a quota snapshot should survive unrelated Apply")
	}

	// 删除 b:其候选消失;若 b 曾因硬配额被剪也一样
	r.Apply([]config.Upstream{{Name: "a", Type: config.TypeOpenAI, Priority: 1, MaxFailures: 1, CooldownSec: 60}})
	if got := r.Candidates("m"); len(got) != 0 {
		t.Fatalf("after removing b, a still open → want 0 candidates, got %v", got)
	}
	r.RecordSuccess("a") // 健康恢复
	if got := r.Candidates("m"); len(got) != 1 || got[0].Name != "a" {
		t.Fatalf("a should be healthy again, got %v", got)
	}
	if _, ok := r.QuotaState("b"); ok {
		t.Error("removed upstream quota snapshot should be pruned")
	}
}

// 配额感知选路:hard 的上游排到正常候选之后(尽力而为);未设置配额 = 不参与。
func TestQuotaTiering(t *testing.T) {
	r := New([]config.Upstream{
		{Name: "a", Type: config.TypeOpenAI, Priority: 1},
		{Name: "b", Type: config.TypeOpenAI, Priority: 2},
		{Name: "c", Type: config.TypeOpenAI, Priority: 3},
	})

	names := func(ups []*config.Upstream) []string {
		out := make([]string, len(ups))
		for i, u := range ups {
			out[i] = u.Name
		}
		return out
	}

	// 初始:无配额状态,按 priority
	if got := names(r.Candidates("m")); len(got) != 3 || got[0] != "a" {
		t.Fatalf("initial order = %v, want [a b c]", got)
	}

	// a(首选)配额耗尽 → 整体排到后面,正常序仍 [b c a]
	r.SetQuota("a", quota.Snapshot{UsedPct: 99, Hard: true, Status: "ok", Window: "monthly"})
	if got := names(r.Candidates("m")); len(got) != 3 || got[0] != "b" || got[2] != "a" {
		t.Fatalf("after hard(a) order = %v, want [b c a]", got)
	}

	// b 也 hard → 只剩 c 正常,其后 [a b]
	r.SetQuota("b", quota.Snapshot{UsedPct: 96, Hard: true})
	if got := names(r.Candidates("m")); len(got) != 3 || got[0] != "c" {
		t.Fatalf("after hard(a,b) order = %v, want c first", got)
	}

	// 全部 hard → 不拦截,尽力而为照常全量(保持 priority 序)
	r.SetQuota("c", quota.Snapshot{UsedPct: 100, Hard: true})
	if got := names(r.Candidates("m")); len(got) != 3 || got[0] != "a" {
		t.Fatalf("all hard order = %v, want [a b c]", got)
	}

	// 未 hard(低于阈值)不改变顺序
	r.SetQuota("a", quota.Snapshot{UsedPct: 60, Hard: false})
	if got := names(r.Candidates("m")); len(got) != 3 || got[0] != "a" {
		t.Fatalf("soft quota order = %v, want [a b c]", got)
	}
}
