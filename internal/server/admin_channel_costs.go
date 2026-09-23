package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"personal-ai-gateway/internal/domain"
	"personal-ai-gateway/internal/store"
)

// 「渠道 × 厂商」成本系数管理面(见迁移 m0012)。
//
// 为什么独立于渠道 PATCH:渠道的 PATCH 是整体覆盖语义(ChannelDraft 漏传即清空),
// 系数若混进去,「改个渠道名」会顺带清掉没回传的系数行。故另开一组资源式接口。

// handleChannelCostRatios 某渠道的全部成本系数。
func (s *Server) handleChannelCostRatios(w http.ResponseWriter, r *http.Request) {
	id, ok := paramID(r, "id")
	if !ok {
		apiErr(w, http.StatusBadRequest, "validation", "bad channel id")
		return
	}
	if _, err := s.st.GetChannel(id); err != nil {
		writeStoreErr(w, err)
		return
	}
	rows, err := s.st.ListChannelCostRatios(id)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

// handleChannelCostRatiosReplace 全量替换某渠道的系数行(PUT 语义)。
//
// 单事务先清后插 —— 中途失败整体回滚,不会出现「删了旧的、新的没进去」。
func (s *Server) handleChannelCostRatiosReplace(w http.ResponseWriter, r *http.Request) {
	id, ok := paramID(r, "id")
	if !ok {
		apiErr(w, http.StatusBadRequest, "validation", "bad channel id")
		return
	}
	if _, err := s.st.GetChannel(id); err != nil {
		writeStoreErr(w, err)
		return
	}
	var req struct {
		Ratios []domain.CostRatioInput `json:"ratios"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if err := s.st.ReplaceChannelCostRatios(id, req.Ratios); err != nil {
		if errors.Is(err, store.ErrInvalidRatio) {
			// 系数 0 不是「免费」的合法表达:要表达免费必须删除该行。
			// 填 0 会让「没配」与「配成 0」不可区分 —— 前者该提示「未设系数」。
			apiErr(w, http.StatusBadRequest, "validation", err.Error())
			return
		}
		writeStoreErr(w, err)
		return
	}
	rows, err := s.st.ListChannelCostRatios(id)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

// handleChannelCostRatioDelete 删除一条系数(退回默认 1.0)。
func (s *Server) handleChannelCostRatioDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := paramID(r, "id")
	if !ok {
		apiErr(w, http.StatusBadRequest, "validation", "bad channel id")
		return
	}
	vendor := domain.Provider(queryStr(r, "vendor"))
	if vendor == "" {
		apiErr(w, http.StatusBadRequest, "validation", "vendor is required")
		return
	}
	if err := s.st.DeleteCostRatio(id, vendor); err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// refreshTimeout 批量刷新的总预算。
//
// 比单厂商的 45s 宽:逐厂商串行抓取,三个可抓厂商最坏情况会逼近上限。
// 前端 axios 的超时需同步放宽,否则会在后端仍在跑时先断开。
const refreshTimeout = 120 * time.Second

// handleOfficialPricesRefresh 批量刷新官方价:先抓 commandcode 单页锚点(issue #27 后的主力来源),
// 再把调用方显式指定的 providers 走旧逐厂商路径(为将来可能恢复的逐厂商来源保留),最后回填模型绑定。
//
// 为什么把 CC 放在最前:它是本站官方价的**唯一可抓来源**(逐厂商官网抓取已停用),批量按钮
// 若还按 pricing.Vendors() 里非 ManualOnly 的厂商循环,会得到空集、整个按钮变成空操作。
//
// 逐厂商独立成败的语义保留:显式指定的厂商仍是一个失败不影响其他。
func (s *Server) handleOfficialPricesRefresh(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Providers []domain.Provider `json:"providers"`
	}
	// body 可空(空 = 只跑 CC 锚点);解析成功才采用,失败即按空处理。
	var body struct {
		Providers []domain.Provider `json:"providers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
		req = body
	}

	ctx, cancel := context.WithTimeout(r.Context(), refreshTimeout)
	defer cancel()

	resp := domain.RefreshPricingResp{Results: make([]domain.RefreshProviderResult, 0, len(req.Providers))}

	// ① CC 单页锚点(主力来源)。
	cc, status, typ, err := s.fetchCommandCode(ctx)
	switch {
	case err != nil && status != 0:
		resp.CommandCodeError = err.Error()
		_ = typ
	case err != nil:
		resp.CommandCodeError = err.Error()
	default:
		resp.CommandCode = &cc
		resp.TotalUpserted += cc.Upserted
		resp.TotalRemoved += cc.Removed
	}

	// ② 显式指定的厂商走旧逐厂商路径(当前全部为 ManualOnly → 会返回 manual_only,属预期)。
	for _, p := range req.Providers {
		item := domain.RefreshProviderResult{Provider: p}
		res, status, typ, err := s.fetchOfficialPrices(ctx, p)
		switch {
		case err != nil && status != 0:
			item.Error, item.ErrorType = err.Error(), typ
		case err != nil:
			item.Error, item.ErrorType = err.Error(), "store_error"
		default:
			resp.TotalUpserted += res.Upserted
			resp.TotalRemoved += res.Removed
			item.Upserted, item.Removed, item.SourceURL = res.Upserted, res.Removed, res.SourceURL
		}
		resp.Results = append(resp.Results, item)
	}

	// ③ 回填绑定:唯一命中才写,多候选跳过(归错比不归更糟)。
	// 回填失败不回滚抓取结果 —— 价已入库是有效事实,绑定只是便利动作。
	fills, err := s.st.BackfillOfficialBindings(false)
	if err != nil {
		resp.BackfillError = err.Error()
	} else {
		resp.Bound = fills
	}
	writeJSON(w, http.StatusOK, resp)
}
