// Package quota 读取上游订阅的用量/配额,驱动"配额感知选路"。
// 校准结论(见 DESIGN):opencode.ai/zen/go 在 GET {base}/v1/usage(需 Authorization: Bearer,
// x-api-key 会 401)返回三层嵌套窗口 rolling/weekly/monthly,各有 {status,percent,resetsAt}。
package quota

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"personal-ai-gateway/internal/config"
)

const (
	WinRolling = "rolling"
	WinWeekly  = "weekly"
	WinMonthly = "monthly"
)

// WindowInfo 是某层窗口的一次读数。
type WindowInfo struct {
	Percent  int       `json:"percent"`
	Status   string    `json:"status"`
	ResetsAt time.Time `json:"-"`
}

// Report 是某次 /usage 拉取返回的全部窗口(键:rolling|weekly|monthly)。
type Report struct {
	Windows map[string]WindowInfo
}

// Snapshot 是归一化后(已按 config 选定窗口/invert 换算)的一份配额状态,供 router 决策与 /api 展示。
type Snapshot struct {
	Window   string    `json:"window"`
	UsedPct  int       `json:"used_pct"` // 已用百分比(换算后,0..100)
	Status   string    `json:"status"`
	ResetsAt time.Time `json:"resets_at"`
	Hard     bool      `json:"hard"` // ≥hard_used_pct 或 status != ok
}

// ReportFetcher 负责真正访问某上游的配额端点。HTTPFetcher 之外可注入假实现测试。
type ReportFetcher interface {
	Fetch(ctx context.Context, up config.Upstream) (Report, error)
}

// HTTPFetcher 访问上游 /usage。注意配额端点只认 Bearer,与模型请求的鉴权头无关。
type HTTPFetcher struct {
	Client  *http.Client
	BaseURL func(up config.Upstream) string // 测试可覆盖;nil 用默认拼法
}

func (f *HTTPFetcher) Fetch(ctx context.Context, up config.Upstream) (Report, error) {
	c := f.Client
	if c == nil {
		c = http.DefaultClient
	}
	base := up.BaseURL
	if f.BaseURL != nil {
		base = f.BaseURL(up)
	}
	url := usageURL(base, up.Type)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Report{}, fmt.Errorf("quota: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+up.APIKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.Do(req)
	if err != nil {
		return Report{}, fmt.Errorf("quota: fetch %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Report{}, fmt.Errorf("quota: %s returned %d", url, resp.StatusCode)
	}
	var body struct {
		Usage map[string]struct {
			Status   string `json:"status"`
			Percent  int    `json:"percent"`
			ResetsAt string `json:"resetsAt"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return Report{}, fmt.Errorf("quota: parse %s: %w", url, err)
	}
	rep := Report{Windows: map[string]WindowInfo{}}
	for k, v := range body.Usage {
		wi := WindowInfo{Percent: v.Percent, Status: v.Status}
		if t, err := time.Parse(time.RFC3339, v.ResetsAt); err == nil {
			wi.ResetsAt = t
		}
		rep.Windows[k] = wi
	}
	if len(rep.Windows) == 0 {
		return Report{}, fmt.Errorf("quota: %s returned no usage windows", url)
	}
	return rep, nil
}

// usageURL 按上游 type 拼配额端点:
//   openai    → base(含 /v1) + /usage
//   anthropic → 根 + /v1/usage
func usageURL(base, typ string) string {
	base = strings.TrimRight(base, "/")
	if typ == config.TypeAnthropic {
		return base + "/v1/usage"
	}
	return base + "/usage"
}

type entry struct {
	cfg      config.QuotaConfig
	last     *Snapshot
	triedAt  time.Time
	lastTier int // 0 正常 / 1 warn / 2 hard,用于只在跨越时打日志
	everOK   bool
}

// Manager 持有各上游配额缓存并按 TTL 轮询;拉到的快照经 updater 推给 router。
type Manager struct {
	mu      sync.Mutex
	ups     map[string]config.Upstream
	byName  map[string]*entry
	fetcher ReportFetcher
	updater func(name string, s Snapshot)
	log     *slog.Logger
}

func NewManager(ups []config.Upstream, f ReportFetcher, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	m := &Manager{
		ups:     map[string]config.Upstream{},
		byName:  map[string]*entry{},
		fetcher: f,
		log:     log,
	}
	for i := range ups {
		u := &ups[i]
		m.ups[u.Name] = *u
		if q := u.Quota; q != nil && q.Enabled {
			m.byName[u.Name] = &entry{cfg: *q}
		}
	}
	return m
}

// SetUpdater 注册快照接收方(通常是 router.SetQuota)。
func (m *Manager) SetUpdater(fn func(name string, s Snapshot)) { m.updater = fn }

// Snapshot 返回某上游最近一次成功快照;未拉取过返回 !ok。
func (m *Manager) Snapshot(name string) (Snapshot, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.byName[name]
	if e == nil || e.last == nil {
		return Snapshot{}, false
	}
	return *e.last, true
}

// Run 后台轮询:每 5s 检查一次,对"距上次尝试已超过 cache_ttl_sec"的上游做一次刷新。
// 用 ctx 取消退出。
func (m *Manager) Run(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		m.pollDue(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// RefreshAll 立即刷新所有启用了配额的上游(供测试/启动时预热)。
func (m *Manager) RefreshAll(ctx context.Context) { m.pollDue(ctx) }

func (m *Manager) pollDue(ctx context.Context) {
	m.mu.Lock()
	var due []string
	now := time.Now()
	for name, e := range m.byName {
		if now.Sub(e.triedAt) >= time.Duration(e.cfg.CacheTTLSec)*time.Second {
			due = append(due, name)
		}
	}
	m.mu.Unlock()

	var wg sync.WaitGroup
	for _, name := range due {
		wg.Add(1)
		go func(n string) {
			defer wg.Done()
			m.refresh(ctx, n)
		}(name)
	}
	wg.Wait()
}

// refresh 拉取一次并:更新缓存、通知 updater、记录 warn/hard 跨越事件。
// 拉取失败按 fail-open 处理:保留旧快照,首次失败打 warn,后续降为 debug。
func (m *Manager) refresh(ctx context.Context, name string) {
	e, ok := m.byName[name]
	if !ok {
		return
	}
	m.mu.Lock()
	e.triedAt = time.Now()
	cfg := e.cfg
	m.mu.Unlock()

	up, ok := m.upstream(name)
	if !ok {
		return
	}
	rep, err := m.fetcher.Fetch(ctx, up)
	if err != nil {
		if e.everOK {
			m.log.Warn("quota refresh failed", "upstream", name, "err", err)
		} else {
			m.log.Debug("quota initial fetch failed (fail-open)", "upstream", name, "err", err)
		}
		return
	}

	snap, ok := m.compute(cfg, rep)
	if !ok {
		m.log.Warn("quota window missing", "upstream", name, "window", cfg.Window)
		return
	}

	m.mu.Lock()
	e.last = &snap
	lastTier := e.lastTier
	e.everOK = true
	m.mu.Unlock()

	tier := 0
	switch {
	case snap.UsedPct >= cfg.HardUsedPct:
		tier = 2
	case snap.UsedPct >= cfg.WarnUsedPct:
		tier = 1
	}
	if tier > lastTier {
		m.log.Info("quota crossed threshold", "upstream", name, "window", snap.Window,
			"used_pct", snap.UsedPct, "status", snap.Status, "tier", tier,
			"resets_at", snap.ResetsAt.UTC().Format(time.RFC3339))
	}
	if tier < lastTier {
		m.log.Info("quota back under threshold", "upstream", name, "window", snap.Window,
			"used_pct", snap.UsedPct, "status", snap.Status)
	}
	m.mu.Lock()
	e.lastTier = tier
	m.mu.Unlock()

	if m.updater != nil {
		m.updater(name, snap)
	}
}

// compute 把某次 Report 折算成所选窗口的 Snapshot(处理 invert 与 hard 判定)。
func (m *Manager) compute(cfg config.QuotaConfig, rep Report) (Snapshot, bool) {
	wi, ok := rep.Windows[cfg.Window]
	if !ok {
		return Snapshot{}, false
	}
	used := wi.Percent
	if cfg.InvertUsedPct {
		used = 100 - used
	}
	if used < 0 {
		used = 0
	}
	if used > 100 {
		used = 100
	}
	status := strings.ToLower(wi.Status)
	hard := used >= cfg.HardUsedPct || (status != "" && status != "ok")
	return Snapshot{Window: cfg.Window, UsedPct: used, Status: wi.Status, ResetsAt: wi.ResetsAt, Hard: hard}, true
}

func (m *Manager) upstream(name string) (config.Upstream, bool) {
	// 无解耦存储时由网关在构造后通过 SetUpstreams 喂入。
	if u, ok := m.ups[name]; ok {
		return u, true
	}
	return config.Upstream{}, false
}
