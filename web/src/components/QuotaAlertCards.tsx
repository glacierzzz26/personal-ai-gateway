import { Flex } from 'antd'
import AlertCard from './AlertCard'
import type { QuotaRow } from '../api/types'

export interface QuotaIssue {
  key: string
  name: string
  severity: 'error' | 'warning'
  title: string
  description: string
}

// 由 /quota 行分析告警:硬耗尽或 status≠ok → error;used_pct≥warn → warning。bad = 需紧急关注数。
export function analyzeQuota(rows: QuotaRow[]): { issues: QuotaIssue[]; bad: number } {
  const issues: QuotaIssue[] = []
  let bad = 0
  for (const r of rows) {
    if (!r.enabled || r.used_pct == null) continue
    const abnormal = r.hard === true || (r.status != null && r.status !== 'ok')
    if (abnormal) {
      bad++
      issues.push({
        key: `err:${r.upstream}`,
        name: r.upstream,
        severity: 'error',
        title: r.hard === true ? '配额已耗尽' : '配额接口状态异常',
        description:
          r.hard === true
            ? `已用 ${r.used_pct}%(硬阈值 ${r.hard_used_pct ?? 95}%)。选路时该上游已从首选降为备选(尽力而为,不硬排除)。`
            : `状态 ${r.status ?? '?'},已用 ${r.used_pct}%。选路判定同「耗尽」处理。`,
      })
    } else if (r.used_pct >= (r.warn_used_pct ?? 80)) {
      issues.push({
        key: `warn:${r.upstream}`,
        name: r.upstream,
        severity: 'warning',
        title: '接近配额上限',
        description: `已用 ${r.used_pct}%(告警阈值 ${r.warn_used_pct ?? 80}%,硬阈值 ${r.hard_used_pct ?? 95}%)。`,
      })
    }
  }
  return { issues, bad }
}

export function QuotaAlertCards({
  rows,
  action,
}: {
  rows: QuotaRow[]
  action?: (issue: QuotaIssue) => React.ReactNode
}) {
  const { issues } = analyzeQuota(rows)
  if (issues.length === 0) {
    return (
      <AlertCard
        severity="success"
        title="一切正常"
        description="已启用配额的订阅源均低于告警阈值;未启用配额的订阅源不参与判定。"
      />
    )
  }
  return (
    <Flex vertical gap={8}>
      {issues.map((it) => (
        <AlertCard
          key={it.key}
          severity={it.severity}
          title={
            <>
              {it.name} · {it.title}
            </>
          }
          description={it.description}
          extra={action?.(it)}
        />
      ))}
    </Flex>
  )
}
