/**
 * 服务层:全部方法映射到网关管理面 REST(会话 cookie)。
 * 页面仅调用本文件导出,不直接 import mock —— mock/db.ts 已删除。
 *
 * 数值口径:后端成功率/错误率中 successRate 用 0..100 百分数,
 * 这里统一除以 100 还原为 0..1 小数供 UI(fmt.pct)使用;errorRate 本身即小数。
 */
import type {
  AdminMe, Channel, ChannelDraft, ChannelTestResult, GatewayToken, LogFilters,
  LogPage, MatchMode, MetricPoint, ModelCatalogItem, ModelDraft, ModelOffer,
  ModelUsageData, OfferDraft, OverviewData, RequestLogItem, RouteRule, RuleDraft,
  Settings, SyncResult, TokenCreateResult, TokenDraft, UsageDim, UsageRow,
} from '@/types';
import { http } from './http';

/** 后端返回的 0..100 → UI 的 0..1 */
const frac = (pct: number): number => (pct == null ? 0 : pct / 100);

function ch(x: Channel): Channel { return { ...x, successRate: frac(x.successRate) }; }
function offer(o: ModelOffer): ModelOffer { return { ...o, successRate: frac(o.successRate) }; }
function model(m: ModelCatalogItem): ModelCatalogItem {
  return { ...m, offers: m.offers.map(offer), successRate: frac(m.successRate) };
}

function qs(params?: Record<string, string | number | undefined>): string {
  if (!params) return '';
  const parts = Object.entries(params)
    .filter(([, v]) => v !== undefined && v !== '')
    .map(([k, v]) => `${encodeURIComponent(k)}=${encodeURIComponent(String(v))}`);
  return parts.length ? `?${parts.join('&')}` : '';
}

export const api = {
  /* —— 账号会话 —— */
  authState(): Promise<{ adminExists: boolean }> { return http.get('/auth/state'); },
  login(username: string, password: string): Promise<AdminMe> { return http.post('/auth/login', { username, password }); },
  bootstrap(username: string, password: string): Promise<AdminMe> { return http.post('/auth/bootstrap', { username, password }); },
  logout(): Promise<unknown> { return http.post('/auth/logout'); },
  me(): Promise<AdminMe> { return http.get('/auth/me'); },

  /* —— 概览 —— */
  getOverview(): Promise<OverviewData> { return http.get('/overview'); },

  /* —— 渠道 —— */
  async getChannels(): Promise<Channel[]> {
    return (await http.get<Channel[]>('/channels')).map(ch);
  },
  createChannel(body: ChannelDraft): Promise<Channel> { return http.post<Channel>('/channels', body).then(ch); },
  updateChannel(id: number, body: ChannelDraft): Promise<Channel> {
    return http.patch<Channel>(`/channels/${id}`, body).then(ch);
  },
  deleteChannel(id: number): Promise<unknown> { return http.del(`/channels/${id}`); },
  testChannel(id: number): Promise<ChannelTestResult> { return http.post(`/channels/${id}/test`); },
  syncModels(id: number): Promise<SyncResult> { return http.post(`/channels/${id}/sync-models`); },

  /* —— 模型广场 —— */
  async getModels(): Promise<ModelCatalogItem[]> {
    return (await http.get<ModelCatalogItem[]>('/models')).map(model);
  },
  createModel(body: ModelDraft): Promise<ModelCatalogItem> { return http.post<ModelCatalogItem>('/models', body).then(model); },
  updateModel(id: number, body: ModelDraft): Promise<ModelCatalogItem> {
    return http.patch<ModelCatalogItem>(`/models/${id}`, body).then(model);
  },
  deleteModel(id: number): Promise<unknown> { return http.del(`/models/${id}`); },
  getModelUsage(id: number, days = 7): Promise<ModelUsageData> {
    return http.get(`/models/${id}/usage${qs({ days })}`);
  },
  async toggleModel(modelId: number, enabled: boolean): Promise<void> {
    const m = (await api.getModels()).find(x => x.id === modelId);
    if (!m) throw new Error('模型不存在');
    await api.updateModel(modelId, {
      name: m.name, contextWindow: m.contextWindow,
      capabilities: m.capabilities, enabled,
    });
  },

  /* —— 供给源 —— */
  createOffer(modelId: number, body: OfferDraft): Promise<ModelOffer> {
    return http.post<ModelOffer>(`/models/${modelId}/offers`, body).then(offer);
  },
  updateOffer(id: number, body: OfferDraft): Promise<ModelOffer> {
    return http.patch<ModelOffer>(`/offers/${id}`, body).then(offer);
  },
  deleteOffer(id: number): Promise<unknown> { return http.del(`/offers/${id}`); },
  reorderOffers(modelId: number, from: number, insertAt: number): Promise<ModelOffer[]> {
    return http.put<ModelOffer[]>(`/models/${modelId}/offers/order`, { from, insertAt }).then(list => list.map(offer));
  },
  /** 设为首选:把指定 offer 拖到队首 */
  async setPrimaryOffer(modelId: number, offerId: number): Promise<void> {
    const list = await api.getModels().then(xs => xs.find(m => m.id === modelId)?.offers ?? []);
    const from = list.findIndex(o => o.id === offerId);
    if (from > 0) await api.reorderOffers(modelId, from, 0);
  },
  /** 供给源启停(PATCH 是全量更新,须带上完整报价快照) */
  async toggleOffer(_modelId: number, offerId: number, enabled: boolean): Promise<void> {
    const list = await api.getModels();
    const o = list.flatMap(m => m.offers).find(x => x.id === offerId);
    if (!o) throw new Error('供给源不存在');
    await api.updateOffer(offerId, api.offerDraft(o, enabled));
  },
  /** 供给源编辑全量草稿(基于当前快照) */
  offerDraft(o: ModelOffer, enabled = o.enabled): OfferDraft {
    return {
      channelId: o.channelId,
      inputPriceUsd: o.inputPriceUsd,
      outputPriceUsd: o.outputPriceUsd,
      cacheReadPriceUsd: o.cacheReadPriceUsd ?? 0,
      overridePrice: o.overridePrice,
      rateLimitRpm: o.rateLimitRpm,
      enabled,
      note: o.note,
    };
  },

  /* —— 令牌 —— */
  getTokens(): Promise<GatewayToken[]> { return http.get('/tokens'); },
  createToken(body: TokenDraft): Promise<TokenCreateResult> { return http.post('/tokens', body); },
  updateToken(id: number, body: TokenDraft): Promise<GatewayToken> { return http.patch(`/tokens/${id}`, body); },
  deleteToken(id: number): Promise<unknown> { return http.del(`/tokens/${id}`); },

  /* —— 路由规则 —— */
  getRules(): Promise<RouteRule[]> { return http.get('/rules'); },
  createRule(body: RuleDraft): Promise<RouteRule> { return http.post('/rules', body); },
  updateRule(id: number, body: RuleDraft): Promise<RouteRule> { return http.patch(`/rules/${id}`, body); },
  deleteRule(id: number): Promise<unknown> { return http.del(`/rules/${id}`); },
  reorderRules(from: number, insertAt: number): Promise<RouteRule[]> {
    return http.put('/rules/order', { from, insertAt });
  },

  /* —— 日志 —— */
  getLogs(filters: LogFilters = {}, page = 1, size = 20): Promise<LogPage> {
    const p = {
      model: filters.model, channel: filters.channel, token: filters.token,
      status: filters.status, kw: filters.kw,
      page, size,
    };
    return http.get(`/logs${qs(p)}`);
  },
  /** 最近若干条(Dashboard 用) */
  async recentLogs(n = 10): Promise<RequestLogItem[]> {
    return (await api.getLogs({}, 1, n)).items;
  },
  clearLogs(): Promise<unknown> { return http.del('/logs'); },

  /* —— 用量 —— */
  getUsage(dim: UsageDim, days: number): Promise<{ rows: UsageRow[]; days: MetricPoint[] }> {
    return http.get(`/usage${qs({ dim, days })}`);
  },

  /* —— 设置 —— */
  getSettings(): Promise<Settings> { return http.get('/settings'); },
  updateSettings(body: Settings): Promise<Settings> { return http.patch('/settings', body); },
};

export type { MatchMode };
