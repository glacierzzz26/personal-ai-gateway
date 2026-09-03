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

func errorsIsNotFound(err error) bool { return err == ErrNotFound }
