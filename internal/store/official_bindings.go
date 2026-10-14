package store

import (
	"fmt"
	"strings"

	"personal-ai-gateway/internal/domain"
)

// canonicalModelKey 归并用的规范化模型名:取最后一个 '/' 之后的段,去空白,大小写归一。
// (实现见 models.go:88 —— 官方价回填与渠道同步归并共用同一套归一,避免两处跑偏。)
//
// 回填官方价绑定时对 models.name 与 official_prices.model_name 两侧同口径归一,
// 于是 `Qwen/Qwen3.8-Flash` ↔ `qwen3.8-flash` 这类前缀/大小写差异正好被吃掉。

// officialSlugKey 官方价绑定匹配用的 slug:canonicalModelKey 之上再把点号折成连字符。
//
// CC 官方价 slug 一律连字符(`deepseek-v4-1-flash`,来自 `/models/<slug>`),而 models.name
// 末段可能带点号(`deepseek/deepseek-v4.1-flash`);折叠后两者视为同一模型,否则带点号的模型名
// 永远命不中连字符 slug → **永不绑定** → 成本系数/官方价点查全落空(成本静默按 1.0)。
//
// 刻意不复用/修改 canonicalModelKey —— 它带 `rawBare` 闸门,服务渠道同步归并语义
// (见 models.go 的 GetModelByCanonical);在它里折点号会让带点号的裸名不再被当作「裸名」，
// 反而拆出重复模型。故折点号只在本文件的官方价回填匹配里做。
//
// 两侧都折(价格名与模型名),这样既能命中连字符 slug,又保住 CC 自身就用点号的 slug
// (`qwen3.6-plus`)——写回时存的仍是 verbatim 的 official_prices.model_name。
func officialSlugKey(s string) string {
	return strings.ReplaceAll(canonicalModelKey(s), ".", "-")
}

// BackfillOfficialBindings 为**未绑定**官方价的模型,按规范名匹配 official_prices 并写回绑定。
//
// 这是让「成本 = 官方价 × 渠道系数」生效的前置动作 —— 没有绑定,派生存本就无从算起。
//
// 保守闸门(宁可漏绑不可错绑):
//   - 只处理 official_vendor 与 official_model_name **都为空**的模型;任何人工绑定绝不覆盖。
//   - 只认**精确** slug 命中(见 officialSlugKey:两侧各取末段、小写、点号折连字符后须完全相等),
//     不做子串/模糊匹配 —— 归错厂商会让成本、售价、展示全盘走偏。
//   - 同一 slug 命中多个**厂商**时跳过(歧义,交人工判断)。
//
// 注意「模型侧多行命中同一官方价行」不算歧义:`Qwen/Qwen3.8-27B` 与裸名 `Qwen3.8-27B`
// 是同一模型被不同渠道以不同前缀上报,两行都该绑到同一条 `qwen3.8-27b`。
//
// dryRun=true 只算不写,供部署前人工过目。
func (s *Store) BackfillOfficialBindings(dryRun bool) ([]domain.OfficialBindingFill, error) {
	models, err := s.ListModels()
	if err != nil {
		return nil, err
	}
	prices, err := s.ListAllOfficialPrices()
	if err != nil {
		return nil, err
	}
	// slug → 该名下的厂商集合(多于一个即歧义,跳过)。
	byKey := map[string]map[domain.Provider]string{}
	for _, q := range prices {
		key := officialSlugKey(q.ModelName)
		if key == "" {
			continue
		}
		if byKey[key] == nil {
			byKey[key] = map[domain.Provider]string{}
		}
		byKey[key][q.Provider] = q.ModelName
	}

	out := []domain.OfficialBindingFill{}
	for _, m := range models {
		if m.OfficialVendor != "" || m.OfficialModelName != "" {
			continue // 已绑定(无论人工还是上一轮)——绝不覆盖
		}
		key := officialSlugKey(m.Name)
		vendors := byKey[key]
		if len(vendors) != 1 {
			continue // 无命中或多厂商歧义:跳过
		}
		var vendor domain.Provider
		var officialName string
		for p, name := range vendors {
			vendor, officialName = p, name
		}
		out = append(out, domain.OfficialBindingFill{
			ModelID: m.ID, ModelName: m.Name, Vendor: vendor, OfficialName: officialName,
		})
	}
	if dryRun || len(out) == 0 {
		return out, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := nowRFC3339()
	for _, f := range out {
		// 再次以「两列皆空」为条件,避免与并发的人工绑定抢写。
		res, err := tx.Exec(`UPDATE models SET official_vendor = ?, official_model_name = ?, updated_at = ?
			WHERE id = ? AND official_vendor = '' AND official_model_name = ''`,
			string(f.Vendor), f.OfficialName, now, f.ModelID)
		if err != nil {
			return nil, fmt.Errorf("bind model %d: %w", f.ModelID, err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, fmt.Errorf("绑定模型 %d 时目标已被改动", f.ModelID)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}
