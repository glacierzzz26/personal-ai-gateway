package store

import (
	"testing"
	"time"

	"personal-ai-gateway/internal/domain"
)

func nowUTCForTest() time.Time { return time.Now().UTC() }

// TestSettleRequestPersistsCostAudit 成本审计两列(迁移 m0012)必须落库并可读回:
// 分时之后同一模型每天有两个成本价,没有这两列无法事后核对「那笔为什么按这个价记」。
func TestSettleRequestPersistsCostAudit(t *testing.T) {
	st := newTestStore(t)
	tk, err := st.CreateToken("k", nil, "", []string{"*"}, 0, 60, nil, "sha-audit", "sk-gw-a…")
	mustNoErr(t, err, "create token")

	mustNoErr(t, st.SettleRequest(domain.LogRow{
		TS: nowUTCForTest(), Model: "deepseek-flash", TokenID: tk.ID, TokenName: tk.Name,
		Status: 200, CostUsd: 0.00026, ChargeUsd: 0.00156,
		CostSource: "official", PriceWindow: "peak",
	}, false), "settle")

	items, _, err := st.ListLogs(LogFilter{}, 0)
	mustNoErr(t, err, "list logs")
	if len(items) != 1 {
		t.Fatalf("logs = %+v", items)
	}
	mustEqual(t, items[0].CostSource, "official", "costSource 落库")
	mustEqual(t, items[0].PriceWindow, "peak", "priceWindow 落库")
}

// TestSettleRequestChargesOwnerWallet 结算事务:落账 + 令牌累加 + 扣钱包 + 流水,原子完成。
func TestSettleRequestChargesOwnerWallet(t *testing.T) {
	st := newTestStore(t)
	u, err := st.CreateAdmin("cust", "h", domain.RoleUser)
	mustNoErr(t, err, "create user")
	if _, err := st.TopupBalance(u.ID, 10, "seed"); err != nil {
		t.Fatalf("topup: %v", err)
	}
	tk, err := st.CreateToken("k", &u.ID, "c", []string{"*"}, 0, 60, nil, "sha-w", "sk-gw-w…")
	mustNoErr(t, err, "create token")

	log := domain.LogRow{
		TS: nowUTCForTest(), Model: "m", TokenID: tk.ID, TokenName: tk.Name, OwnerID: u.ID,
		Status: 200, CostUsd: 0.5, ChargeUsd: 1.2,
	}
	mustNoErr(t, st.SettleRequest(log, true), "settle")

	bal, err := st.GetBalance(u.ID)
	mustNoErr(t, err, "get balance")
	if bal < 8.799 || bal > 8.801 {
		t.Fatalf("balance = %v, want ~8.8", bal)
	}
	tr, _ := st.GetToken(tk.ID)
	if tr.UsedUsd < 1.199 || tr.UsedUsd > 1.201 {
		t.Fatalf("token used = %v, want ~1.2 (售价口径)", tr.UsedUsd)
	}
	recs, err := st.ListBalanceLogs(u.ID, 10)
	mustNoErr(t, err, "list balance logs")
	if len(recs) != 2 || recs[0].Reason != "charge" {
		t.Fatalf("balance logs = %+v", recs)
	}
	if recs[0].Delta > -1.199 || recs[0].Delta < -1.201 {
		t.Fatalf("charge delta = %v, want -1.2", recs[0].Delta)
	}

	items, total, err := st.ListLogs(LogFilter{OwnerID: u.ID}, 0)
	mustNoErr(t, err, "list owner logs")
	if total != 1 || len(items) != 1 || items[0].ChargeUsd < 1.199 {
		t.Fatalf("owner logs = %+v total=%d", items, total)
	}
	// 他人维度查不到。
	_, other, _ := st.ListLogs(LogFilter{OwnerID: u.ID + 1}, 0)
	mustEqual(t, other, 0, "other owner sees nothing")
}

// TestSettleRequestNoWallet 无归属 key(owner_id=0)/wallet=false 时不扣钱包,只落账。
func TestSettleRequestNoWallet(t *testing.T) {
	st := newTestStore(t)
	tk, _ := st.CreateToken("g", nil, "", []string{"*"}, 0, 60, nil, "sha-g2", "sk-gw-g2…")
	log := domain.LogRow{TS: nowUTCForTest(), Model: "m", TokenID: tk.ID, OwnerID: 0, Status: 200, CostUsd: 1, ChargeUsd: 1}
	mustNoErr(t, st.SettleRequest(log, false), "settle")
	tr, _ := st.GetToken(tk.ID)
	if tr.UsedUsd < 0.999 || tr.UsedUsd > 1.001 {
		t.Fatalf("token used = %v, want ~1", tr.UsedUsd)
	}
}

// TestTopupBalance 充值/调整的读写往返(倍率已按模型存,不在账号上)。
func TestTopupBalance(t *testing.T) {
	st := newTestStore(t)
	u, _ := st.CreateAdmin("u", "h", domain.RoleUser)
	rec, err := st.TopupBalance(u.ID, 5, "first")
	mustNoErr(t, err, "topup")
	if rec.BalanceAfter != 5 || rec.Delta != 5 {
		t.Fatalf("topup rec = %+v", rec)
	}
	if _, err := st.TopupBalance(u.ID, -2, "refund"); err != nil {
		t.Fatalf("adjust: %v", err)
	}
	bal, _ := st.GetBalance(u.ID)
	if bal != 3 {
		t.Fatalf("balance = %v, want 3", bal)
	}
	users, err := st.ListUsers()
	mustNoErr(t, err, "list users")
	if len(users) != 1 || users[0].BalanceUsd != 3 {
		t.Fatalf("users = %+v", users)
	}
}
