/**
 * 服务层：当前为本地 mock，接口签名按真实 REST 设计。
 * 接后端时只需把每个方法体换成 http.get/post，并在 src/services/http.ts 配置 baseURL，
 * 页面与组件代码无需改动。
 */
import type {
  Channel, GatewayToken, MetricPoint, ModelCatalogItem, ModelOffer,
  RequestLogItem, RouteRule, UsageRow,
} from '@/types';
import {
  channels, days, hours, logs, models, rules, tokens, usageRows,
} from './mock/db';

const delay = <T>(data: T, ms = 260): Promise<T> =>
  new Promise(resolve => setTimeout(() => resolve(JSON.parse(JSON.stringify(data)) as T), ms));

export interface OverviewData {
  hours: MetricPoint[];
  days: MetricPoint[];
  totalRequests: number;
  totalErrors: number;
  totalCostUsd: number;
  avgFirstTokenMs: number;
}

export const api = {
  /* 概览 */
  getOverview(): Promise<OverviewData> {
    const totalRequests = hours.reduce((a, b) => a + b.requests, 0);
    const totalErrors = hours.reduce((a, b) => a + b.errors, 0);
    const totalCostUsd = hours.reduce((a, b) => a + b.costUsd, 0);
    return delay({
      hours, days, totalRequests, totalErrors, totalCostUsd,
      avgFirstTokenMs: 412,
    });
  },

  /* 渠道 */
  getChannels: (): Promise<Channel[]> => delay(channels),
  testChannel(id: number): Promise<{ ok: boolean; latencyMs: number }> {
    const c = channels.find(x => x.id === id);
    const ok = !!c && c.status !== 'down';
    return delay({ ok, latencyMs: ok ? c!.latencyMs + Math.round(Math.random() * 40) : 0 }, 700);
  },

  /* 模型广场 */
  getModels: (): Promise<ModelCatalogItem[]> => delay(models),

  toggleModel(modelId: number, enabled: boolean): Promise<ModelCatalogItem[]> {
    const m = models.find(x => x.id === modelId);
    if (m) m.enabled = enabled;
    return delay(models, 160);
  },

  /** 拖拽排序：把 from 位置的供给源移动到 insertAt 位置，并重算 1..N 优先级 */
  reorderOffers(modelId: number, from: number, insertAt: number): Promise<ModelOffer[]> {
    const m = models.find(x => x.id === modelId);
    if (!m) return Promise.reject(new Error('模型不存在'));
    const target = insertAt > from ? insertAt - 1 : insertAt;
    if (target < 0 || target > m.offers.length) return Promise.resolve(m.offers);
    const [item] = m.offers.splice(from, 1);
    m.offers.splice(target, 0, item);
    m.offers.forEach((o, i) => { o.priority = i + 1; });
    return delay(m.offers, 180);
  },

  toggleOffer(modelId: number, offerId: number, enabled: boolean): Promise<ModelOffer[]> {
    const m = models.find(x => x.id === modelId);
    const o = m?.offers.find(x => x.id === offerId);
    if (o) o.enabled = enabled;
    return delay(m ? m.offers : [], 160);
  },

  /* 令牌 */
  getTokens: (): Promise<GatewayToken[]> => delay(tokens),
  createToken(): Promise<{ key: string }> {
    const hex = '0123456789abcdef';
    let key = 'sk-gw-';
    for (let i = 0; i < 32; i++) key += hex[Math.floor(Math.random() * 16)];
    return delay({ key }, 420);
  },

  /* 路由规则 */
  getRules: (): Promise<RouteRule[]> => delay(rules),
  reorderRules(from: number, insertAt: number): Promise<RouteRule[]> {
    const target = insertAt > from ? insertAt - 1 : insertAt;
    const [item] = rules.splice(from, 1);
    rules.splice(target, 0, item);
    return delay(rules, 180);
  },

  /* 日志 */
  getLogs(): Promise<RequestLogItem[]> { return delay(logs); },

  /* 用量 */
  getUsage(): Promise<{ rows: UsageRow[]; days: MetricPoint[] }> {
    return delay({ rows: usageRows, days });
  },
};
