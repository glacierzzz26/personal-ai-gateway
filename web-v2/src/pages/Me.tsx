import { useEffect, useMemo, useState } from 'react';
import { Card, Col, Empty, Row, Segmented, Space, Table } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useQuery } from '@tanstack/react-query';
import Chart from '@/components/Chart';
import LedgerDrawer from '@/components/LedgerDrawer';
import RangePicker, { defaultRange, rangeLabel, toQuery } from '@/components/RangePicker';
import RequestLogDrawer from '@/components/RequestLogDrawer';
import { Block as BlockCard, Blocks, BlockHead, MetricBlock } from '@/components/Block';
import PageHeader from '@/components/PageHeader';
import StatusDot from '@/components/StatusDot';
import { EmptyState, ErrorState, SkBlock } from '@/components/States';
import { useChartColors } from '@/hooks/useChartColors';
import { api } from '@/services/api';
import { useSession } from '@/stores/session';
import { useCurrency } from '@/stores/currency';
import { FAIL_LABEL, STATUS_CLIENT_CLOSED, TONE_COLOR, classifyError, fmt } from '@/utils/format';
import type { Tone } from '@/utils/format';
import { TOKENS } from '@/styles/tokens';
import type { EChartsOption } from 'echarts';
import type { RequestLogItem, UsageRow } from '@/types';

const okCode = (code: number) => code >= 100 && code < 400;

/** 令牌额度告警阈值,与管理员 Dashboard 同口径(≥85% 视为告警)。 */
const QUOTA_ALERT = 0.85;

/** 账变流水卡片内联展示的条数;更多走 Drawer。 */
const LEDGER_PREVIEW = 4;

const REASON: Record<string, { t: string; tone: string }> = {
  topup: { t: '充值', tone: 'ok' },
  charge: { t: '扣费', tone: 'aux' },
  adjust: { t: '调整', tone: 'warn' },
};

/** "2026-09-03" → "09-03";小时桶 "2026-09-03 20" → "20:00" */
const dayLabel = (ts: string) => (ts.length > 10 ? `${ts.slice(11)}:00` : ts.slice(5));

/** 余额水位提示文案（≤0 无法调用，<10 提示尽快充值）。 */
function balanceTip(balance: number): string {
  if (balance <= 0) return '余额不足，调用已被拒绝';
  if (balance < 10) return '余额偏低，请及时充值';
  return '本页所有金额为本站售价口径';
}

/** 我的账户 —— 普通用户自助面：余额、用量、请求日志（作用域锁本人）。 */
export default function Me() {
  const me = useSession(s => s.admin);
  const c = useChartColors();
  const setCurrency = useCurrency(s => s.setCurrency);
  const [dim, setDim] = useState<'model' | 'token'>('model');
  /** 统计窗口:预设 1/7/30 天或自定义区间;改动即重取用量与曲线 */
  const [range, setRange] = useState(defaultRange);
  const rq = useMemo(() => toQuery(range), [range]);
  const rl = rangeLabel(range);
  const [page, setPage] = useState(1);
  const [size, setSize] = useState(20);
  const [detail, setDetail] = useState<RequestLogItem | null>(null);
  const [ledgerOpen, setLedgerOpen] = useState(false);

  const balanceQ = useQuery({ queryKey: ['me', 'balance'], queryFn: () => api.myBalance(30) });
  const usageQ = useQuery({
    queryKey: ['me', 'usage', dim, rq],
    queryFn: () => api.getMyUsage(dim, rq),
  });
  const logsQ = useQuery({
    queryKey: ['me', 'logs', page, size],
    queryFn: () => api.getMyLogs({}, page, size),
  });
  // 令牌额度自检(告警条用):用户面 /tokens 只返回本人名下,天然安全。
  const tokensQ = useQuery({ queryKey: ['tokens'], queryFn: api.getTokens });

  // 普通用户读不到 /settings(admin-only),计价币种只能从 /me/balance 带回并水合,
  // 否则管理员配了 USD 时用户侧仍按默认 CNY 渲染(issue #8 P2)。
  const cur = balanceQ.data?.currency;
  useEffect(() => {
    if (cur) setCurrency(cur);
  }, [cur, setCurrency]);

  const balance = balanceQ.data?.balanceUsd ?? 0;
  const rows = usageQ.data?.rows ?? [];
  const series = usageQ.data?.days ?? [];
  const logs = logsQ.data?.items ?? [];
  const total = logsQ.data?.total ?? 0;
  const ledger = balanceQ.data?.logs ?? [];

  // 阈值提醒:余额耗尽/偏低 + 令牌额度逼近(与管理员 Dashboard 同一套阈值)。
  // 用户此前只能等某次请求撞上 402/429 才知道(issue #8 P2)。
  const tokens = tokensQ.data ?? [];
  const alert = useMemo((): { tone: Tone; title: string; desc: string } | null => {
    const tight = tokens.filter(t => t.quotaUsd > 0 && t.usedUsd / t.quotaUsd >= QUOTA_ALERT);
    if (balance <= 0) {
      return { tone: 'err', title: '余额不足', desc: '账户余额已耗尽，调用已被拒绝；请联系管理员充值。' };
    }
    const parts: string[] = [];
    if (balance < 10) parts.push(`账户余额偏低（${fmt.usd(balance)}），可能很快耗尽`);
    if (tight.length) {
      parts.push(`${tight.length} 个令牌额度已用 ≥${(QUOTA_ALERT * 100).toFixed(0)}%（${tight.map(t => t.name).join('、')}）`);
    }
    if (!parts.length) {
      return { tone: 'ok', title: '一切正常', desc: `余额可用 · ${tokens.length} 个令牌额度充足 · 每次调用按本站售价从余额扣除` };
    }
    return { tone: 'warn', title: '请注意', desc: parts.join(' · ') };
  }, [balance, tokens]);

  const totals = useMemo(() => {
    let requests = 0, inTokens = 0, outTokens = 0, cost = 0;
    for (const r of rows) {
      requests += r.requests;
      inTokens += r.inTokens;
      outTokens += r.outTokens;
      cost += r.chargeUsd ?? 0; // 售价比花费,不是成本
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
        name: '花费', type: 'line', yAxisIndex: 1, data: series.map(d => Number((d.chargeUsd ?? 0).toFixed(4))),
        showSymbol: false, lineStyle: { width: 1.8, color: c.warn }, itemStyle: { color: c.warn },
      },
    ],
  };

  const usageColumns: ColumnsType<UsageRow> = [
    { title: dim === 'model' ? '模型' : '令牌', dataIndex: 'name', render: v => <span className="gw-mono">{v}</span> },
    { title: '请求数', dataIndex: 'requests', align: 'right', render: v => <span className="gw-num">{fmt.n(v)}</span> },
    { title: '输入 Token', dataIndex: 'inTokens', align: 'right', render: v => <span className="gw-num">{fmt.k(v)}</span> },
    { title: '输出 Token', dataIndex: 'outTokens', align: 'right', render: v => <span className="gw-num">{fmt.k(v)}</span> },
    { title: '花费', dataIndex: 'chargeUsd', align: 'right', render: v => <span className="gw-num">{fmt.usd(v ?? 0)}</span> },
  ];

  const logColumns: ColumnsType<RequestLogItem> = [
    { title: '时间', dataIndex: 'ts', width: 148, render: v => <span className="gw-mono">{v}</span> },
    {
      title: '状态', dataIndex: 'statusCode', width: 110,
      render: (v: number, r) => {
        if (v === STATUS_CLIENT_CLOSED) return <StatusDot status="" text="中断" tone="aux" />;
        if (okCode(v)) return <StatusDot status="" text="成功" tone="ok" />;
        return (
          <span style={{ display: 'inline-flex', alignItems: 'center', gap: 7 }}>
            <StatusDot status="" text={r.error ? '失败' : String(v)} tone="err" />
            {r.error && <span className="gw-badge">{FAIL_LABEL[classifyError(r.error)]}</span>}
          </span>
        );
      },
    },
    { title: '模型', dataIndex: 'model', render: v => <span className="gw-mono">{v}</span> },
    { title: '令牌', dataIndex: 'tokenName', width: 116, ellipsis: true, render: v => <span style={{ color: 'var(--gw-text-3)' }}>{v || '—'}</span> },
    { title: '输入', dataIndex: 'inTokens', align: 'right', width: 88, render: v => <span className="gw-num">{fmt.k(v)}</span> },
    { title: '输出', dataIndex: 'outTokens', align: 'right', width: 88, render: v => <span className="gw-num">{fmt.k(v)}</span> },
    { title: '总耗时', dataIndex: 'totalMs', align: 'right', width: 88, render: v => <span className="gw-num">{fmt.ms(v)}</span> },
    { title: '花费', dataIndex: 'chargeUsd', align: 'right', width: 92, render: v => <span className="gw-num">{fmt.usd(v ?? 0)}</span> },
  ];

  return (
    <div className="gw-page">
      <PageHeader
        title="我的账户"
        desc={`${me?.username ?? ''} 的余额、用量与请求明细`}
        extra={<RangePicker value={range} onChange={setRange} />}
      />

      {alert && (
        <div
          role="status"
          style={{
            display: 'flex', alignItems: 'flex-start', gap: 11, marginBottom: 16,
            border: '1px solid var(--gw-border)',
            borderLeft: `3px solid ${TONE_COLOR[alert.tone]}`,
            borderRadius: 'var(--gw-r-card)',
            background: 'var(--gw-card)',
            padding: '13px 16px', fontSize: 14,
          }}
        >
          <span style={{ display: 'inline-flex', alignItems: 'center', gap: 7, fontWeight: 500, color: 'var(--gw-text)', flexShrink: 0 }}>
            <i className={`gw-dot ${alert.tone}`} />
            {alert.title}
          </span>
          <span style={{ color: 'var(--gw-text-2)' }}>{alert.desc}</span>
        </div>
      )}

      <Row gutter={16} style={{ marginBottom: 16 }}>
        <Col xs={24} sm={12} xl={6}>
          <MetricBlock
            label="可用余额"
            value={fmt.usd(balance)}
            note={balanceTip(balance)}
          />
        </Col>
        <Col xs={24} sm={12} xl={6}>
          <MetricBlock label={`${rl}请求`} value={fmt.n(totals.requests)} note="仅统计你名下令牌" />
        </Col>
        <Col xs={24} sm={12} xl={6}>
          <MetricBlock
            label={`${rl}Token`}
            value={fmt.k(totals.inTokens + totals.outTokens)}
            note={`入 ${fmt.k(totals.inTokens)} / 出 ${fmt.k(totals.outTokens)}`}
          />
        </Col>
        <Col xs={24} sm={12} xl={6}>
          <MetricBlock label={`${rl}花费`} value={fmt.usd(totals.cost)} note="按本站售价累计" />
        </Col>
      </Row>

      {balanceQ.isError && (
        <Blocks style={{ marginBottom: 16 }}>
          <BlockCard>
            <div style={{ padding: '16px 20px' }}>
              <ErrorState title="余额加载失败" desc="无法读取钱包余额，请稍后重试。" onRetry={() => void balanceQ.refetch()} />
            </div>
          </BlockCard>
        </Blocks>
      )}

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
        <Col xs={24} xl={16}>
          <Card title={`每日请求与花费(${rl})`}>
            <Chart option={barOption} height={280} />
          </Card>
        </Col>
        <Col xs={24} xl={8}>
          <Card
            title="账变流水"
            styles={{ body: { padding: 0 } }}
            extra={
              ledger.length > LEDGER_PREVIEW && (
                <button type="button" className="gw-link" onClick={() => setLedgerOpen(true)}>
                  全部 {ledger.length} 条 →
                </button>
              )
            }
          >
            {balanceQ.isLoading ? (
              <div style={{ padding: 20 }}><SkBlock /></div>
            ) : ledger.length === 0 ? (
              <Empty description="暂无账变记录" image={Empty.PRESENTED_IMAGE_SIMPLE} style={{ padding: '32px 0' }} />
            ) : (
              /*
               * 右栏窄(1366 下约 322px),不放可滚动表格 —— 固定高度 + 内层滚动条既丑又不完整。
               * 这里只列最近几条做摘要,完整流水(含备注)点右上角进 Drawer 看。
               */
              <div className="gw-list">
                {ledger.slice(0, LEDGER_PREVIEW).map(log => (
                  <div className="gw-li" key={log.id} style={{ padding: '12px 20px', gap: 4 }}>
                    <div className="r1">
                      <span className="gw-badge">{REASON[log.reason]?.t ?? log.reason}</span>
                      <span className="v" style={{ color: log.delta < 0 ? TOKENS.err : TOKENS.ok }}>
                        {log.delta > 0 ? '+' : ''}{fmt.usd(log.delta)}
                      </span>
                    </div>
                    <div className="r2" style={{ display: 'flex', gap: 8 }}>
                      <span className="gw-num">{fmt.dt(log.createdAt)}</span>
                      <span style={{ marginLeft: 'auto' }} className="gw-num">
                        余额 {fmt.usd(log.balanceAfter)}
                      </span>
                    </div>
                  </div>
                ))}
              </div>
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
                  title="所选范围内暂无用量"
                  desc="通过网关发起调用后，这里会按模型 / 令牌汇总请求与花费。"
                />
              ),
            }}
          />
        </BlockCard>
      </Blocks>

      <Space direction="vertical" size={16} style={{ width: '100%', marginTop: 16 }}>
        <BlockCard>
          <BlockHead title="我的请求日志" sub="仅显示你名下令牌" />
          <Table<RequestLogItem>
            rowKey="id"
            size="middle"
            loading={logsQ.isFetching && logs.length === 0}
            dataSource={logs}
            columns={logColumns}
            scroll={{ x: 'max-content' }}
            locale={{
              emptyText: logsQ.isError ? (
                <ErrorState title="日志加载失败" desc="无法读取请求日志。" onRetry={() => void logsQ.refetch()} />
              ) : (
                <EmptyState title="还没有请求日志" desc="网关转发过请求后，这里会逐条列出耗时、Token 与花费。" />
              ),
            }}
            onRow={r => ({
              onClick: () => setDetail(r),
              onKeyDown: e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); setDetail(r); } },
              tabIndex: 0,
              style: { cursor: 'pointer' },
            })}
            pagination={{
              current: page,
              pageSize: size,
              total,
              showSizeChanger: true,
              pageSizeOptions: ['20', '50', '100'],
              showTotal: t => `共 ${fmt.n(t)} 条`,
              onChange: p => setPage(p),
              onShowSizeChange: (_, ps) => { setPage(1); setSize(ps); },
            }}
          />
        </BlockCard>
      </Space>

      <RequestLogDrawer detail={detail} onClose={() => setDetail(null)} />
      <LedgerDrawer open={ledgerOpen} logs={ledger} onClose={() => setLedgerOpen(false)} />
    </div>
  );
}
