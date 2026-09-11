package proxy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"personal-ai-gateway/internal/domain"
	"personal-ai-gateway/internal/proxy/translate"
	"personal-ai-gateway/internal/secret"
	"personal-ai-gateway/internal/store"
)

// Relay 是数据面出站执行器:把 engine 定稿的候选按序发往渠道,
// 处理跨协议翻译/流式透传与逐次失败,并回报成功后的归一化 usage。
// 不关心鉴权/计费/日志(那是 Gateway 的责任)。
type Relay struct {
	st  *store.Store
	cfg relayConfig

	cmu     sync.Mutex
	clients map[clientKey]*http.Client // 出站 client 缓存(见 Client)
}

// relayConfig 出站全局参数(每次请求可由 settings 覆盖的部分在调用方)。
type relayConfig struct {
	DialTimeout time.Duration
	// StreamIdleTimeout 流式中途静默窗口的下限(见 minStreamIdle);
	// 独立成字段以便测试缩短窗口,生产恒为 minStreamIdle。
	StreamIdleTimeout time.Duration
}

func NewRelay(st *store.Store) *Relay {
	return &Relay{
		st: st,
		cfg: relayConfig{
			DialTimeout:       10 * time.Second,
			StreamIdleTimeout: minStreamIdle,
		},
		clients: map[clientKey]*http.Client{},
	}
}

// clientKey 出站 client 的复用键:同键共用一个 Transport(连接池)。
type clientKey struct {
	proxy      string
	skipTLS    bool
	headerWait time.Duration
}

// Client 依据设置构造出站 HTTP client(代理/跳过 TLS/响应头超时)。
//
// 同一组参数复用同一份 Transport:此前每请求新建 Transport,连接池随之丢弃,
// 到上游的 TCP+TLS 握手每次重做,是常态延迟 3~10s 与长尾抖动的一大来源。
// timeoutMs 为本次请求的候选超时,作为响应头阶段的硬上限(流式同样适用:
// 拿到响应头后由 stallGuard 接管,整条长流不再受全局超时约束)。
func (r *Relay) Client(settings domain.Settings, timeoutMs int) *http.Client {
	if timeoutMs <= 0 {
		timeoutMs = settings.RequestTimeoutMs
	}
	if timeoutMs <= 0 {
		timeoutMs = defaultRequestTimeoutMs
	}
	k := clientKey{
		proxy:      settings.HTTPProxy,
		skipTLS:    settings.SkipTLSVerify,
		headerWait: time.Duration(timeoutMs) * time.Millisecond,
	}

	r.cmu.Lock()
	defer r.cmu.Unlock()
	if c, ok := r.clients[k]; ok {
		return c
	}
	t := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: r.cfg.DialTimeout}).DialContext,
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: settings.SkipTLSVerify},
		ResponseHeaderTimeout: k.headerWait,
		MaxIdleConns:          64,
		IdleConnTimeout:       90 * time.Second,
	}
	if settings.HTTPProxy != "" {
		if u, err := url.Parse(settings.HTTPProxy); err == nil {
			t.Proxy = http.ProxyURL(u)
		}
	}
	c := &http.Client{Transport: t}
	r.clients[k] = c
	return c
}

// OutProto 由渠道 provider 定出站协议。
func OutProto(p domain.Provider) string {
	if p == domain.ProviderAnthropic {
		return ProtoAnthropic
	}
	return ProtoOpenAI
}

// apiRoot 渠道 base_url 归一为「协议根」:容忍用户按惯例填带尾缀 /v1 的地址
// (https://api.deepseek.com/v1),也容忍只填根(https://api.deepseek.com)。
// 调用方再统一拼 /v1/... 路径,避免 base+"/v1/models" 拼出 …/v1/v1/models。
func apiRoot(base string) string {
	b := strings.TrimRight(base, "/")
	if strings.HasSuffix(b, "/v1") {
		b = strings.TrimSuffix(b, "/v1")
	}
	return b
}

// outboundReq 一条候选的出站请求参数(密文已解密)。
type outboundReq struct {
	URL        string
	Proto      string // 出站协议
	Method     string
	Body       []byte
	APIKey     string
	Stream     bool
	QueryParam map[string]string // 少量 provider 补参(如 Azure api-version)
}

// buildOutbound 依据入站协议/操作与候选渠道拼出站请求。
func buildOutbound(ch domain.ChannelRow, inProto, outProto, op string, body []byte, stream bool) (*outboundReq, error) {
	key, err := secret.Decrypt(ch.APIKeyCipher)
	if err != nil {
		return nil, fmt.Errorf("decrypt channel key: %w", err)
	}
	base := apiRoot(ch.BaseURL)
	req := &outboundReq{APIKey: key, Proto: outProto, Stream: stream, Method: http.MethodPost}
	if inProto == outProto {
		req.Body = body // 同协议 fast path:原样透传
	} else {
		outOp, outBody, _, err := translate.BuildRequest(inProto, outProto, op, body, stream)
		if err != nil {
			return nil, err
		}
		op = outOp
		req.Body = outBody
	}
	switch outProto {
	case ProtoAnthropic:
		req.URL = base + "/v1/messages"
		if op == translate.OpCountTokens {
			req.URL = base + "/v1/messages/count_tokens"
		}
	case ProtoOpenAI:
		req.URL = base + "/v1/chat/completions"
	}
	if ch.Provider == domain.ProviderAzure && !strings.Contains(req.URL, "api-version") {
		sep := "?"
		if strings.Contains(req.URL, "?") {
			sep = "&"
		}
		req.URL += sep + "api-version=2024-06-01"
	}
	return req, nil
}

func setOutboundHeaders(h http.Header, req *outboundReq) {
	h.Set("Content-Type", "application/json")
	h.Set("Accept", "application/json")
	if req.Proto == ProtoAnthropic {
		h.Set("x-api-key", req.APIKey)
		h.Set("anthropic-version", "2023-06-01")
	} else {
		h.Set("Authorization", "Bearer "+req.APIKey)
	}
	if req.Stream {
		h.Set("Accept", "text/event-stream")
	}
}

// attemptResult 一次出站尝试的收敛结果。非 2xx 时 body 为完整错误体。
type attemptResult struct {
	channel   domain.ChannelRow
	offer     domain.OfferRead
	status    int
	body      []byte // 非流成功:成功体;非流失败:错误体
	latencyMs int64  // 整程耗时(非流:含读完体);用于日志/展示
	ttfbMs    int64  // 首字节(响应头)耗时;渠道健康 EWMA 用,与流式 firstTTFB 同口径
	upErr     string // 网络/超时类错误(无 body)
}

// retryableHTTP 判断该状态码是否值得换渠道重试。
func retryableHTTP(status int) bool {
	switch {
	case status >= 500 || status == http.StatusTooManyRequests:
		return true
	case status == http.StatusUnauthorized || status == http.StatusForbidden || status == http.StatusNotFound:
		return true // 换渠道常能解:key 配置错 / 该渠道没这模型
	}
	return false
}

// doNonStream 单候选非流请求:成功返回 2xx 原始体;失败读完整错误体。
// 网络/超时错误以 upErr 返回(无 status/body)。
func (r *Relay) doNonStream(ctx context.Context, client *http.Client, req *outboundReq, ch domain.ChannelRow, offer domain.OfferRead, timeoutMs int) (*attemptResult, error) {
	attemptCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(attemptCtx, req.Method, req.URL, bytes.NewReader(req.Body))
	if err != nil {
		return nil, err
	}
	setOutboundHeaders(httpReq.Header, req)
	t0 := time.Now()
	resp, err := client.Do(httpReq)
	if err != nil {
		return &attemptResult{channel: ch, offer: offer, upErr: err.Error()}, nil
	}
	ttfb := time.Since(t0).Milliseconds() // Do 返回即响应头到达 ≈ 首字节
	defer resp.Body.Close()
	body, rerr := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	lat := time.Since(t0).Milliseconds()
	if rerr != nil {
		return &attemptResult{channel: ch, offer: offer, upErr: rerr.Error()}, nil
	}
	return &attemptResult{channel: ch, offer: offer, status: resp.StatusCode, body: body, latencyMs: lat, ttfbMs: ttfb}, nil
}

// defaultRequestTimeoutMs settings 缺省值兜底(与 domain.Settings.Defaults 一致)。
const defaultRequestTimeoutMs = 60000

// 流式中断的两种可归因超时。看门狗触发时交回这两个(而不是底层的
// "use of closed network connection"),上层才能区分「上游没吐字」与「客户端走了」。
var (
	errFirstByteTimeout = errors.New("upstream first byte timeout")
	errStreamIdle       = errors.New("upstream stream idle timeout")
)

// minStreamIdle 中途静默窗口的下限。
//
// 首字节窗口可以等于请求超时(此时还没向客户端写任何字节,掐断等同一次普通超时);
// 但流一旦开始,再掐断客户端只能拿到半截响应、且已无法换渠道,判定要宽松得多,
// 否则上游一次正常的长思考停顿就被误判成故障(量级对齐常见反代的 read timeout)。
const minStreamIdle = 120 * time.Second

// stallGuard 给响应体加两级静默看门狗,任一触发即关掉底层连接并交回可归因的错误:
//
//	first  响应头已到但体一个字节都没来 —— 窗口从拿到响应头起算,只盯首字节,永不重置;
//	idle   流已开始但中途长时间无数据 —— 每读到数据就重置。
//
// 与旧实现的区别:旧版在每次 Read 时都重新武装定时器,于是「首字节看门狗」实际退化成了
// 「任意两次数据间隔超过 timeout 就掐断」,把上游的正常停顿记成了 502。
type stallGuard struct {
	r      io.Reader
	closer io.Closer
	first  time.Duration // 首字节窗口(<=0 关闭)
	idle   time.Duration // 中途静默窗口(<=0 关闭)

	mu       sync.Mutex
	gotFirst bool
	timer    *time.Timer
	fired    error
}

// newStallGuard 立刻武装首字节看门狗(不等第一次 Read)。
func newStallGuard(body io.ReadCloser, first, idle time.Duration) *stallGuard {
	g := &stallGuard{r: body, closer: body, first: first, idle: idle}
	if first > 0 {
		g.timer = time.AfterFunc(first, func() { g.fire(errFirstByteTimeout) })
	}
	return g
}

// fire 记下触发原因并关掉底层连接(幂等;只有首个原因保留)。
func (g *stallGuard) fire(reason error) {
	g.mu.Lock()
	if g.fired == nil {
		g.fired = reason
	}
	g.mu.Unlock()
	_ = g.closer.Close()
}

func (g *stallGuard) failure() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.fired
}

func (g *stallGuard) Read(p []byte) (int, error) {
	n, err := g.r.Read(p)
	if n > 0 {
		g.mu.Lock()
		if !g.gotFirst || g.idle > 0 {
			g.gotFirst = true
			if g.timer != nil {
				g.timer.Stop()
			}
			if g.idle > 0 {
				g.timer = time.AfterFunc(g.idle, func() { g.fire(errStreamIdle) })
			} else {
				g.timer = nil
			}
		}
		g.mu.Unlock()
	}
	if err != nil {
		if f := g.failure(); f != nil {
			return n, f // 看门狗关的连接:换成可归因的错误
		}
	}
	return n, err
}

func (g *stallGuard) Close() error {
	g.mu.Lock()
	if g.timer != nil {
		g.timer.Stop()
		g.timer = nil
	}
	g.mu.Unlock()
	return g.closer.Close()
}

// doStream 单候选流式请求:返回 2xx 的响应流与失败信息。
// 成功时 resp.Body 由调用方负责耗尽/关闭;失败返回非 2xx 的完整错误体。
// timeoutMs 为候选超时:作响应头阶段上限(client 的 ResponseHeaderTimeout),
// 并作首字节窗口;拿到首字节后再按 minStreamIdle 宽松判定中途静默。
type streamOutcome struct {
	channel   domain.ChannelRow
	offer     domain.OfferRead
	status    int
	body      io.ReadCloser // 2xx 成功流
	errBody   []byte        // 失败体
	upErr     string
	firstTTFB time.Duration
}

func (r *Relay) doStream(ctx context.Context, client *http.Client, req *outboundReq, ch domain.ChannelRow, offer domain.OfferRead, timeoutMs int) (*streamOutcome, error) {
	httpReq, err := http.NewRequestWithContext(ctx, req.Method, req.URL, bytes.NewReader(req.Body))
	if err != nil {
		return nil, err
	}
	setOutboundHeaders(httpReq.Header, req)
	t0 := time.Now()
	resp, err := client.Do(httpReq)
	if err != nil {
		return &streamOutcome{channel: ch, offer: offer, upErr: err.Error()}, nil
	}
	ttfb := time.Since(t0)
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return &streamOutcome{channel: ch, offer: offer, status: resp.StatusCode, errBody: body, firstTTFB: ttfb}, nil
	}
	// 200:两级看门狗。整条流不再设 ctx 超时——长流(实测有 169s 的)会被误杀;
	// 出问题的形态只有「上游不吐字」与「上游中途停住」两种,分别盯住即可。
	first := time.Duration(timeoutMs) * time.Millisecond
	if first <= 0 {
		first = time.Duration(defaultRequestTimeoutMs) * time.Millisecond
	}
	idle := first
	if floor := r.cfg.StreamIdleTimeout; idle < floor {
		idle = floor
	}
	resp.Body = newStallGuard(resp.Body, first, idle)
	return &streamOutcome{channel: ch, offer: offer, status: 200, body: resp.Body, firstTTFB: ttfb}, nil
}

// emit 写一段原样字节并 flush(流式透传与翻译共用)。
func emit(w http.ResponseWriter, b []byte) error {
	if _, err := w.Write(b); err != nil {
		return err
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	return nil
}

// passthroughSSE 原样转发 SSE 流并旁路嗅探 usage;返回归一化 usage。
// 行末统一 LF;usage 仅在解析成功时累加,绝不改写业务字节。
func passthroughSSE(w http.ResponseWriter, src io.Reader, proto string) (usage, error) {
	var u usage
	br := bufio.NewReaderSize(src, 32<<10)
	var pending bytes.Buffer
	for {
		line, err := br.ReadString('\n')
		if len(line) > 0 {
			sniffSSELine(proto, line, &u)
			if werr := emit(w, []byte(line)); werr != nil {
				return u, werr
			}
			pending.Reset()
		}
		if err == io.EOF {
			return u, nil
		}
		if err != nil {
			return u, err
		}
	}
}

// writeTranslatedError 把上游失败体按入站协议重编码后写出。
func writeTranslatedError(w http.ResponseWriter, inProto, outProto string, status int, errBody []byte) {
	if inProto == outProto {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(errBody)
		return
	}
	if inProto == ProtoAnthropic {
		typ, msg := translate.A2OError(status, errBody)
		WriteError(w, ProtoAnthropic, status, typ, msg)
		return
	}
	typ, msg := translate.O2AError(status, errBody)
	WriteError(w, ProtoOpenAI, status, typ, msg)
}

// gateError 网关侧(选路失败等)按入站协议写的错误。
func gateError(w http.ResponseWriter, inProto string, status int, typ, msg string) {
	WriteError(w, inProto, status, typ, msg)
}
