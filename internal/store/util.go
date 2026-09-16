package store

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func formatRFC3339(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

// encodeJSON 序列化任意值为紧凑 JSON 字符串(库内 TEXT 载体)。
func encodeJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func decodeInt64List(raw string) []int64 {
	var out []int64
	if raw == "" || raw == "[]" {
		return []int64{}
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return []int64{}
	}
	return out
}

func decodeStringList(raw string) []string {
	var out []string
	if raw == "" || raw == "[]" {
		return []string{}
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return []string{}
	}
	return out
}

// decodeAnyMap 解析任意 JSON 对象(官方价 detail_json 等)。空/非法 → 空 map。
func decodeAnyMap(raw string) map[string]any {
	out := map[string]any{}
	if raw == "" {
		return out
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return map[string]any{}
	}
	return out
}

// decodeWeightMap 解析 {"<channelId>": weight} JSON 到 map[int64]int。
func decodeWeightMap(raw string) map[int64]int {
	out := map[int64]int{}
	if raw == "" {
		return out
	}
	var rawMap map[string]int
	if err := json.Unmarshal([]byte(raw), &rawMap); err != nil {
		return out
	}
	for k, v := range rawMap {
		id, err := strconv.ParseInt(k, 10, 64)
		if err == nil {
			out[id] = v
		}
	}
	return out
}

func encodeWeightMap(m map[int64]int) string {
	if len(m) == 0 {
		return "{}"
	}
	rawMap := make(map[string]int, len(m))
	for k, v := range m {
		rawMap[strconv.FormatInt(k, 10)] = v
	}
	return encodeJSON(rawMap)
}

// ---- SQLite 布尔(INTEGER 0/1)与可空文本 ----

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// nullFloatPtr 把可空 float64 指针转为驱动可写入的值:nil → SQL NULL(表示「未设」,
// 与显式 0 区分)。用于模型级售价倍率等「未设 = 回落默认」的列。
func nullFloatPtr(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

func lower(s string) string { return strings.ToLower(s) }
