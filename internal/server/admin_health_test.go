package server

import (
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"personal-ai-gateway/internal/config"
	"personal-ai-gateway/internal/secret"
	"personal-ai-gateway/internal/store"
)

// newTestServerWithEng 同 newTestServer,但额外暴露 *Server(便于测试直接驱动 engine 熔断/EWMA)。
func newTestServerWithEng(t *testing.T) (*httptest.Server, *http.Client, *Server) {
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
	return srv, client, s
}

func createChannel(t *testing.T, c *http.Client, base, name, upURL string) int64 {
	t.Helper()
	code, body := doJSON(t, c, http.MethodPost, base+"/api/v1/channels", map[string]any{
		"name": name, "provider": "OpenAI", "baseUrl": upURL, "apiKey": "sk-test-1234567890abcd",
		"priority": 1, "weight": 100, "timeoutMs": 30000, "maxFailures": 3, "cooldownSec": 30, "tags": []string{},
	})
	mustStatus(t, code, http.StatusOK, "create channel "+name)
	return int64(decode[map[string]any](t, body)["id"].(float64))
}

func channelByID(t *testing.T, c *http.Client, base string, id int64) map[string]any {
	t.Helper()
	code, body := doJSON(t, c, http.MethodGet, base+"/api/v1/channels", nil)
	mustStatus(t, code, http.StatusOK, "list channels")
	for _, ch := range decode[[]map[string]any](t, body) {
		if int64(ch["id"].(float64)) == id {
			return ch
		}
	}
	t.Fatalf("channel %d not in list", id)
	return nil
}

// TestChannelTestWritesBackHealth Bug1:探测成功清熔断并把探测延迟计入 EWMA,
// 列表行从 down 恢复 healthy、延迟与测试结果一致。
func TestChannelTestWritesBackHealth(t *testing.T) {
	srv, c, s := newTestServerWithEng(t)
	base := srv.URL
	bootstrap(t, c, base)

	up := fakeUpstream(t, []string{"m1", "m2"})
	id := createChannel(t, c, base, "t-writeback", up.URL)

	// 先制造熔断:单次失败即熔断 600s。
	s.eng.RecordFailure(id, 1, 600)

	ch := channelByID(t, c, base, id)
	if ch["status"] != "down" || ch["circuitOpen"] != true {
		t.Fatalf("pre-test channel = %v, want down + circuitOpen", ch)
	}

	code, body := doJSON(t, c, http.MethodPost, fmt.Sprintf("%s/api/v1/channels/%d/test", base, id), nil)
	mustStatus(t, code, http.StatusOK, "channel test")
	tr := decode[map[string]any](t, body)
	if tr["ok"] != true {
		t.Fatalf("test not ok: %s", body)
	}
	lat := int64(tr["latencyMs"].(float64))

	ch = channelByID(t, c, base, id)
	if ch["status"] != "healthy" {
		t.Fatalf("after-test status = %v, want healthy: %v", ch["status"], ch)
	}
	if open, _ := ch["circuitOpen"].(bool); open {
		t.Fatalf("after-test circuitOpen = true, want cleared: %v", ch)
	}
	if got := int64(ch["latencyMs"].(float64)); got != lat {
		t.Fatalf("after-test latency = %d, want == test latency %d", got, lat)
	}
}

// TestChannelSyncModelCount Bug2:同步响应 modelCount 与渠道列表「N 个模型」一致,
// 幂等重同步不重复计入 added。
func TestChannelSyncModelCount(t *testing.T) {
	srv, c, _ := newTestServer(t)
	base := srv.URL
	bootstrap(t, c, base)

	up := fakeUpstream(t, []string{"m-a", "m-b"})
	id := createChannel(t, c, base, "sync-count", up.URL)

	code, body := doJSON(t, c, http.MethodPost, fmt.Sprintf("%s/api/v1/channels/%d/sync-models", base, id), nil)
	mustStatus(t, code, http.StatusOK, "sync models #1")
	s1 := decode[map[string]any](t, body)
	if s1["added"].(float64) != 2 || s1["updated"].(float64) != 0 {
		t.Fatalf("sync #1 = %s, want added=2 updated=0", body)
	}
	if s1["modelCount"].(float64) != 2 {
		t.Fatalf("sync #1 modelCount = %v, want 2", s1["modelCount"])
	}
	if ch := channelByID(t, c, base, id); ch["modelCount"].(float64) != 2 {
		t.Fatalf("list modelCount after sync = %v, want 2", ch["modelCount"])
	}

	// 幂等重同步:目录已存在 → updated=2、modelCount 不变。
	code, body = doJSON(t, c, http.MethodPost, fmt.Sprintf("%s/api/v1/channels/%d/sync-models", base, id), nil)
	mustStatus(t, code, http.StatusOK, "sync models #2")
	s2 := decode[map[string]any](t, body)
	if s2["added"].(float64) != 0 || s2["updated"].(float64) != 2 {
		t.Fatalf("sync #2 = %s, want added=0 updated=2", body)
	}
	if s2["modelCount"].(float64) != 2 {
		t.Fatalf("sync #2 modelCount = %v, want 2", s2["modelCount"])
	}
}
