package proxy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"personal-ai-gateway/internal/config"
	"personal-ai-gateway/internal/proxy/translate"
)

// tryRelayTranslate 把「已选好但协议不同的上游」的请求先翻成上游协议、发出去,
// 再把上游响应翻回入站协议写回客户端 —— 与 tryRelay 平行的跨协议分支。
//
// 返回值约定同 tryRelay:
//   - handled=true  → 已向客户端写出最终响应,上层不得再重试;
//   - handled=false → 未写出任何字节,错误可重试(传输失败/429/5xx),上层换下一候选。
//
// 翻译路径的特殊约束:
//   - 非流式:整包缓冲、翻译成功后才写首字节 → 翻译失败回干净 502 且不重试(避免重复计费);
//   - 流式:逐 chunk 转 anthropic 事件实时写;一旦开写就不回退。
func (g *Gateway) tryRelayTranslate(ctx context.Context, inProto, model string, w http.ResponseWriter,
	in *http.Request, body []byte, up *config.Upstream, op string, stream bool) (handled bool, status int, err error, tok usage) {

	// 入站体改写(anthropic messages → openai chat)。解析失败属客户端 400,不回退。
	outOp, outBody, estIn, terr := translate.BuildRequest(inProto, up.Type, op, body, stream)
	if terr != nil {
		WriteError(w, inProto, http.StatusBadRequest, "invalid_request", terr.Error())
		return true, http.StatusBadRequest, terr, usage{}
	}

	path, err := outboundPath(up.Type, outOp)
	if err != nil {
		return false, 0, err, usage{}
	}
	url := strings.TrimRight(up.BaseURL, "/") + path

	outReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(outBody))
	if err != nil {
		return false, 0, fmt.Errorf("upstream %s: build request: %w", up.Name, err), usage{}
	}
	setOutboundHeaders(outReq, up, in)

	resp, err := http.DefaultClient.Do(outReq)
	if err != nil {
		return false, 0, fmt.Errorf("upstream %s unreachable: %w", up.Name, err), usage{}
	}
	defer resp.Body.Close()
	// 客户端断连(ctx 取消)时立刻关上游连接,避免继续烧配额/tokens
	stop := cancelOnCtx(ctx, resp.Body)
	defer stop()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if stream {
			return g.streamTranslate(ctx, inProto, up, model, estIn, w, resp)
		}
		return g.nonStreamTranslate(ctx, inProto, up, w, resp)
	}

	// 非 2xx:429/5xx 未写字节 → 可重试交 failover;不可重试 4xx 重编码成入站(anthropic)错误信封
	errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	if retryable(resp.StatusCode) {
		return false, resp.StatusCode, fmt.Errorf("upstream %s: status %d: %s",
			up.Name, resp.StatusCode, brief(errBody)), usage{}
	}
	typ, msg := translate.A2OError(resp.StatusCode, errBody)
	WriteError(w, inProto, resp.StatusCode, typ, msg)
	return true, resp.StatusCode, fmt.Errorf("upstream %s: status %d (re-encoded)",
		up.Name, resp.StatusCode), usage{}
}

// nonStreamTranslate 读完整包 → 翻译成 anthropic message → 成功后才写首字节。
func (g *Gateway) nonStreamTranslate(ctx context.Context, inProto string, up *config.Upstream,
	w http.ResponseWriter, resp *http.Response) (handled bool, status int, err error, tok usage) {
	raw, rerr := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if rerr != nil {
		// 传输读到一半断了、还没写客户端 → 可重试换上游
		return false, resp.StatusCode, fmt.Errorf("upstream %s: read body: %w", up.Name, rerr), usage{}
	}
	out, tu, terr := translate.ConvertNonStream(inProto, up.Type, raw)
	if terr != nil {
		// 翻译失败:首字节未写。不回重试(同一上游重放会重复计费),给干净 502。
		msg := fmt.Sprintf("translate %s response from %s failed: %v", up.Type, up.Name, terr)
		WriteError(w, inProto, http.StatusBadGateway, "api_error", msg)
		return true, http.StatusBadGateway, fmt.Errorf("upstream %s: %s", up.Name, msg), usage{}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if _, werr := w.Write(out); werr != nil {
		return true, http.StatusOK, werr, convertUsage(tu)
	}
	return true, http.StatusOK, nil, convertUsage(tu)
}

// streamTranslate 开写 anthropic SSE 事件流(先定 headers 与 200,之后不回退)。
func (g *Gateway) streamTranslate(ctx context.Context, inProto string, up *config.Upstream,
	model string, estIn int, w http.ResponseWriter, resp *http.Response) (handled bool, status int, err error, tok usage) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	tu, cerr := translate.ConvertStream(inProto, up.Type, resp.Body, w, model, estIn)
	if cerr != nil {
		// 流式中断/客户端断开:已写字节,handled=true 结束,不静默切换
		return true, http.StatusOK, fmt.Errorf("upstream %s: translate stream: %w", up.Name, cerr), convertUsage(tu)
	}
	return true, http.StatusOK, nil, convertUsage(tu)
}

// convertUsage 把 translate 的独立 Usage 口径转成 proxy.usage(两者字段同义,跨包避免 import 环)。
func convertUsage(t translate.Usage) usage {
	return usage{prompt: t.Prompt, completion: t.Completion, cacheRead: t.CacheRead}
}
