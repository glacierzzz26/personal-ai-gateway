package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"personal-ai-gateway/internal/domain"
	"personal-ai-gateway/internal/proxy"
)

// 渠道额度查询是**逐渠道打上游外网 HTTP**(每家 6s 超时,见 proxy.quotaTimeout),
// 而首页每 15s 自动刷新、渠道页对每个渠道各发一次 —— 不设缓冲就会把上游打成密集轮询,
// 且每次都要等一整个 6s。故在管理面加一层**网关级短 TTL 缓存 + 在途去重**:
//   - 同一渠道的并发调用只打一次上游(在途去重,in-flight dedup);
//   - 成功结果缓存 quotaCacheTTL,失败结果缓存 quotaFailTTL(短一些,故障期不至于疯狂重试);
//   - 调用方放弃(客户端断开 / 批量整体超时)的那次**不入缓存**,下次重新打。
//
// 逐渠道(渠道页)与批量(首页)两条路径共用此原语,因此两边看到的是同一份缓存、同一套口径。
const (
	quotaCacheTTL   = 60 * time.Second
	quotaFailTTL    = 20 * time.Second
	quotaBatchLimit = 4                // 批量查询的最大并发(上游是外网,不宜全开)
	quotaBatchCap   = 10 * time.Second // 单次批量查询的整体上限
)

// quotaEntry 一个渠道的一次额度查询(缓存项或在途中的占位)。
// expires 零值表示**仍在途**(尚未完成),此时后来者应等 ready 而不是再打一次上游。
type quotaEntry struct {
	mu      sync.Mutex
	ready   chan struct{}
	resp    domain.ChannelQuotaResp
	expires time.Time
}

func (e *quotaEntry) result() domain.ChannelQuotaResp {
	<-e.ready
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.resp
}

// usable 是否可直接复用(在途 → 等它;未过期 → 直接给)。在途返回 inflight=true。
func (e *quotaEntry) usable(now time.Time) (inflight, fresh bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.expires.IsZero() {
		return true, false
	}
	return false, now.Before(e.expires)
}

func (e *quotaEntry) done(resp domain.ChannelQuotaResp, expires time.Time) {
	e.mu.Lock()
	e.resp = resp
	e.expires = expires
	e.mu.Unlock()
	close(e.ready)
}

// channelQuota 取某渠道额度(带短 TTL 缓存与在途去重)。第二个返回值表示是否命中缓存。
// 命中(含在途等待)不消耗上游请求;未命中则打一次上游并按结果设定 TTL。
func (s *Server) channelQuota(ctx context.Context, ch domain.ChannelRow) (domain.ChannelQuotaResp, bool) {
	now := time.Now()
	s.qmu.Lock()
	e, ok := s.qcache[ch.ID]
	if ok {
		if inflight, fresh := e.usable(now); inflight || fresh {
			s.qmu.Unlock()
			return e.result(), true
		}
		// 已过期:在同一临界区内换上新项,避免并发的第二个调用者也去建新项而重复打上游。
		delete(s.qcache, ch.ID)
	}
	e = &quotaEntry{ready: make(chan struct{})}
	s.qcache[ch.ID] = e
	s.qmu.Unlock()

	resp := s.fetchChannelQuota(ctx, ch)
	var expires time.Time
	if ctx.Err() != nil {
		// 调用方已放弃 —— 这不算渠道故障,不入缓存,下次请求重新打上游。
		s.qmu.Lock()
		if s.qcache[ch.ID] == e {
			delete(s.qcache, ch.ID)
		}
		s.qmu.Unlock()
	} else {
		ttl := quotaCacheTTL
		if !resp.Available {
			ttl = quotaFailTTL
		}
		expires = time.Now().Add(ttl)
	}
	e.done(resp, expires)
	return resp, false
}

// fetchChannelQuota 真正打一次上游并组装响应(含 errorKind 归类)。**不做缓存**。
func (s *Server) fetchChannelQuota(ctx context.Context, ch domain.ChannelRow) domain.ChannelQuotaResp {
	settings, err := s.st.GetSettings()
	if err != nil {
		return quotaFetchFail("读取网关设置失败: "+err.Error(), 0)
	}
	res, err := s.rl.FetchChannelQuota(ctx, s.rl.Client(settings, 0), ch)
	resp := domain.ChannelQuotaResp{Available: true, Windows: map[string]domain.QuotaWindow{}, PlanName: res.PlanName, LatencyMs: res.LatencyMs}
	if err != nil {
		resp.Available = false
		// 用 errors.Is 归类而非匹配文案 —— 前端据此区分「查不了」(常态)与「查失败」(真故障)。
		switch {
		case errors.Is(err, proxy.ErrQuotaNotConfigured):
			resp.ErrorKind, resp.Error = domain.QuotaErrNotConfigured, "该渠道未配置额度查询路径"
		case errors.Is(err, proxy.ErrQuotaUnsupported):
			resp.ErrorKind, resp.Error = domain.QuotaErrUnsupported, "该渠道类型没有已知的额度接口"
		default:
			resp.ErrorKind, resp.Error = domain.QuotaErrFetch, err.Error()
		}
		return resp
	}
	if res.Windows != nil {
		resp.Windows = res.Windows
	}
	resp.Balance = res.Balance
	return resp
}

// quotaFetchFail 构造一个「查询失败」占位响应(真故障,会参与首页告警判定)。
func quotaFetchFail(msg string, latencyMs int64) domain.ChannelQuotaResp {
	return domain.ChannelQuotaResp{
		Available: false,
		Windows:   map[string]domain.QuotaWindow{},
		LatencyMs: latencyMs,
		Error:     msg,
		ErrorKind: domain.QuotaErrFetch,
	}
}

// quotaNeedsConfig 第三方(含空类型兜底行)没有统一额度协议,必须手工配 quota_path;
// 没配就不该发请求,直接给「未配置」占位 —— 与前端 QuotaCell 的 quotaEnabled 同判据。
func quotaNeedsConfig(ch domain.ChannelRow) bool {
	thirdPartyish := ch.ChannelType == domain.ChannelTypeThirdParty || ch.ChannelType == ""
	return thirdPartyish && strings.TrimSpace(ch.QuotaPath) == ""
}

// quotaNotConfiguredResp 「未配置」占位(常态,不是故障,不参与告警)。
func quotaNotConfiguredResp() domain.ChannelQuotaResp {
	return domain.ChannelQuotaResp{
		Available: false,
		Windows:   map[string]domain.QuotaWindow{},
		Error:     "该渠道未配置额度查询路径",
		ErrorKind: domain.QuotaErrNotConfigured,
	}
}

// batchQuotaItem 批量额度响应的一个元素:渠道 id + 其额度(形状与单渠道端点完全一致)。
type batchQuotaItem struct {
	ID    int64                   `json:"id"`
	Quota domain.ChannelQuotaResp `json:"quota"`
}

// handleChannelsQuotaList 批量渠道额度(首页「运行总览」用):一次拿全渠道额度,
// 服务端并发拉 + 共用短 TTL 缓存。首页每 15s 刷新,若不缓存则每轮都产生 N 个最长 6s 的上游请求,
// 既打上游又拖首屏;有了缓存,稳态下每渠道每 60s 至多被问一次。
//
// 并发上限 quotaBatchLimit、整体上限 quotaBatchCap;超时的渠道由 ctx 取消 → 回「查询失败」,
// 不会让个别慢上游把整个首页拖住。
func (s *Server) handleChannelsQuotaList(w http.ResponseWriter, r *http.Request) {
	rows, err := s.st.ListChannels()
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), quotaBatchCap)
	defer cancel()

	out := make([]batchQuotaItem, len(rows))
	sem := make(chan struct{}, quotaBatchLimit)
	var wg sync.WaitGroup
	for i, ch := range rows {
		wg.Add(1)
		go func(i int, ch domain.ChannelRow) {
			defer wg.Done()
			item := batchQuotaItem{ID: ch.ID}
			switch {
			case quotaNeedsConfig(ch):
				item.Quota = quotaNotConfiguredResp()
			case ctx.Err() != nil:
				item.Quota = quotaFetchFail("批量查询超时", 0)
			default:
				sem <- struct{}{}
				q, _ := s.channelQuota(ctx, ch)
				<-sem
				item.Quota = q
			}
			out[i] = item
		}(i, ch)
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, out)
}
