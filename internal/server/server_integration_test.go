package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"personal-ai-gateway/internal/config"
	"personal-ai-gateway/internal/secret"
	"personal-ai-gateway/internal/store"
)

// newTestServer 起真实 HTTP 服务(管理面+数据面共用 Handler)。
func newTestServer(t *testing.T) (*httptest.Server, *http.Client, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	if _, err := secret.BootstrapKey(dir); err != nil {
		t.Fatalf("bootstrap master key: %v", err)
	}
	st, err := store.Open(filepath.Join(dir, "gw.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	s := New(config.Config{}, st)
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	return srv, client, st
}

// doJSON 发 JSON 请求;cookie 由 client.Jar 自动携带。
func doJSON(t *testing.T, c *http.Client, method, url string, body any) (int, []byte) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, rd)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}

func decode[T any](t *testing.T, data []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("decode %s: %v", data, err)
	}
	return v
}

func mustStatus(t *testing.T, got, want int, ctx string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: status %d, want %d", ctx, got, want)
	}
}

func bootstrap(t *testing.T, c *http.Client, base string) {
	t.Helper()
	code, body := doJSON(t, c, http.MethodPost, base+"/api/v1/auth/bootstrap",
		map[string]any{"username": "admin", "password": "password123"})
	if code != http.StatusOK {
		t.Fatalf("bootstrap: %d %s", code, body)
	}
}

// TestAuthFlow 账号引导 + 登录/登出/me 全链路。
func TestAuthFlow(t *testing.T) {
	srv, c, _ := newTestServer(t)
	base := srv.URL

	// 未登录访问受保护端点 → 401
	code, _ := doJSON(t, c, http.MethodGet, base+"/api/v1/auth/me", nil)
	mustStatus(t, code, http.StatusUnauthorized, "me without login")

	// 首启:无管理员 → 创建并登录
	bootstrap(t, c, base)
	code, body := doJSON(t, c, http.MethodGet, base+"/api/v1/auth/me", nil)
	mustStatus(t, code, http.StatusOK, "me after bootstrap")
	me := decode[map[string]any](t, body)
	if me["username"] != "admin" {
		t.Fatalf("me.username = %v", me["username"])
	}

	// 二次 bootstrap → 409
	code, _ = doJSON(t, c, http.MethodPost, base+"/api/v1/auth/bootstrap",
		map[string]any{"username": "again", "password": "password123"})
	mustStatus(t, code, http.StatusConflict, "second bootstrap")

	// 登出后再取 me → 401
	code, _ = doJSON(t, c, http.MethodPost, base+"/api/v1/auth/logout", map[string]any{})
	mustStatus(t, code, http.StatusOK, "logout")
	code, _ = doJSON(t, c, http.MethodGet, base+"/api/v1/auth/me", nil)
	mustStatus(t, code, http.StatusUnauthorized, "me after logout")

	// 密码错 → 401;正确 → 200
	code, _ = doJSON(t, c, http.MethodPost, base+"/api/v1/auth/login",
		map[string]any{"username": "admin", "password": "wrong-pass"})
	mustStatus(t, code, http.StatusUnauthorized, "login wrong password")
	code, _ = doJSON(t, c, http.MethodPost, base+"/api/v1/auth/login",
		map[string]any{"username": "admin", "password": "password123"})
	mustStatus(t, code, http.StatusOK, "login ok")
}

// fakeUpstream 一个最小 openai 形状上游:GET /v1/models 固定模型表。
func fakeUpstream(t *testing.T, modelIDs []string) *httptest.Server {
	t.Helper()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/v1/models") {
			w.Header().Set("Content-Type", "application/json")
			data := make([]map[string]any, 0, len(modelIDs))
			for _, id := range modelIDs {
				data = append(data, map[string]any{"id": id, "object": "model"})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": "not found"}})
	})
	up := httptest.NewServer(h)
	t.Cleanup(up.Close)
	return up
}

// TestAdminCRUD 渠道/模型/供给源/规则/令牌/日志/用量/设置 全 CRUD 冒烟。
func TestAdminCRUD(t *testing.T) {
	srv, c, _ := newTestServer(t)
	base := srv.URL
	bootstrap(t, c, base)

	up := fakeUpstream(t, []string{"claude-fake-1", "claude-fake-2"})

	// —— 渠道:创建 → 列表 → 测试 → 同步模型 ——
	chBody := map[string]any{
		"name": "fake-oa", "provider": "OpenAI", "baseUrl": up.URL,
		"apiKey": "sk-test-1234567890abcd", "priority": 1, "weight": 100,
		"timeoutMs": 30000, "maxFailures": 3, "cooldownSec": 30, "tags": []string{"test"},
	}
	code, body := doJSON(t, c, http.MethodPost, base+"/api/v1/channels", chBody)
	mustStatus(t, code, http.StatusOK, "create channel")
	ch := decode[map[string]any](t, body)
	chID := int64(ch["id"].(float64))

	code, body = doJSON(t, c, http.MethodGet, base+"/api/v1/channels", nil)
	mustStatus(t, code, http.StatusOK, "list channels")
	list := decode[[]map[string]any](t, body)
	if len(list) != 1 || int64(list[0]["id"].(float64)) != chID {
		t.Fatalf("channel list = %v", list)
	}

	code, body = doJSON(t, c, http.MethodPost, fmt.Sprintf("%s/api/v1/channels/%d/test", base, chID), nil)
	mustStatus(t, code, http.StatusOK, "channel test")
	if tr := decode[map[string]any](t, body); tr["ok"] != true {
		t.Fatalf("test channel not ok: %s", body)
	}

	code, body = doJSON(t, c, http.MethodPost, fmt.Sprintf("%s/api/v1/channels/%d/sync-models", base, chID), nil)
	mustStatus(t, code, http.StatusOK, "sync models")
	sync := decode[map[string]any](t, body)
	if sync["added"].(float64) != 2 || sync["updated"].(float64) != 0 {
		t.Fatalf("sync resp = %s", body)
	}

	// 改名/降级优先级
	chBody["name"] = "fake-oa2"
	chBody["priority"] = 5
	code, body = doJSON(t, c, http.MethodPatch, fmt.Sprintf("%s/api/v1/channels/%d", base, chID), chBody)
	mustStatus(t, code, http.StatusOK, "update channel")
	if decode[map[string]any](t, body)["name"] != "fake-oa2" {
		t.Fatalf("channel name not updated: %s", body)
	}

	// —— 模型目录(同步出的两条)——
	code, body = doJSON(t, c, http.MethodGet, base+"/api/v1/models", nil)
	mustStatus(t, code, http.StatusOK, "list models")
	models := decode[[]map[string]any](t, body)
	if len(models) != 2 {
		t.Fatalf("models = %d", len(models))
	}
	// 每条模型都带同步出的 disabled offer
	offers := models[0]["offers"].([]any)
	if len(offers) != 1 {
		t.Fatalf("expected 1 auto offer, got %d", len(offers))
	}

	// 手动建模型 + 建渠道2 后挂 offer → 重排 → 停用 → 删除
	code, body = doJSON(t, c, http.MethodPost, base+"/api/v1/models",
		map[string]any{"name": "gpt-manual", "contextWindow": 128000, "capabilities": []string{"stream"}})
	mustStatus(t, code, http.StatusOK, "create model")
	manual := decode[map[string]any](t, body)
	manualID := int64(manual["id"].(float64))

	code, body = doJSON(t, c, http.MethodPost, base+"/api/v1/channels",
		map[string]any{"name": "offline", "provider": "DeepSeek", "baseUrl": "http://127.0.0.1:9", "apiKey": "sk-b"})
	mustStatus(t, code, http.StatusOK, "create channel2")
	ch2ID := int64(decode[map[string]any](t, body)["id"].(float64))

	code, body = doJSON(t, c, http.MethodPost, fmt.Sprintf("%s/api/v1/models/%d/offers", base, manualID),
		map[string]any{"channelId": ch2ID, "inputPriceUsd": 1.0, "outputPriceUsd": 2.0,
			"overridePrice": true, "rateLimitRpm": 60})
	mustStatus(t, code, http.StatusOK, "create offer")
	offer := decode[map[string]any](t, body)
	offerID := int64(offer["id"].(float64))

	code, body = doJSON(t, c, http.MethodGet, fmt.Sprintf("%s/api/v1/models/%d/usage?days=7", base, manualID), nil)
	mustStatus(t, code, http.StatusOK, "model usage")

	// 停用 offer → 目录 list 中该模型 offers 相应关闭
	code, body = doJSON(t, c, http.MethodPatch, fmt.Sprintf("%s/api/v1/offers/%d", base, offerID),
		map[string]any{"enabled": false})
	mustStatus(t, code, http.StatusOK, "disable offer")

	// 删除渠道2 → 其 offer 级联删除
	code, _ = doJSON(t, c, http.MethodDelete, fmt.Sprintf("%s/api/v1/channels/%d", base, ch2ID), nil)
	mustStatus(t, code, http.StatusOK, "delete channel2")

	// —— 路由规则 ——
	code, body = doJSON(t, c, http.MethodPost, base+"/api/v1/rules",
		map[string]any{"name": "claude-first", "matchMode": "prefix", "pattern": "claude-",
			"strategy": "priority", "channelIds": []int64{chID}, "fallbackChannelId": nil,
			"retry": 1, "timeoutMs": 60000})
	mustStatus(t, code, http.StatusOK, "create rule")
	rule := decode[map[string]any](t, body)
	ruleID := int64(rule["id"].(float64))

	code, body = doJSON(t, c, http.MethodGet, base+"/api/v1/rules", nil)
	mustStatus(t, code, http.StatusOK, "list rules")
	rules := decode[[]map[string]any](t, body)
	if len(rules) != 1 || int64(rules[0]["id"].(float64)) != ruleID {
		t.Fatalf("rules = %v", rules)
	}

	code, _ = doJSON(t, c, http.MethodPatch, fmt.Sprintf("%s/api/v1/rules/%d", base, ruleID),
		map[string]any{"enabled": false})
	mustStatus(t, code, http.StatusOK, "disable rule")
	code, _ = doJSON(t, c, http.MethodDelete, fmt.Sprintf("%s/api/v1/rules/%d", base, ruleID), nil)
	mustStatus(t, code, http.StatusOK, "delete rule")

	// —— 令牌 ——
	code, body = doJSON(t, c, http.MethodPost, base+"/api/v1/tokens",
		map[string]any{"name": "cli", "allowedModels": []string{"*"}, "quotaUsd": 5, "rpmLimit": 10})
	mustStatus(t, code, http.StatusOK, "create token")
	tok := decode[map[string]any](t, body)
	key, _ := tok["key"].(string)
	if !strings.HasPrefix(key, "sk-gw-") {
		t.Fatalf("token key = %q", key)
	}
	tokID := int64(tok["token"].(map[string]any)["id"].(float64))
	code, body = doJSON(t, c, http.MethodGet, base+"/api/v1/tokens", nil)
	mustStatus(t, code, http.StatusOK, "list tokens")
	if len(decode[[]map[string]any](t, body)) != 1 {
		t.Fatalf("tokens list")
	}
	code, _ = doJSON(t, c, http.MethodPatch, fmt.Sprintf("%s/api/v1/tokens/%d", base, tokID),
		map[string]any{"status": "disabled"})
	mustStatus(t, code, http.StatusOK, "disable token")
	code, _ = doJSON(t, c, http.MethodDelete, fmt.Sprintf("%s/api/v1/tokens/%d", base, tokID), nil)
	mustStatus(t, code, http.StatusOK, "delete token")

	// —— 用量/概览/日志/设置 ——
	for _, path := range []string{"/api/v1/overview", "/api/v1/usage?dim=model&days=7", "/api/v1/logs?page=1&size=20", "/api/v1/settings"} {
		code, body := doJSON(t, c, http.MethodGet, base+path, nil)
		mustStatus(t, code, http.StatusOK, "GET "+path)
		if len(body) == 0 {
			t.Fatalf("GET %s empty body", path)
		}
	}
	code, body = doJSON(t, c, http.MethodPatch, base+"/api/v1/settings",
		map[string]any{"requestTimeoutMs": 30000, "maxRetries": 2, "degradeOnError": true,
			"logRetentionDays": 7, "sampleRatePct": 100, "tzOffsetMin": 480})
	mustStatus(t, code, http.StatusOK, "patch settings")
	code, _ = doJSON(t, c, http.MethodDelete, base+"/api/v1/logs", nil)
	mustStatus(t, code, http.StatusOK, "clear logs")
}

// TestChannelQuota 渠道额度接口全链路:正常解析 / 非 ok 窗口略去 / Anthropic 判不支持。
func TestChannelQuota(t *testing.T) {
	srv, c, _ := newTestServer(t)
	base := srv.URL
	bootstrap(t, c, base)

	var hitUsage bool
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/v1/usage") {
			hitUsage = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"usage":{
				"rolling":{"status":"ok","percent":12},
				"weekly":{"status":"ok","percent":34},
				"monthly":{"status":"error","percent":0}
			}}`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(up.Close)

	code, body := doJSON(t, c, http.MethodPost, base+"/api/v1/channels", map[string]any{
		"name": "q-oa", "provider": "OpenAI", "baseUrl": up.URL, "apiKey": "sk-quota",
	})
	mustStatus(t, code, http.StatusOK, "create channel")
	chID := int64(decode[map[string]any](t, body)["id"].(float64))

	code, body = doJSON(t, c, http.MethodGet, fmt.Sprintf("%s/api/v1/channels/%d/quota", base, chID), nil)
	mustStatus(t, code, http.StatusOK, "channel quota")
	if !hitUsage {
		t.Fatal("upstream /v1/usage was not called")
	}
	q := decode[map[string]any](t, body)
	if q["available"] != true {
		t.Fatalf("quota available = %v: %s", q["available"], body)
	}
	windows, ok := q["windows"].(map[string]any)
	if !ok {
		t.Fatalf("quota windows missing: %s", body)
	}
	if len(windows) != 2 {
		t.Fatalf("windows = %v, want only rolling/weekly (monthly status=error dropped)", windows)
	}
	if r, ok := windows["rolling"].(map[string]any); !ok || r["status"] != "ok" || r["percent"].(float64) != 12 {
		t.Fatalf("rolling window = %v", windows["rolling"])
	}

	// Anthropic:协议无额度接口 → available=false + error(不打上游)
	code, body = doJSON(t, c, http.MethodPost, base+"/api/v1/channels", map[string]any{
		"name": "q-ant", "provider": "Anthropic", "baseUrl": up.URL, "apiKey": "sk-x",
	})
	mustStatus(t, code, http.StatusOK, "create anthropic channel")
	antID := int64(decode[map[string]any](t, body)["id"].(float64))
	code, body = doJSON(t, c, http.MethodGet, fmt.Sprintf("%s/api/v1/channels/%d/quota", base, antID), nil)
	mustStatus(t, code, http.StatusOK, "anthropic quota")
	aq := decode[map[string]any](t, body)
	if aq["available"] != false {
		t.Fatalf("anthropic quota available = %v, want false: %s", aq["available"], body)
	}
	if msg, _ := aq["error"].(string); msg == "" {
		t.Fatalf("anthropic quota error missing: %s", body)
	}
}
