export const fmt = {
  n: (v: number) => v.toLocaleString('en-US'),
  usd: (v: number, d = 2) => `$${v.toFixed(d)}`,
  price: (v: number) => `$${v.toFixed(4)}`,
  pct: (v: number, d = 1) => `${(v * 100).toFixed(d)}%`,
  ms: (v: number) => (v >= 1000 ? `${(v / 1000).toFixed(2)}s` : `${Math.round(v)}ms`),
  ctx: (v: number) => (v >= 1000 ? `${Math.round(v / 1000)}K` : String(v)),
  k: (v: number) => (v >= 1000000 ? `${(v / 1000000).toFixed(2)}M` : v >= 1000 ? `${(v / 1000).toFixed(1)}K` : String(v)),
};

export const CAP_LABEL: Record<string, string> = {
  vision: '视觉',
  function: '函数调用',
  stream: '流式',
  reasoning: '推理',
};

export const STATUS_TEXT: Record<string, string> = {
  healthy: '健康',
  degraded: '降级',
  down: '不可用',
  disabled: '已停用',
  active: '正常',
  expired: '已过期',
};

export const STATUS_COLOR: Record<string, string> = {
  healthy: '#16A34A',
  degraded: '#F59E0B',
  down: '#EF4444',
  disabled: '#94A3B8',
  active: '#16A34A',
  expired: '#EF4444',
};
