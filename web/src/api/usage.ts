import { request } from './client'
import type { ListResponse, RequestLogRow, SummaryResp } from './types'

export interface RequestsQuery {
  from?: string
  to?: string
  protocol?: string
  model?: string
  upstream?: string
  status_bucket?: string
  sort?: string
  order?: string
  limit?: number
  offset?: number
}

export function listRequests(q: RequestsQuery): Promise<ListResponse<RequestLogRow>> {
  return request<ListResponse<RequestLogRow>>('GET', '/api/v1/usage/requests', { query: q })
}

export interface SummaryQuery {
  from?: string
  to?: string
  group_by?: string // 逗号分隔 model|upstream|protocol|client_key
  bucket?: string // hour|day
}

export function fetchSummary(q: SummaryQuery = {}): Promise<SummaryResp> {
  return request<SummaryResp>('GET', '/api/v1/usage/summary', { query: q })
}
