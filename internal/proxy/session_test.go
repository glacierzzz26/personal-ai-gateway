package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"personal-ai-gateway/internal/domain"
)

// TestSessionIDFromMetadata 从入站体 metadata.user_id(内嵌 JSON 字符串)抽 session_id:
// 合法内嵌串取到值;缺失 / 类型不符 / 任一层解析失败一律空串(绝不因此让请求失败)。
func TestSessionIDFromMetadata(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"合法内嵌串", `{"metadata":{"user_id":"{\"device_id\":\"d\",\"session_id\":\"sess-1\"}"}}`, "sess-1"},
		{"无 metadata", `{"model":"m"}`, ""},
		{"user_id 缺失", `{"metadata":{}}`, ""},
		{"user_id 是 JSON 对象(非字符串)", `{"metadata":{"user_id":{"session_id":"x"}}}`, ""},
		{"user_id 非 JSON 串", `{"metadata":{"user_id":"notjson"}}`, ""},
		{"内嵌串无 session_id", `{"metadata":{"user_id":"{\"device_id\":\"d\"}"}}`, ""},
		{"空 body", ``, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sessionIDFromMetadata([]byte(c.body)); got != c.want {
				t.Errorf("sessionIDFromMetadata(%s) = %q, want %q", c.body, got, c.want)
			}
		})
	}
}

// TestResolveSessionIDPriority 优先级:① 入站 x-opencode-session > ② X-Claude-Code-Session-Id
// > ③ 体 metadata.user_id > ④ 令牌派生兜底。
func TestResolveSessionIDPriority(t *testing.T) {
	tok := domain.TokenRow{SHA256: "deadbeef"}
	metaBody := `{"metadata":{"user_id":"{\"session_id\":\"meta-1\"}"}}`

	mk := func(hdr map[string]string, body string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		return r
	}

	// ① 三级都在 → 透传头胜。
	got := resolveSessionID(mk(map[string]string{
		"x-opencode-session": "oc-1",
		headerClaudeSession:  "cc-1",
	}, metaBody), []byte(metaBody), tok)
	if got != "oc-1" {
		t.Errorf("① 透传头应胜: got %q", got)
	}
	// ② Claude 头 > metadata。
	got = resolveSessionID(mk(map[string]string{headerClaudeSession: "cc-1"}, metaBody), []byte(metaBody), tok)
	if got != "cc-1" {
		t.Errorf("② Claude 头应胜 metadata: got %q", got)
	}
	// ③ 只剩 metadata。
	got = resolveSessionID(mk(nil, metaBody), []byte(metaBody), tok)
	if got != "meta-1" {
		t.Errorf("③ 应取 metadata.session_id: got %q", got)
	}
	// ④ 都没有 → 令牌派生兜底(非空、UUID 形状、确定)。
	got = resolveSessionID(mk(nil, `{"model":"m"}`), []byte(`{"model":"m"}`), tok)
	if got == "" || !isUUIDShape(got) {
		t.Errorf("④ 兜底应为非空 UUID 形状: got %q", got)
	}
	if again := resolveSessionID(mk(nil, `{"model":"m"}`), []byte(`{"model":"m"}`), tok); again != got {
		t.Errorf("④ 同令牌兜底应稳定: %q vs %q", got, again)
	}
	if other := fallbackSessionID(domain.TokenRow{SHA256: "cafebabe"}); other == got {
		t.Errorf("不同令牌兜底应不同: %q", other)
	}
}

// TestSetOutboundHeadersOpenCodeSession 门控:仅 opencode 渠道写 x-opencode-session 与 UA;
// 空 SessionID 不写头(上游把「空但存在」仍视为缺失);其它渠道一概不碰。
func TestSetOutboundHeadersOpenCodeSession(t *testing.T) {
	r := &Relay{cfg: relayConfig{UserAgent: "personal-ai-gateway/test"}}

	// opencode + 非空 → 两头都设。
	h := http.Header{}
	r.setOutboundHeaders(h, &outboundReq{Proto: ProtoOpenAI, APIKey: "k",
		ChannelType: domain.ChannelTypeOpenCode, SessionID: "s1"})
	if h.Get("x-opencode-session") != "s1" {
		t.Errorf("opencode 应设会话头: got %q", h.Get("x-opencode-session"))
	}
	if h.Get("User-Agent") != "personal-ai-gateway/test" {
		t.Errorf("opencode 应设 UA: got %q", h.Get("User-Agent"))
	}

	// opencode + 空 SessionID → 不设会话头,但仍设 UA。
	h = http.Header{}
	r.setOutboundHeaders(h, &outboundReq{Proto: ProtoOpenAI, APIKey: "k",
		ChannelType: domain.ChannelTypeOpenCode, SessionID: ""})
	if _, ok := h["X-Opencode-Session"]; ok {
		t.Error("空 SessionID 不该写会话头")
	}
	if h.Get("User-Agent") != "personal-ai-gateway/test" {
		t.Errorf("opencode 空 SessionID 仍应设 UA: got %q", h.Get("User-Agent"))
	}

	// 非 opencode → 既不设会话头也不改 UA。
	h = http.Header{}
	r.setOutboundHeaders(h, &outboundReq{Proto: ProtoOpenAI, APIKey: "k",
		ChannelType: domain.ChannelTypeThirdParty, SessionID: "s1"})
	if _, ok := h["X-Opencode-Session"]; ok {
		t.Error("非 opencode 渠道不该泄漏会话头")
	}
	if h.Get("User-Agent") != "" {
		t.Errorf("非 opencode 渠道不该设 UA: got %q", h.Get("User-Agent"))
	}
}

// isUUIDShape 是否为 8-4-4-4-12 的 UUID 形状(只校验形状,不校验版本位)。
func isUUIDShape(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
