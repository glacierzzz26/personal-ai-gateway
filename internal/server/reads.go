package server

import (
	"time"

	"personal-ai-gateway/internal/domain"
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
		now: now, tz: settings.TZOffsetMin,
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

// channelHealth 由最近窗口请求算成功率/延迟/状态。enabled=false 直接 disabled。
func (s *Server) channelHealth(v *adminView, id int64, enabled bool) (successRate float64, latencyMs int64, status domain.HealthStatus, open bool, availableFrom time.Time) {
	if !enabled {
		return 0, 0, domain.StatusDisabled, false, time.Time{}
	}
	if open, at := s.eng.CircuitOpen(id); open {
		return 0, s.eng.LatencyMS(id), domain.StatusDown, true, at
	}
	st, has := v.chRecent[id]
	if !has || st.Requests == 0 {
		return 100, s.eng.LatencyMS(id), domain.StatusHealthy, false, time.Time{}
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
		ID: ch.ID, Name: ch.Name, Provider: ch.Provider, BaseURL: ch.BaseURL,
		Priority: ch.Priority, Weight: ch.Weight,
		KeyMasked: ch.KeyMasked, ModelCount: v.chModels[ch.ID],
		TimeoutMs: ch.TimeoutMs, Tags: ch.Tags, Enabled: ch.Enabled,
		Note: ch.Note, MaxFailures: ch.MaxFailures, CooldownSec: ch.CooldownSec,
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
func (s *Server) offerRead(v *adminView, of domain.OfferRead) domain.OfferRead {
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
			decorated = append(decorated, s.offerRead(v, o))
		}
		mr := domain.ModelRead{
			ID: m.ID, Name: m.PublicName(), DisplayName: m.DisplayName, OriginalName: m.Name,
			ContextWindow: m.ContextWindow,
			Capabilities:  m.Capabilities, Enabled: m.Enabled, Offers: decorated,
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

// overview 组装 Dashboard 首屏(近24h 小时曲线 + 近7d 日曲线 + 汇总)。
func (s *Server) overview() (domain.OverviewResp, error) {
	settings, err := s.st.GetSettings()
	if err != nil {
		return domain.OverviewResp{}, err
	}
	now := time.Now().UTC()
	tz := settings.TZOffsetMin
	hourFrom := now.Add(-24 * time.Hour)
	dayFrom := now.Add(-7 * 24 * time.Hour)

	var resp domain.OverviewResp
	hp, err := s.st.QuerySeries("hour", hourFrom, now, tz)
	if err != nil {
		return resp, err
	}
	dp, err := s.st.QuerySeries("day", dayFrom, now, tz)
	if err != nil {
		return resp, err
	}
	resp.Hours = fillSeries(hp, seriesBuckets("hour", tz, now, 24))
	resp.Days = fillSeries(dp, seriesBuckets("day", tz, now, 7))

	// 汇总口径:近 7 天(与 Days 一致)。
	reqs, errs, cost, err := s.st.WindowTotals(dayFrom, now)
	if err != nil {
		return resp, err
	}
	resp.TotalRequests, resp.TotalErrors, resp.TotalCostUsd = reqs, errs, cost
	avg, err := s.st.AvgFirstTokenMsSince(dayFrom)
	if err != nil {
		return resp, err
	}
	resp.AvgFirstTokenMs = int64(avg)
	return resp, nil
}
