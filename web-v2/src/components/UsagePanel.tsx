import { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import Chart, { ChartLegend } from '@/components/Chart';
import RangePicker, { defaultRange, rangeLabel, toQuery } from '@/components/RangePicker';
import { Card, CardBody, CardHead, Empty, Seg, SkLines, Table } from '@/ui/primitives';
import type { Column } from '@/ui/primitives';
import { api } from '@/services/api';
import { fmt } from '@/utils/format';
import type { UsageDim, UsageRow } from '@/types';

const DIM_LABEL: Record<UsageDim, string> = { model: '按模型', channel: '按渠道', token: '按令牌' };
const COL_LABEL: Record<UsageDim, string> = { model: '模型', channel: '渠道', token: '令牌' };

/** "2026-09-03" → "09-03" */
const dayLabel = (ts: string) => ts.slice(5);

/** 用量维度钻取面板(原「用量统计」页核心):仅保留拆解部分,无独立页头/汇总卡,供 Dashboard 嵌入。 */
export default function UsagePanel() {
  const [dim, setDim] = useState<UsageDim>('model');
  const [range, setRange] = useState(defaultRange);
  const [sort, setSort] = useState<{ key: 'requests' | 'costUsd'; dir: 1 | -1 } | null>(null);
  const rq = useMemo(() => toQuery(range), [range]);
  const rl = rangeLabel(range);

  const { data, isFetching } = useQuery({
    queryKey: ['usage', dim, rq],
    queryFn: () => api.getUsage(dim, rq),
  });
  const rows = data?.rows ?? [];
  const series = data?.days ?? [];

  const sortedRows = useMemo(() => {
    if (!sort) return rows;
    return [...rows].sort((a, b) => (a[sort.key] - b[sort.key]) * sort.dir);
  }, [rows, sort]);

  const onSort = (key: string) => {
    const k = key as 'requests' | 'costUsd';
    setSort(s => (s && s.key === k ? { key: k, dir: s.dir === 1 ? -1 : 1 } : { key: k, dir: 1 }));
  };

  // 后端按请求数倒序返回;Top-5 按花费另排。
  const costRows = useMemo(() => [...rows].sort((a, b) => b.costUsd - a.costUsd), [rows]);

  const totals = useMemo(() => {
    let requests = 0,
      cost = 0,
      errReqs = 0;
    for (const r of rows) {
      requests += r.requests;
      cost += r.costUsd;
      errReqs += r.requests * r.errorRate;
    }
    return { cost, errRate: requests ? errReqs / requests : 0 };
  }, [rows]);

  const maxCost = Math.max(...rows.map(r => r.costUsd), 1);

  const columns: Column<UsageRow>[] = [
    { key: 'name', title: COL_LABEL[dim], render: r => <span className="mono">{r.name}</span> },
    {
      key: 'requests',
      title: '请求数',
      align: 'right',
      sortable: true,
      render: r => <span className="num">{fmt.n(r.requests)}</span>,
    },
    { key: 'inTokens', title: '输入 Token', align: 'right', render: r => <span className="num">{fmt.k(r.inTokens)}</span> },
    { key: 'outTokens', title: '输出 Token', align: 'right', render: r => <span className="num">{fmt.k(r.outTokens)}</span> },
    {
      key: 'costUsd',
      title: '花费',
      align: 'right',
      sortable: true,
      render: r => <span className="num">{fmt.usd(r.costUsd)}</span>,
    },
    {
      key: 'share',
      title: '花费占比',
      width: 180,
      render: r => (
        <div className="hstack" style={{ gap: 8 }}>
          <div className="bar" style={{ flex: 1, marginTop: 0 }} role="img" aria-label={`花费占比 ${((r.costUsd / maxCost) * 100).toFixed(0)}%`}>
            <i style={{ width: `${(r.costUsd / maxCost) * 100}%` }} />
          </div>
          <span className="num cell-sub" style={{ width: 46 }}>
            {totals.cost ? ((r.costUsd / totals.cost) * 100).toFixed(1) : '0.0'}%
          </span>
        </div>
      ),
    },
    {
      key: 'errorRate',
      title: '错误率',
      align: 'right',
      /* 阈值与渠道/令牌侧统一：≥1% 视为需关注，≥0.5% 为观察区 */
      render: r => (
        <span
          className="num"
          style={{ color: r.errorRate > 0.01 ? 'var(--err)' : r.errorRate > 0.005 ? 'var(--warn)' : undefined }}
        >
          {fmt.pct(r.errorRate)}
        </span>
      ),
    },
  ];

  return (
    <>
      <div className="sec-head" style={{ marginTop: 18 }}>
        <div style={{ minWidth: 0 }}>
          <div className="sec-title">用量拆解</div>
          <div className="cell-sub" style={{ marginTop: 4 }}>
            按 {COL_LABEL[dim]} × {rl} 聚合请求与成本
          </div>
        </div>
        <div className="hstack wrap" style={{ gap: 8 }}>
          <RangePicker value={range} onChange={setRange} size="sm" />
          <Seg
            value={dim}
            onChange={v => setDim(v)}
            options={(['model', 'channel', 'token'] as UsageDim[]).map(d => ({ value: d, label: DIM_LABEL[d] }))}
          />
        </div>
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: 'minmax(0,2fr) minmax(0,1fr)', gap: 14, margin: '14px 0' }}>
        <Card>
          <CardHead
            title={`每日请求与花费（${rl}）`}
            right={<ChartLegend items={[{ label: '请求数', color: 'var(--brand)' }, { label: '花费', color: 'var(--warn)' }]} />}
          />
          <CardBody>
            <Chart
              labels={series.map(d => dayLabel(d.ts))}
              height={280}
              leftFmt={fmt.k}
              rightFmt={v => fmt.usd(v)}
              series={[
                { name: '请求数', data: series.map(d => d.requests), color: 'var(--brand)', kind: 'bar', fmt: v => fmt.n(v) },
                {
                  name: '花费',
                  data: series.map(d => Number(d.costUsd.toFixed(4))),
                  color: 'var(--warn)',
                  kind: 'line',
                  axis: 'right',
                  fmt: v => fmt.usd(v, 4),
                },
              ]}
            />
          </CardBody>
        </Card>
        <Card>
          <CardHead title="花费 Top 5" />
          <CardBody>
            {costRows.length === 0 ? (
              <Empty title="暂无数据" />
            ) : (
              costRows.slice(0, 5).map(r => (
                <div key={r.name} className="li">
                  <div className="r1">
                    <span className="k mono">{r.name}</span>
                    <span className="v">{fmt.usd(r.costUsd)}</span>
                  </div>
                  <div className="bar" role="img" aria-label={`花费占比 ${((r.costUsd / maxCost) * 100).toFixed(0)}%`}>
                    <i style={{ width: `${(r.costUsd / maxCost) * 100}%` }} />
                  </div>
                </div>
              ))
            )}
          </CardBody>
        </Card>
      </div>

      <Card>
        <CardHead title="用量明细" count={rows.length} />
        <CardBody style={{ padding: 0 }}>
          {isFetching && rows.length === 0 ? (
            <div style={{ padding: 14 }}>
              <SkLines rows={['', 'w80', 'w60', 'w80', 'w60']} />
            </div>
          ) : rows.length === 0 ? (
            <Empty icon="activity" title={`所选时间范围内暂无${COL_LABEL[dim]}级别的用量记录`} />
          ) : (
            <Table<UsageRow>
              columns={columns}
              rows={sortedRows}
              rowKey={r => r.name}
              sort={sort ?? undefined}
              onSort={onSort}
            />
          )}
        </CardBody>
      </Card>
    </>
  );
}
