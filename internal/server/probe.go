package server

import (
	"net/http"
	"strings"
	"time"

	"personal-ai-gateway/internal/domain"
	"personal-ai-gateway/internal/engine"
)

// 令牌自检(issue #8 P1):POST /api/v1/tokens/{id}/probe
//
// 回答「这个 key 现在能不能用某个模型」,而**不产生一次真实调用与计费**。
// 今天要知道答案只能真发一次请求,再对着上游风格的报错猜。
//
// 只做本地静态判定(与数据面 tokenGateErr/modelAllowed 同源),不访问上游、不计费、不吃 RPM。

// handleTokenProbe 自检指定令牌对某模型的可用性。body: {"model": "claude-sonnet-5"}。
// 令牌越权规则同 loadManageableToken:admin 任意,user 仅自己名下(否则 404)。
func (s *Server) handleTokenProbe(w http.ResponseWriter, r *http.Request) {
	tr, ok := s.loadManageableToken(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		Model string `json:"model"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		apiErr(w, http.StatusBadRequest, "validation", "model is required")
		return
	}

	checks := make([]domain.ProbeCheck, 0, 6)
	pass := func(name, detail string) {
		checks = append(checks, domain.ProbeCheck{Name: name, Ok: true, Detail: detail})
	}
	fail := func(name, detail string) {
		checks = append(checks, domain.ProbeCheck{Name: name, Ok: false, Detail: detail})
	}

	// 1) 令牌状态
	if tr.Status == domain.TokenDisabled {
		fail("令牌状态", "已停用")
	} else {
		pass("令牌状态", "正常")
	}

	// 2) 有效期
	if tr.ExpiresAt != nil && *tr.ExpiresAt != "" {
		if tm, ok := parseDateLoose(*tr.ExpiresAt); ok && time.Now().After(tm) {
			fail("有效期", "已于 "+*tr.ExpiresAt+" 过期")
		} else {
			pass("有效期", "至 "+*tr.ExpiresAt)
		}
	} else {
		pass("有效期", "永不过期")
	}

	// 3) 模型是否在允许名单内(与数据面同源:请求名 / 统一名 / 真实名任一命中)
	if s.modelAllowedForToken(tr.AllowedModels, model) {
		pass("模型授权", "该令牌允许访问 "+model)
	} else {
		fail("模型授权", "不在该令牌的允许名单内")
	}

	// 4) 模型是否在网关目录中且可路由(启用 + 有启用渠道)
	if m, err := s.st.GetModelByPublicName(model); err == nil {
		if !m.Enabled {
			fail("模型可用性", "模型已下架")
		} else {
			offers, _ := s.st.ListModelOffers(m.ID)
			live := 0
			for _, o := range offers {
				if o.Enabled && o.Status != domain.StatusDisabled {
					live++
				}
			}
			if live == 0 {
				fail("模型可用性", "该模型暂无可用渠道")
			} else {
				pass("模型可用性", "有可用渠道")
			}
		}
	} else {
		fail("模型可用性", "网关目录中没有该模型")
	}

	// 5) 令牌额度(子预算)
	if tr.QuotaUsd > 0 && tr.UsedUsd >= tr.QuotaUsd {
		fail("令牌额度", "本令牌额度已用尽")
	} else if tr.QuotaUsd > 0 {
		pass("令牌额度", "剩余额度充足")
	} else {
		pass("令牌额度", "不限额")
	}

	// 6) 账户余额(主闸):仅客户账号有钱包。
	if tr.OwnerID != nil {
		if u, _, err := s.st.AdminByID(*tr.OwnerID); err == nil && u.Role == domain.RoleUser {
			bal, _ := s.st.GetBalance(u.ID)
			if bal <= 0 {
				fail("账户余额", "余额已耗尽,请充值")
			} else {
				pass("账户余额", "余额充足")
			}
		} else {
			pass("账户余额", "管理员/全局令牌不受钱包限制")
		}
	} else {
		pass("账户余额", "全局令牌不受钱包限制")
	}

	allOk := true
	for _, c := range checks {
		if !c.Ok {
			allOk = false
			break
		}
	}
	writeJSON(w, http.StatusOK, domain.TokenProbeResp{Model: model, Ok: allOk, Checks: checks})
}

// modelAllowedForToken 令牌 allowed_models 判定,与 proxy.Gateway.modelAllowed 同源:
// 请求名命中,或解析出模型后按统一名/真实名命中(重命名前后都认)。
func (s *Server) modelAllowedForToken(allowed []string, model string) bool {
	if engine.SupportsModel(allowed, model) {
		return true
	}
	if m, err := s.st.GetModelByPublicName(model); err == nil {
		return engine.SupportsModel(allowed, m.PublicName()) || engine.SupportsModel(allowed, m.Name)
	}
	return false
}
