import { useMemo, useState } from 'react';
import { Card, Col, Empty, Row, Segmented, Select, Table } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useQuery } from '@tanstack/react-query';
import Chart from '@/components/Chart';
import { useChartColors } from '@/hooks/useChartColors';
import { api } from '@/services/api';
import { fmt } from '@/utils/format';
import { TOKENS } from '@/styles/tokens';
import type { EChartsOption } from 'echarts';
import type { UsageDim, UsageRow } from '@/types';

const DIM_LABEL: Record<UsageDim, string> = { model: '按模型', channel: '按渠道', token: '按令牌' };
const COL_LABEL: Record<UsageDim, string> = { model: '模型', channel: '渠道', token: '令牌' };

/** "2026-09-03" → "09-03" */
const dayLabel = (ts: string) => ts.slice(5);

/** 用量维度钻取面板(原「用量统计」页核心):仅保留拆解部分,无独立页头/汇总卡,供 Dashboard 嵌入。 */
export default function UsagePanel() {
  const c = useChartColors();
  const [dim, setDim] = useState<UsageDim>('model');
  const [days, setDays] = useState(7);

  const { data, isFetching } = useQuery({
    queryKey: ['usage', dim, days],
    queryFn: () => api.getUsage(dim, days),
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

  const barOption: EChartsOption = {
    grid: { left: 44, right: 44, top: 34, bottom: 26 },
    tooltip: {
      trigger: 'axis',
      backgroundColor: c.tooltipBg, borderColor: c.tooltipBorder,
      textStyle: { color: c.text, fontSize: 12 },
    },
    legend: {
      data: ['请求数', '花费'], right: 0, top: 0,
      itemWidth: 8, itemHeight: 8, textStyle: { color: c.text, fontSize: 12 },
    },
    xAxis: {
      type: 'category', data: series.map(d => dayLabel(d.ts)),
      axisLine: { lineStyle: { color: c.line } }, axisTick: { show: false },
      axisLabel: { color: c.text, fontSize: 11, interval: 'auto' },
    },
    yAxis: [
      {
        type: 'value', splitLine: { lineStyle: { color: c.line } },
        axisLabel: { color: c.text, fontSize: 11 },
      },
      {
        type: 'value', splitLine: { show: false },
        axisLabel: { color: c.text, fontSize: 11 },
      },
    ],
    series: [
      {
        name: '请求数', type: 'bar', data: series.map(d => d.requests),
        itemStyle: { color: c.primary, borderRadius: 3 }, barWidth: '45%',
      },
      {
        name: '花费', type: 'line', yAxisIndex: 1, data: series.map(d => Number(d.costUsd.toFixed(4))),
        showSymbol: false, lineStyle: { width: 1.8, color: c.warn }, itemStyle: { color: c.warn },
      },
    ],
  };

  const columns: ColumnsType<UsageRow> = [
    { title: COL_LABEL[dim], dataIndex: 'name', render: v => <span className="gw-mono">{v}</span> },
    {
      title: '请求数', dataIndex: 'requests', align: 'right', sorter: (a, b) => a.requests - b.requests,
      render: v => <span className="gw-num">{fmt.n(v)}</span>,
    },
    {
      title: '输入 Token', dataIndex: 'inTokens', align: 'right',
      render: v => <span className="gw-num">{fmt.k(v)}</span>,
    },
    {
      title: '输出 Token', dataIndex: 'outTokens', align: 'right',
      render: v => <span className="gw-num">{fmt.k(v)}</span>,
    },
    {
      title: '花费', dataIndex: 'costUsd', align: 'right', sorter: (a, b) => a.costUsd - b.costUsd,
      render: v => <span className="gw-num">{fmt.usd(v)}</span>,
    },
    {
      title: '花费占比', key: 'share', width: 180,
      render: (_, r) => (
        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <div className="gw-bar" style={{ flex: 1 }} role="img" aria-label={`花费占比 ${((r.costUsd / maxCost) * 100).toFixed(0)}%`}>
            <i style={{ width: `${(r.costUsd / maxCost) * 100}%`, background: 'var(--gw-primary)' }} />
          </div>
          <span className="gw-num" style={{ fontSize: 12, color: 'var(--gw-text-3)', width: 46 }}>
            {totals.cost ? ((r.costUsd / totals.cost) * 100).toFixed(1) : '0.0'}%
          </span>
        </div>
      ),
    },
    {
      title: '错误率', dataIndex: 'errorRate', align: 'right',
      /* 阈值与渠道/令牌侧统一：≥1% 视为需关注，≥0.5% 为观察区 */
      render: v => (
        <span className="gw-num" style={{ color: v > 0.01 ? TOKENS.err : v > 0.005 ? TOKENS.warn : undefined }}>
          {fmt.pct(v)}
        </span>
      ),
    },
  ];

  return (
    <>
      <div style={{ display: 'flex', alignItems: 'flex-start', gap: 16, margin: '20px 0 16px' }}>
        <div style={{ minWidth: 0 }}>
          <div style={{ fontSize: 15, fontWeight: 600 }}>用量拆解</div>
          <div style={{ fontSize: 12, color: 'var(--gw-text-3)', marginTop: 4 }}>
            按 {COL_LABEL[dim]} × 近 {days} 天聚合请求与成本
          </div>
        </div>
        <div style={{ marginLeft: 'auto', display: 'flex', gap: 8, alignItems: 'center', flexShrink: 0 }}>
          <Select
            style={{ width: 130 }} value={days}
            onChange={v => setDays(v)}
            options={[{ value: 7, label: '近 7 天' }, { value: 30, label: '近 30 天' }]}
          />
          <Segmented
            value={dim} onChange={v => setDim(v as UsageDim)}
            options={(['model', 'channel', 'token'] as UsageDim[]).map(d => ({ value: d, label: DIM_LABEL[d] }))}
          />
        </div>
      </div>

      <Row gutter={16} style={{ marginBottom: 16 }}>
        <Col xs={24} xl={16}>
          <Card title={`每日请求与花费(近 ${days} 天)`}>
            <Chart option={barOption} height={280} />
          </Card>
        </Col>
        <Col xs={24} xl={8}>
          <Card title="花费 Top 5">
            {costRows.length === 0 ? (
              <Empty description="暂无数据" image={Empty.PRESENTED_IMAGE_SIMPLE} style={{ padding: '24px 0' }} />
            ) : (
              costRows.slice(0, 5).map(r => (
                <div key={r.name} style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 12, fontSize: 13 }}>
                  <span className="gw-mono" style={{ width: 150, color: 'var(--gw-text-2)' }}>{r.name}</span>
                  <div className="gw-bar" style={{ flex: 1 }} role="img" aria-label={`花费占比 ${((r.costUsd / maxCost) * 100).toFixed(0)}%`}>
                    <i style={{ width: `${(r.costUsd / maxCost) * 100}%`, background: 'var(--gw-primary)' }} />
                  </div>
                  <span className="gw-num" style={{ width: 70, textAlign: 'right' }}>{fmt.usd(r.costUsd)}</span>
                </div>
              ))
            )}
          </Card>
        </Col>
      </Row>

      <Card>
        <Table<UsageRow>
          rowKey="name"
          size="middle"
          loading={isFetching && rows.length === 0}
          dataSource={rows}
          columns={columns}
          pagination={false}
          locale={{ emptyText: <Empty description={`所选时间范围内暂无${COL_LABEL[dim]}级别的用量记录`} image={Empty.PRESENTED_IMAGE_SIMPLE} /> }}
        />
      </Card>
    </>
  );
}
