package server

import (
	"net/http"
	"net/http/cookiejar"
	"strconv"
	"testing"
)

// TestMeEndpointsAndWallet 用户自助面:/me/* 锁本人,admin 面 403;充值后余额可见。
func TestMeEndpointsAndWallet(t *testing.T) {
	srv, admin, st := newTestServer(t)
	base := srv.URL
	bootstrap(t, admin, base)

	// 建一个客户账号。
	code, body := doJSON(t, admin, http.MethodPost, base+"/api/v1/users",
		map[string]any{"username": "cust", "password": "password123", "role": "user"})
	mustStatus(t, code, http.StatusOK, "create user: "+string(body))

	users := decode[[]map[string]any](t, mustGet(t, admin, base+"/api/v1/users"))
	var custID int64
	for _, u := range users {
		if u["username"] == "cust" {
			custID = int64(u["id"].(float64))
		}
	}
	if custID == 0 {
		t.Fatalf("cust not found in %+v", users)
	}
	// 直接经 store 种一笔启动余额,再走接口充值,合计 12.5 + 7.5 = 20。
	if _, err := st.TopupBalance(custID, 12.5, "seed"); err != nil {
		t.Fatalf("seed topup: %v", err)
	}
	code, body = doJSON(t, admin, http.MethodPost, base+"/api/v1/users/"+strconv.FormatInt(custID, 10)+"/topup",
		map[string]any{"amount": 7.5, "note": "test"})
	mustStatus(t, code, http.StatusOK, "topup: "+string(body))

	// 客户登录:同一个 server,另起一个 cookie jar(否则会串 admin 的会话)。
	jar, _ := cookiejar.New(nil)
	cust := &http.Client{Jar: jar}
	code, body = doJSON(t, cust, http.MethodPost, base+"/api/v1/auth/login",
		map[string]any{"username": "cust", "password": "password123"})
	mustStatus(t, code, http.StatusOK, "cust login: "+string(body))

	code, body = doJSON(t, cust, http.MethodGet, base+"/api/v1/me/balance", nil)
	mustStatus(t, code, http.StatusOK, "me/balance: "+string(body))
	resp := decode[map[string]any](t, body)
	if b := resp["balanceUsd"].(float64); b < 19.99 || b > 20.01 {
		t.Fatalf("balance = %v, want ~20", b)
	}

	// /me/usage、/me/logs 可用(空数据也应 200 且形状正确)。
	code, body = doJSON(t, cust, http.MethodGet, base+"/api/v1/me/usage?dim=model", nil)
	mustStatus(t, code, http.StatusOK, "me/usage: "+string(body))
	code, _ = doJSON(t, cust, http.MethodGet, base+"/api/v1/me/logs", nil)
	mustStatus(t, code, http.StatusOK, "me/logs")

	// 客户访问管理员面应 403。
	code, _ = doJSON(t, cust, http.MethodGet, base+"/api/v1/users", nil)
	mustStatus(t, code, http.StatusForbidden, "cust → /users")
	code, _ = doJSON(t, cust, http.MethodGet, base+"/api/v1/overview", nil)
	mustStatus(t, code, http.StatusForbidden, "cust → /overview")

	// 给 admin 自己设倍率应被拒(只有客户账号有钱包/倍率)。
	code, _ = doJSON(t, admin, http.MethodPatch, base+"/api/v1/users/1/rate",
		map[string]any{"rate": 0.5})
	mustStatus(t, code, http.StatusBadRequest, "rate on admin")

	// admin 给客户设倍率成功,流水接口可见充值记录。
	code, body = doJSON(t, admin, http.MethodPatch,
		base+"/api/v1/users/"+strconv.FormatInt(custID, 10)+"/rate", map[string]any{"rate": 2.0})
	mustStatus(t, code, http.StatusOK, "rate on cust: "+string(body))
	code, body = doJSON(t, admin, http.MethodGet,
		base+"/api/v1/users/"+strconv.FormatInt(custID, 10)+"/balance-logs", nil)
	mustStatus(t, code, http.StatusOK, "balance-logs: "+string(body))
	logs := decode[[]map[string]any](t, body)
	if len(logs) == 0 {
		t.Fatalf("balance-logs empty, want topup entry")
	}
}

func mustGet(t *testing.T, c *http.Client, url string) []byte {
	t.Helper()
	code, body := doJSON(t, c, http.MethodGet, url, nil)
	if code != http.StatusOK {
		t.Fatalf("GET %s: %d %s", url, code, body)
	}
	return body
}
