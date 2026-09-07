package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"personal-ai-gateway/internal/domain"
	"personal-ai-gateway/internal/secret"
)

// quotaTimeout 渠道额度查询总超时(额度仅是展示信息,比探测更短)。
const quotaTimeout = 6 * time.Second

// ErrQuotaUnsupported 渠道协议无 /v1/usage 额度接口(当前只有 Anthropic)。
var ErrQuotaUnsupported = errors.New("channel provider has no /v1/usage quota endpoint")

// usageEnvelope 上游 /v1/usage 响应外壳。字段各家不完全一致:
// planName 兼容 planName/plan_name/plan(可选),窗口统一在 usage.{rolling,weekly,monthly}。
type usageEnvelope struct {
	PlanName string `json:"planName"`
	Usage    struct {
		Rolling json.RawMessage `json:"rolling"`
		Weekly  json.RawMessage `json:"weekly"`
		Monthly json.RawMessage `json:"monthly"`
	} `json:"usage"`
}

// quotaWindowJSON 单窗口原始形状。percent 容错字符串(上游偶发引号包裹)。
type quotaWindowJSON struct {
	Status  string `json:"status"`
	Percent any    `json:"percent"`
}

// FetchChannelQuota 拉渠道 /v1/usage 额度:rolling≈近5h / weekly / monthly。
// 仅 OpenAI 协议渠道执行(Anthropic 判 ErrQuotaUnsupported)。返回各 status=ok 的
// 窗口已用百分比(缺失/非 ok 窗口被略去,即"该窗口不提供额度")、可选 planName 与耗时。
func (r *Relay) FetchChannelQuota(ctx context.Context, client *http.Client, ch domain.ChannelRow) (planName string, windows map[string]domain.QuotaWindow, latencyMs int64, err error) {
	if OutProto(ch.Provider) != ProtoOpenAI {
		return "", nil, 0, ErrQuotaUnsupported
	}
	key, err := secret.Decrypt(ch.APIKeyCipher)
	if err != nil {
		return "", nil, 0, fmt.Errorf("decrypt channel key: %w", err)
	}
	quotaCtx, cancel := context.WithTimeout(ctx, quotaTimeout)
	defer cancel()
	req, err := http.NewRequest(http.MethodGet, apiRoot(ch.BaseURL)+"/v1/usage", nil)
	if err != nil {
		return "", nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("User-Agent", "cc-switch/1.0")
	req.Header.Set("Accept", "application/json")

	t0 := time.Now()
	resp, err := client.Do(req.WithContext(quotaCtx))
	if err != nil {
		return "", nil, 0, err
	}
	defer resp.Body.Close()
	latencyMs = time.Since(t0).Milliseconds()
	body, rerr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if rerr != nil {
		return "", nil, latencyMs, rerr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet := bytes.TrimSpace(body)
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return "", nil, latencyMs, fmt.Errorf("upstream %s: %s", resp.Status, snippet)
	}
	var env usageEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return "", nil, latencyMs, fmt.Errorf("cannot parse /v1/usage response: %w", err)
	}
	windows = map[string]domain.QuotaWindow{}
	for key, raw := range map[string]json.RawMessage{
		"rolling": env.Usage.Rolling,
		"weekly":  env.Usage.Weekly,
		"monthly": env.Usage.Monthly,
	} {
		if w, ok := parseQuotaWindow(raw); ok {
			windows[key] = w
		}
	}
	return env.PlanName, windows, latencyMs, nil
}

// parseQuotaWindow 解析单窗口;status!="ok" 或 percent 非法视为"无该窗口"。
func parseQuotaWindow(raw json.RawMessage) (domain.QuotaWindow, bool) {
	var w quotaWindowJSON
	if err := json.Unmarshal(raw, &w); err != nil {
		return domain.QuotaWindow{}, false
	}
	if w.Status != "ok" {
		return domain.QuotaWindow{}, false
	}
	pct, err := toPercent(w.Percent)
	if err != nil || pct < 0 {
		return domain.QuotaWindow{}, false
	}
	return domain.QuotaWindow{Status: "ok", Percent: pct}, true
}

func toPercent(v any) (float64, error) {
	switch n := v.(type) {
	case float64:
		return n, nil
	case string:
		return strconv.ParseFloat(n, 64)
	}
	return 0, fmt.Errorf("percent not a number: %v", v)
}
