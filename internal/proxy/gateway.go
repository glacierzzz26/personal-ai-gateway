// Package proxy 是网关数据面:接收一个已鉴权的模型请求,路由到上游并转发。
// 同协议走逐字节透传 fast path;跨协议(anthropic→openai,a2o)走 internal/proxy/translate,
// 两者都带 failover 与熔断(候选循环里并行分支,见 tryRelay / tryRelayTranslate)。
package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"personal-ai-gateway/internal/pricing"
	"personal-ai-gateway/internal/proxy/translate"
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
			// 有上游能出该模型但候选为空 → 熔断中/冷启动,与协议无关
			ent.Status = http.StatusServiceUnavailable
			ent.Err = "no candidate available"
			WriteError(w, inProto, ent.Status, "api_error",
				fmt.Sprintf("model %q has configured upstreams but none is currently available "+
					"(circuit open or warming up)", probe.Model))
		} else {
			ent.Status = http.StatusNotFound
			ent.Err = "model unavailable"
			WriteError(w, inProto, ent.Status, "not_found_error",
				fmt.Sprintf("model %q is not available on any configured upstream", probe.Model))
		}
		return
	}

	// count_tokens 特判:anthropic 腿才有准确计数端点;模型只由 openai 型上游出时本地估算
	// (启发式,非计量;估算值不落 request_log 的 token 列)。anthropic 腿存在则照常走循环透传。
	if op == OpCountTokens {
		hasAnthropic := false
		for _, up := range cands {
			if up.Type == ProtoAnthropic {
				hasAnthropic = true
				break
			}
		}
		if !hasAnthropic {
			est := translate.EstimateMessagesInput(body)
			ent.Status = http.StatusOK
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"input_tokens": est})
			g.Logger.Debug("count_tokens: local estimate (openai-only upstreams)",
				"model", probe.Model, "input_tokens", est)
			return
		}
	}

	var lastErr error
	for _, up := range cands {
		ent.Upstream = up.Name
		cross := up.Type != inProto
		// 跨协议翻译目前只覆盖「messages op、anthropic→openai」;其余(op 不支持 / o2a)跳过。
		translatable := op == OpMessages && translate.Supported(inProto, up.Type)
		if cross && !translatable {
			lastErr = fmt.Errorf("upstream %s speaks %s but client speaks %s (no translation for op %q)",
				up.Name, up.Type, inProto, op)
			continue
		}

		var handled bool
		var status int
		var err error
		var tok usage
		if cross {
			handled, status, err, tok = g.tryRelayTranslate(ctx, inProto, probe.Model, w, r, body, up, op, probe.Stream)
		} else {
			handled, status, err, tok = g.tryRelay(ctx, w, r, body, up, op, probe.Stream)
		}
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
			// 已向客户端写出最终响应(上游 4xx 重编码 / 翻译路径已写 / 流式中途断开):不再重试
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
