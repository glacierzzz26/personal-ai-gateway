// Package router 维护"规范模型名 → 可用上游"的候选集合,
// 提供优先级排序、健康过滤与熔断(连续失败后冷却)。
package router

import (
	"sort"
	"strings"
	"sync"
	"time"

	"personal-ai-gateway/internal/config"
	"personal-ai-gateway/internal/quota"
)

type Circuit struct {
	mu        sync.Mutex
	fails     int
	openUntil time.Time
}

type Router struct {
	ups  []*config.Upstream
	circ map[string]*Circuit
	qmu  sync.RWMutex
	q    map[string]quota.Snapshot // 配额感知选路状态(见 SetQuota)
}

func New(ups []config.Upstream) *Router {
	r := &Router{
		circ: make(map[string]*Circuit, len(ups)),
		q:    make(map[string]quota.Snapshot, len(ups)),
	}
	for i := range ups {
		u := ups[i]
		r.ups = append(r.ups, &u)
		r.circ[u.Name] = &Circuit{}
	}
	return r
}

// Candidates 返回能提供 model、且当前未熔断的上游。
// 配额感知选路(软偏好):
//   - 配额状态正常(未达 hard)的上游按 priority 升序排前;
//   - 配额已 hard(≥hard_used_pct 或 status!=ok)的上游整体排后,作为"尽力而为"备选;
//   - 若全部候选都 hard,则照常全量返回(不想让请求因配额直接失败)。
// priority 相同则保持配置顺序。熔断(挂了)仍是硬排除,与配额解耦。
func (r *Router) Candidates(model string) []*config.Upstream {
	type item struct {
		up   *config.Upstream
		hard bool
	}
	var out []item
	for _, u := range r.ups {
		if Supports(u, model) && r.healthy(u.Name) {
			out = append(out, item{up: u, hard: r.quotaHard(u.Name)})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].hard != out[j].hard {
			return !out[i].hard // 非 hard 在前
		}
		return out[i].up.Priority < out[j].up.Priority
	})
	res := make([]*config.Upstream, len(out))
	for i, it := range out {
		res[i] = it.up
	}
	return res
}

// SetQuota 由配额管理器推送最新快照;并发安全,候选查询实时生效。
func (r *Router) SetQuota(name string, s quota.Snapshot) {
	r.qmu.Lock()
	defer r.qmu.Unlock()
	if r.q == nil {
		r.q = map[string]quota.Snapshot{}
	}
	r.q[name] = s
}

// QuotaState 返回某上游当前配额状态(用于 /api 展示)。未启用配额或从未成功拉取则 ok=false。
func (r *Router) QuotaState(name string) (quota.Snapshot, bool) {
	r.qmu.RLock()
	defer r.qmu.RUnlock()
	s, ok := r.q[name]
	return s, ok
}

func (r *Router) quotaHard(name string) bool {
	r.qmu.RLock()
	defer r.qmu.RUnlock()
	s, ok := r.q[name]
	return ok && s.Hard
}

// Supports 判断上游是否声称能出 model。
// models 为空或含 "*" = 全部;支持前缀通配,如 "claude-*"。
func Supports(u *config.Upstream, model string) bool {
	if len(u.Models) == 0 {
		return true
	}
	for _, p := range u.Models {
		switch {
		case p == "*" || p == model:
			return true
		case strings.HasSuffix(p, "*"):
			if strings.HasPrefix(model, strings.TrimSuffix(p, "*")) {
				return true
			}
		}
	}
	return false
}

// All 返回全部上游(配置序),用于模型清单等只读场景。
func (r *Router) All() []*config.Upstream { return r.ups }

// HasAny 判断是否至少有一个上游能出该模型(不管协议与健康状态)。
func (r *Router) HasAny(model string) bool {
	for _, u := range r.ups {
		if Supports(u, model) {
			return true
		}
	}
	return false
}

func (r *Router) healthy(name string) bool {
	c := r.circ[name]
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Now().After(c.openUntil)
}

func (r *Router) RecordSuccess(name string) {
	c := r.circ[name]
	if c == nil {
		return
	}
	c.mu.Lock()
	c.fails = 0
	c.openUntil = time.Time{}
	c.mu.Unlock()
}

// RecordFailure 累计失败;达到该上游 max_failures 即打开熔断,冷却 cooldown_sec。
func (r *Router) RecordFailure(u *config.Upstream) {
	c := r.circ[u.Name]
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fails++
	if c.fails >= u.MaxFailures {
		c.openUntil = time.Now().Add(time.Duration(u.CooldownSec) * time.Second)
	}
}
