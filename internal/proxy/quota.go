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
	"strings"
	"time"

	"personal-ai-gateway/internal/domain"
	"personal-ai-gateway/internal/secret"
)

// quotaTimeout 渠道额度查询总超时(额度仅是展示信息,比探测更短)。
const quotaTimeout = 6 * time.Second

var (
	// ErrQuotaUnsupported 该渠道类型没有已知的额度接口。
	ErrQuotaUnsupported = errors.New("channel type has no known quota endpoint")
	// ErrQuotaNotConfigured 第三方渠道尚未手工配置额度查询路径。
	ErrQuotaNotConfigured = errors.New("third-party channel has no quota path configured")
)

// QuotaResult 一次额度查询的结果。窗口型上游(rolling/weekly/monthly)填 Windows,
// 余额型上游(DeepSeek /user/balance、one-api)填 Balance;两者可同时有。
type QuotaResult struct {
	PlanName  string
	Windows   map[string]domain.QuotaWindow
	Balance   *domain.QuotaBalance
	LatencyMs int64
}

// FetchChannelQuota 按渠道类型分派到对应的上游额度协议。各家问法与形状完全不同,
// 故一类一个实现(quota_*.go),此处只做分派。空类型按第三方处理(存量库回填前的兜底)。
func (r *Relay) FetchChannelQuota(ctx context.Context, client *http.Client, ch domain.ChannelRow) (QuotaResult, error) {
	switch ch.ChannelType {
	case domain.ChannelTypeCommandCode:
		return fetchCommandCodeQuota(ctx, client, ch)
	case domain.ChannelTypeOpenCode:
		return fetchOpenCodeQuota(ctx, client, ch)
	case domain.ChannelTypeDeepSeek:
		return fetchDeepSeekQuota(ctx, client, ch)
	case domain.ChannelTypeThirdParty, "":
		return fetchThirdPartyQuota(ctx, client, ch)
	default:
		return QuotaResult{}, ErrQuotaUnsupported
	}
}

// ---------- 共享协议件 ----------

// usageEnvelope 上游 /v1/usage 响应外壳(opencode zen 与既有 OpenAI 协议渠道共用)。
// 字段各家不完全一致:planName 兼容 planName/plan_name/plan(可选),窗口统一在 usage.{rolling,weekly,monthly}。
type usageEnvelope struct {
	PlanName string `json:"planName"`
	Usage    struct {
		Rolling json.RawMessage `json:"rolling"`
		Weekly  json.RawMessage `json:"weekly"`
		Monthly json.RawMessage `json:"monthly"`
	} `json:"usage"`
}

// quotaWindowJSON 单窗口原始形状。percent 容错字符串(上游偶发引号包裹);
// resetsAt 容错 epoch 秒/毫秒 或 RFC3339 字符串(各家不一,统一归一为 RFC3339)。
type quotaWindowJSON struct {
	Status   string `json:"status"`
	Percent  any    `json:"percent"`
	ResetsAt any    `json:"resetsAt"`
}

// windowsFromUsage 把 usage.{rolling,weekly,monthly} 解析成窗口表(status!=ok 视为无该窗口)。
func windowsFromUsage(env usageEnvelope) map[string]domain.QuotaWindow {
	windows := map[string]domain.QuotaWindow{}
	for key, raw := range map[string]json.RawMessage{
		"rolling": env.Usage.Rolling,
		"weekly":  env.Usage.Weekly,
		"monthly": env.Usage.Monthly,
	} {
		if w, ok := parseQuotaWindow(raw); ok {
			windows[key] = w
		}
	}
	return windows
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
	return domain.QuotaWindow{Status: "ok", Percent: pct, ResetAt: normalizeResetAt(w.ResetsAt)}, true
}

// normalizeResetAt 把上游各种重置时间写法归一为 UTC RFC3339;识别不出则返回空串。
func normalizeResetAt(v any) string {
	switch t := v.(type) {
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return ""
		}
		if ts, err := time.Parse(time.RFC3339, s); err == nil {
			return ts.UTC().Format(time.RFC3339)
		}
		if ms, err := strconv.ParseInt(s, 10, 64); err == nil {
			return epochToRFC3339(ms)
		}
	case float64:
		return epochToRFC3339(int64(t))
	}
	return ""
}

// epochToRFC3339 十位当秒、十三位当毫秒(上游有的给秒有的给毫秒)。
func epochToRFC3339(n int64) string {
	if n <= 0 {
		return ""
	}
	if n < 1e11 { // 毫秒时间戳都在 1e12 以上,小于即视为秒
		return time.Unix(n, 0).UTC().Format(time.RFC3339)
	}
	return time.UnixMilli(n).UTC().Format(time.RFC3339)
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

// quotaKey 解密渠道密钥(额度查询与转发同源密钥)。
func quotaKey(ch domain.ChannelRow) (string, error) {
	key, err := secret.Decrypt(ch.APIKeyCipher)
	if err != nil {
		return "", fmt.Errorf("decrypt channel key: %w", err)
	}
	return key, nil
}

// quotaDo 发一次带 Bearer 的只读 GET,返回正文与耗时。非 2xx → 错误(附截断正文)。
func quotaDo(ctx context.Context, client *http.Client, url, key string) ([]byte, int64, error) {
	reqCtx, cancel := context.WithTimeout(ctx, quotaTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("User-Agent", "cc-switch/1.0")
	req.Header.Set("Accept", "application/json")

	t0 := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	lat := time.Since(t0).Milliseconds()
	body, rerr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if rerr != nil {
		return nil, lat, rerr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, lat, fmt.Errorf("upstream %s: %s", resp.Status, snippet(body))
	}
	return body, lat, nil
}

// ensureJSON 上游在路由不存在时可能回 200 的 SPA HTML —— 那不是额度数据,必须判失败。
func ensureJSON(body []byte) error {
	t := bytes.TrimSpace(body)
	if len(t) == 0 || t[0] != '{' {
		return errors.New("上游未返回 JSON(额度路径可能不存在)")
	}
	return nil
}

// relayErrorBody 多数中转站(one-api/new-api 等)查询失败时仍回 200,正文里带 error 字段。
// 返回非空字符串即表示这是错误响应,内容为错误提示。
func relayErrorBody(body []byte) string {
	var env struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return ""
	}
	if len(env.Error) > 0 && string(env.Error) != "null" {
		var s string
		if json.Unmarshal(env.Error, &s) == nil && s != "" {
			return s
		}
		var m map[string]any
		if json.Unmarshal(env.Error, &m) == nil {
			if msg, ok := m["message"].(string); ok && msg != "" {
				return msg
			}
			return string(env.Error)
		}
	}
	return env.Message
}

// snippet 截断上游正文用于错误提示。
func snippet(body []byte) string {
	s := bytes.TrimSpace(body)
	if len(s) > 200 {
		s = s[:200]
	}
	return string(s)
}
