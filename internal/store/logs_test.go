package store

import (
	"testing"
	"time"

	"personal-ai-gateway/internal/domain"
)

func ts(y int, mo time.Month, d, h, mi int) time.Time {
	return time.Date(y, mo, d, h, mi, 0, 0, time.UTC)
}

func errPtr(s string) *string { return &s }

func TestLocalDayWindowUTC(t *testing.T) {
	// tz +480 (Asia/Shanghai):at 09-02T20:00Z = 本地 09-03 04:00 → 本地自然日 09-03
	start, end := LocalDayWindowUTC(480, ts(2026, 9, 2, 20, 0))
	wantStart := ts(2026, 9, 2, 16, 0)
	wantEnd := ts(2026, 9, 3, 16, 0)
	if !start.Equal(wantStart) || !end.Equal(wantEnd) {
		t.Errorf("window = [%s, %s), want [%s, %s)", start, end, wantStart, wantEnd)
	}
}

func TestLogInsertListBuckets(t *testing.T) {
	st := newTestStore(t)
	chID := mkChannel(t, st, "Anthropic 主")

	// ts 均 UTC;本地 +8 后跨日,用于验证桶按 tz 切
	logs := []domain.LogRow{
		{TS: ts(2026, 9, 2, 16, 30), Model: "claude-3-5-sonnet", ChannelID: chID,
			ChannelName: "Anthropic 主", TokenName: "ci", Protocol: "anthropic",
			Status: 200, PromptTokens: 100, Completion: 50, CostUsd: 0.01,
			FirstTokenMs: 300, TotalMs: 900, IP: "1.2.3.4"},
		{TS: ts(2026, 9, 2, 17, 30), Model: "claude-3-5-sonnet", ChannelID: chID,
			ChannelName: "Anthropic 主", TokenName: "ci", Protocol: "anthropic",
			Status: 429, PromptTokens: 10, Completion: 0, CostUsd: 0,
			FirstTokenMs: 0, TotalMs: 50, IP: "1.2.3.4", Err: errPtr("upstream rate limited")},
		{TS: ts(2026, 9, 1, 23, 0), Model: "gpt-5", ChannelID: chID,
			ChannelName: "Anthropic 主", TokenName: "ci", Protocol: "openai",
			Status: 500, PromptTokens: 5, Completion: 5, CostUsd: 0.002,
			FirstTokenMs: 0, TotalMs: 120, IP: "9.9.9.9", Err: errPtr("boom")},
	}
	for _, l := range logs {
		mustNoErr(t, st.InsertLog(l), "insert log")
	}

	// 列表:默认新→旧,3 条
	items, total, err := st.ListLogs(LogFilter{}, 480)
	mustNoErr(t, err, "list logs")
	mustEqual(t, total, 3, "total")
	if items[0].Model != "gpt-5" || items[2].Model != "claude-3-5-sonnet" {
		t.Errorf("order should be newest first: %+v", items)
	}
	// 本地时间显示:16:30Z → 00:30 次日
	if items[2].TS != "2026-09-03 00:30" {
		t.Errorf("ts local = %q, want 2026-09-03 00:30", items[2].TS)
	}
	if items[0].Error == nil {
		t.Error("err row should carry error message")
	}

	// 状态桶
	_, totalErr, _ := st.ListLogs(LogFilter{Status: "error"}, 480)
	mustEqual(t, totalErr, 2, "error bucket")
	_, totalOK, _ := st.ListLogs(LogFilter{Status: "ok"}, 480)
	mustEqual(t, totalOK, 1, "ok bucket")

	// 渠道精确筛选 + 分页
	_, totalCh, _ := st.ListLogs(LogFilter{Channel: "Anthropic 主", Limit: 2, Offset: 0}, 480)
	mustEqual(t, totalCh, 3, "channel filter")
	page2, _, _ := st.ListLogs(LogFilter{Limit: 2, Offset: 2}, 480)
	mustEqual(t, len(page2), 1, "second page size")

	// kw 命中 err 文本
	_, totalKw, _ := st.ListLogs(LogFilter{Keyword: "boom"}, 480)
	mustEqual(t, totalKw, 1, "keyword hits err body")

	// ---- 桶聚合:本地 +8 → 16:30Z 归 09-03,23:00Z 归 09-02 ----
	from := ts(2026, 9, 1, 0, 0)
	to := ts(2026, 9, 4, 0, 0)
	days, err := st.QuerySeries("day", from, to, 480)
	mustNoErr(t, err, "day series")
	byDay := map[string]domain.MetricPoint{}
	for _, p := range days {
		byDay[p.TS] = p
	}
	if d := byDay["2026-09-03"]; d.Requests != 2 || d.Errors != 1 {
		t.Errorf("2026-09-03 bucket = %+v, want req=2 err=1", d)
	}
	if d := byDay["2026-09-02"]; d.Requests != 1 || d.Errors != 1 {
		t.Errorf("2026-09-02 bucket = %+v, want req=1 err=1", d)
	}

	hours, err := st.QuerySeries("hour", from, to, 480)
	mustNoErr(t, err, "hour series")
	byH := map[string]domain.MetricPoint{}
	for _, p := range hours {
		byH[p.TS] = p
	}
	// L1(16:30Z→本地 00:30)与 L2(17:30Z→本地 01:30)分属两个本地小时
	if h := byH["2026-09-03 00"]; h.Requests != 1 || h.Errors != 0 {
		t.Errorf("hour bucket 2026-09-03 00 = %+v, want req=1 err=0", h)
	}
	if h := byH["2026-09-03 01"]; h.Requests != 1 || h.Errors != 1 {
		t.Errorf("hour bucket 2026-09-03 01 = %+v, want req=1 err=1", h)
	}

	// 维度聚合
	rows, err := st.QueryDimSummary("model", from, to, 0)
	mustNoErr(t, err, "model dim")
	if len(rows) != 2 {
		t.Fatalf("model dim rows = %+v", rows)
	}
	// channel dim:同一渠道 3 请求,err=2
	chRows, err := st.QueryDimSummary("channel", from, to, 0)
	mustNoErr(t, err, "channel dim")
	if len(chRows) != 1 || chRows[0].Requests != 3 || chRows[0].OutTokens != 55 {
		t.Errorf("channel dim = %+v", chRows)
	}
	// token dim
	tokRows, _ := st.QueryDimSummary("token", from, to, 0)
	if len(tokRows) != 1 || tokRows[0].Name != "ci" || tokRows[0].InTokens != 115 {
		t.Errorf("token dim = %+v", tokRows)
	}

	// 渠道健康统计:24h 窗口 前 3 条都计入
	since := ts(2026, 9, 1, 0, 0)
	stats, err := st.ChannelStatsSince(since)
	mustNoErr(t, err, "channel stats")
	s, ok := stats[chID]
	if !ok {
		t.Fatalf("channel %d missing from stats", chID)
	}
	if s.Requests != 3 || s.Errors != 2 || s.Tokens != 170 || s.CostUsd < 0.0119 || s.CostUsd > 0.0121 {
		t.Errorf("channel stats = %+v", s)
	}

	// 清空
	mustNoErr(t, st.ClearLogs(), "clear logs")
	_, totalAfter, _ := st.ListLogs(LogFilter{}, 480)
	mustEqual(t, totalAfter, 0, "cleared")
}
