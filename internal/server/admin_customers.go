package server

import (
	"fmt"
	"net/http"

	"personal-ai-gateway/internal/domain"
	"personal-ai-gateway/internal/store"
)

// 客户关注区(admin):「客户」维度的经营视角 —— 谁在消耗、谁快断粮。
//
// 首屏此前全是站点侧聚合(全站请求/渠道健康/模型排行),看不到单个客户。
// 站主每天要盯的两件事在这里:
//   - 欠费风险:余额 ≤0 已被拒(客户在流失),或余额撑不过一天;
//   - 消耗排行:窗口内营收 Top 客户(涨得快的可能是异常或放量)。
//
// 站主自己(admin)不是客户,不进名单 —— 它没有钱包门禁,列进来只会稀释告警。

// handleCustomersFocus 客户关注区:余额告警 + 窗口内消耗排行。
func (s *Server) handleCustomersFocus(w http.ResponseWriter, r *http.Request) {
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
	dist, err := s.st.QueryCustomerDist(rng.from, rng.to)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	wallets, err := s.st.ListCustomerWallets()
	if err != nil {
		writeStoreErr(w, err)
		return
	}

	byOwner := make(map[int64]store.CustomerDist, len(dist))
	for _, d := range dist {
		byOwner[d.OwnerID] = d
	}

	// 余额告警:以全部客户为基准(没消耗但有欠款余额的客户同样要看见)。
	atRisk := make([]domain.CustomerRow, 0, 4)
	for _, wl := range wallets {
		spent := byOwner[wl.ID].ChargeUsd
		switch {
		// 欠费线(与用户确认的口径):
		//   balance ≤ 0          → depleted,转发已被拒,客户正在流失;
		//   0 < balance < 窗口消耗 → low,按当前速度今天就会断。
		case wl.Balance <= 0:
			atRisk = append(atRisk, domain.CustomerRow{
				ID: wl.ID, Username: wl.Username, BalanceUsd: wl.Balance,
				SpendUsd: spent, Requests: byOwner[wl.ID].Requests,
				Risk: "depleted", Note: "余额已用尽，转发被拒（HTTP 402）",
			})
		case spent > 0 && wl.Balance < spent:
			atRisk = append(atRisk, domain.CustomerRow{
				ID: wl.ID, Username: wl.Username, BalanceUsd: wl.Balance,
				SpendUsd: spent, Requests: byOwner[wl.ID].Requests, Risk: "low",
				Note: fmt.Sprintf("余额 %.2f 不足一天消耗 %.2f，今天可能被拒", wl.Balance, spent),
			})
		}
	}

	// 消耗排行:窗口内营收降序(有消耗才有行,没消耗的不占位)。
	balance := make(map[int64]float64, len(wallets))
	name := make(map[int64]string, len(wallets))
	for _, wl := range wallets {
		balance[wl.ID] = wl.Balance
		name[wl.ID] = wl.Username
	}
	top := make([]domain.CustomerRow, 0, len(dist))
	for _, d := range dist {
		top = append(top, domain.CustomerRow{
			ID: d.OwnerID, Username: name[d.OwnerID], BalanceUsd: balance[d.OwnerID],
			SpendUsd: d.ChargeUsd, Requests: d.Requests, Risk: "ok",
		})
	}

	writeJSON(w, http.StatusOK, domain.CustomerFocusResp{
		Window: "近 1 天（同首屏筛选器）",
		AtRisk: atRisk,
		Top:    top,
	})
}
