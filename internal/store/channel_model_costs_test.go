package store

import (
	"testing"

	"personal-ai-gateway/internal/domain"
)

// mkChannel(定义见 models_test.go)建一个渠道,作为模型级系数行的外键目标。

// TestModelCostRatioCRUD 「渠道 × 模型」系数行的增删改查与「无行 = ErrNotFound」的回落契约。
func TestModelCostRatioCRUD(t *testing.T) {
	st := newTestStore(t)
	chID := mkChannel(t, st, "cc")

	// 无行 → ErrNotFound(调用方据此回落厂商级,而不是当成 0 成本)。
	_, err := st.ChannelModelRatio(chID, "deepseek-v4-1-flash")
	mustErrIs(t, err, ErrNotFound, "no row yet")

	// 写入(CC GOAT $10 买 $60 → 1/6,订阅页自动派生 source=plan)。
	mustNoErr(t, st.SetChannelModelCostRatio(chID, "deepseek-v4-1-flash", 1.0/6.0, "plan", "订阅计划页自动派生"), "set plan ratio")
	got, err := st.ChannelModelRatio(chID, "deepseek-v4-1-flash")
	mustNoErr(t, err, "read ratio")
	if got < 0.1666 || got > 0.1667 {
		t.Errorf("ratio = %v, want ~0.16667", got)
	}

	// upsert:同键再写应更新而非插新行。
	mustNoErr(t, st.SetChannelModelCostRatio(chID, "deepseek-v4-1-flash", 0.2, "manual", "人工改过"), "update ratio")
	got, _ = st.ChannelModelRatio(chID, "deepseek-v4-1-flash")
	mustEqual(t, got, 0.2, "updated ratio")
	rows, err := st.ListChannelModelCostRatios(chID)
	mustNoErr(t, err, "list")
	if len(rows) != 1 || rows[0].Note != "人工改过" || rows[0].Source != "manual" {
		t.Fatalf("upsert 应只有 1 行且来源/备注已更新: %+v", rows)
	}

	// 不同模型各自一行 —— 键是 (channel, model) 而非仅 channel。
	mustNoErr(t, st.SetChannelModelCostRatio(chID, "glm-5.3-flash", 1.0/6.0, "plan", ""), "set second model")
	rows, _ = st.ListChannelModelCostRatios(chID)
	mustEqual(t, len(rows), 2, "two models")
	if rows[0].ModelName != "deepseek-v4-1-flash" || rows[1].ModelName != "glm-5.3-flash" {
		t.Errorf("list 应按 model_name 排序: %+v", rows)
	}

	// 删除后退回落。
	mustNoErr(t, st.DeleteChannelModelCostRatio(chID, "deepseek-v4-1-flash"), "delete")
	_, err = st.ChannelModelRatio(chID, "deepseek-v4-1-flash")
	mustErrIs(t, err, ErrNotFound, "deleted")
	mustErrIs(t, st.DeleteChannelModelCostRatio(chID, "deepseek-v4-1-flash"), ErrNotFound, "delete missing")
}

// TestSetChannelModelCostRatioRejectsInvalid 系数 0 与空模型名必须拒绝:
// 0 = 上游免费送,若真如此也要显式删除该行 —— 填 0 会让「没配」与「配成免费」不可区分。
func TestSetChannelModelCostRatioRejectsInvalid(t *testing.T) {
	st := newTestStore(t)
	chID := mkChannel(t, st, "cc")
	for _, r := range []float64{0, -1, -0.5} {
		mustErrIs(t, st.SetChannelModelCostRatio(chID, "m", r, "manual", ""), ErrInvalidRatio, "reject ratio")
	}
	if err := st.SetChannelModelCostRatio(chID, "", 1.0, "manual", ""); err == nil {
		t.Error("空模型名应报错")
	}
	if err := st.SetChannelModelCostRatio(0, "m", 1.0, "manual", ""); err == nil {
		t.Error("非法渠道 ID 应报错")
	}
}

// TestReplaceChannelModelCostRatios 全量替换:先清后插(单事务),写入行一律 source=manual。
func TestReplaceChannelModelCostRatios(t *testing.T) {
	st := newTestStore(t)
	chID := mkChannel(t, st, "cc")
	// 先播一条 plan 行(订阅页派生),替换后应被清掉。
	mustNoErr(t, st.SetChannelModelCostRatio(chID, "old-model", 0.9, "plan", "老行"), "seed")

	mustNoErr(t, st.ReplaceChannelModelCostRatios(chID, []domain.ModelCostRatioInput{
		{ModelName: "deepseek-v4-1-flash", Ratio: 1.0 / 6.0},
		{ModelName: "glm-5.3-flash", Ratio: 0.5, Note: "人工"},
	}), "replace")
	rows, _ := st.ListChannelModelCostRatios(chID)
	mustEqual(t, len(rows), 2, "只留新提交的两行")
	for _, r := range rows {
		if r.Source != "manual" {
			t.Errorf("PUT 替换写入行一律 source=manual,got %q", r.Source)
		}
	}
	if got, _ := st.ChannelModelRatio(chID, "glm-5.3-flash"); got != 0.5 {
		t.Errorf("glm ratio = %v, want 0.5", got)
	}
	// 老行(plan)已清。
	mustErrIs(t, func() error { _, e := st.ChannelModelRatio(chID, "old-model"); return e }(), ErrNotFound, "old row gone")

	// 非法输入整体拒绝:校验在开事务前完成,老行不被清。
	mustErrIs(t, st.ReplaceChannelModelCostRatios(chID, []domain.ModelCostRatioInput{
		{ModelName: "a", Ratio: 1.0},
		{ModelName: "b", Ratio: 0},
	}), ErrInvalidRatio, "reject bad batch")
	rows, _ = st.ListChannelModelCostRatios(chID)
	mustEqual(t, len(rows), 2, "非法批次不得改动现有行")

	// 重复模型拒绝。
	if err := st.ReplaceChannelModelCostRatios(chID, []domain.ModelCostRatioInput{
		{ModelName: "dup", Ratio: 1},
		{ModelName: "dup", Ratio: 2},
	}); err == nil {
		t.Error("重复模型应报错")
	}
	// 空模型名拒绝。
	if err := st.ReplaceChannelModelCostRatios(chID, []domain.ModelCostRatioInput{{ModelName: "", Ratio: 1}}); err == nil {
		t.Error("空模型名应报错")
	}
}

// TestUpsertPlanRatiosDoesNotOverwriteManual 订阅页自动派生**不得覆盖人工行** ——
// 自动值只是默认,站主手改过的必须留住;非人工行(plan/无)则可更新。
func TestUpsertPlanRatiosDoesNotOverwriteManual(t *testing.T) {
	st := newTestStore(t)
	chID := mkChannel(t, st, "cc")

	// ① 人工行(站主手改 0.05)。
	mustNoErr(t, st.SetChannelModelCostRatio(chID, "manual-model", 0.05, "manual", "手改"), "seed manual")
	// ② 旧 plan 行(上次抓取写入)。
	mustNoErr(t, st.SetChannelModelCostRatio(chID, "plan-model", 0.9, "plan", ""), "seed plan")

	written, err := st.UpsertPlanRatios(chID, map[string]float64{
		"manual-model": 1.0 / 6.0,  // 不得覆盖
		"plan-model":   1.0 / 8.0,  // 应更新
		"new-model":    1.0 / 15.0, // 应新增
	})
	mustNoErr(t, err, "upsert plan")
	mustEqual(t, written, 2, "写入行数:plan 更新 + new 新增,manual 被挡下")

	// 人工行纹丝不动。
	if got, _ := st.ChannelModelRatio(chID, "manual-model"); got != 0.05 {
		t.Errorf("人工行被覆盖: ratio = %v, want 0.05", got)
	}
	rows, _ := st.ListChannelModelCostRatios(chID)
	for _, r := range rows {
		if r.ModelName == "manual-model" && r.Source != "manual" {
			t.Errorf("人工行来源被改: %+v", r)
		}
	}
	// plan 行已更新。
	if got, _ := st.ChannelModelRatio(chID, "plan-model"); got != 1.0/8.0 {
		t.Errorf("plan 行未更新: %v, want 0.125", got)
	}
	// 新行已落库,source=plan。
	var src string
	mustNoErr(t, st.db.QueryRow(`SELECT source FROM channel_model_cost_ratios WHERE channel_id=? AND model_name=?`,
		chID, "new-model").Scan(&src), "read new src")
	mustEqual(t, src, "plan", "新行 source")

	// 每来源计数(行数骤降护栏用它)。
	nPlan, _ := st.CountChannelModelRatiosBySource(chID, "plan")
	mustEqual(t, nPlan, 2, "plan 计数(plan-model + new-model)")
	nManual, _ := st.CountChannelModelRatiosBySource(chID, "manual")
	mustEqual(t, nManual, 1, "manual 计数")

	// 空 map 是 no-op,不报错也不写。
	w, err := st.UpsertPlanRatios(chID, nil)
	mustNoErr(t, err, "empty upsert")
	mustEqual(t, w, 0, "空 map 写入 0 行")
}

// TestModelRatioScopedPerChannel 同一模型在不同渠道可有不同系数(CC 1/6 vs opencode 1/8)。
func TestModelRatioScopedPerChannel(t *testing.T) {
	st := newTestStore(t)
	cc := mkChannel(t, st, "cc")
	oc := mkChannel(t, st, "oc")

	mustNoErr(t, st.SetChannelModelCostRatio(cc, "deepseek-v4-1-flash", 1.0/6.0, "plan", ""), "cc ratio")
	mustNoErr(t, st.SetChannelModelCostRatio(oc, "deepseek-v4.1-flash", 1.0/8.0, "plan", ""), "oc ratio")

	if got, _ := st.ChannelModelRatio(cc, "deepseek-v4-1-flash"); got != 1.0/6.0 {
		t.Errorf("cc ratio = %v", got)
	}
	if got, _ := st.ChannelModelRatio(oc, "deepseek-v4.1-flash"); got != 1.0/8.0 {
		t.Errorf("oc ratio = %v", got)
	}
	// 删除一个渠道的系数不影响另一个。
	mustNoErr(t, st.DeleteChannelModelCostRatio(cc, "deepseek-v4-1-flash"), "delete cc")
	if got, _ := st.ChannelModelRatio(oc, "deepseek-v4.1-flash"); got != 1.0/8.0 {
		t.Errorf("oc ratio 应不受影响, got %v", got)
	}
}
