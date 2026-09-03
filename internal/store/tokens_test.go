package store

import (
	"testing"
	"time"

	"personal-ai-gateway/internal/domain"
)

func rfc(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func TestTokenLifecycleCharge(t *testing.T) {
	st := newTestStore(t)
	exp := rfc(time.Now().Add(24 * time.Hour))

	tk, err := st.CreateToken("ci", []string{"*"}, 1.0, 60, &exp, "sha1", "sk-gw-abc…")
	mustNoErr(t, err, "create token")
	if tk.Status != domain.TokenActive {
		t.Errorf("new token status = %s, want active", tk.Status)
	}
	if len(tk.AllowedModels) != 1 || tk.AllowedModels[0] != "*" {
		t.Errorf("allowed models mismatch: %+v", tk.AllowedModels)
	}

	// 扣减:0.6 成功,再 0.6 超额度被拒,且 used 保持不变
	mustNoErr(t, st.ChargeToken(tk.ID, 0.6), "charge 0.6")
	err = st.ChargeToken(tk.ID, 0.6)
	mustErrIs(t, err, ErrQuotaExceeded, "charge over quota")
	read, _ := st.GetToken(tk.ID)
	if read.UsedUsd < 0.599 || read.UsedUsd > 0.601 {
		t.Errorf("used = %v, want ~0.6", read.UsedUsd)
	}
	if read.LastUsedAt == nil {
		t.Error("lastUsedAt should be set after charge")
	}

	// 由 sha 精确查
	row, err := st.LookupTokenBySHA256("sha1")
	mustNoErr(t, err, "lookup by sha")
	if row.ID != tk.ID || row.QuotaUsd != 1.0 {
		t.Errorf("lookup row mismatch: %+v", row)
	}

	// 停用 & 删除
	mustNoErr(t, st.SetTokenStatus(tk.ID, domain.TokenDisabled), "disable token")
	read, _ = st.GetToken(tk.ID)
	if read.Status != domain.TokenDisabled {
		t.Errorf("status = %s, want disabled", read.Status)
	}
	mustNoErr(t, st.DeleteToken(tk.ID), "delete token")
	_, err = st.GetToken(tk.ID)
	mustErrIs(t, err, ErrNotFound, "get deleted token")
}

func TestTokenExpiredDerived(t *testing.T) {
	st := newTestStore(t)
	past := rfc(time.Now().Add(-time.Hour))
	tk, err := st.CreateToken("expired", []string{}, 0, 60, &past, "sha-exp", "sk-gw-exp…")
	mustNoErr(t, err, "create expired token")
	// 读接口应派生 expired,即使落库是 active
	read, _ := st.GetToken(tk.ID)
	if read.Status != domain.TokenExpired {
		t.Errorf("derived status = %s, want expired", read.Status)
	}
	row, err := st.LookupTokenBySHA256("sha-exp")
	mustNoErr(t, err, "lookup")
	if row.Status != domain.TokenExpired {
		t.Errorf("lookup derived status = %s, want expired", row.Status)
	}
}

func TestUnlimitedQuota(t *testing.T) {
	st := newTestStore(t)
	tk, _ := st.CreateToken("unlim", []string{}, 0, 60, nil, "sha-u", "sk-gw-u…")
	mustNoErr(t, st.ChargeToken(tk.ID, 999), "unlimited charge ok")
}
