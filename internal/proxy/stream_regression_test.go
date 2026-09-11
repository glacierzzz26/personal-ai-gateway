package proxy

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"personal-ai-gateway/internal/domain"
	"personal-ai-gateway/internal/store"
)

// slowOpenAIUpstream 假 OpenAI 流式上游:先吐 head 个 chunk、每块间隔 gap,随后按 tail 行为收尾:
//   - "stall":永久挂住(测首字节 / 中途静默超时)
//   - "eof":  正常结束(测长流不被误杀)
func slowOpenAIUpstream(t *testing.T, head int, gap time.Duration, tail string) *httptest.Server {
	t.Helper()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		if fl != nil {
			fl.Flush() // 响应头先落地,后续「一个字节都不吐」才是真的首字节停住
		}
		for i := 0; i < head; i++ {
			time.Sleep(gap)
			fmt.Fprintf(w, "data: {\"id\":\"c\",\"object\":\"chat.completion.chunk\",\"model\":\"m\","+
				"\"choices\":[{\"index\":0,\"delta\":{\"content\":\"x\"},\"finish_reason\":null}]}\n\n")
			if fl != nil {
				fl.Flush()
			}
		}
		if tail == "eof" {
			fmt.Fprint(w, "data: {\"id\":\"c\",\"choices\":[],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":8}}\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
			if fl != nil {
				fl.Flush()
			}
			return
		}
		<-r.Context().Done() // stall:挂住,直到网关(看门狗)断开
		time.Sleep(20 * time.Millisecond)
	})
	up := httptest.NewServer(h)
	t.Cleanup(up.Close)
	return up
}

// setSettings 直接改网关参数(测试里缩短超时窗口用)。
func (e *e2eEnv) setSettings(t *testing.T, mut func(*domain.Settings)) {
	t.Helper()
	s, err := e.st.GetSettings()
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	mut(&s)
	if err := e.st.SaveSettings(s); err != nil {
		t.Fatalf("save settings: %v", err)
	}
}

// lastLog 取最新一条日志。
func (e *e2eEnv) lastLog(t *testing.T) domain.LogItem {
	t.Helper()
	items, _, err := e.st.ListLogs(store.LogFilter{Limit: 1}, 0)
	if err != nil || len(items) == 0 {
		t.Fatalf("no log: %v", err)
	}
	return items[0]
}

// postStream 发数据面流式请求并完整读走响应体,返回状态码。
func (e *e2eEnv) postStream(path, bearer, body string) (int, string) {
	e.t.Helper()
	req, err := http.NewRequest(http.MethodPost, e.srv.URL+path, strings.NewReader(body))
	if err != nil {
		e.t.Fatalf("new req: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatalf("do stream: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// TestE2EStreamStallBeforeFirstByte 上游回 200 后一个字节都不吐 → 504,而不是
// 记录成渠道 502 或 "use of closed network connection"。
func TestE2EStreamStallBeforeFirstByte(t *testing.T) {
	e := newE2E(t)
	up := slowOpenAIUpstream(t, 0, 0, "stall")
	chID := e.addChannel("slow", domain.ProviderOpenAI, up.URL, "sk", 1)
	e.addModelOffer("m1", chID, 1)
	tok := e.addToken("t", []string{"*"}, 100)
	e.setSettings(t, func(s *domain.Settings) { s.RequestTimeoutMs = 200 })

	code, _ := e.postStream("/v1/chat/completions", tok, `{"model":"m1","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	if code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504(上游停住)", code)
	}
	lg := e.lastLog(t)
	if lg.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("log status = %d, want 504", lg.StatusCode)
	}
	if lg.Error == nil {
		t.Fatalf("log err = nil, want 首字节超时说明")
	}
	if !strings.Contains(*lg.Error, "no data") {
		t.Fatalf("log err = %q, want 首字节超时说明", *lg.Error)
	}
}

// TestE2EStreamLongButHealthy 数据持续到达的长流不得被看门狗掐断(生产 18 条误报的回归)。
func TestE2EStreamLongButHealthy(t *testing.T) {
	e := newE2E(t)
	// 8 块 × 60ms = 480ms,而请求超时只有 100ms:旧实现会在第二个 100ms 空档就掐断。
	up := slowOpenAIUpstream(t, 8, 60*time.Millisecond, "eof")
	chID := e.addChannel("slow", domain.ProviderOpenAI, up.URL, "sk", 1)
	e.addModelOffer("m1", chID, 1)
	tok := e.addToken("t", []string{"*"}, 100)
	e.setSettings(t, func(s *domain.Settings) { s.RequestTimeoutMs = 100 })

	code, body := e.postStream("/v1/chat/completions", tok, `{"model":"m1","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200(body=%s)", code, body)
	}
	lg := e.lastLog(t)
	if lg.StatusCode != http.StatusOK || lg.Error != nil {
		t.Fatalf("log = %d/%v, want 200 无错误(长流被误判)", lg.StatusCode, lg.Error)
	}
	// usage 末块应被嗅探到并记账(12/8)。
	if lg.InTokens == 0 && lg.OutTokens == 0 {
		t.Fatalf("成功流未记到 usage: %+v", lg)
	}
}

// TestE2EStreamIdleMidway 流中途停住 → 504 且归因为 idle,不是 502。
func TestE2EStreamIdleMidway(t *testing.T) {
	e := newE2E(t)
	up := slowOpenAIUpstream(t, 2, 5*time.Millisecond, "stall") // 先来 2 块,然后挂住
	chID := e.addChannel("slow", domain.ProviderOpenAI, up.URL, "sk", 1)
	e.addModelOffer("m1", chID, 1)
	tok := e.addToken("t", []string{"*"}, 100)
	e.setSettings(t, func(s *domain.Settings) { s.RequestTimeoutMs = 200 })
	e.gw.rl.cfg.StreamIdleTimeout = 150 * time.Millisecond // 缩短 silent 窗口便于测试

	// 已经吐了 2 块出去,HTTP 状态码改不了(客户端早已收到 200);要断言的是「账记对了」。
	e.postStream("/v1/chat/completions", tok, `{"model":"m1","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	lg := e.lastLog(t)
	if lg.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("log status = %d, want 504(中途停住)", lg.StatusCode)
	}
	if lg.Error == nil || !strings.Contains(*lg.Error, "mid-stream") {
		t.Fatalf("log err = %v, want 中途静默说明", lg.Error)
	}
	if lg.InTokens == 0 {
		t.Fatalf("中断流未做估算兜底记账: %+v", lg)
	}
}

// TestE2EClientDisconnectNotChannelFailure 客户端断开:记 499、不写 err、不熔断渠道,
// 且不计入错误率。分片写入后主动断连,模拟 Claude Code 按 Esc。
func TestE2EClientDisconnectNotChannelFailure(t *testing.T) {
	e := newE2E(t)
	up := slowOpenAIUpstream(t, 100, 20*time.Millisecond, "stall")
	chID := e.addChannel("slow", domain.ProviderOpenAI, up.URL, "sk", 1)
	e.addModelOffer("m1", chID, 1)
	tok := e.addToken("t", []string{"*"}, 100)
	e.setSettings(t, func(s *domain.Settings) { s.RequestTimeoutMs = 5000 })
	e.gw.rl.cfg.StreamIdleTimeout = 300 * time.Millisecond // 断连后上游即使还有数据也不该拖住收尾

	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+"/v1/chat/completions",
		strings.NewReader(`{"model":"m1","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	buf := make([]byte, 64)
	if _, err := resp.Body.Read(buf); err != nil {
		t.Fatalf("read first chunk: %v", err)
	}
	resp.Body.Close() // 客户端走人

	// 等网关把断连落账。
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		items, _, _ := e.st.ListLogs(store.LogFilter{}, 0)
		if len(items) > 0 {
			lg := items[0]
			if lg.StatusCode != domain.StatusClientClosed {
				t.Fatalf("log status = %d, want 499(客户端断开)", lg.StatusCode)
			}
			if lg.Error != nil {
				t.Fatalf("499 不应带 err 字段(否则会被计入错误率): %v", *lg.Error)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("断连后 3s 内未见日志")
}
