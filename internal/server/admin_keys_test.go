package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"personal-ai-gateway/internal/config"
)

// apiKeyReq 发一次请求;secret 走 x-api-key(空则用 config testKey)。
func apiKeyReq(t *testing.T, base, method, path, secret, body string) (int, []byte) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, _ := http.NewRequestWithContext(context.Background(), method, base+path, rd)
	if secret == "" {
		secret = testKey
	}
	req.Header.Set("x-api-key", secret)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func TestKeyManagementAPI(t *testing.T) {
	ts, _, _ := newUpstreamAPI(t, nil)

	// 空列表
	code, body := apiKeyReq(t, ts.URL, "GET", "/api/v1/keys", "", "")
	if code != 200 {
		t.Fatalf("list: %d %s", code, body)
	}
	if data := decodeObj(t, body)["data"].([]any); len(data) != 0 {
		t.Fatalf("want empty list, got %v", data)
	}

	// 创建:201 带一次性 secret;行里绝无 sha256
	code, body = apiKeyReq(t, ts.URL, "POST", "/api/v1/keys", "", `{"name":"ci","note":"跑 CI"}`)
	if code != 201 {
		t.Fatalf("create: %d %s", code, body)
	}
	row := decodeObj(t, body)
	secret, _ := row["secret"].(string)
	if secret == "" || !strings.HasPrefix(secret, "sk-gw-") {
		t.Fatalf("create missing secret: %v", row)
	}
	if row["name"] != "ci" || row["note"] != "跑 CI" || row["revoked"] != false {
		t.Errorf("row fields wrong: %v", row)
	}
	if row["prefix"] != secret[:12] {
		t.Errorf("prefix=%v want %q", row["prefix"], secret[:12])
	}
	if _, has := row["sha256"]; has {
		t.Errorf("row must not expose sha256")
	}

	// 列表:有该行,但 secret 绝不出现
	code, body = apiKeyReq(t, ts.URL, "GET", "/api/v1/keys", "", "")
	if code != 200 {
		t.Fatalf("list: %d %s", code, body)
	}
	rows := decodeObj(t, body)["data"].([]any)
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	listRow := rows[0].(map[string]any)
	if _, has := listRow["secret"]; has {
		t.Errorf("list leaked secret")
	}
	if listRow["name"] != "ci" || listRow["revoked"] != false {
		t.Errorf("list row wrong: %v", listRow)
	}

	// 校验与冲突
	if code, _ := apiKeyReq(t, ts.URL, "POST", "/api/v1/keys", "", `{"name":"ci"}`); code != 409 {
		t.Errorf("duplicate name want 409 got %d", code)
	}
	if code, _ := apiKeyReq(t, ts.URL, "POST", "/api/v1/keys", "", `{"name":"laptop"}`); code != 409 {
		t.Errorf("config-key name collision want 409 got %d", code)
	}
	if code, _ := apiKeyReq(t, ts.URL, "POST", "/api/v1/keys", "", `{"name":"has space"}`); code != 400 {
		t.Errorf("bad name want 400 got %d", code)
	}
	if code, _ := apiKeyReq(t, ts.URL, "POST", "/api/v1/keys", "", `{"name":""}`); code != 400 {
		t.Errorf("empty name want 400 got %d", code)
	}

	// 吊销 + 幂等 + 404
	code, body = apiKeyReq(t, ts.URL, "POST", "/api/v1/keys/ci/revoke", "", "")
	if code != 200 {
		t.Fatalf("revoke: %d %s", code, body)
	}
	rv := decodeObj(t, body)
	if rv["revoked"] != true || rv["revoked_at"] == nil {
		t.Errorf("revoke row wrong: %v", rv)
	}
	if code, body = apiKeyReq(t, ts.URL, "POST", "/api/v1/keys/ci/revoke", "", ""); code != 200 {
		t.Errorf("re-revoke want 200 got %d", code)
	} else if got := decodeObj(t, body)["revoked_at"]; got != rv["revoked_at"] {
		t.Errorf("re-revoke changed revoked_at: %v → %v", rv["revoked_at"], got)
	}
	if code, _ := apiKeyReq(t, ts.URL, "POST", "/api/v1/keys/nope/revoke", "", ""); code != 404 {
		t.Errorf("revoke missing want 404 got %d", code)
	}
}

// 生成 key 的作用域:模型面 /v1/* 放行、写 client_key 归属;管理面 /api/* 一律 401;吊销即失效。
func TestKeyScopeAndAttribution(t *testing.T) {
	var gotPath string
	var gotAuth string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`))
	}))
	defer fake.Close()

	seed := []config.Upstream{{
		Name: "fake", Type: config.TypeOpenAI, BaseURL: fake.URL + "/v1", APIKey: "sk-upstream", Priority: 1, Models: []string{"*"},
	}}
	ts, _, st := newUpstreamAPI(t, seed)

	// 建一个激活的模型面 key
	code, body := apiKeyReq(t, ts.URL, "POST", "/api/v1/keys", "", `{"name":"modelkey","note":"cc 用"}`)
	if code != 201 {
		t.Fatalf("create: %d %s", code, body)
	}
	gen := decodeObj(t, body)["secret"].(string)

	// 模型面 key 访问管理面 → 401(与无效 key 同表现)
	if code, _ := apiKeyReq(t, ts.URL, "GET", "/api/v1/keys", gen, ""); code != 401 {
		t.Errorf("generated key on /api want 401 got %d", code)
	}

	// 模型面 key 打 /v1/chat/completions → 200,request_log.client_key = 生成的 key 名
	code, body = apiKeyReq(t, ts.URL, "POST", "/v1/chat/completions", gen,
		`{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	if code != 200 {
		t.Fatalf("model call: %d %s", code, body)
	}
	if gotPath != "/v1/chat/completions" || gotAuth != "Bearer sk-upstream" {
		t.Errorf("outbound path=%q auth=%q", gotPath, gotAuth)
	}
	recent, err := st.Recent(3)
	if err != nil || len(recent) == 0 {
		t.Fatalf("recent: %v", err)
	}
	top := recent[0]
	if top.ClientKey != "modelkey" || top.Protocol != "openai" || top.Upstream != "fake" || top.Status != 200 {
		t.Errorf("request_log attribution wrong: %+v", top)
	}
	if top.PromptTokens != 10 || top.CompletionTokens != 2 {
		t.Errorf("usage not parsed: %+v", top)
	}

	// 吊销后,同 key 模型调用 → 401
	if code, _ := apiKeyReq(t, ts.URL, "POST", "/api/v1/keys/modelkey/revoke", "", ""); code != 200 {
		t.Fatalf("revoke: %d", code)
	}
	if code, _ := apiKeyReq(t, ts.URL, "POST", "/v1/chat/completions", gen,
		`{"model":"m","messages":[{"role":"user","content":"hi"}]}`); code != 401 {
		t.Errorf("revoked key model call want 401 got %d", code)
	}
}
