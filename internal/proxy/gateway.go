// Package proxy 承载数据面(model-plane):/v1 全部端点。
//
// 职责:令牌鉴权(RPM/额度/有效期/允许模型)→ 引擎选路(目录+规则+熔断)
// → 出站按候选逐一执行(重试/翻译/流式)→ 计费(offer 单价)+ 落账 + 令牌扣额。
// 管理面(/api)见 internal/server。
package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"personal-ai-gateway/internal/auth"
	"personal-ai-gateway/internal/domain"
	"personal-ai-gateway/internal/engine"
	"personal-ai-gateway/internal/proxy/translate"
	"personal-ai-gateway/internal/store"
)

// Gateway 模型面处理器(挂载到 server 的 /v1/*)。
type Gateway struct {
	st  *store.Store
	eng *engine.Engine
	rl  *Relay
	log *slog.Logger

	rpm   sync.Map // tokenID → *rpmWindow
	nowFn func() time.Time
}

func NewGateway(st *store.Store, eng *engine.Engine, rl *Relay) *Gateway {
	return &Gateway{st: st, eng: eng, rl: rl, log: slog.Default(), nowFn: time.Now}
}

// handler 方法
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
		g.handleListModels(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/messages":
		g.forwardMessages(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
		g.forwardChat(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/messages/count_tokens":
		g.handleCountTokens(w, r)
	default:
		gateError(w, ProtoOpenAI, http.StatusNotFound, "not_found_error", "no such model endpoint")
	}
}

// —— 入站解析 ——

// inboundReq 一次 /v1 业务请求的收敛信息。
type inboundReq struct {
	op      string // messages | chat | count_tokens
	inProto string // anthropic | openai
	body    []byte
	model   string
	stream  bool
	ip      string
	tool    string
	token   domain.TokenRow
}

// parseModelStream 从 body 取 model/stream(两种协议都这两个顶层字段)。
func parseModelStream(body []byte) (model string, stream bool, err error) {
	var probe struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return "", false, err
	}
	return probe.Model, probe.Stream, nil
}

// parseInbound 做鉴权与模型解析;失败时已写入错误响应。
func (g *Gateway) parseInbound(w http.ResponseWriter, r *http.Request, op string) (*inboundReq, bool) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil {
		gateError(w, inboundProtoOf(op), http.StatusBadRequest, "invalid_request_error", "read body: "+err.Error())
		return nil, false
	}
	in := &inboundReq{op: op, inProto: inboundProtoOf(op), body: body, ip: clientIP(r), tool: clientTool(r)}

	secretKey := extractSecret(r)
	if secretKey == "" {
		gateError(w, in.inProto, http.StatusUnauthorized, "authentication_error", "missing api key (x-api-key or Authorization: Bearer)")
		return nil, false
	}
	token, err := g.st.LookupTokenBySHA256(auth.HashSecret(secretKey))
	if err != nil {
		gateError(w, in.inProto, http.StatusUnauthorized, "authentication_error", "invalid api key")
		return nil, false
	}
	if code, typ, msg := tokenGateErr(g.nowFn(), token); code != 0 {
		gateError(w, in.inProto, code, typ, msg)
		return nil, false
	}
	if !g.rpmAllow(token.ID, token.RpmLimit) {
		w.Header().Set("Retry-After", "1")
		gateError(w, in.inProto, http.StatusTooManyRequests, "rate_limit_error", "token rate limit exceeded")
		return nil, false
	}
	in.token = token

	if op != countOp {
		model, stream, err := parseModelStream(body)
		if err != nil || model == "" {
			gateError(w, in.inProto, http.StatusBadRequest, "invalid_request_error", "request body must include a model")
			return nil, false
		}
		if !engine.SupportsModel(token.AllowedModels, model) {
			gateError(w, in.inProto, http.StatusForbidden, "permission_error", "model "+model+" is not allowed for this token")
			return nil, false
		}
		in.model = model
		in.stream = stream
	}
	return in, true
}

// tokenGateErr 令牌硬校验:0=通过;否则返回客户端错误(401 disabled/过期,402 额度)。
func tokenGateErr(now time.Time, t domain.TokenRow) (int, string, string) {
	switch t.Status {
	case domain.TokenDisabled:
		return http.StatusUnauthorized, "authentication_error", "token is disabled"
	case domain.TokenExpired:
		return http.StatusUnauthorized, "authentication_error", "token is expired"
	}
	if t.ExpiresAt != nil && *t.ExpiresAt != "" {
		if tm, ok := parseDate(*t.ExpiresAt); ok && now.After(tm) {
			return http.StatusUnauthorized, "authentication_error", "token is expired"
		}
	}
	if t.QuotaUsd > 0 && t.UsedUsd >= t.QuotaUsd {
		return http.StatusPaymentRequired, "quota_exceeded", "token quota exhausted"
	}
	return 0, "", ""
}

func parseDate(s string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// rpmAllow 令牌级滑动窗口限速(60s)。
func (g *Gateway) rpmAllow(id int64, limit int) bool {
	if limit <= 0 {
		return true
	}
	now := g.nowFn()
	v, _ := g.rpm.LoadOrStore(id, &rpmWindow{})
	w := v.(*rpmWindow)
	return w.allow(now, limit)
}

type rpmWindow struct {
	mu sync.Mutex
	t  []time.Time
}

func (w *rpmWindow) allow(now time.Time, limit int) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	cut := now.Add(-time.Minute)
	kept := w.t[:0]
	for _, x := range w.t {
		if x.After(cut) {
			kept = append(kept, x)
		}
	}
	w.t = kept
	if len(w.t) >= limit {
		return false
	}
	w.t = append(w.t, now)
	return true
}

func inboundProtoOf(op string) string {
	if op == countOp {
		return ProtoAnthropic
	}
	if op == messagesOp {
		return ProtoAnthropic
	}
	return ProtoOpenAI
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i > 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	host := r.RemoteAddr
	if i := strings.LastIndexByte(host, ':'); i > 0 {
		return host[:i]
	}
	return host
}

// clientTool 识别常见客户端,便于日志/用量归因。
func clientTool(r *http.Request) string {
	ua := r.Header.Get("User-Agent")
	switch {
	case strings.Contains(ua, "ClaudeCode") || strings.Contains(ua, "claude-code"):
		return "claude-code"
	case strings.Contains(ua, "opencode"):
		return "opencode"
	case strings.Contains(ua, "openai"):
		return "openai-sdk"
	case strings.Contains(ua, "curl"):
		return "curl"
	}
	if v := r.Header.Get("x-stainless-package-version"); v != "" {
		return "anthropic-sdk"
	}
	if ua == "" {
		return "unknown"
	}
	if len(ua) > 40 {
		ua = ua[:40]
	}
	return ua
}

func extractSecret(r *http.Request) string {
	if k := strings.TrimSpace(r.Header.Get("x-api-key")); k != "" {
		return k
	}
	authz := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(authz, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(authz, "Bearer "))
	}
	return ""
}

// —— 端点 ——

func (g *Gateway) forwardMessages(w http.ResponseWriter, r *http.Request) {
	g.forward(w, r, messagesOp, ProtoAnthropic)
}

func (g *Gateway) forwardChat(w http.ResponseWriter, r *http.Request) {
	g.forward(w, r, chatOp, ProtoOpenAI)
}

func (g *Gateway) forward(w http.ResponseWriter, r *http.Request, op, inProto string) {
	in, ok := g.parseInbound(w, r, op)
	if !ok {
		return
	}
	settings, err := g.st.GetSettings()
	if err != nil {
		gateError(w, inProto, http.StatusInternalServerError, "api_error", err.Error())
		return
	}

	plan, err := g.eng.Evaluate(in.model)
	if err != nil {
		if errors.Is(err, engine.ErrModelUnavailable) {
			gateError(w, inProto, http.StatusNotFound, modelNotFoundType(inProto), "model "+in.model+" is not available (not in catalog / disabled / no enabled offer)")
		} else {
			gateError(w, inProto, http.StatusInternalServerError, "api_error", err.Error())
		}
		return
	}
	if plan.MatchedID > 0 {
		_ = g.st.HitRule(plan.MatchedID)
	}
	if !settings.DegradeOnError && len(plan.Attempts) > 1 {
		plan.Attempts = plan.Attempts[:1] // 关闭自动降级:只用首个候选
	}
	plan.Attempts = attemptSequence(plan.Attempts, plan.Retry)

	if in.stream {
		g.forwardStream(w, r, in, plan, settings, inProto)
	} else {
		g.forwardOnceNonStream(w, r, in, plan, settings, inProto)
	}
}

// attemptSequence 按重试轮数展开候选:一轮走完所有候选后,若配置了 retry,再重复若干轮
// (失败才继续,成功即 return;非可重试错误 break)。Retry<=0 原样返回;上限 10 轮防病态配置。
func attemptSequence(attempts []engine.Attempt, retry int) []engine.Attempt {
	if retry <= 0 || len(attempts) == 0 {
		return attempts
	}
	if retry > 10 {
		retry = 10
	}
	out := make([]engine.Attempt, 0, len(attempts)*(1+retry))
	for i := 0; i <= retry; i++ {
		out = append(out, attempts...)
	}
	return out
}

// attemptEnv 取候选渠道行(出站要用 provider/base_url/密钥)。
func (g *Gateway) attemptEnv(at engine.Attempt) (domain.ChannelRow, bool) {
	ch, err := g.st.GetChannel(at.Offer.ChannelID)
	return ch, err == nil
}

// —— 非流 ——

func (g *Gateway) forwardOnceNonStream(w http.ResponseWriter, r *http.Request, in *inboundReq, plan *engine.Plan, settings domain.Settings, inProto string) {
	start := time.Now()
	var firstErr *attemptResult // 兜底展示(保留首个错误)
	for _, at := range plan.Attempts {
		ch, ok := g.attemptEnv(at)
		if !ok {
			continue
		}
		outProto := OutProto(ch.Provider)
		ob, err := buildOutbound(ch, inProto, outProto, in.op, in.body, false)
		if err != nil {
			gateError(w, inProto, http.StatusBadRequest, "invalid_request_error", "cannot build request: "+err.Error())
			return
		}
		client := g.rl.Client(settings, at.TimeoutMs)
		res, err := g.rl.doNonStream(r.Context(), client, ob, ch, at.Offer, at.TimeoutMs)
		if err != nil {
			g.eng.RecordFailure(ch.ID, ch.MaxFailures, ch.CooldownSec)
			continue
		}
		if res.upErr != "" {
			if clientGone(r.Context(), nil) {
				g.logDisconnect(in, ch, at.Offer, time.Since(start).Milliseconds(), nil)
				return
			}
			g.eng.RecordFailure(ch.ID, ch.MaxFailures, ch.CooldownSec)
			firstErr = res
			continue
		}
		if res.status < 200 || res.status >= 300 {
			if firstErr == nil {
				firstErr = res
			}
			g.eng.RecordFailure(ch.ID, ch.MaxFailures, ch.CooldownSec)
			if !retryableHTTP(res.status) {
				break // 400/422 属请求自身问题,换渠道无益
			}
			continue
		}
		// 成功
		g.eng.RecordSuccess(ch.ID, res.ttfbMs)
		var tok translate.Usage
		outBody := res.body
		if inProto == outProto {
			u := usage{}
			if outProto == ProtoAnthropic {
				u = parseAnthropicUsage(res.body)
			} else {
				u = parseOpenAIUsage(res.body)
			}
			tok = translate.Usage{Prompt: u.prompt, Completion: u.completion, CacheRead: u.cacheRead}
		} else {
			var convErr error
			outBody, tok, convErr = translate.ConvertNonStream(inProto, outProto, res.body)
			if convErr != nil {
				// 网关自身的跨协议翻译失败(上游响应形状意外):不是渠道故障,不计入渠道健康/熔断
				// (否则一个翻译 bug 会把健康渠道打成 down)。记为首个错误供全败兜底,
				// 状态用 502 以免把上游 2xx 误当成功回给客户端。
				firstErr = &attemptResult{
					channel: res.channel, offer: res.offer, status: http.StatusBadGateway,
					body: []byte("response translation failed: " + convErr.Error()),
				}
				continue
			}
		}
		g.finish(w, r, in, res.latencyMs, res.status, outBody, ch, at.Offer, tok, start)
		return
	}
	// 全候选失败:回错误前也落一条失败账(供用量/错误率/渠道健康统计)。
	total := time.Since(start).Milliseconds()
	if firstErr == nil {
		gateError(w, inProto, http.StatusBadGateway, "api_error", "all upstream channels failed")
		g.logFailure(in, domain.ChannelRow{}, domain.OfferRead{}, http.StatusBadGateway,
			total, nil, ptrStr("all upstream channels failed"))
		return
	}
	if firstErr.upErr != "" {
		gateError(w, inProto, http.StatusGatewayTimeout, "api_error", "upstream unavailable: "+firstErr.upErr)
		g.logFailure(in, firstErr.channel, firstErr.offer, http.StatusGatewayTimeout,
			total, nil, ptrStr("upstream unavailable: "+firstErr.upErr))
		return
	}
	writeTranslatedError(w, inProto, OutProto(firstErr.channel.Provider), firstErr.status, firstErr.body)
	g.logFailure(in, firstErr.channel, firstErr.offer, firstErr.status,
		total, &firstErr.latencyMs, upstreamMsg(firstErr.status, firstErr.body))
}

// finish 落账+回写:非流成功路径。
func (g *Gateway) finish(w http.ResponseWriter, r *http.Request, in *inboundReq, latencyMs int64, status int, outBody []byte, ch domain.ChannelRow, offer domain.OfferRead, tok translate.Usage, start time.Time) {
	cost := costUsd(offer, tok)
	_ = g.st.ChargeToken(in.token.ID, cost)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(outBody)
	total := time.Since(start).Milliseconds()
	g.writeLog(in, ch, offer, status, tok, latencyMs, total, nil)
}

// —— 流式 ——

func (g *Gateway) forwardStream(w http.ResponseWriter, r *http.Request, in *inboundReq, plan *engine.Plan, settings domain.Settings, inProto string) {
	start := time.Now()
	var firstErr *streamOutcome
	for _, at := range plan.Attempts {
		ch, ok := g.attemptEnv(at)
		if !ok {
			continue
		}
		outProto := OutProto(ch.Provider)
		ob, err := buildOutbound(ch, inProto, outProto, in.op, in.body, true)
		if err != nil {
			gateError(w, inProto, http.StatusBadRequest, "invalid_request_error", "cannot build request: "+err.Error())
			return
		}
		client := g.rl.Client(settings, at.TimeoutMs)
		res, err := g.rl.doStream(r.Context(), client, ob, ch, at.Offer, at.TimeoutMs)
		if err != nil {
			g.eng.RecordFailure(ch.ID, ch.MaxFailures, ch.CooldownSec)
			continue
		}
		if res.upErr != "" {
			// 客户端自己走了(按 Esc / 断网):不是渠道故障,别熔断健康渠道。
			if clientGone(r.Context(), nil) {
				g.logDisconnect(in, ch, at.Offer, time.Since(start).Milliseconds(), nil)
				return
			}
			g.eng.RecordFailure(ch.ID, ch.MaxFailures, ch.CooldownSec)
			firstErr = res
			continue
		}
		if res.status != http.StatusOK {
			if firstErr == nil {
				firstErr = res
			}
			g.eng.RecordFailure(ch.ID, ch.MaxFailures, ch.CooldownSec)
			if !retryableHTTP(res.status) {
				break
			}
			continue
		}
		// 200:开始向客户端回推;中途失败无法再换渠道。
		g.eng.RecordSuccess(ch.ID, res.firstTTFB.Milliseconds())
		g.streamFrom(w, r, in, res, ch, at.Offer, inProto, outProto, start)
		return
	}
	total := time.Since(start).Milliseconds()
	if firstErr == nil {
		gateError(w, inProto, http.StatusBadGateway, "api_error", "all upstream channels failed")
		g.logFailure(in, domain.ChannelRow{}, domain.OfferRead{}, http.StatusBadGateway,
			total, nil, ptrStr("all upstream channels failed"))
		return
	}
	if firstErr.upErr != "" {
		gateError(w, inProto, http.StatusGatewayTimeout, "api_error", "upstream unavailable: "+firstErr.upErr)
		g.logFailure(in, firstErr.channel, firstErr.offer, http.StatusGatewayTimeout,
			total, nil, ptrStr("upstream unavailable: "+firstErr.upErr))
		return
	}
	writeTranslatedError(w, inProto, OutProto(firstErr.channel.Provider), firstErr.status, firstErr.errBody)
	g.logFailure(in, firstErr.channel, firstErr.offer, firstErr.status,
		total, nil, upstreamMsg(firstErr.status, firstErr.errBody))
}

// streamFrom 已选定上游且响应头为 200:把 SSE 流转给客户端并记账。
//
// 流一旦开始就无法再换渠道,所以这里的重点是「记对账、归对因」:
//   - 客户端断开(context canceled)→ 499 client_disconnect,不是渠道故障;
//   - 上游停住(看门狗的 first-byte / idle 超时)→ 504 并说明停在哪一段;
//   - 其余(上游协议错、翻译失败、真的被对方切断)→ 502。
//
// 中断时拿不到权威 usage,但上游已生成并计费了这部分 token,故用本地估算兜底落账
// (只对已观测到的部分计费),否则网关账面上的成本会系统性偏低。
func (g *Gateway) streamFrom(w http.ResponseWriter, r *http.Request, in *inboundReq, res *streamOutcome, ch domain.ChannelRow, offer domain.OfferRead, inProto, outProto string, start time.Time) {
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	hw := &headWriter{ResponseWriter: w}

	// 客户端一走就立刻掐掉上游:否则上游继续生成、继续计费,纯属白烧额度。
	// (此前只在写回客户端失败时才发现断开,响应头间歇性错误无法及时归因。)
	stopWatch := make(chan struct{})
	defer close(stopWatch)
	go func() {
		select {
		case <-r.Context().Done():
			_ = res.body.Close()
		case <-stopWatch:
		}
	}()

	var tok translate.Usage
	var streamErr error
	if inProto == outProto {
		var u usage
		u, streamErr = passthroughSSE(hw, res.body, outProto)
		tok = translate.Usage{Prompt: u.prompt, Completion: u.completion, CacheRead: u.cacheRead}
	} else {
		estIn := 0
		if inProto == ProtoAnthropic {
			estIn = translate.EstimateMessagesInput(in.body)
		}
		tok, streamErr = translate.ConvertStream(inProto, outProto, res.body, hw, in.model, estIn)
	}
	_ = res.body.Close()
	total := time.Since(start).Milliseconds()

	if streamErr == nil {
		cost := costUsd(offer, tok)
		_ = g.st.ChargeToken(in.token.ID, cost)
		g.writeLog(in, ch, offer, http.StatusOK, tok, res.firstTTFB.Milliseconds(), total, nil)
		return
	}

	// 客户端侧断开优先判定:此时 ctx 已取消,底层错误裹的多半就是它。
	if clientGone(r.Context(), streamErr) {
		g.logDisconnect(in, ch, offer, total, &tok)
		return
	}
	if tok.Prompt == 0 && tok.Completion == 0 && tok.CacheRead == 0 {
		tok.Prompt = estInTokens(in)
	}
	status, msg := http.StatusBadGateway, "stream interrupted: "+streamErr.Error()
	switch {
	case errors.Is(streamErr, errFirstByteTimeout):
		status, msg = http.StatusGatewayTimeout, "upstream returned 200 but sent no data within the first-byte window"
	case errors.Is(streamErr, errStreamIdle):
		status, msg = http.StatusGatewayTimeout, "upstream stalled mid-stream (no data for the idle window)"
	}
	// 还没向客户端写过任何字节 → 按失败语义回明确错误,别让客户端拿到空 200(会被当成「成功但无内容」)。
	if !hw.wrote {
		gateError(w, inProto, status, "api_error", msg)
	}
	g.writeLog(in, ch, offer, status, tok, res.firstTTFB.Milliseconds(), total, &msg)
}

// headWriter 记录「是否已向客户端写过字节」,用于判断中断能否补一个错误响应。
// 保留 Flush 转发,翻译层依赖 http.Flusher 做流式推送。
type headWriter struct {
	http.ResponseWriter
	wrote bool
}

func (h *headWriter) Write(p []byte) (int, error) {
	h.wrote = true
	return h.ResponseWriter.Write(p)
}

func (h *headWriter) Flush() {
	if f, ok := h.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// estInTokens 入站请求的输入 token 本地估算(中断流无权威 usage 时的兜底)。
func estInTokens(in *inboundReq) int {
	if in.op == countOp {
		return 0
	}
	if in.inProto == ProtoAnthropic {
		return translate.EstimateMessagesInput(in.body)
	}
	return translate.EstimateOpenAIChatInput(in.body)
}

// clientGone 判断失败是否由客户端断开导致:请求 ctx 已取消,或错误链里含 context.Canceled
// (也可能是本次尝试自身的超时 —— 那属于渠道问题,故用 ctx 是否已取消来区分)。
func clientGone(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return true
	}
	return err != nil && errors.Is(err, context.Canceled)
}

// —— 计费与日志 ——

// costUsd 按命中 offer 单价 × token(每百万)算成本。
func costUsd(offer domain.OfferRead, tok translate.Usage) float64 {
	pm := func(price float64, n int) float64 { return price * float64(n) / 1e6 }
	return pm(offer.InputPriceUsd, tok.Prompt) + pm(offer.OutputPriceUsd, tok.Completion) + pm(offer.CacheReadPriceUsd, tok.CacheRead)
}

// writeLog 请求日志落库。ok=true 成功;errMsg 非空记录错误。
func (g *Gateway) writeLog(in *inboundReq, ch domain.ChannelRow, offer domain.OfferRead, status int, tok translate.Usage, firstMs, totalMs int64, errMsg *string) {
	var errField *string
	if logIsError(status) || errMsg != nil {
		e := errMsg
		if e == nil {
			s := http.StatusText(status)
			e = &s
		}
		errField = e
	}
	_ = g.st.InsertLog(domain.LogRow{
		TS:           g.nowFn().UTC(),
		Model:        in.model,
		ChannelID:    ch.ID,
		ChannelName:  ch.Name,
		TokenID:      in.token.ID,
		TokenName:    in.token.Name,
		ClientTool:   in.tool,
		Protocol:     in.inProto,
		Stream:       in.stream,
		Status:       status,
		PromptTokens: tok.Prompt,
		Completion:   tok.Completion,
		CacheRead:    tok.CacheRead,
		CostUsd:      costUsd(offer, tok),
		FirstTokenMs: int(firstMs),
		TotalMs:      int(totalMs),
		IP:           in.ip,
		Err:          errField,
	})
}

// logFailure 全候选失败(或网关内部错)时的失败账:供用量/错误率/渠道健康统计。
func (g *Gateway) logFailure(in *inboundReq, ch domain.ChannelRow, offer domain.OfferRead, status int, totalMs int64, firstMs *int64, msg *string) {
	errField := msg
	if errField == nil {
		s := http.StatusText(status)
		errField = &s
	}
	var ft int
	if firstMs != nil {
		ft = int(*firstMs)
	}
	_ = g.st.InsertLog(domain.LogRow{
		TS:           g.nowFn().UTC(),
		Model:        in.model,
		ChannelID:    ch.ID,
		ChannelName:  ch.Name,
		TokenID:      in.token.ID,
		TokenName:    in.token.Name,
		ClientTool:   in.tool,
		Protocol:     in.inProto,
		Stream:       in.stream,
		Status:       status,
		FirstTokenMs: ft,
		TotalMs:      int(totalMs),
		IP:           in.ip,
		Err:          errField,
	})
}

// logDisconnect 客户端主动断开(Claude Code 按 Esc / 关窗 / 网络掉):单列一条账,
// 不写 err 字段、状态码用 499,让聚合口径把它排除在「错误率」之外 —— 它不是任何一方的故障。
func (g *Gateway) logDisconnect(in *inboundReq, ch domain.ChannelRow, offer domain.OfferRead, totalMs int64, tok *translate.Usage) {
	var t translate.Usage
	if tok != nil {
		t = *tok
	}
	_ = g.st.InsertLog(domain.LogRow{
		TS:           g.nowFn().UTC(),
		Model:        in.model,
		ChannelID:    ch.ID,
		ChannelName:  ch.Name,
		TokenID:      in.token.ID,
		TokenName:    in.token.Name,
		ClientTool:   in.tool,
		Protocol:     in.inProto,
		Stream:       in.stream,
		Status:       domain.StatusClientClosed,
		PromptTokens: t.Prompt,
		Completion:   t.Completion,
		CacheRead:    t.CacheRead,
		CostUsd:      costUsd(offer, t),
		TotalMs:      int(totalMs),
		IP:           in.ip,
	})
}

// logIsError 一条日志是否算「错误」:客户端断开(499)不计入。
func logIsError(status int) bool {
	return status >= 400 && status != domain.StatusClientClosed
}

// upstreamMsg 从上游错误体抽一句人读信息(兼容 {"error":{message}} 两形状),无则状态文案。
func upstreamMsg(status int, body []byte) *string {
	var probe struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if len(body) > 0 && json.Unmarshal(body, &probe) == nil && probe.Error.Message != "" {
		return &probe.Error.Message
	}
	s := http.StatusText(status)
	if s == "" {
		s = "upstream request failed"
	}
	return &s
}

func ptrStr(s string) *string { return &s }

// —— GET /v1/models 与 count_tokens ——

func (g *Gateway) handleListModels(w http.ResponseWriter, r *http.Request) {
	secretKey := extractSecret(r)
	if secretKey == "" {
		gateError(w, ProtoOpenAI, http.StatusUnauthorized, "authentication_error", "missing api key")
		return
	}
	token, err := g.st.LookupTokenBySHA256(auth.HashSecret(secretKey))
	if err != nil {
		gateError(w, ProtoOpenAI, http.StatusUnauthorized, "authentication_error", "invalid api key")
		return
	}
	if code, typ, msg := tokenGateErr(g.nowFn(), token); code != 0 {
		gateError(w, ProtoOpenAI, code, typ, msg)
		return
	}
	models, err := g.st.EnabledModelsWithOffers()
	if err != nil {
		gateError(w, ProtoOpenAI, http.StatusInternalServerError, "api_error", err.Error())
		return
	}
	var names []string
	for _, m := range models {
		if engine.SupportsModel(token.AllowedModels, m.Name) {
			names = append(names, m.Name)
		}
	}
	anthropic := r.Header.Get("anthropic-version") != ""
	if anthropic {
		data := make([]any, 0, len(names))
		for _, n := range names {
			data = append(data, map[string]any{"type": "model", "id": n, "display_name": n, "created_at": g.nowFn().UTC().Format(time.RFC3339)})
		}
		writeJSONBytes(w, http.StatusOK, map[string]any{"data": data, "has_more": false})
		return
	}
	data := make([]any, 0, len(names))
	for _, n := range names {
		data = append(data, map[string]any{"id": n, "object": "model", "created": g.nowFn().Unix(), "owned_by": "gateway"})
	}
	writeJSONBytes(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}

func (g *Gateway) handleCountTokens(w http.ResponseWriter, r *http.Request) {
	in, ok := g.parseInbound(w, r, countOp)
	if !ok {
		return
	}
	n := translate.EstimateMessagesInput(in.body)
	writeJSONBytes(w, http.StatusOK, map[string]any{"input_tokens": n})
}

func writeJSONBytes(w http.ResponseWriter, status int, v any) {
	b, _ := json.Marshal(v)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

func modelNotFoundType(inProto string) string {
	if inProto == ProtoAnthropic {
		return "not_found_error"
	}
	return "model_not_found"
}

// 端点/操作字符串(小写常量,避与 translate 命名混淆)。
const (
	messagesOp = "messages"
	chatOp     = "chat"
	countOp    = "count_tokens"
)
