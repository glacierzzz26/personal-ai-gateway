package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"personal-ai-gateway/internal/domain"
)

// ocEchoUpstream 假 openai 上游:把收到的 x-opencode-session(空则 "<none>")回显进 assistant
// content,供断言网关是否按渠道类型注入会话头。走 anthropic 入站 → openai 出站,回包再由网关
// 译回 anthropic,客户端从 content[0].text 读回该值。
func ocEchoUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s := r.Header.Get("x-opencode-session")
		if s == "" {
			s = "<none>"
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":"c","object":"chat.completion","model":"m",`+
			`"choices":[{"index":0,"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],`+
			`"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`, s)
	})
	up := httptest.NewServer(h)
	t.Cleanup(up.Close)
	return up
}

// postMsgs 发 anthropic 入站请求(/v1/messages),可带自定义头,返回状态码与 body。
func (e *e2eEnv) postMsgs(key, body string, hdr map[string]string) (int, string) {
	e.t.Helper()
	req, err := http.NewRequest(http.MethodPost, e.srv.URL+"/v1/messages", strings.NewReader(body))
	if err != nil {
		e.t.Fatalf("new req: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", key)
	req.Header.Set("anthropic-version", "2023-06-01")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// ocSetup 建一个「模型 m → 指定类型渠道 → 令牌」的隔离环境,返回环境与令牌明文。
// 令牌 allowed=["*"](nil 是空白名单 = 一个模型都不放行,会 403)。
func ocSetup(t *testing.T, ct domain.ChannelType) (*e2eEnv, string) {
	t.Helper()
	e := newE2E(t)
	chID := e.addChannelType("ch", domain.ProviderOpenAI, ocEchoUpstream(t).URL, "sk", ct)
	e.addModelOffer("m", chID, 1)
	return e, e.addToken("t", []string{"*"}, 1000)
}

const testUUID = "11111111-2222-4333-8444-555555555555"

// metaMessagesBody 构造一条 anthropic 入站体,metadata.user_id 是**内嵌 JSON 字符串**
// (其内容为 {"device_id":"d","session_id":<sid>})。整条走 json.Marshal,保证外层转义正确
// —— 手搓字符串极易漏转义内层引号,导致外层 body 本身不合法。
func metaMessagesBody(sessionID string) string {
	inner, _ := json.Marshal(map[string]string{"device_id": "d", "session_id": sessionID})
	body, _ := json.Marshal(map[string]any{
		"model": "m", "max_tokens": 64,
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
		"metadata": map[string]string{"user_id": string(inner)},
	})
	return string(body)
}

// TestE2EOpenCodeSessionFromClaudeHeader Claude Code 的 X-Claude-Code-Session-Id 应被转发为
// opencode 出站 x-opencode-session。
func TestE2EOpenCodeSessionFromClaudeHeader(t *testing.T) {
	e, tok := ocSetup(t, domain.ChannelTypeOpenCode)
	code, body := e.postMsgs(tok, fmt.Sprintf(messagesBody, "m"),
		map[string]string{headerClaudeSession: testUUID})
	if code != 200 {
		t.Fatalf("status = %d, body %s", code, body)
	}
	if got := firstText([]byte(body)); got != testUUID {
		t.Errorf("上游收到的会话头 = %q, want %q", got, testUUID)
	}
}

// TestE2EOpenCodeSessionPassthroughWins 入站已带 x-opencode-session 时原样透传,胜过 Claude 头。
func TestE2EOpenCodeSessionPassthroughWins(t *testing.T) {
	e, tok := ocSetup(t, domain.ChannelTypeOpenCode)
	code, body := e.postMsgs(tok, fmt.Sprintf(messagesBody, "m"), map[string]string{
		headerOpenCodeSession: "client-oc",
		headerClaudeSession:   testUUID,
	})
	if code != 200 {
		t.Fatalf("status = %d, body %s", code, body)
	}
	if got := firstText([]byte(body)); got != "client-oc" {
		t.Errorf("透传头应胜: got %q, want client-oc", got)
	}
}

// TestE2EOpenCodeSessionFromMetadata 无会话头时,从体 metadata.user_id 抽 session_id。
func TestE2EOpenCodeSessionFromMetadata(t *testing.T) {
	e, tok := ocSetup(t, domain.ChannelTypeOpenCode)
	code, body := e.postMsgs(tok, metaMessagesBody(testUUID), nil)
	if code != 200 {
		t.Fatalf("status = %d, body %s", code, body)
	}
	if got := firstText([]byte(body)); got != testUUID {
		t.Errorf("metadata.session_id 应被采用: got %q, want %q", got, testUUID)
	}
}

// TestE2EOpenCodeSessionFallbackStable 无头无 metadata 时,同令牌兜底稳定(同值)、异令牌互异。
func TestE2EOpenCodeSessionFallbackStable(t *testing.T) {
	e, tok := ocSetup(t, domain.ChannelTypeOpenCode)
	plain := fmt.Sprintf(messagesBody, "m")

	_, b1 := e.postMsgs(tok, plain, nil)
	s1 := firstText([]byte(b1))
	_, b2 := e.postMsgs(tok, plain, nil)
	s2 := firstText([]byte(b2))

	if s1 == "" || s1 == "<none>" || !isUUIDShape(s1) {
		t.Fatalf("兜底应为非空 UUID 形状: got %q", s1)
	}
	if s1 != s2 {
		t.Errorf("同令牌兜底应稳定: %q vs %q", s1, s2)
	}
	// 另一个令牌 → 不同兜底值。
	tok2 := e.addToken("t2", nil, 1000)
	_, b3 := e.postMsgs(tok2, plain, nil)
	if s3 := firstText([]byte(b3)); s3 == s1 {
		t.Errorf("异令牌兜底应不同: 都是 %q", s3)
	}
}

// TestE2ENoSessionHeaderLeak 非 opencode(thirdparty)渠道:即便客户端带了 Claude 会话头,
// 上游也**不得**收到 x-opencode-session。
func TestE2ENoSessionHeaderLeak(t *testing.T) {
	e, tok := ocSetup(t, domain.ChannelTypeThirdParty)
	code, body := e.postMsgs(tok, fmt.Sprintf(messagesBody, "m"),
		map[string]string{headerClaudeSession: testUUID})
	if code != 200 {
		t.Fatalf("status = %d, body %s", code, body)
	}
	if got := firstText([]byte(body)); got != "<none>" {
		t.Errorf("非 opencode 渠道不该泄漏会话头: 上游收到 %q", got)
	}
}

// TestE2EOpenCodeMalformedMetadataNoError 体 metadata.user_id 内层 JSON 截断/畸形时不 400、不 panic,
// 回落兜底且头良构。外层 body 仍是合法 JSON(只是 user_id 里那段内嵌串坏了)。
func TestE2EOpenCodeMalformedMetadataNoError(t *testing.T) {
	e, tok := ocSetup(t, domain.ChannelTypeOpenCode)
	raw, _ := json.Marshal(map[string]any{
		"model": "m", "max_tokens": 64,
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
		"metadata": map[string]string{"user_id": `{"session_id":"trunc`},
	})
	code, body := e.postMsgs(tok, string(raw), nil)
	if code != 200 {
		t.Fatalf("畸形 metadata 不该失败: status = %d, body %s", code, body)
	}
	if got := firstText([]byte(body)); !isUUIDShape(got) {
		t.Errorf("应回落兜底 UUID 形状头: got %q", got)
	}
}
