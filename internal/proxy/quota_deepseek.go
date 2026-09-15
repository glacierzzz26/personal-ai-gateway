package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"personal-ai-gateway/internal/domain"
)

// deepSeekBalance /user/balance 响应。金额字段是**字符串**,可能多币种。
type deepSeekBalance struct {
	IsAvailable  bool `json:"is_available"`
	BalanceInfos []struct {
		Currency        string `json:"currency"`
		TotalBalance    string `json:"total_balance"`
		GrantedBalance  string `json:"granted_balance"`
		ToppedUpBalance string `json:"topped_up_balance"`
	} `json:"balance_infos"`
}

// fetchDeepSeekQuota 查 DeepSeek 官方账户余额。这类上游只报绝对值,没有窗口百分比。
// 端点 = apiRoot(base_url) + /user/balance —— 注意**不在 /v1 下**,apiRoot 去掉尾缀 /v1 后
// 拼出的正是 https://api.deepseek.com/user/balance。
func fetchDeepSeekQuota(ctx context.Context, client *http.Client, ch domain.ChannelRow) (QuotaResult, error) {
	key, err := quotaKey(ch)
	if err != nil {
		return QuotaResult{}, err
	}
	body, lat, err := quotaDo(ctx, client, apiRoot(ch.BaseURL)+"/user/balance", key)
	if err != nil {
		return QuotaResult{}, err
	}
	if err := ensureJSON(body); err != nil {
		return QuotaResult{}, err
	}
	if msg := relayErrorBody(body); msg != "" {
		return QuotaResult{LatencyMs: lat}, fmt.Errorf("upstream: %s", msg)
	}
	var resp deepSeekBalance
	if err := json.Unmarshal(body, &resp); err != nil {
		return QuotaResult{}, fmt.Errorf("cannot parse /user/balance response: %w", err)
	}
	if len(resp.BalanceInfos) == 0 {
		return QuotaResult{LatencyMs: lat}, fmt.Errorf("上游未返回余额信息")
	}
	// 主要币种取第一个(DeepSeek 目前单币种返回)。
	info := resp.BalanceInfos[0]
	amount, err := strconv.ParseFloat(info.TotalBalance, 64)
	if err != nil {
		return QuotaResult{LatencyMs: lat}, fmt.Errorf("cannot parse balance %q: %w", info.TotalBalance, err)
	}
	out := QuotaResult{LatencyMs: lat, Balance: &domain.QuotaBalance{Amount: amount, Currency: info.Currency}}
	if !resp.IsAvailable {
		out.PlanName = "账户不可用"
	}
	return out, nil
}
