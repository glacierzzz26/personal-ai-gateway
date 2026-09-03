package proxy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
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
}

// relayConfig 出站全局参数(每次请求可由 settings 覆盖的部分在调用方)。
type relayConfig struct {
	DialTimeout time.Duration
}

func NewRelay(st *store.Store) *Relay {
	return &Relay{st: st, cfg: relayConfig{DialTimeout: 10 * time.Second}}
}

// Client 依据设置构造出站 HTTP client(代理/跳过 TLS)。
func (r *Relay) Client(settings domain.Settings) *http.Client {
	t := &http.Transport{
		DialContext:     (&net.Dialer{Timeout: r.cfg.DialTimeout}).DialContext,
		TLSClientConfig: &tls.Config{InsecureSkipVerify: settings.SkipTLSVerify},
	}
	if settings.HTTPProxy != "" {
		if u, err := url.Parse(settings.HTTPProxy); err == nil {
			t.Proxy = http.ProxyURL(u)
		}
	}
	return &http.Client{Transport: t}
}

// OutProto 由渠道 provider 定出站协议。
func OutProto(p domain.Provider) string {
	if p == domain.ProviderAnthropic {
		return ProtoAnthropic
	}
	return ProtoOpenAI
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
	base := strings.TrimRight(ch.BaseURL, "/")
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
	latencyMs int64
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
	defer resp.Body.Close()
	body, rerr := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	lat := time.Since(t0).Milliseconds()
	if rerr != nil {
		return &attemptResult{channel: ch, offer: offer, upErr: rerr.Error()}, nil
	}
	return &attemptResult{channel: ch, offer: offer, status: resp.StatusCode, body: body, latencyMs: lat}, nil
}

// fbGuard 首字节看门狗:流若在 timeout 内一个字节都不到,则中止并报 timeout(换候选)。
type fbGuard struct {
	r      io.Reader
	closer io.Closer
	d      time.Duration
	mu     sync.Mutex
	timer  *time.Timer
	fired  bool
}

func (g *fbGuard) Close() error { return g.closer.Close() }

func (g *fbGuard) Read(p []byte) (int, error) {
	g.mu.Lock()
	if g.timer == nil && !g.fired {
		g.timer = time.AfterFunc(g.d, func() {
			g.mu.Lock()
			g.fired = true
			g.mu.Unlock()
			_ = g.closer.Close()
		})
	}
	g.mu.Unlock()
	n, err := g.r.Read(p)
	if n > 0 {
		g.mu.Lock()
		if g.timer != nil {
			g.timer.Stop()
			g.timer = nil
		}
		g.mu.Unlock()
	}
	return n, err
}

// doStream 单候选流式请求:返回 2xx 的响应流与失败信息。
// 成功时 resp.Body 由调用方负责耗尽/关闭;失败返回非 2xx 的完整错误体。
// 首个字节超过 ttfbMs 视为超时失败。
type streamOutcome struct {
	channel   domain.ChannelRow
	offer     domain.OfferRead
	status    int
	body      io.ReadCloser // 2xx 成功流
	errBody   []byte        // 失败体
	upErr     string
	firstTTFB time.Duration
}

func (r *Relay) doStream(ctx context.Context, client *http.Client, req *outboundReq, ch domain.ChannelRow, offer domain.OfferRead, ttfbMs int) (*streamOutcome, error) {
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
	// 200:包首字节看门狗
	resp.Body = &fbGuard{r: resp.Body, closer: resp.Body, d: time.Duration(ttfbMs) * time.Millisecond}
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
