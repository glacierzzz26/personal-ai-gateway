package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"personal-ai-gateway/internal/domain"
)

// commandCodeHost command code 的额度接口不在渠道 base_url(那是 /provider/v1 转发端点),
// 而在其主站的 alpha 计费接口 —— 故这里写死 host,不看 ch.BaseURL。var 非 const:测试可替换。
var commandCodeHost = "https://api.commandcode.ai"

// commandCodeCredits /alpha/billing/credits 响应。
// 注:credits.* 是**剩余**额度(命令名与 vendor CLI 反编译一致),不是已用。
type commandCodeCredits struct {
	Credits struct {
		MonthlyCredits   float64 `json:"monthlyCredits"`
		PurchasedCredits float64 `json:"purchasedCredits"`
		FreeCredits      float64 `json:"freeCredits"`
	} `json:"credits"`
	WindowLimits struct {
		Limited  bool              `json:"limited"`
		FiveHour commandCodeWindow `json:"fiveHour"`
		Weekly   commandCodeWindow `json:"weekly"`
	} `json:"windowLimits"`
}

// commandCodeWindow 单个限额窗口。resetAt 是 epoch 毫秒。
type commandCodeWindow struct {
	Used     float64 `json:"used"`
	Cap      float64 `json:"cap"`
	Exceeded bool    `json:"exceeded"`
	ResetAt  int64   `json:"resetAt"`
}

// fetchCommandCodeQuota 查 command code 订阅用量:窗口已用% + 剩余额度。
func fetchCommandCodeQuota(ctx context.Context, client *http.Client, ch domain.ChannelRow) (QuotaResult, error) {
	key, err := quotaKey(ch)
	if err != nil {
		return QuotaResult{}, err
	}
	body, lat, err := quotaDo(ctx, client, commandCodeHost+"/alpha/billing/credits", key)
	if err != nil {
		return QuotaResult{}, err
	}
	if err := ensureJSON(body); err != nil {
		return QuotaResult{}, err
	}
	if msg := relayErrorBody(body); msg != "" {
		return QuotaResult{LatencyMs: lat}, fmt.Errorf("upstream: %s", msg)
	}
	var resp commandCodeCredits
	if err := json.Unmarshal(body, &resp); err != nil {
		return QuotaResult{}, fmt.Errorf("cannot parse /alpha/billing/credits response: %w", err)
	}

	out := QuotaResult{LatencyMs: lat, Windows: map[string]domain.QuotaWindow{}}
	// fiveHour ≈ 网关既有的 rolling(近5h)口径,沿用同一窗口键以便前端一致展示。
	for key, w := range map[string]commandCodeWindow{
		"rolling": resp.WindowLimits.FiveHour,
		"weekly":  resp.WindowLimits.Weekly,
	} {
		if w.Cap <= 0 {
			continue
		}
		out.Windows[key] = domain.QuotaWindow{
			Status:  "ok",
			Percent: w.Used / w.Cap * 100,
			Used:    w.Used,
			Cap:     w.Cap,
			ResetAt: epochToRFC3339(w.ResetAt),
		}
	}
	remaining := resp.Credits.MonthlyCredits + resp.Credits.PurchasedCredits + resp.Credits.FreeCredits
	out.Balance = &domain.QuotaBalance{Amount: remaining, Currency: string(domain.CurrencyUSD)}
	return out, nil
}
