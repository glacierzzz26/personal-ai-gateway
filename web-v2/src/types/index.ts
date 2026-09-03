export type Provider =
  | 'OpenAI' | 'Azure' | 'Anthropic' | 'DeepSeek'
  | '通义千问' | '智谱' | 'Moonshot' | '聚合中转';

export type HealthStatus = 'healthy' | 'degraded' | 'down' | 'disabled';

export interface Channel {
  id: number;
  name: string;
  provider: Provider;
  baseUrl: string;
  priority: number;
  weight: number;
  status: HealthStatus;
  latencyMs: number;
  /** 0..1 小数(API 返回 0..100 百分数,服务层已换算) */
  successRate: number;
  todayTokens: number;
  todayCostUsd: number;
  keyMasked: string;
  modelCount: number;
  timeoutMs: number;
  enabled: boolean;
  maxFailures: number;
  cooldownSec: number;
  tags: string[];
  note?: string;
  proxy?: string;
  circuitOpen?: boolean;
  createdAt?: string;
  updatedAt?: string;
}

/** 渠道创建/编辑入参(apiKey 仅新建或显式填新值;留空=不改) */
export interface ChannelDraft {
  name: string;
  provider: Provider;
  baseUrl: string;
  apiKey?: string;
  priority: number;
  weight: number;
  timeoutMs: number;
  enabled: boolean;
  maxFailures: number;
  cooldownSec: number;
  tags: string[];
  note?: string;
}

export type Capability = 'vision' | 'function' | 'stream' | 'reasoning';

export interface ModelOffer {
  id: number;
  modelId: number;
  channelId: number;
  channelName: string;
  provider: Provider;
  inputPriceUsd: number;
  outputPriceUsd: number;
  cacheReadPriceUsd?: number;
  overridePrice: boolean;
  latencyMs: number;
  successRate: number;
  priority: number;
  enabled: boolean;
  contextWindow: number;
  rateLimitRpm: number;
  status: HealthStatus;
  note?: string;
}

/** 供给源创建/编辑入参 */
export interface OfferDraft {
  channelId?: number;
  inputPriceUsd: number;
  outputPriceUsd: number;
  cacheReadPriceUsd?: number;
  overridePrice?: boolean;
  rateLimitRpm: number;
  enabled?: boolean;
  priority?: number;
  note?: string;
}

export interface ModelCatalogItem {
  id: number;
  name: string;
  contextWindow: number;
  capabilities: Capability[];
  offers: ModelOffer[];
  enabled: boolean;
  todayRequests: number;
  successRate: number;
}

/** 模型创建/编辑入参 */
export interface ModelDraft {
  name: string;
  contextWindow: number;
  capabilities: Capability[];
  enabled: boolean;
}

export interface GatewayToken {
  id: number;
  name: string;
  keyMasked: string;
  allowedModels: string[];
  quotaUsd: number;
  usedUsd: number;
  rpmLimit: number;
  expiresAt: string | null;
  lastUsedAt: string | null;
  status: 'active' | 'disabled' | 'expired';
  createdAt?: string;
}

/** 令牌创建/编辑入参(expiresAt 传 null 表示永不过期) */
export interface TokenDraft {
  name: string;
  allowedModels: string[];
  quotaUsd: number;
  rpmLimit: number;
  expiresAt: string | null;
  status?: 'active' | 'disabled';
}

export type MatchMode = 'prefix' | 'wildcard' | 'regex';
export type RouteStrategy = 'priority' | 'weight' | 'latency';

export interface RouteRule {
  id: number;
  name: string;
  enabled: boolean;
  matchMode: MatchMode;
  pattern: string;
  strategy: RouteStrategy;
  channelIds: number[];
  weights?: Record<number, number>;
  fallbackChannelId: number | null;
  retry: number;
  timeoutMs: number;
  hit: number;
}

export interface RuleDraft {
  name: string;
  enabled: boolean;
  matchMode: MatchMode;
  pattern: string;
  strategy: RouteStrategy;
  channelIds: number[];
  weights?: Record<number, number>;
  fallbackChannelId: number | null;
  retry: number;
  timeoutMs: number;
}

export interface RequestLogItem {
  id: number;
  ts: string;
  model: string;
  channelName: string;
  tokenName: string;
  inTokens: number;
  outTokens: number;
  cacheReadTokens?: number;
  costUsd: number;
  firstTokenMs: number;
  totalMs: number;
  statusCode: number;
  ip: string;
  error?: string | null;
}

export interface LogPage {
  items: RequestLogItem[];
  total: number;
}

export interface LogFilters {
  model?: string;
  channel?: string;
  token?: string;
  status?: 'ok' | 'error' | '';
  kw?: string;
}

export interface MetricPoint {
  ts: string;
  requests: number;
  errors: number;
  costUsd: number;
}

export interface UsageRow {
  name: string;
  requests: number;
  inTokens: number;
  outTokens: number;
  costUsd: number;
  errorRate: number;
}

export type UsageDim = 'model' | 'channel' | 'token';

export interface OverviewData {
  hours: MetricPoint[];
  days: MetricPoint[];
  totalRequests: number;
  totalErrors: number;
  totalCostUsd: number;
  avgFirstTokenMs: number;
}

export interface ModelUsageData {
  daily: MetricPoint[];
  byChannel: { channelName: string; requests: number; costUsd: number }[];
}

export interface Settings {
  requestTimeoutMs: number;
  maxRetries: number;
  degradeOnError: boolean;
  httpProxy?: string;
  skipTlsVerify: boolean;
  logRetentionDays: number;
  recordRequestBody: boolean;
  sampleRatePct: number;
  tzOffsetMin: number;
}

export interface AdminMe {
  id: number;
  username: string;
  createdAt: string;
}

export interface ChannelTestResult {
  ok: boolean;
  latencyMs: number;
  message?: string;
}

export interface SyncResult {
  added: number;
  updated: number;
  models: string[];
}

export interface TokenCreateResult {
  key: string;
  token: GatewayToken;
}
