import { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import Chart, { ChartLegend } from '@/components/Chart';
import RangePicker, { defaultRange, rangeLabel, toQuery } from '@/components/RangePicker';
import { Block as BlockCard, BlockBody, BlockHead, Blocks, MetricBlock } from '@/components/Block';
import PageHeader from '@/components/PageHeader';
import { ErrorState, SkLines } from '@/components/States';
import { Empty, Seg, Table } from '@/ui/primitives';
import type { Column } from '@/ui/primitives';
import { api } from '@/services/api';
import { useSession } from '@/stores/session';
import { fmt } from '@/utils/format';
import type { UsageRow } from '@/types';

/**
 * 自用统计(仅管理员)—— 站主**自己**用网关产生的用量与花费。
 *
 * 与「运行总览」的经营口径刻意分开:经营口径只算**客户归属**流量(role=user);
 * 这里只算**当前登录管理员自己名下**的流量(owner_id=me),且「花费」= **成本**
 * (你付上游的钱)—— 站主自用不计营收(不自扣钱包,见 proxy.resolveBilling),只有上游成本是真实的钱。
 *
 * 数据源 `GET /me/usage`(owner 作用域,行与曲线都带 costUsd);纯前端,后端无新接口。
 */

/** "2026-09-03" → "09-03";小时桶 "2026-09-03 20" → "20:00" */
const dayLabel = (ts: string) => (ts.length > 10 ? `${ts.slice(11)}:00` : ts.slice(5));

export default function SelfUsage() {
  const me = useSession(s => s.admin);
  const [dim, setDim] = useState<'model' | 'token'>('model');
  /** 统计窗口:预设 1/7/30 天或自定义区间;改动即重取用量与曲线 */
  const [range, setRange] = useState(defaultRange);
  const rq = useMemo(() => toQuery(range), [range]);
  const rl = rangeLabel(range);

  const usageQ = useQuery({
    queryKey: ['self-usage', dim, rq],
    queryFn: () => api.getMyUsage(dim, rq),
  });

  const rows = usageQ.data?.rows ?? [];
  const series = usageQ.data?.days ?? [];

  // 合计由 rows 求和(与下方表格同源);站主自用的模型/令牌维度数远小于后端 limit(50),不会截断。
  const totals = useMemo(() => {
    let requests = 0, inTokens = 0, outTokens = 0, cost = 0;
    for (const r of rows) {
      requests += r.requests;
      inTokens += r.inTokens;
      outTokens += r.outTokens;
      cost += r.costUsd ?? 0; // 花费 = 成本(你付上游);站主自用无售价/营收
    }
    return { requests, inTokens, outTokens, cost };
  }, [rows]);

  const usageColumns: Column<UsageRow>[] = [
    { key: 'name', title: dim === 'model' ? '模型' : '令牌', render: r => <span className="mono">{r.name}</span> },
    { key: 'requests', title: '请求数', align: 'right', render: r => <span className="num">{fmt.n(r.requests)}</span> },
    { key: 'inTokens', title: '输入 Token', align: 'right', render: r => <span className="num">{fmt.k(r.inTokens)}</span> },
    { key: 'outTokens', title: '输出 Token', align: 'right', render: r => <span className="num">{fmt.k(r.outTokens)}</span> },
    { key: 'costUsd', title: '花费', align: 'right', render: r => <span className="num">{fmt.usd(r.costUsd ?? 0)}</span> },
  ];

  return (
    <>
      <PageHeader
        title="自用统计"
        desc={`${me?.username ?? ''} 自己名下流量的用量与花费（成本口径 · 含站主自用，不含客户）`}
        extra={<RangePicker value={range} onChange={setRange} />}
      />

      <div className="kpi-grid" style={{ gridTemplateColumns: 'repeat(4, minmax(0,1fr))' }}>
        <MetricBlock label={`${rl}请求`} value={fmt.n(totals.requests)} note="仅统计你自己名下令牌" />
        <MetricBlock
          label={`${rl}Token`}
          value={fmt.k(totals.inTokens + totals.outTokens)}
          note={`入 ${fmt.k(totals.inTokens)} / 出 ${fmt.k(totals.outTokens)}`}
        />
        <MetricBlock label={`${rl}花费`} value={fmt.usd(totals.cost)} note="你付上游的成本（非客户营收）" />
        <MetricBlock
          label="平均单请求"
          value={totals.requests ? fmt.usd(totals.cost / totals.requests) : '—'}
          note="花费 ÷ 请求数"
        />
      </div>

      <div className="sec-head" style={{ marginTop: 20 }}>
        <div style={{ minWidth: 0 }}>
          <div className="sec-title">用量拆解</div>
          <div className="cell-sub" style={{ marginTop: 4 }}>
            按 {dim === 'model' ? '模型' : '令牌'} × {rl} 聚合请求与花费
          </div>
        </div>
        <Seg
          value={dim}
          onChange={v => setDim(v)}
          options={[
            { value: 'model' as const, label: '按模型' },
            { value: 'token' as const, label: '按令牌' },
          ]}
        />
      </div>

      <div style={{ margin: '14px 0' }}>
        <BlockCard>
          <BlockHead
            title={`每日请求与花费（${rl}）`}
            right={<ChartLegend items={[{ label: '请求数', color: 'var(--brand)' }, { label: '花费', color: 'var(--warn)' }]} />}
          />
          <BlockBody>
            {usageQ.isError ? (
              <ErrorState title="用量加载失败" desc="无法读取用量数据，请稍后重试。" onRetry={() => void usageQ.refetch()} />
            ) : (
              <Chart
                labels={series.map(d => dayLabel(d.ts))}
                height={280}
                leftFmt={fmt.k}
                rightFmt={v => fmt.usd(v)}
                series={[
                  { name: '请求数', data: series.map(d => d.requests), color: 'var(--brand)', kind: 'bar', fmt: v => fmt.n(v) },
                  {
                    name: '花费',
                    data: series.map(d => Number((d.costUsd ?? 0).toFixed(4))),
                    color: 'var(--warn)',
                    kind: 'line',
                    axis: 'right',
                    fmt: v => fmt.usd(v, 4),
                  },
                ]}
              />
            )}
          </BlockBody>
        </BlockCard>
      </div>

      <Blocks>
        <BlockCard>
          <BlockHead title="用量明细" sub={`按${dim === 'model' ? '模型' : '令牌'}聚合`} />
          <BlockBody style={{ padding: 0 }}>
            {usageQ.isFetching && rows.length === 0 ? (
              <div style={{ padding: 14 }}>
                <SkLines rows={['', 'w80', 'w60', 'w80']} />
              </div>
            ) : rows.length === 0 ? (
              <Empty
                icon="activity"
                title="所选范围内暂无自用流量"
                desc="你自己名下的令牌发起调用后，这里会按模型 / 令牌汇总请求与花费（成本）。"
              />
            ) : (
              <Table<UsageRow> columns={usageColumns} rows={rows} rowKey={r => r.name} />
            )}
          </BlockBody>
        </BlockCard>
      </Blocks>
    </>
  );
}
