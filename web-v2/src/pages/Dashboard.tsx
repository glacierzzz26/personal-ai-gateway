import { useNavigate } from 'react-router-dom';
import { App, Button, Card, Col, Empty, Row, Table, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { ReloadOutlined } from '@ant-design/icons';
import { useQuery, useQueryClient } from '@tanstack/react-query';
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
  note: string;
  noteColor?: string;
  values?: number[];
  color?: string;
}

function StatCard({ label, value, note, noteColor, values, color }: StatProps) {
  return (
    <Card className="gw-card-hover">
      <div style={{ fontSize: 13, color: 'var(--gw-text-3)' }}>{label}</div>
      <div className="gw-num" style={{ fontSize: 24, fontWeight: 600, marginTop: 8, letterSpacing: '-.4px' }}>
        {value}
      </div>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginTop: 12, gap: 12, minHeight: 28 }}>
        <span style={{ fontSize: 12, color: noteColor ?? 'var(--gw-text-3)' }}>{note}</span>
        {values && values.length > 0 && <Sparkline values={values} color={color} />}
      </div>
    </Card>
  );
}

/** "2026-09-03 20" → "20:00" */
const hourLabel = (ts: string) => `${ts.slice(11)}:00`;

export default function Dashboard() {
  const { message } = App.useApp();
  const navigate = useNavigate();
  const c = useChartColors();
  const qc = useQueryClient();

  const { data: overview } = useQuery({ queryKey: ['overview'], queryFn: api.getOverview, refetchInterval: 15000 });
  const { data: channels = [] } = useQuery({ queryKey: ['channels'], queryFn: api.getChannels });
  const { data: recent = [], isFetching: logsLoading } = useQuery({
    queryKey: ['recentLogs'],
    queryFn: () => api.recentLogs(8),
    refetchInterval: 15000,
  });

  const hours = overview?.hours ?? [];
  const days = overview?.days ?? [];

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
      type: 'category', data: hours.map(h => hourLabel(h.ts)), boundaryGap: false,
      axisLine: { lineStyle: { color: c.line } }, axisTick: { show: false },
      axisLabel: { color: c.text, fontSize: 11, interval: 2 },
    },
    yAxis: {
      type: 'value', splitLine: { lineStyle: { color: c.line } },
      axisLabel: { color: c.text, fontSize: 11 },
    },
    series: [
      {
        name: '请求数', type: 'line', data: hours.map(h => h.requests),
        showSymbol: false, lineStyle: { width: 1.8, color: c.primary }, itemStyle: { color: c.primary },
        areaStyle: { color: c.primarySoft, opacity: 0.12 },
      },
      {
        name: '错误数', type: 'line', data: hours.map(h => h.errors),
        showSymbol: false, lineStyle: { width: 1.8, color: c.error }, itemStyle: { color: c.error },
      },
    ],
  };

  const refresh = async () => {
    await qc.invalidateQueries({ queryKey: ['overview'] });
    await qc.invalidateQueries({ queryKey: ['recentLogs'] });
    message.success('已刷新');
  };

  const totalReq = overview?.totalRequests ?? 0;
  const totalErr = overview?.totalErrors ?? 0;
  const successRate = totalReq ? 1 - totalErr / totalReq : 1;
  const todayCost = days.length ? days[days.length - 1].costUsd : 0;

  const logCols: ColumnsType<RequestLogItem> = [
    { title: '时间', dataIndex: 'ts', width: 130, render: v => <span className="gw-mono" style={{ fontSize: 12 }}>{String(v).slice(5)}</span> },
    { title: '模型', dataIndex: 'model', render: v => <span className="gw-mono">{v}</span> },
    { title: '渠道', dataIndex: 'channelName', width: 140 },
    { title: '令牌', dataIndex: 'tokenName', width: 130 },
    {
      title: '状态', dataIndex: 'statusCode', width: 90,
      render: (v: number, r) => {
        const status = v >= 400 ? 'down' : v >= 300 ? 'degraded' : 'healthy';
        return <StatusTag status={status} text={r.error ? `失败 ${v}` : String(v)} />;
      },
    },
    { title: '费用', dataIndex: 'costUsd', align: 'right', width: 100, render: v => <span className="gw-num">{fmt.usd(v, 4)}</span> },
    { title: '总耗时', dataIndex: 'totalMs', align: 'right', width: 100, render: v => <span className="gw-num">{fmt.ms(v)}</span> },
  ];

  return (
    <div className="gw-page">
      <PageHeader
        title="运行总览"
        desc="近 7 天请求/成本与近 24 小时曲线，每 15 秒自动刷新"
        extra={
          <Button size="small" icon={<ReloadOutlined />} onClick={refresh}>
            刷新
          </Button>
        }
      />

      <Row gutter={16} style={{ marginBottom: 16 }}>
        <Col xs={24} sm={12} xl={6}>
          <StatCard label="请求数(近 7 天)" value={fmt.n(totalReq)}
            note={`其中 ${fmt.n(totalErr)} 次错误`} noteColor={totalErr ? '#EF4444' : '#16A34A'}
            values={days.map(d => d.requests)} color={c.primary} />
        </Col>
        <Col xs={24} sm={12} xl={6}>
          <StatCard label="总花费(近 7 天)" value={fmt.usd(overview?.totalCostUsd ?? 0)}
            note={`今日 ${fmt.usd(todayCost)}`}
            values={days.map(d => Number(d.costUsd.toFixed(2)))} color="#F59E0B" />
        </Col>
        <Col xs={24} sm={12} xl={6}>
          <StatCard label="平均首字延迟" value={fmt.ms(overview?.avgFirstTokenMs ?? 0)}
            note="近 7 天请求均值" />
        </Col>
        <Col xs={24} sm={12} xl={6}>
          <StatCard label="成功率" value={fmt.pct(successRate)} note="近 7 天"
            noteColor={successRate >= 0.995 ? '#16A34A' : successRate >= 0.98 ? '#F59E0B' : '#EF4444'}
            values={days.map(d => (d.requests ? 1 - d.errors / d.requests : 1))} color="#16A34A" />
        </Col>
      </Row>

      <Row gutter={16} style={{ marginBottom: 16 }}>
        <Col xs={24} xl={16}>
          <Card title="近 24 小时请求量与错误量">
            <Chart option={option} height={280} />
          </Card>
        </Col>
        <Col xs={24} xl={8}>
          <Card title="渠道健康度" styles={{ body: { paddingTop: 8, paddingBottom: 16 } }}>
            {channels.length === 0 ? (
              <Empty description="暂无渠道" image={Empty.PRESENTED_IMAGE_SIMPLE} style={{ padding: '24px 0' }} />
            ) : (
              channels.slice(0, 6).map(ch => {
                const color = ch.successRate < 0.98 ? '#EF4444' : ch.successRate < 0.995 ? '#F59E0B' : 'var(--gw-primary)';
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
                        {ch.status === 'down' ? '—' : fmt.ms(ch.latencyMs)} · {fmt.pct(ch.successRate)}
                      </div>
                    </div>
                    <div style={{ width: 64 }}>
                      <div style={{ height: 4, borderRadius: 2, background: 'var(--gw-fill)', overflow: 'hidden' }}>
                        <div style={{ height: '100%', width: `${ch.successRate * 100}%`, background: color, borderRadius: 2 }} />
                      </div>
                    </div>
                  </div>
                );
              })
            )}
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
          loading={logsLoading && recent.length === 0}
          dataSource={recent}
          columns={logCols}
          scroll={{ x: 860 }}
          pagination={false}
          locale={{ emptyText: <Empty description="暂无请求，快去创建渠道与令牌体验吧" image={Empty.PRESENTED_IMAGE_SIMPLE} /> }}
        />
      </Card>
    </div>
  );
}
