import { TOKENS } from '@/styles/tokens';

export const fmt = {
  n: (v: number) => v.toLocaleString('en-US'),
  usd: (v: number, d = 2) => `$${v.toFixed(d)}`,
  price: (v: number) => `$${v.toFixed(4)}`,
  pct: (v: number, d = 1) => `${(v * 100).toFixed(d)}%`,
  ms: (v: number) => (v >= 1000 ? `${(v / 1000).toFixed(2)}s` : `${Math.round(v)}ms`),
  ctx: (v: number) => (v >= 1000 ? `${Math.round(v / 1000)}K` : String(v)),
  k: (v: number) => (v >= 1000000 ? `${(v / 1000000).toFixed(2)}M` : v >= 1000 ? `${(v / 1000).toFixed(1)}K` : String(v)),
};

/** 空值统一占位，避免各页各写一个破折号 */
export const dash = (v: number | null | undefined, render: (n: number) => string) =>
  v === null || v === undefined ? '—' : render(v);

export const CAP_LABEL: Record<string, string> = {
  vision: '视觉',
  function: '函数调用',
  stream: '流式',
  reasoning: '推理',
};

/* ============================================================
   状态：颜色只是辅助，必须同时给出文字（无障碍红线）
   ============================================================ */
export type Tone = 'ok' | 'warn' | 'err' | 'aux';

export const TONE_COLOR: Record<Tone, string> = {
  ok: TOKENS.ok,
  warn: TOKENS.warn,
  err: TOKENS.err,
  aux: TOKENS.aux,
};

/** 渠道 / 令牌状态 → 文字 + 语义色 */
export const STATUS: Record<string, { t: string; tone: Tone }> = {
  healthy: { t: '健康', tone: 'ok' },
  degraded: { t: '降级', tone: 'warn' },
  down: { t: '不可用', tone: 'err' },
  disabled: { t: '已停用', tone: 'aux' },
  active: { t: '正常', tone: 'ok' },
  expired: { t: '已过期', tone: 'err' },
};

/** 兼容旧引用 */
export const STATUS_TEXT: Record<string, string> = Object.fromEntries(
  Object.entries(STATUS).map(([k, v]) => [k, v.t]),
);
export const STATUS_COLOR: Record<string, string> = Object.fromEntries(
  Object.entries(STATUS).map(([k, v]) => [k, TONE_COLOR[v.tone]]),
);

/* ============================================================
   失败归因 —— 与后端 proxy 的错误文案同源
   errCond = (status>=400 OR err IS NOT NULL) AND status<>499
   ============================================================ */
export type FailKind = 'upstream' | 'first' | 'idle' | 'canceled';

/** 客户端主动断开：不计入故障、不熔断渠道，恒单独列出 */
export const STATUS_CLIENT_CLOSED = 499;

export const FAIL_LABEL: Record<FailKind, string> = {
  upstream: '上游错误',
  first: '首字节超时',
  idle: '中途静默',
  canceled: '客户端中断 (499)',
};

export const FAIL_DESC: Record<FailKind, string> = {
  upstream: '上游返回 4xx/5xx，已按规则重试或降级',
  first: '候选超时窗口内未拿到响应头 → 504，此时尚未向客户端写字节，已换上游',
  idle: '流已开始后静默超窗口 → 504，无法再换上游，仅落账',
  canceled: '客户端主动断开，不计入故障、不熔断渠道',
};

export const FAIL_TONE: Record<FailKind, Tone> = {
  upstream: 'err',
  first: 'warn',
  idle: 'warn',
  canceled: 'aux',
};

/** 由日志 err 文本判定归因（文案见 proxy/gateway.go 的 504 分支）。 */
export function classifyError(err: string | null | undefined): FailKind {
  const s = (err ?? '').toLowerCase();
  if (s.includes('no data')) return 'first';
  if (s.includes('mid-stream')) return 'idle';
  return 'upstream';
}

/** 日志 err 文本 → 短标签，用于表格里的归因徽章。 */
export function failBadge(errText: string): { label: string; tone: Tone } {
  const kind = classifyError(errText);
  return { label: FAIL_LABEL[kind], tone: FAIL_TONE[kind] };
}
