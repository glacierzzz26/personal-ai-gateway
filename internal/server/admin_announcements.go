package server

import (
	"net/http"
	"time"

	"personal-ai-gateway/internal/domain"
)

// 公告管理(管理员后台):发布/编辑/启停/删除。
// 用户面拉取与确认见 me_announcements.go —— 两部分共用一个 store,但作用域不同。

// validateAnnouncement 校验公告体:标题/正文非空,级别合法,定时/过期时刻可解析。
// 返回错误消息(空串 = 通过)。时刻串统一要求 RFC3339Nano,与库内写入口径一致。
func validateAnnouncement(in *domain.AnnouncementInput) string {
	in.Defaults()
	if in.Title == "" || in.Body == "" {
		return "title and body are required"
	}
	if !in.Level.Valid() {
		return "level must be info|warn|danger"
	}
	for _, ts := range []*string{in.PublishAt, in.ExpiresAt} {
		if ts == nil || *ts == "" {
			continue
		}
		if _, err := time.Parse(time.RFC3339Nano, *ts); err != nil {
			return "publishAt/expiresAt must be RFC3339"
		}
	}
	return ""
}

// handleAnnouncementsList 全部公告(新建在前,附已读计数)。
func (s *Server) handleAnnouncementsList(w http.ResponseWriter, r *http.Request) {
	items, err := s.st.ListAnnouncements()
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if items == nil {
		items = []domain.AnnouncementRead{}
	}
	writeJSON(w, http.StatusOK, items)
}

// handleAnnouncementsCreate 发布公告。
func (s *Server) handleAnnouncementsCreate(w http.ResponseWriter, r *http.Request) {
	var in domain.AnnouncementInput
	if !decodeBody(w, r, &in) {
		return
	}
	if msg := validateAnnouncement(&in); msg != "" {
		apiErr(w, http.StatusBadRequest, "validation", msg)
		return
	}
	a, err := s.st.CreateAnnouncement(in)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

// handleAnnouncementsUpdate 更新公告(整字段覆盖)。
func (s *Server) handleAnnouncementsUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := paramID(r, "id")
	if !ok {
		apiErr(w, http.StatusBadRequest, "validation", "bad announcement id")
		return
	}
	var in domain.AnnouncementInput
	if !decodeBody(w, r, &in) {
		return
	}
	if msg := validateAnnouncement(&in); msg != "" {
		apiErr(w, http.StatusBadRequest, "validation", msg)
		return
	}
	a, err := s.st.UpdateAnnouncement(id, in)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

// handleAnnouncementsDelete 删除公告(已读记录级联清除)。
func (s *Server) handleAnnouncementsDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := paramID(r, "id")
	if !ok {
		apiErr(w, http.StatusBadRequest, "validation", "bad announcement id")
		return
	}
	if err := s.st.DeleteAnnouncement(id); err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
