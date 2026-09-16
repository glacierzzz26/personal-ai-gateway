package server

import (
	"net/http"

	"personal-ai-gateway/internal/domain"
)

// handleOverview Dashboard 首屏。窗口由 ?days=1|7|30 或 ?from&to 决定。
func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	settings, err := s.st.GetSettings()
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	rng, err := parseStatRange(r, settings.TZOffsetMin)
	if err != nil {
		apiErr(w, http.StatusBadRequest, "validation", err.Error())
		return
	}
	ov, err := s.overview(rng)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	ov.Days = rng.days
	if rng.days <= 3 {
		ov.Bucket = "hour"
	} else {
		ov.Bucket = "day"
	}
	// 环比基准与前端 previousWindow 对齐:预设窗口(含默认的 1 天)是「与上一等长的
	// 自然日窗口比」—— 1 天即昨日整日,而不是前 1×24 小时(那会跨零点漂移)。
	// 自定义区间没有「上一等长区间」的自然对齐,故不回填(前端只在预设窗口下显示环比)。
	if !rng.custom {
		ov.Prev = s.prevTotals(rng)
	}
	writeJSON(w, http.StatusOK, ov)
}

// prevTotals 上一等长自然日窗口的合计(环比用)。只看合计,不取曲线。
//
// 窗口非零长度是 parseStatRange 保证的;这里仍做一次防御:days<1 视为 1 天。
func (s *Server) prevTotals(rng statRange) *domain.MarginTotals {
	days := rng.days
	if days < 1 {
		days = 1
	}
	// 当前窗口起点的前一个自然日窗口:[from-days, from)。
	to := rng.from
	from := to.AddDate(0, 0, -days)
	reqs, revenue, cost, err := s.st.WindowTotalsCustomers(from, to)
	if err != nil {
		return nil // 环比只是附加信息,取不到就不给,不让首屏整体失败
	}
	return &domain.MarginTotals{
		Requests:   reqs,
		RevenueUsd: revenue,
		CostUsd:    cost,
		MarginUsd:  revenue - cost,
		MarginRate: marginRate(revenue, cost),
	}
}

// handleUsage 用量统计:按 dim 聚合行 + 曲线。窗口同 /overview(days 或 from&to)。
func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	dim := queryStr(r, "dim")
	if dim == "" {
		dim = "model"
	}
	switch dim {
	case "model", "channel", "token":
	default:
		apiErr(w, http.StatusBadRequest, "validation", "dim must be model|channel|token")
		return
	}
	settings, err := s.st.GetSettings()
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	rng, err := parseStatRange(r, settings.TZOffsetMin)
	if err != nil {
		apiErr(w, http.StatusBadRequest, "validation", err.Error())
		return
	}
	rows, err := s.st.QueryDimSummary(dim, rng.from, rng.to, 50)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	series, err := s.st.QuerySeries("day", rng.from, rng.to, settings.TZOffsetMin)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if rows == nil {
		rows = []domain.UsageRow{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"rows": rows,
		"days": fillSeries(series, seriesBuckets("day", settings.TZOffsetMin, rng.to, rng.days)),
	})
}
