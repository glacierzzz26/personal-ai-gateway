package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"personal-ai-gateway/internal/config"
	"personal-ai-gateway/internal/pricing"
	"personal-ai-gateway/internal/proxy"
	"personal-ai-gateway/internal/router"
	"personal-ai-gateway/internal/store"
)

// newUpstreamAPI:seed 写入 DB(权威 raw),router 用同 seed 的 resolved 态构造,保持两者一致。
func newUpstreamAPI(t *testing.T, seed []config.Upstream) (*httptest.Server, *router.Router, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/gw.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.ReplaceUpstreams(seed); err != nil {
		t.Fatal(err)
	}
	rt := router.New(config.ResolveUpstreams(seed))
	cfg := config.Config{Keys: []config.Key{{Name: "laptop", Secret: testKey}}}
	gw := proxy.New(rt, st, pricing.New(nil))
	ts := httptest.NewServer(New(&cfg, gw, nil).Handler())
	t.Cleanup(ts.Close)
	return ts, rt, st
}

func apiJSON(t *testing.T, base, method, path, body string) (int, []byte) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, _ := http.NewRequestWithContext(context.Background(), method, base+path, rd)
	req.Header.Set("Authorization", "Bearer "+testKey)
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

func mustJSON(t *testing.T, v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestUpstreamCRUD(t *testing.T) {
	upA := config.Upstream{Name: "a", Type: config.TypeOpenAI, BaseURL: "https://x.example/v1", APIKey: "sk-real-secret-0001", Priority: 1}
	ts, rt, st := newUpstreamAPI(t, []config.Upstream{upA})

	// GET 列表:掩码 api_key,不回显明文;env 引用原样
	code, body := apiJSON(t, ts.URL, "GET", "/api/v1/upstreams", "")
	if code != 200 {
		t.Fatalf("list: %d %s", code, body)
	}
	row := decodeObj(t, body)["data"].([]any)[0].(map[string]any)
	if got := row["api_key"].(string); got == "sk-real-secret-0001" || !strings.Contains(got, "…") {
		t.Errorf("list leaked key: %q", got)
	}
	if got := row["name"].(string); got != "a" {
		t.Errorf("name=%q", got)
	}

	// POST 新增:env-ref key 与 plain key 都收;缺 api_key → 400;重名 → 409
	code, _ = apiJSON(t, ts.URL, "POST", "/api/v1/upstreams",
		mustJSON(t, config.Upstream{Name: "b", Type: config.TypeAnthropic, BaseURL: "${ANTHROPIC_BASE_URL}", APIKey: "${ANTHROPIC_API_KEY}", Priority: 2}))
	if code != 201 {
		t.Fatalf("create b: %d", code)
	}
	// store 落盘的是 raw,env 引用原样
	if ups, _ := st.LoadUpstreams(); len(ups) != 2 || ups[1].APIKey != "${ANTHROPIC_API_KEY}" {
		t.Fatalf("raw not preserved on create: %+v", ups)
	}
	code, _ = apiJSON(t, ts.URL, "POST", "/api/v1/upstreams",
		mustJSON(t, config.Upstream{Name: "c", Type: config.TypeOpenAI, BaseURL: "https://x/v1"}))
	if code != 400 {
		t.Errorf("missing api_key want 400 got %d", code)
	}
	code, _ = apiJSON(t, ts.URL, "POST", "/api/v1/upstreams",
		mustJSON(t, config.Upstream{Name: "b", Type: config.TypeOpenAI, BaseURL: "https://x/v1", APIKey: "k"}))
	if code != 409 {
		t.Errorf("duplicate want 409 got %d", code)
	}

	// PUT 修改 a:换 base_url、api_key 留空 → 密钥保持不变,router 同步换 resolved
	code, body = apiJSON(t, ts.URL, "PUT", "/api/v1/upstreams/a",
		mustJSON(t, config.Upstream{Name: "a", Type: config.TypeOpenAI, BaseURL: "https://new.example/v1", Priority: 5}))
	if code != 200 {
		t.Fatalf("update a: %d %s", code, body)
	}
	if ups, _ := st.LoadUpstreams(); ups[0].APIKey != "sk-real-secret-0001" || ups[0].BaseURL != "https://new.example/v1" {
		t.Fatalf("update kept key but changed base wrong: %+v", ups[0])
	}
	if got := rt.All(); len(got) != 2 {
		t.Fatalf("router sync count=%d", len(got))
	} else {
		var a *config.Upstream
		for _, u := range got {
			if u.Name == "a" {
				a = u
			}
		}
		if a == nil || a.BaseURL != "https://new.example/v1" {
			t.Fatalf("router not applied update: %+v", got)
		}
	}
	// PUT 带新 key → 更新
	code, _ = apiJSON(t, ts.URL, "PUT", "/api/v1/upstreams/a",
		mustJSON(t, config.Upstream{Name: "a", Type: config.TypeOpenAI, BaseURL: "https://new.example/v1", APIKey: "sk-rotated-999", Priority: 5}))
	if code != 200 {
		t.Fatalf("update a key: %d", code)
	}
	if ups, _ := st.LoadUpstreams(); ups[0].APIKey != "sk-rotated-999" {
		t.Errorf("key not rotated in store: %+v", ups[0].APIKey)
	}
	// PUT 不存在 → 404
	if code, _ := apiJSON(t, ts.URL, "PUT", "/api/v1/upstreams/nope",
		mustJSON(t, config.Upstream{Type: config.TypeOpenAI, BaseURL: "https://x/v1", APIKey: "k"})); code != 404 {
		t.Errorf("update missing want 404 got %d", code)
	}

	// DELETE b → 204,剩 1 条;再删最后一条 → 409;删不存在 → 404
	if code, _ := apiJSON(t, ts.URL, "DELETE", "/api/v1/upstreams/b", ""); code != 204 {
		t.Errorf("delete b want 204 got %d", code)
	}
	if ups, _ := st.LoadUpstreams(); len(ups) != 1 || ups[0].Name != "a" {
		t.Fatalf("after delete b: %+v", ups)
	}
	if code, _ := apiJSON(t, ts.URL, "DELETE", "/api/v1/upstreams/a", ""); code != 409 {
		t.Errorf("delete last want 409 got %d", code)
	}
	if code, _ := apiJSON(t, ts.URL, "DELETE", "/api/v1/upstreams/nope", ""); code != 404 {
		t.Errorf("delete missing want 404 got %d", code)
	}
}

// 连通测试端点:200 上游 → reachable;401/403 → 可达但 key 被拒。
func TestUpstreamTestEndpoint(t *testing.T) {
	var gotPath string
	var gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	up := config.Upstream{Name: "real", Type: config.TypeOpenAI, BaseURL: upstream.URL + "/v1", APIKey: "sk-upstream-key", Priority: 1}
	ts, _, _ := newUpstreamAPI(t, []config.Upstream{up})

	code, body := apiJSON(t, ts.URL, "POST", "/api/v1/upstreams/real/test", "")
	if code != 200 {
		t.Fatalf("test: %d %s", code, body)
	}
	m := decodeObj(t, body)
	if m["reachable"] != true || m["status"].(float64) != 200 {
		t.Errorf("want reachable, got %v", m)
	}
	if gotPath != "/v1/models" || gotAuth != "Bearer sk-upstream-key" {
		t.Errorf("probe path=%q auth=%q", gotPath, gotAuth)
	}

	if code, _ := apiJSON(t, ts.URL, "POST", "/api/v1/upstreams/missing/test", ""); code != 404 {
		t.Errorf("test missing want 404 got %d", code)
	}
}
