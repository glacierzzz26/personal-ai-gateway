import { useMemo, useState } from 'react';
import { Card, Empty, Seg, Th } from '@/ui/primitives';
import { api } from '@/services/api';
import Chart from '@/components/Chart';
import RangePicker, { defaultRange, rangeLabel, toQuery } from '@/components/RangePicker';
import { useQuery } from '@tanstack/react-query';
import { fmt } from '@/utils/format';
import type { UsageDim } from '@/types';

const DIM_LABEL: Record<UsageDim, string> = { model: '按模型', channel: '按渠道', token: '按令牌' };
const COL_LABEL: Record<UsageDim, string> = { model: '模型', channel: '渠道', token: '令牌' };

/** "2026-09-03" → "09-03" */
const dayLabel = (ts: string) => ts.slice(5);

type SortKey = 'requests' | 'costUsd';

/** 用量维度钻取面板(原「用量统计」页核心):仅保留拆解部分,无独立页头/汇总卡,供 Dashboard 嵌入。 */
export default function UsagePanel() {
  const [dim, setDim] = useState<UsageDim>('model');
  const [range, setRange] = useState(defaultRange);
  const rq = useMemo(() => toQuery(range), [range]);
  const rl = rangeLabel(range);
  const [sort, setSort] = useState<{ key: SortKey | null; dir: 'asc' | 'desc' }>({ key: null, dir: 'desc' });

  const { data, isFetching } = useQuery({
    queryKey: ['usage', dim, rq],
    queryFn: () => api.getUsage(dim, rq),
  });
  const rows = data?.rows ?? [];
  const series = data?.days ?? [];

  // 后端按请求数倒序返回;Top-5 按花费另排。
  const costRows = useMemo(() => [...rows].sort((a, b) => b.costUsd - a.costUsd), [rows]);

  const totals = useMemo(() => {
    let requests = 0, inTokens = 0, outTokens = 0, cost = 0, errReqs = 0;
    for (const r of rows) {
      requests += r.requests;
      inTokens += r.inTokens;
      outTokens += r.outTokens;
      cost += r.costUsd;
      errReqs += r.requests * r.errorRate;
    }
    return { requests, inTokens, outTokens, cost, errRate: requests ? errReqs / requests : 0 };
  }, [rows]);

  const maxCost = Math.max(...rows.map(r => r.costUsd), 1);

  const sortedRows = useMemo(() => {
    if (!sort.key) return rows;
    const arr = [...rows];
    arr.sort((a, b) => (sort.dir === 'asc' ? a[sort.key!] - b[sort.key!] : b[sort.key!] - a[sort.key!]));
    return arr;
  }, [rows, sort]);

  const toggleSort = (k: SortKey) =>
    setSort(prev => ({ key: k, dir: prev.key === k && prev.dir === 'desc' ? 'asc' : 'desc' }));

  // 双轴(请求数 bar + 花费 line)→ 近似为单轴双 series。
  const labels = series.map(d => dayLabel(d.ts));
  const usageSeries = [
    { name: '请求数', data: series.map(d => d.requests), color: 'var(--brand)' },
    { name: '花费', data: series.map(d => Number((d.costUsd ?? 0).toFixed(4))), color: 'var(--warn)' },
  ];

  const shareBar = (pct: number, color = 'var(--brand)') => (
    <div style={{ flex: 1, height: 6, background: 'var(--panel-3)', border: '1px solid var(--line)', borderRadius: 2, overflow: 'hidden' }}>
      <i style={{ display: 'block', height: '100%', width: `${pct}%`, background: color }} />
    </div>
  );

  return (
    <>
      <div style={{ display: 'flex', alignItems: 'flex-start', gap: 16, margin: '20px 0 16px' }}>
        <div style={{ minWidth: 0 }}>
          <div style={{ fontSize: 15, fontWeight: 600 }}>用量拆解</div>
          <div className="cell-sub" style={{ marginTop: 4 }}>
            按 {COL_LABEL[dim]} × {rl} 聚合请求与成本
          </div>
        </div>
        <div style={{ marginLeft: 'auto', display: 'flex', gap: 8, alignItems: 'center', flexShrink: 0 }}>
          <RangePicker value={range} onChange={setRange} size="small" />
          <Seg
            value={dim}
            onChange={v => setDim(v as UsageDim)}
            options={(['model', 'channel', 'token'] as UsageDim[]).map(d => ({ v: d, label: DIM_LABEL[d] }))}
          />
        </div>
      </div>

      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 16, marginBottom: 16 }}>
        <Card title={`每日请求与花费(${rl})`} bodyClass="" style={{ flex: '2 1 360px', minWidth: 0 }}>
          <Chart labels={labels} series={usageSeries} h={280} yFmt={fmt.k} tipFmt={fmt.n} />
        </Card>
        <Card title="花费 Top 5" bodyClass="" style={{ flex: '1 1 260px', minWidth: 0 }}>
          {costRows.length === 0 ? (
            <Empty icon="scroll" title="暂无数据" />
          ) : (
            costRows.slice(0, 5).map(r => (
              <div key={r.name} style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 12, fontSize: 13 }}>
                <span className="mono" style={{ width: 150, color: 'var(--t2)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{r.name}</span>
                {shareBar((r.costUsd / maxCost) * 100)}
                <span className="num" style={{ width: 70, textAlign: 'right' }}>{fmt.usd(r.costUsd)}</span>
              </div>
            ))
          )}
        </Card>
      </div>

      <Card>
        {isFetching && rows.length === 0 ? (
          <div style={{ padding: '20px 0' }}><span className="sk" style={{ display: 'block', width: '100%', height: 200 }} /></div>
        ) : sortedRows.length === 0 ? (
          <Empty icon="table2" title={`所选时间范围内暂无${COL_LABEL[dim]}级别的用量记录`} />
        ) : (
          <div className="table-wrap">
            <table className="tb">
              <thead>
                <tr>
                  <th>{COL_LABEL[dim]}</th>
                  <Th right sortable dir={sort.key === 'requests' ? sort.dir : null} onClick={() => toggleSort('requests')}>请求数</Th>
                  <th className="right">输入 Token</th>
                  <th className="right">输出 Token</th>
                  <Th right sortable dir={sort.key === 'costUsd' ? sort.dir : null} onClick={() => toggleSort('costUsd')}>花费</Th>
                  <th style={{ width: 180 }}>花费占比</th>
                  <th className="right">错误率</th>
                </tr>
              </thead>
              <tbody>
                {sortedRows.map(r => (
                  <tr key={r.name}>
                    <td><span className="mono">{r.name}</span></td>
                    <td className="right num">{fmt.n(r.requests)}</td>
                    <td className="right num">{fmt.k(r.inTokens)}</td>
                    <td className="right num">{fmt.k(r.outTokens)}</td>
                    <td className="right num">{fmt.usd(r.costUsd)}</td>
                    <td>
                      <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                        {shareBar((r.costUsd / maxCost) * 100)}
                        <span className="num" style={{ fontSize: 12, color: 'var(--t3)', width: 46 }}>
                          {totals.cost ? ((r.costUsd / totals.cost) * 100).toFixed(1) : '0.0'}%
                        </span>
                      </div>
                    </td>
                    <td className="right num" style={{ color: r.errorRate > 0.01 ? 'var(--err)' : r.errorRate > 0.005 ? 'var(--warn)' : undefined }}>
                      {fmt.pct(r.errorRate)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
    </>
  );
}
