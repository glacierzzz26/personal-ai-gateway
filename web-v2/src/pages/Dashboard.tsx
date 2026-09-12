import { useMemo, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Button, Empty, Table } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useQuery } from '@tanstack/react-query';
import Chart from '@/components/Chart';
import Sparkline from '@/components/Sparkline';
import StatusDot from '@/components/StatusDot';
import { Block as BlockCard, BlockBody, BlockHead, Blocks, MetricBlock } from '@/components/Block';
import PageHeader from '@/components/PageHeader';
import RequestLogDrawer from '@/components/RequestLogDrawer';
import { EmptyState, ErrorState, NoResultState, SkBlock, SkLines, SkMetric } from '@/components/States';
import { IconRefresh } from '@/components/icons';
import { useFailureAttribution } from '@/hooks/useFailureAttribution';
import { useChartColors } from '@/hooks/useChartColors';
import { api } from '@/services/api';
import { TOKENS } from '@/styles/tokens';
import {
  FAIL_DESC, FAIL_LABEL, FAIL_TONE, STATUS_CLIENT_CLOSED, TONE_COLOR, classifyError, fmt,
} from '@/utils/format';
import type { EChartsOption } from 'echarts';
import type { FailKind } from '@/utils/format';
import type { RequestLogItem } from '@/types';

/** "2026-09-03 20" → "20:00" */
const hourLabel = (ts: string) => `${ts.slice(11)}:00`;

/** 环比：(当前 − 上期) / 上期；上期为 0 时无意义，返回 null。 */
function delta(cur: number, prev: number): { text: string; up: boolean } | null {
  if (!prev) return null;
  const r = (cur - prev) / prev;
  return { text: `${r > 0 ? '+' : ''}${(r * 100).toFixed(1)}%`, up: r >= 0 };
}

export default function Dashboard() {
  const navigate = useNavigate();
  const c = useChartColors();
  const [detail, setDetail] = useState<RequestLogItem | null>(null);

  const {
    data: overview, isLoading: ovLoading, isError: ovError, refetch: refetchOv,
  } = useQuery({ queryKey: ['overview'], queryFn: api.getOverview, refetchInterval: 15_000 });
  const { data: channels = [], isLoading: chLoading, isError: chError, refetch: refetchCh } = useQuery({
    queryKey: ['channels'], queryFn: api.getChannels, refetchInterval: 60_000,
  });
  const { data: tokens = [] } = useQuery({ queryKey: ['tokens'], queryFn: api.getTokens });
  const { data: recent = [], isLoading: logLoading, isError: logError, refetch: refetchLogs } = useQuery({
    queryKey: ['recentLogs'],
    queryFn: () => api.recentLogs(8),
    refetchInterval: 15_000,
  });
  /* 今日花费：按模型聚合 1 天（服务端按今日本地口径切桶） */
  const { data: todayUsage } = useQuery({
    queryKey: ['usage', 'model', 1],
    queryFn: () => api.getUsage('model', 1),
    refetchInterval: 60_000,
  });
  const fails = useFailureAttribution();

  const hours = overview?.hours ?? [];
  const days = overview?.days ?? [];

  /* ---------- 指标 ---------- */
  const todayReq = hours.reduce((s, h) => s + h.requests, 0);
  const prevDayReq = days.length >= 2 ? days[days.length - 2].requests : 0;
  const todayCost = days.length ? days[days.length - 1].costUsd : 0;
  const prevDayCost = days.length >= 2 ? days[days.length - 2].costUsd : 0;
  const monthAvgCost = days.length ? days.reduce((s, d) => s + d.costUsd, 0) / days.length : 0;
  const avgFt = overview?.avgFirstTokenMs ?? 0;
  /* 失败率必须与今日请求同口径（都用 24h 桶）；
     overview.totalErrors 是近 7 天合计，拿它除 24h 请求数会算出负数。 */
  const todayErrors = hours.reduce((s, h) => s + h.errors, 0);
  /* 口径与后端 errCond 一致：499 客户端中断不计入分子，也不在分母剔除 */
  const failRate = todayReq ? todayErrors / todayReq : 0;

  const reqDelta = delta(todayReq, prevDayReq);
  const costDelta = delta(todayCost, prevDayCost);

  /* ---------- 24h 图表 ---------- */
  /* 逐小时失败率（%），同时供图表与"失败率"指标块的迷你趋势线使用 */
  const failPct = hours.map(h => (h.requests ? (h.errors / h.requests) * 100 : 0));
  const option: EChartsOption = {
    grid: { left: 52, right: 46, top: 34, bottom: 26 },
    tooltip: {
      trigger: 'axis',
      backgroundColor: c.tooltipBg, borderColor: c.tooltipBorder,
      textStyle: { color: c.text, fontSize: 13 }, borderWidth: 1,
    },
    legend: {
      data: ['请求数', '错误数'], right: 0, top: 0,
      itemWidth: 9, itemHeight: 9, textStyle: { color: c.text, fontSize: 13.5 },
    },
    xAxis: {
      type: 'category', data: hours.map(h => hourLabel(h.ts)), boundaryGap: false,
      axisLine: { lineStyle: { color: c.line } }, axisTick: { show: false },
      axisLabel: { color: c.text, fontSize: 12, interval: 2 },
    },
    yAxis: [
      {
        type: 'value', splitLine: { lineStyle: { color: c.line } },
        axisLabel: { color: c.text, fontSize: 12 },
      },
      /* 右轴给错误数单独刻度 —— 否则错误曲线会被请求量压成一条直线 */
      {
        type: 'value', splitLine: { show: false }, axisLabel: { color: c.text, fontSize: 12 },
      },
    ],
    series: [
      {
        name: '请求数', type: 'line', data: hours.map(h => h.requests),
        showSymbol: false, lineStyle: { width: 2, color: c.primary }, itemStyle: { color: c.primary },
        areaStyle: { color: TOKENS.primary50 },
      },
      {
        name: '错误数', type: 'line', yAxisIndex: 1, data: hours.map(h => h.errors),
        showSymbol: false, lineStyle: { width: 1.6, color: c.error }, itemStyle: { color: c.error },
      },
    ],
  };

  /* ---------- 渠道健康 ---------- */
  const chRows = useMemo(() => [...channels].sort((a, b) => a.priority - b.priority).slice(0, 6), [channels]);

  /* ---------- 花费 Top5 ---------- */
  const topCost = useMemo(() => {
    const rows = todayUsage?.rows ?? [];
    return [...rows].sort((a, b) => b.costUsd - a.costUsd).slice(0, 5);
  }, [todayUsage]);
  const topCostTotal = topCost.reduce((s, r) => s + r.costUsd, 0);

  /* ---------- 额度逼近 ---------- */
  const nearQuota = useMemo(
    () =>
      tokens
        .filter(t => t.quotaUsd > 0 && t.usedUsd / t.quotaUsd >= 0.6)
        .sort((a, b) => b.usedUsd / b.quotaUsd - a.usedUsd / a.quotaUsd),
    [tokens],
  );

  /* ---------- 顶部状态条 ---------- */
  const downCh = channels.filter(ch => ch.status === 'down' || ch.circuitOpen);
  const degradedCh = channels.filter(ch => ch.status === 'degraded');
  const tightTokens = tokens.filter(t => t.quotaUsd > 0 && t.usedUsd / t.quotaUsd >= 0.85);

  const strip = (() => {
    if (ovLoading) return { tone: 'aux' as const, title: '读取中', desc: '正在读取网关状态…' };
    if (ovError) return { tone: 'err' as const, title: '状态不可用', desc: '无法读取网关状态（/overview 请求失败）。' };
    if (channels.length === 0 && tokens.length === 0) {
      return { tone: 'aux' as const, title: '尚未配置', desc: '还没有渠道与令牌，创建后这里会显示全局状态。' };
    }
    if (downCh.length || tightTokens.length) {
      const parts: string[] = [];
      if (downCh.length) {
        parts.push(
          `${downCh.length} 个渠道异常（${downCh.map(c => c.name).join('、')}）`,
        );
      }
      if (tightTokens.length) {
        parts.push(
          `${tightTokens.length} 个令牌额度已用 ≥85%（${tightTokens.map(t => t.name).join('、')}）`,
        );
      }
      return { tone: 'warn' as const, title: '需注意', desc: parts.join(' · ') };
    }
    if (degradedCh.length) {
      return {
        tone: 'warn' as const,
        title: '需注意',
        desc: `${degradedCh.length} 个渠道降级（${degradedCh.map(c => c.name).join('、')}）`,
      };
    }
    return {
      tone: 'ok' as const,
      title: '全部正常',
      desc: `${channels.length} 个渠道可用 · 无熔断 · ${tokens.length} 个令牌额度充足 · 近 1 小时无失败`,
    };
  })();

  const refreshAll = () => {
    void refetchOv();
    void refetchCh();
    void refetchLogs();
    fails.refetch();
  };

  const logCols: ColumnsType<RequestLogItem> = [
    { title: '时间', dataIndex: 'ts', width: 100, render: v => <span className="gw-mono" style={{ fontSize: 13 }}>{String(v).slice(11, 19)}</span> },
    { title: '模型', dataIndex: 'model', render: v => <span className="gw-mono" style={{ fontSize: 13.5 }}>{v}</span> },
    { title: '渠道', dataIndex: 'channelName', width: 150 },
    { title: '令牌', dataIndex: 'tokenName', width: 140, render: v => <span style={{ color: 'var(--gw-text-3)' }}>{v}</span> },
    {
      title: '状态', dataIndex: 'statusCode', width: 170,
      render: (v: number, r) => {
        if (v === STATUS_CLIENT_CLOSED) return <StatusDot status="" text="中断" tone="aux" />;
        if (v >= 400 || r.error) {
          const kind: FailKind = classifyError(r.error);
          return (
            <span style={{ display: 'inline-flex', alignItems: 'center', gap: 7 }}>
              <StatusDot status="" text="失败" tone="err" />
              <span className="gw-badge">{FAIL_LABEL[kind]}</span>
            </span>
          );
        }
        return <StatusDot status="" text="成功" tone="ok" />;
      },
    },
    { title: '首字', dataIndex: 'firstTokenMs', align: 'right', width: 90, render: v => <span className="gw-num">{v ? fmt.ms(v) : '—'}</span> },
    { title: '总耗时', dataIndex: 'totalMs', align: 'right', width: 100, render: v => <span className="gw-num">{fmt.ms(v)}</span> },
    { title: '花费', dataIndex: 'costUsd', align: 'right', width: 100, render: v => <span className="gw-num">{v ? fmt.usd(v, 4) : '—'}</span> },
  ];

  const maxFail = Math.max(...fails.buckets.map(b => b.count), 1);

  return (
    <div className="gw-page">
      <PageHeader
        title="运行总览"
        desc={<>当前状态、失败归因与今日花费 · 每 15 秒自动刷新</>}
        extra={
          <Button icon={<IconRefresh />} onClick={refreshAll}>
            刷新
          </Button>
        }
      />

      <Blocks>
        {/* 区块 1：提示条 */}
        <div
          role="status"
          style={{
            display: 'flex',
            alignItems: 'flex-start',
            gap: 11,
            border: '1px solid var(--gw-border)',
            borderLeft: `3px solid ${TONE_COLOR[strip.tone]}`,
            borderRadius: 'var(--gw-r-card)',
            background: 'var(--gw-card)',
            padding: '13px 16px',
            fontSize: 14,
          }}
        >
          <span style={{ display: 'inline-flex', alignItems: 'center', gap: 7, fontWeight: 500, color: 'var(--gw-text)' }}>
            <i className={`gw-dot ${strip.tone}`} />
            {strip.title}
          </span>
          <span style={{ color: 'var(--gw-text-2)' }}>{strip.desc}</span>
        </div>

        {/* 区块 2：指标块行 */}
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(4, minmax(0,1fr))', gap: 18 }}>
          {ovLoading ? (
            <>
              {[0, 1, 2, 3].map(i => (
                <BlockCard key={i}>
                  <BlockBody>
                    <SkMetric />
                  </BlockBody>
                </BlockCard>
              ))}
            </>
          ) : (
            <>
              <MetricBlock
                label="今日请求"
                value={fmt.n(todayReq)}
                delta={reqDelta?.text}
                deltaGood={false}
                note="较昨日全天"
                spark={<Sparkline values={hours.map(h => h.requests)} />}
              />
              <MetricBlock
                label="今日花费"
                value={fmt.usd(todayCost)}
                delta={costDelta?.text}
                deltaGood={false}
                note={`本月日均 ${fmt.usd(monthAvgCost)}`}
                spark={<Sparkline values={days.map(d => Number(d.costUsd.toFixed(2)))} />}
              />
              <MetricBlock
                label="平均首字延迟"
                value={String(Math.round(avgFt))}
                unit="ms"
                note="近 7 天成功请求均值"
              />
              <MetricBlock
                label="失败率"
                value={(failRate * 100).toFixed(1)}
                unit="%"
                note={`24h ${fails.faultTotal} 次故障；另 ${fails.canceled} 次中断不计入`}
                spark={<Sparkline values={failPct} color={TOKENS.err} />}
              />
            </>
          )}
        </div>

        {/* 区块 3：24 小时曲线 */}
        <BlockCard>
          <BlockHead
            title="请求与错误"
            sub="近 24 小时 · 按小时"
            right={
              <>
                <span style={{ display: 'inline-flex', alignItems: 'center', gap: 7, fontSize: 13.5, color: 'var(--gw-text-2)' }}>
                  <i style={{ width: 9, height: 9, borderRadius: 4, background: TOKENS.c1, display: 'inline-block' }} />
                  请求数
                </span>
                <span style={{ display: 'inline-flex', alignItems: 'center', gap: 7, fontSize: 13.5, color: 'var(--gw-text-2)' }}>
                  <i style={{ width: 9, height: 9, borderRadius: 4, background: TOKENS.err, display: 'inline-block' }} />
                  错误数
                </span>
              </>
            }
          />
          <BlockBody>
            {ovLoading ? (
              <SkBlock />
            ) : ovError ? (
              <ErrorState
                title="图表加载失败"
                desc="/overview 请求失败，可能是网关管理面不可达。"
                onRetry={() => void refetchOv()}
              />
            ) : todayReq === 0 && hours.every(h => h.requests === 0) ? (
              <EmptyState
                title="还没有请求"
                desc="创建渠道与访问令牌后，这里会显示逐小时请求量与错误量。"
                action={<Button size="small" type="primary" onClick={() => navigate('/channels')}>创建渠道</Button>}
              />
            ) : (
              <Chart option={option} height={300} />
            )}
          </BlockBody>
        </BlockCard>

        {/* 区块 4：渠道健康 × 失败归因 */}
        <div style={{ display: 'grid', gridTemplateColumns: 'minmax(0,2.1fr) minmax(0,1fr)', gap: 18 }}>
          <BlockCard>
            <BlockHead
              title="渠道健康度"
              right={<button type="button" className="gw-link" onClick={() => navigate('/channels')}>全部渠道 →</button>}
            />
            {chLoading ? (
              <BlockBody><SkLines rows={['w80', 'w60', 'w80', 'w40']} /></BlockBody>
            ) : chError ? (
              <BlockBody>
                <ErrorState title="渠道数据加载失败" desc="无法读取渠道健康度，路由与转发不受影响。" onRetry={() => void refetchCh()} />
              </BlockBody>
            ) : channels.length === 0 ? (
              <BlockBody>
                <EmptyState
                  title="还没有渠道"
                  desc="一条渠道 = 一个上游端点与凭据。至少建一个才能转发请求。"
                  action={<Button size="small" type="primary" onClick={() => navigate('/channels')}>新建渠道</Button>}
                />
              </BlockBody>
            ) : (
              <div style={{ overflowX: 'auto' }}>
                <table className="gw-table">
                  <thead>
                    <tr>
                      <th>渠道</th><th>供应商</th><th>状态</th>
                      <th className="num">成功率</th><th className="num">首字延迟</th>
                      <th className="num">今日 Token</th><th className="num">今日花费</th>
                    </tr>
                  </thead>
                  <tbody>
                    {chRows.map(ch => (
                      <tr key={ch.id} className="clickable" tabIndex={0}
                        onClick={() => navigate('/channels')}
                        onKeyDown={e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); navigate('/channels'); } }}>
                        <td className="name">{ch.name}</td>
                        <td>{ch.provider}</td>
                        <td>
                          <StatusDot status={ch.status} />
                          {ch.circuitOpen && <span className="gw-badge" style={{ marginLeft: 6 }}>熔断中</span>}
                        </td>
                        <td className="num">{ch.successRate ? fmt.pct(ch.successRate) : '—'}</td>
                        <td className="num">{ch.status === 'down' ? '—' : fmt.ms(ch.latencyMs)}</td>
                        <td className="num">{ch.todayTokens ? fmt.k(ch.todayTokens) : '—'}</td>
                        <td className="num">{ch.todayCostUsd ? fmt.usd(ch.todayCostUsd) : '—'}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </BlockCard>

          <BlockCard>
            <BlockHead title="失败归因" sub="近 24 小时" />
            {fails.isLoading ? (
              <BlockBody><SkLines rows={['w60', 'w80', 'w60']} /></BlockBody>
            ) : fails.isError ? (
              <BlockBody>
                <ErrorState title="归因数据加载失败" desc="日志聚合查询失败，失败次数暂时未知。" onRetry={fails.refetch} />
              </BlockBody>
            ) : channels.length === 0 ? (
              <BlockBody>
                <EmptyState title="还没有请求" desc="有失败时这里会按归因拆开：上游错误 / 首字节超时 / 中途静默，并单列不计入故障的客户端中断。" />
              </BlockBody>
            ) : fails.faultTotal === 0 && fails.canceled === 0 ? (
              <BlockBody>
                <NoResultState title="近 24 小时无失败" desc="没有可归因的失败记录，渠道健康度未被扣分。" />
              </BlockBody>
            ) : (
              <div className="gw-list">
                {fails.buckets.map(b => (
                  <div className="gw-li" key={b.kind}>
                    <div className="r1">
                      <span className="k">{FAIL_LABEL[b.kind]}</span>
                      <span className="n">
                        {b.kind === 'canceled' ? '不计入' : fails.faultTotal ? fmt.pct(b.count / fails.faultTotal, 0) : '0%'}
                      </span>
                      <span className="v">{b.count}</span>
                    </div>
                    <div className="gw-bar" role="img" aria-label={`${FAIL_LABEL[b.kind]} ${b.count} 次`}>
                      <i style={{ width: `${(b.count / maxFail) * 100}%`, background: TONE_COLOR[FAIL_TONE[b.kind]] }} />
                    </div>
                    <div className="r2">{FAIL_DESC[b.kind]}</div>
                  </div>
                ))}
              </div>
            )}
          </BlockCard>
        </div>

        {/* 区块 5：花费 Top × 额度逼近 */}
        <div style={{ display: 'grid', gridTemplateColumns: 'minmax(0,1fr) minmax(0,1fr)', gap: 18 }}>
          <BlockCard>
            <BlockHead
              title="花费 Top 5 模型"
              sub={`今日 · 合计 ${fmt.usd(topCostTotal)}`}
              right={<button type="button" className="gw-link" onClick={() => navigate('/logs')}>用量明细 →</button>}
            />
            {topCost.length === 0 ? (
              <BlockBody>
                <EmptyState title="今日还没有花费" desc="产生请求后这里按模型聚合成花费排行。" />
              </BlockBody>
            ) : (
              <div className="gw-list">
                {topCost.map(r => (
                  <div className="gw-li" key={r.name}>
                    <div className="r1">
                      <span className="k gw-mono" style={{ fontSize: 13.5 }}>{r.name}</span>
                      <span className="n">{fmt.n(r.requests)} 次</span>
                      <span className="v">{fmt.usd(r.costUsd)}</span>
                    </div>
                    <div className="gw-bar" role="img" aria-label={`${r.name} 花费占比 ${fmt.pct(r.costUsd / (topCostTotal || 1), 0)}`}>
                      <i style={{ width: `${(r.costUsd / (topCostTotal || 1)) * 100}%`, background: TOKENS.c1 }} />
                    </div>
                  </div>
                ))}
              </div>
            )}
          </BlockCard>

          <BlockCard>
            <BlockHead
              title="额度逼近"
              sub="用量 ≥ 60% 的令牌"
              right={<button type="button" className="gw-link" onClick={() => navigate('/tokens')}>全部令牌 →</button>}
            />
            {tokens.length === 0 ? (
              <BlockBody>
                <EmptyState
                  title="还没有令牌"
                  desc="令牌用满额度后请求会被拒（quota_exceeded）。"
                  action={<Button size="small" type="primary" onClick={() => navigate('/tokens')}>新建令牌</Button>}
                />
              </BlockBody>
            ) : nearQuota.length === 0 ? (
              <BlockBody>
                <NoResultState title="没有令牌逼近额度" desc="所有令牌用量均低于 60%，无耗尽风险。" />
              </BlockBody>
            ) : (
              <div className="gw-list">
                {nearQuota.map(t => {
                  const r = t.usedUsd / t.quotaUsd;
                  const tone = r >= 0.85 ? 'warn' : 'primary';
                  return (
                    <div className="gw-li" key={t.id}>
                      <div className="r1">
                        <span className="k">{t.name}</span>
                        <span className="n">{(r * 100).toFixed(0)}%</span>
                        <span className="v">
                          {fmt.usd(t.usedUsd)}{' '}
                          <span style={{ color: 'var(--gw-text-3)', fontWeight: 400 }}>/ {fmt.usd(t.quotaUsd)}</span>
                        </span>
                      </div>
                      <div className="gw-bar" role="img" aria-label={`${t.name} 额度已用 ${(r * 100).toFixed(0)}%`}>
                        <i style={{ width: `${r * 100}%`, background: tone === 'warn' ? TOKENS.warn : TOKENS.c1 }} />
                      </div>
                      <div className="r2">
                        {r >= 1
                          ? '额度已用尽，新请求会被拒（HTTP 402）'
                          : `剩余 ${fmt.usd(t.quotaUsd - t.usedUsd)}，按当前速度会持续逼近`}
                      </div>
                    </div>
                  );
                })}
              </div>
            )}
          </BlockCard>
        </div>

        {/* 区块 6：最近请求 */}
        <BlockCard>
          <BlockHead
            title="最近请求"
            right={<button type="button" className="gw-link" onClick={() => navigate('/logs')}>全部日志 →</button>}
          />
          <BlockBody flush>
            <Table<RequestLogItem>
              rowKey="id"
              size="middle"
              loading={logLoading && recent.length === 0}
              dataSource={recent}
              columns={logCols}
              scroll={{ x: 1080 }}
              pagination={false}
              locale={{
                emptyText: logError ? (
                  <ErrorState title="日志加载失败" desc="/logs 请求失败，不影响网关转发。" onRetry={() => void refetchLogs()} />
                ) : (
                  <Empty description="暂无请求，建好渠道与令牌后打一次请求就能看到" image={Empty.PRESENTED_IMAGE_SIMPLE} />
                ),
              }}
              onRow={r => ({
                onClick: () => setDetail(r),
                onKeyDown: e => {
                  if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); setDetail(r); }
                },
                tabIndex: 0,
                style: { cursor: 'pointer' },
              })}
            />
          </BlockBody>
        </BlockCard>
      </Blocks>

      <RequestLogDrawer detail={detail} onClose={() => setDetail(null)} />
    </div>
  );
}

