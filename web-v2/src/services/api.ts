/**
 * 服务层:全部方法映射到网关管理面 REST(会话 cookie)。
 * 页面仅调用本文件导出,不直接 import mock —— mock/db.ts 已删除。
 *
 * 数值口径:后端成功率/错误率中 successRate 用 0..100 百分数,
 * 这里统一除以 100 还原为 0..1 小数供 UI(fmt.pct)使用;errorRate 本身即小数。
 */
import type {
  AdminMe, BalanceLogItem, BalanceResp, Channel, ChannelDraft, ChannelQuota, ChannelQuotaItem, ChannelTestResult, ClaudeConfig,
  CostRatioInput, CostRatioRow,
  FetchPricingResult, GatewayToken, LogFilters, LogPage, ManualPriceDraft, MatchMode, MetricPoint,
  AnnouncementDraft, AnnouncementItem,
  ModelCatalogItem, ModelDraft, ModelOffer, ModelUsageData, OfferDraft, OfficialPriceView, OfficialVendorInfo,
  OverviewData, CustomerFocusData, Provider, RefreshPricingResp, RequestLogItem, RouteRule, RuleDraft, Settings,
  StatRangeQuery, SyncResult,
  TokenCreateResult,
  TokenDraft, TokenProbeResp, UsageDim, UsageRow, UserAccount, UserModelItem,
} from '@/types';
import { http } from './http';

/** 后端返回的 0..100 → UI 的 0..1 */
const frac = (pct: number): number => (pct == null ? 0 : pct / 100);

/** 批量重抓的前端超时:后端逐厂商各 45s,留足余量(见 http.RequestOpts)。 */
export const REFRESH_TIMEOUT_MS = 120_000;

/**
 * 成本系数建议值:credit 型套餐的折扣率。
 *
 * 刻意只做「建议」不做「落库」—— $10 买 $60 是商业事实不是 schema 事实,
 * 各环境(测试库/生产库)渠道 id 不同,迁移 seed 业务数据是错的。改为新建系数行时
 * 预填 + 要人点确认。
 */
export const SUGGESTED_RATIOS: { match: RegExp; vendor: Provider; ratio: number; note: string }[] = [
  { match: /command\s*code/i, vendor: 'DeepSeek' as Provider, ratio: 1 / 6, note: '$10 买 $60 额度' },
];

/** 按渠道类型/名给出建议系数(命中才返回;空 = 无建议,不预填)。 */
export function suggestedRatio(channelType: string | undefined, vendor: Provider):
  { ratio: number; note: string } | null {
  if (!channelType) return null;
  for (const s of SUGGESTED_RATIOS) {
    if (s.match.test(channelType) && s.vendor === vendor) return { ratio: s.ratio, note: s.note };
  }
  return null;
}

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
  changePassword(oldPassword: string, newPassword: string): Promise<AdminMe> {
    return http.post('/auth/password', { oldPassword, newPassword });
  },

  /* —— 用户管理(管理员) —— */
  getUsers(): Promise<UserAccount[]> { return http.get('/users'); },
  createUser(body: { username: string; password: string; role: 'admin' | 'user' }): Promise<UserAccount> {
    return http.post('/users', body);
  },
  resetUserPassword(id: number, newPassword: string): Promise<unknown> {
    return http.patch(`/users/${id}/password`, { newPassword });
  },
  deleteUser(id: number): Promise<unknown> { return http.del(`/users/${id}`); },
  /** 给客户充值(正=充值,负=扣减调整);仅普通用户有钱包 */
  topupUser(id: number, amount: number, note?: string): Promise<BalanceLogItem> {
    return http.post(`/users/${id}/topup`, { amount, note });
  },
  /** 设客户名下令牌的额度/RPM 上限(0 = 不限);用户自助建令牌不得超过此值 */
  setUserCeiling(id: number, quotaUsd: number, rpmLimit: number): Promise<unknown> {
    return http.patch(`/users/${id}/ceiling`, { quotaUsd, rpmLimit });
  },
  /** 某客户的账变流水(管理员审计) */
  userBalanceLogs(id: number, limit = 50): Promise<BalanceLogItem[]> {
    return http.get(`/users/${id}/balance-logs${qs({ limit })}`);
  },

  /* —— 用户自助面(登录态即可,作用域锁本人) —— */
  myBalance(limit = 50): Promise<BalanceResp> {
    return http.get(`/me/balance${qs({ limit })}`);
  },
  /** 我的用量:dim=model|token(用户侧不暴露渠道);range 决定统计窗口 */
  getMyUsage(dim: 'model' | 'token', range: StatRangeQuery = { days: 7 }): Promise<{ rows: UsageRow[]; days: MetricPoint[] }> {
    return http.get(`/me/usage${qs({ dim, ...range })}`);
  },
  /** 我的请求日志(自动限定为本人名下令牌) */
  getMyLogs(filters: LogFilters = {}, page = 1, size = 20): Promise<LogPage> {
    const p = {
      model: filters.model, token: filters.token, status: filters.status, kw: filters.kw,
      page, size,
    };
    return http.get(`/me/logs${qs(p)}`);
  },
  /** 我能用的模型与价格(role=user 时 /models 返回收敛清单:无渠道/上游/来源/成本) */
  getMyModels(): Promise<UserModelItem[]> { return http.get('/models'); },

  /* —— 概览 —— */
  getOverview(range: StatRangeQuery = { days: 1 }): Promise<OverviewData> {
    return http.get(`/overview${qs({ ...range })}`);
  },
  /** 客户关注区:余额告警 + 窗口内消耗排行(仅 admin) */
  getCustomerFocus(range: StatRangeQuery = { days: 1 }): Promise<CustomerFocusData> {
    return http.get(`/customers/focus${qs({ ...range })}`);
  },

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
  /** 渠道额度(按 channel_type 分派到对应上游额度接口;后端带短 TTL 缓存) */
  channelQuota(id: number): Promise<ChannelQuota> { return http.get(`/channels/${id}/quota`); },
  /** 批量渠道额度(首页「运行总览」用):一次拿全渠道,服务端并发拉 + 短 TTL 缓存。 */
  channelsQuota(): Promise<ChannelQuotaItem[]> { return http.get('/channels/quota'); },
  syncModels(id: number): Promise<SyncResult> { return http.post(`/channels/${id}/sync-models`); },

  /* —— 渠道 × 厂商 成本系数(成本 = 厂商官方价 × ratio) —— */
  //
  // 独立端点而非并入 ChannelDraft:渠道 PATCH 是整体覆盖语义,系数混进去会被
  // 「改个渠道名」误清空。详见迁移 m0012。
  /**
   * 本渠道的系数行(只含显式配置过的;未配的厂商不在列表里,计费按 1.0)。
   * **勿据此渲染全部厂商** —— 要列全厂商请配 officialVendors()。
   */
  channelCostRatios(id: number): Promise<CostRatioRow[]> {
    return http.get(`/channels/${id}/cost-ratios`);
  },
  /** 全量替换本渠道的系数行:未回传的厂商即被删除(不是合并) */
  replaceChannelCostRatios(id: number, ratios: CostRatioInput[]): Promise<CostRatioRow[]> {
    return http.put(`/channels/${id}/cost-ratios`, { ratios });
  },
  /** 删除一条系数(退回默认 1.0);该行不存在时后端 404 */
  deleteChannelCostRatio(id: number, vendor: Provider): Promise<unknown> {
    return http.del(`/channels/${id}/cost-ratios${qs({ vendor })}`);
  },

  /* —— 模型广场 —— */
  async getModels(): Promise<ModelCatalogItem[]> {
    return (await http.get<ModelCatalogItem[]>('/models')).map(model);
  },
  createModel(body: ModelDraft): Promise<ModelCatalogItem> { return http.post<ModelCatalogItem>('/models', body).then(model); },
  updateModel(id: number, body: ModelDraft): Promise<ModelCatalogItem> {
    return http.patch<ModelCatalogItem>(`/models/${id}`, body).then(model);
  },
  deleteModel(id: number): Promise<unknown> { return http.del(`/models/${id}`); },
  /** 合并重复模型:把 fromId 的供给源并入 intoId 后删除 fromId */
  mergeModel(fromId: number, intoId: number): Promise<ModelCatalogItem> {
    return http.post<ModelCatalogItem>(`/models/${fromId}/merge`, { intoId }).then(model);
  },
  getModelUsage(id: number, days = 7): Promise<ModelUsageData> {
    return http.get(`/models/${id}/usage${qs({ days })}`);
  },
  async toggleModel(modelId: number, enabled: boolean): Promise<void> {
    const m = (await api.getModels()).find(x => x.id === modelId);
    if (!m) throw new Error('模型不存在');
    await api.updateModel(modelId, api.modelDraft(m, { enabled }));
  },
  /**
   * 模型编辑全量草稿(基于当前快照)。
   * 后端 PATCH 为全量替换:除 name/displayName/绑定字段外按请求体原值写入,
   * 手搓部分草稿会清零上下文/能力并强制启用 —— 一律经此构造。
   */
  modelDraft(m: ModelCatalogItem, patch: Partial<ModelDraft> = {}): ModelDraft {
    return {
      name: m.originalName,
      displayName: m.displayName ?? '',
      contextWindow: m.contextWindow,
      capabilities: m.capabilities,
      enabled: m.enabled,
      officialVendor: m.officialVendor ?? '',
      officialModelName: m.officialModelName ?? '',
      ...patch,
    };
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
      // 缓存写价同理必须回传:PATCH 全量替换语义下漏传即清零(0=无依据→按 input 价回落)
      cacheWritePriceUsd: o.cacheWritePriceUsd ?? 0,
      overridePrice: o.overridePrice,
      rateLimitRpm: o.rateLimitRpm,
      enabled,
      note: o.note,
      // 来源留证必须原样回传,否则任一编辑都会清空官方价来源(PATCH 全量替换语义)
      priceSourceUrl: o.priceSourceUrl,
      priceFetchedAt: o.priceFetchedAt,
      priceCurrency: o.priceCurrency,
      priceNativeText: o.priceNativeText,
      // 上游真实名同理:漏传即清空,出站会退回模型名
      upstreamModel: o.upstreamModel,
    };
  },

  /* —— 官方定价(厂商官网) —— */
  /** 按渠道 provider 抓取官方单价表 → 落 official_prices(不直接改 offer 价) */
  fetchPricing(channelId: number): Promise<FetchPricingResult> {
    return http.post(`/channels/${channelId}/fetch-pricing`);
  },
  /** 按厂商抓取官方单价表(无需厂商直连渠道;聚合中转场景) */
  fetchOfficialPrices(provider: Provider): Promise<FetchPricingResult> {
    return http.post('/official-prices/fetch', { provider });
  },
  /** 有官方来源的厂商清单(驱动抓取/手工录入入口与门禁) */
  officialVendors(): Promise<OfficialVendorInfo[]> { return http.get('/official-prices/vendors'); },
  /** 某渠道 provider 的官方参考价 + 与现有 offer 的比对 */
  channelOfficialPrices(channelId: number): Promise<OfficialPriceView[]> {
    return http.get(`/channels/${channelId}/official-prices`);
  },
  /** 全部官方参考价(模型广场;可按 provider 过滤) */
  officialPrices(provider?: string): Promise<OfficialPriceView[]> {
    return http.get(`/official-prices${qs({ provider })}`);
  },
  /** 手工录入官方参考价(智谱等页面不可抓的厂商;来源 URL 必填) */
  manualOfficialPrice(body: ManualPriceDraft): Promise<OfficialPriceView> {
    return http.post('/official-prices/manual', body);
  },
  /** 应用官方价到某 offer(写四价 + 来源留证;手工覆盖价需 confirmOverride) */
  applyOfficialPrice(id: number, offerId: number, confirmOverride = false): Promise<ModelOffer> {
    return http.post<ModelOffer>(`/official-prices/${id}/apply`, { offerId, confirmOverride }).then(offer);
  },
  /** 删除一条官方参考价(已应用到 offer 的价与留证不受影响) */
  deleteOfficialPrice(id: number): Promise<unknown> { return http.del(`/official-prices/${id}`); },
  /**
   * 重抓官方价并回填模型绑定。providers 留空 = 只抓 commandcode 单页锚点(issue #27 后的主力来源);
   * 显式给 providers 时,CC 之后再把它们走旧逐厂商路径。
   *
   * 逐厂商独立成败:某厂商失败只记在它自己的结果里,不影响其他厂商 —— 所以整请求
   * 仍是 200,前端要看 `results[].error` 才知道谁失败。CC 的成败单拎在
   * `commandCode` / `commandCodeError` 两个字段。
   *
   * ⚠️ 抓取有破坏性:内部会删掉官网已下架、页面上不再列出的模型行。
   *
   * 超时前端必须放宽到 120s:后端逐厂商各占 45s,浏览器默认早断会让人以为「点了没反应」。
   */
  refreshOfficialPrices(providers?: Provider[]): Promise<RefreshPricingResp> {
    return http.post<RefreshPricingResp>('/official-prices/refresh', { providers: providers ?? [] },
      { timeoutMs: REFRESH_TIMEOUT_MS });
  },

  /* —— 令牌 —— */
  getTokens(): Promise<GatewayToken[]> { return http.get('/tokens'); },
  createToken(body: TokenDraft): Promise<TokenCreateResult> { return http.post('/tokens', body); },
  updateToken(id: number, body: TokenDraft): Promise<GatewayToken> { return http.patch(`/tokens/${id}`, body); },
  deleteToken(id: number): Promise<unknown> { return http.del(`/tokens/${id}`); },
  getClaudeConfig(id: number): Promise<ClaudeConfig> { return http.get(`/tokens/${id}/claude-config`); },
  /** 令牌自检:不访问上游、不计费地判定「这个 key 能否用某模型」 */
  probeToken(id: number, model: string): Promise<TokenProbeResp> {
    return http.post(`/tokens/${id}/probe`, { model });
  },

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
  getUsage(dim: UsageDim, range: StatRangeQuery = { days: 7 }): Promise<{ rows: UsageRow[]; days: MetricPoint[] }> {
    return http.get(`/usage${qs({ dim, ...range })}`);
  },

  /* —— 设置 —— */
  getSettings(): Promise<Settings> { return http.get('/settings'); },
  updateSettings(body: Settings): Promise<Settings> { return http.patch('/settings', body); },

  /* —— 通知/公告 —— */
  /** 管理员:全部公告(附已读计数) */
  listAnnouncements(): Promise<AnnouncementItem[]> { return http.get('/announcements'); },
  createAnnouncement(body: AnnouncementDraft): Promise<AnnouncementItem> { return http.post('/announcements', body); },
  updateAnnouncement(id: number, body: AnnouncementDraft): Promise<AnnouncementItem> {
    return http.patch(`/announcements/${id}`, body);
  },
  deleteAnnouncement(id: number): Promise<unknown> { return http.del(`/announcements/${id}`); },
  /** 我最新一条未确认公告;无则 null */
  myAnnouncement(): Promise<{ announcement: AnnouncementItem | null }> {
    return http.get('/me/announcement');
  },
  /** 「我已知晓」:此后该公告不再对我弹出 */
  ackAnnouncement(id: number): Promise<unknown> { return http.post(`/me/announcement/${id}/ack`); },
};

export type { MatchMode };
