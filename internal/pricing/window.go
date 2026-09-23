package pricing

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"personal-ai-gateway/internal/domain"
)

// 分时选价:把官方价的一行,按「请求发生的时刻」落成一组生效单价。
//
// 为什么需要:DeepSeek 是峰谷分时计价(高峰价为空闲价的 2 倍),但改造前
// RetailPrice 只读三个标量(取值 = 空闲价),等于**把所有时段都按空闲价计费** ——
// 高峰时段实收低于应收,且成本口径同样偏低。
//
// 设计边界(务必遵守):
//   - 只处理 flat 与 peak_offpeak。tiered(通义阶梯)与 discount 一律返回标量四价,
//     与改造前 RetailPrice 逐位一致。
//   - 绝不消费 Detail["tiers"]:生产 166 行通义官方价的 tiers 是坏的
//     (qwen3-max 的 15 档里 0<Token≤32K 重复 4 次且价不同,qwen3.7-plus 的 range 全为空串),
//     任何基于它的选价都会把通义的价格算飞。
//   - 成本与售价共用本文件:同一 at、同一档位判定,保证毛利 = 官方价 × (倍率 − 系数)
//     不会出现「按峰价收费、按谷价记成本」的假毛利。

// isoMonFirst 星期映射:time.Weekday 的 Sunday=0 → ISO 1=Mon … 7=Sun。
func isoWeekday(t time.Time) int {
	if t.Weekday() == time.Sunday {
		return 7
	}
	return int(t.Weekday())
}

// PriceWindow 一个机器可读的峰时段,半开区间 [Start, End),按 Days 里的 ISO 星期生效。
type PriceWindow struct {
	Days        []int  `json:"days"`                  // 1=Mon … 7=Sun
	Start       string `json:"start"`                 // "HH:MM"
	End         string `json:"end"`                   // 必须 > Start(不支持跨零点)
	TZOffsetMin int    `json:"tzOffsetMin,omitempty"` // 偏移分钟;0 是否生效取决于 TZSet
	// TZSet 表示 tzOffsetMin 是**显式给定**的(含显式的 0 = UTC)。
	//
	// 为什么需要:0 既是「UTC 的真实偏移」,又是 int 零值(未设置)。CC 的峰谷窗口正是
	// UTC(=0),若无此标志会被当成「未设置」而套用回退时区(默认 +480),峰谷整体偏 8 小时。
	// 取值来源:decodeWindows 按 key 是否存在判定;内存构造(如 ccPeakWindows)显式置 true。
	// 不进 JSON —— 它只是「该值是否可信」的标志,由解码路径重建。
	TZSet bool `json:"-"`
}

// parseHHMM 解析 "HH:MM" 为当日分钟数。
func parseHHMM(s string) (int, error) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 2 {
		return 0, fmt.Errorf("时刻格式应为 HH:MM,得到 %q", s)
	}
	h, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, fmt.Errorf("小时解析失败 %q: %w", s, err)
	}
	m, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return 0, fmt.Errorf("分钟解析失败 %q: %w", s, err)
	}
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, fmt.Errorf("时刻超出范围 %q", s)
	}
	return h*60 + m, nil
}

// validate 校验窗口自身合法(起点早于终点 —— 跨零点不支持,显式报错而非猜)。
func (w PriceWindow) validate() error {
	start, err := parseHHMM(w.Start)
	if err != nil {
		return err
	}
	end, err := parseHHMM(w.End)
	if err != nil {
		return err
	}
	if end <= start {
		return fmt.Errorf("峰时段结束不得早于或等于开始(%s→%s);不支持跨零点", w.Start, w.End)
	}
	if len(w.Days) == 0 {
		return fmt.Errorf("峰时段未指定生效星期")
	}
	for _, d := range w.Days {
		if d < 1 || d > 7 {
			return fmt.Errorf("星期取值须为 1..7(ISO),得到 %d", d)
		}
	}
	return nil
}

// legacyDeepSeekPeakHours 是旧版 parseDeepSeek 写进 detail_json 的中文字面量。
//
// 已落库的行没有 windows 键,但 peakHours 就是本仓库自己写死的这个常量,
// 精确相等即语义确定,可以安全地映射成等价窗口。**非空但不等则一律不猜**
// (厂商可能真改了时段),返回 ok=false 由调用方按生效默认价计费。
const legacyDeepSeekPeakHours = "北京时间周一至周五 9:00-12:00、14:00-18:00(其余为空闲时段)"

// legacyDeepSeekWindows 旧字面量对应的机器可读窗口(北京时间 = UTC+8,即 480 分钟)。
func legacyDeepSeekWindows() []PriceWindow {
	return []PriceWindow{
		{Days: []int{1, 2, 3, 4, 5}, Start: "09:00", End: "12:00", TZOffsetMin: 480, TZSet: true},
		{Days: []int{1, 2, 3, 4, 5}, Start: "14:00", End: "18:00", TZOffsetMin: 480, TZSet: true},
	}
}

// windowsToAny 把 []PriceWindow 转成可直接放进 detail_json 的形状。
// 走 JSON 往返,保证与 decodeWindows 的解析路径严格互逆(手搓 map 容易漏键或类型不符)。
func windowsToAny(ws []PriceWindow) []any {
	out := make([]any, 0, len(ws))
	for _, w := range ws {
		days := make([]any, 0, len(w.Days))
		for _, d := range w.Days {
			days = append(days, d)
		}
		out = append(out, map[string]any{
			"days": days, "start": w.Start, "end": w.End, "tzOffsetMin": w.TZOffsetMin,
		})
	}
	return out
}

// defaultTZOffsetMin 窗口未自带时区时的回退(北京时间)。
const defaultTZOffsetMin = 480

// WindowsFromDetail 从 detail_json 解析峰时段。
//
// 三层兼容,依次尝试:
//  1. windows(新结构,parseDeepSeek 已开始产出)
//  2. peakHours 精确等于历史字面量 → 等价窗口(已落库的 2 行 DeepSeek 靠这层立即正确计费)
//  3. 都没有 → ok=false,调用方按标量四价(= 生效默认价)计费,与改造前一致
//
// shape 非 peak_offpeak 时直接 ok=false —— 只有分时形态才有窗口语义。
func WindowsFromDetail(detail map[string]any, shape domain.BillingShape) ([]PriceWindow, bool) {
	if shape != domain.ShapePeakOff || detail == nil {
		return nil, false
	}
	if raw, ok := detail["windows"]; ok {
		if ws, ok := decodeWindows(raw); ok && len(ws) > 0 {
			return ws, true
		}
	}
	// 旧字面量:仅精确相等才认(本仓库自己写死的常量,语义确定)。
	if s, ok := detail["peakHours"].(string); ok && strings.TrimSpace(s) == legacyDeepSeekPeakHours {
		return legacyDeepSeekWindows(), true
	}
	return nil, false
}

// decodeWindows 把 detail_json 反序列化出来的 any 还原成 []PriceWindow。
// 走 JSON 往返以保证与编码路径同构(decodeAnyMap 产出的是 map[string]any / []any)。
func decodeWindows(raw any) ([]PriceWindow, bool) {
	list, ok := raw.([]any)
	if !ok || len(list) == 0 {
		return nil, false
	}
	out := make([]PriceWindow, 0, len(list))
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, false
		}
		w := PriceWindow{}
		if s, ok := m["start"].(string); ok {
			w.Start = s
		}
		if s, ok := m["end"].(string); ok {
			w.End = s
		}
		if n, ok := toFloat(m["tzOffsetMin"]); ok {
			w.TZOffsetMin = int(n)
			w.TZSet = true // key 存在即显式(显式的 0 = UTC,不能被当成「未设置」)
		}
		if days, ok := m["days"].([]any); ok {
			for _, d := range days {
				if n, ok := toFloat(d); ok {
					w.Days = append(w.Days, int(n))
				}
			}
		}
		if err := w.validate(); err != nil {
			return nil, false
		}
		out = append(out, w)
	}
	return out, true
}

// toFloat 宽容地把 JSON 数字(可能是 float64 或 json.Number)转成 float64。
func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

// IsPeak 该时刻是否落在任一峰时段。
//
// windows 为空 → false(= 谷价,即 detail 的 effectiveDefault)。
// fallbackTZMin 仅在窗口自带 tzOffsetMin 为 0 时使用。
// 返回 error 只在窗口自身非法时(格式错误/跨零点)—— 调用方应视作"无法判定档位"
// 并按生效默认价计费,而不是猜一个档位。
func IsPeak(windows []PriceWindow, at time.Time, fallbackTZMin int) (bool, error) {
	if len(windows) == 0 {
		return false, nil
	}
	for _, w := range windows {
		if err := w.validate(); err != nil {
			return false, err
		}
		// 只有**显式给过** tzOffsetMin 才采信它;否则用调用方回退时区。
		// 不能写 `if tz == 0`——那会把 UTC(真实偏移 0)误判成「未设置」(issue #27 的坑)。
		tz := fallbackTZMin
		if w.TZSet {
			tz = w.TZOffsetMin
		}
		local := at.UTC().Add(time.Duration(tz) * time.Minute)
		wd := isoWeekday(local)
		if !containsInt(w.Days, wd) {
			continue
		}
		start, _ := parseHHMM(w.Start)
		end, _ := parseHHMM(w.End)
		cur := local.Hour()*60 + local.Minute()
		if cur >= start && cur < end { // 半开区间 [start, end)
			return true, nil
		}
	}
	return false, nil
}

func containsInt(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// peakOffpeakTriple 从 detail 的 peak/offpeak 子对象取一组价(in/out/cacheRead)。
// 缓存写不分档:两档共用行级 CacheWritePrice(见 PeakOffpeakTriples)。
func peakOffpeakTriple(detail map[string]any, key string) (in, out, cache float64, ok bool) {
	sub, ok := detail[key].(map[string]any)
	if !ok {
		return 0, 0, 0, false
	}
	i, ok1 := toFloat(sub["in"])
	o, ok2 := toFloat(sub["out"])
	if !ok1 || !ok2 {
		return 0, 0, 0, false
	}
	c, _ := toFloat(sub["cacheRead"]) // 缓存价缺失(0)是合法的
	return i, o, c, true
}

// ShapePrice 一条官方价在 at 时刻应生效的四价(原币种,每百万 token)。
//
// 分派:
//   - flat / discount / 未知:原样返回标量四价(改造前行为)
//   - peak_offpeak:按 windows 选档取 detail 的 peak/offpeak;无法判定档位时回落标量
//   - tiered:**原样返回标量四价,绝不读 detail["tiers"]**(见文件头说明)
//
// 缓存写价(cacheWrite)是**行级标量**:CC 的峰谷行只在首格给 in/out 两档,缓存写不随档位变;
// 值为 0 表示「无依据」——此时回落**该时刻生效的输入价**(= 改造前 cache_creation 折进
// prompt 的行为,逐位一致,不是回归)。回落放在选档**之后**,故峰谷行也能跟着档位走。
//
// 返回的 isPeak 供调用方记录 request_logs.price_window,便于事后核对账面。
func ShapePrice(q domain.OfficialPriceRow, at time.Time, tzOffsetMin int) (in, out, cacheRead, cacheWrite float64, isPeak bool, err error) {
	scalar := func() (float64, float64, float64, float64, bool, error) {
		cw := q.CacheWritePrice
		if cw == 0 {
			cw = q.InputPrice
		}
		return q.InputPrice, q.OutputPrice, q.CacheReadPrice, cw, false, nil
	}
	if q.BillingShape != domain.ShapePeakOff {
		return scalar()
	}
	windows, ok := WindowsFromDetail(q.Detail, q.BillingShape)
	if !ok {
		// 分时但拿不到机器可读时段 → 按生效默认(标量 = 空闲价)。
		return scalar()
	}
	peak, err := IsPeak(windows, at, tzOffsetMin)
	if err != nil {
		return scalar()
	}
	key := "offpeak"
	if peak {
		key = "peak"
	}
	pi, po, pc, ok := peakOffpeakTriple(q.Detail, key)
	if !ok {
		return scalar()
	}
	cw := q.CacheWritePrice
	if cw == 0 {
		cw = pi // 回落到该档位生效的输入价
	}
	return pi, po, pc, cw, peak, nil
}

// PriceTriple 一组每百万 token 的四价(官方原币种)。CacheWrite = 0 表示该行没给缓存写价。
type PriceTriple struct{ In, Out, CacheRead, CacheWrite float64 }

// PeakOffpeakTriples 分时形态模型的两个档位四价(谷价、峰价),官方原币种。
//
// 展示面专用:客户面要**并列**列出谷/峰两价与时段(见 PLAN.md §5),而不是随时间跳动的单值
// —— 后者随 react-query 缓存过期就变,客户截图对不上账。
//
// 仅 peak_offpeak 且 detail 里两档都可解析时 ok=true;其余形态(flat/tiered/折扣)false,
// 调用方按单一价展示。
func PeakOffpeakTriples(q domain.OfficialPriceRow) (off, peak PriceTriple, ok bool) {
	if q.BillingShape != domain.ShapePeakOff {
		return PriceTriple{}, PriceTriple{}, false
	}
	oi, oo, oc, ok1 := peakOffpeakTriple(q.Detail, "offpeak")
	pi, po, pc, ok2 := peakOffpeakTriple(q.Detail, "peak")
	if !ok1 || !ok2 {
		return PriceTriple{}, PriceTriple{}, false
	}
	return PriceTriple{In: oi, Out: oo, CacheRead: oc, CacheWrite: q.CacheWritePrice},
		PriceTriple{In: pi, Out: po, CacheRead: pc, CacheWrite: q.CacheWritePrice}, true
}

// PeakHoursText detail 里人读的峰时段说明(厂商原文)。空 = 该行没写。
func PeakHoursText(q domain.OfficialPriceRow) string {
	if s, ok := q.Detail["peakHours"].(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

// applyMultiplier 按币种换算并乘系数(成本用渠道系数、售价用倍率),统一 Round6。
func applyMultiplier(q domain.OfficialPriceRow, display domain.Currency, usdPerCNY, multiplier float64,
	in, out, cacheRead, cacheWrite float64) (float64, float64, float64, float64, error) {
	rate := multiplier
	if rate <= 0 {
		rate = 1.0
	}
	ci, err := Convert(in, q.Currency, display, usdPerCNY)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	co, err := Convert(out, q.Currency, display, usdPerCNY)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	cc, err := Convert(cacheRead, q.Currency, display, usdPerCNY)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	cw, err := Convert(cacheWrite, q.Currency, display, usdPerCNY)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	return Round6(ci * rate), Round6(co * rate), Round6(cc * rate), Round6(cw * rate), nil
}

// RetailPriceAt 本站价 = 该时刻生效的官方价 × 倍率(计价币种,每百万 token)。
func RetailPriceAt(q domain.OfficialPriceRow, at time.Time, tzOffsetMin int,
	display domain.Currency, usdPerCNY, multiplier float64) (in, out, cacheRead, cacheWrite float64, isPeak bool, err error) {
	si, so, sc, sw, peak, err := ShapePrice(q, at, tzOffsetMin)
	if err != nil {
		return 0, 0, 0, 0, false, err
	}
	in, out, cacheRead, cacheWrite, err = applyMultiplier(q, display, usdPerCNY, multiplier, si, so, sc, sw)
	return in, out, cacheRead, cacheWrite, peak, err
}

// WholesalePriceAt 成本 = 该时刻生效的官方价 × 渠道系数(计价币种,每百万 token)。
//
// 与 RetailPriceAt 共用同一 ShapePrice 与同一 at —— 这是「成本与售价同步浮动」的实现点:
// 两者只会相差一个乘数,不会因档位判定不一致而产生假毛利。
func WholesalePriceAt(q domain.OfficialPriceRow, at time.Time, tzOffsetMin int,
	display domain.Currency, usdPerCNY, ratio float64) (in, out, cacheRead, cacheWrite float64, isPeak bool, err error) {
	si, so, sc, sw, peak, err := ShapePrice(q, at, tzOffsetMin)
	if err != nil {
		return 0, 0, 0, 0, false, err
	}
	in, out, cacheRead, cacheWrite, err = applyMultiplier(q, display, usdPerCNY, ratio, si, so, sc, sw)
	return in, out, cacheRead, cacheWrite, peak, err
}
