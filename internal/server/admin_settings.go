package server

import (
	"net/http"
	"strings"

	"personal-ai-gateway/internal/domain"
)

// handleSettingsGet 网关参数读取。
func (s *Server) handleSettingsGet(w http.ResponseWriter, r *http.Request) {
	st, err := s.st.GetSettings()
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// handleSettingsPatch 保存网关参数(校验范围;日志保留天数变化触发即时清理)。
func (s *Server) handleSettingsPatch(w http.ResponseWriter, r *http.Request) {
	var in domain.Settings
	if !decodeBody(w, r, &in) {
		return
	}
	if in.RequestTimeoutMs <= 0 {
		in.RequestTimeoutMs = 60000
	}
	if in.MaxRetries < 0 {
		in.MaxRetries = 0
	}
	if in.SampleRatePct < 0 {
		in.SampleRatePct = 0
	}
	if in.SampleRatePct > 100 {
		in.SampleRatePct = 100
	}
	if in.LogRetentionDays < 0 {
		in.LogRetentionDays = 0
	}
	if in.TZOffsetMin < -720 || in.TZOffsetMin > 840 {
		apiErr(w, http.StatusBadRequest, "validation", "tzOffsetMin out of range [-720, 840]")
		return
	}
	// 对外基址:规范化(去空白与尾斜杠);留空=按访问地址推断。
	in.PublicBaseURL = strings.TrimRight(strings.TrimSpace(in.PublicBaseURL), "/")
	if err := s.st.SaveSettings(in); err != nil {
		writeStoreErr(w, err)
		return
	}
	if err := s.st.PruneLogs(in.LogRetentionDays); err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, in)
}
