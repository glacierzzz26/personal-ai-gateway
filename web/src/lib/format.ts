import dayjs from './dayjs'

// 展示层统一格式化。金额默认 $ 4 位;数额大了自动降精度,避免挤爆单元格。
export function fmtInt(n?: number | null): string {
  return (n ?? 0).toLocaleString('zh-CN')
}

export function fmtUSD(v?: number | null): string {
  if (v == null) return '$0'
  const abs = Math.abs(v)
  const digits = abs === 0 ? 2 : abs >= 1000 ? 2 : abs >= 1 ? 3 : 4
  return '$' + v.toFixed(digits)
}

export function fmtUSDCompact(v?: number | null): string {
  if (v == null || v === 0) return '$0'
  const abs = Math.abs(v)
  if (abs >= 1e6) return '$' + (v / 1e6).toFixed(2) + 'M'
  if (abs >= 1e3) return '$' + (v / 1e3).toFixed(2) + 'k'
  return '$' + v.toFixed(2)
}

export function fmtMs(n?: number | null): string {
  return n == null ? '—' : fmtInt(n) + ' ms'
}

// RFC3339Nano(UTC) → 本地时间展示
export function fmtTime(iso?: string | null): string {
  return iso ? dayjs(iso).format('YYYY-MM-DD HH:mm:ss') : '—'
}

export function fmtTimeShort(iso?: string | null): string {
  return iso ? dayjs(iso).format('MM-DD HH:mm') : '—'
}

// 相对“多久后重置”的友好文案
export function fmtCountdown(iso?: string | null): string {
  if (!iso) return '—'
  const d = dayjs(iso)
  const diffH = d.diff(dayjs(), 'hour', true)
  if (diffH <= 0) return '即将重置'
  if (diffH < 24) return `${Math.round(diffH)} 小时后重置`
  const days = Math.floor(diffH / 24)
  return `${days} 天 ${Math.round(diffH % 24)} 小时后重置`
}
