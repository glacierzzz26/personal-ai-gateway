package server

import (
	"errors"
	"net/http"
	"time"

	"personal-ai-gateway/internal/domain"
	"personal-ai-gateway/internal/pricing"
	"personal-ai-gateway/internal/store"
)

// handleModelsList 模型目录(模型 + 供给源 + 今日用量)。
//
// 分角色(PLAN.md §5):管理员拿全量(含渠道名/上游真实名/官方价来源/全站用量);
// 普通用户拿收敛后的清单(仅启用 ∩ 模型可及性,只留对外名/上下文/能力/官方价+本站价)。
func (s *Server) handleModelsList(w http.ResponseWriter, r *http.Request) {
	if s.currentAdmin(r).Role != domain.RoleAdmin {
		views, err := s.userModelsList(nil)
		if err != nil {
			writeStoreErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, views)
		return
	}
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
	cur, err := s.st.GetModel(id)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	// 缺省 enabled 视为 true(与 ModelInput.Defaults 一致)
	enabledAfter := in.Enabled == nil || *in.Enabled
	if _, err := s.st.UpdateModel(id, in); err != nil {
		writeStoreErr(w, err)
		return
	}
	// 模型由停用切到启用时,联动打开其下全部供给源;单独关/开供给源不受父级锁死。
	if !cur.Enabled && enabledAfter {
		if err := s.st.SetModelOffersEnabled(id, true); err != nil {
			writeStoreErr(w, err)
			return
		}
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

// handleModelsMerge 手工合并重复模型:把 {id} 的供给源并入 {intoId} 后删除 {id}。
func (s *Server) handleModelsMerge(w http.ResponseWriter, r *http.Request) {
	id, ok := paramID(r, "id")
	if !ok {
		apiErr(w, http.StatusBadRequest, "validation", "bad model id")
		return
	}
	var in struct {
		IntoID int64 `json:"intoId"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	if in.IntoID <= 0 {
		apiErr(w, http.StatusBadRequest, "validation", "intoId is required")
		return
	}
	if in.IntoID == id {
		apiErr(w, http.StatusBadRequest, "validation", "不能合并到自身")
		return
	}
	if _, err := s.st.MergeModels(id, in.IntoID); err != nil {
		if errors.Is(err, store.ErrConflict) {
			apiErr(w, http.StatusConflict, "merge_conflict",
				"两个模型在同一渠道上都有供给源,请先删除其中一个再合并")
			return
		}
		writeStoreErr(w, err)
		return
	}
	mr, err := s.singleModelRead(in.IntoID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, mr)
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
	// 日志统一以对外名归因(见 proxy 落账);重命名时按真实名与统一名都查,兼容重命名前历史。
	names := []string{m.PublicName()}
	if m.DisplayName != "" && m.DisplayName != m.Name {
		names = append(names, m.Name)
	}
	daily, byCh, err := s.modelUsageFromLogs(names, days, from, now, settings.TZOffsetMin)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	resp.Daily = daily
	resp.ByChannel = byCh
	writeJSON(w, http.StatusOK, resp)
}

// modelUsageFromLogs 汇总一个模型的日曲线与按渠道用量;合并多个名称(重命名前后的历史)。
func (s *Server) modelUsageFromLogs(names []string, days int, from, now time.Time, tz int) ([]domain.MetricPoint, []domain.ModelChannelUsage, error) {
	var series []domain.MetricPoint
	var byCh []domain.ModelChannelUsage
	for _, name := range names {
		st, err := s.st.QueryModelSeries(name, "day", from, now, tz)
		if err != nil {
			return nil, nil, err
		}
		series = mergeSeries(series, st)
		ch, err := s.st.QueryModelChannels(name, from, now)
		if err != nil {
			return nil, nil, err
		}
		byCh = mergeChannelUsage(byCh, ch)
	}
	if byCh == nil {
		byCh = []domain.ModelChannelUsage{}
	}
	return fillSeries(series, seriesBuckets("day", tz, now, days)), byCh, nil
}

// mergeSeries 把同名桶的曲线相加(按 TS 归并)。
func mergeSeries(dst, add []domain.MetricPoint) []domain.MetricPoint {
	idx := make(map[string]int, len(dst))
	for i, p := range dst {
		idx[p.TS] = i
	}
	for _, p := range add {
		if i, ok := idx[p.TS]; ok {
			dst[i].Requests += p.Requests
			dst[i].Errors += p.Errors
			dst[i].CostUsd += p.CostUsd
			continue
		}
		idx[p.TS] = len(dst)
		dst = append(dst, p)
	}
	return dst
}

// mergeChannelUsage 把同名渠道的用量相加。
func mergeChannelUsage(dst, add []domain.ModelChannelUsage) []domain.ModelChannelUsage {
	idx := make(map[string]int, len(dst))
	for i, c := range dst {
		idx[c.ChannelName] = i
	}
	for _, c := range add {
		if i, ok := idx[c.ChannelName]; ok {
			dst[i].Requests += c.Requests
			dst[i].CostUsd += c.CostUsd
			continue
		}
		idx[c.ChannelName] = len(dst)
		dst = append(dst, c)
	}
	return dst
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
		ID: m.ID, Name: m.PublicName(), DisplayName: m.DisplayName, OriginalName: m.Name,
		ContextWindow: m.ContextWindow,
		Capabilities:  m.Capabilities, Enabled: m.Enabled,
		Offers:         make([]domain.OfferRead, 0, len(offers)),
		OfficialVendor: m.OfficialVendor, OfficialModelName: m.OfficialModelName,
		InferredVendor: pricing.InferVendor(m.Name),
		RateOverride:   m.RateOverride,
	}
	for _, o := range offers {
		mr.Offers = append(mr.Offers, s.offerRead(v, o))
	}
	if u, ok := today[m.PublicName()]; ok {
		mr.TodayRequests = u.Requests
		mr.SuccessRate = (1 - u.ErrorRate) * 100
	} else {
		mr.SuccessRate = 100
	}
	return mr, nil
}
