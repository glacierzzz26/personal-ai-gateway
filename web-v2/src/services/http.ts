/**
 * 与网关管理面同源的 fetch 封装。
 * - baseURL `/api/v1`(同源会话 cookie;开发期 Vite 代理到 :8787)
 * - 非 2xx 抛出 HttpError({status, type, message}),由调用方 message 提示
 * - 401 触发 onUnauthorized(仅当此前已登录,由 SessionGate 注册)
 */

const baseURL = '/api/v1';

export class HttpError extends Error {
  status: number;
  type: string;
  constructor(status: number, type: string, message: string) {
    super(message);
    this.status = status;
    this.type = type;
  }
}

type UnauthorizedHandler = () => void;
let onUnauthorized: UnauthorizedHandler | null = null;
export function setUnauthorizedHandler(h: UnauthorizedHandler | null) { onUnauthorized = h; }

interface ApiErrorBody { error?: { type?: string; message?: string } }

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const init: RequestInit = {
    method,
    credentials: 'include',
    headers: { Accept: 'application/json' },
  };
  if (body !== undefined) {
    init.headers = { ...init.headers, 'Content-Type': 'application/json' };
    init.body = JSON.stringify(body);
  }
  const resp = await fetch(baseURL + path, init);
  const text = await resp.text();
  let data: unknown = null;
  if (text) {
    try { data = JSON.parse(text); } catch { /* 非 JSON(如 HTML)保持 null */ }
  }
  if (!resp.ok) {
    const eb = (data as ApiErrorBody | null)?.error;
    const err = new HttpError(resp.status, eb?.type ?? 'http', eb?.message ?? resp.statusText);
    if (resp.status === 401 && onUnauthorized) onUnauthorized();
    throw err;
  }
  return data as T;
}

export const http = {
  get: <T>(path: string) => request<T>('GET', path),
  post: <T>(path: string, body?: unknown) => request<T>('POST', path, body),
  patch: <T>(path: string, body?: unknown) => request<T>('PATCH', path, body),
  put: <T>(path: string, body?: unknown) => request<T>('PUT', path, body),
  del: <T>(path: string) => request<T>('DELETE', path),
};
