package server

import (
	"personal-ai-gateway/internal/domain"
	"personal-ai-gateway/internal/pricing"
)

// 用户面模型清单(/api/v1/models,role=user)。
//
// 与管理员视角的差别(见 PLAN.md §5):客户只需要知道「我能用哪些模型、每个多少钱」,
// 不该看到渠道名、每个供给源的上游真实名、官方价来源 URL,以及**全站**调用量
// (那是别人的生意数据)。因此这里另起一套结构,而不是给 ModelRead 打码 ——
// 打码式实现一旦漏字段就是泄漏,独立结构默认只带显式列出的字段。

// UserModelView 客户可见的模型条目。
type UserModelView struct {
	Name          string              `json:"name"`
	ContextWindow int                 `json:"contextWindow"`
	Capabilities  []domain.Capability `json:"capabilities"`
	// Official 官方价锚(划线原价,计价币种,每百万 token);未录官方价时为空。
	Official *UserPrice `json:"official,omitempty"`
	// Retail 本站价 = 官方价 × 倍率(客户实付口径);无官方价时为空。
	Retail *UserPrice `json:"retail,omitempty"`
	// PeakVaries 该模型分时计价(峰谷两档价不同)。前端据此决定是否渲染第二行价格。
	PeakVaries bool `json:"peakVaries,omitempty"`
	// PeakRetail 高峰档本站价;仅 PeakVaries 时有值。客户在页面上同时看到谷/峰两价 ——
	// 报价是「现在买多少钱」,而计费按请求时刻选档,不并列展示就会出现「看到谷价、
	// 恰在峰时段请求、被按峰价收费」的争议(见 PLAN.md §5)。
	PeakRetail *UserPrice `json:"peakRetail,omitempty"`
	// PeakHours 峰时段的人读说明(厂商原文,如「北京时间周一至周五 9:00-12:00、14:00-18:00」)。
	PeakHours string `json:"peakHours,omitempty"`
	// PriceNote 价格不可用时的说明(未录官方价 / 未设汇率),供前端提示。
	PriceNote string `json:"priceNote,omitempty"`
}

// UserPrice 一组每百万 token 的三价(计价币种)。
type UserPrice struct {
	Input     float64 `json:"input"`
	Output    float64 `json:"output"`
	CacheRead float64 `json:"cacheRead"`
	Currency  string  `json:"currency"`
}

// userModelsList 客户可见模型 = 可用(启用 ∩ 有启用供给源 ∩ 渠道启用) ∩ 允许名单;
// 价格取官方价 × 倍率,倍率按模型定(全站同模型同价,与归属用户无关)。
// allowed 为令牌的 allowed_models(空 = 不过滤,仅用于无令牌的登录态预览)。
func (s *Server) userModelsList(allowed []string) ([]UserModelView, error) {
	settings, err := s.st.GetSettings()
	if err != nil {
		return nil, err
	}
	models, err := s.st.ListModels()
	if err != nil {
		return nil, err
	}
	// 与数据面同口径的「可调用」集合:光 m.Enabled 不够 —— 模型启用但供给源/渠道被停用时,
	// 数据面会因 EnabledModelsWithOffers 过滤掉它,用户面若仍列出就是「看得见调不通」。
	usable, err := s.st.UsableModelIDs()
	if err != nil {
		return nil, err
	}
	out := make([]UserModelView, 0, len(models))
	for _, m := range models {
		if !m.Enabled || !usable[m.ID] {
			continue
		}
		name := m.PublicName()
		if !allowedName(allowed, name, m.Name) {
			continue
		}
		// 倍率:该模型覆盖 ?? 全局(与 proxy.chargeUsd 同源,避免展示与结算漂移)。
		rate := settings.PriceMultiplier
		if m.RateOverride != nil {
			rate = *m.RateOverride
		}
		v := UserModelView{
			Name: name, ContextWindow: m.ContextWindow, Capabilities: m.Capabilities,
		}
		if q, err := s.st.GetOfficialPriceByName(m.OfficialVendor, m.OfficialModelName); err == nil {
			cur := string(settings.DisplayCurrency)
			// 分时模型:定价按档位算出「谷价 / 峰价」两行**并列**展示,不让客户只看到谷价
			// (报价是「多少钱」,计费按请求时刻选档;只给单值会出现「看到谷价、恰在峰时段
			// 请求被按峰价收费」的争议,事后无法解释)。
			//
			// 为什么不用 RetailPrice(此刻):它按当前时钟选档,同一模型同一份列表在
			// 9:00 和 13:00 刷出两个不同的「本站价」,客户截图对不上账。这里把档位显式拆开,
			// 展示面不再依赖「什么时候看的」。
			if off, peak, ok := pricing.PeakOffpeakTriples(q); ok {
				oi, oo, oc := mustConvert(off.In, q, settings), mustConvert(off.Out, q, settings), mustConvert(off.CacheRead, q, settings)
				pi, po, pc := mustConvert(peak.In, q, settings), mustConvert(peak.Out, q, settings), mustConvert(peak.CacheRead, q, settings)
				v.Official = &UserPrice{Input: oi, Output: oo, CacheRead: oc, Currency: cur}
				v.Retail = &UserPrice{
					Input: pricing.Round6(oi * rate), Output: pricing.Round6(oo * rate),
					CacheRead: pricing.Round6(oc * rate), Currency: cur,
				}
				v.PeakVaries = true
				v.PeakRetail = &UserPrice{
					Input: pricing.Round6(pi * rate), Output: pricing.Round6(po * rate),
					CacheRead: pricing.Round6(pc * rate), Currency: cur,
				}
				v.PeakHours = pricing.PeakHoursText(q)
				out = append(out, v)
				continue
			}
			// 其余形态(平坦/阶梯/折扣):单一价,取标量三价换算 —— 与改造前逐位一致。
			if _, err := pricing.Convert(q.InputPrice, q.Currency, settings.DisplayCurrency, settings.USDPerCNY); err != nil {
				v.PriceNote = "官方价币种与计价币种不一致，请管理员在【系统设置】填写汇率后显示"
			} else {
				v.Official = &UserPrice{
					Input:     mustConvert(q.InputPrice, q, settings),
					Output:    mustConvert(q.OutputPrice, q, settings),
					CacheRead: mustConvert(q.CacheReadPrice, q, settings),
					Currency:  cur,
				}
				in, outP, cache := v.Official.Input, v.Official.Output, v.Official.CacheRead
				v.Retail = &UserPrice{
					Input: pricing.Round6(in * rate), Output: pricing.Round6(outP * rate),
					CacheRead: pricing.Round6(cache * rate), Currency: cur,
				}
			}
		} else {
			v.PriceNote = "该模型尚未录入官方参考价，价格待定"
		}
		out = append(out, v)
	}
	return out, nil
}

// mustConvert 官方原币 → 计价币种;失败返回 0(调用方已按 ErrNoExchangeRate 分支处理)。
func mustConvert(price float64, q domain.OfficialPriceRow, settings domain.Settings) float64 {
	v, err := pricing.Convert(price, q.Currency, settings.DisplayCurrency, settings.USDPerCNY)
	if err != nil {
		return 0
	}
	return v
}

// allowedName 令牌允许名单匹配:对外名或真实名任一命中即可(与数据面校验同规矩)。
func allowedName(allowed []string, publicName, originName string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, a := range allowed {
		if a == "*" || a == publicName || a == originName {
			return true
		}
	}
	return false
}
