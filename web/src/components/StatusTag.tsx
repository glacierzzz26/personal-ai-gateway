import { Tag } from 'antd'
import type { UpstreamType } from '../api/types'

// HTTP 状态 → antd Tag 配色
export function statusColor(status: number): string {
  if (status >= 500) return 'red'
  if (status >= 400) return 'orange'
  if (status >= 300) return 'blue'
  return 'green'
}

export function statusTier(status: number): string {
  if (status >= 500) return '5xx'
  if (status >= 400) return '4xx'
  if (status >= 300) return '3xx'
  return '2xx'
}

export function StatusTag({ status }: { status: number }) {
  return <Tag color={statusColor(status)}>{status}</Tag>
}

export function ProtocolTag({ protocol }: { protocol: string }) {
  const color = protocol === 'openai' ? 'geekblue' : 'purple'
  return <Tag color={color}>{protocol}</Tag>
}

export function UpstreamTypeTag({ type }: { type: UpstreamType }) {
  return type === 'openai' ? <Tag color="geekblue">openai</Tag> : <Tag color="purple">anthropic</Tag>
}
