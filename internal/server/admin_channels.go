package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"personal-ai-gateway/internal/domain"
	"personal-ai-gateway/internal/proxy"
)

// handleChannelsList 渠道列表(含状态/延迟/今日用量/供给源数展示)。
func (s *Server) handleChannelsList(w http.ResponseWriter, r *http.Request) {
	v, err := s.buildView()
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	out := make([]domain.ChannelRead, 0, len(v.chOrder))
	for _, ch := range v.chOrder {
		out = append(out, s.channelRead(v, ch))
	}
	writeJSON(w, http.StatusOK, out)
}

// handleChannelsCreate 新建渠道(返回组合后的展示行)。
func (s *Server) handleChannelsCreate(w http.ResponseWriter, r *http.Request) {
	var in domain.ChannelInput
	if !decodeBody(w, r, &in) {
		return
	}
	if in.Name == "" || in.BaseURL == "" {
		apiErr(w, http.StatusBadRequest, "validation", "name and baseUrl are required")
		return
	}
	ch, err := s.st.CreateChannel(in)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	v, _ := s.buildView()
	writeJSON(w, http.StatusOK, s.channelRead(v, ch))
}

// handleChannelsUpdate 整体更新渠道(name/apiKey/启停等)。
func (s *Server) handleChannelsUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := paramID(r, "id")
	if !ok {
		apiErr(w, http.StatusBadRequest, "validation", "bad channel id")
		return
	}
	var in domain.ChannelInput
	if !decodeBody(w, r, &in) {
		return
	}
	ch, err := s.st.UpdateChannel(id, in)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	v, _ := s.buildView()
	writeJSON(w, http.StatusOK, s.channelRead(v, ch))
}

// handleChannelsDelete 删除渠道(offers 级联)。
func (s *Server) handleChannelsDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := paramID(r, "id")
	if !ok {
		apiErr(w, http.StatusBadRequest, "validation", "bad channel id")
		return
	}
	if err := s.st.DeleteChannel(id); err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleChannelTest 连通探测(调渠道 /v1/models)。
func (s *Server) handleChannelTest(w http.ResponseWriter, r *http.Request) {
	id, ok := paramID(r, "id")
	if !ok {
		apiErr(w, http.StatusBadRequest, "validation", "bad channel id")
		return
	}
	ch, err := s.st.GetChannel(id)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	settings, err := s.st.GetSettings()
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	lat, err := s.rl.Probe(r.Context(), s.rl.Client(settings, 0), ch)
	resp := domain.TestResp{OK: err == nil, LatencyMs: lat}
	if err != nil {
		resp.Message = err.Error()
		writeJSON(w, http.StatusOK, resp)
		return
	}
	// 测试成功即回写健康态:清熔断 + EWMA 计入探测延迟(与真实转发同一原语)。
	// 无近 15 分钟真实流量的渠道,列表/仪表盘行延迟即显示 ≈ 最近探测值。
	s.eng.RecordSuccess(ch.ID, lat)
	writeJSON(w, http.StatusOK, resp)
}

// handleChannelQuota 渠道额度(GET {{apiRoot}}/v1/usage 的 rolling/weekly/monthly)。
// 失败/不支持也回 200 + available=false + error(前端据此展示灰色占位,不抛查询异常)。
func (s *Server) handleChannelQuota(w http.ResponseWriter, r *http.Request) {
	id, ok := paramID(r, "id")
	if !ok {
		apiErr(w, http.StatusBadRequest, "validation", "bad channel id")
		return
	}
	ch, err := s.st.GetChannel(id)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	settings, err := s.st.GetSettings()
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	resp := domain.ChannelQuotaResp{Available: true, Windows: map[string]domain.QuotaWindow{}}
	plan, windows, lat, err := s.rl.FetchChannelQuota(r.Context(), s.rl.Client(settings, 0), ch)
	resp.PlanName = plan
	resp.LatencyMs = lat
	if err != nil {
		resp.Available = false
		if errors.Is(err, proxy.ErrQuotaUnsupported) {
			resp.Error = "该渠道协议无 /v1/usage 额度接口"
		} else {
			resp.Error = err.Error()
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}
	resp.Windows = windows
	writeJSON(w, http.StatusOK, resp)
}

// handleChannelSyncModels 拉渠道 /v1/models → 补目录模型 + 挂供给源(默认停用待定价)。
func (s *Server) handleChannelSyncModels(w http.ResponseWriter, r *http.Request) {
	id, ok := paramID(r, "id")
	if !ok {
		apiErr(w, http.StatusBadRequest, "validation", "bad channel id")
		return
	}
	ch, err := s.st.GetChannel(id)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	settings, err := s.st.GetSettings()
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	ids, _, err := s.rl.FetchModels(ctx, s.rl.Client(settings, 0), ch)
	if err != nil {
		apiErr(w, http.StatusBadGateway, "upstream_error", "sync failed: "+err.Error())
		return
	}
	if len(ids) == 0 {
		// 上游无返回 → 渠道已有关联保持不变,仍回权威总数。
		n, err := s.st.ChannelModelCount(ch.ID)
		if err != nil {
			writeStoreErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, domain.SyncResp{Added: 0, Updated: 0, Models: []string{}, ModelCount: n})
		return
	}
	resp := domain.SyncResp{Models: ids}
	for _, name := range ids {
		model, err := s.st.GetModelByName(name)
		if err != nil {
			// 目录无此模型 → 新建(能力未知,可后编辑)
			model, err = s.st.CreateModel(domain.ModelInput{Name: name})
			if err != nil {
				writeStoreErr(w, err)
				return
			}
			resp.Added++
		} else {
			resp.Updated++
		}
		if _, _, err := s.offerForChannel(model.ID, ch.ID); err != nil {
			writeStoreErr(w, err)
			return
		}
	}
	// 该渠道同步后总关联数(与列表「N 个模型」同口径),供前端弹窗与列表同屏一致。
	n, err := s.st.ChannelModelCount(ch.ID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	resp.ModelCount = n
	writeJSON(w, http.StatusOK, resp)
}

// offerForChannel 若模型尚未挂到该渠道则补一个停用 offer(定价后启用)。
func (s *Server) offerForChannel(modelID, channelID int64) (domain.OfferRead, bool, error) {
	offers, err := s.st.ListModelOffers(modelID)
	if err != nil {
		return domain.OfferRead{}, false, err
	}
	for _, o := range offers {
		if o.ChannelID == channelID {
			return o, false, nil
		}
	}
	enabled := false
	of, err := s.st.CreateOffer(modelID, domain.OfferInput{
		ChannelID: channelID,
		Enabled:   &enabled,
		Note:      "auto-synced · set price then enable",
	})
	if err != nil {
		return domain.OfferRead{}, false, err
	}
	return of, true, nil
}
