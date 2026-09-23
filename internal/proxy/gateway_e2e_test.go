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
	"time"

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

// anthropicUpstreamUsage 同 anthropicUpstream,但 usage 可定制(缓存写/缓存读计费用)。
func anthropicUpstreamUsage(t *testing.T, echo string, status int, usage map[string]any) *httptest.Server {
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
			"usage":       usage,
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
	if state, _ := e.gw.eng.CircuitState(ch.ID); state != engine.CircuitClosed {
		t.Fatalf("翻译失败被误记为渠道故障:渠道熔断态 = %s", state)
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
	// 成本口径改造后**派生于官方价**(不再读 offer 标量):该渠道无系数行 → ratio 1.0,
	// 故成本 = 官方价 ¥30/¥150 × 1.0 = ¥30/¥150 每百万 → 12×30/1e6 + 8×150/1e6 = 0.00156。
	// 售价(0.00078)是成本的一半 —— 倍率 0.5 < 系数 1.0 就该亏损,账面对得上。
	const wantCost = 0.00156
	if d := logs[0].CostUsd - wantCost; d > 1e-9 || d < -1e-9 {
		t.Fatalf("cost = %v, want %v(官方价 × 渠道系数)", logs[0].CostUsd, wantCost)
	}
	if logs[0].CostSource != string(CostFromOfficial) {
		t.Errorf("costSource = %q, want official", logs[0].CostSource)
	}
	// flat 形态不该记档位 —— 只有分时模型才有峰谷。
	if logs[0].PriceWindow != "" {
		t.Errorf("flat 模型不该有 priceWindow, got %q", logs[0].PriceWindow)
	}
}

// TestE2ECacheWriteBilledAtItsOwnPrice 缓存写(cache_creation)按**自己的价**计费,
// 不再折进输入 token 按输入价计 —— 补 m0013 的核心验收(issue #27 commit 2)。
//
// 上游是 Anthropic(客户端也是 anthropic 协议,走同协议 fast path),固定
// input=12 / output=8 / cache_creation=4 / cache_read=3。
// 官方价 USD in 3 / out 15 / cacheRead 0.3 / cacheWrite 3.75;计价 CNY 汇率 0.1 → 每百万
// ¥30/¥150/¥3/¥37.5。倍率 1、渠道系数 1(未设)= 收支同价,便于逐项核对。
//
//	成本 = 12×30 + 8×150 + 3×3 + 4×37.5 = 360+1200+9+150 = 1719(/1e6 = 0.001719)
//
// 若缓存写被折进 input(旧行为):prompt 会变成 16,成本 = 16×30+… = 0.001839 —— 高 0.00012,
// 且随缓存写 token 数线性放大。这条正是「成本被系统性低估」的回归钉子。
func TestE2ECacheWriteBilledAtItsOwnPrice(t *testing.T) {
	e := newE2E(t)
	up := anthropicUpstreamUsage(t, "pong", http.StatusOK, map[string]any{
		"input_tokens": 12, "output_tokens": 8,
		"cache_creation_input_tokens": 4, "cache_read_input_tokens": 3,
	})
	chID := e.addChannel("oa", domain.ProviderAnthropic, up.URL, "sk-oa", 1)
	model := "claude-sonnet-5"
	e.addModelOffer(model, chID, 1)

	e.bindOfficial(model, domain.ProviderAnthropic, "claude-sonnet-5-20250929", domain.OfficialPriceRow{
		Currency: domain.CurrencyUSD, BillingShape: domain.ShapeFlat,
		InputPrice: 3, OutputPrice: 15, CacheReadPrice: 0.3, CacheWritePrice: 3.75,
	})
	settings, err := e.st.GetSettings()
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	settings.DisplayCurrency = domain.CurrencyCNY
	settings.USDPerCNY = 0.1
	settings.PriceMultiplier = 1
	if err := e.st.SaveSettings(settings); err != nil {
		t.Fatalf("save settings: %v", err)
	}

	key := e.addToken("cli", []string{"*"}, 100)
	code, body := e.post("/v1/messages", key, true, fmt.Sprintf(messagesBody, model))
	if code != http.StatusOK {
		t.Fatalf("status %d body %s", code, body)
	}

	logs := e.logsFor()
	if len(logs) != 1 {
		t.Fatalf("logs = %+v", logs)
	}
	// 归一化口径:prompt 只含 input(12),缓存写单列 4,缓存读单列 3。
	if logs[0].InTokens != 12 || logs[0].CacheWrite != 4 || logs[0].CacheRead != 3 {
		t.Errorf("token 口径 = in:%d cw:%d cr:%d, want in 12 cw 4 cr 3",
			logs[0].InTokens, logs[0].CacheWrite, logs[0].CacheRead)
	}
	const want = 0.001719
	if d := logs[0].CostUsd - want; d > 1e-9 || d < -1e-9 {
		t.Fatalf("cost = %v, want %v(缓存写按自身价 4×¥37.5 计)", logs[0].CostUsd, want)
	}
	if d := logs[0].ChargeUsd - want; d > 1e-9 || d < -1e-9 {
		t.Fatalf("charge = %v, want %v(倍率 1)", logs[0].ChargeUsd, want)
	}
}

// TestE2ECacheWriteFallsBackToInputPrice 官方价没给缓存写价(0 = 无依据)时,
// 缓存写 token 按**输入价**计 —— 与「cache_creation 折进 input」的旧账面逐位一致,
// 所以补 m0013 对未提供该价的厂商不是回归。
//
// 同 fixture 但 CacheWritePrice=0:成本 = (12+4)×30 + 8×150 + 3×3 = 480+1200+9 = 1689。
func TestE2ECacheWriteFallsBackToInputPrice(t *testing.T) {
	e := newE2E(t)
	up := anthropicUpstreamUsage(t, "pong", http.StatusOK, map[string]any{
		"input_tokens": 12, "output_tokens": 8,
		"cache_creation_input_tokens": 4, "cache_read_input_tokens": 3,
	})
	chID := e.addChannel("oa", domain.ProviderAnthropic, up.URL, "sk-oa", 1)
	model := "claude-sonnet-5"
	e.addModelOffer(model, chID, 1)

	e.bindOfficial(model, domain.ProviderAnthropic, "claude-sonnet-5-20250929", domain.OfficialPriceRow{
		Currency: domain.CurrencyUSD, BillingShape: domain.ShapeFlat,
		InputPrice: 3, OutputPrice: 15, CacheReadPrice: 0.3, CacheWritePrice: 0, // 未给 → 回落 input
	})
	settings, err := e.st.GetSettings()
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	settings.DisplayCurrency = domain.CurrencyCNY
	settings.USDPerCNY = 0.1
	settings.PriceMultiplier = 1
	if err := e.st.SaveSettings(settings); err != nil {
		t.Fatalf("save settings: %v", err)
	}

	key := e.addToken("cli", []string{"*"}, 100)
	code, body := e.post("/v1/messages", key, true, fmt.Sprintf(messagesBody, model))
	if code != http.StatusOK {
		t.Fatalf("status %d body %s", code, body)
	}
	logs := e.logsFor()
	if len(logs) != 1 {
		t.Fatalf("logs = %+v", logs)
	}
	const want = 0.001689
	if d := logs[0].CostUsd - want; d > 1e-9 || d < -1e-9 {
		t.Fatalf("cost = %v, want %v(缓存写价缺失 → 按 input 价)", logs[0].CostUsd, want)
	}
}

// TestE2ECostFromOfficialTimesChannelRatio 「成本 = 官方价 × 渠道系数」的核心验收:
// 同一模型同一官方价,渠道系数 1/6 让成本变成官方价的六分之一(commandcode 的 $10 买 $60)。
// 同时断言毛利 = (倍率 − 系数) × 官方价,而不是「营收 − 假成本」。
func TestE2ECostFromOfficialTimesChannelRatio(t *testing.T) {
	e := newE2E(t)
	up := openaiUpstream(t, "pong", http.StatusOK)
	chID := e.addChannel("cc", domain.ProviderOpenAI, up.URL, "sk-cc", 1)
	model := "claude-sonnet-5"
	e.addModelOffer(model, chID, 1)

	// 官方价 USD 3/15;计价 CNY,汇率 0.1 → ¥30/¥150 每百万。
	e.bindOfficial(model, domain.ProviderAnthropic, "claude-sonnet-5-20250929", domain.OfficialPriceRow{
		Currency: domain.CurrencyUSD, BillingShape: domain.ShapeFlat,
		InputPrice: 3, OutputPrice: 15,
	})
	// 渠道对 Anthropic 的成本系数 1/6($10 买 $60 额度)。
	if err := e.st.SetCostRatio(chID, domain.ProviderAnthropic, 1.0/6.0, "$10→$60"); err != nil {
		t.Fatalf("set cost ratio: %v", err)
	}
	settings, _ := e.st.GetSettings()
	settings.DisplayCurrency = domain.CurrencyCNY
	settings.USDPerCNY = 0.1
	settings.PriceMultiplier = 1.0
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
	// 官方 ¥30/¥150:成本 = 官方 × 1/6 = ¥5/¥25 → 12×5/1e6 + 8×25/1e6 = 0.00026。
	const wantCost = 0.00026
	if d := logs[0].CostUsd - wantCost; d > 1e-9 || d < -1e-9 {
		t.Fatalf("cost = %v, want %v(官方价 × 1/6)", logs[0].CostUsd, wantCost)
	}
	// 售价 = 官方 × 1.0 → 12×30/1e6 + 8×150/1e6 = 0.00156。
	const wantCharge = 0.00156
	if d := logs[0].ChargeUsd - wantCharge; d > 1e-9 || d < -1e-9 {
		t.Fatalf("charge = %v, want %v(官方价 × 倍率)", logs[0].ChargeUsd, wantCharge)
	}
	// 毛利 = (倍率 − 系数) × 官方价 = (1 − 1/6) × 官方价 —— 结构性正确,而非「营收 − 假成本」。
	if gotRatio := logs[0].CostUsd / logs[0].ChargeUsd; gotRatio < 0.1666 || gotRatio > 0.1667 {
		t.Errorf("成本/售价 = %v, want 1/6", gotRatio)
	}
	if logs[0].CostSource != string(CostFromOfficial) {
		t.Errorf("costSource = %q, want official", logs[0].CostSource)
	}
}

// TestE2EPeakOffpeakSelectedByRequestTime 分时的核心:同一模型、同一渠道,
// 注入的计费时刻落在高峰 vs 空闲,成本与售价**同步**翻倍(官方高峰价 = 空闲价 × 2)。
func TestE2EPeakOffpeakSelectedByRequestTime(t *testing.T) {
	beijing := func(h, mi int) time.Time {
		return time.Date(2026, time.September, 14, h, mi, 0, 0, time.FixedZone("CST", 8*3600))
	}
	cases := []struct {
		name       string
		at         time.Time
		wantWindow string
		wantCharge float64
	}{
		// 周一 10:00 = 高峰:官方高峰 ¥2/¥8 → 12×2/1e6 + 8×8/1e6 = 0.000088。
		{"高峰时段", beijing(10, 0), "peak", 0.000088},
		// 周一 13:00 = 午休(空闲):官方空闲 ¥1/¥4 → 12×1/1e6 + 8×4/1e6 = 0.000044。
		{"空闲时段", beijing(13, 0), "offpeak", 0.000044},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newE2E(t)
			up := openaiUpstream(t, "pong", http.StatusOK)
			chID := e.addChannel("cc", domain.ProviderOpenAI, up.URL, "sk-cc", 1)
			model := "deepseek-flash"
			e.addModelOffer(model, chID, 1)

			// 官方价 CNY,分时形态,detail 带机器可读 windows(谷 ¥1/¥4,峰 ¥2/¥8)。
			e.bindOfficial(model, domain.ProviderDeepSeek, "deepseek-flash", domain.OfficialPriceRow{
				Currency: domain.CurrencyCNY, BillingShape: domain.ShapePeakOff,
				InputPrice: 1, OutputPrice: 4, CacheReadPrice: 0.02,
				Detail: map[string]any{
					"peak":      map[string]any{"in": 2.0, "out": 8.0, "cacheRead": 0.04},
					"offpeak":   map[string]any{"in": 1.0, "out": 4.0, "cacheRead": 0.02},
					"peakHours": "北京时间周一至周五 9:00-12:00、14:00-18:00(其余为空闲时段)",
					"windows": []any{
						map[string]any{"days": []any{1, 2, 3, 4, 5}, "start": "09:00", "end": "12:00", "tzOffsetMin": 480},
						map[string]any{"days": []any{1, 2, 3, 4, 5}, "start": "14:00", "end": "18:00", "tzOffsetMin": 480},
					},
				},
			})
			settings, _ := e.st.GetSettings()
			settings.DisplayCurrency = domain.CurrencyCNY
			settings.PriceMultiplier = 1.0
			if err := e.st.SaveSettings(settings); err != nil {
				t.Fatalf("save settings: %v", err)
			}
			// 计费基准时刻可注入:这正是 at 与 start 分开的价值(不影响耗时统计)。
			e.gw.nowFn = func() time.Time { return c.at }

			key := e.addToken("cli", []string{"*"}, 100)
			code, body := e.post("/v1/chat/completions", key, false, fmt.Sprintf(chatBody, model))
			if code != http.StatusOK {
				t.Fatalf("status %d body %s", code, body)
			}
			logs := e.logsFor()
			if len(logs) != 1 {
				t.Fatalf("logs = %+v", logs)
			}
			if logs[0].PriceWindow != c.wantWindow {
				t.Errorf("priceWindow = %q, want %q", logs[0].PriceWindow, c.wantWindow)
			}
			if d := logs[0].ChargeUsd - c.wantCharge; d > 1e-9 || d < -1e-9 {
				t.Errorf("charge = %v, want %v", logs[0].ChargeUsd, c.wantCharge)
			}
			// 成本与售价同步浮动:无系数行 → ratio = 倍率 = 1.0,故两者相等。
			// 关键断言是「同一档位下两者一起变」,而非某个固定值。
			if d := logs[0].CostUsd - logs[0].ChargeUsd; d > 1e-9 || d < -1e-9 {
				t.Errorf("ratio(1.0) == rate(1.0) 时成本应等于售价: cost=%v charge=%v",
					logs[0].CostUsd, logs[0].ChargeUsd)
			}
		})
	}
}

// TestE2ETieredPriceIgnoredBySelector 阶梯价必须按标量计,绝不消费 detail["tiers"]。
// 生产 166 行通义官方价的 tiers 是坏的(重复档位 + 空 range),这条是红线。
func TestE2ETieredPriceIgnoredBySelector(t *testing.T) {
	e := newE2E(t)
	up := openaiUpstream(t, "pong", http.StatusOK)
	chID := e.addChannel("qwen", domain.ProviderOpenAI, up.URL, "sk-qw", 1)
	model := "qwen3.8-max"
	e.addModelOffer(model, chID, 1)

	// 标量 ¥1/¥4;detail.tiers 是坏数据(重复档位、空 range)—— 必须被忽略。
	e.bindOfficial(model, domain.ProviderQwen, "qwen3.8-max", domain.OfficialPriceRow{
		Currency: domain.CurrencyCNY, BillingShape: domain.ShapeTiered,
		InputPrice: 1, OutputPrice: 4,
		Detail: map[string]any{
			"tiers": []any{
				map[string]any{"range": "0<Token≤32K", "in": 2.5},
				map[string]any{"range": "0<Token≤32K", "in": 8.807},
				map[string]any{"range": "", "in": 99.0},
			},
		},
	})
	settings, _ := e.st.GetSettings()
	settings.DisplayCurrency = domain.CurrencyCNY
	settings.PriceMultiplier = 1.0
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
	// 标量 ¥1/¥4 → 12×1/1e6 + 8×4/1e6 = 0.000044。若误消费 tiers 会算出别的数。
	const want = 0.000044
	if d := logs[0].ChargeUsd - want; d > 1e-9 || d < -1e-9 {
		t.Errorf("charge = %v, want %v(阶梯按首档标量计)", logs[0].ChargeUsd, want)
	}
	if logs[0].PriceWindow != "" {
		t.Errorf("tiered 不该有 priceWindow, got %q", logs[0].PriceWindow)
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

// TestE2EChannelRatioDefaultsToOne 未配系数的渠道按 1.0 计 —— 方向性选择:
// 高估成本只会让毛利看起来偏低(你会去查),低估成本会伪造利润(你不会去查)。
//
// 但 1.0 必须**可被察觉**:CostQuote 会带 warn,管理面「成本」列以告警色显示「系数未设」。
// 少了这个信号,用户看到的毛利会静默偏低而不知为何。
func TestE2EChannelRatioDefaultsToOne(t *testing.T) {
	e := newE2E(t)
	up := openaiUpstream(t, "pong", http.StatusOK)
	chID := e.addChannel("cc", domain.ProviderOpenAI, up.URL, "sk-cc", 1)
	model := "claude-sonnet-5"
	e.addModelOffer(model, chID, 1)

	// 绑定官方价,但**不设任何系数行**。
	e.bindOfficial(model, domain.ProviderAnthropic, "claude-sonnet-5-20250929", domain.OfficialPriceRow{
		Currency: domain.CurrencyUSD, BillingShape: domain.ShapeFlat,
		InputPrice: 3, OutputPrice: 15,
	})

	// 计价币种 USD(与官方同币种,不需要汇率)—— 本测试要钉的是**系数缺行**的回落,
	// 不该被「未设汇率」这条无关的回落盖过去。
	settings, _ := e.st.GetSettings()
	settings.DisplayCurrency = domain.CurrencyUSD
	settings.PriceMultiplier = 1.0
	if err := e.st.SaveSettings(settings); err != nil {
		t.Fatalf("save settings: %v", err)
	}

	// 直接点查热路径的取数口:无行必须报 ErrNotFound(而非悄悄返回 0 或 1.0)——
	// 「缺行」与「配了 1.0」在业务上不同(后者是主动核对过的不折扣),
	// 回落到 1.0 是**调用方**(costRatio)的判断,不是 store 的判断。
	// 两者混起来会让「未设系数」这条 warn 再也发不出来。
	_, err := e.st.ChannelVendorRatio(chID, domain.ProviderAnthropic)
	if err != store.ErrNotFound {
		t.Fatalf("ChannelVendorRatio err = %v, want ErrNotFound(缺行与配 1.0 必须可区分)", err)
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
	// 官方 $3/15,计价币种默认 USD(settings 未改),故派生可用 —— 走 official 档,
	// 且系数按 1.0 计,于是成本 = 官方价原值。
	if logs[0].CostSource != string(CostFromOfficial) {
		t.Fatalf("costSource = %q, want official", logs[0].CostSource)
	}
	// 成本 (12×3 + 8×15)/1e6 = 0.000156。这是「按 1.0 计」的直接后果。
	const wantCost = 0.000156
	if d := logs[0].CostUsd - wantCost; d > 1e-9 || d < -1e-9 {
		t.Fatalf("cost = %v, want %v(官方价 × 1.0)", logs[0].CostUsd, wantCost)
	}
	// 关键:成本不是 0 —— 0 会让毛利永远是假的 100%。
	if logs[0].CostUsd <= 0 {
		t.Errorf("成本 = %v,必须为正(0 会伪装成 100%% 毛利)", logs[0].CostUsd)
	}
}
