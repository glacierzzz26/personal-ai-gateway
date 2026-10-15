package proxy

import (
	"net/http"
	"testing"

	"personal-ai-gateway/internal/domain"
)

// TestRetryableUpstream commandcode 瞬态 400 的识别:
// 仅当 (渠道类型=commandcode ∧ 状态 400 ∧ 体含泛化 "invalid request error") 才判可重试;
// 真·坏请求(带成因的 400)、其它渠道、其它状态码一律按原规则。
func TestRetryableUpstream(t *testing.T) {
	cc := domain.ChannelRow{ChannelType: domain.ChannelTypeCommandCode}
	oc := domain.ChannelRow{ChannelType: domain.ChannelTypeOpenCode}
	tp := domain.ChannelRow{ChannelType: domain.ChannelTypeThirdParty}

	// 实测的两类 400 体:泛化瞬态 vs 带成因。
	transient := []byte(`{"error":{"message":"{\"message\":\"invalid request error trace_id: 7fc819ccefef5a1bb07cf664e81a2092\",\"type\":\"invalid_request_error\"}\n","type":"invalid_request_error"}}`)
	genuineSmall := []byte(`{"error":{"message":"Too small: expected number to be >0","type":"invalid_request_error","param":"max_tokens"}}`)
	genuineModel := []byte(`{"error":{"message":"Model \"x\" is not supported on this endpoint.","type":"invalid_request_error","param":"model","code":"unsupported_model"}}`)
	genuineBody := []byte(`{"error":{"message":"{\"reason\":\"INVALID_REQUEST_BODY\",\"message\":\"max_tokens must be between 0 and 393216\"}","type":"invalid_request_error"}}`)

	cases := []struct {
		name   string
		ch     domain.ChannelRow
		status int
		body   []byte
		want   bool
	}{
		{"commandcode 泛化瞬态 400 → 可重试", cc, http.StatusBadRequest, transient, true},
		{"commandcode 真·坏请求(参数小)→ 不重试", cc, http.StatusBadRequest, genuineSmall, false},
		{"commandcode 真·坏请求(模型不支持)→ 不重试", cc, http.StatusBadRequest, genuineModel, false},
		{"commandcode 真·坏请求(体非法)→ 不重试", cc, http.StatusBadRequest, genuineBody, false},
		{"非 commandcode 渠道泛化 400 → 不重试", oc, http.StatusBadRequest, transient, false},
		{"thirdparty 泛化 400 → 不重试", tp, http.StatusBadRequest, transient, false},
		// 既有可重试状态码仍生效(与渠道类型/体无关)。
		{"500 可重试", oc, http.StatusInternalServerError, nil, true},
		{"429 可重试", tp, http.StatusTooManyRequests, nil, true},
		{"401 可重试(换渠道解 key 错)", tp, http.StatusUnauthorized, nil, true},
		{"404 可重试", tp, http.StatusNotFound, nil, true},
		// 422 等「请求自身问题」仍不可重试。
		{"422 不重试", oc, http.StatusUnprocessableEntity, nil, false},
		{"commandcode 泛化 400 但体为空 → 不重试", cc, http.StatusBadRequest, nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := retryableUpstream(c.ch, c.status, c.body); got != c.want {
				t.Errorf("retryableUpstream(%v,%d)=%v, want %v", c.ch.ChannelType, c.status, got, c.want)
			}
		})
	}
}
