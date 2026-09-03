package server

import (
	"net/http"
	"time"

	"personal-ai-gateway/internal/domain"
)

// handleModelsList 模型目录(模型 + 供给源 + 今日用量)。
func (s *Server) handleModelsList(w http.ResponseWriter, r *http.Request) {
	models, err := s.modelsListRead()
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, models)
}

// handleModelsCreate 新建目录模型。
func (s *Server) handleModelsCreate(w http.ResponseWriter, r *http.Request) {
	var in domain.ModelInput
	if !decodeBody(w, r, &in) {
		return
	}
	if in.Name == "" {
		apiErr(w, http.StatusBadRequest, "validation", "model name is required")
		return
	}
	m, err := s.st.CreateModel(in)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	mr, err := s.singleModelRead(m.ID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, mr)
}

// handleModelsUpdate 更新模型(名称/上下文/能力/启停)。
func (s *Server) handleModelsUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := paramID(r, "id")
	if !ok {
		apiErr(w, http.StatusBadRequest, "validation", "bad model id")
		return
	}
	var in domain.ModelInput
	if !decodeBody(w, r, &in) {
		return
	}
	if _, err := s.st.UpdateModel(id, in); err != nil {
		writeStoreErr(w, err)
		return
	}
	mr, err := s.singleModelRead(id)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, mr)
}

// handleModelsDelete 删除目录模型(offers 级联)。
func (s *Server) handleModelsDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := paramID(r, "id")
	if !ok {
		apiErr(w, http.StatusBadRequest, "validation", "bad model id")
		return
	}
	if err := s.st.DeleteModel(id); err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleModelUsage 模型抽屉「用量」:日曲线 + 按渠道聚合。
func (s *Server) handleModelUsage(w http.ResponseWriter, r *http.Request) {
	id, ok := paramID(r, "id")
	if !ok {
		apiErr(w, http.StatusBadRequest, "validation", "bad model id")
		return
	}
	m, err := s.st.GetModel(id)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	settings, err := s.st.GetSettings()
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	days := queryInt(r, "days", 7)
	if days < 1 {
		days = 1
	}
	if days > 90 {
		days = 90
	}
	now := time.Now().UTC()
	from := now.AddDate(0, 0, -days)

	var resp domain.ModelUsageResp
	series, err := s.st.QueryModelSeries(m.Name, "day", from, now, settings.TZOffsetMin)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	resp.Daily = fillSeries(series, seriesBuckets("day", settings.TZOffsetMin, now, days))

	byCh, err := s.st.QueryModelChannels(m.Name, from, now)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	resp.ByChannel = byCh
	if resp.ByChannel == nil {
		resp.ByChannel = []domain.ModelChannelUsage{}
	}
	writeJSON(w, http.StatusOK, resp)
}

// ---------------- 供给源 ----------------

// handleOffersCreate 为模型挂载供给源。
func (s *Server) handleOffersCreate(w http.ResponseWriter, r *http.Request) {
	modelID, ok := paramID(r, "id")
	if !ok {
		apiErr(w, http.StatusBadRequest, "validation", "bad model id")
		return
	}
	var in domain.OfferInput
	if !decodeBody(w, r, &in) {
		return
	}
	if in.ChannelID <= 0 {
		apiErr(w, http.StatusBadRequest, "validation", "channelId is required")
		return
	}
	if _, err := s.st.GetChannel(in.ChannelID); err != nil {
		apiErr(w, http.StatusBadRequest, "validation", "channel does not exist")
		return
	}
	of, err := s.st.CreateOffer(modelID, in)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	v, _ := s.buildView()
	writeJSON(w, http.StatusOK, s.offerRead(v, of))
}

// handleOffersUpdate 改价/启停/限流等。
func (s *Server) handleOffersUpdate(w http.ResponseWriter, r *http.Request) {
	oid, ok := paramID(r, "oid")
	if !ok {
		apiErr(w, http.StatusBadRequest, "validation", "bad offer id")
		return
	}
	var in domain.OfferInput
	if !decodeBody(w, r, &in) {
		return
	}
	of, err := s.st.UpdateOffer(oid, in)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	v, _ := s.buildView()
	writeJSON(w, http.StatusOK, s.offerRead(v, of))
}

// handleOffersDelete 移除供给源。
func (s *Server) handleOffersDelete(w http.ResponseWriter, r *http.Request) {
	oid, ok := paramID(r, "oid")
	if !ok {
		apiErr(w, http.StatusBadRequest, "validation", "bad offer id")
		return
	}
	if err := s.st.DeleteOffer(oid); err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleOffersReorder 拖拽/设为首选重排(priority 重算 1..N)。
func (s *Server) handleOffersReorder(w http.ResponseWriter, r *http.Request) {
	modelID, ok := paramID(r, "id")
	if !ok {
		apiErr(w, http.StatusBadRequest, "validation", "bad model id")
		return
	}
	var body struct {
		From     int `json:"from"`
		InsertAt int `json:"insertAt"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	offers, err := s.st.ReorderOffers(modelID, body.From, body.InsertAt)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	v, _ := s.buildView()
	out := make([]domain.OfferRead, 0, len(offers))
	for _, o := range offers {
		out = append(out, s.offerRead(v, o))
	}
	writeJSON(w, http.StatusOK, out)
}

// singleModelRead 单模型读(编辑/新建回显)。
func (s *Server) singleModelRead(id int64) (domain.ModelRead, error) {
	m, err := s.st.GetModel(id)
	if err != nil {
		return domain.ModelRead{}, err
	}
	settings, err := s.st.GetSettings()
	if err != nil {
		return domain.ModelRead{}, err
	}
	v, err := s.buildView()
	if err != nil {
		return domain.ModelRead{}, err
	}
	offers, err := s.st.ListModelOffers(id)
	if err != nil {
		return domain.ModelRead{}, err
	}
	now := time.Now().UTC()
	today, err := s.modelTodayStats(now, settings.TZOffsetMin)
	if err != nil {
		return domain.ModelRead{}, err
	}
	mr := domain.ModelRead{
		ID: m.ID, Name: m.Name, ContextWindow: m.ContextWindow,
		Capabilities: m.Capabilities, Enabled: m.Enabled,
		Offers: make([]domain.OfferRead, 0, len(offers)),
	}
	for _, o := range offers {
		mr.Offers = append(mr.Offers, s.offerRead(v, o))
	}
	if u, ok := today[m.Name]; ok {
		mr.TodayRequests = u.Requests
		mr.SuccessRate = (1 - u.ErrorRate) * 100
	} else {
		mr.SuccessRate = 100
	}
	return mr, nil
}
