package store

import (
	"testing"
	"time"

	"personal-ai-gateway/internal/domain"
)

// TestModelOfficialBindingRoundTrip 模型级官方价绑定:建 → 读回 → nil 保留 → 空串清空 → 列表带出。
func TestModelOfficialBindingRoundTrip(t *testing.T) {
	st := newTestStore(t)
	const name = "deepseek/deepseek-v4.1-flash"

	created, err := st.CreateModel(domain.ModelInput{
		Name: name, ContextWindow: 64000,
		OfficialVendor:    strPtrStore(string(domain.ProviderDeepSeek)),
		OfficialModelName: strPtrStore("deepseek-flash"),
	})
	mustNoErr(t, err, "create model with binding")
	if created.OfficialVendor != domain.ProviderDeepSeek || created.OfficialModelName != "deepseek-flash" {
		t.Fatalf("binding not persisted on create: %+v", created)
	}

	got, err := st.GetModel(created.ID)
	mustNoErr(t, err, "get model")
	if got.OfficialVendor != domain.ProviderDeepSeek || got.OfficialModelName != "deepseek-flash" {
		t.Fatalf("binding not read back: %+v", got)
	}

	// 指针为 nil = 不改动(前端只传要改的字段时不得清空绑定)。
	kept, err := st.UpdateModel(created.ID, domain.ModelInput{Name: name, ContextWindow: 64000})
	mustNoErr(t, err, "update without binding")
	if kept.OfficialVendor != domain.ProviderDeepSeek || kept.OfficialModelName != "deepseek-flash" {
		t.Fatalf("nil binding must be preserved: %+v", kept)
	}

	// 空串 = 显式清空(回落自动匹配)。
	cleared, err := st.UpdateModel(created.ID, domain.ModelInput{
		Name: name, ContextWindow: 64000,
		OfficialVendor:    strPtrStore(""),
		OfficialModelName: strPtrStore(""),
	})
	mustNoErr(t, err, "clear binding")
	if cleared.OfficialVendor != "" || cleared.OfficialModelName != "" {
		t.Fatalf("empty binding must clear: %+v", cleared)
	}

	// ListModels 同样带出绑定字段。
	if _, err := st.UpdateModel(created.ID, domain.ModelInput{
		Name: name, ContextWindow: 64000,
		OfficialVendor:    strPtrStore(string(domain.ProviderDeepSeek)),
		OfficialModelName: strPtrStore("deepseek-flash"),
	}); err != nil {
		t.Fatalf("re-set binding: %v", err)
	}
	list, err := st.ListModels()
	mustNoErr(t, err, "list models")
	if len(list) != 1 || list[0].OfficialVendor != domain.ProviderDeepSeek || list[0].OfficialModelName != "deepseek-flash" {
		t.Fatalf("list must carry binding: %+v", list)
	}
}

// TestModelUpdateKeepsBindingAndFields 回归:UpdateModel 是「按请求体原值写入」而非增量 PATCH,
// 复刻前端 toggleModel 式全量草稿提交,断言绑定与上下文/能力不被误清。
func TestModelUpdateKeepsBindingAndFields(t *testing.T) {
	st := newTestStore(t)
	created, err := st.CreateModel(domain.ModelInput{
		Name: "qwen/qwen3.8-max", ContextWindow: 128000,
		Capabilities:      []domain.Capability{"vision", "reasoning"},
		OfficialVendor:    strPtrStore(string(domain.ProviderQwen)),
		OfficialModelName: strPtrStore("qwen3.8-max"),
	})
	mustNoErr(t, err, "create model")

	// 前端全量草稿:回传当前快照 + 仅改 enabled(与 api.modelDraft 一致)。
	enabled := false
	updated, err := st.UpdateModel(created.ID, domain.ModelInput{
		Name:              created.Name,
		ContextWindow:     created.ContextWindow,
		Capabilities:      created.Capabilities,
		Enabled:           &enabled,
		OfficialVendor:    strPtrStore(string(created.OfficialVendor)),
		OfficialModelName: strPtrStore(created.OfficialModelName),
	})
	mustNoErr(t, err, "update model")
	if updated.OfficialVendor != domain.ProviderQwen || updated.OfficialModelName != "qwen3.8-max" {
		t.Fatalf("binding lost: %+v", updated)
	}
	if updated.ContextWindow != 128000 || len(updated.Capabilities) != 2 {
		t.Fatalf("context/caps clobbered: %+v", updated)
	}
	if updated.Enabled {
		t.Fatalf("enabled should be false: %+v", updated)
	}
}

// TestBackfillDualSourceSameSlugNotAmbiguous 双来源同厂商同 slug(如 CC 与 opencode 的 Claude 系)
// **不**构成回填歧义 —— 歧义判据是**厂商**,两行同 provider 只算一个键,故仍能唯一回填。
func TestBackfillDualSourceSameSlugNotAmbiguous(t *testing.T) {
	st := newTestStore(t)
	for _, s := range []domain.PriceSource{domain.PriceSourceCommandCode, domain.PriceSourceOpenCode} {
		if _, err := st.UpsertOfficialPrice(domain.OfficialPriceRow{
			Provider: domain.ProviderAnthropic, ModelName: "claude-sonnet-5",
			Source: s, SourceURL: string(s), FetchedAt: time.Now().UTC(),
			Currency: domain.CurrencyUSD, InputPrice: 1, OutputPrice: 2,
		}); err != nil {
			t.Fatalf("seed %s: %v", s, err)
		}
	}
	m, err := st.CreateModel(domain.ModelInput{Name: "anthropic/claude-sonnet-5"})
	mustNoErr(t, err, "create model")

	fills, err := st.BackfillOfficialBindings(false)
	mustNoErr(t, err, "backfill")
	if len(fills) != 1 || fills[0].Vendor != domain.ProviderAnthropic {
		t.Fatalf("双来源同 slug 应唯一回填, got %+v", fills)
	}
	if fills[0].OfficialName != "claude-sonnet-5" {
		t.Errorf("official name = %q, want claude-sonnet-5", fills[0].OfficialName)
	}
	got, _ := st.GetModel(m.ID)
	if got.OfficialVendor != domain.ProviderAnthropic || got.OfficialModelName != "claude-sonnet-5" {
		t.Errorf("binding not written: %+v", got)
	}
}
