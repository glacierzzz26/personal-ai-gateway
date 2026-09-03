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
  successRate: number;
  todayTokens: number;
  todayCostUsd: number;
  keyMasked: string;
  modelCount: number;
  timeoutMs: number;
  proxy?: string;
  tags: string[];
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

export interface RequestLogItem {
  id: string;
  ts: string;
  model: string;
  channelName: string;
  tokenName: string;
  inTokens: number;
  outTokens: number;
  costUsd: number;
  firstTokenMs: number;
  totalMs: number;
  statusCode: number;
  ip: string;
  error?: string | null;
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
