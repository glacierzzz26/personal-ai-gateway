package server

import (
	"net/http"
	"testing"

	"personal-ai-gateway/internal/domain"
)

// 零价供给源禁止启用(issue #26)。
//
// 判定口径:该模型**未绑定可用官方价** **且** 该供给源手填四价全为 0 → 拒绝启用。
// 服务端是唯一权威闸门(前端置灰只是提前提示),覆盖三条启用路径:
//   - 新建供给源(缺省 enabled=true)
//   - 编辑供给源(把价改成 0 而保留 enabled)
//   - 模型停用→启用时的批量联动(跳过零价,不整批失败)
//
// 回归红线:缓存读价为 0、或已绑定有效官方价 → 不受影响。

// mkZeroPriceModel 建一个未绑官方价的模型(渠道由用例自行准备),返回 modelID。
func mkZeroPriceModel(t *testing.T, c *http.Client, base string) int64 {
	t.Helper()
	// 建成「停用」态:模型启用联动只在 停用→启用 那一步触发,是本 issue 的主路径。
	code, body := doJSON(t, c, http.MethodPost, base+"/api/v1/models",
		map[string]any{"name": "gpt-zero", "contextWindow": 128000, "enabled": false})
	mustStatus(t, code, http.StatusOK, "create model")
	return decode[domain.ModelRead](t, body).ID
}

// apiErrType 解析 apiErr 的错误体,取 error.type。
func apiErrType(t *testing.T, body []byte) string {
	t.Helper()
	e := decode[struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}](t, body)
	return e.Error.Type
}

// TestCreateOfferZeroPriceRejected 新建零价供给源(缺省启用)被拒,错误码 zero_price 且带成因。
func TestCreateOfferZeroPriceRejected(t *testing.T) {
	srv, admin, _ := newTestServer(t)
	base := srv.URL
	bootstrap(t, admin, base)
	modelID := mkZeroPriceModel(t, admin, base)
	chID := mkChannelOf(t, admin, base, "cc", domain.ProviderOpenAI)

	// 不给价 → 零价 → 拒。
	code, body := doJSON(t, admin, http.MethodPost, base+"/api/v1/models/"+itoa(modelID)+"/offers",
		map[string]any{"channelId": chID})
	mustStatus(t, code, http.StatusBadRequest, "create zero-price offer")
	if got := apiErrType(t, body); got != "zero_price" {
		t.Fatalf("error.type = %q, want zero_price (body %s)", got, body)
	}
	msg := decode[struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}](t, body).Error.Message
	if msg == "" {
		t.Error("错误信息须说明成因(差什么)")
	}
}

// TestCreateOfferZeroPriceAllowedCases 两条回归红线:
//  1. 缓存读价/写价为 0 但输入价非 0 → 放行(缓存价 0 是合法形态);
//  2. 绑定了有效官方价 → 放行(兜底为空不影响)。
func TestCreateOfferZeroPriceAllowedCases(t *testing.T) {
	srv, admin, st := newTestServer(t)
	base := srv.URL
	bootstrap(t, admin, base)
	modelID := mkZeroPriceModel(t, admin, base)
	chID := mkChannelOf(t, admin, base, "cc", domain.ProviderOpenAI)

	// 1) 只有输入价,缓存读/写为 0 → 放行。
	code, body := doJSON(t, admin, http.MethodPost, base+"/api/v1/models/"+itoa(modelID)+"/offers",
		map[string]any{"channelId": chID, "inputPriceUsd": 3})
	mustStatus(t, code, http.StatusOK, "input-only offer")

	// 2) 建一条零价模型,但绑定有效官方价 → 零价供给源放行。
	code, body = doJSON(t, admin, http.MethodPost, base+"/api/v1/models",
		map[string]any{"name": "gpt-bound", "contextWindow": 128000})
	mustStatus(t, code, http.StatusOK, "create model")
	m2 := decode[domain.ModelRead](t, body)
	if _, err := st.UpsertOfficialPrice(domain.OfficialPriceRow{
		Provider: domain.ProviderOpenAI, ModelName: "gpt-bound",
		Currency: domain.CurrencyCNY, BillingShape: domain.ShapeFlat,
		InputPrice: 3, OutputPrice: 15,
	}); err != nil {
		t.Fatalf("upsert official: %v", err)
	}
	vendor, oname := string(domain.ProviderOpenAI), "gpt-bound"
	if _, err := st.UpdateModel(m2.ID, domain.ModelInput{OfficialVendor: &vendor, OfficialModelName: &oname}); err != nil {
		t.Fatalf("bind official: %v", err)
	}
	code, _ = doJSON(t, admin, http.MethodPost, base+"/api/v1/models/"+itoa(m2.ID)+"/offers",
		map[string]any{"channelId": chID})
	mustStatus(t, code, http.StatusOK, "zero-fallback offer with official price")
}

// TestUpdateOfferToZeroPriceRejected 已启用的供给源把价改成 0(仍 enabled)→ 被拒,
// 防止从「有价启用」改成「零价启用」绕过创建闸门。
func TestUpdateOfferToZeroPriceRejected(t *testing.T) {
	srv, admin, _ := newTestServer(t)
	base := srv.URL
	bootstrap(t, admin, base)
	modelID := mkZeroPriceModel(t, admin, base)
	chID := mkChannelOf(t, admin, base, "cc", domain.ProviderOpenAI)

	// 先建一条有价启用的。
	code, body := doJSON(t, admin, http.MethodPost, base+"/api/v1/models/"+itoa(modelID)+"/offers",
		map[string]any{"channelId": chID, "inputPriceUsd": 3, "outputPriceUsd": 15})
	mustStatus(t, code, http.StatusOK, "create priced offer")
	of := decode[domain.OfferRead](t, body)

	// 改成全 0 且仍启用 → 拒。
	code, body = doJSON(t, admin, http.MethodPatch, base+"/api/v1/offers/"+itoa(of.ID),
		map[string]any{"channelId": chID, "inputPriceUsd": 0, "outputPriceUsd": 0, "enabled": true})
	mustStatus(t, code, http.StatusBadRequest, "update to zero price")
	if got := apiErrType(t, body); got != "zero_price" {
		t.Fatalf("error.type = %q, want zero_price", got)
	}

	// 改成全 0 但显式停用 → 放行(不影响存量,允许「先停用再补价」)。
	code, _ = doJSON(t, admin, http.MethodPatch, base+"/api/v1/offers/"+itoa(of.ID),
		map[string]any{"channelId": chID, "inputPriceUsd": 0, "outputPriceUsd": 0, "enabled": false})
	mustStatus(t, code, http.StatusOK, "update to zero price disabled")
}

// TestModelEnableSkipsZeroPriceOffer 模型由停用切启用:零价供给源不被打开,有价的正常打开,
// 响应回报被跳过的渠道名。
func TestModelEnableSkipsZeroPriceOffer(t *testing.T) {
	srv, admin, st := newTestServer(t)
	base := srv.URL
	bootstrap(t, admin, base)
	modelID := mkZeroPriceModel(t, admin, base)
	chA := mkChannelOf(t, admin, base, "cc", domain.ProviderOpenAI)
	chB := mkChannelOf(t, admin, base, "cc-b", domain.ProviderOpenAI)

	// 有价的(停用挂上,待模型启用时联动打开)。
	if _, err := st.CreateOffer(modelID, domain.OfferInput{
		ChannelID: chA, InputPriceUsd: 3, OutputPriceUsd: 15, Enabled: boolPtrT(false),
	}); err != nil {
		t.Fatalf("create priced offer: %v", err)
	}
	// 零价的(直接落库绕过闸门,模拟迁移前挂上的存量)。
	if _, err := st.CreateOffer(modelID, domain.OfferInput{
		ChannelID: chB, Enabled: boolPtrT(false),
	}); err != nil {
		t.Fatalf("create zero offer: %v", err)
	}

	// 模型启用 → 联动。
	code, body := doJSON(t, admin, http.MethodPatch, base+"/api/v1/models/"+itoa(modelID),
		map[string]any{"name": "gpt-zero", "enabled": true})
	mustStatus(t, code, http.StatusOK, "enable model")

	resp := decode[struct {
		SkippedZeroPrice []string           `json:"skippedZeroPrice"`
		Offers           []domain.OfferRead `json:"offers"`
	}](t, body)

	if len(resp.SkippedZeroPrice) != 1 || resp.SkippedZeroPrice[0] != "cc-b" {
		t.Fatalf("skippedZeroPrice = %v, want [cc-b]", resp.SkippedZeroPrice)
	}
	got := map[string]bool{}
	for _, o := range resp.Offers {
		got[o.ChannelName] = o.Enabled
	}
	if !got["cc"] {
		t.Error("有价供给源应被联动启用")
	}
	if got["cc-b"] {
		t.Error("零价供给源不应被联动启用")
	}
}

// TestCostQuoteMarksZeroPrice 读接口回传 zeroPrice + zeroReason,前端据此置灰。
func TestCostQuoteMarksZeroPrice(t *testing.T) {
	srv, admin, st := newTestServer(t)
	base := srv.URL
	bootstrap(t, admin, base)
	modelID := mkZeroPriceModel(t, admin, base)
	chA := mkChannelOf(t, admin, base, "cc", domain.ProviderOpenAI)
	chB := mkChannelOf(t, admin, base, "cc-b", domain.ProviderOpenAI)

	// 零价的(绕过闸门落库,模拟存量);有价的作对照。
	if _, err := st.CreateOffer(modelID, domain.OfferInput{ChannelID: chA, Enabled: boolPtrT(false)}); err != nil {
		t.Fatalf("create zero offer: %v", err)
	}
	if _, err := st.CreateOffer(modelID, domain.OfferInput{
		ChannelID: chB, InputPriceUsd: 3, Enabled: boolPtrT(false),
	}); err != nil {
		t.Fatalf("create priced offer: %v", err)
	}

	models := decode[[]domain.ModelRead](t, mustGet(t, admin, base+"/api/v1/models"))
	var target *domain.ModelRead
	for i := range models {
		if models[i].ID == modelID {
			target = &models[i]
		}
	}
	if target == nil {
		t.Fatal("model not found in list")
	}
	byChannel := map[string]*domain.OfferRead{}
	for i := range target.Offers {
		byChannel[target.Offers[i].ChannelName] = &target.Offers[i]
	}
	zero := byChannel["cc"]
	if zero == nil || zero.Cost == nil {
		t.Fatal("zero-price offer / its cost not found")
	}
	if !zero.Cost.ZeroPrice {
		t.Errorf("零价供给源应带 zeroPrice=true,got %+v", *zero.Cost)
	}
	if zero.Cost.ZeroReason == "" {
		t.Error("零价供给源应带成因 zeroReason")
	}
	priced := byChannel["cc-b"]
	if priced == nil || priced.Cost == nil {
		t.Fatal("priced offer / its cost not found")
	}
	if priced.Cost.ZeroPrice {
		t.Errorf("有价供给源不应带 zeroPrice,got %+v", *priced.Cost)
	}
}
