package server

import (
	"testing"
	"time"
)

// TestBucketCount 回归:bucketCount 不得从「窗口时长」反推自然桶数。
//
// 预设窗口的 to 是「此刻」而非当天结束,时长里含当天已过的一小截。
// 旧实现用 round(时长) 反推,会把这截吞掉:days=7 在本地 11:57 时算出 6 个桶
// (当天被整个丢掉);过了 12:00 小数部分 ≥0.5 又会对上 —— 于是这个 bug 按时辰
// 飘,只在上午红。这里用固定时刻断言,不看当前时间,避免测试再次「自愈」。
func TestBucketCount(t *testing.T) {
	tz := 480 // Asia/Shanghai
	// 本地 2026-09-16 11:57 = UTC 03:57
	utc1149 := time.Date(2026, 9, 16, 3, 57, 0, 0, time.UTC)

	cases := []struct {
		name   string
		rng    statRange
		bucket string
		want   int
	}{
		{
			name:   "days=7 本地上午(旧实现在此少一个日桶)",
			rng:    statRange{from: localDayStart(utc1149, tz).AddDate(0, 0, -6), to: utc1149, days: 7},
			bucket: "day",
			want:   7,
		},
		{
			name:   "days=30 本地上午",
			rng:    statRange{from: localDayStart(utc1149, tz).AddDate(0, 0, -29), to: utc1149, days: 30},
			bucket: "day",
			want:   30,
		},
		{
			name:   "days=1 本地下午 12:24(旧实现算出 12 而非 13)",
			rng:    statRange{from: localDayStart(utc1224, tz), to: utc1224, days: 1},
			bucket: "hour",
			want:   13,
		},
		{
			name:   "days=1 刚过零点(旧实现算出 0,被钳到 1)",
			rng:    statRange{from: localDayStart(utc0001, tz), to: utc0001, days: 1},
			bucket: "hour",
			want:   1,
		},
		{
			name:   "days=3 本地上午,仍是小时桶",
			rng:    statRange{from: localDayStart(utc1149, tz).AddDate(0, 0, -2), to: utc1149, days: 3},
			bucket: "hour",
			want:   60, // 覆盖 [两天前 00:00, 今天 11:00] = 2*24 + 12 个小时桶
		},
	}
	for _, c := range cases {
		if got := bucketCount(c.rng, c.bucket); got != c.want {
			t.Errorf("%s: bucketCount = %d, want %d", c.name, got, c.want)
		}
	}

	// 日桶的桶数必须等于 days,与「时长」无关。
	for days := 1; days <= 90; days += 7 {
		rng := statRange{from: localDayStart(utc1149, tz).AddDate(0, 0, -(days - 1)), to: utc1149, days: days}
		if got := bucketCount(rng, "day"); got != days {
			t.Errorf("days=%d: bucketCount(day) = %d, want %d", days, got, days)
		}
	}
}

// 本地 2026-09-16 12:24 = UTC 04:24;本地 00:01 = UTC 前一天 16:01。
var (
	utc1224 = time.Date(2026, 9, 16, 4, 24, 0, 0, time.UTC)
	utc0001 = time.Date(2026, 9, 15, 16, 1, 0, 0, time.UTC)
)
