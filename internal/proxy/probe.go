package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"personal-ai-gateway/internal/domain"
	"personal-ai-gateway/internal/secret"
)

// probeTimeout 渠道探测(测试/同步)总超时。
const probeTimeout = 8 * time.Second

// channelProbeReq 按渠道协议拼 models 探测请求(复用处与 /v1/models 一致)。
func channelProbeReq(ch domain.ChannelRow) (*http.Request, error) {
	key, err := secret.Decrypt(ch.APIKeyCipher)
	if err != nil {
		return nil, fmt.Errorf("decrypt channel key: %w", err)
	}
	req, err := http.NewRequest(http.MethodGet, apiRoot(ch.BaseURL)+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	if OutProto(ch.Provider) == ProtoAnthropic {
		req.Header.Set("x-api-key", key)
		req.Header.Set("anthropic-version", "2023-06-01")
	} else {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	return req, nil
}

// probeDo 执行探测请求并解析 data[].id 模型清单(兼容 anthropic/openai 双形状)。
// 返回:模型 id、延迟、错误(非 2xx 或网络失败时错误非 nil)。
func probeDo(ctx context.Context, client *http.Client, ch domain.ChannelRow) (ids []string, latencyMs int64, err error) {
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	req, err := channelProbeReq(ch)
	if err != nil {
		return nil, 0, err
	}
	t0 := time.Now()
	resp, err := client.Do(req.WithContext(probeCtx))
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	latencyMs = time.Since(t0).Milliseconds()
	body, rerr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if rerr != nil {
		return nil, latencyMs, rerr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet := bytes.TrimSpace(body)
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return nil, latencyMs, fmt.Errorf("upstream %s: %s", resp.Status, snippet)
	}
	var envelope struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, latencyMs, fmt.Errorf("cannot parse /v1/models response: %w", err)
	}
	for _, m := range envelope.Data {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	return ids, latencyMs, nil
}

// Probe 渠道连通测试(管理端「测试」按钮)。
func (r *Relay) Probe(ctx context.Context, client *http.Client, ch domain.ChannelRow) (latencyMs int64, err error) {
	_, latencyMs, err = probeDo(ctx, client, ch)
	return latencyMs, err
}

// FetchModels 拉渠道 /v1/models 模型清单(管理端「从渠道同步」)。
func (r *Relay) FetchModels(ctx context.Context, client *http.Client, ch domain.ChannelRow) (ids []string, latencyMs int64, err error) {
	return probeDo(ctx, client, ch)
}
