package translate

import "testing"

// 自编码的 id 必须可逆。
func TestToolIDCodecRoundTrip(t *testing.T) {
	for _, id := range []string{"call_abc123", "call_01J9Z", "x", "call_with-dash_under"} {
		enc := OpenAItoAnthropicToolID(id)
		if enc == id {
			t.Fatalf("encode(%q) 未加前缀", id)
		}
		if got := AnthropicToOpenAIToolID(enc); got != id {
			t.Errorf("round trip %q: encode=%q decode=%q, want %q", id, enc, got, id)
		}
	}
	if OpenAItoAnthropicToolID("") != "" {
		t.Errorf("空 id 应编码为空")
	}
}

// anthropic 原生 tool_use id(非我们编码)必须原样透传,不得被 base64 解码成乱码。
func TestToolIDNativeAnthropicPassThrough(t *testing.T) {
	for _, id := range []string{
		"toolu_01A09q90qw90lq917835lq9",
		"toolu_vrtx_01J9Z",
		"chatcmpl-abc",
		"",
	} {
		if got := AnthropicToOpenAIToolID(id); got != id {
			t.Errorf("AnthropicToOpenAIToolID(%q) = %q, want 原样透传", id, got)
		}
	}
}
