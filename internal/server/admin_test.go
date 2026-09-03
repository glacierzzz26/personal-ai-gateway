package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"personal-ai-gateway/internal/config"
	"personal-ai-gateway/internal/pricing"
	"personal-ai-gateway/internal/proxy"
	"personal-ai-gateway/internal/router"
	"personal-ai-gateway/internal/store"
)

func seedStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/gw.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	now := time.Now().UTC()
	rows := []store.LogEntry{
		{TS: now.Add(-1 * time.Hour), ClientKey: "k", ClientTool: "tool", Protocol: "openai", Model: "m1", Upstream: "u1", Status: 200, PromptTokens: 100, CompletionTokens: 50, CacheReadTokens: 10, Cost: 0.02},
		{TS: now.Add(-2 * time.Hour), ClientKey: "k", ClientTool: "tool", Protocol: "anthropic", Model: "m1", Upstream: "u2", Status: 200, PromptTokens: 200, CompletionTokens: 100, Cost: 0.10},
		{TS: now.Add(-3 * time.Hour), ClientKey: "k", ClientTool: "tool", Protocol: "openai", Model: "m2", Upstream: "u1", Status: 429, PromptTokens: 5, Err: "quota"},
		{TS: now.Add(-25 * time.Hour), ClientKey: "k", ClientTool: "tool", Protocol: "anthropic", Model: "m2", Upstream: "u2", Status: 200, PromptTokens: 50, CompletionTokens: 60, Cost: 0.05},
		{TS: now.Add(-26 * time.Hour), ClientKey: "k", ClientTool: "tool", Protocol: "openai", Model: "m1", Upstream: "u1", Status: 500, PromptTokens: 300, Err: "boom"},
	}
	for _, r := range rows {
		if err := st.Log(r); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	return st
}

// buildAPI 构造只含 /api 的测试服务器(无上游,仅查库)。
func buildAPI(t *testing.T, st *store.Store) *httptest.Server {
	t.Helper()
	cfg := config.Config{Keys: []config.Key{{Name: "laptop", Secret: testKey}}}
	gw := proxy.New(router.New(nil), st, pricing.New(nil))
	ts := httptest.NewServer(New(&cfg, gw, nil).Handler())
	t.Cleanup(ts.Close)
	return ts
}

func apiGet(t *testing.T, base, path string) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, base+path, nil)
	req.Header.Set("Authorization", "Bearer "+testKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, body
}

func decodeObj(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("bad json: %v\n%s", err, b)
	}
	return m
}

func metaOf(m map[string]any) map[string]any { return m["meta"].(map[string]any) }

func TestListPaginationFilterSort(t *testing.T) {
	ts := buildAPI(t, seedStore(t))

	// 基础列表
	code, body := apiGet(t, ts.URL, "/api/v1/usage/requests")
	if code != 200 {
		t.Fatalf("list: %d %s", code, body)
	}
	m := decodeObj(t, body)
	if metaOf(m)["total"].(float64) != 5 {
		t.Fatalf("total=%v want 5", metaOf(m)["total"])
	}

	// 分页
	code, body = apiGet(t, ts.URL, "/api/v1/usage/requests?limit=2&offset=2")
	if code != 200 {
		t.Fatal(string(body))
	}
	m = decodeObj(t, body)
	meta := metaOf(m)
	if meta["total"].(float64) != 5 || len(m["data"].([]any)) != 2 {
		t.Fatalf("page wrong: meta=%v data=%d", meta, len(m["data"].([]any)))
	}

	// 过滤
	for path, want := range map[string]float64{
		"/api/v1/usage/requests?protocol=openai":     3,
		"/api/v1/usage/requests?model=m1":            3,
		"/api/v1/usage/requests?upstream=u1":         3,
		"/api/v1/usage/requests?status_bucket=2xx":   3,
		"/api/v1/usage/requests?status=429":          1,
		"/api/v1/usage/requests?status_bucket=5xx":   1,
	} {
		_, body = apiGet(t, ts.URL, path)
		if got := metaOf(decodeObj(t, body))["total"].(float64); got != want {
			t.Errorf("%s: total=%v want %v", path, got, want)
		}
	}

	// 排序:prompt 升序第一个应为 5(m2/429/quota)
	_, body = apiGet(t, ts.URL, "/api/v1/usage/requests?sort=prompt_tokens&order=asc")
	data := decodeObj(t, body)["data"].([]any)
	first := data[0].(map[string]any)
	if first["prompt_tokens"].(float64) != 5 || first["status"].(float64) != 429 {
		t.Fatalf("sort asc wrong: %v", first)
	}

	// 参数错误
	for _, path := range []string{
		"/api/v1/usage/requests?sort=nope",
		"/api/v1/usage/requests?protocol=bad",
		"/api/v1/usage/requests?order=sideways",
		"/api/v1/usage/requests?limit=0",
		"/api/v1/usage/requests?status=999",
	} {
		code, _ := apiGet(t, ts.URL, path)
		if code != 400 {
			t.Errorf("%s: want 400 got %d", path, code)
		}
	}
}

func TestSummaryGrouping(t *testing.T) {
	ts := buildAPI(t, seedStore(t))
	base := "/api/v1/usage/summary?from=2020-01-01T00:00:00Z&to=2030-01-01T00:00:00Z"

	// 按 model 分组
	code, body := apiGet(t, ts.URL, base+"&group_by=model")
	if code != 200 {
		t.Fatalf("summary model: code=%d body=%s", code, body)
	}
	m := decodeObj(t, body)
	rows := m["data"].([]any)
	if len(rows) != 2 {
		t.Fatalf("want 2 model rows, got %d", len(rows))
	}
	got := map[string]map[string]any{}
	for _, r := range rows {
		rm := r.(map[string]any)
		got[rm["model"].(string)] = rm
	}
	// 种子里 m1:200/200/500 → 3 req 1 err;m2:429/200 → 2 req 1 err
	if m1 := got["m1"]; m1["requests"].(float64) != 3 || m1["errors"].(float64) != 1 {
		t.Fatalf("m1 row wrong: %v", m1)
	}
	if m2 := got["m2"]; m2["requests"].(float64) != 2 || m2["errors"].(float64) != 1 {
		t.Fatalf("m2 row wrong: %v", m2)
	}

	// totals(全量)
	tot := m["totals"].(map[string]any)
	if tot["requests"].(float64) != 5 || tot["errors"].(float64) != 2 {
		t.Fatalf("totals wrong: %v", tot)
	}
	if tot["prompt_tokens"].(float64) != 655 || tot["completion_tokens"].(float64) != 210 {
		t.Fatalf("totals tokens wrong: %v", tot)
	}
	// 种子 cost 相加 = 0.02+0.10+0.00+0.05+0.00 = 0.17
	if got := tot["cost"].(float64); got < 0.169 || got > 0.171 {
		t.Fatalf("totals cost=%v want ~0.17", got)
	}

	// bucket=day 分组上游 → 4 行(2 上游 × 2 天)
	code, body = apiGet(t, ts.URL, base+"&group_by=upstream&bucket=day")
	if code != 200 {
		t.Fatalf("summary day: code=%d body=%s", code, body)
	}
	m = decodeObj(t, body)
	if len(m["data"].([]any)) != 4 {
		t.Fatalf("want 4 day×upstream rows, got %d", len(m["data"].([]any)))
	}

	// bucket 参数错误
	if code, _ := apiGet(t, ts.URL, base+"&bucket=week"); code != 400 {
		t.Fatalf("bad bucket want 400 got %d", code)
	}
	if code, _ := apiGet(t, ts.URL, base+"&group_by=wat"); code != 400 {
		t.Fatalf("bad group want 400 got %d", code)
	}
}

// CapturedUsagePersisted 验证:真实请求经转发后,token 用量与成本入库并能在 summary 查到。
func TestCapturedUsagePersisted(t *testing.T) {
	up := fakeOpenAI(t, okOpenAIHandler(t)) // 返回 prompt_tokens=10 completion_tokens=5
	st, _ := store.Open(t.TempDir() + "/gw.db")
	t.Cleanup(func() { st.Close() })

	price := pricing.New([]config.PriceRule{
		{Model: "*", PromptPerM: 10, CompletionPerM: 20, CacheReadPerM: 5},
	})
	gw := proxy.New(router.New([]config.Upstream{{
		Name: "a", Type: config.TypeOpenAI, BaseURL: up.URL + "/v1", APIKey: "up-key-a",
	}}), st, price)

	cfg := config.Config{Keys: []config.Key{{Name: "laptop", Secret: testKey}}}
	ts := httptest.NewServer(New(&cfg, gw, nil).Handler())
	t.Cleanup(ts.Close)

	resp := doChat(t, ts.URL, testKey, "gpt-4o", false)
	if resp.StatusCode != 200 {
		t.Fatalf("relay status %d", resp.StatusCode)
	}
	resp.Body.Close()

	_, body := apiGet(t, ts.URL, "/api/v1/usage/summary?group_by=model")
	m := decodeObj(t, body)
	tot := m["totals"].(map[string]any)
	if tot["prompt_tokens"].(float64) != 10 || tot["completion_tokens"].(float64) != 5 {
		t.Fatalf("captured tokens wrong: %v", tot)
	}
	// cost = (10*10 + 5*20)/1e6
	want := (10*10 + 5*20) / 1e6
	if got := tot["cost"].(float64); got < want*0.999 || got > want*1.001 {
		t.Fatalf("captured cost=%v want ~%v", got, want)
	}
}
