package store

import (
	"testing"
	"time"

	"personal-ai-gateway/internal/domain"
)

// TestAvgFirstTokenMsStreamOnly 回归:首字延迟均值只统计流式请求。
//
// 非流式请求一次返回完整响应,「首字」等于整程,把它平均进来会系统性抬高该指标,
// 且同一列在不同流式占比的窗口间不可比(实测生产数据:非流式均值约为流式的 2 倍)。
func TestAvgFirstTokenMsStreamOnly(t *testing.T) {
	st := newTestStore(t)
	chID := mkChannel(t, st, "Anthropic 主")

	base := ts(2026, 9, 2, 10, 0)
	rows := []domain.LogRow{
		// 流式成功:100 / 300 → 计入,均值 200
		{TS: base, Model: "m", ChannelID: chID, ChannelName: "ch", TokenName: "tk",
			Protocol: "anthropic", Stream: true, Status: 200, FirstTokenMs: 100, TotalMs: 900},
		{TS: base.Add(time.Minute), Model: "m", ChannelID: chID, ChannelName: "ch", TokenName: "tk",
			Protocol: "anthropic", Stream: true, Status: 200, FirstTokenMs: 300, TotalMs: 1200},
		// 非流式成功,且 first_token_ms 很大 → 不应计入
		{TS: base.Add(2 * time.Minute), Model: "m", ChannelID: chID, ChannelName: "ch", TokenName: "tk",
			Protocol: "openai", Stream: false, Status: 200, FirstTokenMs: 9000, TotalMs: 9000},
		// 流式但失败(4xx)→ 不计入
		{TS: base.Add(3 * time.Minute), Model: "m", ChannelID: chID, ChannelName: "ch", TokenName: "tk",
			Protocol: "anthropic", Stream: true, Status: 429, FirstTokenMs: 5000, TotalMs: 5000},
		// 流式成功但没有首字样本(0)→ 不计入
		{TS: base.Add(4 * time.Minute), Model: "m", ChannelID: chID, ChannelName: "ch", TokenName: "tk",
			Protocol: "anthropic", Stream: true, Status: 200, FirstTokenMs: 0, TotalMs: 700},
	}
	for _, row := range rows {
		if err := st.InsertLog(row); err != nil {
			t.Fatalf("seed log: %v", err)
		}
	}

	got, err := st.AvgFirstTokenMsSince(base.Add(-time.Hour))
	if err != nil {
		t.Fatalf("AvgFirstTokenMsSince: %v", err)
	}
	if got != 200 {
		t.Fatalf("avg first token = %v, want 200 (仅流式成功的 100/300 平均)", got)
	}

	// 无流式样本时返回 0,而不是把非流式的值顶上来。
	late, err := st.AvgFirstTokenMsSince(base.Add(90 * time.Minute))
	if err != nil {
		t.Fatalf("AvgFirstTokenMsSince(empty window): %v", err)
	}
	if late != 0 {
		t.Fatalf("avg first token (无样本) = %v, want 0", late)
	}
}
