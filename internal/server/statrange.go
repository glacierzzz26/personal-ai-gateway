package server

import (
	"errors"
	"net/http"
	"time"

	"personal-ai-gateway/internal/store"
)

// 请求日志最多保留 90 天(见 store.RetentionDays / 清理任务),故统计窗口一律钳制在 90 天。
const maxRangeDays = 90

// statRange 统计窗口(UTC 半开区间 [from, to))。
type statRange struct {
	from   time.Time
	to     time.Time
	days   int // 自然日跨度,用于日桶补齐
	custom bool
}

/*
parseStatRange 解析统计窗口,两种写法:

  - 预设:?days=1|7|30(默认 7),窗口 = 本地自然日对齐的最近 N 天;
  - 自定义:?from=YYYY-MM-DD&to=YYYY-MM-DD(含首尾),to 取当日结束。

两者都给时自定义优先。from>to、跨度>90 天一律 400 —— 数据只留 90 天,
再往前查只会拿到空桶,不如直接告诉调用方超界。
*/
func parseStatRange(r *http.Request, tzOffMin int) (statRange, error) {
	return parseStatRangeAt(r, tzOffMin, time.Now().UTC())
}

// parseStatRangeAt 与 parseStatRange 同语义,但把「此刻」显式传入 —— 供测试锁定
// 时刻,避免回归用例随运行时辰漂移(见 statrange_custom_test.go)。
func parseStatRangeAt(r *http.Request, tzOffMin int, now time.Time) (statRange, error) {
	fromS, toS := queryStr(r, "from"), queryStr(r, "to")
	if fromS != "" || toS != "" {
		from, err := queryTime(r, "from")
		if err != nil {
			return statRange{}, err
		}
		to, err := queryTime(r, "to")
		if err != nil {
			return statRange{}, err
		}
		if from == nil || to == nil {
			return statRange{}, errors.New("from 与 to 需同时提供")
		}
		f := localDayStart(*from, tzOffMin)
		toStart := localDayStart(*to, tzOffMin)
		// days 是「含首尾的自然日数」,用两个本地零点之差直接算 —— 不能拿下面被钳过的
		// end 反推:to=今天 时 end 被钳到此刻,时长里只剩当天已过的一小截,四舍五入
		// 会在上午吞掉一天(与 bucketCount 同一类错)。见 reads.go:bucketCount 注释。
		days := int(toStart.Sub(f).Hours()/24) + 1
		// to 取「该自然日的次日零点」,让 to=今天 时能把今天算进去;超出的未来部分钳到此刻。
		end := toStart.Add(24 * time.Hour)
		if end.After(now) {
			end = now
		}
		if !f.Before(end) || days < 1 {
			return statRange{}, errors.New("起始日期不能晚于结束日期")
		}
		if days > maxRangeDays {
			return statRange{}, errors.New("统计区间最长 90 天(日志保留上限)")
		}
		return statRange{from: f, to: end, days: days, custom: true}, nil
	}

	days := queryInt(r, "days", 7)
	if days < 1 {
		days = 1
	}
	if days > maxRangeDays {
		days = maxRangeDays
	}
	// 与曲线桶口径一致:窗口 = 最近 N 个自然日,含今天
	todayStart := localDayStart(now, tzOffMin)
	from := todayStart.AddDate(0, 0, -(days - 1))
	return statRange{from: from, to: now, days: days}, nil
}

// localDayStart 把 UTC 时刻换算到本地自然日后,再折回 UTC(与 store.LocalDayWindowUTC 同口径)。
func localDayStart(at time.Time, tzOffMin int) time.Time {
	f, _ := store.LocalDayWindowUTC(tzOffMin, at)
	return f
}
