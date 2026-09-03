import type {
  Channel, GatewayToken, MetricPoint, ModelCatalogItem, ModelOffer,
  Provider, RequestLogItem, RouteRule, UsageRow, Capability, HealthStatus,
} from '@/types';

/* ---------------- 渠道 ---------------- */
export const channels: Channel[] = [
  { id: 1, name: 'OpenAI 官方', provider: 'OpenAI', baseUrl: 'https://api.openai.com/v1', priority: 1, weight: 100, status: 'healthy', latencyMs: 42, successRate: .998, todayTokens: 1840200, todayCostUsd: 12.48, keyMasked: 'sk-p****4f2a', modelCount: 6, timeoutMs: 60000, tags: ['海外', '直连'] },
  { id: 2, name: 'Azure 华东', provider: 'Azure', baseUrl: 'https://gw-east.openai.azure.com', priority: 2, weight: 60, status: 'healthy', latencyMs: 88, successRate: .995, todayTokens: 640300, todayCostUsd: 5.21, keyMasked: '8f2****c91d', modelCount: 4, timeoutMs: 60000, tags: ['企业'] },
  { id: 3, name: 'Anthropic 官方', provider: 'Anthropic', baseUrl: 'https://api.anthropic.com', priority: 1, weight: 100, status: 'healthy', latencyMs: 156, successRate: .997, todayTokens: 2140000, todayCostUsd: 38.62, keyMasked: 'sk-a****7b3e', modelCount: 4, timeoutMs: 120000, tags: ['海外'] },
  { id: 4, name: 'DeepSeek 官方', provider: 'DeepSeek', baseUrl: 'https://api.deepseek.com', priority: 1, weight: 100, status: 'healthy', latencyMs: 210, successRate: .992, todayTokens: 3120000, todayCostUsd: 4.86, keyMasked: 'sk-d****19ac', modelCount: 3, timeoutMs: 120000, tags: ['国内'] },
  { id: 5, name: '通义千问', provider: '通义千问', baseUrl: 'https://dashscope.aliyuncs.com', priority: 2, weight: 80, status: 'healthy', latencyMs: 76, successRate: .996, todayTokens: 980400, todayCostUsd: 1.92, keyMasked: 'sk-q****88e1', modelCount: 5, timeoutMs: 60000, tags: ['国内'] },
  { id: 6, name: '智谱 GLM', provider: '智谱', baseUrl: 'https://open.bigmodel.cn', priority: 3, weight: 40, status: 'degraded', latencyMs: 320, successRate: .964, todayTokens: 142000, todayCostUsd: .74, keyMasked: 'zp-****4d0f', modelCount: 2, timeoutMs: 60000, tags: ['国内'] },
  { id: 7, name: 'Moonshot', provider: 'Moonshot', baseUrl: 'https://api.moonshot.cn', priority: 4, weight: 30, status: 'healthy', latencyMs: 95, successRate: .991, todayTokens: 86000, todayCostUsd: .31, keyMasked: 'sk-m****2a70', modelCount: 2, timeoutMs: 60000, tags: ['国内'] },
  { id: 8, name: '聚合中转', provider: '聚合中转', baseUrl: 'https://api.siliconflow.cn', priority: 9, weight: 20, status: 'down', latencyMs: 0, successRate: .412, todayTokens: 0, todayCostUsd: 0, keyMasked: 'sk-s****c03b', modelCount: 8, timeoutMs: 30000, tags: ['兜底'] },
];

export const providers: Provider[] = ['OpenAI', 'Azure', 'Anthropic', 'DeepSeek', '通义千问', '智谱', 'Moonshot', '聚合中转'];

/* ---------------- 模型广场 ---------------- */
let offerSeq = 1;
function offer(
  modelId: number, channelId: number, inputPriceUsd: number, outputPriceUsd: number,
  latencyMs: number, successRate: number, priority: number,
  extra: Partial<ModelOffer> = {},
): ModelOffer {
  const c = channels.find(x => x.id === channelId)!;
  return {
    id: offerSeq++, modelId, channelId, channelName: c.name, provider: c.provider,
    inputPriceUsd, outputPriceUsd, overridePrice: false, latencyMs, successRate,
    priority, enabled: true, contextWindow: 0, rateLimitRpm: 3000,
    status: c.status, ...extra,
  };
}

type ModelDef = [name: string, ctx: number, caps: Capability[], req: number, offers: ModelOffer[]];

const defs: ModelDef[] = [
  ['gpt-4o-mini', 128000, ['vision', 'function', 'stream'], 18420, [
    offer(0, 1, .1500, .6000, 42, .998, 1),
    offer(0, 2, .1650, .6600, 88, .995, 2),
    offer(0, 8, .1200, .4800, 130, .972, 3, { enabled: false }),
  ]],
  ['gpt-4o', 128000, ['vision', 'function', 'stream'], 3260, [
    offer(0, 1, 2.5000, 10.0000, 168, .997, 1),
    offer(0, 2, 2.7500, 11.0000, 205, .993, 2),
  ]],
  ['claude-3-5-sonnet', 200000, ['vision', 'function', 'stream'], 8110, [
    offer(0, 3, 3.0000, 15.0000, 156, .997, 1),
    offer(0, 8, 2.7000, 13.5000, 240, .961, 2, { enabled: false }),
  ]],
  ['claude-3-5-haiku', 200000, ['vision', 'stream'], 12400, [
    offer(0, 3, .8000, 4.0000, 62, .998, 1),
  ]],
  ['deepseek-chat', 64000, ['function', 'stream'], 24100, [
    offer(0, 4, .2700, 1.1000, 210, .992, 1),
    offer(0, 8, .2400, 1.0000, 320, .954, 2),
  ]],
  ['deepseek-reasoner', 64000, ['reasoning', 'stream'], 4180, [
    offer(0, 4, .5500, 2.1900, 1860, .988, 1),
  ]],
  ['qwen-plus', 131000, ['function', 'stream'], 15200, [
    offer(0, 5, .4000, 1.2000, 76, .996, 1),
    offer(0, 8, .3500, 1.0500, 142, .978, 2),
  ]],
  ['qwen-max', 32000, ['function', 'stream'], 2860, [
    offer(0, 5, 1.6000, 6.4000, 240, .994, 1),
  ]],
  ['glm-4-plus', 128000, ['function', 'stream'], 940, [
    offer(0, 6, .5000, 1.5000, 320, .964, 1),
  ]],
  ['moonshot-v1-8k', 8000, ['stream'], 620, [
    offer(0, 7, .1700, .1700, 95, .991, 1),
  ]],
  ['gpt-4-turbo', 128000, ['vision', 'function', 'stream'], 1180, [
    offer(0, 1, 10.0000, 30.0000, 890, .996, 1),
  ]],
  ['claude-3-opus', 200000, ['vision', 'function', 'stream'], 240, [
    offer(0, 3, 15.0000, 75.0000, 1240, .995, 1),
  ]],
  ['text-embedding-3-small', 8000, [], 36200, [
    offer(0, 1, .0200, 0, 18, .999, 1, { rateLimitRpm: 8000 }),
    offer(0, 8, .0150, 0, 46, .988, 2, { enabled: false }),
  ]],
  ['qwen-turbo', 131000, ['stream'], 9400, [
    offer(0, 5, .0500, .1500, 54, .997, 1),
  ]],
];

export const models: ModelCatalogItem[] = defs.map(([name, ctx, caps, req, offers], i) => {
  const id = i + 1;
  const os = offers.map(o => ({ ...o, modelId: id, contextWindow: ctx }));
  return {
    id, name, contextWindow: ctx, capabilities: caps, offers: os, enabled: true,
    todayRequests: req,
    successRate: os.filter(o => o.enabled).reduce((a, o) => Math.max(a, o.successRate), 0),
  };
});

/* ---------------- 令牌 ---------------- */
export const tokens: GatewayToken[] = [
  { id: 1, name: '本机开发', keyMasked: 'sk-gw-****3f9a', allowedModels: ['*'], quotaUsd: 50, usedUsd: 12.84, rpmLimit: 60, expiresAt: null, lastUsedAt: '2026-09-03 20:24', status: 'active' },
  { id: 2, name: '笔记插件', keyMasked: 'sk-gw-****b21c', allowedModels: ['gpt-4o-mini', 'qwen-plus'], quotaUsd: 10, usedUsd: 8.62, rpmLimit: 20, expiresAt: '2026-10-01', lastUsedAt: '2026-09-03 19:48', status: 'active' },
  { id: 3, name: '脚本批处理', keyMasked: 'sk-gw-****7d04', allowedModels: ['deepseek-chat', 'qwen-turbo'], quotaUsd: 20, usedUsd: 19.31, rpmLimit: 120, expiresAt: '2026-09-08', lastUsedAt: '2026-09-03 18:02', status: 'active' },
  { id: 4, name: '测试环境', keyMasked: 'sk-gw-****e5b8', allowedModels: ['*'], quotaUsd: 5, usedUsd: .42, rpmLimit: 10, expiresAt: '2026-09-30', lastUsedAt: '2026-09-02 11:20', status: 'disabled' },
  { id: 5, name: '旧版 App', keyMasked: 'sk-gw-****0a6f', allowedModels: ['gpt-4o'], quotaUsd: 30, usedUsd: 30, rpmLimit: 30, expiresAt: '2026-08-20', lastUsedAt: '2026-08-19 22:11', status: 'expired' },
];

/* ---------------- 路由规则 ---------------- */
export const rules: RouteRule[] = [
  { id: 1, name: '长上下文走 Claude', enabled: true, matchMode: 'prefix', pattern: 'claude-*', strategy: 'latency', channelIds: [3, 8], fallbackChannelId: 1, retry: 2, timeoutMs: 120000, hit: 10320 },
  { id: 2, name: '国内模型优先', enabled: true, matchMode: 'wildcard', pattern: 'qwen-*/glm-*', strategy: 'priority', channelIds: [5, 6], fallbackChannelId: 4, retry: 1, timeoutMs: 60000, hit: 28740 },
  { id: 3, name: '推理类降级到中转', enabled: true, matchMode: 'regex', pattern: '^(deepseek-reasoner|o1-)', strategy: 'weight', channelIds: [4, 8], weights: { 4: 70, 8: 30 }, fallbackChannelId: null, retry: 3, timeoutMs: 180000, hit: 4180 },
  { id: 4, name: '嵌入请求走高速通道', enabled: true, matchMode: 'prefix', pattern: 'text-embedding-*', strategy: 'priority', channelIds: [1, 8], fallbackChannelId: 5, retry: 1, timeoutMs: 15000, hit: 36200 },
  { id: 5, name: 'GPT-4 系列限流', enabled: false, matchMode: 'prefix', pattern: 'gpt-4*', strategy: 'weight', channelIds: [1, 2], weights: { 1: 70, 2: 30 }, fallbackChannelId: 3, retry: 2, timeoutMs: 90000, hit: 0 },
];

/* ---------------- 日志与指标 ---------------- */
const logModels = ['gpt-4o-mini', 'claude-3-5-sonnet', 'deepseek-chat', 'qwen-plus', 'text-embedding-3-small', 'gpt-4o', 'qwen-turbo'];

export const logs: RequestLogItem[] = Array.from({ length: 40 }, (_, i) => {
  const ok = i % 9 !== 3;
  const ch = channels[i % 6];
  const inT = 200 + ((i * 137) % 4200);
  const outT = ok ? 120 + ((i * 91) % 1800) : 0;
  return {
    id: 'req_' + (98214 - i),
    ts: `2026-09-03 20:${String(24 - Math.floor(i / 2)).padStart(2, '0')}:${String((i * 17) % 60).padStart(2, '0')}`,
    model: logModels[i % logModels.length],
    channelName: ch.name,
    tokenName: tokens[i % 4].name,
    inTokens: inT, outTokens: outT,
    costUsd: inT * .00015 + outT * .0006,
    firstTokenMs: ok ? 180 + ((i * 53) % 900) : 0,
    totalMs: ok ? 620 + ((i * 71) % 3200) : 480 + ((i * 31) % 900),
    statusCode: ok ? 200 : [429, 500, 502, 401][i % 4],
    ip: `127.0.0.${1 + (i % 3)}`,
    error: ok ? null : ['上游限流 rate_limit_exceeded', '上游 500 Internal Error', '网关连接超时', '令牌额度已用尽'][i % 4],
  };
});

export const hours: MetricPoint[] = Array.from({ length: 24 }, (_, i) => {
  const base = 180 + Math.sin(i / 3) * 120 + (i > 8 && i < 22 ? 420 : 0);
  const requests = Math.round(base + ((i * 37) % 90));
  return {
    ts: `${String(i).padStart(2, '0')}:00`,
    requests,
    errors: Math.round(requests * (.004 + ((i * 13) % 22) / 1000)),
    costUsd: requests * .012,
  };
});

export const days: MetricPoint[] = ['08-28', '08-29', '08-30', '08-31', '09-01', '09-02', '09-03']
  .map((d, i) => ({
    ts: d,
    requests: 12400 + i * 860 + ((i * 431) % 1200),
    errors: 40 + ((i * 17) % 60),
    costUsd: 42.6 + i * 3.4 + ((i * 17) % 9) / 10,
  }));

export const usageRows: UsageRow[] = [
  { name: 'gpt-4o-mini', requests: 18420, inTokens: 3120000, outTokens: 986000, costUsd: 12.48, errorRate: .002 },
  { name: 'deepseek-chat', requests: 24100, inTokens: 6840000, outTokens: 1420000, costUsd: 4.86, errorRate: .008 },
  { name: 'qwen-plus', requests: 15200, inTokens: 4210000, outTokens: 812000, costUsd: 2.94, errorRate: .004 },
  { name: 'claude-3-5-sonnet', requests: 8110, inTokens: 1840000, outTokens: 620000, costUsd: 14.82, errorRate: .003 },
  { name: 'text-embedding-3-small', requests: 36200, inTokens: 12400000, outTokens: 0, costUsd: .248, errorRate: .001 },
  { name: 'qwen-turbo', requests: 9400, inTokens: 2180000, outTokens: 640000, costUsd: .41, errorRate: .001 },
];

export function statusLabel(s: HealthStatus): string {
  return ({ healthy: '健康', degraded: '降级', down: '不可用', disabled: '已停用' } as const)[s];
}
