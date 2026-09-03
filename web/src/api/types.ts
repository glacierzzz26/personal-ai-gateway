// 网关管理 API 的 DTO(与 internal/config、internal/server 的 JSON 字段对齐)。

export type UpstreamType = 'openai' | 'anthropic'

export interface QuotaCfg {
  enabled: boolean
  window: string
  warn_used_pct: number
  hard_used_pct: number
  cache_ttl_sec: number
  invert_used_pct: boolean
}

// GET /upstreams 的行:api_key 已被掩码(env 引用原样 / 字面量只露头尾)。
// 提交(POST/PUT)用同形状,api_key 填明文;PUT 留空 = 保持原密钥。
export interface Upstream {
  name: string
  type: UpstreamType
  base_url: string
  api_key: string
  priority: number
  models: string[] | null
  cooldown_sec: number
  max_failures: number
  quota: QuotaCfg | null
}

export interface QuotaRow {
  upstream: string
  type: UpstreamType
  enabled: boolean
  // 以下仅在 enabled 时出现
  window?: string
  warn_used_pct?: number
  hard_used_pct?: number
  // 以下仅在 enabled 且已拉到快照时出现;否则为 null
  used_pct?: number | null
  status?: string | null // "ok" 为正常
  hard?: boolean
  resets_at?: string | null
}

export interface PingResult {
  reachable: boolean
  status?: number // 连接失败时后端 omitempty,无此字段
  message: string
}

export interface RequestLogRow {
  id: number
  ts: string // RFC3339Nano, UTC
  client_key: string
  client_tool: string
  protocol: string
  model: string
  upstream: string
  stream: boolean
  status: number
  prompt_tokens: number
  completion_tokens: number
  cache_read_tokens: number
  cost: number
  latency_ms: number
  error: string
}

export interface ReqMeta {
  limit: number
  offset: number
  total: number
  returned: number
}

export interface ListResponse<T> {
  meta: ReqMeta
  data: T[]
}

export interface SummaryMetrics {
  requests: number
  errors: number
  prompt_tokens: number
  completion_tokens: number
  cache_read_tokens: number
  cost: number
  avg_latency_ms: number
}

// 聚合行:除 7 个指标外,可能带 bucket(仅请求了 bucket)与各 group_by 维度列(值为字符串)。
export type SummaryRow = SummaryMetrics & {
  bucket?: string
  [dim: string]: string | number | undefined
}

export interface SummaryResp {
  meta: {
    from: string | null
    to: string | null
    group_by: string[] | null
    bucket: string
  }
  totals: SummaryMetrics
  data: SummaryRow[]
}

export interface ApiErrorBody {
  error?: {
    type?: string
    message?: string
  }
}
