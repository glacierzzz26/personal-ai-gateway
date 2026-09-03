package server

import (
	"net/http"
	"time"

	"personal-ai-gateway/internal/domain"
)

// handleOverview Dashboard 首屏。
func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	ov, err := s.overview()
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ov)
}

// handleUsage 用量统计:按 dim 聚合行 + 日曲线。
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
	days := queryInt(r, "days", 7)
	if days < 1 {
		days = 1
	}
	if days > 90 {
		days = 90
	}
	settings, err := s.st.GetSettings()
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	now := time.Now().UTC()
	from := now.AddDate(0, 0, -days)

	rows, err := s.st.QueryDimSummary(dim, from, now, 50)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	series, err := s.st.QuerySeries("day", from, now, settings.TZOffsetMin)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if rows == nil {
		rows = []domain.UsageRow{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"rows": rows,
		"days": fillSeries(series, seriesBuckets("day", settings.TZOffsetMin, now, days)),
	})
}
