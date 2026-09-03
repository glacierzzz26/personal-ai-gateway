import { useMemo, useState } from 'react';
import { Card, Col, Row, Segmented, Select, Table } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useQuery } from '@tanstack/react-query';
import PageHeader from '@/components/PageHeader';
import Chart from '@/components/Chart';
import { useChartColors } from '@/hooks/useChartColors';
import { api } from '@/services/api';
import { fmt } from '@/utils/format';
import type { EChartsOption } from 'echarts';
import type { UsageRow } from '@/types';

export default function Usage() {
  const c = useChartColors();
  const [dim, setDim] = useState('按模型');
  const { data, isLoading } = useQuery({ queryKey: ['usage'], queryFn: api.getUsage });
  const rows = data?.rows ?? [];
  const days = data?.days ?? [];

  const totals = useMemo(() => ({
    requests: rows.reduce((a, b) => a + b.requests, 0),
    tokens: rows.reduce((a, b) => a + b.inTokens + b.outTokens, 0),
    cost: rows.reduce((a, b) => a + b.costUsd, 0),
  }), [rows]);

  const maxCost = Math.max(...rows.map(r => r.costUsd), 1);

  const barOption: EChartsOption = {
    grid: { left: 48, right: 12, top: 14, bottom: 26 },
    tooltip: {
      trigger: 'axis', axisPointer: { type: 'shadow' },
      backgroundColor: c.tooltipBg, borderColor: c.tooltipBorder,
      textStyle: { color: c.text, fontSize: 12 },
    },
    xAxis: {
      type: 'category', data: days.map(d => d.ts),
      axisLine: { lineStyle: { color: c.line } }, axisTick: { show: false },
      axisLabel: { color: c.text, fontSize: 11 },
    },
    yAxis: {
      type: 'value', splitLine: { lineStyle: { color: c.line } },
      axisLabel: { color: c.text, fontSize: 11 },
    },
    series: [{
      name: '花费', type: 'bar', data: days.map(d => Number(d.costUsd.toFixed(2))),
      itemStyle: { color: c.primary, borderRadius: 3 }, barWidth: '45%',
    }],
  };

  const columns: ColumnsType<UsageRow> = [
    { title: dim === '按模型' ? '模型' : dim === '按渠道' ? '渠道' : '令牌', dataIndex: 'name', render: v => <span className="gw-mono">{v}</span> },
    {
      title: '请求数', dataIndex: 'requests', align: 'right',
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
      title: '花费', dataIndex: 'costUsd', align: 'right',
      render: v => <span className="gw-num">{fmt.usd(v)}</span>,
    },
    {
      title: '占比', key: 'share', width: 180,
      render: (_, r) => (
        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <div style={{ flex: 1, height: 4, borderRadius: 2, background: 'var(--gw-fill)', overflow: 'hidden' }}>
            <div style={{ height: '100%', width: `${(r.costUsd / maxCost) * 100}%`, background: 'var(--gw-primary)', borderRadius: 2 }} />
          </div>
          <span className="gw-num" style={{ fontSize: 12, color: 'var(--gw-text-3)', width: 46 }}>
            {totals.cost ? ((r.costUsd / totals.cost) * 100).toFixed(1) : '0.0'}%
          </span>
        </div>
      ),
    },
    {
      title: '错误率', dataIndex: 'errorRate', align: 'right',
      render: v => (
        <span className="gw-num" style={{ color: v > .005 ? '#F59E0B' : undefined }}>{fmt.pct(v)}</span>
      ),
    },
  ];

  return (
    <div className="gw-page">
      <PageHeader
        title="用量统计"
        desc="按模型 / 渠道 / 令牌维度拆解调用与成本"
        extra={
          <>
            <Select
              style={{ width: 120 }} defaultValue="7d"
              options={[{ value: '7d', label: '近 7 天' }, { value: '30d', label: '近 30 天' }]}
            />
            <Segmented value={dim} onChange={setDim} options={['按模型', '按渠道', '按令牌']} />
          </>
        }
      />

      <Row gutter={16} style={{ marginBottom: 16 }}>
        {[
          ['总请求', fmt.n(totals.requests), '+18.2%', '#16A34A'],
          ['总 Token', fmt.k(totals.tokens), '+21.6%', '#16A34A'],
          ['总花费', fmt.usd(totals.cost), '+9.4%', '#EF4444'],
          ['平均延迟', '486ms', '-5.1%', '#16A34A'],
        ].map(([label, value, delta, color]) => (
          <Col xs={24} sm={12} xl={6} key={label}>
            <Card className="gw-card-hover">
              <div style={{ fontSize: 13, color: 'var(--gw-text-3)' }}>{label}</div>
              <div className="gw-num" style={{ fontSize: 24, fontWeight: 600, marginTop: 8, letterSpacing: '-.4px' }}>
                {value}
              </div>
              <div style={{ fontSize: 12, color, marginTop: 12 }}>{delta}</div>
            </Card>
          </Col>
        ))}
      </Row>

      <Row gutter={16} style={{ marginBottom: 16 }}>
        <Col xs={24} xl={16}>
          <Card title="每日花费">
            <Chart option={barOption} height={280} />
          </Card>
        </Col>
        <Col xs={24} xl={8}>
          <Card title="花费 Top 5">
            {rows.slice(0, 5).map(r => (
              <div key={r.name} style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 12, fontSize: 13 }}>
                <span className="gw-mono" style={{ width: 150, color: 'var(--gw-text-2)' }}>{r.name}</span>
                <div style={{ flex: 1, height: 4, borderRadius: 2, background: 'var(--gw-fill)', overflow: 'hidden' }}>
                  <div style={{ height: '100%', width: `${(r.costUsd / maxCost) * 100}%`, background: 'var(--gw-primary)', borderRadius: 2 }} />
                </div>
                <span className="gw-num" style={{ width: 70, textAlign: 'right' }}>{fmt.usd(r.costUsd)}</span>
              </div>
            ))}
          </Card>
        </Col>
      </Row>

      <Card>
        <Table<UsageRow>
          rowKey="name"
          size="middle"
          loading={isLoading}
          dataSource={rows}
          columns={columns}
          pagination={false}
        />
      </Card>
    </div>
  );
}
