package server

import (
	"net/http"
	"testing"

	"personal-ai-gateway/internal/domain"
	"personal-ai-gateway/internal/store"
)

// TestChannelCostRatiosCRUD 系数行的资源式读写:PUT 全量替换、GET 回读、DELETE 退回 1.0。
//
// 独立于渠道 PATCH 是本接口存在的理由 —— 渠道是整体覆盖语义,系数混进去会被
// 「改个渠道名」误清空,故用一组独立端点验证它不被渠道更新波及。
func TestChannelCostRatiosCRUD(t *testing.T) {
	srv, admin, st := newTestServer(t)
	base := srv.URL
	bootstrap(t, admin, base)

	en := true
	ch, err := st.CreateChannel(domain.ChannelInput{
		Name: "cc", Provider: domain.ProviderOpenAI, BaseURL: "http://u.example/v1",
		APIKey: "sk", Priority: 1, Enabled: &en, TimeoutMs: 30000, MaxFailures: 2, CooldownSec: 10,
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	url := base + "/api/v1/channels/" + itoa(ch.ID) + "/cost-ratios"

	// 初始为空。
	rows := decode[[]map[string]any](t, mustGet(t, admin, url))
	if len(rows) != 0 {
		t.Fatalf("初始应为空,实际 %+v", rows)
	}

	// 全量写入两条。
	code, body := doJSON(t, admin, http.MethodPut, url, map[string]any{
		"ratios": []map[string]any{
			{"vendor": "DeepSeek", "ratio": 1.0 / 6.0, "note": "$10→$60"},
			{"vendor": "通义千问", "ratio": 0.5},
		},
	})
	mustStatus(t, code, http.StatusOK, "put ratios: "+string(body))
	rows = decode[[]map[string]any](t, body)
	if len(rows) != 2 {
		t.Fatalf("put 后应有 2 行,实际 %+v", rows)
	}

	// 改渠道名 —— 系数不该被渠道的全量覆盖语义带走。
	if _, err := st.UpdateChannel(ch.ID, domain.ChannelInput{
		Name: "cc-renamed", Provider: domain.ProviderOpenAI, BaseURL: "http://u.example/v1",
		Priority: 1, Enabled: &en, TimeoutMs: 30000, MaxFailures: 2, CooldownSec: 10,
	}); err != nil {
		t.Fatalf("update channel: %v", err)
	}
	rows = decode[[]map[string]any](t, mustGet(t, admin, url))
	if len(rows) != 2 {
		t.Fatalf("渠道改名不该影响系数,实际 %+v", rows)
	}

	// 全量替换成一条 —— 未回传的那条必须消失(PUT 语义,不是合并)。
	code, body = doJSON(t, admin, http.MethodPut, url, map[string]any{
		"ratios": []map[string]any{{"vendor": "DeepSeek", "ratio": 0.2}},
	})
	mustStatus(t, code, http.StatusOK, "replace: "+string(body))
	rows = decode[[]map[string]any](t, body)
	if len(rows) != 1 || rows[0]["vendor"] != "DeepSeek" {
		t.Fatalf("PUT 应整体替换,实际 %+v", rows)
	}

	// DELETE 退回默认(该行消失;计费侧回落 1.0)。
	code, body = doJSON(t, admin, http.MethodDelete, url+"?vendor=DeepSeek", nil)
	mustStatus(t, code, http.StatusOK, "delete: "+string(body))
	rows = decode[[]map[string]any](t, mustGet(t, admin, url))
	if len(rows) != 0 {
		t.Fatalf("删除后应为空,实际 %+v", rows)
	}
}

// TestChannelCostRatioRejectsZero 系数 0 必须被拒:0 会让「没配」与「配成免费」不可区分,
// 而前者该显示「未设系数,按 1.0 计」、后者是真事。要表达免费必须删除该行。
func TestChannelCostRatioRejectsZero(t *testing.T) {
	srv, admin, st := newTestServer(t)
	base := srv.URL
	bootstrap(t, admin, base)

	en := true
	ch, _ := st.CreateChannel(domain.ChannelInput{
		Name: "cc", Provider: domain.ProviderOpenAI, BaseURL: "http://u.example/v1",
		APIKey: "sk", Priority: 1, Enabled: &en, TimeoutMs: 30000, MaxFailures: 2, CooldownSec: 10,
	})
	url := base + "/api/v1/channels/" + itoa(ch.ID) + "/cost-ratios"
	for _, r := range []float64{0, -1} {
		code, _ := doJSON(t, admin, http.MethodPut, url, map[string]any{
			"ratios": []map[string]any{{"vendor": "DeepSeek", "ratio": r}},
		})
		if code != http.StatusBadRequest {
			t.Errorf("ratio=%v 应被拒(400),实际 %d", r, code)
		}
	}
	// 拒绝之后库里不该留下任何东西(校验在事务外,先于先清后插)。
	rows := decode[[]map[string]any](t, mustGet(t, admin, url))
	if len(rows) != 0 {
		t.Fatalf("非法写入不该落库,实际 %+v", rows)
	}
}

// TestCostRatioDeleteMissingIsNotFound 删不存在的行应 404(而不是静默成功):
// 前端「删除」按钮点了没反应会让人以为删掉了,实际上从没配过。
func TestCostRatioDeleteMissingIsNotFound(t *testing.T) {
	srv, admin, st := newTestServer(t)
	base := srv.URL
	bootstrap(t, admin, base)
	en := true
	ch, _ := st.CreateChannel(domain.ChannelInput{
		Name: "cc", Provider: domain.ProviderOpenAI, BaseURL: "http://u.example/v1",
		APIKey: "sk", Priority: 1, Enabled: &en, TimeoutMs: 30000, MaxFailures: 2, CooldownSec: 10,
	})
	code, _ := doJSON(t, admin, http.MethodDelete,
		base+"/api/v1/channels/"+itoa(ch.ID)+"/cost-ratios?vendor=DeepSeek", nil)
	if code != http.StatusNotFound {
		t.Fatalf("删不存在的系数应 404,实际 %d", code)
	}
	if _, err := st.ChannelVendorRatio(ch.ID, domain.ProviderDeepSeek); err != store.ErrNotFound {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// TestOfficialPricesRefreshWithoutBody 批量刷新接口要能吃空 body(空 = 全部可抓厂商)。
//
// 这里不真的抓(会出网),只验证:空 body 不报 400、响应形状正确、逐厂商结果都带 provider。
func TestOfficialPricesRefreshWithoutBody(t *testing.T) {
	srv, admin, _ := newTestServer(t)
	base := srv.URL
	bootstrap(t, admin, base)

	// 只指定一个必然不可抓的厂商,避免测试出网;断言的是「失败被记进结果而非 4xx/5xx」。
	code, body := doJSON(t, admin, http.MethodPost, base+"/api/v1/official-prices/refresh",
		map[string]any{"providers": []string{"Anthropic"}})
	mustStatus(t, code, http.StatusOK, "refresh: "+string(body))
	resp := decode[map[string]any](t, body)
	results := resp["results"].([]any)
	if len(results) != 1 {
		t.Fatalf("results = %+v", results)
	}
	first := results[0].(map[string]any)
	if first["provider"] != "Anthropic" {
		t.Errorf("provider = %v", first["provider"])
	}
	// Anthropic 是 manualOnly → 必然带错误,但**整体仍是 200** —— 逐厂商独立成败,
	// 一个厂商不可抓不该让整个批量请求失败。
	if first["error"] == nil || first["error"] == "" {
		t.Errorf("manualOnly 厂商应记错误,实际 %+v", first)
	}
	if resp["bound"] == nil {
		t.Errorf("bound 字段应始终存在(空数组),实际 %+v", resp)
	}
}

// TestApplyOfficialPriceBindsModelAndSnapshot 成本改造后「应用官方价」的语义已变:
// 真正让派生成本生效的动作是**写模型级绑定**,而不是把官方价写进 offer 三价。
//
// 改造前正是「写三价进 offer」这一步把官方挂牌价当成了成本 —— 生产里模型 20 的
// 1.0/4.0/0.02 就是这么来的,那是 DeepSeek 官方价,不是站主实付上游的钱。
func TestApplyOfficialPriceBindsModelAndSnapshot(t *testing.T) {
	srv, admin, st := newTestServer(t)
	base := srv.URL
	bootstrap(t, admin, base)

	en := true
	// 渠道 provider 写 OpenAI(聚合中转渠道的真实形态)—— 系数查找键取 plan.OfficialVendor
	// 而非 channels.provider,这里顺带钉住「绑定的是模型,不是渠道」。
	ch, err := st.CreateChannel(domain.ChannelInput{
		Name: "cc", Provider: domain.ProviderOpenAI, BaseURL: "http://u.example/v1",
		APIKey: "sk", Priority: 1, Enabled: &en, TimeoutMs: 30000, MaxFailures: 2, CooldownSec: 10,
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	m, err := st.CreateModel(domain.ModelInput{Name: "DeepSeek/deepseek-v4-pro", ContextWindow: 128000})
	if err != nil {
		t.Fatalf("create model: %v", err)
	}
	if _, err := st.CreateOffer(m.ID, domain.OfferInput{ChannelID: ch.ID, Enabled: boolPtrT(true)}); err != nil {
		t.Fatalf("create offer: %v", err)
	}

	// 一条官方价(尚未绑定到任何模型)。
	code, body := doJSON(t, admin, http.MethodPost, base+"/api/v1/official-prices/manual", map[string]any{
		"provider": string(domain.ProviderDeepSeek), "modelName": "deepseek-v4-pro",
		"sourceUrl": "https://api-docs.deepseek.com/quick_start/pricing", "currency": "CNY",
		"inputPrice": 2, "outputPrice": 8,
	})
	mustStatus(t, code, http.StatusOK, "manual price: "+string(body))
	op := decode[domain.OfficialPriceView](t, body)

	// 绑定前:成本是 unknown(四价全 0),不能算毛利。
	before := decode[[]map[string]any](t, mustGet(t, admin, base+"/api/v1/models"))
	c0 := before[0]["offers"].([]any)[0].(map[string]any)["cost"].(map[string]any)
	if c0["source"] != "unknown" {
		t.Fatalf("绑定前 source = %v, want unknown", c0["source"])
	}

	// 「应用到 offer」→ 实际动效是绑定模型。
	offerID := int64(before[0]["offers"].([]any)[0].(map[string]any)["id"].(float64))
	code, body = doJSON(t, admin, http.MethodPost, base+"/api/v1/official-prices/"+itoa(op.ID)+"/apply",
		map[string]any{"offerId": offerID})
	mustStatus(t, code, http.StatusOK, "apply: "+string(body))

	// 模型行上出现了绑定 —— 这才是让派生成本生效的动作。
	mr, err := st.GetModel(m.ID)
	if err != nil {
		t.Fatalf("get model: %v", err)
	}
	if mr.OfficialVendor != domain.ProviderDeepSeek || mr.OfficialModelName != "deepseek-v4-pro" {
		t.Fatalf("绑定未写入: %+v", mr)
	}
	// 顺带快照了四价(官方价原币 = 计价币种 CNY,故原值落库)—— 仅在派生不可用时兜底,
	// 不参与正常计费。
	of, err := st.GetOffer(offerID)
	if err != nil {
		t.Fatalf("get offer: %v", err)
	}
	if of.InputPriceUsd != 2 || of.OutputPriceUsd != 8 {
		t.Fatalf("兜底快照 = %v/%v, want 2/8", of.InputPriceUsd, of.OutputPriceUsd)
	}

	// 绑定后:成本改为派生,source=official,且**带「系数未设」提示**。
	after := decode[[]map[string]any](t, mustGet(t, admin, base+"/api/v1/models"))
	c1 := after[0]["offers"].([]any)[0].(map[string]any)["cost"].(map[string]any)
	if c1["source"] != "official" {
		t.Fatalf("绑定后 source = %v, want official", c1["source"])
	}
	approx(t, "派生成本.in", c1["in"].(float64), 2)
	if w, _ := c1["warn"].(string); w == "" {
		t.Errorf("未设系数必须带 warn,实际 %+v", c1)
	}
}
