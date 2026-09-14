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
	writeJSON(w, http.StatusOK, ov)
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
