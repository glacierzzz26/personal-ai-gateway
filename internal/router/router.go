// Package router 维护"规范模型名 → 可用上游"的候选集合,
// 提供优先级排序、健康过滤与熔断(连续失败后冷却)。
// 上游列表在运行期可整体更换(Apply,管理 API 增删改订阅源时调用):
// 换的是指针切片,已在飞的请求持有的旧指针不会被原地改写,天然无数据竞争。
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
	mu   sync.RWMutex // 保护 ups / circ 字段访问
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

// Apply 整体更换上游集合(管理 API 增删改后的落地)。不做原地修改:
// 每个上游建新指针,因此正被某个在飞请求持有的旧指针仍安全可读。
// 同名存活者的 Circuit 对象被携带 —— 只改某一家时,别家的熔断状态不受影响。
func (r *Router) Apply(ups []config.Upstream) {
	r.mu.Lock()
	ns := make([]*config.Upstream, len(ups))
	ncirc := make(map[string]*Circuit, len(ups))
	for i := range ups {
		u := &ups[i]
		ns[i] = u
		if c := r.circ[u.Name]; c != nil {
			ncirc[u.Name] = c
		} else {
			ncirc[u.Name] = &Circuit{}
		}
	}
	r.ups = ns
	r.circ = ncirc
	r.mu.Unlock()

	// 剪掉已被移除名字的配额快照,避免"删了又建同名"留下 stale-hard。
	r.qmu.Lock()
	keep := make(map[string]bool, len(ups))
	for i := range ups {
		keep[ups[i].Name] = true
	}
	for name := range r.q {
		if !keep[name] {
			delete(r.q, name)
		}
	}
	r.qmu.Unlock()
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
	r.mu.RLock()
	var out []item
	for _, u := range r.ups {
		if Supports(u, model) && !r.circOpenLocked(u.Name) {
			out = append(out, item{up: u, hard: r.quotaHard(u.Name)})
		}
	}
	r.mu.RUnlock()

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

// All 返回全部上游的拷贝(配置序),调用方可安全持有。
func (r *Router) All() []*config.Upstream {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*config.Upstream, len(r.ups))
	copy(out, r.ups)
	return out
}

// HasAny 判断是否至少有一个上游能出该模型(不管协议与健康状态)。
func (r *Router) HasAny(model string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, u := range r.ups {
		if Supports(u, model) {
			return true
		}
	}
	return false
}

// healthy 报告熔断是否关闭(该名字可被选路)。不在册视为不可用。
func (r *Router) healthy(name string) bool {
	r.mu.RLock()
	c := r.circ[name]
	r.mu.RUnlock()
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Now().After(c.openUntil)
}

// circOpenLocked 调用方需已持有 mu(读)。返回 true = 熔断打开或不在册。
func (r *Router) circOpenLocked(name string) bool {
	c := r.circ[name]
	if c == nil {
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return !time.Now().After(c.openUntil)
}

func (r *Router) RecordSuccess(name string) {
	r.mu.RLock()
	c := r.circ[name]
	r.mu.RUnlock()
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
	r.mu.RLock()
	c := r.circ[u.Name]
	r.mu.RUnlock()
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
