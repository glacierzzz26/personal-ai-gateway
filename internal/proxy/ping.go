package proxy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"personal-ai-gateway/internal/config"
)

// PingResult 是一次连通性探测的结论。
type PingResult struct {
	Reachable bool   `json:"reachable"`
	Status    int    `json:"status,omitempty"` // 上游返回的 HTTP 状态(可达时)
	Message   string `json:"message"`
}

// PingUpstream 验证某上游的 base_url + api_key 是否真的可用,不打模型请求不耗配额:
// GET {models} 列表 —— openai 型 base(含 /v1)+ /models,anthropic 型根 + /v1/models。
// 结论按对选路最有用的口径分类:
//   2xx           → 可达且 key 有效;
//   401/403       → 可达但 key 被上游拒绝;
//   其它状态      → 可达(HTTP n),端点行为待观察;
//   连接失败/超时 → 不可达。
func PingUpstream(ctx context.Context, up config.Upstream, timeout time.Duration) PingResult {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	base := strings.TrimRight(up.BaseURL, "/")
	path := "/models"
	if up.Type == config.TypeAnthropic {
		path = "/v1/models"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return PingResult{Message: "build request: " + err.Error()}
	}
	req.Header.Set("Accept", "application/json")
	if up.Type == config.TypeAnthropic {
		req.Header.Set("x-api-key", up.APIKey)
		req.Header.Set("anthropic-version", "2023-06-01")
	} else {
		req.Header.Set("Authorization", "Bearer "+up.APIKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return PingResult{Message: "unreachable: " + err.Error()}
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 16<<10))

	code := resp.StatusCode
	switch {
	case code >= 200 && code < 300:
		return PingResult{Reachable: true, Status: code, Message: "ok"}
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return PingResult{Reachable: true, Status: code,
			Message: fmt.Sprintf("reachable but api key rejected (HTTP %d)", code)}
	default:
		return PingResult{Reachable: true, Status: code,
			Message: fmt.Sprintf("reachable (HTTP %d)", code)}
	}
}
