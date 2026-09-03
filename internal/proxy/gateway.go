// Package proxy 是网关数据面:接收一个已鉴权的模型请求,路由到上游并转发。
// P1 只做"同协议透传"(anthropic↔anthropic / openai↔openai),带 failover 与熔断;
// 跨协议翻译(a2o)后续在 internal/proxy/translate 实施。
package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"personal-ai-gateway/internal/pricing"
	"personal-ai-gateway/internal/router"
	"personal-ai-gateway/internal/sess"
	"personal-ai-gateway/internal/store"
)

const (
	ProtoAnthropic = "anthropic"
	ProtoOpenAI    = "openai"
)

// op 标识入站要执行的操作,由 server 按 path 判定。
const (
	OpMessages    = "messages"
	OpChat        = "chat"
	OpCountTokens = "count_tokens"
)

const maxBody = 64 << 20 // 64MB:足够容纳长提示词+工具定义,同时防超限

type Gateway struct {
	Router *router.Router
	Store  *store.Store
	Price  *pricing.Resolver
	Logger *slog.Logger
}

func New(r *router.Router, s *store.Store, price *pricing.Resolver) *Gateway {
	if price == nil {
		price = pricing.New(nil)
	}
	return &Gateway{Router: r, Store: s, Price: price, Logger: slog.Default()}
}

// Relay 处理一次模型请求(inProto/op 由 server 依据路径给出)。
// 返回前保证已向客户端写完整响应(成功流 / 透传错误 / 汇总错误)。
func (g *Gateway) Relay(w http.ResponseWriter, r *http.Request, inProto, op string) {
	ctx := r.Context()
	info, _ := sess.From(ctx)
	start := time.Now()

	ent := store.LogEntry{
		TS:         start,
		ClientKey:  info.KeyName,
		ClientTool: info.Tool,
		Protocol:   inProto,
	}
	defer func() {
		ent.LatencyMs = time.Since(start).Milliseconds()
		if g.Store != nil {
			if err := g.Store.Log(ent); err != nil {
				g.Logger.Warn("store log failed", "err", err)
			}
		}
	}()

	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
	if err != nil {
		ent.Status = http.StatusBadRequest
		ent.Err = err.Error()
		WriteError(w, inProto, ent.Status, "invalid_request", "read body: "+err.Error())
		return
	}

	var probe struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	_ = json.Unmarshal(body, &probe) // 解析失败按 model="" 处理,由下面校验兜底
	ent.Model = probe.Model
	ent.Stream = probe.Stream

	if probe.Model == "" {
		ent.Status = http.StatusBadRequest
		ent.Err = "missing model"
		WriteError(w, inProto, ent.Status, "invalid_request", "request body must include a model")
		return
	}

	cands := g.Router.Candidates(probe.Model)
	if len(cands) == 0 {
		if g.Router.HasAny(probe.Model) {
			// 有上游能出该模型,但都是异协议 → P1 跨协议翻译未实施,给明确提示
			ent.Status = http.StatusNotImplemented
			ent.Err = "cross-protocol translation not implemented"
			WriteError(w, inProto, ent.Status, "api_error",
				fmt.Sprintf("model %q is only served by upstreams of a different protocol "+
					"(cross-protocol translation lands in a later milestone)", probe.Model))
		} else {
			ent.Status = http.StatusNotFound
			ent.Err = "model unavailable"
			WriteError(w, inProto, ent.Status, "not_found_error",
				fmt.Sprintf("model %q is not available on any configured upstream", probe.Model))
		}
		return
	}

	var lastErr error
	for _, up := range cands {
		ent.Upstream = up.Name
		if up.Type != inProto {
			// 跨协议候选:P1 直接跳过,不改熔断状态
			lastErr = fmt.Errorf("upstream %s speaks %s but client speaks %s (translation pending)",
				up.Name, up.Type, inProto)
			continue
		}

		handled, status, err, tok := g.tryRelay(ctx, w, r, body, up, op, probe.Stream)
		if err == nil {
			g.Router.RecordSuccess(up.Name)
			ent.PromptTokens = tok.prompt
			ent.CompletionTokens = tok.completion
			ent.CacheReadTokens = tok.cacheRead
			ent.Cost = g.Price.Price(probe.Model).Cost(tok.prompt, tok.completion, tok.cacheRead)
			ent.Status = status
			g.Logger.Debug("relay ok", "upstream", up.Name, "model", probe.Model, "status", status,
				"prompt", tok.prompt, "completion", tok.completion, "cache_read", tok.cacheRead)
			return
		}
		if handled {
			// 已向客户端写出最终响应(上游 4xx 透传 / 流式中途断开):不再重试
			ent.Status = status
			ent.Err = err.Error()
			g.Logger.Warn("relay ended", "upstream", up.Name, "model", probe.Model, "err", err)
			return
		}
		// 未写出任何响应且可重试 → 记失败,换下一个候选
		g.Router.RecordFailure(up)
		lastErr = err
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("no candidate reached")
	}
	ent.Status = http.StatusBadGateway
	ent.Err = lastErr.Error()
	g.Logger.Warn("all upstreams failed", "model", probe.Model, "err", lastErr)
	WriteError(w, inProto, ent.Status, "api_error",
		fmt.Sprintf("all upstreams failed for %q: %v", probe.Model, lastErr))
}
