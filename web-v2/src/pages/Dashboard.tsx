import { useNavigate } from 'react-router-dom';
import { Card, Col, Row, Table, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useQuery } from '@tanstack/react-query';
import PageHeader from '@/components/PageHeader';
import Chart from '@/components/Chart';
import Sparkline from '@/components/Sparkline';
import StatusTag from '@/components/StatusTag';
import { api } from '@/services/api';
import { fmt } from '@/utils/format';
import { useChartColors } from '@/hooks/useChartColors';
import type { EChartsOption } from 'echarts';
import type { RequestLogItem } from '@/types';

interface StatProps {
  label: string;
  value: string;
  delta: string;
  tone: 'good' | 'bad' | 'neutral';
  values: number[];
  color?: string;
}

function StatCard({ label, value, delta, tone, values, color }: StatProps) {
  const c = tone === 'good' ? '#16A34A' : tone === 'bad' ? '#EF4444' : 'var(--gw-text-3)';
  return (
    <Card className="gw-card-hover">
      <div style={{ fontSize: 13, color: 'var(--gw-text-3)' }}>{label}</div>
      <div className="gw-num" style={{ fontSize: 24, fontWeight: 600, marginTop: 8, letterSpacing: '-.4px' }}>
        {value}
      </div>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginTop: 12, gap: 12 }}>
        <span style={{ fontSize: 12, color: c }}>{delta}</span>
        <Sparkline values={values} color={color} />
      </div>
    </Card>
  );
}

export default function Dashboard() {
  const navigate = useNavigate();
  const c = useChartColors();
  const { data: overview } = useQuery({ queryKey: ['overview'], queryFn: api.getOverview, refetchInterval: 15000 });
  const { data: channels = [] } = useQuery({ queryKey: ['channels'], queryFn: api.getChannels });
  const { data: logs = [] } = useQuery({ queryKey: ['logs'], queryFn: api.getLogs });

  const hours = overview?.hours ?? [];
  const option: EChartsOption = {
    grid: { left: 44, right: 12, top: 34, bottom: 26 },
    tooltip: {
      trigger: 'axis',
      backgroundColor: c.tooltipBg, borderColor: c.tooltipBorder,
      textStyle: { color: c.text, fontSize: 12 },
    },
    legend: {
      data: ['请求数', '错误数'], right: 0, top: 0,
      itemWidth: 8, itemHeight: 8, textStyle: { color: c.text, fontSize: 12 },
    },
    xAxis: {
      type: 'category', data: hours.map(h => h.ts), boundaryGap: false,
      axisLine: { lineStyle: { color: c.line } }, axisTick: { show: false },
      axisLabel: { color: c.text, fontSize: 11, interval: 3 },
    },
    yAxis: {
      type: 'value', splitLine: { lineStyle: { color: c.line } },
      axisLabel: { color: c.text, fontSize: 11 },
    },
    series: [
      {
        name: '请求数', type: 'line', data: hours.map(h => h.requests),
        showSymbol: false, lineStyle: { width: 1.8, color: c.primary }, itemStyle: { color: c.primary },
      },
      {
        name: '错误数', type: 'line', data: hours.map(h => h.errors),
        showSymbol: false, lineStyle: { width: 1.8, color: c.error }, itemStyle: { color: c.error },
      },
    ],
  };

  const logCols: ColumnsType<RequestLogItem> = [
    { title: '时间', dataIndex: 'ts', width: 110, render: v => <span className="gw-mono">{String(v).slice(11)}</span> },
    {
      title: '状态', dataIndex: 'statusCode', width: 90,
      render: v => <StatusTag status={v === 200 ? 'healthy' : 'down'} text={String(v)} />,
    },
    { title: '模型', dataIndex: 'model', render: v => <span className="gw-mono">{v}</span> },
    { title: '渠道', dataIndex: 'channelName' },
    { title: 'Token', key: 'tok', align: 'right', render: (_, r) => <span className="gw-num">{fmt.k(r.inTokens + r.outTokens)}</span> },
    { title: '耗时', dataIndex: 'totalMs', align: 'right', render: v => <span className="gw-num">{fmt.ms(v)}</span> },
    { title: '花费', dataIndex: 'costUsd', align: 'right', render: v => <span className="gw-num">{fmt.usd(v, 4)}</span> },
  ];

  const totalReq = overview?.totalRequests ?? 0;
  const totalErr = overview?.totalErrors ?? 0;

  return (
    <div className="gw-page">
      <PageHeader
        title="运行总览"
        desc="网关实时运行态势，每 15 秒自动刷新"
        extra={<span style={{ fontSize: 13, color: 'var(--gw-text-3)' }}>最后更新 20:31:04</span>}
      />

      <Row gutter={16} style={{ marginBottom: 16 }}>
        <Col xs={24} sm={12} xl={6}>
          <StatCard label="今日请求" value={fmt.n(totalReq)} delta="+12.4%" tone="neutral"
            values={hours.map(h => h.requests)} />
        </Col>
        <Col xs={24} sm={12} xl={6}>
          <StatCard label="今日花费" value={fmt.usd(overview?.totalCostUsd ?? 0)} delta="+8.1%" tone="bad"
            values={hours.map(h => h.costUsd)} color="#F59E0B" />
        </Col>
        <Col xs={24} sm={12} xl={6}>
          <StatCard label="平均首字延迟" value={fmt.ms(overview?.avgFirstTokenMs ?? 0)} delta="-6.3%" tone="good"
            values={hours.map(h => 300 + h.errors * 8)} />
        </Col>
        <Col xs={24} sm={12} xl={6}>
          <StatCard label="成功率" value={fmt.pct(1 - totalErr / (totalReq || 1))} delta="-0.2%" tone="bad"
            values={hours.map(h => 1 - h.errors / (h.requests || 1))} color="#16A34A" />
        </Col>
      </Row>

      <Row gutter={16} style={{ marginBottom: 16 }}>
        <Col xs={24} xl={16}>
          <Card title="近 24 小时请求量与错误量">
            <Chart option={option} height={280} />
          </Card>
        </Col>
        <Col xs={24} xl={8}>
          <Card
            title="渠道健康度"
            styles={{ body: { paddingTop: 8, paddingBottom: 16 } }}
          >
            {channels.slice(0, 6).map(ch => {
              const color = ch.successRate < .98 ? '#EF4444' : ch.successRate < .995 ? '#F59E0B' : 'var(--gw-primary)';
              return (
                <div
                  key={ch.id}
                  style={{
                    display: 'flex', alignItems: 'center', gap: 10, padding: '10px 0',
                    borderBottom: '1px solid var(--gw-border-2)',
                  }}
                >
                  <div style={{ flex: 1, minWidth: 0 }}>
                    <div style={{ fontSize: 13 }}>{ch.name}</div>
                    <div style={{ fontSize: 12, color: 'var(--gw-text-3)', marginTop: 2 }}>
                      {fmt.ms(ch.latencyMs)} · {fmt.pct(ch.successRate)}
                    </div>
                  </div>
                  <div style={{ width: 64 }}>
                    <div style={{ height: 4, borderRadius: 2, background: 'var(--gw-fill)', overflow: 'hidden' }}>
                      <div style={{ height: '100%', width: `${ch.successRate * 100}%`, background: color, borderRadius: 2 }} />
                    </div>
                  </div>
                </div>
              );
            })}
            <div style={{ paddingTop: 12 }}>
              <Typography.Link onClick={() => navigate('/channels')}>查看全部渠道 →</Typography.Link>
            </div>
          </Card>
        </Col>
      </Row>

      <Card
        title="最近请求"
        extra={<Typography.Link onClick={() => navigate('/logs')}>查看全部 →</Typography.Link>}
      >
        <Table<RequestLogItem>
          rowKey="id"
          size="middle"
          loading={!overview}
          dataSource={logs.slice(0, 10)}
          columns={logCols}
          pagination={false}
        />
      </Card>
    </div>
  );
}
