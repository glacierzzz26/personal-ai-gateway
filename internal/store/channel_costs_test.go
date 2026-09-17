package store

import (
	"testing"

	"personal-ai-gateway/internal/domain"
)

// mkChannel(定义见 models_test.go)建一个渠道,作为系数行的外键目标。

// TestCostRatioCRUD 系数行的增删改查与「无行 = ErrNotFound」的回落契约。
func TestCostRatioCRUD(t *testing.T) {
	st := newTestStore(t)
	chID := mkChannel(t, st, "cc")

	// 无行 → ErrNotFound(调用方据此回落 1.0,而不是当成 0 成本)。
	_, err := st.ChannelVendorRatio(chID, domain.ProviderDeepSeek)
	mustErrIs(t, err, ErrNotFound, "no row yet")

	// 写入(commandcode: $10 买 $60 额度 → 1/6)。
	mustNoErr(t, st.SetCostRatio(chID, domain.ProviderDeepSeek, 1.0/6.0, "$10→$60"), "set ratio")
	got, err := st.ChannelVendorRatio(chID, domain.ProviderDeepSeek)
	mustNoErr(t, err, "read ratio")
	if got < 0.1666 || got > 0.1667 {
		t.Errorf("ratio = %v, want ~0.16667", got)
	}

	// upsert:同键再写应更新而非插新行。
	mustNoErr(t, st.SetCostRatio(chID, domain.ProviderDeepSeek, 0.2, "改过"), "update ratio")
	got, _ = st.ChannelVendorRatio(chID, domain.ProviderDeepSeek)
	mustEqual(t, got, 0.2, "updated ratio")
	rows, err := st.ListChannelCostRatios(chID)
	mustNoErr(t, err, "list")
	if len(rows) != 1 || rows[0].Note != "改过" {
		t.Fatalf("upsert 应只有 1 行且备注已更新: %+v", rows)
	}

	// 不同厂商各自一行 —— 键是 (channel, vendor) 而非仅 channel。
	mustNoErr(t, st.SetCostRatio(chID, domain.ProviderQwen, 0.5, ""), "set qwen ratio")
	rows, _ = st.ListChannelCostRatios(chID)
	mustEqual(t, len(rows), 2, "two vendors")

	// 删除后退回落。
	mustNoErr(t, st.DeleteCostRatio(chID, domain.ProviderDeepSeek), "delete")
	_, err = st.ChannelVendorRatio(chID, domain.ProviderDeepSeek)
	mustErrIs(t, err, ErrNotFound, "deleted")
	mustErrIs(t, st.DeleteCostRatio(chID, domain.ProviderDeepSeek), ErrNotFound, "delete missing")
}

// TestSetCostRatioRejectsNonPositive 系数 0 必须拒绝:0 = 上游免费送,
// 若真如此也要显式删除该行 —— 填 0 会让「没配」与「配成免费」不可区分。
func TestSetCostRatioRejectsNonPositive(t *testing.T) {
	st := newTestStore(t)
	chID := mkChannel(t, st, "cc")
	for _, r := range []float64{0, -1, -0.5} {
		mustErrIs(t, st.SetCostRatio(chID, domain.ProviderDeepSeek, r, ""), ErrInvalidRatio, "reject ratio")
	}
	if err := st.SetCostRatio(chID, "", 1.0, ""); err == nil {
		t.Error("空厂商应报错")
	}
}

// TestReplaceChannelCostRatios 全量替换:先清后插,且单事务(失败不留半截状态)。
func TestReplaceChannelCostRatios(t *testing.T) {
	st := newTestStore(t)
	chID := mkChannel(t, st, "cc")
	mustNoErr(t, st.SetCostRatio(chID, domain.ProviderDeepSeek, 0.9, "老行"), "seed")

	// 全量替换:老行(D账)应被清掉,只留新提交的两行。
	mustNoErr(t, st.ReplaceChannelCostRatios(chID, []domain.CostRatioInput{
		{Vendor: domain.ProviderDeepSeek, Ratio: 1.0 / 6.0},
		{Vendor: domain.ProviderQwen, Ratio: 0.5},
	}), "replace")
	_, err := st.ChannelVendorRatio(chID, domain.ProviderQwen)
	mustNoErr(t, err, "qwen present")
	if got, _ := st.ChannelVendorRatio(chID, domain.ProviderDeepSeek); got != 1.0/6.0 {
		t.Errorf("deepseek ratio = %v, want 1/6", got)
	}
	rows, _ := st.ListChannelCostRatios(chID)
	mustEqual(t, len(rows), 2, "只留新提交的两行")

	// 非法输入整体拒绝:校验在开事务前完成,老行不被清。
	mustErrIs(t, st.ReplaceChannelCostRatios(chID, []domain.CostRatioInput{
		{Vendor: domain.ProviderDeepSeek, Ratio: 1.0},
		{Vendor: domain.ProviderQwen, Ratio: 0},
	}), ErrInvalidRatio, "reject bad batch")
	rows, _ = st.ListChannelCostRatios(chID)
	mustEqual(t, len(rows), 2, "非法批次不得改动现有行")

	// 重复厂商拒绝。
	if err := st.ReplaceChannelCostRatios(chID, []domain.CostRatioInput{
		{Vendor: domain.ProviderQwen, Ratio: 1},
		{Vendor: domain.ProviderQwen, Ratio: 2},
	}); err == nil {
		t.Error("重复厂商应报错")
	}
}

// TestCostRatioScopedPerChannel 同一厂商在不同渠道可有不同系数 —— 这正是
// 「每个渠道每个模型单独核算成本」的落点(commandcode 1/6 vs 官方直连 1.0)。
func TestCostRatioScopedPerChannel(t *testing.T) {
	st := newTestStore(t)
	cc := mkChannel(t, st, "cc")
	direct := mkChannel(t, st, "direct")

	mustNoErr(t, st.SetCostRatio(cc, domain.ProviderDeepSeek, 1.0/6.0, ""), "cc ratio")
	mustNoErr(t, st.SetCostRatio(direct, domain.ProviderDeepSeek, 1.0, ""), "direct ratio")

	if got, _ := st.ChannelVendorRatio(cc, domain.ProviderDeepSeek); got != 1.0/6.0 {
		t.Errorf("cc ratio = %v", got)
	}
	if got, _ := st.ChannelVendorRatio(direct, domain.ProviderDeepSeek); got != 1.0 {
		t.Errorf("direct ratio = %v", got)
	}
	// 删除一个渠道的系数不影响另一个。
	mustNoErr(t, st.DeleteCostRatio(cc, domain.ProviderDeepSeek), "delete cc")
	if got, _ := st.ChannelVendorRatio(direct, domain.ProviderDeepSeek); got != 1.0 {
		t.Errorf("direct ratio 应不受影响, got %v", got)
	}
}

// TestBackfillOfficialBindings 自动回填:唯一规范名命中才写;歧义跳过;已绑定不覆盖。
func TestBackfillOfficialBindings(t *testing.T) {
	st := newTestStore(t)

	// 三行官方价:两行可分派,两个同厂商同规范名(造就歧义),一个孤立。
	for _, q := range []domain.OfficialPriceRow{
		{Provider: domain.ProviderQwen, ModelName: "qwen3.8-flash", Currency: domain.CurrencyCNY, InputPrice: 1, OutputPrice: 2},
		{Provider: domain.ProviderDeepSeek, ModelName: "deepseek-v4-pro", Currency: domain.CurrencyCNY, InputPrice: 1, OutputPrice: 2},
		{Provider: domain.ProviderQwen, ModelName: "qwen3.8-27b", Currency: domain.CurrencyCNY, InputPrice: 1, OutputPrice: 2},
	} {
		if _, err := st.UpsertOfficialPrice(q); err != nil {
			t.Fatalf("upsert official price %s: %v", q.ModelName, err)
		}
	}

	// 模型:① 带前缀可归一命中 ② 人工已绑定(不得覆盖) ③ 无官方价来源。
	m1, err := st.CreateModel(domain.ModelInput{Name: "Qwen/Qwen3.8-Flash"})
	mustNoErr(t, err, "create m1")
	m2, err := st.CreateModel(domain.ModelInput{
		Name: "already/bound", OfficialVendor: strPtrStore(string(domain.ProviderDeepSeek)),
		OfficialModelName: strPtrStore("deepseek-v4-pro"),
	})
	mustNoErr(t, err, "create m2")
	m3, err := st.CreateModel(domain.ModelInput{Name: "xai/grok-4.5"})
	mustNoErr(t, err, "create m3")

	fills, err := st.BackfillOfficialBindings(true) // dry-run:不写库
	mustNoErr(t, err, "dry-run")
	mustEqual(t, len(fills), 1, "dry-run 只应命中 1 个")
	if got, _ := st.GetModel(m1.ID); got.OfficialVendor != "" {
		t.Fatalf("dry-run 不得写库: %+v", got)
	}

	fills, err = st.BackfillOfficialBindings(false)
	mustNoErr(t, err, "apply")
	mustEqual(t, len(fills), 1, "只应命中 1 个")
	got, _ := st.GetModel(m1.ID)
	if got.OfficialVendor != domain.ProviderQwen || got.OfficialModelName != "qwen3.8-flash" {
		t.Errorf("m1 绑定 = %s/%s, want 通义千问/qwen3.8-flash", got.OfficialVendor, got.OfficialModelName)
	}

	// 人工绑定原样保留。
	m2got, _ := st.GetModel(m2.ID)
	if m2got.OfficialVendor != domain.ProviderDeepSeek || m2got.OfficialModelName != "deepseek-v4-pro" {
		t.Errorf("人工绑定被改动: %+v", m2got)
	}
	// 无来源的保持未绑定。
	if m3got, _ := st.GetModel(m3.ID); m3got.OfficialVendor != "" {
		t.Errorf("无官方价来源的模型不该被绑: %+v", m3got)
	}

	// 幂等:再跑一次无可绑定。
	fills, err = st.BackfillOfficialBindings(false)
	mustNoErr(t, err, "second run")
	mustEqual(t, len(fills), 0, "第二次应无可绑定")
}

// TestBackfillSkipsAmbiguousVendor 同一规范名命中多个厂商时跳过(交人工),不猜。
func TestBackfillSkipsAmbiguousVendor(t *testing.T) {
	st := newTestStore(t)
	for _, q := range []domain.OfficialPriceRow{
		{Provider: domain.ProviderQwen, ModelName: "flash-x", Currency: domain.CurrencyCNY, InputPrice: 1, OutputPrice: 2},
		{Provider: domain.ProviderZhipu, ModelName: "flash-x", Currency: domain.CurrencyCNY, InputPrice: 1, OutputPrice: 2},
	} {
		if _, err := st.UpsertOfficialPrice(q); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}
	m, err := st.CreateModel(domain.ModelInput{Name: "vendor/flash-x"})
	mustNoErr(t, err, "create model")

	fills, err := st.BackfillOfficialBindings(false)
	mustNoErr(t, err, "backfill")
	mustEqual(t, len(fills), 0, "多厂商歧义应跳过")
	if got, _ := st.GetModel(m.ID); got.OfficialVendor != "" {
		t.Errorf("歧义模型不该被绑: %+v", got)
	}
}

// TestBackfillLegacyDetailKeepsPeakFields 回归:回填只动绑定两列,不碰其它字段。
func TestBackfillDoesNotTouchOtherColumns(t *testing.T) {
	st := newTestStore(t)
	if _, err := st.UpsertOfficialPrice(domain.OfficialPriceRow{
		Provider: domain.ProviderQwen, ModelName: "qwen3.8-flash",
		Currency: domain.CurrencyCNY, InputPrice: 1, OutputPrice: 2,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	m, err := st.CreateModel(domain.ModelInput{
		Name: "Qwen/Qwen3.8-Flash", ContextWindow: 128000,
		Capabilities: []domain.Capability{"vision"},
	})
	mustNoErr(t, err, "create")

	if _, err := st.BackfillOfficialBindings(false); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	got, _ := st.GetModel(m.ID)
	if got.ContextWindow != 128000 || len(got.Capabilities) != 1 || got.Capabilities[0] != "vision" {
		t.Errorf("回填改动了绑定以外的列: %+v", got)
	}
}
