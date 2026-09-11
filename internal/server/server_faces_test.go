package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestHandlerFacesIsolation TLS 双口把管理面/数据面物理隔离:
// 数据面口不暴露管理台(静态与 /api/v1 均 404),管理台口不暴露 /v1(404 而非 SPA 假 200)。
func TestHandlerFacesIsolation(t *testing.T) {
	_, _, s := newTestServerWithEng(t)

	api := httptest.NewServer(s.HandlerAPI())
	defer api.Close()
	admin := httptest.NewServer(s.HandlerAdmin())
	defer admin.Close()

	cases := []struct {
		name string
		url  string
	}{
		{"api face: /api/v1 不暴露", api.URL + "/api/v1/auth/state"},
		{"api face: 静态根不暴露", api.URL + "/"},
		{"admin face: /v1 不暴露", admin.URL + "/v1/models"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := http.Get(tc.url)
			if err != nil {
				t.Fatalf("get %s: %v", tc.url, err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusNotFound {
				t.Fatalf("GET %s = %d, want 404", tc.url, resp.StatusCode)
			}
			if ct := resp.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(ct, "application/json") {
				t.Fatalf("GET %s content-type = %q, want json 404", tc.url, ct)
			}
		})
	}

	// 两面的 /healthz 都可用,且回显注入的版本号。
	for _, base := range []string{api.URL, admin.URL} {
		resp, err := http.Get(base + "/healthz")
		if err != nil {
			t.Fatalf("healthz %s: %v", base, err)
		}
		var got map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&got)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || got["ok"] != true {
			t.Fatalf("healthz %s = %d %v, want ok", base, resp.StatusCode, got)
		}
		if _, ok := got["version"]; !ok {
			t.Fatalf("healthz %s 缺 version 字段: %v", base, got)
		}
	}
}
