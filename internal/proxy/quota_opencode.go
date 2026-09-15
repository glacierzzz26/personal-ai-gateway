package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"personal-ai-gateway/internal/domain"
)

// fetchOpenCodeQuota 查 opencode zen 订阅用量。
// 端点 = apiRoot(base_url) + /v1/usage —— 渠道按惯例填 https://opencode.ai/zen/go/v1 时
// 归一后正好落在 /zen/go/v1/usage(apiRoot 会去掉尾缀 /v1 再由本函数拼回)。
// 响应形状与既有 OpenAI 协议渠道的 /v1/usage 逐字节相同({usage:{rolling,weekly,monthly:{status,percent,resetsAt}}}),
// 故直接复用 usageEnvelope / windowsFromUsage —— resetsAt 由 parseQuotaWindow 归一。
func fetchOpenCodeQuota(ctx context.Context, client *http.Client, ch domain.ChannelRow) (QuotaResult, error) {
	key, err := quotaKey(ch)
	if err != nil {
		return QuotaResult{}, err
	}
	body, lat, err := quotaDo(ctx, client, apiRoot(ch.BaseURL)+"/v1/usage", key)
	if err != nil {
		return QuotaResult{}, err
	}
	if err := ensureJSON(body); err != nil {
		return QuotaResult{}, err
	}
	if msg := relayErrorBody(body); msg != "" {
		return QuotaResult{LatencyMs: lat}, fmt.Errorf("upstream: %s", msg)
	}
	var env usageEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return QuotaResult{}, fmt.Errorf("cannot parse zen usage response: %w", err)
	}
	return QuotaResult{PlanName: env.PlanName, Windows: windowsFromUsage(env), LatencyMs: lat}, nil
}
