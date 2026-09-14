package server

import (
	"net/http"
	"net/http/cookiejar"
	"strconv"
	"testing"
)

// TestUserTokenCeiling 普通用户自助建/改令牌不得超过管理员设的上限;
// 管理员自己建令牌不受限。上限 0 = 不限。
func TestUserTokenCeiling(t *testing.T) {
	srv, admin, _ := newTestServer(t)
	base := srv.URL
	bootstrap(t, admin, base)

	// 建客户并设上限:额度 ≤ 5、RPM ≤ 30。
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
	cid := strconv.FormatInt(custID, 10)
	code, body = doJSON(t, admin, http.MethodPatch, base+"/api/v1/users/"+cid+"/ceiling",
		map[string]any{"quotaUsd": 5, "rpmLimit": 30})
	mustStatus(t, code, http.StatusOK, "set ceiling: "+string(body))
	// 给管理员自己设上限应被拒(仅客户账号有令牌上限)。
	code, _ = doJSON(t, admin, http.MethodPatch, base+"/api/v1/users/1/ceiling",
		map[string]any{"quotaUsd": 5, "rpmLimit": 30})
	mustStatus(t, code, http.StatusBadRequest, "ceiling on admin")

	jar, _ := cookiejar.New(nil)
	cust := &http.Client{Jar: jar}
	code, body = doJSON(t, cust, http.MethodPost, base+"/api/v1/auth/login",
		map[string]any{"username": "cust", "password": "password123"})
	mustStatus(t, code, http.StatusOK, "cust login: "+string(body))

	// /me/balance 应带回上限,供前端预校验。
	resp := decode[map[string]any](t, mustGet(t, cust, base+"/api/v1/me/balance"))
	if q := resp["tokenQuotaCeiling"].(float64); q != 5 {
		t.Fatalf("tokenQuotaCeiling = %v, want 5", q)
	}
	if r := resp["tokenRpmCeiling"].(float64); r != 30 {
		t.Fatalf("tokenRpmCeiling = %v, want 30", r)
	}

	newToken := func(quota float64, rpm int) (int, []byte) {
		return doJSON(t, cust, http.MethodPost, base+"/api/v1/tokens", map[string]any{
			"name": "k", "allowedModels": []string{"*"},
			"quotaUsd": quota, "rpmLimit": rpm, "expiresAt": nil,
		})
	}
	// 超额度上限 → 400。
	code, body = newToken(10, 10)
	mustStatus(t, code, http.StatusBadRequest, "quota over ceiling: "+string(body))
	// 超 RPM 上限 → 400。
	code, body = newToken(1, 100)
	mustStatus(t, code, http.StatusBadRequest, "rpm over ceiling: "+string(body))
	// 恰好等于上限 → 允许。
	code, body = newToken(5, 30)
	mustStatus(t, code, http.StatusOK, "at ceiling: "+string(body))
	created := decode[map[string]any](t, body)
	tokID := int64(created["token"].(map[string]any)["id"].(float64))

	// 编辑时同样受约束:先建小令牌、再改成不限,应被拒。
	code, body = doJSON(t, cust, http.MethodPatch,
		base+"/api/v1/tokens/"+strconv.FormatInt(tokID, 10),
		map[string]any{"name": "k", "allowedModels": []string{"*"},
			"quotaUsd": 0, "rpmLimit": 30, "expiresAt": nil, "status": "active"})
	mustStatus(t, code, http.StatusBadRequest, "edit bypass ceiling: "+string(body))

	// 管理员建令牌不受限:大额度也应通过。
	code, body = doJSON(t, admin, http.MethodPost, base+"/api/v1/tokens", map[string]any{
		"name": "adm", "allowedModels": []string{"*"},
		"quotaUsd": 9999, "rpmLimit": 9999, "expiresAt": nil,
	})
	mustStatus(t, code, http.StatusOK, "admin unlimited: "+string(body))
}
