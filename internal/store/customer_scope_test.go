package store

import (
	"testing"

	"personal-ai-gateway/internal/domain"
)

/*
客户归属口径(issue #13):营收/成本/毛利只算 role=user 名下的请求。

关键不变量:
  - 站主(admin)自用流量不进经营口径 —— 它没有钱包门禁,是自用不是生意;
  - 无归属流量(owner_id=0,含改造前旧日志)同样不进;
  - 营收/成本取自同一批行,故差额(毛利)可直接相减,不会因口径不同源而失真。
*/
func TestWindowTotalsCustomersScope(t *testing.T) {
	st := newTestStore(t)

	// 三类账号:站长(admin)、客户(user)、另一个客户。
	owner, err := st.CreateAdmin("owner", "x", domain.RoleAdmin)
	mustNoErr(t, err, "create admin")
	cust, err := st.CreateAdmin("cust", "x", domain.RoleUser)
	mustNoErr(t, err, "create user")
	cust2, err := st.CreateAdmin("cust2", "x", domain.RoleUser)
	mustNoErr(t, err, "create user 2")

	at := ts(2026, 9, 15, 3, 0)
	seed := func(o int64, cost, charge float64) {
		mustNoErr(t, st.InsertLog(domain.LogRow{
			TS: at, Model: "m", ChannelName: "ch", TokenName: "tk", OwnerID: o,
			Status: 200, PromptTokens: 10, CostUsd: cost, ChargeUsd: charge,
		}), "seed log")
	}
	seed(cust.ID, 1, 2)   // 客户:成本 1,营收 2
	seed(cust2.ID, 3, 5)  // 客户:成本 3,营收 5
	seed(owner.ID, 10, 0) // 站主自用:成本 10,不该进
	seed(0, 7, 0)         // 无归属(旧日志):成本 7,不该进

	from, to := ts(2026, 9, 15, 0, 0), ts(2026, 9, 16, 0, 0)
	reqs, revenue, cost, err := st.WindowTotalsCustomers(from, to)
	mustNoErr(t, err, "customer window totals")

	mustEqual(t, reqs, 2, "客户请求数(站主自用与无归属应被排除)")
	if revenue != 7 {
		t.Errorf("营收 = %v, want 7(2+5)", revenue)
	}
	if cost != 4 {
		t.Errorf("成本 = %v, want 4(1+3)", cost)
	}

	// 全站口径仍然覆盖全部四行 —— 两个口径互不干扰。
	greqs, _, gcost, err := st.WindowTotals(from, to)
	mustNoErr(t, err, "global window totals")
	mustEqual(t, greqs, 4, "全站请求数")
	if gcost != 21 {
		t.Errorf("全站成本 = %v, want 21", gcost)
	}
}

// TestQueryCustomerDist 按客户聚合:只出客户、按营收降序、无消耗客户不占位。
func TestQueryCustomerDist(t *testing.T) {
	st := newTestStore(t)
	cust, err := st.CreateAdmin("c1", "x", domain.RoleUser)
	mustNoErr(t, err, "create c1")
	cust2, err := st.CreateAdmin("c2", "x", domain.RoleUser)
	mustNoErr(t, err, "create c2")

	at := ts(2026, 9, 15, 6, 0)
	for _, l := range []domain.LogRow{
		{TS: at, OwnerID: cust2.ID, Model: "m", Status: 200, CostUsd: 1, ChargeUsd: 9},
		{TS: at, OwnerID: cust.ID, Model: "m", Status: 200, CostUsd: 2, ChargeUsd: 3},
		{TS: at, OwnerID: cust.ID, Model: "m", Status: 200, CostUsd: 2, ChargeUsd: 3},
		{TS: at, OwnerID: 0, Model: "m", Status: 200, CostUsd: 5, ChargeUsd: 5},
	} {
		mustNoErr(t, st.InsertLog(l), "seed log")
	}

	dist, err := st.QueryCustomerDist(ts(2026, 9, 15, 0, 0), ts(2026, 9, 16, 0, 0))
	mustNoErr(t, err, "customer dist")
	if len(dist) != 2 {
		t.Fatalf("客户数 = %d, want 2(无归属行不得出现)", len(dist))
	}
	// 营收降序:c2(9) 在 c1(6) 前,尽管 c1 请求数更多。
	if dist[0].OwnerID != cust2.ID {
		t.Errorf("首行 owner = %d, want %d(c2 营收更高)", dist[0].OwnerID, cust2.ID)
	}
	if dist[1].Requests != 2 || dist[1].ChargeUsd != 6 || dist[1].CostUsd != 4 {
		t.Errorf("c1 行 = %+v, want reqs=2 charge=6 cost=4", dist[1])
	}
}

// TestListCustomerWallets 只列 role=user —— 站主账号不是客户,不该进欠费名单。
func TestListCustomerWallets(t *testing.T) {
	st := newTestStore(t)
	_, err := st.CreateAdmin("owner", "x", domain.RoleAdmin)
	mustNoErr(t, err, "create admin")
	rich, err := st.CreateAdmin("rich", "x", domain.RoleUser)
	mustNoErr(t, err, "create rich")
	poor, err := st.CreateAdmin("poor", "x", domain.RoleUser)
	mustNoErr(t, err, "create poor")
	_, err = st.TopupBalance(rich.ID, 100, "topup")
	mustNoErr(t, err, "topup rich")
	_, err = st.TopupBalance(poor.ID, -5, "adjust")
	mustNoErr(t, err, "debit poor")

	ws, err := st.ListCustomerWallets()
	mustNoErr(t, err, "list wallets")
	if len(ws) != 2 {
		t.Fatalf("钱包行数 = %d, want 2(admin 不得出现)", len(ws))
	}
	// 余额升序:欠费的 poor 在前(最危险先看)。
	if ws[0].Username != "poor" || ws[0].Balance != -5 {
		t.Errorf("首行 = %+v, want poor/-5", ws[0])
	}
	if ws[1].Username != "rich" || ws[1].Balance != 100 {
		t.Errorf("次行 = %+v, want rich/100", ws[1])
	}
}

// TestQuerySeriesCustomers 客户口径曲线与合计同源:逐桶营收求和 = 合计营收。
func TestQuerySeriesCustomers(t *testing.T) {
	st := newTestStore(t)
	cust, err := st.CreateAdmin("c1", "x", domain.RoleUser)
	mustNoErr(t, err, "create c1")

	for _, l := range []domain.LogRow{
		{TS: ts(2026, 9, 15, 1, 0), OwnerID: cust.ID, Model: "m", Status: 200, CostUsd: 1, ChargeUsd: 2},
		{TS: ts(2026, 9, 15, 2, 0), OwnerID: cust.ID, Model: "m", Status: 200, CostUsd: 3, ChargeUsd: 4},
		{TS: ts(2026, 9, 15, 2, 30), OwnerID: 0, Model: "m", Status: 200, CostUsd: 9, ChargeUsd: 9},
	} {
		mustNoErr(t, st.InsertLog(l), "seed log")
	}

	from, to := ts(2026, 9, 15, 0, 0), ts(2026, 9, 16, 0, 0)
	pts, err := st.QuerySeriesCustomers("hour", from, to, 0)
	mustNoErr(t, err, "customer series")

	var sumCharge, sumCost float64
	var sumReqs int
	for _, p := range pts {
		sumCharge += p.ChargeUsd
		sumCost += p.CostUsd
		sumReqs += p.Requests
	}
	_, revenue, cost, err := st.WindowTotalsCustomers(from, to)
	mustNoErr(t, err, "customer totals")
	if sumCharge != revenue || sumCost != cost || sumReqs != 2 {
		t.Errorf("曲线求和 (reqs=%d charge=%v cost=%v) 与合计 (reqs=2 charge=%v cost=%v) 不一致",
			sumReqs, sumCharge, sumCost, revenue, cost)
	}
	if len(pts) != 2 {
		t.Errorf("桶数 = %d, want 2(1 点与 2 点各一桶)", len(pts))
	}
}
