package store

import (
	"testing"

	"personal-ai-gateway/internal/domain"
)

func mkChannel(t *testing.T, st *Store, name string) int64 {
	t.Helper()
	ch, err := st.CreateChannel(domain.ChannelInput{
		Name: name, Provider: domain.ProviderOpenAI, BaseURL: "http://" + name,
	})
	mustNoErr(t, err, "create channel "+name)
	return ch.ID
}

func TestModelCRUD(t *testing.T) {
	st := newTestStore(t)
	m, err := st.CreateModel(domain.ModelInput{Name: "gpt-4o", ContextWindow: 128000,
		Capabilities: []domain.Capability{domain.CapVision, domain.CapStream}})
	mustNoErr(t, err, "create model")
	if !m.Enabled {
		t.Error("model should default enabled")
	}
	byName, err := st.GetModelByName("gpt-4o")
	mustNoErr(t, err, "get by name")
	if byName.ID != m.ID {
		t.Error("get by name id mismatch")
	}
	_, err = st.CreateModel(domain.ModelInput{Name: "gpt-4o"})
	mustErrIs(t, err, ErrConflict, "dup model name")

	up, err := st.UpdateModel(m.ID, domain.ModelInput{Name: "gpt-4o-mini", ContextWindow: 8192})
	mustNoErr(t, err, "update model")
	if up.ContextWindow != 8192 || len(up.Capabilities) != 0 {
		t.Errorf("update not applied: %+v", up)
	}

	mustNoErr(t, st.SetModelEnabled(m.ID, false), "disable model")
	m, _ = st.GetModel(m.ID)
	if m.Enabled {
		t.Error("model should be disabled")
	}
	mustNoErr(t, st.DeleteModel(m.ID), "delete model")
	_, err = st.GetModel(m.ID)
	mustErrIs(t, err, ErrNotFound, "get deleted model")
}

func TestOfferLifecycleAndCascade(t *testing.T) {
	st := newTestStore(t)
	a := mkChannel(t, st, "A")
	b := mkChannel(t, st, "B")
	dead := mkChannel(t, st, "Dead")
	m, _ := st.CreateModel(domain.ModelInput{Name: "gpt-5", ContextWindow: 400000})

	// 两个供给源(priority 自动递增)
	o1, err := st.CreateOffer(m.ID, domain.OfferInput{ChannelID: a, InputPriceUsd: 1.5, OutputPriceUsd: 6})
	mustNoErr(t, err, "offer a")
	o2, err := st.CreateOffer(m.ID, domain.OfferInput{ChannelID: b, InputPriceUsd: 2, OutputPriceUsd: 8})
	mustNoErr(t, err, "offer b")
	if o1.Priority != 1 || o2.Priority != 2 {
		t.Errorf("auto priorities = %d,%d want 1,2", o1.Priority, o2.Priority)
	}

	// 重复供给源
	_, err = st.CreateOffer(m.ID, domain.OfferInput{ChannelID: a})
	mustErrIs(t, err, ErrConflict, "dup offer")

	// 读展示字段带渠道名/供应商
	if o1.ChannelName != "A" || o1.Provider != domain.ProviderOpenAI || o1.ContextWindow != 400000 {
		t.Errorf("offer read fields wrong: %+v", o1)
	}

	// 拖拽:把 A(o1) 拖到队尾
	ordered, err := st.ReorderOffers(m.ID, 0, 2)
	mustNoErr(t, err, "reorder offers")
	if len(ordered) != 2 || ordered[0].ID != o2.ID || ordered[1].ID != o1.ID {
		t.Errorf("reorder result wrong: %+v", ordered)
	}
	if ordered[0].Priority != 1 || ordered[1].Priority != 2 {
		t.Errorf("priority not renumbered: %d,%d", ordered[0].Priority, ordered[1].Priority)
	}

	// 启停
	mustNoErr(t, st.SetOfferEnabled(o1.ID, false), "disable offer")
	enabledOffers, err := st.ListEnabledOffersForModel(m.ID)
	mustNoErr(t, err, "list enabled offers")
	if len(enabledOffers) != 1 || enabledOffers[0].ID != o2.ID {
		t.Errorf("enabled offers = %+v", enabledOffers)
	}

	// 停用渠道的供给源即使 enabled 也不在启用列表;且读结构标 disabled
	mustNoErr(t, st.SetChannelEnabled(dead, false), "disable channel dead")
	_, err = st.CreateOffer(m.ID, domain.OfferInput{ChannelID: dead})
	mustNoErr(t, err, "offer on disabled channel")
	enabledOffers, _ = st.ListEnabledOffersForModel(m.ID)
	if len(enabledOffers) != 1 {
		t.Errorf("disabled-channel offer must not be enabled-candidate, got %d", len(enabledOffers))
	}

	// 启用模型且有启用 offer → 进目录;全关渠道 offer 后只剩 b
	reads, err := st.EnabledModelsWithOffers()
	mustNoErr(t, err, "enabled models")
	found := false
	for _, r := range reads {
		if r.Name == "gpt-5" {
			found = true
		}
	}
	if !found {
		t.Error("gpt-5 should appear in enabled catalog")
	}

	// 删除渠道级联删 offer
	mustNoErr(t, st.DeleteChannel(a), "delete channel a")
	if _, err := st.GetOffer(o1.ID); !errorsIsNotFound(err) {
		t.Errorf("offer of deleted channel should cascade; err=%v", err)
	}
}

// TestSetModelOffersEnabled 批量启停某模型下全部供给源(父级联动开关用)。
func TestSetModelOffersEnabled(t *testing.T) {
	st := newTestStore(t)
	a := mkChannel(t, st, "A")
	b := mkChannel(t, st, "B")
	m, _ := st.CreateModel(domain.ModelInput{Name: "gpt-cascade"})

	_, err := st.CreateOffer(m.ID, domain.OfferInput{ChannelID: a, Enabled: boolPtrStore(false)})
	mustNoErr(t, err, "offer a disabled")
	_, err = st.CreateOffer(m.ID, domain.OfferInput{ChannelID: b, Enabled: boolPtrStore(false)})
	mustNoErr(t, err, "offer b disabled")

	// 全部打开
	mustNoErr(t, st.SetModelOffersEnabled(m.ID, true), "enable all offers")
	offers, err := st.ListModelOffers(m.ID)
	mustNoErr(t, err, "list offers")
	for _, o := range offers {
		if !o.Enabled {
			t.Errorf("offer %d should be enabled", o.ID)
		}
	}

	// 单个供给源仍可单独关闭,不受父级锁死
	mustNoErr(t, st.SetOfferEnabled(offers[0].ID, false), "disable single offer")
	offers, _ = st.ListModelOffers(m.ID)
	if offers[0].Enabled {
		t.Error("single offer should be disabled independently")
	}
	if !offers[1].Enabled {
		t.Error("sibling offer should stay enabled")
	}

	// 只影响目标模型
	m2, _ := st.CreateModel(domain.ModelInput{Name: "other"})
	_, _ = st.CreateOffer(m2.ID, domain.OfferInput{ChannelID: a, Enabled: boolPtrStore(true)})
	mustNoErr(t, st.SetModelOffersEnabled(m.ID, false), "disable all offers of m")
	offers2, _ := st.ListModelOffers(m2.ID)
	if !offers2[0].Enabled {
		t.Error("other model's offer must be untouched")
	}
}

// TestModelRateOverrideTristate 售价倍率的三态语义:未传保持、显式 null 清空、数值覆盖。
// 倍率按模型存(迁移 v9),「清空回落全局」必须可用 —— 早期用 *float64 时 null 与未传同义,
// 清空会静默失效,故用 domain.OptionalFloat 区分。
func TestModelRateOverrideTristate(t *testing.T) {
	st := newTestStore(t)
	m, err := st.CreateModel(domain.ModelInput{Name: "gpt-rate"})
	mustNoErr(t, err, "create model")
	if m.RateOverride != nil {
		t.Fatalf("new model rate should be nil, got %v", *m.RateOverride)
	}

	// 设值
	up, err := st.UpdateModel(m.ID, domain.ModelInput{Name: "gpt-rate", RateOverride: domain.SetFloat(2.5)})
	mustNoErr(t, err, "set rate")
	if up.RateOverride == nil || *up.RateOverride != 2.5 {
		t.Fatalf("set rate not applied: %+v", up.RateOverride)
	}

	// 未传(零值 OptionalFloat)→ 保持原值
	up, err = st.UpdateModel(m.ID, domain.ModelInput{Name: "gpt-rate"})
	mustNoErr(t, err, "omit rate keeps value")
	if up.RateOverride == nil || *up.RateOverride != 2.5 {
		t.Fatalf("omitted rate should keep 2.5, got %+v", up.RateOverride)
	}

	// 显式清空 → nil(回落全局)
	up, err = st.UpdateModel(m.ID, domain.ModelInput{Name: "gpt-rate", RateOverride: domain.ClearFloat()})
	mustNoErr(t, err, "clear rate")
	if up.RateOverride != nil {
		t.Fatalf("cleared rate should be nil, got %v", *up.RateOverride)
	}

	// 清零也走「设值」态,不与清空混同
	up, err = st.UpdateModel(m.ID, domain.ModelInput{Name: "gpt-rate", RateOverride: domain.SetFloat(0)})
	mustNoErr(t, err, "set zero rate")
	if up.RateOverride == nil || *up.RateOverride != 0 {
		t.Fatalf("zero rate should persist as 0, got %+v", up.RateOverride)
	}
}

// TestUsableModelIDs 用户可见模型 = 启用 ∩ 有启用 offer ∩ 渠道启用(与数据面同源)。
func TestUsableModelIDs(t *testing.T) {
	st := newTestStore(t)
	ch := mkChannel(t, st, "A")
	chDead := mkChannel(t, st, "Dead")
	mOK, _ := st.CreateModel(domain.ModelInput{Name: "ok"})
	mNoOffer, _ := st.CreateModel(domain.ModelInput{Name: "no-offer"})
	mDeadCh, _ := st.CreateModel(domain.ModelInput{Name: "dead-ch"})
	mDisabled, _ := st.CreateModel(domain.ModelInput{Name: "disabled"})

	_, _ = st.CreateOffer(mOK.ID, domain.OfferInput{ChannelID: ch})
	_, _ = st.CreateOffer(mDeadCh.ID, domain.OfferInput{ChannelID: chDead})
	_, _ = st.CreateOffer(mDisabled.ID, domain.OfferInput{ChannelID: ch})
	mustNoErr(t, st.SetChannelEnabled(chDead, false), "disable channel")
	mustNoErr(t, st.SetModelEnabled(mDisabled.ID, false), "disable model")

	usable, err := st.UsableModelIDs()
	mustNoErr(t, err, "usable model ids")
	if !usable[mOK.ID] {
		t.Error("model with live offer+channel should be usable")
	}
	if usable[mNoOffer.ID] {
		t.Error("model without offers should not be usable")
	}
	if usable[mDeadCh.ID] {
		t.Error("model on disabled channel should not be usable")
	}
	if usable[mDisabled.ID] {
		t.Error("disabled model should not be usable")
	}
}

func boolPtrStore(b bool) *bool { return &b }

func errorsIsNotFound(err error) bool { return err == ErrNotFound }

// TestModelDisplayName 统一名称:创建/更新持久化、唯一约束、按对外名解析(含原真实名回落)。
func TestModelDisplayName(t *testing.T) {
	st := newTestStore(t)
	m, err := st.CreateModel(domain.ModelInput{Name: "deepseek-chat", DisplayName: strPtrStore("deepseek-v3")})
	mustNoErr(t, err, "create renamed model")
	if m.DisplayName != "deepseek-v3" || m.PublicName() != "deepseek-v3" {
		t.Fatalf("display name not persisted: %+v", m)
	}
	// 真实名与对外名都能解析到同一条
	byOrigin, err := st.GetModelByPublicName("deepseek-chat")
	mustNoErr(t, err, "resolve by origin name")
	byAlias, err := st.GetModelByPublicName("deepseek-v3")
	mustNoErr(t, err, "resolve by display name")
	if byOrigin.ID != m.ID || byAlias.ID != m.ID {
		t.Fatalf("resolution mismatch: origin=%d alias=%d want %d", byOrigin.ID, byAlias.ID, m.ID)
	}

	// 统一名唯一(部分索引)
	_, err = st.CreateModel(domain.ModelInput{Name: "other", DisplayName: strPtrStore("deepseek-v3")})
	mustErrIs(t, err, ErrConflict, "dup display name")

	// 统一名不得与他模型真实名歧义(否则按名解析会串模型)
	_, err = st.CreateModel(domain.ModelInput{Name: "glm-4", DisplayName: strPtrStore("deepseek-chat")})
	mustErrIs(t, err, ErrConflict, "display name shadows other model's origin name")
	// 真实名不得与他模型统一名歧义
	_, err = st.CreateModel(domain.ModelInput{Name: "deepseek-v3"})
	mustErrIs(t, err, ErrConflict, "origin name shadows other model's display name")

	// 更新为另一个统一名
	up, err := st.UpdateModel(m.ID, domain.ModelInput{Name: "deepseek-chat", DisplayName: strPtrStore("deepseek-v3-0324")})
	mustNoErr(t, err, "rename model")
	if up.PublicName() != "deepseek-v3-0324" {
		t.Fatalf("rename not applied: %+v", up)
	}
	// 清空统一名 → 回落真实名
	up, err = st.UpdateModel(m.ID, domain.ModelInput{Name: "deepseek-chat", DisplayName: strPtrStore("")})
	mustNoErr(t, err, "clear display name")
	if up.DisplayName != "" || up.PublicName() != "deepseek-chat" {
		t.Fatalf("clear display name failed: %+v", up)
	}
	// DisplayName 为 nil → 保持原统一名
	up, err = st.UpdateModel(m.ID, domain.ModelInput{Name: "deepseek-chat", DisplayName: strPtrStore("ds")})
	mustNoErr(t, err, "set display name")
	up, err = st.UpdateModel(m.ID, domain.ModelInput{Name: "deepseek-chat-2", ContextWindow: 100})
	mustNoErr(t, err, "update without displayName keeps it")
	if up.DisplayName != "ds" || up.Name != "deepseek-chat-2" {
		t.Fatalf("nil displayName should keep value: %+v", up)
	}
}

func strPtrStore(s string) *string { return &s }
