import { useEffect, useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import Chart, { ChartLegend } from '@/components/Chart';
import LedgerDrawer from '@/components/LedgerDrawer';
import RangePicker, { defaultRange, rangeLabel, toQuery } from '@/components/RangePicker';
import RequestLogDrawer from '@/components/RequestLogDrawer';
import { Block as BlockCard, BlockBody, BlockHead, Blocks, MetricBlock } from '@/components/Block';
import PageHeader from '@/components/PageHeader';
import StatusDot from '@/components/StatusDot';
import { ErrorState, SkBlock, SkLines } from '@/components/States';
import { Badge, Card, CardBody, CardHead, Empty, Li, Pager, Seg, Strip, Table } from '@/ui/primitives';
import type { Column } from '@/ui/primitives';
import { Icon } from '@/ui/icons';
import { api } from '@/services/api';
import { useSession } from '@/stores/session';
import { useCurrency } from '@/stores/currency';
import { FAIL_LABEL, STATUS_CLIENT_CLOSED, classifyError, fmt } from '@/utils/format';
import type { Tone } from '@/utils/format';
import type { RequestLogItem, UsageRow } from '@/types';

const okCode = (code: number) => code >= 100 && code < 400;

/** 令牌额度告警阈值,与管理员 Dashboard 同口径(≥85% 视为告警)。 */
const QUOTA_ALERT = 0.85;

/** 账变流水卡片内联展示的条数;更多走 Drawer。 */
const LEDGER_PREVIEW = 4;

const REASON: Record<string, string> = {
  topup: '充值',
  charge: '扣费',
  adjust: '调整',
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

  const usageColumns: Column<UsageRow>[] = [
    { key: 'name', title: dim === 'model' ? '模型' : '令牌', render: r => <span className="mono">{r.name}</span> },
    { key: 'requests', title: '请求数', align: 'right', render: r => <span className="num">{fmt.n(r.requests)}</span> },
    { key: 'inTokens', title: '输入 Token', align: 'right', render: r => <span className="num">{fmt.k(r.inTokens)}</span> },
    { key: 'outTokens', title: '输出 Token', align: 'right', render: r => <span className="num">{fmt.k(r.outTokens)}</span> },
    { key: 'chargeUsd', title: '花费', align: 'right', render: r => <span className="num">{fmt.usd(r.chargeUsd ?? 0)}</span> },
  ];

  const logColumns: Column<RequestLogItem>[] = [
    { key: 'ts', title: '时间', width: 148, render: r => <span className="mono small">{r.ts}</span> },
    {
      key: 'statusCode',
      title: '状态',
      width: 110,
      render: r => {
        if (r.statusCode === STATUS_CLIENT_CLOSED) return <StatusDot status="" text="中断" tone="aux" />;
        if (okCode(r.statusCode)) return <StatusDot status="" text="成功" tone="ok" />;
        return (
          <span className="hstack" style={{ gap: 7 }}>
            <StatusDot status="" text={r.error ? '失败' : String(r.statusCode)} tone="err" />
            {r.error && <Badge tone="slate">{FAIL_LABEL[classifyError(r.error)]}</Badge>}
          </span>
        );
      },
    },
    { key: 'model', title: '模型', render: r => <span className="mono ellip" title={r.model}>{r.model}</span> },
    { key: 'tokenName', title: '令牌', width: 116, render: r => <span className="muted">{r.tokenName || '—'}</span> },
    { key: 'inTokens', title: '输入', align: 'right', width: 88, render: r => <span className="num">{fmt.k(r.inTokens)}</span> },
    { key: 'outTokens', title: '输出', align: 'right', width: 88, render: r => <span className="num">{fmt.k(r.outTokens)}</span> },
    { key: 'totalMs', title: '总耗时', align: 'right', width: 88, render: r => <span className="num">{fmt.ms(r.totalMs)}</span> },
    { key: 'chargeUsd', title: '花费', align: 'right', width: 92, render: r => <span className="num">{fmt.usd(r.chargeUsd ?? 0)}</span> },
  ];

  return (
    <>
      <PageHeader
        title="我的账户"
        desc={`${me?.username ?? ''} 的余额、用量与请求明细`}
        extra={<RangePicker value={range} onChange={setRange} />}
      />

      {alert && (
        <div style={{ marginBottom: 16 }}>
          <Strip tone={alert.tone}>
            <span className="st-title">{alert.title}</span>
            <span className="st-desc">{alert.desc}</span>
          </Strip>
        </div>
      )}

      <div className="kpi-grid" style={{ gridTemplateColumns: 'repeat(4, minmax(0,1fr))' }}>
        <MetricBlock label="可用余额" value={fmt.usd(balance)} note={balanceTip(balance)} />
        <MetricBlock label={`${rl}请求`} value={fmt.n(totals.requests)} note="仅统计你名下令牌" />
        <MetricBlock
          label={`${rl}Token`}
          value={fmt.k(totals.inTokens + totals.outTokens)}
          note={`入 ${fmt.k(totals.inTokens)} / 出 ${fmt.k(totals.outTokens)}`}
        />
        <MetricBlock label={`${rl}花费`} value={fmt.usd(totals.cost)} note="按本站售价累计" />
      </div>

      {balanceQ.isError && (
        <Blocks style={{ marginTop: 16 }}>
          <BlockCard>
            <BlockBody>
              <ErrorState title="余额加载失败" desc="无法读取钱包余额，请稍后重试。" onRetry={() => void balanceQ.refetch()} />
            </BlockBody>
          </BlockCard>
        </Blocks>
      )}

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

      <div style={{ display: 'grid', gridTemplateColumns: 'minmax(0,2fr) minmax(0,1fr)', gap: 14, margin: '14px 0' }}>
        <Card>
          <CardHead
            title={`每日请求与花费(${rl})`}
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
                  data: series.map(d => Number((d.chargeUsd ?? 0).toFixed(4))),
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
          <CardHead
            title="账变流水"
            right={
              ledger.length > LEDGER_PREVIEW ? (
                <button type="button" className="card-link" onClick={() => setLedgerOpen(true)}>
                  全部 {ledger.length} 条 <Icon name="right" size={12} />
                </button>
              ) : undefined
            }
          />
          <CardBody>
            {balanceQ.isLoading ? (
              <SkBlock />
            ) : ledger.length === 0 ? (
              <Empty icon="wallet" title="暂无账变记录" />
            ) : (
              /*
               * 右栏窄,不放可滚动表格 —— 这里只列最近几条做摘要,完整流水(含备注)点右上角进 Drawer 看。
               */
              ledger.slice(0, LEDGER_PREVIEW).map(log => (
                <Li
                  key={log.id}
                  k={<Badge tone="slate">{REASON[log.reason] ?? log.reason}</Badge>}
                  v={
                    <span style={{ color: log.delta < 0 ? 'var(--err)' : 'var(--ok)' }}>
                      {log.delta > 0 ? '+' : ''}
                      {fmt.usd(log.delta)}
                    </span>
                  }
                  r2={
                    <span className="hstack" style={{ gap: 8 }}>
                      <span className="num">{fmt.dt(log.createdAt)}</span>
                      <span className="num" style={{ marginLeft: 'auto' }}>
                        余额 {fmt.usd(log.balanceAfter)}
                      </span>
                    </span>
                  }
                />
              ))
            )}
          </CardBody>
        </Card>
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
              <Empty icon="activity" title="所选范围内暂无用量" desc="通过网关发起调用后，这里会按模型 / 令牌汇总请求与花费。" />
            ) : (
              <Table<UsageRow> columns={usageColumns} rows={rows} rowKey={r => r.name} />
            )}
          </BlockBody>
        </BlockCard>
      </Blocks>

      <Blocks style={{ marginTop: 16 }}>
        <BlockCard>
          <BlockHead
            title="我的请求日志"
            sub="仅显示你名下令牌"
            right={
              <Seg
                size="sm"
                value={size}
                onChange={v => {
                  setPage(1);
                  setSize(Number(v));
                }}
                options={[20, 50, 100].map(n => ({ value: n, label: `${n}/页` }))}
              />
            }
          />
          <BlockBody style={{ padding: 0 }}>
            {logsQ.isFetching && logs.length === 0 ? (
              <div style={{ padding: 14 }}>
                <SkLines rows={['', 'w80', 'w60', 'w80', 'w60']} />
              </div>
            ) : logs.length === 0 ? (
              logsQ.isError ? (
                <div style={{ padding: 14 }}>
                  <ErrorState title="日志加载失败" desc="无法读取请求日志。" onRetry={() => void logsQ.refetch()} />
                </div>
              ) : (
                <Empty icon="scroll" title="还没有请求日志" desc="网关转发过请求后，这里会逐条列出耗时、Token 与花费。" />
              )
            ) : (
              <>
                <Table<RequestLogItem> columns={logColumns} rows={logs} rowKey={r => r.id} onRowClick={r => setDetail(r)} />
                <Pager total={total} page={page} size={size} onChange={p => setPage(p)} />
              </>
            )}
          </BlockBody>
        </BlockCard>
      </Blocks>

      <RequestLogDrawer detail={detail} onClose={() => setDetail(null)} />
      <LedgerDrawer open={ledgerOpen} logs={ledger} onClose={() => setLedgerOpen(false)} />
    </>
  );
}
