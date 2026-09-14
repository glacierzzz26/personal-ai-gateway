package server

import (
	"net/http"
	"time"

	"personal-ai-gateway/internal/domain"
	"personal-ai-gateway/internal/store"
)

// 用户自助面(/api/v1/me/*):普通用户视角的余额与用量。
// 作用域一律锁死在当前会话账号,前端/客户端无法越权查他人(与 /tokens 同规矩)。

// handleMeBalance 我的余额 + 近期账变流水。
// 顺带带出计价币种:普通用户读不到 /settings,前端无法自行水合,余额符号会错。
func (s *Server) handleMeBalance(w http.ResponseWriter, r *http.Request) {
	me := s.currentAdmin(r)
	bal, err := s.st.GetBalance(me.ID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	logs, err := s.st.ListBalanceLogs(me.ID, queryInt(r, "limit", 50))
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	cur := domain.CurrencyCNY
	if st, err := s.st.GetSettings(); err == nil && st.DisplayCurrency.Valid() {
		cur = st.DisplayCurrency
	}
	// 顺带带出令牌上限:用户建令牌时前端据此预校验,不然提交后才吃 400。
	var qCeil float64
	var rpmCeil int
	if me.Role == domain.RoleUser {
		qCeil, rpmCeil, _ = s.st.TokenCeiling(me.ID)
	}
	writeJSON(w, http.StatusOK, domain.BalanceResp{
		BalanceUsd: bal, Logs: logs, Currency: cur,
		TokenQuotaCeiling: qCeil, TokenRpmCeiling: rpmCeil,
	})
}

// handleMeUsage 我的用量:按维度聚合行 + 日曲线(dim=model|token,days 默认 7)。
func (s *Server) handleMeUsage(w http.ResponseWriter, r *http.Request) {
	me := s.currentAdmin(r)
	dim := queryStr(r, "dim")
	if dim == "" {
		dim = "model"
	}
	switch dim {
	case "model", "token":
	default:
		apiErr(w, http.StatusBadRequest, "validation", "dim must be model|token")
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
	rows, err := s.st.QueryDimSummaryOwner(dim, from, now, 50, me.ID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	series, err := s.st.QuerySeriesOwner("day", from, now, settings.TZOffsetMin, me.ID)
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

// handleMeLogs 我的请求日志(owner 维度)。
func (s *Server) handleMeLogs(w http.ResponseWriter, r *http.Request) {
	me := s.currentAdmin(r)
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
		Token:   queryStr(r, "token"),
		Status:  queryStr(r, "status"),
		Keyword: queryStr(r, "kw"),
		OwnerID: me.ID, // 强制本人,不可越权
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
	// 用户面只给「我付了多少」:剥掉成本(costUsd)与来源 IP —— 那是站主的账。
	// ChargeUsd/TokenName 保留(自己的令牌、自己实付)。
	for i := range items {
		items[i].CostUsd = 0
		items[i].IP = ""
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}
