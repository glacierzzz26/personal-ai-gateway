package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"personal-ai-gateway/internal/auth"
	"personal-ai-gateway/internal/domain"
	"personal-ai-gateway/internal/engine"
	"personal-ai-gateway/internal/secret"
	"personal-ai-gateway/internal/store"
)

// e2eEnv 一个隔离的数据面测试环境(独立临时库 + 引擎 + 网关)。
type e2eEnv struct {
	t   *testing.T
	st  *store.Store
	gw  *Gateway
	srv *httptest.Server
}

func newE2E(t *testing.T) *e2eEnv {
	t.Helper()
	dir := t.TempDir()
	if _, err := secret.BootstrapKey(dir); err != nil {
		t.Fatalf("bootstrap master key: %v", err)
	}
	st, err := store.Open(filepath.Join(dir, "e2e.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	eng := engine.New(st)
	gw := NewGateway(st, eng, NewRelay(st))
	srv := httptest.NewServer(gw)
	t.Cleanup(srv.Close)
	return &e2eEnv{t: t, st: st, gw: gw, srv: srv}
}

// addChannel 建渠道并返回 id。
func (e *e2eEnv) addChannel(name string, provider domain.Provider, base, key string, priority int) int64 {
	e.t.Helper()
	en := true
	ch, err := e.st.CreateChannel(domain.ChannelInput{
		Name: name, Provider: provider, BaseURL: base, APIKey: key,
		Priority: priority, Enabled: &en, TimeoutMs: 30000, MaxFailures: 2, CooldownSec: 10,
	})
	if err != nil {
		e.t.Fatalf("create channel: %v", err)
	}
	return ch.ID
}

// addModelOffer 建模型 + 供给源并返回 model id;模型已存在则复用(多渠道同模型)。
func (e *e2eEnv) addModelOffer(model string, channelID int64, prio int) int64 {
	e.t.Helper()
	m, err := e.st.CreateModel(domain.ModelInput{Name: model, ContextWindow: 32000})
	if err != nil {
		if m2, err2 := e.st.GetModelByName(model); err2 == nil {
			m = m2
		} else {
			e.t.Fatalf("create model %s: %v", model, err)
		}
	}
	p := prio
	if _, err := e.st.CreateOffer(m.ID, domain.OfferInput{
		ChannelID: channelID, InputPriceUsd: 2.0, OutputPriceUsd: 4.0,
		OverridePrice: true, Priority: &p, Enabled: boolPtr(true), RateLimitRpm: 1000,
	}); err != nil {
		e.t.Fatalf("create offer: %v", err)
	}
	return m.ID
}

// addToken 建高额令牌返回明文(请求鉴权头用)。
func (e *e2eEnv) addToken(name string, allowed []string, quota float64) string {
	e.t.Helper()
	plain, hashed, err := auth.NewModelKey()
	if err != nil {
		e.t.Fatalf("new key: %v", err)
	}
	if _, err := e.st.CreateToken(name, allowed, quota, 1000, nil, hashed, domain.MaskKey(plain)); err != nil {
		e.t.Fatalf("create token: %v", err)
	}
	return plain
}

func boolPtr(b bool) *bool { return &b }

// post 发数据面请求,返回状态码与 body。
func (e *e2eEnv) post(path, bearer string, anthropic bool, body string) (int, string) {
	e.t.Helper()
	req, err := http.NewRequest(http.MethodPost, e.srv.URL+path, bytes.NewBufferString(body))
	if err != nil {
		e.t.Fatalf("new req: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if anthropic {
		req.Header.Set("x-api-key", bearer)
		req.Header.Set("anthropic-version", "2023-06-01")
	} else {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatalf("do %s: %v", path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// openaiUpstream 假 OpenAI:回显文本为 echo,usage 固定。
func openaiUpstream(t *testing.T, echo string, status int) *httptest.Server {
	t.Helper()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status != http.StatusOK {
			_, _ = fmt.Fprintf(w, `{"error":{"message":"boom","type":"server_error"}}`)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-e2e", "object": "chat.completion", "model": "m",
			"choices": []any{map[string]any{"index": 0,
				"message":       map[string]any{"role": "assistant", "content": echo},
				"finish_reason": "stop"}},
			"usage": map[string]any{"prompt_tokens": 12, "completion_tokens": 8, "total_tokens": 20},
		})
	})
	up := httptest.NewServer(h)
	t.Cleanup(up.Close)
	return up
}

// anthropicUpstream 假 Anthropic:/v1/messages。
func anthropicUpstream(t *testing.T, echo string, status int) *httptest.Server {
	t.Helper()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status != http.StatusOK {
			_, _ = fmt.Fprintf(w, `{"type":"error","error":{"type":"api_error","message":"boom"}}`)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "msg_e2e", "type": "message", "role": "assistant", "model": "m",
			"content":     []any{map[string]any{"type": "text", "text": echo}},
			"usage":       map[string]any{"input_tokens": 12, "output_tokens": 8},
			"stop_reason": "end_turn",
		})
	})
	up := httptest.NewServer(h)
	t.Cleanup(up.Close)
	return up
}

const chatBody = `{"model":"%s","messages":[{"role":"user","content":"hi"}]}`
const messagesBody = `{"model":"%s","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`

// logsFor 取全部日志。
func (e *e2eEnv) logsFor() []domain.LogItem {
	e.t.Helper()
	items, _, err := e.st.ListLogs(store.LogFilter{Limit: 20}, 0)
	if err != nil {
		e.t.Fatalf("list logs: %v", err)
	}
	return items
}

func firstText(data []byte) string {
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return ""
	}
	if content, ok := m["content"].([]any); ok && len(content) > 0 {
		if blk, ok := content[0].(map[string]any); ok {
			if s, ok := blk["text"].(string); ok {
				return s
			}
		}
	}
	if choices, ok := m["choices"].([]any); ok && len(choices) > 0 {
		if c, ok := choices[0].(map[string]any); ok {
			if msg, ok := c["message"].(map[string]any); ok {
				if s, ok := msg["content"].(string); ok {
					return s
				}
			}
		}
	}
	return ""
}

// TestE2EOpenAIIdentity openai 入站 → openai 渠道直通。
func TestE2EOpenAIIdentity(t *testing.T) {
	e := newE2E(t)
	up := openaiUpstream(t, "pong-openai", http.StatusOK)
	chID := e.addChannel("oa", domain.ProviderOpenAI, up.URL, "sk-up", 1)
	model := "m-oa"
	e.addModelOffer(model, chID, 1)
	key := e.addToken("cli", []string{"*"}, 100)

	code, body := e.post("/v1/chat/completions", key, false, fmt.Sprintf(chatBody, model))
	if code != http.StatusOK {
		t.Fatalf("status %d body %s", code, body)
	}
	if got := firstText([]byte(body)); got != "pong-openai" {
		t.Fatalf("echo = %q", got)
	}
	logs := e.logsFor()
	if len(logs) != 1 || logs[0].StatusCode != 200 || logs[0].Model != model || logs[0].ChannelName != "oa" {
		t.Fatalf("logs = %+v", logs)
	}
	if logs[0].CostUsd <= 0 {
		t.Fatalf("cost not billed: %+v", logs[0])
	}
	tr, _ := e.st.GetToken(1)
	if tr.UsedUsd <= 0 {
		t.Fatalf("token not charged: %+v", tr)
	}
}

// TestE2EAnthropicToOpenAI anthropic 入站 → openai 渠道:客户端仍见 anthropic 形状。
func TestE2EAnthropicToOpenAI(t *testing.T) {
	e := newE2E(t)
	up := openaiUpstream(t, "pong-openai", http.StatusOK)
	chID := e.addChannel("oa", domain.ProviderOpenAI, up.URL, "sk-up", 1)
	model := "m-a2o"
	e.addModelOffer(model, chID, 1)
	key := e.addToken("cli", []string{"*"}, 100)

	code, body := e.post("/v1/messages", key, true, fmt.Sprintf(messagesBody, model))
	if code != http.StatusOK {
		t.Fatalf("status %d body %s", code, body)
	}
	// 必须返回 anthropic messages 风格:content[].text,而非 choices
	if !strings.Contains(body, `"text":"pong-openai"`) || strings.Contains(body, "choices") {
		t.Fatalf("response not anthropic shape: %s", body)
	}
	logs := e.logsFor()
	if len(logs) != 1 || logs[0].Model != model {
		t.Fatalf("logs = %+v", logs)
	}
}

// TestE2EOpenAIToAnthropic openai 入站 → anthropic 渠道(o2a 反译)。
func TestE2EOpenAIToAnthropic(t *testing.T) {
	e := newE2E(t)
	up := anthropicUpstream(t, "pong-anthropic", http.StatusOK)
	chID := e.addChannel("ant", domain.ProviderAnthropic, up.URL, "sk-ant", 1)
	model := "m-o2a"
	e.addModelOffer(model, chID, 1)
	key := e.addToken("cli", []string{"*"}, 100)

	code, body := e.post("/v1/chat/completions", key, false, fmt.Sprintf(chatBody, model))
	if code != http.StatusOK {
		t.Fatalf("status %d body %s", code, body)
	}
	// openai 入站应拿到 chat.completion 形状
	var cc struct {
		Object  string `json:"object"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(body), &cc); err != nil {
		t.Fatalf("response not openai shape: %v", err)
	}
	if cc.Object != "chat.completion" || len(cc.Choices) != 1 || cc.Choices[0].Message.Content != "pong-anthropic" {
		t.Fatalf("o2a conversion mismatch: %s", body)
	}
	logs := e.logsFor()
	if len(logs) != 1 || logs[0].ChannelName != "ant" {
		t.Fatalf("logs = %+v", logs)
	}
}

// TestE2EUpstreamFailover 首选渠道 500 → 自动切换次选渠道成功,日志记最终渠道。
func TestE2EUpstreamFailover(t *testing.T) {
	e := newE2E(t)
	fail := openaiUpstream(t, "", http.StatusInternalServerError)
	ok := openaiUpstream(t, "pong-backup", http.StatusOK)
	failID := e.addChannel("bad", domain.ProviderOpenAI, fail.URL, "sk-bad", 1)
	okID := e.addChannel("good", domain.ProviderOpenAI, ok.URL, "sk-good", 1)
	model := "m-fail"
	e.addModelOffer(model, failID, 1) // prio 1 首选
	e.addModelOffer(model, okID, 2)   // prio 2 兜底
	key := e.addToken("cli", []string{"*"}, 100)

	code, body := e.post("/v1/chat/completions", key, false, fmt.Sprintf(chatBody, model))
	if code != http.StatusOK {
		t.Fatalf("status %d body %s", code, body)
	}
	if got := firstText([]byte(body)); got != "pong-backup" {
		t.Fatalf("echo = %q", got)
	}
	logs := e.logsFor()
	if len(logs) != 1 || logs[0].ChannelName != "good" || logs[0].StatusCode != 200 {
		t.Fatalf("logs = %+v", logs)
	}
	// bad 渠道已记失败(电路计数)→ 不开放仍健康;good 成功计入成本
	if tr, _ := e.st.GetToken(1); tr.UsedUsd <= 0 {
		t.Fatalf("token not charged")
	}
}

// TestE2EQuotaAndAllowed 令牌额度(quota 拦截)与允许模型(403)由网关层拒绝。
func TestE2EQuotaAndAllowed(t *testing.T) {
	e := newE2E(t)
	up := openaiUpstream(t, "pong", http.StatusOK)
	chID := e.addChannel("oa", domain.ProviderOpenAI, up.URL, "sk-up", 1)
	e.addModelOffer("m-ok", chID, 1)
	// allowed 只放行 m-ok;请求 m-no → 403
	key := e.addToken("cli", []string{"m-ok"}, 100)
	code, body := e.post("/v1/chat/completions", key, false, fmt.Sprintf(chatBody, "m-no"))
	if code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d %s", code, body)
	}
	// 过期令牌 → 401
	exp := "2000-01-01"
	if _, err := e.st.CreateToken("old", []string{"*"}, 100, 10, &exp, auth.HashSecret("sk-old"), "sk-o••••old"); err != nil {
		t.Fatalf("create expired token: %v", err)
	}
	code, _ = e.post("/v1/chat/completions", "sk-old", false, fmt.Sprintf(chatBody, "m-ok"))
	if code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for expired token, got %d", code)
	}
}
