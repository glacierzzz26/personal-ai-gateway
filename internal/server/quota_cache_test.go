package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeQuotaUpstream 起一个假上游,GET 任意路径都回 {usage:{rolling:{status:ok,percent:P}}},
// 并统计被打次数(验证缓存/去重是否真的省下了上游请求)。
// delay 用于制造「慢上游」以暴露在途去重:两个并发调用应共用同一次上游请求。
func fakeQuotaUpstream(t *testing.T, percent float64, hits *int32, delay chan struct{}) *httptest.Server {
	t.Helper()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(hits, 1)
		if delay != nil {
			<-delay // 阻塞到测试放行,期间允许第二个调用进来
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fmt.Sprintf(`{"planName":"Pro","usage":{"rolling":{"status":"ok","percent":%g}}}`, percent)))
	}))
	t.Cleanup(up.Close)
	return up
}

// createThirdPartyChannel 建一条 thirdparty 渠道并配上额度路径(走 /v1/usage 通用信封)。
func createThirdPartyChannel(t *testing.T, c *http.Client, base, name, upURL, quotaPath string) int64 {
	t.Helper()
	code, body := doJSON(t, c, http.MethodPost, base+"/api/v1/channels", map[string]any{
		"name": name, "provider": "OpenAI", "channelType": "thirdparty", "egressProto": "openai",
		"baseUrl": upURL, "apiKey": "sk-test-1234567890abcd",
		"priority": 1, "weight": 100, "timeoutMs": 30000, "maxFailures": 3, "cooldownSec": 30, "tags": []string{},
		"quotaPath": quotaPath, "quotaShape": "usage",
	})
	mustStatus(t, code, http.StatusOK, "create thirdparty channel "+name)
	return int64(decode[map[string]any](t, body)["id"].(float64))
}

// TestChannelsQuotaBatchRouting 「/channels/quota」必须命中批量端点,不能被当作 id="quota"
// 的单渠道端点(Go 1.22 ServeMux 字面量优先)。这里用一个**配了额度路径**的渠道验证:
// 若路由落到 handleChannelQuota 会因 paramID 解析失败回 400。
func TestChannelsQuotaBatchRouting(t *testing.T) {
	srv, c, _ := newTestServer(t)
	base := srv.URL
	bootstrap(t, c, base)

	var hits int32
	up := fakeQuotaUpstream(t, 12, &hits, nil)
	id := createThirdPartyChannel(t, c, base, "batch", up.URL, "/v1/usage")

	code, body := doJSON(t, c, http.MethodGet, base+"/api/v1/channels/quota", nil)
	mustStatus(t, code, http.StatusOK, "batch quota")
	items := decode[[]batchQuotaItem](t, body)
	if len(items) != 1 || items[0].ID != id {
		t.Fatalf("batch = %s, want one item for channel %d", body, id)
	}
	q := items[0].Quota
	if !q.Available || q.Windows["rolling"].Percent != 12 {
		t.Fatalf("batch item quota = %s, want available rolling=12", body)
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("upstream hits = %d, want 1", hits)
	}
}

// TestChannelsQuotaBatchSkipsUnconfigured 没配额度路径的第三方渠道不发上游请求,
// 直接回「未配置」(errorKind=not_configured),且不计入告警。
func TestChannelsQuotaBatchSkipsUnconfigured(t *testing.T) {
	srv, c, _ := newTestServer(t)
	base := srv.URL
	bootstrap(t, c, base)

	var hits int32
	up := fakeQuotaUpstream(t, 5, &hits, nil)
	createThirdPartyChannel(t, c, base, "no-path", up.URL, "")

	code, body := doJSON(t, c, http.MethodGet, base+"/api/v1/channels/quota", nil)
	mustStatus(t, code, http.StatusOK, "batch quota")
	items := decode[[]batchQuotaItem](t, body)
	if len(items) != 1 {
		t.Fatalf("batch = %s, want 1 item", body)
	}
	q := items[0].Quota
	if q.Available || q.ErrorKind != "not_configured" {
		t.Fatalf("unconfigured quota = %s, want available=false errorKind=not_configured", body)
	}
	if hits != 0 {
		t.Fatalf("upstream hits = %d, want 0 (must not query without a path)", hits)
	}
}

// TestChannelQuotaCacheDedupesUpstream 同一渠道连续两次查询只打一次上游(第二次命中缓存)。
func TestChannelQuotaCacheDedupesUpstream(t *testing.T) {
	srv, c, s := newTestServerWithEng(t)
	base := srv.URL
	bootstrap(t, c, base)

	var hits int32
	up := fakeQuotaUpstream(t, 42, &hits, nil)
	id := createThirdPartyChannel(t, c, base, "cached", up.URL, "/v1/usage")

	url := fmt.Sprintf("%s/api/v1/channels/%d/quota", base, id)
	for i := 0; i < 2; i++ {
		code, body := doJSON(t, c, http.MethodGet, url, nil)
		mustStatus(t, code, http.StatusOK, "channel quota")
		if q := decode[map[string]any](t, body); q["available"] != true {
			t.Fatalf("quota #%d = %s, want available", i+1, body)
		}
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("upstream hits = %d, want 1 (second call should hit cache)", got)
	}
	// 缓存项确实在 server 上(而不是每次现打)。
	if len(s.qcache) != 1 {
		t.Fatalf("cache size = %d, want 1", len(s.qcache))
	}
}

// TestChannelQuotaInflightDedup 在途去重:第一次查询还压在上游(未返回)时,
// 并发的第二次查询应**等待同一次结果**,而不是再打一次上游。
func TestChannelQuotaInflightDedup(t *testing.T) {
	srv, c, _ := newTestServerWithEng(t)
	base := srv.URL
	bootstrap(t, c, base)

	release := make(chan struct{})
	var hits int32
	up := fakeQuotaUpstream(t, 77, &hits, release)
	id := createThirdPartyChannel(t, c, base, "inflight", up.URL, "/v1/usage")

	url := fmt.Sprintf("%s/api/v1/channels/%d/quota", base, id)
	var wg sync.WaitGroup
	results := make([]string, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, body := doJSON(t, c, http.MethodGet, url, nil)
			results[i] = string(body)
		}(i)
	}
	// 两次请求都已发出(第二次撞在途项,等待第一次);放行上游。
	waitFor(t, func() bool { return atomic.LoadInt32(&hits) == 1 })
	close(release)
	wg.Wait()

	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("upstream hits = %d, want 1 (in-flight dedup)", got)
	}
	for i, b := range results {
		var q map[string]any
		if err := json.Unmarshal([]byte(b), &q); err != nil {
			t.Fatalf("result #%d not JSON: %s", i, b)
		}
		if q["available"] != true {
			t.Fatalf("result #%d = %s, want available", i, b)
		}
	}
}

// TestChannelQuotaErrorKindUnsupported 无额度协议的类型(如 deepseek 之外未实现的枚举不会出现,
// 这里用「已配置路径但上游不是 JSON」代表真实查询失败) → errorKind=fetch。
func TestChannelQuotaErrorKindFetch(t *testing.T) {
	srv, c, _ := newTestServer(t)
	base := srv.URL
	bootstrap(t, c, base)

	// 上游回 HTML(路由不存在)→ ensureJSON 判失败 → fetch 类错误。
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<!doctype html><html>not json</html>"))
	}))
	t.Cleanup(up.Close)
	id := createThirdPartyChannel(t, c, base, "bad-json", up.URL, "/v1/usage")

	code, body := doJSON(t, c, http.MethodGet, fmt.Sprintf("%s/api/v1/channels/%d/quota", base, id), nil)
	mustStatus(t, code, http.StatusOK, "channel quota")
	q := decode[map[string]any](t, body)
	if q["available"] != false || q["errorKind"] != "fetch" {
		t.Fatalf("quota = %s, want available=false errorKind=fetch", body)
	}
}

// waitFor 轮询等待条件成立(上限约 2s),用于等在途请求抵达上游。
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for i := 0; i < 2000; i++ {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}
