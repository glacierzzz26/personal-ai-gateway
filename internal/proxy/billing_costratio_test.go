package proxy

import (
	"testing"

	"personal-ai-gateway/internal/domain"
	"personal-ai-gateway/internal/engine"
	"personal-ai-gateway/internal/storetest"
)

// TestCostRatioThreeTier 计费成本系数的三级口径(成本 = 官方价 × ratio):
//
//	① 模型级(channel_model_cost_ratios)> ② 厂商级(channel_vendor_costs)> ③ 1.0。
//
// 同时断言 scope 标注(模型级→"model" / 厂商级→"vendor" / 都没有→"")。
func TestCostRatioThreeTier(t *testing.T) {
	st := storetest.Open(t)
	gw := NewGateway(st, engine.New(st), NewRelay(st))

	en := true
	ch, err := st.CreateChannel(domain.ChannelInput{
		Name: "cc", Provider: domain.ProviderOpenAI, BaseURL: "http://cc", Enabled: &en,
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	const model = "deepseek-v4-1-flash"
	const vendor = domain.ProviderDeepSeek

	// ③ 两者都没有 → 1.0(不折扣),scope 空。
	if r, s := gw.costRatio(ch.ID, vendor, model); r != 1.0 || s != "" {
		t.Fatalf("no ratio: got (%v, %q), want (1, \"\")", r, s)
	}

	// ② 只配厂商级 → 取厂商级。
	if err := st.SetCostRatio(ch.ID, vendor, 1.0/6.0, "$10→$60"); err != nil {
		t.Fatalf("set vendor ratio: %v", err)
	}
	if r, s := gw.costRatio(ch.ID, vendor, model); r != 1.0/6.0 || s != "vendor" {
		t.Fatalf("vendor only: got (%v, %q), want (%v, \"vendor\")", r, s, 1.0/6.0)
	}

	// ① 模型级与厂商级并存 → 模型级优先。
	if err := st.SetChannelModelCostRatio(ch.ID, model, 1.0/8.0, "plan", ""); err != nil {
		t.Fatalf("set model ratio: %v", err)
	}
	if r, s := gw.costRatio(ch.ID, vendor, model); r != 1.0/8.0 || s != "model" {
		t.Fatalf("model wins: got (%v, %q), want (%v, \"model\")", r, s, 1.0/8.0)
	}

	// 模型名不匹配(该模型无模型级行)→ 回落厂商级。
	if r, s := gw.costRatio(ch.ID, vendor, "glm-5.3-flash"); r != 1.0/6.0 || s != "vendor" {
		t.Fatalf("model miss falls back: got (%v, %q), want (%v, \"vendor\")", r, s, 1.0/6.0)
	}

	// 删除模型级 → 回落厂商级。
	if err := st.DeleteChannelModelCostRatio(ch.ID, model); err != nil {
		t.Fatalf("delete model ratio: %v", err)
	}
	if r, s := gw.costRatio(ch.ID, vendor, model); r != 1.0/6.0 || s != "vendor" {
		t.Fatalf("after delete: got (%v, %q), want (%v, \"vendor\")", r, s, 1.0/6.0)
	}

	// 删除厂商级 → 回落 1.0。
	if err := st.DeleteCostRatio(ch.ID, vendor); err != nil {
		t.Fatalf("delete vendor ratio: %v", err)
	}
	if r, s := gw.costRatio(ch.ID, vendor, model); r != 1.0 || s != "" {
		t.Fatalf("all gone: got (%v, %q), want (1, \"\")", r, s)
	}

	// channelID <= 0 → 1.0(无渠道上下文,cost 不折扣)。
	if r, s := gw.costRatio(0, vendor, model); r != 1.0 || s != "" {
		t.Fatalf("no channel: got (%v, %q), want (1, \"\")", r, s)
	}

	// 模型名为空 → 跳过模型级,按厂商级。
	if err := st.SetCostRatio(ch.ID, vendor, 0.5, ""); err != nil {
		t.Fatalf("re-set vendor ratio: %v", err)
	}
	if r, s := gw.costRatio(ch.ID, vendor, ""); r != 0.5 || s != "vendor" {
		t.Fatalf("empty model name: got (%v, %q), want (0.5, \"vendor\")", r, s)
	}
}
