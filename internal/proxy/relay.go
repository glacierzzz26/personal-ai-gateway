package proxy

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"personal-ai-gateway/internal/config"
)

// outboundPath 依据上游协议类型与入站操作,决定出站 URL 路径。
// 约定:openai 型 base_url 含 /v1,anthropic 型 base_url 为域名根(见 DESIGN.md §3.5)。
func outboundPath(upType, op string) (string, error) {
	switch upType {
	case config.TypeAnthropic:
		switch op {
		case OpMessages:
			return "/v1/messages", nil
		case OpCountTokens:
			return "/v1/messages/count_tokens", nil
		}
	case config.TypeOpenAI:
		if op == OpChat {
			return "/chat/completions", nil
		}
	}
	return "", fmt.Errorf("no outbound path for protocol %q op %q", upType, op)
}

// setOutboundHeaders 按上游协议装配认证等出站请求头。
// 透传 anthropic-beta(如提示词缓存/beta 能力),因为客户端可能带了它。
func setOutboundHeaders(out *http.Request, up *config.Upstream, in *http.Request) {
	out.Header.Set("Content-Type", "application/json")
	out.Header.Set("Accept", "application/json")
	if up.Type == config.TypeAnthropic {
		out.Header.Set("x-api-key", up.APIKey)
		out.Header.Set("anthropic-version", "2023-06-01")
		if b := in.Header.Get("anthropic-beta"); b != "" {
			out.Header.Set("anthropic-beta", b)
		}
	} else {
		out.Header.Set("Authorization", "Bearer "+up.APIKey)
	}
}

// tryRelay 把一个已选好上游的请求转发出去,并顺带解析真实 token 用量(tok)。
//
// 返回值约定:
//   - handled=true  → 已向客户端写出最终响应(成功体/透传 4xx/流式中途结束),上层不得再重试;
//   - handled=false → 未写出任何响应,错误为可重试(传输层失败/429/5xx),上层可换下一个上游。
func (g *Gateway) tryRelay(ctx context.Context, w http.ResponseWriter, in *http.Request,
	body []byte, up *config.Upstream, op string, stream bool) (handled bool, status int, err error, tok usage) {

	path, err := outboundPath(up.Type, op)
	if err != nil {
		return false, 0, err, usage{}
	}
	url := strings.TrimRight(up.BaseURL, "/") + path

	outReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return false, 0, fmt.Errorf("upstream %s: build request: %w", up.Name, err), usage{}
	}
	setOutboundHeaders(outReq, up, in)

	resp, err := http.DefaultClient.Do(outReq)
	if err != nil {
		return false, 0, fmt.Errorf("upstream %s unreachable: %w", up.Name, err), usage{}
	}
	defer resp.Body.Close()
	// 客户端断连(ctx 取消)时立刻关闭上游连接,避免继续烧配额/tokens
	stop := cancelOnCtx(ctx, resp.Body)
	defer stop()

	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/json"
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		w.Header().Set("Content-Type", ct)
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(resp.StatusCode)
		if stream {
			tok, err = copySSE(ctx, w, resp.Body, up.Type)
		} else {
			tok, err = copyNonStream(w, resp.Body, up.Type)
		}
		if err != nil {
			return true, resp.StatusCode, err, tok
		}
		return true, resp.StatusCode, nil, tok
	}

	errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	if retryable(resp.StatusCode) {
		return false, resp.StatusCode, fmt.Errorf("upstream %s: status %d: %s",
			up.Name, resp.StatusCode, brief(errBody)), usage{}
	}
	// 不可重试的 4xx:把上游错误体原样透传给客户端,便于看清是哪家拒绝
	w.Header().Set("Content-Type", ct)
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(errBody)
	return true, resp.StatusCode, fmt.Errorf("upstream %s: status %d (forwarded)",
		up.Name, resp.StatusCode), usage{}
}

// copyNonStream 整段转发非流式响应,同时把响应体读进缓冲,转发完解析 usage。
func copyNonStream(w http.ResponseWriter, src io.Reader, proto string) (usage, error) {
	var buf bytes.Buffer
	if _, err := io.Copy(w, io.TeeReader(src, &buf)); err != nil {
		return usage{}, err
	}
	switch proto {
	case ProtoAnthropic:
		return parseAnthropicUsage(buf.Bytes()), nil
	default:
		return parseOpenAIUsage(buf.Bytes()), nil
	}
}

// copySSE 把上游 SSE 流原样回传(逐行写+Flush),边写边嗅探 usage;
// 客户端断开时关闭上游连接,及时收手。
func copySSE(ctx context.Context, w http.ResponseWriter, src io.Reader, proto string) (usage, error) {
	errc := make(chan error, 1)
	var tok usage
	go func() {
		br := bufio.NewReaderSize(src, 32<<10)
		for {
			line, err := br.ReadString('\n')
			if len(line) > 0 {
				if _, werr := w.Write([]byte(line)); werr != nil {
					errc <- werr
					return
				}
				if fl, ok := w.(http.Flusher); ok {
					fl.Flush()
				}
				sniffSSELine(proto, line, &tok)
			}
			switch err {
			case nil:
			case io.EOF:
				errc <- nil
				return
			default:
				errc <- err
				return
			}
		}
	}()

	select {
	case err := <-errc:
		return tok, err
	case <-ctx.Done():
		if c, ok := src.(io.Closer); ok {
			_ = c.Close() // 让转发协程立刻退出
		}
		<-errc
		return usage{}, fmt.Errorf("client disconnected: %w", ctx.Err())
	}
}

// cancelOnCtx 返回一个 stop 函数;在 stop 之前若 ctx 结束则关闭 closer。
func cancelOnCtx(ctx context.Context, closer io.Closer) (stop func()) {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = closer.Close()
		case <-done:
		}
	}()
	return func() { close(done) }
}

func retryable(code int) bool {
	return code == http.StatusTooManyRequests || code == http.StatusRequestTimeout || code >= 500
}

// brief 截断错误响应体,方便塞进一行日志/错误信息。
func brief(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 400 {
		s = s[:400] + "…"
	}
	if s == "" {
		return "(empty body)"
	}
	return s
}
