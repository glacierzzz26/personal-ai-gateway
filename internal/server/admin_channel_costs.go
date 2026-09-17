package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"personal-ai-gateway/internal/domain"
	"personal-ai-gateway/internal/pricing"
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

// handleOfficialPricesRefresh 批量刷新全部(或指定)可抓厂商的官方价,并回填模型绑定。
//
// 与单厂商接口的区别是**逐厂商独立成败**:一个厂商抓失败不能中止其他厂商 ——
// 三个厂商共用一个按钮,任何一个的网络抖动都不该让另外两个的更新白跑。
//
// 抓完顺带回填绑定:回填是「新抓到的官方价是否对得上某个模型」的收敛动作,
// 单独一个按钮没人会记得点(re-fetch 后模型仍未绑定 = 成本仍派生不出来)。
func (s *Server) handleOfficialPricesRefresh(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Providers []domain.Provider `json:"providers"`
	}
	// body 可空(空 = 全部可抓厂商);解析成功才采用,失败即按「全部」处理。
	var body struct {
		Providers []domain.Provider `json:"providers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
		req = body
	}

	providers := req.Providers
	if len(providers) == 0 {
		for _, v := range pricing.Vendors() {
			if !v.ManualOnly {
				providers = append(providers, v.Provider)
			}
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), refreshTimeout)
	defer cancel()

	resp := domain.RefreshPricingResp{Results: make([]domain.RefreshProviderResult, 0, len(providers))}
	for _, p := range providers {
		item := domain.RefreshProviderResult{Provider: p}
		res, status, typ, err := s.fetchOfficialPrices(ctx, p)
		switch {
		case err != nil && status != 0:
			// 该厂商失败(不可抓/网络/解析):记下原因,继续跑其余厂商。
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

	// 回填绑定:唯一命中才写,多候选跳过(归错比不归更糟)。
	// 回填失败不回滚抓取结果 —— 价已入库是有效事实,绑定只是便利动作。
	fills, err := s.st.BackfillOfficialBindings(false)
	if err != nil {
		resp.BackfillError = err.Error()
	} else {
		resp.Bound = fills
	}
	writeJSON(w, http.StatusOK, resp)
}
