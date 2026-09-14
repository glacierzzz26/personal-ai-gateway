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
	"sync/atomic"
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

// setModelRate 给模型设售价倍率(定价按模型,不再是用户级)。
func (e *e2eEnv) setModelRate(modelID int64, rate float64) {
	e.t.Helper()
	m, err := e.st.GetModel(modelID)
	if err != nil {
		e.t.Fatalf("get model %d: %v", modelID, err)
	}
	_, err = e.st.UpdateModel(modelID, domain.ModelInput{
		Name: m.Name, ContextWindow: m.ContextWindow, Capabilities: m.Capabilities,
		Enabled: boolPtr(m.Enabled), RateOverride: domain.SetFloat(rate),
	})
	if err != nil {
		e.t.Fatalf("set model rate: %v", err)
	}
}

// addToken 建高额令牌返回明文(请求鉴权头用)。
func (e *e2eEnv) addToken(name string, allowed []string, quota float64) string {
	e.t.Helper()
	plain, hashed, err := auth.NewModelKey()
	if err != nil {
		e.t.Fatalf("new key: %v", err)
	}
	if _, err := e.st.CreateToken(name, nil, "", allowed, quota, 1000, nil, hashed, domain.MaskKey(plain)); err != nil {
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
// TestE2EWalletChargeAndGate 归属客户(user)的令牌:按售价扣钱包、记 charge 流水;
// 余额耗尽后下一笔在入口被 402(insufficient_balance)挡住。
func TestE2EWalletChargeAndGate(t *testing.T) {
	e := newE2E(t)
	up := openaiUpstream(t, "pong", http.StatusOK)
	chID := e.addChannel("oa", domain.ProviderOpenAI, up.URL, "sk-up", 1)
	mid := e.addModelOffer("m-w", chID, 1)

	u, err := e.st.CreateAdmin("cust", "h", domain.RoleUser)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	// 倍率 2.0(按模型):售价 = 成本 × 2。
	e.setModelRate(mid, 2.0)
	// 先只给一点点余额,让一笔就扣穿。
	if _, err := e.st.TopupBalance(u.ID, 0.00002, "seed"); err != nil {
		t.Fatalf("topup: %v", err)
	}
	plain, hashed, _ := auth.NewModelKey()
	if _, err := e.st.CreateToken("cust-key", &u.ID, "", []string{"*"}, 0, 1000, nil, hashed, domain.MaskKey(plain)); err != nil {
		t.Fatalf("create token: %v", err)
	}

	code, body := e.post("/v1/chat/completions", plain, false, fmt.Sprintf(chatBody, "m-w"))
	if code != http.StatusOK {
		t.Fatalf("first request should succeed, got %d %s", code, body)
	}
	bal, err := e.st.GetBalance(u.ID)
	if err != nil {
		t.Fatalf("balance: %v", err)
	}
	if bal >= 0.00002 {
		t.Fatalf("balance should have decreased, got %v", bal)
	}
	logs := e.logsFor()
	if len(logs) != 1 || logs[0].ChargeUsd <= 0 {
		t.Fatalf("charge not recorded: %+v", logs)
	}
	// 售价应为成本的 2 倍。
	if d := logs[0].ChargeUsd - 2*logs[0].CostUsd; d > 1e-9 || d < -1e-9 {
		t.Fatalf("charge %v should be 2x cost %v", logs[0].ChargeUsd, logs[0].CostUsd)
	}
	recs, err := e.st.ListBalanceLogs(u.ID, 10)
	if err != nil || len(recs) == 0 {
		t.Fatalf("balance logs: %v (%d)", err, len(recs))
	}
	if recs[0].Reason != "charge" {
		t.Fatalf("latest balance log reason = %s, want charge", recs[0].Reason)
	}

	// 余额已扣成负数 → 下一笔 402。
	code, body = e.post("/v1/chat/completions", plain, false, fmt.Sprintf(chatBody, "m-w"))
	if code != http.StatusPaymentRequired {
		t.Fatalf("second request should be 402, got %d %s", code, body)
	}
}

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

// TestE2ERenamedModelRoutesAndRewrites 统一名重命名:客户端用统一名请求,选路成功,
// 且出站请求体的 model 改回渠道侧真实名;日志按统一名归因;/v1/models 返回统一名。
func TestE2ERenamedModelRoutesAndRewrites(t *testing.T) {
	e := newE2E(t)
	var gotModel string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		var req struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		gotModel = req.Model
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-rn", "object": "chat.completion", "model": req.Model,
			"choices": []any{map[string]any{"index": 0,
				"message":       map[string]any{"role": "assistant", "content": "pong-renamed"},
				"finish_reason": "stop"}},
			"usage": map[string]any{"prompt_tokens": 5, "completion_tokens": 3},
		})
	}))
	t.Cleanup(up.Close)

	chID := e.addChannel("oa", domain.ProviderOpenAI, up.URL, "sk-up", 1)
	en := true
	origin := "deepseek-chat"
	display := "deepseek-v3"
	if _, err := e.st.CreateModel(domain.ModelInput{Name: origin, DisplayName: &display, Enabled: &en}); err != nil {
		t.Fatalf("create renamed model: %v", err)
	}
	m, _ := e.st.GetModelByName(origin)
	if _, err := e.st.CreateOffer(m.ID, domain.OfferInput{ChannelID: chID, Enabled: &en, RateLimitRpm: 1000}); err != nil {
		t.Fatalf("create offer: %v", err)
	}
	key := e.addToken("cli", []string{"*"}, 100)

	code, body := e.post("/v1/chat/completions", key, false, fmt.Sprintf(chatBody, display))
	if code != http.StatusOK {
		t.Fatalf("status %d body %s", code, body)
	}
	if gotModel != origin {
		t.Fatalf("outbound model = %q, want channel origin %q", gotModel, origin)
	}
	logs := e.logsFor()
	if len(logs) != 1 || logs[0].Model != display {
		t.Fatalf("logs should be attributed to display name %q: %+v", display, logs)
	}

	// /v1/models 返回统一名而非真实名
	req, _ := http.NewRequest(http.MethodGet, e.srv.URL+"/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("list models: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), display) || strings.Contains(string(raw), origin) {
		t.Fatalf("/v1/models should expose display name only: %s", raw)
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
	if _, err := e.st.CreateToken("old", nil, "", []string{"*"}, 100, 10, &exp, auth.HashSecret("sk-old"), "sk-o••••old"); err != nil {
		t.Fatalf("create expired token: %v", err)
	}
	code, _ = e.post("/v1/chat/completions", "sk-old", false, fmt.Sprintf(chatBody, "m-ok"))
	if code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for expired token, got %d", code)
	}
}

// TestE2EQuotaOverrunChargesAndRejects 额度不足以覆盖一笔成本时的终态:
// 该笔照常成功并记账(used 越过 quota),下一笔在入口稳定 402 —— 不再无限白跑。
// 回归点:ChargeToken 若带「不超上限」条件 + 调用方吞错,used 永不前进 → 每笔都 200。
func TestE2EQuotaOverrunChargesAndRejects(t *testing.T) {
	e := newE2E(t)
	up := openaiUpstream(t, "pong", http.StatusOK)
	chID := e.addChannel("oa", domain.ProviderOpenAI, up.URL, "sk-up", 1)
	e.addModelOffer("m-ok", chID, 1)
	// 每笔成本 ≈ 2.0*12/1e6 + 4.0*8/1e6 = 0.000056;额度设 0.00001(不足一笔)。
	key := e.addToken("cli", []string{"m-ok"}, 0.00001)

	code, body := e.post("/v1/chat/completions", key, false, fmt.Sprintf(chatBody, "m-ok"))
	if code != http.StatusOK {
		t.Fatalf("first request should succeed (quota not yet exhausted), got %d %s", code, body)
	}
	tk, err := e.st.ListTokens(nil)
	if err != nil || len(tk) != 1 {
		t.Fatalf("list tokens: %v (%d)", err, len(tk))
	}
	if tk[0].UsedUsd <= tk[0].QuotaUsd {
		t.Fatalf("used=%v should exceed quota=%v after settle", tk[0].UsedUsd, tk[0].QuotaUsd)
	}

	code, body = e.post("/v1/chat/completions", key, false, fmt.Sprintf(chatBody, "m-ok"))
	if code != http.StatusPaymentRequired {
		t.Fatalf("second request should be 402, got %d %s", code, body)
	}
}

// TestE2ERetryRepeatsCandidates 配置的 retry 轮数应真的重跑候选(此前 Plan.Retry 算了没人读)。// 单渠道、上游前两次 500、第三次 200,默认 maxRetries=2 → 序列 [ch,ch,ch],第三次成功。
func TestE2ERetryRepeatsCandidates(t *testing.T) {
	e := newE2E(t)
	var hits int32
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		if n < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = fmt.Fprint(w, `{"error":{"message":"boom","type":"server_error"}}`)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-r", "object": "chat.completion", "model": "m",
			"choices": []any{map[string]any{"index": 0,
				"message":       map[string]any{"role": "assistant", "content": "ok"},
				"finish_reason": "stop"}},
			"usage": map[string]any{"prompt_tokens": 3, "completion_tokens": 2},
		})
	})
	up := httptest.NewServer(h)
	t.Cleanup(up.Close)
	chID := e.addChannel("flaky", domain.ProviderOpenAI, up.URL, "sk-up", 1)
	model := "m-retry"
	e.addModelOffer(model, chID, 1)
	key := e.addToken("cli", []string{"*"}, 100)

	code, body := e.post("/v1/chat/completions", key, false, fmt.Sprintf(chatBody, model))
	if code != http.StatusOK {
		t.Fatalf("status %d body %s", code, body)
	}
	if got := atomic.LoadInt32(&hits); got != 3 {
		t.Fatalf("上游被调用 %d 次, want 3(1 轮 + 2 重试)", got)
	}
}

// TestE2ETranslationErrorNotChannelFailure 跨协议响应翻译失败(网关侧问题)不应计入渠道健康/熔断。
// anthropic 渠道回 200 但体非合法 anthropic message → o2a 翻译失败;maxFailures=1,若误记则渠道已熔断。
func TestE2ETranslationErrorNotChannelFailure(t *testing.T) {
	e := newE2E(t)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `not-a-json`)
	}))
	t.Cleanup(up.Close)
	en := true
	ch, err := e.st.CreateChannel(domain.ChannelInput{
		Name: "bad-shape", Provider: domain.ProviderAnthropic, BaseURL: up.URL, APIKey: "sk-up",
		Enabled: &en, TimeoutMs: 30000, MaxFailures: 1, CooldownSec: 10,
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	model := "m-badconv"
	e.addModelOffer(model, ch.ID, 1)
	key := e.addToken("cli", []string{"*"}, 100)

	code, body := e.post("/v1/chat/completions", key, false, fmt.Sprintf(chatBody, model))
	if code != http.StatusBadGateway {
		t.Fatalf("status = %d body %s, want 502(翻译失败)", code, body)
	}
	if open, _ := e.gw.eng.CircuitOpen(ch.ID); open {
		t.Fatalf("翻译失败被误记为渠道故障:渠道已熔断")
	}
}

// bindOfficial 把模型绑定到一条官方价,并返回该官方价行。
func (e *e2eEnv) bindOfficial(model string, p domain.Provider, officialName string, q domain.OfficialPriceRow) {
	e.t.Helper()
	q.Provider, q.ModelName = p, officialName
	if _, err := e.st.UpsertOfficialPrice(q); err != nil {
		e.t.Fatalf("upsert official price: %v", err)
	}
	vendor, name := string(p), officialName
	m, err := e.st.GetModelByName(model)
	if err != nil {
		e.t.Fatalf("get model %s: %v", model, err)
	}
	if _, err := e.st.UpdateModel(m.ID, domain.ModelInput{OfficialVendor: &vendor, OfficialModelName: &name}); err != nil {
		e.t.Fatalf("bind official: %v", err)
	}
}

// TestE2ERetailPriceBilledFromOfficial 有官方价绑定时,客户付的是「官方价 × 倍率」,
// 而不是成本 × 倍率 —— 定价模型的 A 口径(见 PLAN.md §2)。
func TestE2ERetailPriceBilledFromOfficial(t *testing.T) {
	e := newE2E(t)
	up := openaiUpstream(t, "pong", http.StatusOK)
	chID := e.addChannel("oa", domain.ProviderOpenAI, up.URL, "sk-up", 1)
	model := "claude-sonnet-5"
	e.addModelOffer(model, chID, 1) // 成本固定 2.0 / 4.0 每百万

	// 官方价 USD 3/15;计价币种 CNY,汇率 0.1 → ¥30/¥150 每百万。
	e.bindOfficial(model, domain.ProviderAnthropic, "claude-sonnet-5-20250929", domain.OfficialPriceRow{
		Currency: domain.CurrencyUSD, BillingShape: domain.ShapeFlat,
		InputPrice: 3, OutputPrice: 15,
	})
	settings, err := e.st.GetSettings()
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	settings.DisplayCurrency = domain.CurrencyCNY
	settings.USDPerCNY = 0.1
	settings.PriceMultiplier = 0.5
	if err := e.st.SaveSettings(settings); err != nil {
		t.Fatalf("save settings: %v", err)
	}

	key := e.addToken("cli", []string{"*"}, 100)
	code, body := e.post("/v1/chat/completions", key, false, fmt.Sprintf(chatBody, model))
	if code != http.StatusOK {
		t.Fatalf("status %d body %s", code, body)
	}

	logs := e.logsFor()
	if len(logs) != 1 {
		t.Fatalf("logs = %+v", logs)
	}
	// 假上游固定 usage:prompt=12, completion=8。
	// 官方价 ¥30/¥150 × 0.5 = ¥15/¥75 每百万 → 12×15/1e6 + 8×75/1e6 = 0.00078。
	const wantCharge = 0.00078
	if d := logs[0].ChargeUsd - wantCharge; d > 1e-9 || d < -1e-9 {
		t.Fatalf("charge = %v, want %v(官方价 × 倍率)", logs[0].ChargeUsd, wantCharge)
	}
	// 成本口径(prompt 12 × 2 + completion 8 × 4)/1e6 = 0.000056,与售价不同 ——
	// 这正是「成本 ≠ 售价」的证明。
	if d := logs[0].CostUsd - 0.000056; d > 1e-9 || d < -1e-9 {
		t.Fatalf("cost = %v, want 0.000056", logs[0].CostUsd)
	}
}

// TestE2ENoOfficialFallsBackToCost 未绑定官方价的模型照常按「成本 × 倍率」计费,不漏收。
func TestE2ENoOfficialFallsBackToCost(t *testing.T) {
	e := newE2E(t)
	up := openaiUpstream(t, "pong", http.StatusOK)
	chID := e.addChannel("oa", domain.ProviderOpenAI, up.URL, "sk-up", 1)
	model := "m-nopricing"
	e.addModelOffer(model, chID, 1)

	settings, _ := e.st.GetSettings()
	settings.PriceMultiplier = 2.0
	if err := e.st.SaveSettings(settings); err != nil {
		t.Fatalf("save settings: %v", err)
	}

	key := e.addToken("cli", []string{"*"}, 100)
	code, body := e.post("/v1/chat/completions", key, false, fmt.Sprintf(chatBody, model))
	if code != http.StatusOK {
		t.Fatalf("status %d body %s", code, body)
	}
	logs := e.logsFor()
	if len(logs) != 1 {
		t.Fatalf("logs = %+v", logs)
	}
	// 成本 (12×2 + 8×4)/1e6 = 0.000056 → ×2 = 0.000112。
	const want = 0.000112
	if d := logs[0].ChargeUsd - want; d > 1e-9 || d < -1e-9 {
		t.Fatalf("charge = %v, want %v(回落成本 × 倍率)", logs[0].ChargeUsd, want)
	}
}
