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

// TestChannelCircuitSurvivesIdle issue #17 回归:渠道熔断后即使**完全没有流量**,
// 列表也必须如实反映熔断态 —— 旧实现里近 15 分钟统计为空就回 healthy/100%,
// 于是空闲渠道(尤其低频/兜底)静默一会儿就「自愈」成健康,实际仍在被剔除。
//
// 两个阶段:
//   - 冷却中(600s)→ down + circuitOpen=true;
//   - 冷却已过但未复检(用 -1s 冷却模拟)→ unknown「待观察」,绝不显示 healthy。
func TestChannelCircuitSurvivesIdle(t *testing.T) {
	srv, c, s := newTestServerWithEng(t)
	base := srv.URL
	bootstrap(t, c, base)

	up := fakeUpstream(t, []string{"m1"})
	id := createChannel(t, c, base, "idle-trip", up.URL)

	// 阶段一:熔断 600s,期间不产生任何请求日志。
	s.eng.RecordFailure(id, 1, 600)
	ch := channelByID(t, c, base, id)
	if ch["status"] != "down" || ch["circuitOpen"] != true {
		t.Fatalf("cooling channel = %v, want down + circuitOpen", ch)
	}
	if ch["availableFrom"] == nil || ch["availableFrom"] == "" {
		t.Fatalf("cooling channel must expose availableFrom: %v", ch)
	}

	// 阶段二:冷却已过、仍未复检 → 不得回 healthy。
	s.eng.RecordFailure(id, 1, -1)
	ch = channelByID(t, c, base, id)
	if ch["status"] == "healthy" {
		t.Fatalf("idle half-open channel reported healthy (issue #17): %v", ch)
	}
	if ch["status"] != "unknown" {
		t.Fatalf("half-open channel status = %v, want unknown", ch["status"])
	}
	if open, _ := ch["circuitOpen"].(bool); open {
		t.Fatalf("half-open channel must not be flagged circuitOpen: %v", ch)
	}

	// 复检成功后才回到健康。
	s.eng.RecordSuccess(id, 42)
	ch = channelByID(t, c, base, id)
	if ch["status"] != "healthy" {
		t.Fatalf("after successful probe status = %v, want healthy", ch["status"])
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
