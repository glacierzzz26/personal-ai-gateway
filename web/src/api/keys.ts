import { request } from './client'
import type { ApiKeyCreated, ApiKeyRow } from './types'

async function unwrap(p: Promise<{ data: ApiKeyRow[] }>): Promise<ApiKeyRow[]> {
  return (await p).data
}

export function listKeys(): Promise<ApiKeyRow[]> {
  return unwrap(request<{ data: ApiKeyRow[] }>('GET', '/api/v1/keys'))
}

// 创建返回 201 + 一次性明文 secret:必须当场展示给用户,之后无处可查。
export function createKey(name: string, note: string): Promise<ApiKeyCreated> {
  return request<ApiKeyCreated>('POST', '/api/v1/keys', { body: { name, note } })
}

export function revokeKey(name: string): Promise<ApiKeyRow> {
  return request<ApiKeyRow>('POST', `/api/v1/keys/${encodeURIComponent(name)}/revoke`)
}
