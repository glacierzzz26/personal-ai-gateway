import { useMemo, useState } from 'react';
import { Card, Col, Row, Segmented, Table } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useQuery } from '@tanstack/react-query';
import Chart from '@/components/Chart';
import RangePicker, { defaultRange, rangeLabel, toQuery } from '@/components/RangePicker';
import { Block as BlockCard, Blocks, BlockHead, MetricBlock } from '@/components/Block';
import PageHeader from '@/components/PageHeader';
import { EmptyState, ErrorState } from '@/components/States';
import { useChartColors } from '@/hooks/useChartColors';
import { api } from '@/services/api';
import { useSession } from '@/stores/session';
import { fmt } from '@/utils/format';
import type { EChartsOption } from 'echarts';
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
      axisLabel: { color: c.text, fontSize: 11, interval: 'auto', rotate: 0, hideOverlap: true },
    },
    yAxis: [
      { type: 'value', splitLine: { lineStyle: { color: c.line } }, axisLabel: { color: c.text, fontSize: 11 } },
      { type: 'value', splitLine: { show: false }, axisLabel: { color: c.text, fontSize: 11 } },
    ],
    series: [
      { name: '请求数', type: 'bar', data: series.map(d => d.requests), itemStyle: { color: c.primary, borderRadius: 3 }, barWidth: '45%' },
      {
        name: '花费', type: 'line', yAxisIndex: 1, data: series.map(d => Number((d.costUsd ?? 0).toFixed(4))),
        showSymbol: false, lineStyle: { width: 1.8, color: c.warn }, itemStyle: { color: c.warn },
      },
    ],
  };

  const usageColumns: ColumnsType<UsageRow> = [
    { title: dim === 'model' ? '模型' : '令牌', dataIndex: 'name', render: v => <span className="gw-mono">{v}</span> },
    { title: '请求数', dataIndex: 'requests', align: 'right', render: v => <span className="gw-num">{fmt.n(v)}</span> },
    { title: '输入 Token', dataIndex: 'inTokens', align: 'right', render: v => <span className="gw-num">{fmt.k(v)}</span> },
    { title: '输出 Token', dataIndex: 'outTokens', align: 'right', render: v => <span className="gw-num">{fmt.k(v)}</span> },
    { title: '花费', dataIndex: 'costUsd', align: 'right', render: v => <span className="gw-num">{fmt.usd(v ?? 0)}</span> },
  ];

  return (
    <div className="gw-page">
      <PageHeader
        title="自用统计"
        desc={`${me?.username ?? ''} 自己名下流量的用量与花费（成本口径 · 含站主自用，不含客户）`}
        extra={<RangePicker value={range} onChange={setRange} />}
      />

      <Row gutter={16} style={{ marginBottom: 16 }}>
        <Col xs={24} sm={12} xl={6}>
          <MetricBlock label={`${rl}请求`} value={fmt.n(totals.requests)} note="仅统计你自己名下令牌" />
        </Col>
        <Col xs={24} sm={12} xl={6}>
          <MetricBlock
            label={`${rl}Token`}
            value={fmt.k(totals.inTokens + totals.outTokens)}
            note={`入 ${fmt.k(totals.inTokens)} / 出 ${fmt.k(totals.outTokens)}`}
          />
        </Col>
        <Col xs={24} sm={12} xl={6}>
          <MetricBlock label={`${rl}花费`} value={fmt.usd(totals.cost)} note="你付上游的成本（非客户营收）" />
        </Col>
        <Col xs={24} sm={12} xl={6}>
          <MetricBlock
            label="平均单请求"
            value={totals.requests ? fmt.usd(totals.cost / totals.requests) : '—'}
            note="花费 ÷ 请求数"
          />
        </Col>
      </Row>

      <div style={{ display: 'flex', alignItems: 'flex-start', gap: 16, margin: '20px 0 16px' }}>
        <div style={{ minWidth: 0 }}>
          <div style={{ fontSize: 15, fontWeight: 600 }}>用量拆解</div>
          <div style={{ fontSize: 12, color: 'var(--gw-text-3)', marginTop: 4 }}>
            按 {dim === 'model' ? '模型' : '令牌'} × {rl} 聚合请求与花费
          </div>
        </div>
        <div style={{ marginLeft: 'auto', display: 'flex', gap: 8, alignItems: 'center', flexShrink: 0 }}>
          <Segmented
            value={dim} onChange={v => setDim(v as 'model' | 'token')}
            options={[{ value: 'model', label: '按模型' }, { value: 'token', label: '按令牌' }]}
          />
        </div>
      </div>

      <Row gutter={16} style={{ marginBottom: 16 }}>
        <Col xs={24}>
          <Card title={`每日请求与花费(${rl})`}>
            {usageQ.isError ? (
              <ErrorState title="用量加载失败" desc="无法读取用量数据，请稍后重试。" onRetry={() => void usageQ.refetch()} />
            ) : (
              <Chart option={barOption} height={280} />
            )}
          </Card>
        </Col>
      </Row>

      <Blocks>
        <BlockCard>
          <BlockHead title="用量明细" sub={`按${dim === 'model' ? '模型' : '令牌'}聚合`} />
          <Table<UsageRow>
            rowKey="name"
            size="middle"
            loading={usageQ.isFetching && rows.length === 0}
            dataSource={rows}
            columns={usageColumns}
            pagination={false}
            locale={{
              emptyText: (
                <EmptyState
                  title="所选范围内暂无自用流量"
                  desc="你自己名下的令牌发起调用后，这里会按模型 / 令牌汇总请求与花费（成本）。"
                />
              ),
            }}
          />
        </BlockCard>
      </Blocks>
    </div>
  );
}
