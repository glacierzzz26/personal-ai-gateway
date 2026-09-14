package domain

import (
	"encoding/json"
	"testing"
)

// TestOptionalFloatJSON 锁定三态的 JSON 契约:键缺席 / null / 数值 各归其态。
// 这是「清空模型倍率回落全局」能生效的前提 —— 普通 *float64 下 null 与未传同义。
func TestOptionalFloatJSON(t *testing.T) {
	type body struct {
		Rate OptionalFloat `json:"rate"`
	}
	cases := []struct {
		in    string
		set   bool
		clear bool
		value float64
	}{
		{`{}`, false, false, 0},            // 键缺席:未传
		{`{"rate":null}`, true, true, 0},   // 显式 null:清空
		{`{"rate":2.5}`, true, false, 2.5}, // 数值:覆盖
		{`{"rate":0}`, true, false, 0},     // 0 也是覆盖(非清空)
	}
	for _, c := range cases {
		var b body
		if err := json.Unmarshal([]byte(c.in), &b); err != nil {
			t.Fatalf("unmarshal %s: %v", c.in, err)
		}
		if b.Rate.Set != c.set || b.Rate.Clear != c.clear || b.Rate.Value != c.value {
			t.Errorf("%s → Set=%v Clear=%v Value=%v, want Set=%v Clear=%v Value=%v",
				c.in, b.Rate.Set, b.Rate.Clear, b.Rate.Value, c.set, c.clear, c.value)
		}
	}
}

func TestOptionalFloatApply(t *testing.T) {
	cur := func() *float64 { v := 3.0; return &v }
	if got := (OptionalFloat{}).Apply(cur()); got == nil || *got != 3.0 {
		t.Errorf("unset should keep original, got %v", got)
	}
	if got := ClearFloat().Apply(cur()); got != nil {
		t.Errorf("clear should yield nil, got %v", *got)
	}
	if got := SetFloat(5).Apply(cur()); got == nil || *got != 5 {
		t.Errorf("set should override, got %v", got)
	}
}
