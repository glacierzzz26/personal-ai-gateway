package proxy

import (
	"encoding/json"
	"net/http"
	"strings"
)

// ListModels 提供 /v1/models:返回全部上游配置里"字面声明"的模型 id(按首次出现顺序)。
// 通配(如 "claude-*" / "*")无法枚举成具体 id,这里不展开;
// 模型探测主要靠真实请求验证,清单只做给工具看的示意。
func (g *Gateway) ListModels(w http.ResponseWriter, proto string) {
	ids := distinctModels(g)
	w.Header().Set("Content-Type", "application/json")

	if proto == ProtoAnthropic {
		data := make([]map[string]any, 0, len(ids))
		for _, id := range ids {
			data = append(data, map[string]any{
				"id":           id,
				"type":         "model",
				"display_name": id,
			})
		}
		first, last := "", ""
		if len(ids) > 0 {
			first, last = ids[0], ids[len(ids)-1]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":     data,
			"has_more": false,
			"first_id": first,
			"last_id":  last,
		})
		return
	}

	// openai 形状
	data := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		data = append(data, map[string]any{
			"id":       id,
			"object":   "model",
			"created":  0,
			"owned_by": "personal-ai-gateway",
		})
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"object": "list",
		"data":   data,
	})
}

func distinctModels(g *Gateway) []string {
	var ids []string
	seen := map[string]bool{}
	for _, u := range g.Router.All() {
		for _, p := range u.Models {
			if p == "*" || strings.HasSuffix(p, "*") {
				continue // 无法枚举
			}
			if !seen[p] {
				seen[p] = true
				ids = append(ids, p)
			}
		}
	}
	return ids
}
