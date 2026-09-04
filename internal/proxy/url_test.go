package proxy

import "testing"

func TestAPIBase(t *testing.T) {
	cases := []struct{ in, want string }{
		// 用户按主流惯例填带尾缀 /v1(前端示例 https://api.deepseek.com/v1)
		{"https://api.deepseek.com/v1", "https://api.deepseek.com"},
		{"https://opencode.ai/zen/go/v1", "https://opencode.ai/zen/go"},
		{"https://api.openai.com/v1/", "https://api.openai.com"},
		// 只填根(测试/部分厂商惯例)
		{"https://api.anthropic.com", "https://api.anthropic.com"},
		{"http://127.0.0.1:9999", "http://127.0.0.1:9999"},
		// 尾斜杠一律清掉
		{"https://host/path/", "https://host/path"},
		// 非 /v1 结尾不动(Azure deployment 等)
		{"https://x.openai.azure.com/openai/deployments/gpt-4o", "https://x.openai.azure.com/openai/deployments/gpt-4o"},
	}
	for _, c := range cases {
		if got := apiRoot(c.in); got != c.want {
			t.Errorf("apiRoot(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
