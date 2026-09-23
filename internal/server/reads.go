package server

import (
	"time"

	"personal-ai-gateway/internal/domain"
	"personal-ai-gateway/internal/engine"
	"personal-ai-gateway/internal/pricing"
	"personal-ai-gateway/internal/store"
)

// 展示口径:延迟/成功率取「近 15 分钟」滚动窗口(重启后由日志自愈,EWMA 只在运行期累计)。
const (
	displayWindow = 15 * time.Minute
	healthyPct    = 80.0 // 成功率 < 80% 视为 degraded
)

// adminView 一次管理面读请求所需的展示聚合(批量取回,避免逐行 N+1)。
type adminView struct {
	now      time.Time
	tz       int
	settings domain.Settings
	chOrder  []domain.ChannelRow // ListChannels 原始序(priority ASC),列表展示照此
	chByID   map[int64]domain.ChannelRow
	chRecent map[int64]store.ChannelStat // 近 displayWindow
	chToday  map[int64]store.ChannelStat // 本地自然日
	chModels map[int64]int               // 渠道挂载供给源数
}

func (s *Server) buildView() (*adminView, error) {
	settings, err := s.st.GetSettings()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	rows, err := s.st.ListChannels()
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]domain.ChannelRow, len(rows))
	order := make([]domain.ChannelRow, 0, len(rows))
	for _, ch := range rows {
		byID[ch.ID] = ch
		order = append(order, ch)
	}
	todayStart, _ := store.LocalDayWindowUTC(settings.TZOffsetMin, now)
	recent, err := s.st.ChannelStatsSince(now.Add(-displayWindow))
	if err != nil {
		return nil, err
	}
	today, err := s.st.ChannelStatsSince(todayStart)
	if err != nil {
		return nil, err
	}
	counts, err := s.st.ChannelModelCounts()
	if err != nil {
		return nil, err
	}
	return &adminView{
		now: now, tz: settings.TZOffsetMin, settings: settings,
		chOrder: order, chByID: byID, chRecent: recent, chToday: today, chModels: counts,
	}, nil
}

// modelTodayStats 目录所有模型在本地自然日的用量(name→UsageRow)。
func (s *Server) modelTodayStats(nowUTC time.Time, tz int) (map[string]domain.UsageRow, error) {
	start, _ := store.LocalDayWindowUTC(tz, nowUTC)
	rows, err := s.st.QueryDimSummary("model", start, nowUTC, 0)
	if err != nil {
		return nil, err
	}
	m := make(map[string]domain.UsageRow, len(rows))
	for _, r := range rows {
		m[r.Name] = r
	}
	return m, nil
}

// channelHealth 由熔断态 + 最近窗口请求算成功率/延迟/状态。enabled=false 直接 disabled。
//
// 熔断态优先于窗口统计:冷却中 → down;冷却已过待复检(半开)→ 不算健康,
// 用 StatusUnknown 表达「尚无流量验证」。**没有流量 ≠ 健康** —— 空闲渠道(低频、
// 兜底)的成功率无从验证,只能记 unknown 等它自己跑一次;否则一条刚熔断又恰好
// 静默的渠道会被窗口统计判成 healthy,而引擎仍在按半开剔除它。
func (s *Server) channelHealth(v *adminView, id int64, enabled bool) (successRate float64, latencyMs int64, status domain.HealthStatus, open bool, availableFrom time.Time) {
	if !enabled {
		return 0, 0, domain.StatusDisabled, false, time.Time{}
	}
	switch state, at := s.eng.CircuitState(id); state {
	case engine.CircuitDown:
		return 0, s.eng.LatencyMS(id), domain.StatusDown, true, at
	case engine.CircuitProbing:
		// 半开:曾熔断、冷却已过、等待一次真实复检。不报健康,也不报熔断中。
		return 0, s.eng.LatencyMS(id), domain.StatusUnknown, false, time.Time{}
	}
	st, has := v.chRecent[id]
	if !has || st.Requests == 0 {
		// 无近期流量:只在最近确有一次成功(真实转发或管理台探测)时才敢报健康,
		// 否则无从验证 → unknown。**没有流量 ≠ 健康**,空闲渠道不冒充已验证。
		if !s.eng.LastSuccessAt(id).IsZero() {
			return 100, s.eng.LatencyMS(id), domain.StatusHealthy, false, time.Time{}
		}
		return 0, s.eng.LatencyMS(id), domain.StatusUnknown, false, time.Time{}
	}
	rate := 100.0
	if st.Requests > 0 {
		rate = 100 * (1 - float64(st.Errors)/float64(st.Requests))
	}
	status = domain.StatusHealthy
	if rate < healthyPct {
		status = domain.StatusDegraded
	}
	if st.LatencySum > 0 {
		latencyMs = st.LatencySum / int64(st.Requests)
	} else {
		latencyMs = s.eng.LatencyMS(id)
	}
	return rate, latencyMs, status, false, time.Time{}
}

// channelRead 组合渠道展示行。
func (s *Server) channelRead(v *adminView, ch domain.ChannelRow) domain.ChannelRead {
	r := domain.ChannelRead{
		ID: ch.ID, Name: ch.Name, Provider: ch.Provider,
		ChannelType: ch.ChannelType, EgressProto: ch.EgressProto, BaseURL: ch.BaseURL,
		Priority: ch.Priority, Weight: ch.Weight,
		KeyMasked: ch.KeyMasked, ModelCount: v.chModels[ch.ID],
		TimeoutMs: ch.TimeoutMs, Tags: ch.Tags, Enabled: ch.Enabled,
		Note: ch.Note, QuotaPath: ch.QuotaPath, QuotaShape: ch.QuotaShape,
		MaxFailures: ch.MaxFailures, CooldownSec: ch.CooldownSec,
		CreatedAt: ch.CreatedAt, UpdatedAt: ch.UpdatedAt,
	}
	if t, ok := v.chToday[ch.ID]; ok {
		r.TodayTokens = t.Tokens
		r.TodayCostUsd = t.CostUsd
	}
	r.SuccessRate, r.LatencyMs, r.Status, r.CircuitOpen, r.AvailableFrom = s.channelHealth(v, ch.ID, ch.Enabled)
	return r
}

// offerRead 组合供给源展示行(健康随其渠道,渠道禁用→disabled)。
//
// m 是该 offer 所属模型 —— 成本派生要模型的官方价绑定,故不再是纯装饰函数。
// 时刻与 settings 取 v 里的(一次读请求一份):一次列表里所有行的成本必须同一时刻,
// 否则跨峰谷边界的那几毫秒会让同一次响应里两行价格对不上。
func (s *Server) offerRead(v *adminView, m domain.ModelRow, of domain.OfferRead) domain.OfferRead {
	// 厂商推断只看该供给源自己的上游名;为空则交给模型级 InferredVendor 兜底(前端逻辑)。
	of.InferredVendor = pricing.InferVendor(of.UpstreamModel)
	c := s.gw.CostQuote(m, of, v.settings, v.now)
	of.Cost = &c
	ch, ok := v.chByID[of.ChannelID]
	if !ok {
		of.Status = domain.StatusDisabled
		return of
	}
	of.SuccessRate, of.LatencyMs, of.Status, _, _ = s.channelHealth(v, of.ChannelID, ch.Enabled)
	return of
}

// modelsListRead 目录列表(每模型带已组合 offers + 今日用量)。
func (s *Server) modelsListRead() ([]domain.ModelRead, error) {
	settings, err := s.st.GetSettings()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	v, err := s.buildView()
	if err != nil {
		return nil, err
	}
	today, err := s.modelTodayStats(now, settings.TZOffsetMin)
	if err != nil {
		return nil, err
	}
	models, err := s.st.ListModels()
	if err != nil {
		return nil, err
	}
	out := make([]domain.ModelRead, 0, len(models))
	for _, m := range models {
		offers, err := s.st.ListModelOffers(m.ID)
		if err != nil {
			return nil, err
		}
		decorated := make([]domain.OfferRead, 0, len(offers))
		for _, o := range offers {
			decorated = append(decorated, s.offerRead(v, m, o))
		}
		mr := domain.ModelRead{
			ID: m.ID, Name: m.PublicName(), DisplayName: m.DisplayName, OriginalName: m.Name,
			ContextWindow: m.ContextWindow,
			Capabilities:  m.Capabilities, Enabled: m.Enabled, Offers: decorated,
			OfficialVendor: m.OfficialVendor, OfficialModelName: m.OfficialModelName,
			InferredVendor: pricing.InferVendor(m.Name),
			RateOverride:   m.RateOverride,
		}
		if u, ok := today[m.PublicName()]; ok {
			mr.TodayRequests = u.Requests
			mr.SuccessRate = (1 - u.ErrorRate) * 100
		} else {
			mr.SuccessRate = 100
		}
		out = append(out, mr)
	}
	return out, nil
}

// ---------------- 图表补齐 ----------------

// seriesBuckets 生成连续桶标签(本地时区)。hour=YYYY-MM-DD HH, day=YYYY-MM-DD。
func seriesBuckets(bucket string, tz int, endUTC time.Time, n int) []string {
	off := time.Duration(tz) * time.Minute
	out := make([]string, 0, n)
	switch bucket {
	case "hour":
		h := time.Date(endUTC.Year(), endUTC.Month(), endUTC.Day(), endUTC.Hour(), 0, 0, 0, time.UTC)
		start := h.Add(-time.Duration(n-1) * time.Hour)
		for i := 0; i < n; i++ {
			out = append(out, start.Add(time.Duration(i)*time.Hour).Add(off).Format("2006-01-02 15"))
		}
	case "day":
		dayStart, _ := store.LocalDayWindowUTC(tz, endUTC)
		start := dayStart.Add(-time.Duration(n-1) * 24 * time.Hour)
		for i := 0; i < n; i++ {
			out = append(out, start.Add(time.Duration(i)*24*time.Hour).Add(off).Format("2006-01-02"))
		}
	}
	return out
}

// fillSeries 将 DB 聚合(可能缺桶)填成连续序列,缺桶补零。
func fillSeries(points []domain.MetricPoint, buckets []string) []domain.MetricPoint {
	by := make(map[string]domain.MetricPoint, len(points))
	for _, p := range points {
		by[p.TS] = p
	}
	out := make([]domain.MetricPoint, 0, len(buckets))
	for _, b := range buckets {
		p, ok := by[b]
		if !ok {
			p.TS = b
		}
		out = append(out, p)
	}
	return out
}

// overview 组装 Dashboard 首屏。窗口由 rng 决定:起止都落在窗口内的按日曲线 + 同窗口汇总。
// 桶粒度随窗口伸缩:≤3 天按小时,>3 天按日 —— 一天 1 个点太粗,30 天 720 个点太密。
//
// 首屏要给站主看的是「这门生意」,故除全站请求/错误外还带客户归属口径的营收/成本/毛利
// (Totals,见 domain.MarginTotals)。全站成本(totalCostUsd)保留但不再当营收用 ——
// 它含站主自用与无归属流量,与营收不同源。
func (s *Server) overview(rng statRange) (domain.OverviewResp, error) {
	settings, err := s.st.GetSettings()
	if err != nil {
		return domain.OverviewResp{}, err
	}
	tz := settings.TZOffsetMin

	var resp domain.OverviewResp
	bucket := "day"
	if rng.days <= 3 {
		bucket = "hour"
	}
	pts, err := s.st.QuerySeries(bucket, rng.from, rng.to, tz)
	if err != nil {
		return resp, err
	}
	resp.Points = fillSeries(pts, seriesBuckets(bucket, tz, rng.to, bucketCount(rng, bucket)))

	// 汇总口径与曲线窗口一致。
	reqs, errs, cost, err := s.st.WindowTotals(rng.from, rng.to)
	if err != nil {
		return resp, err
	}
	resp.TotalRequests, resp.TotalErrors, resp.TotalCostUsd = reqs, errs, cost
	avg, err := s.st.AvgFirstTokenMsSince(rng.from)
	if err != nil {
		return resp, err
	}
	resp.AvgFirstTokenMs = int64(avg)

	// 经营口径:客户归属的营收/成本/毛利,同一批请求行算出(见 store.WindowTotalsCustomers)。
	// 曲线也走客户归属口径 —— 与合计同源,否则「曲线求和 ≠ 条上营收」。
	cpts, err := s.st.QuerySeriesCustomers(bucket, rng.from, rng.to, tz)
	if err != nil {
		return resp, err
	}
	resp.CustomerPoints = fillSeries(cpts, seriesBuckets(bucket, tz, rng.to, bucketCount(rng, bucket)))

	creqs, revenue, ccost, err := s.st.WindowTotalsCustomers(rng.from, rng.to)
	if err != nil {
		return resp, err
	}
	resp.Totals = domain.MarginTotals{
		Requests:   creqs,
		RevenueUsd: revenue,
		CostUsd:    ccost,
		MarginUsd:  revenue - ccost,
		MarginRate: marginRate(revenue, ccost),
	}
	return resp, nil
}

// marginRate 毛利率 = 毛利 / 营收;营收为 0 时返回 0(没有分母就不臆造 100%)。
func marginRate(revenue, cost float64) float64 {
	if revenue == 0 {
		return 0
	}
	return (revenue - cost) / revenue
}

// bucketCount 曲线应补多少个桶。
//
// 日桶直接用 rng.days(自然日跨度)—— 不能用「窗口时长 ÷ 24h 再四舍五入」反推:
// 预设窗口的 to 是「此刻」而非当天结束,时长里含着当天已过的一小截,
// round 会把这一截吞掉。例:days=7 在本地 11:57 时,时长 = 6d11.95h,
// round(6.498) = 6 → 曲线少画一天(当天被整个丢掉)。过了 12:00 小数部分
// ≥0.5 又会对上,所以这个 bug 按时辰飘,不是稳定失败。
//
// 小时桶同理,但它要覆盖「已经过去的小时 + 当前这个不完整小时」,
// 故取 floor(时长) + 1:11:57 → 12 个桶(0..11 点),12:24 → 13 个(0..12 点)。
func bucketCount(rng statRange, bucket string) int {
	if bucket == "hour" {
		n := int(rng.to.Sub(rng.from).Hours()) + 1
		if n < 1 {
			n = 1
		}
		return n
	}
	if rng.days < 1 {
		return 1
	}
	return rng.days
}
