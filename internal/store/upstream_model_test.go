package store

import (
	"testing"

	"personal-ai-gateway/internal/domain"
)

func boolPtrStoreModel(b bool) *bool { return &b }

// TestOfferUpstreamModelRoundTrip 上游名经 Create/Update/scan 往返;UpdateOffer 全量替换语义下漏传即清空。
func TestOfferUpstreamModelRoundTrip(t *testing.T) {
	st := newTestStore(t)
	ch := mkChannel(t, st, "A")
	m, _ := st.CreateModel(domain.ModelInput{Name: "deepseek-v4.1-flash"})

	o, err := st.CreateOffer(m.ID, domain.OfferInput{
		ChannelID: ch, InputPriceUsd: 1, OutputPriceUsd: 2, UpstreamModel: "deepseek/deepseek-v4.1-flash",
	})
	mustNoErr(t, err, "create offer with upstream")
	if o.UpstreamModel != "deepseek/deepseek-v4.1-flash" {
		t.Fatalf("upstream not persisted: %q", o.UpstreamModel)
	}

	// 读回(经 scanOffer)
	got, err := st.GetOffer(o.ID)
	mustNoErr(t, err, "get offer")
	if got.UpstreamModel != "deepseek/deepseek-v4.1-flash" {
		t.Fatalf("upstream not scanned: %q", got.UpstreamModel)
	}

	// 全量替换:不传 upstreamModel → 被清空(记录该语义,前端必须回传)
	_, err = st.UpdateOffer(o.ID, domain.OfferInput{
		ChannelID: ch, InputPriceUsd: 1, OutputPriceUsd: 2, OverridePrice: true,
		Enabled: boolPtrStoreModel(true),
	})
	mustNoErr(t, err, "update offer")
	got, _ = st.GetOffer(o.ID)
	if got.UpstreamModel != "" {
		t.Fatalf("full-replace should clear upstream when omitted, got %q", got.UpstreamModel)
	}

	// 窄 setter 恢复
	mustNoErr(t, st.SetOfferUpstreamModel(o.ID, "deepseek-v4.1-flash"), "set upstream")
	got, _ = st.GetOffer(o.ID)
	if got.UpstreamModel != "deepseek-v4.1-flash" {
		t.Fatalf("setter failed: %q", got.UpstreamModel)
	}
}

func TestCanonicalModelKey(t *testing.T) {
	cases := map[string]string{
		"deepseek/deepseek-v4.1-flash": "deepseek-v4.1-flash",
		"deepseek-v4.1-flash":          "deepseek-v4.1-flash",
		"DeepSeek-V4.1-Flash":          "deepseek-v4.1-flash",
		"  a/b/c  ":                    "c",
		"gpt-4o":                       "gpt-4o",
	}
	for in, want := range cases {
		if got := canonicalModelKey(in); got != want {
			t.Errorf("canonicalModelKey(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestGetModelByCanonical 带前缀与裸名归到同一模型;两边都带前缀不误合并。
func TestGetModelByCanonical(t *testing.T) {
	st := newTestStore(t)
	bare, _ := st.CreateModel(domain.ModelInput{Name: "deepseek-v4.1-flash"})

	got, err := st.GetModelByCanonical("deepseek/deepseek-v4.1-flash")
	mustNoErr(t, err, "canonical resolve to bare")
	if got.ID != bare.ID {
		t.Fatalf("expected canonically-equal bare model, got id %d", got.ID)
	}
	// 精确命中
	got, err = st.GetModelByCanonical("deepseek-v4.1-flash")
	mustNoErr(t, err, "canonical exact")
	if got.ID != bare.ID {
		t.Fatalf("exact resolve mismatch")
	}

	// 两个都带前缀、后缀相同 → 不归并
	p1, _ := st.CreateModel(domain.ModelInput{Name: "openai/gpt-4o"})
	_, _ = st.CreateModel(domain.ModelInput{Name: "anthropic/gpt-4o"})
	got, err = st.GetModelByCanonical("anthropic/gpt-4o")
	mustNoErr(t, err, "prefixed exact")
	if got.Name != "anthropic/gpt-4o" {
		t.Fatalf("prefixed exact should hit itself, got %q", got.Name)
	}
	// 第三方前缀的 gpt-4o 不落到 openai/gpt-4o(两边都带前缀,闸门拦住)
	_, err = st.GetModelByCanonical("xai/gpt-4o")
	mustErrIs(t, err, ErrNotFound, "both prefixed must not merge")
	_ = p1
}

// TestMergeModels 合并重复模型:搬 offer + 空上游名回填 + 删空模型 + priority 重排。
func TestMergeModels(t *testing.T) {
	st := newTestStore(t)
	a := mkChannel(t, st, "A")
	b := mkChannel(t, st, "B")
	src, _ := st.CreateModel(domain.ModelInput{Name: "deepseek-v4.1-flash"})
	dst, _ := st.CreateModel(domain.ModelInput{Name: "deepseek/deepseek-v4.1-flash"})

	// src 的 offer 未配上游名(存量),dst 的已配
	_, err := st.CreateOffer(src.ID, domain.OfferInput{ChannelID: a, InputPriceUsd: 1, OutputPriceUsd: 2})
	mustNoErr(t, err, "src offer")
	_, err = st.CreateOffer(dst.ID, domain.OfferInput{
		ChannelID: b, InputPriceUsd: 1, OutputPriceUsd: 2, UpstreamModel: "deepseek/deepseek-v4.1-flash",
	})
	mustNoErr(t, err, "dst offer")

	merged, err := st.MergeModels(src.ID, dst.ID)
	mustNoErr(t, err, "merge")
	if merged.ID != dst.ID {
		t.Fatalf("merge returned wrong model")
	}
	offers, err := st.ListModelOffers(dst.ID)
	mustNoErr(t, err, "list merged offers")
	if len(offers) != 2 {
		t.Fatalf("expected 2 offers after merge, got %d", len(offers))
	}
	// 空上游名回填为 src.Name(否则会错误继承 dst.Name 路由到错模型)
	for _, o := range offers {
		if o.ChannelID == a && o.UpstreamModel != "deepseek-v4.1-flash" {
			t.Errorf("src offer upstream not backfilled, got %q", o.UpstreamModel)
		}
		if o.ChannelID == b && o.UpstreamModel != "deepseek/deepseek-v4.1-flash" {
			t.Errorf("dst offer upstream clobbered, got %q", o.UpstreamModel)
		}
	}
	// priority 重排 1..N
	if offers[0].Priority != 1 || offers[1].Priority != 2 {
		t.Errorf("priority not renumbered: %d, %d", offers[0].Priority, offers[1].Priority)
	}
	// src 已删
	_, err = st.GetModel(src.ID)
	mustErrIs(t, err, ErrNotFound, "src deleted after merge")
}

// TestMergeModelsConflict 两模型在同一渠道都有 offer → ErrConflict。
func TestMergeModelsConflict(t *testing.T) {
	st := newTestStore(t)
	a := mkChannel(t, st, "A")
	src, _ := st.CreateModel(domain.ModelInput{Name: "m1"})
	dst, _ := st.CreateModel(domain.ModelInput{Name: "m2"})
	_, _ = st.CreateOffer(src.ID, domain.OfferInput{ChannelID: a})
	_, _ = st.CreateOffer(dst.ID, domain.OfferInput{ChannelID: a})

	_, err := st.MergeModels(src.ID, dst.ID)
	mustErrIs(t, err, ErrConflict, "same-channel merge conflict")
	// 冲突时两边都要还在(事务回滚)
	if _, err := st.GetModel(src.ID); err != nil {
		t.Errorf("src should survive failed merge: %v", err)
	}
	_, _ = st.MergeModels(src.ID, src.ID)
}
