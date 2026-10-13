import { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import Chart from '@/components/Chart';
import RangePicker, { defaultRange, rangeLabel, toQuery } from '@/components/RangePicker';
import { Card, Kpi, Empty, SkLines, Btn, PageHead, Seg } from '@/ui/primitives';
import { useChartColors } from '@/hooks/useChartColors';
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

const chartAxis = (v: number) =>
  v >= 1000 ? `${(v / 1000).toFixed(1).replace(/\.0$/, '')}k` : String(Math.round(v));

export default function SelfUsage() {
  const me = useSession(s => s.admin);
  const c = useChartColors();
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

  const chartData = {
    labels: series.map(d => dayLabel(d.ts)),
    series: [
      { name: '请求数', data: series.map(d => d.requests), color: c.primary, fill: false },
      { name: '花费', data: series.map(d => Number((d.costUsd ?? 0).toFixed(4))), color: c.warn, fill: true },
    ],
    yFmt: chartAxis,
    tipFmt: (v: number) => fmt.usd(v),
  };

  return (
    <div className="gw-page">
      <PageHead
        title="自用统计"
        sub={`${me?.username ?? ''} 自己名下流量的用量与花费（成本口径 · 含站主自用，不含客户）`}
        actions={<RangePicker value={range} onChange={setRange} />}
      />

      {/* 指标条 */}
      <div className="grid g-12" style={{ gap: 10 }}>
        <div className="c3">
          <Kpi
            label={`${rl}请求`}
            value={fmt.n(totals.requests)}
            foot={<span className="cell-sub" style={{ whiteSpace: 'normal' }}>仅统计你自己名下令牌</span>}
          />
        </div>
        <div className="c3">
          <Kpi
            label={`${rl}Token`}
            value={fmt.k(totals.inTokens + totals.outTokens)}
            foot={<span className="cell-sub" style={{ whiteSpace: 'normal' }}>{`入 ${fmt.k(totals.inTokens)} / 出 ${fmt.k(totals.outTokens)}`}</span>}
          />
        </div>
        <div className="c3">
          <Kpi
            label={`${rl}花费`}
            value={fmt.usd(totals.cost)}
            foot={<span className="cell-sub" style={{ whiteSpace: 'normal' }}>你付上游的成本（非客户营收）</span>}
          />
        </div>
        <div className="c3">
          <Kpi
            label="平均单请求"
            value={totals.requests ? fmt.usd(totals.cost / totals.requests) : '—'}
            foot={<span className="cell-sub" style={{ whiteSpace: 'normal' }}>花费 ÷ 请求数</span>}
          />
        </div>
      </div>

      {/* 用量拆解标题 + 维度切换 */}
      <div style={{ display: 'flex', alignItems: 'flex-start', gap: 16, margin: '20px 0 16px', flexWrap: 'wrap' }}>
        <div style={{ minWidth: 0 }}>
          <div style={{ fontSize: 15, fontWeight: 600, color: 'var(--t1)' }}>用量拆解</div>
          <div className="cell-sub" style={{ marginTop: 4 }}>
            按 {dim === 'model' ? '模型' : '令牌'} × {rl} 聚合请求与花费
          </div>
        </div>
        <div style={{ marginLeft: 'auto', display: 'flex', gap: 8, alignItems: 'center', flexShrink: 0 }}>
          <Seg
            value={dim}
            onChange={v => setDim(v as 'model' | 'token')}
            options={[
              { v: 'model', label: '按模型' },
              { v: 'token', label: '按令牌' },
            ]}
          />
        </div>
      </div>

      {/* 每日请求与花费曲线（成本口径） */}
      <Card icon="activity" title={`每日请求与花费(${rl})`}>
        {usageQ.isError ? (
          <Empty icon="warn" title="用量加载失败">
            无法读取用量数据，请稍后重试。
            <Btn size="sm" style={{ marginTop: 12 }} onClick={() => void usageQ.refetch()}>重试</Btn>
          </Empty>
        ) : usageQ.isLoading && series.length === 0 ? (
          <SkLines rows={['w80', 'w60', 'w80', 'w40']} />
        ) : (
          <Chart labels={chartData.labels} series={chartData.series} h={280} yFmt={chartData.yFmt} tipFmt={chartData.tipFmt} />
        )}
      </Card>

      {/* 用量明细表 */}
      <div style={{ marginTop: 12 }}>
        <Card icon="table2" title="用量明细" note={`按${dim === 'model' ? '模型' : '令牌'}聚合`}>
          {usageQ.isError ? (
            <Empty icon="warn" title="用量加载失败">
              无法读取用量数据，请稍后重试。
              <Btn size="sm" style={{ marginTop: 12 }} onClick={() => void usageQ.refetch()}>重试</Btn>
            </Empty>
          ) : rows.length === 0 ? (
            <Empty icon="box" title="所选范围内暂无自用流量">
              你自己名下的令牌发起调用后，这里会按模型 / 令牌汇总请求与花费（成本）。
            </Empty>
          ) : (
            <div className="table-wrap">
              <table className="tb">
                <thead>
                  <tr>
                    <th>{dim === 'model' ? '模型' : '令牌'}</th>
                    <th className="right">请求数</th>
                    <th className="right">输入 Token</th>
                    <th className="right">输出 Token</th>
                    <th className="right">花费</th>
                  </tr>
                </thead>
                <tbody>
                  {rows.map((r: UsageRow) => (
                    <tr key={r.name}>
                      <td className="strong mono">{r.name}</td>
                      <td className="right mono">{fmt.n(r.requests)}</td>
                      <td className="right mono">{fmt.k(r.inTokens)}</td>
                      <td className="right mono">{fmt.k(r.outTokens)}</td>
                      <td className="right mono">{fmt.usd(r.costUsd ?? 0)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </Card>
      </div>
    </div>
  );
}
