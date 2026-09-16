package server

import (
	"net/http/httptest"
	"testing"
	"time"
)

// TestParseStatRangeCustomDays 回归:自定义区间的 days 必须是「含首尾的自然日数」,
// 不能拿被钳到此刻的 end 反推。
//
// to=今天 时 end 会被钳成 now(只含当天已过的一小截),旧实现 round(时长) 在上午会
// 把当天整个吞掉:查「7 天前 → 今天」算出 6 天,曲线少画一天。这个 bug 按时辰飘
// (下午小数部分 ≥0.5 就自愈),所以必须用 parseStatRangeAt 钉死「此刻」为上午,
// 否则测试会随运行时间变红变绿 —— 那不是回归测试。
func TestParseStatRangeCustomDays(t *testing.T) {
	const tz = 480 // Asia/Shanghai
	// 本地 2026-09-16 10:20 = UTC 02:20,落在「上午」这一旧实现对不上的区间。
	now := time.Date(2026, 9, 16, 2, 20, 0, 0, time.UTC)
	day := func(offset int) string {
		// 从「今天」起算的本地自然日字符串。
		return now.Add(time.Duration(tz)*time.Minute).AddDate(0, 0, offset).Format("2006-01-02")
	}

	cases := []struct {
		name string
		from string
		to   string
		want int
	}{
		// 终点是今天:end 被钳到此刻,是旧实现唯一会算错的形状。
		{"含今天 7 天", day(-6), day(0), 7},
		{"含今天 30 天", day(-29), day(0), 30},
		{"含今天 1 天(当天)", day(0), day(0), 1},
		// 终点在过去:end 不被钳,新旧实现都对,作为不回归的对照。
		{"过去 10 天", day(-30), day(-21), 10},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/api/v1/overview?from="+c.from+"&to="+c.to, nil)
			rng, err := parseStatRangeAt(r, tz, now)
			if err != nil {
				t.Fatalf("parseStatRange(%s→%s): %v", c.from, c.to, err)
			}
			if rng.days != c.want {
				t.Errorf("days = %d, want %d (from=%s to=%s)", rng.days, c.want, c.from, c.to)
			}
			// 桶数必须与 days 一致,否则曲线尾部缺一天。
			if got := bucketCount(rng, "day"); got != c.want {
				t.Errorf("bucketCount(day) = %d, want %d", got, c.want)
			}
		})
	}
}
