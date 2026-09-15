package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"personal-ai-gateway/internal/domain"
)

// newAPIQuotaPerUnit new-api 的额度单位换算(500000 额度 = 1 USD,为 new-api 默认值)。
const newAPIQuotaPerUnit = 500000

// fetchThirdPartyQuota 查第三方中转站额度 —— 路径由管理员手工配置(ch.QuotaPath),
// 形状从有限集里选(ch.QuotaShape)。空配置不是错误,是「还没配」,单独给 ErrQuotaNotConfigured
// 让前端提示去填,而不是当成上游故障。
func fetchThirdPartyQuota(ctx context.Context, client *http.Client, ch domain.ChannelRow) (QuotaResult, error) {
	path := strings.TrimSpace(ch.QuotaPath)
	if path == "" {
		return QuotaResult{}, ErrQuotaNotConfigured
	}
	shape := domain.QuotaShape(ch.QuotaShape)
	if shape == "" {
		shape = domain.ShapeUsage
	}
	if !shape.Valid() {
		return QuotaResult{}, fmt.Errorf("未知的额度形状: %q", ch.QuotaShape)
	}
	key, err := quotaKey(ch)
	if err != nil {
		return QuotaResult{}, err
	}
	base := apiRoot(ch.BaseURL)
	switch shape {
	case domain.ShapeUsage:
		return fetchUsageShapeQuota(ctx, client, base+path, key)
	case domain.ShapeOneAPI:
		return fetchOneAPIQuota(ctx, client, base, path, key)
	case domain.ShapeNewAPIUser:
		return fetchNewAPIUserQuota(ctx, client, base, path, key)
	}
	return QuotaResult{}, ErrQuotaUnsupported
}

// fetchUsageShapeQuota 通用 {usage:{rolling,weekly,monthly}} 信封(网关原生协议)。
func fetchUsageShapeQuota(ctx context.Context, client *http.Client, url, key string) (QuotaResult, error) {
	body, lat, err := quotaDo(ctx, client, url, key)
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
		return QuotaResult{}, fmt.Errorf("cannot parse usage response: %w", err)
	}
	return QuotaResult{PlanName: env.PlanName, Windows: windowsFromUsage(env), LatencyMs: lat}, nil
}

// fetchOneAPIQuota one-api/new-api 计费接口:
//
//	{subscriptionPath} → {hard_limit_usd}   总额度(美元)
//	{usagePath}        → {total_usage}      已用(美分)
//
// usagePath 由 subscriptionPath 末段 subscription→usage 推出(二者同前缀是该系列固定约定);
// 推不出(路径里没有 subscription)则只报总额度。
func fetchOneAPIQuota(ctx context.Context, client *http.Client, base, path, key string) (QuotaResult, error) {
	subBody, lat, err := quotaDo(ctx, client, base+path, key)
	if err != nil {
		return QuotaResult{}, err
	}
	if err := ensureJSON(subBody); err != nil {
		return QuotaResult{}, err
	}
	if msg := relayErrorBody(subBody); msg != "" {
		return QuotaResult{LatencyMs: lat}, fmt.Errorf("upstream: %s", msg)
	}
	var sub struct {
		HardLimitUsd float64 `json:"hard_limit_usd"`
		SoftLimitUsd float64 `json:"soft_limit_usd"`
	}
	if err := json.Unmarshal(subBody, &sub); err != nil {
		return QuotaResult{}, fmt.Errorf("cannot parse billing subscription: %w", err)
	}
	limit := sub.HardLimitUsd
	if limit <= 0 {
		limit = sub.SoftLimitUsd
	}
	out := QuotaResult{LatencyMs: lat, Windows: map[string]domain.QuotaWindow{}}

	usagePath, ok := usagePathFor(path)
	if !ok {
		// 只有总额度、拿不到已用 —— 作为余额展示。
		out.Balance = &domain.QuotaBalance{Amount: limit, Currency: string(domain.CurrencyUSD)}
		return out, nil
	}
	useBody, lat2, err := quotaDo(ctx, client, base+usagePath, key)
	if err != nil {
		return out, err
	}
	out.LatencyMs = lat + lat2
	if err := ensureJSON(useBody); err != nil {
		return out, err
	}
	var use struct {
		TotalUsage float64 `json:"total_usage"` // 美分
	}
	if err := json.Unmarshal(useBody, &use); err != nil {
		return out, fmt.Errorf("cannot parse billing usage: %w", err)
	}
	usedUsd := use.TotalUsage / 100
	out.Balance = &domain.QuotaBalance{Amount: limit - usedUsd, Currency: string(domain.CurrencyUSD)}
	if limit > 0 {
		out.Windows["monthly"] = domain.QuotaWindow{
			Status:  "ok",
			Percent: usedUsd / limit * 100,
			Used:    usedUsd,
			Cap:     limit,
		}
	}
	return out, nil
}

// usagePathFor 由 subscription 路径推 usage 路径(同前缀改名)。
func usagePathFor(path string) (string, bool) {
	i := strings.LastIndex(path, "subscription")
	if i < 0 {
		return "", false
	}
	return path[:i] + "usage" + path[i+len("subscription"):], true
}

// ValidateQuotaPath 校验第三方额度路径。**安全红线**:额度请求会带上该渠道的 Bearer 密钥,
// 所以路径只能是一个**路径** —— 绝不能因为一个自由文本字段把密钥送到其它域名(SSRF / 密钥外泄)。
// 空串合法(表示「未配置」)。返回 nil 表示可用。
func ValidateQuotaPath(path string) error {
	if path == "" {
		return nil
	}
	if len(path) > 512 {
		return errors.New("额度路径过长(上限 512)")
	}
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return errors.New("额度路径必须以单个 / 开头")
	}
	if strings.Contains(path, "://") || strings.Contains(path, "\\") {
		return errors.New("额度路径只能是路径,不能包含协议或域名")
	}
	for _, r := range path {
		if r <= ' ' || r == 0x7f {
			return errors.New("额度路径不能包含空白或控制字符")
		}
	}
	u, err := url.Parse(path)
	if err != nil || u.Host != "" || u.Scheme != "" {
		return errors.New("额度路径只能是路径,不能包含协议或域名")
	}
	return nil
}

// fetchNewAPIUserQuota new-api /api/user/self → {data:{quota(剩余),used_quota(已用)}}。
// 额度单位制(默认 500000 = 1 USD),无币种信息,按 USD 呈现。
func fetchNewAPIUserQuota(ctx context.Context, client *http.Client, base, path, key string) (QuotaResult, error) {
	body, lat, err := quotaDo(ctx, client, base+path, key)
	if err != nil {
		return QuotaResult{}, err
	}
	if err := ensureJSON(body); err != nil {
		return QuotaResult{}, err
	}
	if msg := relayErrorBody(body); msg != "" {
		return QuotaResult{LatencyMs: lat}, fmt.Errorf("upstream: %s", msg)
	}
	var resp struct {
		Data struct {
			Quota     float64 `json:"quota"`
			UsedQuota float64 `json:"used_quota"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return QuotaResult{}, fmt.Errorf("cannot parse /api/user/self: %w", err)
	}
	remaining := resp.Data.Quota / newAPIQuotaPerUnit
	used := resp.Data.UsedQuota / newAPIQuotaPerUnit
	out := QuotaResult{
		LatencyMs: lat,
		Balance:   &domain.QuotaBalance{Amount: remaining, Currency: string(domain.CurrencyUSD)},
		Windows:   map[string]domain.QuotaWindow{},
	}
	if total := remaining + used; total > 0 {
		out.Windows["monthly"] = domain.QuotaWindow{
			Status:  "ok",
			Percent: used / total * 100,
			Used:    used,
			Cap:     total,
		}
	}
	return out, nil
}
