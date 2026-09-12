import { useQueries } from '@tanstack/react-query';
import { api } from '@/services/api';
import type { FailKind } from '@/utils/format';

export interface FailBucket {
  kind: FailKind;
  count: number;
}

export interface FailureAttribution {
  buckets: FailBucket[];
  /** 计入故障的失败合计（上游错误 + 首字节超时 + 中途静默），不含 499 */
  faultTotal: number;
  /** 客户端中断，单独列出：不计入故障、不熔断渠道 */
  canceled: number;
  isLoading: boolean;
  isError: boolean;
  refetch: () => void;
}

/**
 * 失败归因拆桶。
 *
 * 后端没有专门的归因接口，但 `/logs` 的 `total` + `kw`（对 err 文本做子串匹配）
 * 足以把 errCond 的三类拆开，且口径与后端 SQL 完全一致（不含 499）：
 *   first = status=error & kw="no data"        （gateway.go: "sent no data within the first-byte window"）
 *   idle  = status=error & kw="mid-stream"     （gateway.go: "stalled mid-stream"）
 *   upstream = error 总数 − first − idle
 *   canceled = status=canceled（即 499）
 *
 * 只取 total，所以 size=1 即可，不拉回任何明细行。
 */
export function useFailureAttribution(enabled = true): FailureAttribution {
  const results = useQueries({
    queries: [
      { key: 'errorTotal', filters: { status: 'error' as const } },
      { key: 'first', filters: { status: 'error' as const, kw: 'no data' } },
      { key: 'idle', filters: { status: 'error' as const, kw: 'mid-stream' } },
      { key: 'canceled', filters: { status: 'canceled' as const } },
    ].map(q => ({
      queryKey: ['fail-attr', q.key],
      queryFn: () => api.getLogs(q.filters, 1, 1),
      enabled,
      staleTime: 15_000,
      retry: 0,
    })),
  });

  const [errQ, firstQ, idleQ, cancelQ] = results;
  const total = errQ.data?.total ?? 0;
  const first = firstQ.data?.total ?? 0;
  const idle = idleQ.data?.total ?? 0;
  // 防御：子串统计理论上不会超过总数，异常时不出现负数
  const upstream = Math.max(0, total - first - idle);

  return {
    buckets: [
      { kind: 'upstream', count: upstream },
      { kind: 'first', count: first },
      { kind: 'idle', count: idle },
      { kind: 'canceled', count: cancelQ.data?.total ?? 0 },
    ],
    faultTotal: upstream + first + idle,
    canceled: cancelQ.data?.total ?? 0,
    isLoading: results.some(r => r.isPending),
    isError: results.some(r => r.isError),
    refetch: () => results.forEach(r => void r.refetch()),
  };
}
