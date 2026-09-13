package server

import (
	"fmt"
	"net/http"
	"testing"
)

// TestSyncAutoMergesCanonicalModels 两个渠道上报「带前缀 / 裸名」的同一模型 → 归并为一行两供给源。
func TestSyncAutoMergesCanonicalModels(t *testing.T) {
	srv, c, _ := newTestServer(t)
	base := srv.URL
	bootstrap(t, c, base)

	// 上游 A 报带前缀名,B 报裸名。
	upA := fakeUpstream(t, []string{"deepseek/deepseek-v4.1-flash"})
	upB := fakeUpstream(t, []string{"deepseek-v4.1-flash"})

	mkChan := func(name string, up string) int64 {
		code, body := doJSON(t, c, http.MethodPost, base+"/api/v1/channels",
			map[string]any{"name": name, "provider": "DeepSeek", "baseUrl": up, "apiKey": "sk-x1234567890abcd"})
		mustStatus(t, code, http.StatusOK, "create channel "+name)
		return int64(decode[map[string]any](t, body)["id"].(float64))
	}
	chA := mkChan("A", upA.URL)
	chB := mkChan("B", upB.URL)

	code, body := doJSON(t, c, http.MethodPost, fmt.Sprintf("%s/api/v1/channels/%d/sync-models", base, chA), nil)
	mustStatus(t, code, http.StatusOK, "sync A")
	if added := decode[map[string]any](t, body)["added"].(float64); added != 1 {
		t.Fatalf("A sync added = %v", added)
	}
	code, body = doJSON(t, c, http.MethodPost, fmt.Sprintf("%s/api/v1/channels/%d/sync-models", base, chB), nil)
	mustStatus(t, code, http.StatusOK, "sync B")
	// B 归并到 A 已建的那一行 → added=0, updated=1
	if added := decode[map[string]any](t, body)["added"].(float64); added != 0 {
		t.Fatalf("B sync should merge (added=0), got %v (%s)", added, body)
	}

	code, body = doJSON(t, c, http.MethodGet, base+"/api/v1/models", nil)
	mustStatus(t, code, http.StatusOK, "list models")
	models := decode[[]map[string]any](t, body)
	if len(models) != 1 {
		t.Fatalf("expected 1 merged model row, got %d: %s", len(models), body)
	}
	offers := models[0]["offers"].([]any)
	if len(offers) != 2 {
		t.Fatalf("expected 2 offers (one per channel), got %d", len(offers))
	}
	// 每条 offer 带各自渠道路由用的上游名
	got := map[string]string{}
	for _, o := range offers {
		om := o.(map[string]any)
		got[om["channelName"].(string)] = fmt.Sprint(om["upstreamModel"])
	}
	if got["A"] != "deepseek/deepseek-v4.1-flash" || got["B"] != "deepseek-v4.1-flash" {
		t.Fatalf("per-offer upstream names wrong: %+v", got)
	}
}

// TestMergeModelsEndpoint 手工合并重复模型:成功 + 自身/冲突/缺失的错误码。
func TestMergeModelsEndpoint(t *testing.T) {
	srv, c, _ := newTestServer(t)
	base := srv.URL
	bootstrap(t, c, base)

	mkModel := func(name string) int64 {
		code, body := doJSON(t, c, http.MethodPost, base+"/api/v1/models",
			map[string]any{"name": name, "contextWindow": 32000})
		mustStatus(t, code, http.StatusOK, "create model "+name)
		return int64(decode[map[string]any](t, body)["id"].(float64))
	}
	src := mkModel("dup-a")
	dst := mkModel("dup-b")

	// 合并到自身 → 400
	code, _ := doJSON(t, c, http.MethodPost, fmt.Sprintf("%s/api/v1/models/%d/merge", base, src),
		map[string]any{"intoId": src})
	mustStatus(t, code, http.StatusBadRequest, "self merge")

	// 合并成功 → 返回目标模型,源已删
	code, body := doJSON(t, c, http.MethodPost, fmt.Sprintf("%s/api/v1/models/%d/merge", base, src),
		map[string]any{"intoId": dst})
	mustStatus(t, code, http.StatusOK, "merge")
	if int64(decode[map[string]any](t, body)["id"].(float64)) != dst {
		t.Fatalf("merge should return target model: %s", body)
	}
	code, body = doJSON(t, c, http.MethodGet, base+"/api/v1/models", nil)
	mustStatus(t, code, http.StatusOK, "list after merge")
	if models := decode[[]map[string]any](t, body); len(models) != 1 {
		t.Fatalf("expected 1 model after merge, got %d", len(models))
	}

	// 缺失目标 → 404
	m1 := mkModel("x1")
	code, _ = doJSON(t, c, http.MethodPost, fmt.Sprintf("%s/api/v1/models/%d/merge", base, m1),
		map[string]any{"intoId": 99999})
	mustStatus(t, code, http.StatusNotFound, "merge missing target")
}

