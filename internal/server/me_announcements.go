package server

import (
	"net/http"
)

// 用户自助面(/api/v1/me/announcement*):公告的「拉取未读」与「确认已读」。
// 作用域一律锁死在当前会话账号 —— 已读记录按 (公告, 账号) 去重,互不可见。
// 管理员也走这里:站主同为客户视角的一份,自己的后台公告自己也能看到。

// handleMeAnnouncement 我最新的未确认公告;无则 announcement = null。
func (s *Server) handleMeAnnouncement(w http.ResponseWriter, r *http.Request) {
	me := s.currentAdmin(r)
	a, err := s.st.PendingAnnouncement(me.ID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"announcement": a})
}

// handleMeAnnouncementAck 记「我已知晓」:此后该公告不再对本人弹出。
// 幂等:重复确认返回 200;公告不存在 → 404(不写入悬挂记录)。
func (s *Server) handleMeAnnouncementAck(w http.ResponseWriter, r *http.Request) {
	me := s.currentAdmin(r)
	id, ok := paramID(r, "id")
	if !ok {
		apiErr(w, http.StatusBadRequest, "validation", "bad announcement id")
		return
	}
	if err := s.st.DismissAnnouncement(me.ID, id); err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
