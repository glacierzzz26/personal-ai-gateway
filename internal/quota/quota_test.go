package quota

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"personal-ai-gateway/internal/config"
)

// 路径拼接:openai(base 含 /v1)+/usage;anthropic(根)+/v1/usage。
func TestUsageURL(t *testing.T) {
	cases := []struct{ base, typ, want string }{
		{"https://opencode.ai/zen/go", "anthropic", "https://opencode.ai/zen/go/v1/usage"},
		{"https://opencode.ai/zen/go/", "anthropic", "https://opencode.ai/zen/go/v1/usage"},
		{"https://opencode.ai/zen/go/v1", "openai", "https://opencode.ai/zen/go/v1/usage"},
		{"https://x.example/v1/", "openai", "https://x.example/v1/usage"},
	}
	for _, c := range cases {
		if got := usageURL(c.base, c.typ); got != c.want {
			t.Errorf("usageURL(%q,%q)=%q want %q", c.base, c.typ, got, c.want)
		}
	}
}

// HTTPFetcher 用 Bearer 鉴权访问 {..}/v1/usage 并解析三层窗口。
func TestHTTPFetcher(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if r.URL.Path != "/v1/usage" {
			t.Errorf("path=%q want /v1/usage", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"usage":{
			"rolling":{"status":"ok","percent":9,"resetsAt":"2026-09-03T07:36:00Z"},
			"weekly":{"status":"ok","percent":24,"resetsAt":"2026-09-07T00:00:00Z"},
			"monthly":{"status":"ok","percent":78,"resetsAt":"2026-09-20T08:34:00Z"}}}`)
	}))
	defer srv.Close()

	f := &HTTPFetcher{}
	for _, up := range []config.Upstream{
		{Type: config.TypeAnthropic, BaseURL: srv.URL, APIKey: "k1"},
		{Type: config.TypeOpenAI, BaseURL: srv.URL + "/v1", APIKey: "k2"},
	} {
		rep, err := f.Fetch(context.Background(), up)
		if err != nil {
			t.Fatalf("fetch: %v", err)
		}
		if want := "Bearer " + up.APIKey; gotAuth != want {
			t.Errorf("auth=%q want %q", gotAuth, want)
		}
		if len(rep.Windows) != 3 {
			t.Fatalf("windows=%d want 3", len(rep.Windows))
		}
		if m := rep.Windows["monthly"]; m.Percent != 78 {
			t.Errorf("monthly percent=%d want 78", m.Percent)
		}
		if r := rep.Windows["rolling"]; r.ResetsAt.IsZero() {
			t.Error("rolling resetsAt not parsed")
		}
	}
}

// 非 200 → 报错(上层按 fail-open 处理)。
func TestHTTPFetcherErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	f := &HTTPFetcher{}
	if _, err := f.Fetch(context.Background(), config.Upstream{BaseURL: srv.URL, Type: config.TypeOpenAI}); err == nil {
		t.Fatal("want error on non-200")
	}
}

// compute:hard / invert / 非 ok 状态判定。
func TestCompute(t *testing.T) {
	rep := Report{Windows: map[string]WindowInfo{
		"monthly": {Percent: 95, Status: "ok"},
		"weekly":  {Percent: 30, Status: "ok"},
		"rolling": {Percent: 100, Status: "exceeded"},
	}}
	cases := []struct {
		name string
		cfg  config.QuotaConfig
		want Snapshot
	}{
		{"over hard", config.QuotaConfig{Window: "monthly", HardUsedPct: 90}, Snapshot{Window: "monthly", UsedPct: 95, Status: "ok", Hard: true}},
		{"under hard", config.QuotaConfig{Window: "weekly", HardUsedPct: 90}, Snapshot{Window: "weekly", UsedPct: 30, Status: "ok", Hard: false}},
		{"invert = remaining", config.QuotaConfig{Window: "weekly", HardUsedPct: 90, InvertUsedPct: true}, Snapshot{Window: "weekly", UsedPct: 70, Status: "ok", Hard: false}},
		{"non-ok status forces hard", config.QuotaConfig{Window: "rolling", HardUsedPct: 90}, Snapshot{Window: "rolling", UsedPct: 100, Status: "exceeded", Hard: true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := newManagerForTest().compute(c.cfg, rep)
			if !ok {
				t.Fatal("compute ok=false")
			}
			if got != c.want {
				t.Errorf("got %+v want %+v", got, c.want)
			}
		})
	}
}

// fakeFetcher 依次吐出预设的 monthly 读数;耗尽后返回错误。
type fakeFetcher struct {
	next []struct {
		percent int
		status  string
	}
}

func (f *fakeFetcher) Fetch(context.Context, config.Upstream) (Report, error) {
	if len(f.next) == 0 {
		return Report{}, errors.New("quota endpoint down")
	}
	p := f.next[0]
	f.next = f.next[1:]
	return Report{Windows: map[string]WindowInfo{
		"monthly": {Percent: p.percent, Status: p.status},
	}}, nil
}

// Manager 端到端:推送快照 → 阈值跨越后再次推送 → 拉取失败保留旧快照(fail-open)。
func TestManagerPushAndFailOpen(t *testing.T) {
	up := config.Upstream{Name: "oc", Type: config.TypeOpenAI, BaseURL: "http://x/v1", APIKey: "k"}
	up.Quota = &config.QuotaConfig{Enabled: true, Window: "monthly", HardUsedPct: 90, CacheTTLSec: 60}

	f := &fakeFetcher{next: []struct {
		percent int
		status  string
	}{
		{40, "ok"},
		{97, "ok"},
	}}
	m := NewManager([]config.Upstream{up}, f, discardLogger())
	var pushed []Snapshot
	m.SetUpdater(func(_ string, s Snapshot) { pushed = append(pushed, s) })

	ctx := context.Background()
	m.RefreshAll(ctx)
	if len(pushed) != 1 || pushed[0].UsedPct != 40 || pushed[0].Hard {
		t.Fatalf("first push wrong: %+v", pushed)
	}
	if st, ok := m.Snapshot("oc"); !ok || st.UsedPct != 40 {
		t.Fatalf("snapshot wrong: %+v %v", st, ok)
	}

	// 模拟 TTL 到期:拉到 97 → hard 再次推送
	m.byName["oc"].triedAt = time.Time{}
	m.RefreshAll(ctx)
	if len(pushed) != 2 || !pushed[1].Hard || pushed[1].UsedPct != 97 {
		t.Fatalf("second push wrong: %+v", pushed)
	}

	// 之后拉取失败 → 保留最后一次成功快照(仍 97,hard),不推送
	m.byName["oc"].triedAt = time.Time{}
	f.next = nil
	m.RefreshAll(ctx)
	if st, ok := m.Snapshot("oc"); !ok || st.UsedPct != 97 || !st.Hard {
		t.Fatalf("fail-open should keep last snapshot: %+v %v", st, ok)
	}
	if len(pushed) != 2 {
		t.Fatalf("no push expected on failure, got %d", len(pushed))
	}
}

func newManagerForTest() *Manager {
	return &Manager{byName: map[string]*entry{}, ups: map[string]config.Upstream{}, log: discardLogger()}
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
