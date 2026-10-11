package server

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"personal-ai-gateway/internal/auth"
)

// TestSessionSlidingRefresh 会话滑动续期:每次带有效 cookie 的受保护请求,
// 响应都重签并下发新 gw_session,其 Max-Age = 空闲窗(与 JWT exp 一致)。
func TestSessionSlidingRefresh(t *testing.T) {
	srv, c, _ := newTestServer(t)
	base := srv.URL
	bootstrap(t, c, base)

	if sessionCookieValue(t, c, base) == "" {
		t.Fatal("no gw_session after bootstrap")
	}

	req, err := http.NewRequest(http.MethodGet, base+"/api/v1/auth/me", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("me: %v", err)
	}
	resp.Body.Close()
	mustStatus(t, resp.StatusCode, http.StatusOK, "me")

	sc := resp.Header.Get("Set-Cookie")
	if !strings.Contains(sc, auth.SessionCookie+"=") {
		t.Fatalf("no refreshed %s in Set-Cookie: %q", auth.SessionCookie, sc)
	}
	if want := "Max-Age=3600"; !strings.Contains(sc, want) {
		t.Errorf("cookie Max-Age should equal idle window: want %q in %q", want, sc)
	}
}

// sessionCookieValue 从 jar 取当前 gw_session 值。
func sessionCookieValue(t *testing.T, c *http.Client, base string) string {
	t.Helper()
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse base: %v", err)
	}
	for _, ck := range c.Jar.Cookies(u) {
		if ck.Name == auth.SessionCookie {
			return ck.Value
		}
	}
	return ""
}
