import { request } from './client'
import type { QuotaRow } from './types'

export async function listQuota(): Promise<QuotaRow[]> {
  const r = await request<{ data: QuotaRow[] }>('GET', '/api/v1/quota')
  return r.data
}

// 登录校验:给显式 key,走 401 不触发全局会话踢出。
export function validateKey(key: string): Promise<{ data: QuotaRow[] }> {
  return request<{ data: QuotaRow[] }>('GET', '/api/v1/quota', { key, notify401: false })
}
