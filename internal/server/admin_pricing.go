package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"personal-ai-gateway/internal/domain"
	"personal-ai-gateway/internal/pricing"
)

// 官方定价(pricing)管理面。
//
// 红线(issue #3):只采信厂商官方域名(传输层白名单);每条价格留来源 URL + 抓取时间 + 内容 sha256;
// 抓取/解析/校验任一失败一律显式报错,绝不写非官方或估算值,原报价保持不变。
// 抓取结果先落 official_prices(官方参考价,与手工报价分表),再需人工点「应用」才写 offer。

// fetchOfficialPrices 按 provider 抓取官方单价表并 upsert official_prices(渠道级与厂商级共用核心)。
//
// 返回 (结果, httpStatus, errType, err):err == nil 时后三者无意义;status == 0 表示 store 错误
// (调用方用 writeStoreErr 呈现),否则用 apiErr(status, errType, ...)。
// 抓取失败返回 502 + 明确 message(不落库、不动原价);无官方来源/仅手工录入返回 400。
func (s *Server) fetchOfficialPrices(ctx context.Context, p domain.Provider) (domain.FetchPricingResult, int, string, error) {
	if !pricing.Supports(p) {
		return domain.FetchPricingResult{}, http.StatusBadRequest, "unsupported",
			fmt.Errorf("%w: %s", pricing.ErrNoOfficialSource, p)
	}
	settings, err := s.st.GetSettings()
	if err != nil {
		return domain.FetchPricingResult{}, 0, "", err
	}
	// 复用出站 client(代理/跳过 TLS/超时),再包一层官方域名白名单 —— 结构性保证只访问官方域。
	base := s.rl.Client(settings, 0)
	if s.pricingBase != nil {
		base = s.pricingBase(p, settings)
	}
	client := pricing.AllowlistClient(*base, pricing.Hosts(p))

	rows, _, err := pricing.Fetch(ctx, client, p)
	if err != nil {
		status, typ := http.StatusBadGateway, "fetch_failed"
		if errors.Is(err, pricing.ErrManualOnly) || errors.Is(err, pricing.ErrNoOfficialSource) {
			status, typ = http.StatusBadRequest, "manual_only"
		}
		return domain.FetchPricingResult{}, status, typ, err
	}

	resp := domain.FetchPricingResult{Provider: p, SourceURL: pricing.SourceURL(p)}
	for _, row := range rows {
		saved, err := s.st.UpsertOfficialPrice(row)
		if err != nil {
			return resp, 0, "", err
		}
		resp.Models = append(resp.Models, saved.ModelName)
		resp.Upserted++
		if resp.ContentSHA == "" {
			resp.ContentSHA = saved.ContentSHA256
		}
	}
	return resp, http.StatusOK, "", nil
}

// handleFetchPricing 按渠道 provider 抓取官方单价表 → upsert official_prices。
func (s *Server) handleFetchPricing(w http.ResponseWriter, r *http.Request) {
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
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	resp, status, typ, err := s.fetchOfficialPrices(ctx, ch.Provider)
	if err != nil {
		if status == 0 {
			writeStoreErr(w, err)
			return
		}
		apiErr(w, status, typ, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleOfficialPricesFetch 按厂商抓取官方单价表(无需厂商直连渠道)。
//
// 聚合中转渠道的 provider 不是厂商,渠道级抓取(handleFetchPricing)对它们不适用;
// 此接口直接指定厂商抓取,复用同一抓取核心与官方域名白名单。
func (s *Server) handleOfficialPricesFetch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider domain.Provider `json:"provider"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Provider == "" {
		apiErr(w, http.StatusBadRequest, "validation", "provider is required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	resp, status, typ, err := s.fetchOfficialPrices(ctx, req.Provider)
	if err != nil {
		if status == 0 {
			writeStoreErr(w, err)
			return
		}
		apiErr(w, status, typ, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleOfficialVendors 可抓取/可手工录入的厂商清单(前端据此驱动入口与门禁,替代硬编码)。
func (s *Server) handleOfficialVendors(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, pricing.Vendors())
}

// handleChannelOfficialPrices 某渠道 provider 的官方参考价 + 与现有 offer 的比对。
func (s *Server) handleChannelOfficialPrices(w http.ResponseWriter, r *http.Request) {
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
	rows, err := s.st.ListOfficialPrices(ch.Provider)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.officialPriceViews(ch.Provider, rows))
}

// handleOfficialPricesAll 全部官方参考价(模型广场「查看官方参考价」)。
// 可选 ?provider= 过滤。
func (s *Server) handleOfficialPricesAll(w http.ResponseWriter, r *http.Request) {
	p := domain.Provider(queryStr(r, "provider"))
	var (
		rows []domain.OfficialPriceRow
		err  error
	)
	if p != "" {
		rows, err = s.st.ListOfficialPrices(p)
	} else {
		rows, err = s.st.ListAllOfficialPrices()
	}
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.officialPriceViews(p, rows))
}

// handleOfficialPriceManual 手工录入官方参考价(智谱等页面不可抓的厂商)。
// 来源 URL 必填 —— 手工录入同样要留证可核对。
func (s *Server) handleOfficialPriceManual(w http.ResponseWriter, r *http.Request) {
	var in domain.OfficialPriceInput
	if !decodeBody(w, r, &in) {
		return
	}
	row, err := pricing.BuildManual(in)
	if err != nil {
		apiErr(w, http.StatusBadRequest, "validation", err.Error())
		return
	}
	saved, err := s.st.UpsertOfficialPrice(row)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.officialPriceView(saved))
}

// handleOfficialPriceDelete 删除一条官方参考价(不影响已应用到 offer 的价与留证)。
func (s *Server) handleOfficialPriceDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := paramID(r, "id")
	if !ok {
		apiErr(w, http.StatusBadRequest, "validation", "bad official price id")
		return
	}
	if err := s.st.DeleteOfficialPrice(id); err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleOfficialPriceApply 把官方价应用到某 offer(写三价 + 来源留证)。
//
// 手工覆盖价优先:offer.override_price=true 时必须 confirmOverride=true 才放行,
// 避免自动化把用户手工维护的价悄悄冲掉。原币为 CNY 且未设汇率时无法换算,直接拒绝。
func (s *Server) handleOfficialPriceApply(w http.ResponseWriter, r *http.Request) {
	id, ok := paramID(r, "id")
	if !ok {
		apiErr(w, http.StatusBadRequest, "validation", "bad official price id")
		return
	}
	var req domain.ApplyPriceReq
	if !decodeBody(w, r, &req) {
		return
	}
	if req.OfferID <= 0 {
		apiErr(w, http.StatusBadRequest, "validation", "offerId is required")
		return
	}
	q, err := s.st.GetOfficialPrice(id)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	of, err := s.st.GetOffer(req.OfferID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if of.OverridePrice && !req.ConfirmOverride {
		apiErr(w, http.StatusConflict, "override_required",
			"该供给源已手工覆盖报价(override_price);应用官方价会覆盖手工价,需显式确认")
		return
	}
	settings, err := s.st.GetSettings()
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	in, out, cache, err := convertPrice(q, settings.DisplayCurrency, settings.USDPerCNY)
	if err != nil {
		apiErr(w, http.StatusBadRequest, "no_rate", err.Error())
		return
	}
	if err := s.st.ApplyOfficialPrice(req.OfferID, q, in, out, cache); err != nil {
		writeStoreErr(w, err)
		return
	}
	updated, err := s.st.GetOffer(req.OfferID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// convertPrice 官方原币价 → 计价币种金额(每百万 token)。
// 折算口径与计费链路共用 pricing.Convert,避免两处漂移。
func convertPrice(q domain.OfficialPriceRow, target domain.Currency, usdPerCNY float64) (in, out, cache float64, err error) {
	in, err = pricing.Convert(q.InputPrice, q.Currency, target, usdPerCNY)
	if err != nil {
		return 0, 0, 0, err
	}
	out, err = pricing.Convert(q.OutputPrice, q.Currency, target, usdPerCNY)
	if err != nil {
		return 0, 0, 0, err
	}
	cache, err = pricing.Convert(q.CacheReadPrice, q.Currency, target, usdPerCNY)
	if err != nil {
		return 0, 0, 0, err
	}
	return in, out, cache, nil
}

// officialPriceViews 批量组装读视图(换算 + 已应用 offer 标注)。
func (s *Server) officialPriceViews(p domain.Provider, rows []domain.OfficialPriceRow) []domain.OfficialPriceView {
	// 已应用标注:遍历全部 offer,按 (provider, 来源URL, 抓取时间) 归并。
	applied := s.appliedOfferIndex()
	out := make([]domain.OfficialPriceView, 0, len(rows))
	for _, q := range rows {
		v := s.officialPriceView(q)
		v.AppliedOfferIDs = applied[applyKey(q.Provider, q.SourceURL, formatStamp(q.FetchedAt))]
		if v.AppliedOfferIDs == nil {
			v.AppliedOfferIDs = []int64{}
		}
		out = append(out, v)
	}
	return out
}

// officialPriceView 单行读视图(换算三价 + 可用标记)。
func (s *Server) officialPriceView(q domain.OfficialPriceRow) domain.OfficialPriceView {
	v := domain.OfficialPriceView{OfficialPriceRow: q, AppliedOfferIDs: []int64{}}
	target, rate := domain.CurrencyCNY, 0.0
	if st, err := s.st.GetSettings(); err == nil {
		target, rate = st.DisplayCurrency, st.USDPerCNY
	}
	if in, out, cache, err := convertPrice(q, target, rate); err == nil {
		v.InputPriceUsd, v.OutputPriceUsd, v.CacheReadPriceUsd = in, out, cache
		v.RateSet = true // 金额已按计价币种给出(同币种,或已按汇率折算)
	}
	return v
}

// appliedOfferIndex 建立「已应用来源」索引:key = provider|来源URL|抓取时间 → offer id 列表。
func (s *Server) appliedOfferIndex() map[string][]int64 {
	idx := map[string][]int64{}
	if _, err := s.st.ListAllOfficialPrices(); err != nil {
		return idx
	}
	// 逐模型取 offer(全量模型数有限,管理面读路径可接受)。
	models, err := s.st.ListModels()
	if err != nil {
		return idx
	}
	for _, m := range models {
		offers, err := s.st.ListModelOffers(m.ID)
		if err != nil {
			continue
		}
		for _, o := range offers {
			if o.PriceSourceURL == "" {
				continue
			}
			k := applyKey(o.Provider, o.PriceSourceURL, o.PriceFetchedAt)
			idx[k] = append(idx[k], o.ID)
		}
	}
	return idx
}

// applyKey 归一化「已应用」匹配键。抓取时间在不同路径下格式(纳秒/秒)可能不同,统一到秒。
func applyKey(p domain.Provider, url, fetchedAt string) string {
	return string(p) + "|" + url + "|" + normStamp(fetchedAt)
}

// formatStamp RFC3339Nano → 秒级;供 applyKey 归一。
func formatStamp(t time.Time) string { return normStamp(t.UTC().Format(time.RFC3339Nano)) }

func normStamp(s string) string {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UTC().Truncate(time.Second).Format(time.RFC3339)
	}
	return strings.TrimSpace(s)
}

// round6 保留 6 位小数(与 pricing 包同口径)。
func round6(v float64) float64 { return pricing.Round6(v) }
