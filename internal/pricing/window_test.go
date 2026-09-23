package pricing

import (
	"testing"
	"time"

	"personal-ai-gateway/internal/domain"
)

// at 构造一个北京时间(UTC+8)的时刻,便于按人话描述峰谷。
func beijing(y int, mo time.Month, d, h, mi int) time.Time {
	return time.Date(y, mo, d, h, mi, 0, 0, time.FixedZone("CST", 8*3600))
}

// deepseekRow 生产里那 2 行 DeepSeek 官方价的形状(CNY,峰谷分时)。
// legacy 为 true 时不带 windows,只有中文 peakHours —— 复刻已落库的旧行。
func deepseekRow(legacy bool) domain.OfficialPriceRow {
	detail := map[string]any{
		"effectiveDefault": "offpeak",
		"peak":             map[string]any{"in": 2.0, "out": 8.0, "cacheRead": 0.04},
		"offpeak":          map[string]any{"in": 1.0, "out": 4.0, "cacheRead": 0.02},
		"peakHours":        legacyDeepSeekPeakHours,
	}
	if !legacy {
		detail["windows"] = windowsToAny(legacyDeepSeekWindows())
	}
	return domain.OfficialPriceRow{
		Provider: "deepseek", ModelName: "deepseek-flash",
		Currency: domain.CurrencyCNY, BillingShape: domain.ShapePeakOff,
		InputPrice: 1.0, OutputPrice: 4.0, CacheReadPrice: 0.02, // 标量 = 空闲价
		Detail: detail,
	}
}

// TestShapePricePeakOffpeakByTime 分时选价的核心:同一行官方价,按请求时刻选峰/谷。
func TestShapePricePeakOffpeakByTime(t *testing.T) {
	q := deepseekRow(false)
	cases := []struct {
		name string
		at   time.Time
		peak bool
		out  float64
	}{
		{"周一上午高峰", beijing(2026, time.September, 14, 10, 0), true, 8},
		{"周一下午高峰", beijing(2026, time.September, 14, 15, 0), true, 8},
		{"周一午休谷段", beijing(2026, time.September, 14, 13, 0), false, 4},
		{"周一凌晨谷段", beijing(2026, time.September, 14, 3, 0), false, 4},
		{"周六上午非高峰", beijing(2026, time.September, 19, 10, 0), false, 4},
		{"高峰起点含(半开左闭)", beijing(2026, time.September, 14, 9, 0), true, 8},
		{"高峰终点不含(半开右开)", beijing(2026, time.September, 14, 12, 0), false, 4},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, out, _, peak, err := ShapePrice(q, c.at, 480)
			if err != nil {
				t.Fatalf("ShapePrice: %v", err)
			}
			if peak != c.peak {
				t.Errorf("peak = %v, want %v", peak, c.peak)
			}
			mustClose(t, "out", out, c.out)
		})
	}
}

// TestShapePriceLegacyDetailWithoutWindows 已落库的旧行(只有中文 peakHours、无 windows)
// 必须立即可正确计费 —— 靠内置的字面量精确映射,不必等 admin 重抓。
func TestShapePriceLegacyDetailWithoutWindows(t *testing.T) {
	q := deepseekRow(true)
	if _, ok := q.Detail["windows"]; ok {
		t.Fatal("夹具不该带 windows")
	}
	if _, out, _, peak, err := ShapePrice(q, beijing(2026, time.September, 14, 10, 0), 480); err != nil || !peak || out != 8 {
		t.Errorf("旧行高峰选价: peak=%v out=%v err=%v, want peak=true out=8", peak, out, err)
	}
	if _, out, _, peak, err := ShapePrice(q, beijing(2026, time.September, 14, 3, 0), 480); err != nil || peak || out != 4 {
		t.Errorf("旧行谷段选价: peak=%v out=%v err=%v, want peak=false out=4", peak, out, err)
	}
}

// TestShapePriceUnknownPeakHoursFallsBackToScalar peakHours 非空但**不是**本仓库写死的那个
// 字面量时,一律不猜(厂商可能真改了时段)→ 回落标量三价(生效默认价)。
func TestShapePriceUnknownPeakHoursFallsBackToScalar(t *testing.T) {
	q := deepseekRow(true)
	q.Detail["peakHours"] = "每晚 20:00-22:00"
	_, out, _, peak, err := ShapePrice(q, beijing(2026, time.September, 14, 10, 0), 480)
	if err != nil {
		t.Fatalf("ShapePrice: %v", err)
	}
	if peak {
		t.Error("未知时段串不该判成高峰")
	}
	mustClose(t, "out", out, q.OutputPrice) // = 标量
}

// TestShapePriceTieredIgnoresTiers 阶梯价**必须**等价于标量三价 —— 绝不消费 detail["tiers"]。
//
// 生产 166 行通义官方价的 tiers 是坏的(qwen3-max 15 档里 0<Token≤32K 重复 4 次且价不同;
// qwen3.7-plus 的 range 全为空串)。这条单测钉住「选价器不碰 tiers」这个红线。
func TestShapePriceTieredIgnoresTiers(t *testing.T) {
	q := domain.OfficialPriceRow{
		Provider: "qwen", ModelName: "qwen3-max",
		Currency: domain.CurrencyCNY, BillingShape: domain.ShapeTiered,
		InputPrice: 2.5, OutputPrice: 10.0, CacheReadPrice: 0.5,
		Detail: map[string]any{
			// 坏 tiers:重复档位、空 range —— 任何消费它的实现都会算出怪数。
			"tiers": []any{
				map[string]any{"range": "0<Token≤32K", "in": 2.5},
				map[string]any{"range": "0<Token≤32K", "in": 8.807},
				map[string]any{"range": "", "in": 99.0},
			},
			"windows": windowsToAny(legacyDeepSeekWindows()), // 即便误带了 windows 也不该生效
		},
	}
	// 故意挑高峰时刻:若实现误按 windows 选价,out 会变。
	in, out, cache, peak, err := ShapePrice(q, beijing(2026, time.September, 14, 10, 0), 480)
	if err != nil {
		t.Fatalf("ShapePrice: %v", err)
	}
	if peak {
		t.Error("tiered 形态不该报告峰位")
	}
	mustClose(t, "in", in, 2.5)
	mustClose(t, "out", out, 10.0)
	mustClose(t, "cache", cache, 0.5)
}

// TestIsPeakWindowTimezoneWins 窗口自带的 tzOffsetMin 优先于调用方回退时区。
// 峰谷时段是厂商属性(DeepSeek 按北京时间),站点展示时区不该改写它。
func TestIsPeakWindowTimezoneWins(t *testing.T) {
	// TZSet 表示「该偏移是显式给定的」—— 内存构造的窗口必须显式置位(见 PriceWindow.TZSet 注释)。
	ws := []PriceWindow{{Days: []int{1}, Start: "09:00", End: "12:00", TZOffsetMin: 480, TZSet: true}}
	// 北京周一 10:00 = UTC 02:00。若误用 UTC(+0)判定会落到周一 02:00 → 谷段。
	at := time.Date(2026, time.September, 14, 2, 0, 0, 0, time.UTC)
	peak, err := IsPeak(ws, at, 0)
	if err != nil {
		t.Fatalf("IsPeak: %v", err)
	}
	if !peak {
		t.Error("窗口自带 +480 未生效,被回退时区(+0)顶掉了")
	}
}

// TestPriceWindowValidate 非法窗口必须在解析期报错,不猜。
func TestPriceWindowValidate(t *testing.T) {
	bad := []PriceWindow{
		{Days: []int{1}, Start: "18:00", End: "09:00"}, // 跨零点
		{Days: []int{1}, Start: "09:00", End: "09:00"}, // 空区间
		{Days: nil, Start: "09:00", End: "12:00"},      // 无星期
		{Days: []int{0}, Start: "09:00", End: "12:00"}, // 星期越界
		{Days: []int{1}, Start: "9时", End: "12:00"},    // 格式错
	}
	for _, w := range bad {
		if err := w.validate(); err == nil {
			t.Errorf("窗口 %+v 应校验失败", w)
		}
	}
}

// TestWindowsFromDetailRejectsGarbage windows 存在但内容坏时,视为不可判定(ok=false),
// 由调用方回落标量 —— 而不是猜一个档位。
func TestWindowsFromDetailRejectsGarbage(t *testing.T) {
	base := map[string]any{}
	for _, raw := range []any{
		"not-a-list",
		[]any{map[string]any{"start": "09:00"}}, // 缺 end/days
		[]any{map[string]any{"days": []any{1}, "start": "18:00", "end": "09:00"}}, // 跨零点
		[]any{map[string]any{"days": []any{}, "start": "09:00", "end": "12:00"}},  // 无星期
	} {
		base["windows"] = raw
		if _, ok := WindowsFromDetail(base, domain.ShapePeakOff); ok {
			t.Errorf("坏 windows 不该解析成功: %#v", raw)
		}
	}
}
