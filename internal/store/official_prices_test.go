package store

import (
	"testing"
	"time"

	"personal-ai-gateway/internal/domain"
)

func mkOfficialPrice(p domain.Provider, model, url string, in, out float64) domain.OfficialPriceRow {
	return domain.OfficialPriceRow{
		Provider: p, ModelName: model, SourceURL: url,
		FetchedAt: time.Date(2026, 9, 13, 8, 0, 0, 0, time.UTC),
		Currency:  domain.CurrencyCNY, BillingShape: domain.ShapePeakOff,
		InputPrice: in, OutputPrice: out, CacheReadPrice: 0.02, CacheDerived: false,
		NativeText:    "1元 / 4元(每百万tokens)",
		Detail:        map[string]any{"peak": map[string]any{"in": 2.0, "out": 8.0}},
		ContentSHA256: "abc123",
	}
}

func TestOfficialPriceUpsertAndUnique(t *testing.T) {
	st := newTestStore(t)

	q, err := st.UpsertOfficialPrice(mkOfficialPrice(domain.ProviderDeepSeek, "deepseek-flash", "u1", 1, 4))
	mustNoErr(t, err, "first upsert")
	if q.ID == 0 || q.Currency != domain.CurrencyCNY {
		t.Fatalf("bad row: %+v", q)
	}
	firstCreated := q.CreatedAt

	// 同键二次 upsert → 更新而非新增。
	q2, err := st.UpsertOfficialPrice(mkOfficialPrice(domain.ProviderDeepSeek, "deepseek-flash", "u2", 1.5, 5))
	mustNoErr(t, err, "second upsert")
	if q2.ID != q.ID {
		t.Errorf("upsert should reuse row: id %d vs %d", q2.ID, q.ID)
	}
	if q2.InputPrice != 1.5 || q2.SourceURL != "u2" {
		t.Errorf("upsert did not update: %+v", q2)
	}
	rows, err := st.ListOfficialPrices(domain.ProviderDeepSeek)
	mustNoErr(t, err, "list")
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	// created_at 保留首次写入时间。
	if !rows[0].CreatedAt.Equal(firstCreated) {
		t.Errorf("created_at should be preserved: %v vs %v", rows[0].CreatedAt, firstCreated)
	}
	// detail_json 往返。
	if _, ok := rows[0].Detail["peak"]; !ok {
		t.Errorf("detail_json round-trip lost: %+v", rows[0].Detail)
	}

	// 其它 provider 互不影响。
	_, err = st.UpsertOfficialPrice(mkOfficialPrice(domain.ProviderQwen, "qwen3.8-max", "u3", 2, 6))
	mustNoErr(t, err, "qwen upsert")
	all, err := st.ListAllOfficialPrices()
	mustNoErr(t, err, "list all")
	if len(all) != 2 {
		t.Fatalf("want 2 rows, got %d", len(all))
	}
}

func TestApplyOfficialPriceWritesProvenanceNotOverride(t *testing.T) {
	st := newTestStore(t)
	ch := mkChannel(t, st, "A")
	m, _ := st.CreateModel(domain.ModelInput{Name: "deepseek-flash", ContextWindow: 64000})
	// 手工覆盖价 offer:override_price=true,应用官方价不得改动该标志,也不改用户主动设的价除非显式。
	over := true
	of, err := st.CreateOffer(m.ID, domain.OfferInput{
		ChannelID: ch, InputPriceUsd: 0.7, OutputPriceUsd: 3, OverridePrice: over, Note: "手工价",
	})
	mustNoErr(t, err, "create offer")

	q, err := st.UpsertOfficialPrice(mkOfficialPrice(domain.ProviderDeepSeek, "deepseek-flash", "https://api-docs.deepseek.com/x", 1, 4))
	mustNoErr(t, err, "upsert")

	mustNoErr(t, st.ApplyOfficialPrice(of.ID, q, 0.14, 0.56, 0.0028), "apply")

	got, err := st.GetOffer(of.ID)
	mustNoErr(t, err, "get offer")
	if got.InputPriceUsd != 0.14 || got.OutputPriceUsd != 0.56 || got.CacheReadPriceUsd != 0.0028 {
		t.Errorf("prices not applied: %+v", got)
	}
	if !got.OverridePrice {
		t.Error("override_price flag must be untouched by apply")
	}
	if got.Note != "手工价" {
		t.Errorf("note must be untouched: %q", got.Note)
	}
	if got.PriceSourceURL != "https://api-docs.deepseek.com/x" {
		t.Errorf("source url not written: %q", got.PriceSourceURL)
	}
	if got.PriceCurrency != string(domain.CurrencyCNY) {
		t.Errorf("currency not written: %q", got.PriceCurrency)
	}
	if got.PriceNativeText == "" {
		t.Error("native text not written")
	}
	if got.PriceFetchedAt == "" {
		t.Error("fetched_at not written")
	}
	// provenance 需在后续全量更新中存活(前端 offerDraft 必须回传)。
	keep := got
	p2 := keep.Priority
	_, err = st.UpdateOffer(of.ID, domain.OfferInput{
		ChannelID: ch, InputPriceUsd: keep.InputPriceUsd, OutputPriceUsd: keep.OutputPriceUsd,
		CacheReadPriceUsd: keep.CacheReadPriceUsd, OverridePrice: over, Priority: &p2,
		PriceSourceURL: keep.PriceSourceURL, PriceFetchedAt: keep.PriceFetchedAt,
		PriceCurrency: keep.PriceCurrency, PriceNativeText: keep.PriceNativeText,
	})
	mustNoErr(t, err, "update offer")
	after, _ := st.GetOffer(of.ID)
	if after.PriceSourceURL != keep.PriceSourceURL {
		t.Errorf("provenance lost on full update: %+v", after)
	}
}

func TestDeleteOfficialPrice(t *testing.T) {
	st := newTestStore(t)
	q, err := st.UpsertOfficialPrice(mkOfficialPrice(domain.ProviderDeepSeek, "m1", "u", 1, 2))
	mustNoErr(t, err, "upsert")
	mustNoErr(t, st.DeleteOfficialPrice(q.ID), "delete")
	mustErrIs(t, st.DeleteOfficialPrice(q.ID), ErrNotFound, "delete again")
}

// TestReconcileOfficialPrices 抓取对账:来源页不再列出的模型被删,同页保留项与他厂数据不受影响。
func TestReconcileOfficialPrices(t *testing.T) {
	st := newTestStore(t)
	const url = "https://help.aliyun.com/zh/model-studio/model-pricing"
	for _, m := range []string{"qwen-max", "glm-4.5", "deepseek-v3", "kimi-k2.5"} {
		if _, err := st.UpsertOfficialPrice(mkOfficialPrice(domain.ProviderQwen, m, url, 1, 2)); err != nil {
			t.Fatalf("seed %s: %v", m, err)
		}
	}
	// 另一厂商同页 URL 的行不得被误删(按 provider 隔离)。
	if _, err := st.UpsertOfficialPrice(mkOfficialPrice(domain.ProviderDeepSeek, "glm-4.5", url, 9, 9)); err != nil {
		t.Fatalf("seed deepseek: %v", err)
	}

	// 修正后的页面只剩 qwen-max → 其余 Qwen 视图内的第三方模型应被清掉。
	removed, err := st.DeleteOfficialPricesNotIn(domain.ProviderQwen, url, []string{"qwen-max"})
	mustNoErr(t, err, "reconcile")
	if removed != 3 {
		t.Fatalf("removed = %d, want 3", removed)
	}
	qwen, _ := st.ListOfficialPrices(domain.ProviderQwen)
	if len(qwen) != 1 || qwen[0].ModelName != "qwen-max" {
		t.Fatalf("qwen rows = %+v, want only qwen-max", qwen)
	}
	ds, _ := st.ListOfficialPrices(domain.ProviderDeepSeek)
	if len(ds) != 1 {
		t.Fatalf("deepseek rows = %+v, 不应被 Qwen 对账影响", ds)
	}

	// 空 keep → 不删(防御:宁留不误删)。
	removed, err = st.DeleteOfficialPricesNotIn(domain.ProviderQwen, url, nil)
	mustNoErr(t, err, "empty keep")
	if removed != 0 {
		t.Fatalf("empty keep should remove nothing, removed=%d", removed)
	}
}

func TestSettingsUSDPerCNY(t *testing.T) {
	st := newTestStore(t)
	cfg, err := st.GetSettings()
	mustNoErr(t, err, "get default")
	if cfg.USDPerCNY != 0 {
		t.Errorf("default usdPerCny should be 0, got %v", cfg.USDPerCNY)
	}
	cfg.USDPerCNY = 0.1405
	mustNoErr(t, st.SaveSettings(cfg), "save")

	got, err := st.GetSettings()
	mustNoErr(t, err, "get saved")
	if got.USDPerCNY != 0.1405 {
		t.Errorf("usdPerCny round-trip: %v", got.USDPerCNY)
	}
}
