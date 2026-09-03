package server

import (
	"net/http"

	"personal-ai-gateway/internal/domain"
)

// handleRulesList 全部路由规则(匹配序自上而下)。
func (s *Server) handleRulesList(w http.ResponseWriter, r *http.Request) {
	rules, err := s.st.ListRules()
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if rules == nil {
		rules = []domain.RuleRead{}
	}
	writeJSON(w, http.StatusOK, rules)
}

// handleRulesCreate 新增规则(追加到队尾)。
func (s *Server) handleRulesCreate(w http.ResponseWriter, r *http.Request) {
	var in domain.RuleInput
	if !decodeBody(w, r, &in) {
		return
	}
	if in.Name == "" || in.Pattern == "" {
		apiErr(w, http.StatusBadRequest, "validation", "name and pattern are required")
		return
	}
	rule, err := s.st.CreateRule(in)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rule)
}

// handleRulesUpdate 更新规则(含启停、渠道集、策略、兜底)。
func (s *Server) handleRulesUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := paramID(r, "id")
	if !ok {
		apiErr(w, http.StatusBadRequest, "validation", "bad rule id")
		return
	}
	var in domain.RuleInput
	if !decodeBody(w, r, &in) {
		return
	}
	rule, err := s.st.UpdateRule(id, in)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rule)
}

// handleRulesDelete 删除规则(其余 sort 重排)。
func (s *Server) handleRulesDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := paramID(r, "id")
	if !ok {
		apiErr(w, http.StatusBadRequest, "validation", "bad rule id")
		return
	}
	if err := s.st.DeleteRule(id); err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleRulesReorder 拖拽重排(按展示序 from → insertAt)。
func (s *Server) handleRulesReorder(w http.ResponseWriter, r *http.Request) {
	var body struct {
		From     int `json:"from"`
		InsertAt int `json:"insertAt"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	rules, err := s.st.MoveRule(body.From, body.InsertAt)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rules)
}
