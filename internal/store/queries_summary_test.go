package store

import (
	"testing"
	"time"
)

// 时间过滤 + 聚合的回归测试。
// 曾踩的坑:modernc 下 strftime('%s', ts) 的 TEXT 结果与整数参数直接比较,<= 恒假,
// 导致带 from/to 的查询永远返回空;空集聚合又产生 SUM=NULL,Scan 进 int 报错。
func TestUsageSummaryTimeFiltered(t *testing.T) {
	st, err := Open(t.TempDir() + "/db.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	now := time.Now().UTC()
	for _, e := range []LogEntry{
		{TS: now, Model: "m1", Upstream: "u1", Protocol: "openai", Status: 200, PromptTokens: 100, CompletionTokens: 50, CacheReadTokens: 10, Cost: 0.02},
		{TS: now, Model: "m1", Upstream: "u1", Protocol: "openai", Status: 500, PromptTokens: 3, Err: "boom"},
		{TS: now.Add(-25 * time.Hour), Model: "m2", Upstream: "u2", Protocol: "anthropic", Status: 200, PromptTokens: 7, Cost: 0.01},
	} {
		if err := st.Log(e); err != nil {
			t.Fatal(err)
		}
	}

	from, _ := time.Parse(time.RFC3339, "2020-01-01T00:00:00Z")
	to, _ := time.Parse(time.RFC3339, "2030-01-01T00:00:00Z")
	f := ReqFilter{From: &from, To: &to}

	// 1) 带时间过滤的分组聚合必须返回全部 3 条(时间过滤不误伤)。
	rows, err := st.UsageSummary(f, []string{"model"}, "")
	if err != nil {
		t.Fatalf("summary err: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 model rows, got %d: %+v", len(rows), rows)
	}
	for _, r := range rows {
		if r.Groups["model"] == "m1" {
			if r.Requests != 2 || r.Errors != 1 {
				t.Errorf("m1 row wrong: %+v", r)
			}
		} else if r.Groups["model"] == "m2" {
			if r.Requests != 1 || r.Errors != 0 {
				t.Errorf("m2 row wrong: %+v", r)
			}
		}
	}

	// 2) 时间过滤同样作用于明细列表
	list, err := st.ListRequests(f, 10, 0, "ts", "desc")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("list want 3, got %d", len(list))
	}

	// 3) 窄窗口(只罩住 -25h 那条)应只命中 m2
	from2 := now.Add(-26 * time.Hour)
	to2 := now.Add(-24 * time.Hour)
	rows2, err := st.UsageSummary(ReqFilter{From: &from2, To: &to2}, []string{"model"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows2) != 1 || rows2[0].Requests != 1 || rows2[0].Groups["model"] != "m2" {
		t.Fatalf("narrow window wrong: %+v", rows2)
	}
}

// 空窗口(无任何请求)的整体聚合应返回全 0 的一行,而不是 SUM=NULL 报 Scan 错。
func TestUsageSummaryEmptyWindow(t *testing.T) {
	st, err := Open(t.TempDir() + "/db.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Log(LogEntry{TS: time.Now().UTC(), Model: "m1", Status: 200}); err != nil {
		t.Fatal(err)
	}

	from := time.Now().Add(-48 * time.Hour)
	to := time.Now().Add(-47 * time.Hour) // 无请求落在该窗口
	rows, err := st.UsageSummary(ReqFilter{From: &from, To: &to}, nil, "")
	if err != nil {
		t.Fatalf("summary err: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 aggregate row, got %d", len(rows))
	}
	r := rows[0]
	if r.Requests != 0 || r.Errors != 0 || r.PromptTokens != 0 || r.Cost != 0 {
		t.Errorf("empty window should be all zeros: %+v", r)
	}
}
