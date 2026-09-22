import { TOKENS } from '@/styles/tokens';
import { currentCurrency } from '@/stores/currency';
import type { PriceCurrency } from '@/types';

/** 币种符号。币种由 settings.displayCurrency 决定(见 stores/currency.ts)。 */
const SYMBOL: Record<PriceCurrency, string> = { CNY: '¥', USD: '$' };
const sym = () => SYMBOL[currentCurrency()];

/**
 * 金额小数位:默认 2 位。但 0 < v < 0.005 时 2 位会渲染成 0.00,把「很小的花费/单价」
 * 读成「免费」,这种小额退回 4 位(显式要求更多位时取较大者)。
 */
const dec = (v: number, d: number) => (v > 0 && v < 0.005 ? Math.max(d, 4) : d);

export const fmt = {
  n: (v: number) => v.toLocaleString('en-US'),
  /** 金额(默认 2 位小数),用于用量/花费等记账口径 —— 单位同计价币种。 */
  usd: (v: number, d = 2) => `${sym()}${v.toFixed(dec(v, d))}`,
  /** 单价(默认 2 位小数),用于每百万 token 的模型价格 —— 单位同计价币种。 */
  price: (v: number, d = 2) => `${sym()}${v.toFixed(dec(v, d))}`,
  pct: (v: number, d = 1) => `${(v * 100).toFixed(d)}%`,
  ms: (v: number) => (v >= 1000 ? `${(v / 1000).toFixed(2)}s` : `${Math.round(v)}ms`),
  ctx: (v: number) => (v >= 1000 ? `${Math.round(v / 1000)}K` : String(v)),
  k: (v: number) => (v >= 1000000 ? `${(v / 1000000).toFixed(2)}M` : v >= 1000 ? `${(v / 1000).toFixed(1)}K` : String(v)),
  /** RFC3339 → 本地「YYYY-MM-DD HH:mm」。用于官方价抓取时间留证展示。 */
  dt: (v?: string | null) => {
    if (!v) return '—';
    const d = new Date(v);
    if (Number.isNaN(d.getTime())) return v;
    const p = (n: number) => String(n).padStart(2, '0');
    return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
  },
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
  // 尚无流量 / 半开待复检 —— 没有证据说它健康,也没有证据说它坏,如实标灰。
  unknown: { t: '待观察', tone: 'aux' },
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
