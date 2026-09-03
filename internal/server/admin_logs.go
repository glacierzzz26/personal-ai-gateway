package server

import (
	"net/http"

	"personal-ai-gateway/internal/store"
)

// handleLogsList 分页日志(新→旧),支持 model/channel/token/status/kw/时间筛选。
func (s *Server) handleLogsList(w http.ResponseWriter, r *http.Request) {
	settings, err := s.st.GetSettings()
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	from, err := queryTime(r, "from")
	if err != nil {
		apiErr(w, http.StatusBadRequest, "validation", "bad from: "+err.Error())
		return
	}
	to, err := queryTime(r, "to")
	if err != nil {
		apiErr(w, http.StatusBadRequest, "validation", "bad to: "+err.Error())
		return
	}
	limit, offset := pageRange(r, 50)
	f := store.LogFilter{
		Model:   queryStr(r, "model"),
		Channel: queryStr(r, "channel"),
		Token:   queryStr(r, "token"),
		Status:  queryStr(r, "status"),
		Keyword: queryStr(r, "kw"),
		From:    from,
		To:      to,
		Limit:   limit,
		Offset:  offset,
	}
	items, total, err := s.st.ListLogs(f, settings.TZOffsetMin)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

// handleLogsClear 清空全部日志(前端二次确认)。
func (s *Server) handleLogsClear(w http.ResponseWriter, r *http.Request) {
	if err := s.st.ClearLogs(); err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
