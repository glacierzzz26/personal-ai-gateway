/** 真实厂商(卖的是谁的模型)。已不含 Azure / 聚合中转 —— 出站协议见 EgressProto,
 *  渠道上游归属见 ChannelType。空串 = 不是单一厂商(多厂商聚合渠道)。 */
export type Provider =
  | 'OpenAI' | 'Anthropic' | 'DeepSeek'
  | '通义千问' | '智谱' | 'Moonshot' | '';

/** 渠道类型:决定上游额度怎么查。 */
export type ChannelType = 'deepseek' | 'commandcode' | 'opencode' | 'thirdparty';

/** 出站协议:决定请求怎么发上去。 */
export type EgressProto = 'openai' | 'anthropic' | 'azure';

/** 第三方渠道额度接口的响应形状。 */
export type QuotaShape = 'usage' | 'oneapi' | 'newapi_user';

export type HealthStatus = 'healthy' | 'degraded' | 'down' | 'disabled' | 'unknown';

export interface Channel {
  id: number;
  name: string;
  provider: Provider;
  channelType: ChannelType;
  egressProto: EgressProto;
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
  /** 第三方渠道额度查询路径(仅 thirdparty 有意义) */
  quotaPath?: string;
  quotaShape?: string;
  proxy?: string;
  circuitOpen?: boolean;
  createdAt?: string;
  updatedAt?: string;
}

/** 渠道创建/编辑入参(apiKey 仅新建或显式填新值;留空=不改) */
export interface ChannelDraft {
  name: string;
  provider: Provider;
  channelType: ChannelType;
  egressProto: EgressProto;
  baseUrl: string;
  apiKey?: string;
  priority: number;
  weight: number;
  timeoutMs: number;
  enabled: boolean;
  maxFailures: number;
  cooldownSec: number;
  tags: string[];
  quotaPath?: string;
  quotaShape?: string;
  note?: string;
}

export type Capability = 'vision' | 'function' | 'stream' | 'reasoning';

export interface ModelOffer {
  id: number;
  modelId: number;
  channelId: number;
  channelName: string;
  provider: Provider;
  /** 所属渠道的类型;provider 为空(聚合渠道)时前端用它代替供应商展示 */
  channelType?: ChannelType;
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
  /** 官方价来源留证(空=未从官方来源应用过)。仅作核对,不参与计费。 */
  priceSourceUrl?: string;
  priceFetchedAt?: string;
  priceCurrency?: string;
  priceNativeText?: string;
  /** 本渠道侧真实模型名(非空=出站发往本渠道时用该名;空=用模型名) */
  upstreamModel?: string;
  /** 由上游名/模型名推断出的厂商(空=判不出)。聚合渠道据此匹配厂商官方价 */
  inferredVendor?: Provider;
  /** 成本派生视图(仅管理面返回;用户面结构独立,天然不含此字段) */
  cost?: CostQuote;
}

/** 成本口径。unknown = 无任何成本依据 → **必须隐藏毛利列**,不能显示 0 毛利。 */
export type CostSource = 'official' | 'offer' | 'unknown';

/**
 * 供给源成本的派生视图(每百万 token,计价币种)。
 *
 * 成本不落库,是「官方价 × 渠道系数」现算的 —— 官方价一变、系数一改即时生效。
 * source='unknown' 时三价恒为 0,前端据此显示「成本未知」而非「成本 0」。
 */
export interface CostQuote {
  in: number;
  out: number;
  cacheRead: number;
  source: CostSource;
  /** 仅 source='official' 时有值 */
  vendor?: Provider;
  /** 渠道成本系数;1.0 = 未设(带 warn 说明) */
  ratio?: number;
  /** 该时刻是否落在高峰档(仅分时形态有值) */
  peak?: boolean;
  window?: 'peak' | 'offpeak';
  /** 非致命提示:阶梯按首档计 / 系数未设 / 未绑定官方价 */
  warn?: string;
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
  /** 来源留证四字段必须原样回传(后端 PATCH 为全量替换,漏传即被清空) */
  priceSourceUrl?: string;
  priceFetchedAt?: string;
  priceCurrency?: string;
  priceNativeText?: string;
  /** 上游真实模型名;同样受全量替换约束,编辑时必须回传(空串=清空,用模型名) */
  upstreamModel?: string;
}

export interface ModelCatalogItem {
  id: number;
  /** 对外统一名(重命名后为新名,否则等于 originalName) */
  name: string;
  /** 已设置的统一名;未重命名时为空/缺省 */
  displayName?: string;
  /** 渠道侧真实模型名,始终可查看 */
  originalName: string;
  contextWindow: number;
  capabilities: Capability[];
  offers: ModelOffer[];
  enabled: boolean;
  todayRequests: number;
  successRate: number;
  /** 模型级官方价绑定:显式指向某厂商 official_prices 的一行(空=走自动匹配) */
  officialVendor?: Provider;
  officialModelName?: string;
  /** 由模型名推断出的厂商(空=判不出)。聚合渠道据此自动匹配厂商官方价 */
  inferredVendor?: Provider;
  /** 模型级售价倍率;缺省/空=跟随全局 settings.priceMultiplier */
  rateOverride?: number | null;
}

/** 模型创建/编辑入参 */
export interface ModelDraft {
  name: string;
  /** 统一名称;空串=取消重命名(取真实名)。不传=保持原值 */
  displayName?: string;
  contextWindow: number;
  capabilities: Capability[];
  enabled: boolean;
  /** 官方价绑定;不传=保持原值,空串=清空 */
  officialVendor?: string;
  officialModelName?: string;
  /** 售价倍率;null=清空覆盖回落全局,不传=保持原值 */
  rateOverride?: number | null;
}

/** 用户面模型清单一行(GET /models,role=user)。
 *  刻意不含渠道名 / 上游真实名 / 官方价来源 URL / 成本 / 全站用量。 */
export interface UserModelItem {
  name: string;
  contextWindow: number;
  capabilities: Capability[];
  /** 官方价锚(划线原价,计价币种,每百万 token);未录官方价时缺省 */
  official?: UserPrice;
  /** 本站价 = 官方价 × 倍率(客户实付口径);无官方价时缺省。
   *  分时模型这里是**空闲档**价(展示面刻意不随当前时钟变,免得截图对不上账) */
  retail?: UserPrice;
  /** 该模型分时计价(峰谷两档价不同)。true 时下方 peakRetail/peakHours 有值 */
  peakVaries?: boolean;
  /** 高峰档本站价;仅 peakVaries 时有值。与 retail 并列展示 */
  peakRetail?: UserPrice;
  /** 峰时段人读说明(厂商原文,如「北京时间周一至周五 9:00-12:00、14:00-18:00」) */
  peakHours?: string;
  /** 价格不可用时的说明(未录官方价 / 未设汇率) */
  priceNote?: string;
}

/** 用户面每百万 token 的三价(计价币种) */
export interface UserPrice {
  input: number;
  output: number;
  cacheRead: number;
  currency: string;
}

/** 可抓取/可手工录入官方价的厂商(GET /official-prices/vendors) */
export interface OfficialVendorInfo {
  provider: Provider;
  sourceUrl: string;
  /** true=官方页动态渲染,只能手工录入 */
  manualOnly: boolean;
  /** 手工录入时的默认原币种(CNY/USD);仅 manualOnly 厂商有值 */
  manualCurrency?: PriceCurrency;
}

/**
 * 一条「渠道 × 厂商」成本系数(GET/PUT /channels/{id}/cost-ratios)。
 *
 * 按 (渠道, 厂商) 而非按模型:credit 型套餐($10 买 $60 额度)对所有模型同倍率,
 * 按模型是 O(渠道×模型) 个格子,按厂商是 O(渠道×厂商)。缺行 = 1.0(不折扣)。
 */
export interface CostRatioRow {
  channelId: number;
  vendor: Provider;
  /** 成本 = 该厂商官方价 × ratio;1.0 = 不折扣 */
  ratio: number;
  note?: string;
  updatedAt?: string;
}

/** PUT /channels/{id}/cost-ratios 的入参:全量替换该渠道的系数行 */
export interface CostRatioInput {
  vendor: Provider;
  ratio: number;
  note?: string;
}

/** 批量刷新中单个厂商的结果。逐厂商独立成败:一个失败不影响其他。 */
export interface RefreshProviderResult {
  provider: Provider;
  upserted: number;
  removed?: number;
  sourceUrl?: string;
  error?: string;
  /** 失败类别(unsupported/fetch_failed/manual_only/store_error) */
  errorType?: string;
}

/** 一次「绑定到模型」的回填结果 */
export interface OfficialBindingFill {
  modelId: number;
  modelName: string;
  vendor: Provider;
  officialName: string;
}

/** POST /official-prices/refresh 返回 */
export interface RefreshPricingResp {
  results: RefreshProviderResult[];
  /** 本次新绑定的模型(已绑定的不重复出现) */
  bound: OfficialBindingFill[];
  backfillError?: string;
  totalUpserted: number;
  totalRemoved?: number;
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
  ownerId: number | null;
  ownerName?: string;
  /** key_cipher 非空才可回显/生成配置(本特性前建的旧 key 为 false) */
  keyRetrievable: boolean;
  /** 实际向归属用户钱包扣的金额(计价币种,= 官方价 × 归属用户倍率;无官方价时回落成本 × 倍率);0 = 未结算 */
  chargeUsd: number;
}

/** 令牌创建/编辑入参(expiresAt 传 null 表示永不过期;ownerId 仅创建时生效,admin 可指定) */
export interface TokenDraft {
  name: string;
  allowedModels: string[];
  quotaUsd: number;
  rpmLimit: number;
  expiresAt: string | null;
  status?: 'active' | 'disabled';
  ownerId?: number | null;
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
  /** 实际向归属用户钱包扣的金额(计价币种,= 官方价 × 归属用户倍率;无官方价时回落成本 × 倍率);0 = 未结算(失败请求 / 管理员键) */
  chargeUsd?: number;
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
  /** canceled = 客户端主动断开(499),不计入错误率。 */
  status?: 'ok' | 'error' | 'canceled' | '';
  kw?: string;
}

export interface MetricPoint {
  ts: string;
  requests: number;
  errors: number;
  costUsd: number;
  /** 该桶向客户收的钱(售价口径);用户面曲线用这个,不是成本 */
  chargeUsd?: number;
}

export interface UsageRow {
  name: string;
  requests: number;
  inTokens: number;
  outTokens: number;
  costUsd: number;
  /** 该维度向客户收的钱(售价口径);用户面「花费」用这个 */
  chargeUsd?: number;
  errorRate: number;
}

export type UsageDim = 'model' | 'channel' | 'token';

/**
 * 统计窗口查询参数。二选一:
 *  - 预设 `{ days }`:最近 N 个自然日(含今天);
 *  - 自定义 `{ from, to }`:YYYY-MM-DD,含首尾,跨度 ≤90 天。
 * 后端统一解析(见 internal/server/statrange.go),日志只留 90 天。
 */
export interface StatRangeQuery {
  days?: number;
  from?: string;
  to?: string;
}

/** 请求日志保留上限(天),前端日期选择器据此钳制可选范围。 */
export const STAT_MAX_DAYS = 90;

export interface OverviewData {
  /** 窗口内曲线:≤3 天按小时,>3 天按日;桶由服务端补零成连续序列 */
  points: MetricPoint[];
  totalRequests: number;
  totalErrors: number;
  /** 全站成本(你付上游,含站主自用与无归属流量)。不是营收 —— 营收看 totals。 */
  totalCostUsd: number;
  avgFirstTokenMs: number;
  /** 窗口自然日数(服务端回填) */
  days: number;
  /** 曲线桶粒度:hour | day */
  bucket: 'hour' | 'day';
  /** 客户归属口径的同窗口曲线(营收/成本),与 totals 同源同桶 */
  customerPoints: MetricPoint[];
  /** 客户归属的经营口径:营收/成本/毛利(仅 admin 面返回) */
  totals: MarginTotals;
  /** 上一等长自然日窗口的同口径合计(环比基准);自定义区间为 null */
  prev: MarginTotals | null;
}

/** 一窗口的经营合计:营收(客户付你)、成本(你付上游)、毛利与毛利率。 */
export interface MarginTotals {
  requests: number;
  revenueUsd: number;
  costUsd: number;
  marginUsd: number;
  /** 毛利率 = 毛利/营收;营收为 0 时为 0 */
  marginRate: number;
}

/** 客户关注区一行:余额 + 窗口消耗 + 风险判定。 */
export interface CustomerRow {
  id: number;
  username: string;
  balanceUsd: number;
  spendUsd: number;
  requests: number;
  /** depleted 余额≤0(已被拒)/ low 撑不过一天 / ok */
  risk: 'depleted' | 'low' | 'ok';
  note: string;
}

/** GET /customers/focus:余额告警 + 窗口内消耗排行(仅 admin)。 */
export interface CustomerFocusData {
  window: string;
  atRisk: CustomerRow[];
  top: CustomerRow[];
}

export interface ModelUsageData {
  daily: MetricPoint[];
  /** 按渠道用量。chargeUsd 是该渠道实际向客户收的钱 —— 与 costUsd 相减即真实毛利
   *  (同一模型走不同渠道成本不同:渠道系数不同) */
  byChannel: { channelName: string; requests: number; costUsd: number; chargeUsd: number }[];
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
  /** 生成 Claude 配置时对外可见的网关基址;留空=按访问地址推断 */
  publicBaseUrl?: string;
  /** 网关计价币种:所有价格的展示币种,也是「应用官方价」的目标币种。默认 CNY。 */
  displayCurrency?: PriceCurrency;
  /** 人民币→美元换算率(手工维护,如 1 元 = 0.139 美元)。仅当官方价原币种与计价币种
   *  不一致时才用于折算;0=未设,此时拒绝折算(不臆造汇率)。 */
  usdPerCny?: number;
  /** 全局售价倍率:本站价 = 官方价 × 倍率(模型级 rateOverride 优先;模型未绑官方价时回落成本 × 倍率)。<=0/缺省 = 1.0 不加价。 */
  priceMultiplier?: number;
}

/* —— 官方定价(厂商官网) —— */

/** 官方计费形态:flat 单一价 / peak_offpeak 峰谷分时 / tiered 阶梯 / discount 限时折扣 */
export type BillingShape = 'flat' | 'peak_offpeak' | 'tiered' | 'discount';
export type PriceCurrency = 'CNY' | 'USD';

/** 官方参考价一行(原币种 / 百万 token)。分时类取空闲价为「生效默认」,明细在 detail。 */
export interface OfficialPrice {
  id: number;
  provider: Provider;
  modelName: string;
  sourceUrl: string;
  fetchedAt: string;
  currency: PriceCurrency;
  billingShape: BillingShape;
  inputPrice: number;
  outputPrice: number;
  cacheReadPrice: number;
  /** 缓存价由官方规则推导(非官方列,如通义) */
  cacheDerived: boolean;
  nativeText?: string;
  detail?: Record<string, unknown>;
  contentSha256?: string;
  note?: string;
  createdAt?: string;
  updatedAt?: string;
}

/** 官方价 + 与现有 offer 的比对(GET 读接口返回) */
export interface OfficialPriceView extends OfficialPrice {
  /** 按 settings.displayCurrency 换算后的计价金额(每百万 token)。
   *  字段名保留 *Usd 是历史命名,语义已是「当前计价币种的金额」。
   *  官方原币种与计价币种一致 → 原值直通;不一致 → 按汇率折算;折算不了则留 0。 */
  inputPriceUsd: number;
  outputPriceUsd: number;
  cacheReadPriceUsd: number;
  /** 金额可用:原币种与计价币种一致,或已按汇率折算成功 */
  rateSet: boolean;
  /** 已应用该官方价(来源 URL + 抓取时间匹配)的 offer */
  appliedOfferIds: number[];
}

/** POST /channels/{id}/fetch-pricing 返回。失败即失败:failed 非空且 upserted=0。 */
export interface FetchPricingResult {
  provider: Provider;
  sourceUrl: string;
  upserted: number;
  models: string[];
  failed?: string[];
  contentSha256?: string;
  /** 对账删掉的陈旧行数(官方页已不再列出的模型) */
  removed?: number;
}

/** 手工录入官方参考价(智谱等页面不可抓的厂商) */
export interface ManualPriceDraft {
  provider: Provider;
  modelName: string;
  sourceUrl: string;
  currency: PriceCurrency;
  inputPrice: number;
  outputPrice: number;
  cacheReadPrice?: number;
  nativeText?: string;
  note?: string;
}

export type Role = 'admin' | 'user';

export interface AdminMe {
  id: number;
  username: string;
  role: Role;
  createdAt: string;
}

/** 用户管理页行(管理员视角) */
export interface UserAccount {
  id: number;
  username: string;
  role: Role;
  keyCount: number;
  createdAt: string;
  /** 钱包余额(计价币种);仅普通用户有钱包,管理员恒为 0 */
  balanceUsd: number;
  /** 该用户名下令牌的额度上限(0 = 不限);只约束普通用户自助建令牌 */
  tokenQuotaCeiling: number;
  /** 该用户名下令牌的 RPM 上限(0 = 不限) */
  tokenRpmCeiling: number;
}

/** 账变流水行(GET /me/balance 与 GET /users/{id}/balance-logs) */
export interface BalanceLogItem {
  id: number;
  delta: number;
  balanceAfter: number;
  /** charge 扣费 | topup 充值 | adjust 调整 */
  reason: string;
  logId?: number;
  note?: string;
  createdAt: string;
}

/** GET /me/balance:余额 + 近期流水 */
export interface BalanceResp {
  balanceUsd: number;
  logs: BalanceLogItem[];
  /** 计价币种(用户读不到 /settings,靠这里决定余额符号) */
  currency?: PriceCurrency;
  /** 该账号名下令牌的额度上限(0 = 不限);用户建令牌时前端预校验 */
  tokenQuotaCeiling: number;
  /** 该账号名下令牌的 RPM 上限(0 = 不限) */
  tokenRpmCeiling: number;
}

/** GET /tokens/{id}/claude-config 返回 */
export interface ClaudeConfig {
  tokenId: number;
  baseUrl: string;
  settingsJson: string;
  modelAliases: Record<string, string>;
  warnings?: string[];
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
  /** 该渠道同步后总关联供给源数(与渠道列表「N 个模型」同口径) */
  modelCount: number;
}

/** 渠道额度单窗口(status==="ok" 时 percent 为已用百分比,0-100)。
 *  used/cap/resetAt 是上游给得出时才有的原始信息(commandcode 给 used/cap,opencode 给 resetsAt)。 */
export interface QuotaWindow {
  status: string;
  percent: number;
  used?: number;
  cap?: number;
  /** 窗口重置时间,已归一为 RFC3339 */
  resetAt?: string;
}

export type QuotaWindowKey = 'rolling' | 'weekly' | 'monthly';

/** 绝对余额型额度(DeepSeek / one-api 等只报「还剩多少钱」的上游)。
 *  currency 是上游原币种,不做折算 —— 汇率是手工维护的,不该拿它当余额前提。 */
export interface QuotaBalance {
  amount: number;
  currency: string;
}

/** GET /channels/{id}/quota 返回:windows 仅含可用窗口;available=false 时 error 给出原因。
 *  windows 与 balance 可同时有(one-api 既算得出百分比也报余额)。 */
export interface ChannelQuota {
  available: boolean;
  planName?: string;
  windows?: Partial<Record<QuotaWindowKey, QuotaWindow>>;
  balance?: QuotaBalance;
  latencyMs: number;
  error?: string;
}

export interface TokenCreateResult {
  key: string;
  token: GatewayToken;
}

/** POST /tokens/{id}/probe:令牌对某模型「能不能用」的静态自检(不访问上游、不计费)。 */
export interface TokenProbeResp {
  model: string;
  ok: boolean;
  checks: ProbeCheck[];
}

/** 自检的一项结果。 */
export interface ProbeCheck {
  name: string;
  ok: boolean;
  detail?: string;
}

/* —— 通知/公告 —— */

export type AnnouncementLevel = 'info' | 'warn' | 'danger';

/** 公告(管理员列表带已读计数;用户面弹窗读同一结构但无计数)。 */
export interface AnnouncementItem {
  id: number;
  title: string;
  body: string;
  level: AnnouncementLevel;
  enabled: boolean;
  /** 到点才可见;null = 立即发布 */
  publishAt: string | null;
  /** 到点即失效;null = 永不过期 */
  expiresAt: string | null;
  createdAt: string;
  updatedAt: string;
  /** 已确认人数(仅管理员列表) */
  readCount?: number;
  /** 站点普通用户总数(仅管理员列表) */
  userTotal?: number;
}

/** 创建/更新公告的提交体。 */
export interface AnnouncementDraft {
  title: string;
  body: string;
  level: AnnouncementLevel;
  enabled: boolean;
  publishAt: string | null;
  expiresAt: string | null;
}
