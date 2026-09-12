package server

import (
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"reflect"
	"strings"
	"testing"
)

func mustEqual(t *testing.T, got, want any, ctx string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: got %v (%T), want %v (%T)", ctx, got, got, want, want)
	}
}

// newClient 独立 cookie jar(来模拟不同账号并发登录)。
func newClient(t *testing.T) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar}
}

// loginAsUser 用独立 client 以给定账号登录。
func loginAsUser(t *testing.T, c *http.Client, base, username, password string) map[string]any {
	t.Helper()
	code, body := doJSON(t, c, http.MethodPost, base+"/api/v1/auth/login",
		map[string]any{"username": username, "password": password})
	mustStatus(t, code, http.StatusOK, "login "+username)
	return decode[map[string]any](t, body)
}

// createUser 管理员建号。
func createUser(t *testing.T, c *http.Client, base, username, password, role string) int64 {
	t.Helper()
	code, body := doJSON(t, c, http.MethodPost, base+"/api/v1/users",
		map[string]any{"username": username, "password": password, "role": role})
	mustStatus(t, code, http.StatusOK, "create user "+username)
	return int64(decode[map[string]any](t, body)["id"].(float64))
}

func TestUserScopingAndGuards(t *testing.T) {
	srv, admin, _ := newTestServer(t)
	base := srv.URL
	bootstrap(t, admin, base)

	// me 带角色
	code, body := doJSON(t, admin, http.MethodGet, base+"/api/v1/auth/me", nil)
	mustStatus(t, code, http.StatusOK, "admin me")
	mustEqual(t, decode[map[string]any](t, body)["role"], "admin", "admin role in me")

	createUser(t, admin, base, "bob", "password123", "user")

	// 重名 → 409;短密码 → 400
	code, _ = doJSON(t, admin, http.MethodPost, base+"/api/v1/users",
		map[string]any{"username": "bob", "password": "password123", "role": "user"})
	mustStatus(t, code, http.StatusConflict, "dup user")
	code, _ = doJSON(t, admin, http.MethodPost, base+"/api/v1/users",
		map[string]any{"username": "carol", "password": "short", "role": "user"})
	mustStatus(t, code, http.StatusBadRequest, "short pw")

	// bob 登录
	bob := newClient(t)
	me := loginAsUser(t, bob, base, "bob", "password123")
	mustEqual(t, me["role"], "user", "bob role")

	// bob 不能访问管理员端点
	code, _ = doJSON(t, bob, http.MethodGet, base+"/api/v1/users", nil)
	mustStatus(t, code, http.StatusForbidden, "user cannot list users")
	code, _ = doJSON(t, bob, http.MethodGet, base+"/api/v1/channels", nil)
	mustStatus(t, code, http.StatusForbidden, "user cannot list channels")
	code, _ = doJSON(t, bob, http.MethodGet, base+"/api/v1/settings", nil)
	mustStatus(t, code, http.StatusForbidden, "user cannot read settings")

	// bob 建令牌 → 列表只见自己
	code, _ = doJSON(t, bob, http.MethodPost, base+"/api/v1/tokens",
		map[string]any{"name": "bob-key", "allowedModels": []string{"*"}})
	mustStatus(t, code, http.StatusOK, "bob create token")
	code, body = doJSON(t, bob, http.MethodGet, base+"/api/v1/tokens", nil)
	mustStatus(t, code, http.StatusOK, "bob list tokens")
	bl := decode[[]map[string]any](t, body)
	mustEqual(t, len(bl), 1, "bob sees own only")
	mustEqual(t, bl[0]["ownerName"], "bob", "owner name")

	// admin 看全部(含归属名)
	code, body = doJSON(t, admin, http.MethodGet, base+"/api/v1/tokens", nil)
	mustStatus(t, code, http.StatusOK, "admin list tokens")
	al := decode[[]map[string]any](t, body)
	mustEqual(t, len(al), 1, "admin sees all (bob's)")

	// bob 不能删别人的(使用 admin 建的全局 key 作为他人资源)
	code, body = doJSON(t, admin, http.MethodPost, base+"/api/v1/tokens",
		map[string]any{"name": "global-key", "allowedModels": []string{"*"}})
	mustStatus(t, code, http.StatusOK, "admin create global token")
	gid := int64(decode[map[string]any](t, body)["token"].(map[string]any)["id"].(float64))
	code, _ = doJSON(t, bob, http.MethodDelete, fmt.Sprintf("%s/api/v1/tokens/%d", base, gid), nil)
	mustStatus(t, code, http.StatusNotFound, "bob cannot delete others' key (404)")
	code, _ = doJSON(t, bob, http.MethodGet, fmt.Sprintf("%s/api/v1/tokens/%d/claude-config", base, gid), nil)
	mustStatus(t, code, http.StatusNotFound, "bob cannot read others' config (404)")

	// 守卫:不能删自己 / 不能删最后一个管理员
	bid := int64(-1)
	code, body = doJSON(t, admin, http.MethodGet, base+"/api/v1/users", nil)
	for _, u := range decode[[]map[string]any](t, body) {
		if u["username"] == "bob" {
			bid = int64(u["id"].(float64))
		}
	}
	code, _ = doJSON(t, admin, http.MethodDelete, fmt.Sprintf("%s/api/v1/users/%d", base, bid), nil)
	mustStatus(t, code, http.StatusOK, "delete bob")
	// 删最后一个管理员 → 409(先查 admin id)
	code, body = doJSON(t, admin, http.MethodGet, base+"/api/v1/auth/me", nil)
	aID := int64(decode[map[string]any](t, body)["id"].(float64))
	code, _ = doJSON(t, admin, http.MethodDelete, fmt.Sprintf("%s/api/v1/users/%d", base, aID), nil)
	mustStatus(t, code, http.StatusBadRequest, "cannot delete yourself")
}

func TestChangeOwnPasswordReissuesSession(t *testing.T) {
	srv, admin, _ := newTestServer(t)
	base := srv.URL
	bootstrap(t, admin, base)
	createUser(t, admin, base, "bob", "password123", "user")

	bob := newClient(t)
	loginAsUser(t, bob, base, "bob", "password123")

	// 旧密码错 → 401
	code, _ := doJSON(t, bob, http.MethodPost, base+"/api/v1/auth/password",
		map[string]any{"oldPassword": "wrong", "newPassword": "newpassword1"})
	mustStatus(t, code, http.StatusUnauthorized, "wrong old password")

	// 正确 → 200,且本机仍可用(重签 cookie)
	code, _ = doJSON(t, bob, http.MethodPost, base+"/api/v1/auth/password",
		map[string]any{"oldPassword": "password123", "newPassword": "newpassword1"})
	mustStatus(t, code, http.StatusOK, "change password")
	code, _ = doJSON(t, bob, http.MethodGet, base+"/api/v1/auth/me", nil)
	mustStatus(t, code, http.StatusOK, "bob still logged in after change")

	// 新密码可登录
	bob2 := newClient(t)
	loginAsUser(t, bob2, base, "bob", "newpassword1")
}

func TestClaudeConfigGeneration(t *testing.T) {
	srv, admin, _ := newTestServer(t)
	base := srv.URL
	bootstrap(t, admin, base)

	// 渠道 + 同步三个 Claude 系模型 + 启用其供给源
	up := fakeUpstream(t, []string{"claude-opus-9", "claude-sonnet-9", "claude-haiku-9"})
	chID := createChannel(t, admin, base, "up", up.URL)
	code, _ := doJSON(t, admin, http.MethodPost, fmt.Sprintf("%s/api/v1/channels/%d/sync-models", base, chID), nil)
	mustStatus(t, code, http.StatusOK, "sync models")

	// 启用全部模型与供给源(同步默认停用;打开模型开关会联动开供给源)
	code, body := doJSON(t, admin, http.MethodGet, base+"/api/v1/models", nil)
	mustStatus(t, code, http.StatusOK, "list models")
	for _, m := range decode[[]map[string]any](t, body) {
		mid := int64(m["id"].(float64))
		code, _ = doJSON(t, admin, http.MethodPatch, fmt.Sprintf("%s/api/v1/models/%d", base, mid),
			map[string]any{"name": m["name"], "contextWindow": m["contextWindow"],
				"capabilities": m["capabilities"], "enabled": true})
		mustStatus(t, code, http.StatusOK, "enable model")
		for _, o := range m["offers"].([]any) {
			oid := int64(o.(map[string]any)["id"].(float64))
			code, _ = doJSON(t, admin, http.MethodPatch, fmt.Sprintf("%s/api/v1/offers/%d", base, oid),
				map[string]any{"enabled": true, "inputPriceUsd": 1, "outputPriceUsd": 2, "rateLimitRpm": 60})
			mustStatus(t, code, http.StatusOK, "enable offer")
		}
	}

	// 建令牌(admin 全局)
	code, body = doJSON(t, admin, http.MethodPost, base+"/api/v1/tokens",
		map[string]any{"name": "cfg", "allowedModels": []string{"*"}})
	mustStatus(t, code, http.StatusOK, "create token")
	tokID := int64(decode[map[string]any](t, body)["token"].(map[string]any)["id"].(float64))

	// 生成配置
	code, body = doJSON(t, admin, http.MethodGet, fmt.Sprintf("%s/api/v1/tokens/%d/claude-config", base, tokID), nil)
	mustStatus(t, code, http.StatusOK, "claude config")
	cfg := decode[map[string]any](t, body)
	mustEqual(t, cfg["baseUrl"], base, "base url")
	aliases := cfg["modelAliases"].(map[string]any)
	mustEqual(t, aliases["opus"], "claude-opus-9", "opus alias")
	mustEqual(t, aliases["sonnet"], "claude-sonnet-9", "sonnet alias")
	mustEqual(t, aliases["haiku"], "claude-haiku-9", "haiku alias")
	sj := cfg["settingsJson"].(string)
	if !strings.Contains(sj, "ANTHROPIC_BASE_URL") || !strings.Contains(sj, "sk-gw-") {
		t.Errorf("settingsJson missing expected content: %s", sj)
	}
}
