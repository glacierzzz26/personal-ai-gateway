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

// TestClientClosedExcludedFromErrorRate 499(客户端主动断开)不参与任何错误统计口径:
// 错误率只反映网关/上游故障,「用户按 Esc」不该被算成渠道不健康。
func TestClientClosedExcludedFromErrorRate(t *testing.T) {
	st := newTestStore(t)
	chID := mkChannel(t, st, "Anthropic 主")

	logs := []domain.LogRow{
		{TS: ts(2026, 9, 2, 10, 0), Model: "claude-3-5-sonnet", ChannelID: chID,
			ChannelName: "Anthropic 主", TokenName: "ci", Protocol: "anthropic",
			Status: 200, PromptTokens: 100, Completion: 50, CostUsd: 0.01,
			FirstTokenMs: 300, TotalMs: 900, IP: "1.2.3.4"},
		{TS: ts(2026, 9, 2, 11, 0), Model: "claude-3-5-sonnet", ChannelID: chID,
			ChannelName: "Anthropic 主", TokenName: "ci", Protocol: "anthropic",
			Status: 500, PromptTokens: 5, Completion: 0, CostUsd: 0,
			FirstTokenMs: 0, TotalMs: 120, IP: "1.2.3.4", Err: errPtr("boom")},
		{TS: ts(2026, 9, 2, 12, 0), Model: "claude-3-5-sonnet", ChannelID: chID,
			ChannelName: "Anthropic 主", TokenName: "ci", Protocol: "anthropic",
			Status: domain.StatusClientClosed, PromptTokens: 40, Completion: 0, CostUsd: 0.001,
			FirstTokenMs: 200, TotalMs: 3000, IP: "1.2.3.4"},
		// 断连时若已写下 err 文本,同样不得计入(状态口径优先)。
		{TS: ts(2026, 9, 2, 13, 0), Model: "claude-3-5-sonnet", ChannelID: chID,
			ChannelName: "Anthropic 主", TokenName: "ci", Protocol: "anthropic",
			Status: domain.StatusClientClosed, PromptTokens: 30, Completion: 0, CostUsd: 0.001,
			FirstTokenMs: 210, TotalMs: 2500, IP: "1.2.3.4", Err: errPtr("use of closed connection")},
	}
	for _, l := range logs {
		mustNoErr(t, st.InsertLog(l), "insert log")
	}

	from, to := ts(2026, 9, 2, 0, 0), ts(2026, 9, 3, 0, 0)

	// 合计口径:4 请求,只有那条 500 是错误
	reqs, errs, _, err := st.WindowTotals(from, to)
	mustNoErr(t, err, "window totals")
	mustEqual(t, reqs, 4, "window requests")
	mustEqual(t, errs, 1, "window errors(499 不得计入)")

	// 时间桶
	series, err := st.QuerySeries("day", from, to, 480)
	mustNoErr(t, err, "day series")
	if len(series) != 1 || series[0].Requests != 4 || series[0].Errors != 1 {
		t.Errorf("day series = %+v, want req=4 err=1", series)
	}

	// 维度聚合的错误率:1/4,而非 3/4
	dims, err := st.QueryDimSummary("channel", from, to, 0)
	mustNoErr(t, err, "channel dim")
	if len(dims) != 1 || dims[0].Requests != 4 {
		t.Fatalf("channel dim = %+v", dims)
	}
	if dims[0].ErrorRate < 0.24 || dims[0].ErrorRate > 0.26 {
		t.Errorf("channel errorRate = %v, want 0.25(499 被计入)", dims[0].ErrorRate)
	}

	// 渠道健康统计同样只认 1 条错误
	stats, err := st.ChannelStatsSince(from)
	mustNoErr(t, err, "channel stats")
	if s := stats[chID]; s.Errors != 1 || s.Requests != 4 {
		t.Errorf("channel stats = %+v, want req=4 err=1", s)
	}

	// 列表筛选:ok / error / canceled 三桶互斥且覆盖全部
	_, nOK, _ := st.ListLogs(LogFilter{Status: "ok"}, 480)
	_, nErr, _ := st.ListLogs(LogFilter{Status: "error"}, 480)
	_, nCanceled, _ := st.ListLogs(LogFilter{Status: "canceled"}, 480)
	mustEqual(t, nOK, 1, "ok bucket")
	mustEqual(t, nErr, 1, "error bucket")
	mustEqual(t, nCanceled, 2, "canceled bucket")
	mustEqual(t, nOK+nErr+nCanceled, 4, "三桶应覆盖全部 4 条")
}
