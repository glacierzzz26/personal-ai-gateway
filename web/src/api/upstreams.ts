import { request } from './client'
import type { PingResult, Upstream } from './types'

async function unwrap(p: Promise<{ data: Upstream[] }>): Promise<Upstream[]> {
  return (await p).data
}

export function listUpstreams(): Promise<Upstream[]> {
  return unwrap(request<{ data: Upstream[] }>('GET', '/api/v1/upstreams'))
}

// POST:api_key 必填(字面值或 ${ENV} 均可)。
export function createUpstream(u: Upstream): Promise<Upstream> {
  return request<Upstream>('POST', '/api/v1/upstreams', { body: u })
}

// PUT:name 取自路径;api_key 传空 = 保持原密钥(由后端处理)。
export function updateUpstream(name: string, u: Upstream): Promise<Upstream> {
  return request<Upstream>('PUT', `/api/v1/upstreams/${encodeURIComponent(name)}`, { body: u })
}

export function deleteUpstream(name: string): Promise<void> {
  return request<void>('DELETE', `/api/v1/upstreams/${encodeURIComponent(name)}`)
}

export function testUpstream(name: string): Promise<PingResult> {
  return request<PingResult>('POST', `/api/v1/upstreams/${encodeURIComponent(name)}/test`)
}
