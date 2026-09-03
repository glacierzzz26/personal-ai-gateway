// 统一 fetch 封装:相对 /api 基址(同源,无 CORS)、注入 Bearer、错误信封解析、401 全局回调。
import type { ApiErrorBody } from './types'

const KEY_SESSION = 'gw.api_key'

export function getKey(): string {
  try {
    return sessionStorage.getItem(KEY_SESSION) ?? ''
  } catch {
    return ''
  }
}

export function setKey(k: string): void {
  try {
    if (k) sessionStorage.setItem(KEY_SESSION, k)
    else sessionStorage.removeItem(KEY_SESSION)
  } catch {
    /* ignore */
  }
}

export function clearKey(): void {
  setKey('')
}

// 任一请求 401 → 全局回调(上层切回密钥门)。单一订阅者即可。
type UnauthorizedHandler = () => void
let unauthorizedHandler: UnauthorizedHandler | null = null
export function setUnauthorizedHandler(h: UnauthorizedHandler | null): void {
  unauthorizedHandler = h
}

export class ApiError extends Error {
  status: number
  type: string
  constructor(status: number, type: string, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.type = type
  }
}

// 页级友好文案:后端错误带 message;网络/解析错误给通用提示。
export function errMessage(e: unknown): string {
  return e instanceof ApiError ? e.message : '网络异常或网关不可达'
}

async function toApiError(resp: Response): Promise<ApiError> {
  let type = 'api_error'
  let message = `HTTP ${resp.status}`
  try {
    const body = (await resp.json()) as ApiErrorBody
    if (body?.error?.type) type = body.error.type
    if (body?.error?.message) message = body.error.message
  } catch {
    /* non-JSON error body */
  }
  return new ApiError(resp.status, type, message)
}

export interface RequestOpts {
  query?: object
  body?: unknown
  key?: string // 显式指定(登录校验用);缺省取 sessionStorage
  notify401?: boolean // 默认 true;登录校验传 false(错 key 不该把会话踢掉)
}

export async function request<T>(method: string, path: string, opts: RequestOpts = {}): Promise<T> {
  const url = new URL(path, window.location.origin)
  if (opts.query) {
    for (const [k, v] of Object.entries(opts.query)) {
      if (v === undefined || v === null || v === '') continue
      url.searchParams.set(k, String(v))
    }
  }
  const headers: Record<string, string> = {}
  const key = opts.key ?? getKey()
  if (key) headers['Authorization'] = `Bearer ${key}`
  if (opts.body !== undefined) headers['Content-Type'] = 'application/json'

  const resp = await fetch(url.toString(), {
    method,
    headers,
    body: opts.body === undefined ? undefined : JSON.stringify(opts.body),
  })
  if (resp.status === 401 && opts.notify401 !== false) unauthorizedHandler?.()
  if (!resp.ok) throw await toApiError(resp)
  if (resp.status === 204) return undefined as T
  return (await resp.json()) as T
}
