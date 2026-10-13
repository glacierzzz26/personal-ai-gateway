import { useEffect, useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import Chart from '@/components/Chart';
import LedgerDrawer from '@/components/LedgerDrawer';
import RangePicker, { defaultRange, rangeLabel, toQuery } from '@/components/RangePicker';
import RequestLogDrawer from '@/components/RequestLogDrawer';
import StatusDot from '@/components/StatusDot';
import { EmptyState, ErrorState, SkBlock, SkLines } from '@/components/States';
import { Badge, Btn, Card, Empty, Kpi, PageHead, Pager, Seg, Select, Strip } from '@/ui/primitives';
import { api } from '@/services/api';
import { useSession } from '@/stores/session';
import { useCurrency } from '@/stores/currency';
import { useChartColors } from '@/hooks/useChartColors';
import { FAIL_LABEL, STATUS_CLIENT_CLOSED, classifyError, fmt } from '@/utils/format';
import type { RequestLogItem, UsageRow } from '@/types';

const okCode = (code: number) => code >= 100 && code < 400;

/** 令牌额度告警阈值,与管理员 Dashboard 同口径(≥85% 视为告警)。 */
const QUOTA_ALERT = 0.85;

/** 账变流水卡片内联展示的条数;更多走 Drawer。 */
const LEDGER_PREVIEW = 4;

/** 账变类型 → 文案 + 徽章色 */
const REASON: Record<string, { t: string; badge: 'green' | 'slate' | 'amber' }> = {
  topup: { t: '充值', badge: 'green' },
  charge: { t: '扣费', badge: 'slate' },
  adjust: { t: '调整', badge: 'amber' },
};

/** "2026-09-03" → "09-03";小时桶 "2026-09-03 20" → "20:00" */
const dayLabel = (ts: string) => (ts.length > 10 ? `${ts.slice(11)}:00` : ts.slice(5));

/** 余额水位提示文案（≤0 无法调用，<10 提示尽快充值）。 */
function balanceTip(balance: number): string {
  if (balance <= 0) return '余额不足，调用已被拒绝';
  if (balance < 10) return '余额偏低，请及时充值';
  return '本页所有金额为本站售价口径';
}

/** 纵轴紧凑刻度(与自用统计同款)。 */
const chartAxis = (v: number) =>
  v >= 1000 ? `${(v / 1000).toFixed(1).replace(/\.0$/, '')}k` : String(Math.round(v));

/**
 * 双轴 → 单轴近似后的 tooltip 格式:请求数恒为整数,花费固定 4 位小数,
 * 用这一特征区分口径(与原 echarts 双 y 轴各用各的 formatter 等效)。
 */
const tipFmt = (v: number) => (Number.isInteger(v) ? fmt.n(v) : fmt.usd(v));

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
  const alert = useMemo((): { tone: 'ok' | 'warn' | 'err'; title: string; desc: string } | null => {
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

  const chartData = {
    labels: series.map(d => dayLabel(d.ts)),
    // 原 echarts 为「请求数(柱/左轴) + 花费(线/右轴)」双轴;新 Chart 单轴,
    // 改双 series 近似(量纲差异大时以请求数为主视觉,花费走 tooltip 精确读数)。
    series: [
      { name: '请求数', data: series.map(d => d.requests), color: c.primary, fill: false },
      { name: '花费', data: series.map(d => Number((d.chargeUsd ?? 0).toFixed(4))), color: c.warn, fill: true },
    ],
  };

  return (
    <div>
      <PageHead
        title="我的账户"
        sub={`${me?.username ?? ''} 的余额、用量与请求明细`}
        actions={<RangePicker value={range} onChange={setRange} />}
      />

      {alert && (
        <div role="status" style={{ marginBottom: 16 }}>
          <Strip
            tone={alert.tone}
            title={
              <>
                <span className={`dot ${alert.tone === 'ok' ? 'g' : alert.tone === 'warn' ? 'a' : 'r'}`} />
                {alert.title}
              </>
            }
            desc={alert.desc}
          />
        </div>
      )}

      {/* 指标条 */}
      <div className="grid g-12" style={{ gap: 10, marginBottom: 16 }}>
        <div className="c3">
          <Kpi
            label="可用余额"
            value={<span style={balance <= 0 ? { color: 'var(--err)' } : undefined}>{fmt.usd(balance)}</span>}
            foot={<span className="cell-sub" style={{ whiteSpace: 'normal' }}>{balanceTip(balance)}</span>}
          />
        </div>
        <div className="c3">
          <Kpi
            label={`${rl}请求`}
            value={fmt.n(totals.requests)}
            foot={<span className="cell-sub" style={{ whiteSpace: 'normal' }}>仅统计你名下令牌</span>}
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
            foot={<span className="cell-sub" style={{ whiteSpace: 'normal' }}>按本站售价累计</span>}
          />
        </div>
      </div>

      {balanceQ.isError && (
        <Card icon="wallet" title="可用余额" style={{ marginBottom: 16 }}>
          <ErrorState title="余额加载失败" desc="无法读取钱包余额，请稍后重试。" onRetry={() => void balanceQ.refetch()} />
        </Card>
      )}

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

      <div className="two-col" style={{ marginBottom: 16 }}>
        <Card icon="activity" title={`每日请求与花费(${rl})`}>
          {usageQ.isError ? (
            <Empty icon="warn" title="用量加载失败">
              无法读取用量数据，请稍后重试。
              <div><Btn size="sm" style={{ marginTop: 12 }} onClick={() => void usageQ.refetch()}>重试</Btn></div>
            </Empty>
          ) : usageQ.isLoading && series.length === 0 ? (
            <SkLines rows={['w80', 'w60', 'w80', 'w40']} />
          ) : (
            <Chart labels={chartData.labels} series={chartData.series} h={280} yFmt={chartAxis} tipFmt={tipFmt} />
          )}
        </Card>

        <Card
          icon="coins"
          title="账变流水"
          extra={
            ledger.length > LEDGER_PREVIEW && (
              <Btn size="sm" variant="ghost" onClick={() => setLedgerOpen(true)}>
                全部 {ledger.length} 条 →
              </Btn>
            )
          }
        >
          {balanceQ.isLoading ? (
            <SkBlock />
          ) : ledger.length === 0 ? (
            <div style={{ padding: '32px 0' }}>
              <Empty icon="box" title="暂无账变记录" />
            </div>
          ) : (
            /*
             * 右栏窄(1366 下约一半宽),不放可滚动表格 —— 固定高度 + 内层滚动条既丑又不完整。
             * 这里只列最近几条做摘要,完整流水(含备注)点右上角进 Drawer 看。
             */
            <div>
              {ledger.slice(0, LEDGER_PREVIEW).map(log => (
                <div className="li" key={log.id} style={{ padding: '12px 0', gap: 4 }}>
                  <div className="r1">
                    <Badge tone={REASON[log.reason]?.badge ?? 'slate'}>{REASON[log.reason]?.t ?? log.reason}</Badge>
                    <span className="v num" style={{ color: log.delta < 0 ? 'var(--err)' : 'var(--ok)' }}>
                      {log.delta > 0 ? '+' : ''}{fmt.usd(log.delta)}
                    </span>
                  </div>
                  <div className="r2" style={{ display: 'flex', gap: 8 }}>
                    <span className="num">{fmt.dt(log.createdAt)}</span>
                    <span style={{ marginLeft: 'auto' }} className="num">
                      余额 {fmt.usd(log.balanceAfter)}
                    </span>
                  </div>
                </div>
              ))}
            </div>
          )}
        </Card>
      </div>

      <Card icon="table2" title="用量明细" note={`按${dim === 'model' ? '模型' : '令牌'}聚合`}>
        {usageQ.isFetching && rows.length === 0 ? (
          <SkLines rows={['w80', 'w60', 'w80', 'w60']} />
        ) : rows.length === 0 ? (
          <EmptyState
            title="所选范围内暂无用量"
            desc="通过网关发起调用后，这里会按模型 / 令牌汇总请求与花费。"
          />
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
                    <td className="right num">{fmt.n(r.requests)}</td>
                    <td className="right num">{fmt.k(r.inTokens)}</td>
                    <td className="right num">{fmt.k(r.outTokens)}</td>
                    <td className="right num">{fmt.usd(r.chargeUsd ?? 0)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      <Card icon="scroll" title="我的请求日志" note="仅显示你名下令牌" style={{ marginTop: 16 }}>
        <div className="table-wrap">
          <table className="tb">
            <thead>
              <tr>
                <th style={{ width: 148 }}>时间</th>
                <th style={{ width: 118 }}>状态</th>
                <th>模型</th>
                <th style={{ width: 116 }}>令牌</th>
                <th className="right" style={{ width: 88 }}>输入</th>
                <th className="right" style={{ width: 88 }}>输出</th>
                <th className="right" style={{ width: 88 }}>总耗时</th>
                <th className="right" style={{ width: 92 }}>花费</th>
              </tr>
            </thead>
            <tbody>
              {logsQ.isFetching && logs.length === 0 ? (
                <tr><td colSpan={8} style={{ padding: 20 }}><SkLines rows={['w80', 'w60', 'w80', 'w60']} /></td></tr>
              ) : logsQ.isError ? (
                <tr><td colSpan={8}>
                  <ErrorState title="日志加载失败" desc="无法读取请求日志。" onRetry={() => void logsQ.refetch()} />
                </td></tr>
              ) : logs.length === 0 ? (
                <tr><td colSpan={8}>
                  <EmptyState title="还没有请求日志" desc="网关转发过请求后，这里会逐条列出耗时、Token 与花费。" />
                </td></tr>
              ) : (
                logs.map(r => (
                  <tr
                    key={r.id}
                    className="clickable"
                    tabIndex={0}
                    onClick={() => setDetail(r)}
                    onKeyDown={e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); setDetail(r); } }}
                  >
                    <td className="mono">{r.ts}</td>
                    <td>
                      {r.statusCode === STATUS_CLIENT_CLOSED ? (
                        <StatusDot status="" text="中断" tone="aux" />
                      ) : okCode(r.statusCode) ? (
                        <StatusDot status="" text="成功" tone="ok" />
                      ) : (
                        <span style={{ display: 'inline-flex', alignItems: 'center', gap: 7 }}>
                          <StatusDot status="" text={r.error ? '失败' : String(r.statusCode)} tone="err" />
                          {r.error && <Badge tone="red">{FAIL_LABEL[classifyError(r.error)]}</Badge>}
                        </span>
                      )}
                    </td>
                    <td><span className="mono ellip" title={r.model}>{r.model}</span></td>
                    <td><span style={{ color: 'var(--t3)' }}>{r.tokenName || '—'}</span></td>
                    <td className="right num">{fmt.k(r.inTokens)}</td>
                    <td className="right num">{fmt.k(r.outTokens)}</td>
                    <td className="right num">{fmt.ms(r.totalMs)}</td>
                    <td className="right num">{fmt.usd(r.chargeUsd ?? 0)}</td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>

        {/* 分页:每页条数 + 页码(与原 antd Table pagination 同状态源) */}
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'flex-end', gap: 12 }}>
          <Select
            value={String(size)}
            style={{ width: 96 }}
            aria-label="每页条数"
            onChange={e => { setPage(1); setSize(Number(e.target.value)); }}
          >
            <option value="20">20 条/页</option>
            <option value="50">50 条/页</option>
            <option value="100">100 条/页</option>
          </Select>
          <Pager page={page} size={size} total={total} onPage={setPage} />
        </div>
      </Card>

      <RequestLogDrawer detail={detail} onClose={() => setDetail(null)} />
      <LedgerDrawer open={ledgerOpen} logs={ledger} onClose={() => setLedgerOpen(false)} />
    </div>
  );
}
