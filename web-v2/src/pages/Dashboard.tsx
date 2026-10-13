import { useMemo, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import Chart from '@/components/Chart';
import RangePicker, { defaultRange, rangeLabel, toQuery } from '@/components/RangePicker';
import RequestLogDrawer from '@/components/RequestLogDrawer';
import { Card, Kpi, Strip, Badge, Empty, SkLines, Btn, PageHead, StatusDot, Spark } from '@/ui/primitives';
import { useFailureAttribution } from '@/hooks/useFailureAttribution';
import { useChartColors } from '@/hooks/useChartColors';
import { api } from '@/services/api';
import { channelLabel } from '@/utils/channel';
import {
  FAIL_DESC, FAIL_LABEL, STATUS_CLIENT_CLOSED, classifyError, fmt,
} from '@/utils/format';
import { QUOTA_ALERT, QUOTA_WARN, QUOTA_WINS, hasCap, pctText, quotaRatio } from '@/utils/quota';
import type { FailKind } from '@/utils/format';
import type { Channel, ChannelQuota, ChannelQuotaItem, CustomerRow, RequestLogItem } from '@/types';

/** "2026-09-03 20" → "20:00" */
const hourLabel = (ts: string) => `${ts.slice(11)}:00`;

/** 图表横轴标签:小时桶 → "20:00";日桶 → "09-03"。 */
const axisLabel = (bucket: 'hour' | 'day', ts: string) => (bucket === 'hour' ? hourLabel(ts) : ts.slice(5));

/** 环比：（当前 − 上期）/ 上期；上期为 0 时无意义，返回 null。 */
function delta(cur: number, prev: number): { text: string; up: boolean } | null {
  if (!prev) return null;
  const r = (cur - prev) / prev;
  return { text: `${r > 0 ? '+' : ''}${(r * 100).toFixed(1)}%`, up: r >= 0 };
}

/** 风险等级 -> 展示文案与底色（客户关注区）。 */
const RISK_META: Record<CustomerRow['risk'], { label: string; tone: 'err' | 'warn' | 'aux' }> = {
  depleted: { label: '已欠费', tone: 'err' },
  low: { label: '余额预警', tone: 'warn' },
  ok: { label: '正常', tone: 'aux' },
};

/** 渠道状态 → 圆点语义 + 文字（对齐原型 statusDotHtml）。 */
function chDot(st: string): { dot: 'g' | 'a' | 'r' | 's'; text: string } {
  switch (st) {
    case 'down': return { dot: 'r', text: '异常' };
    case 'degraded': return { dot: 'a', text: '降级' };
    case 'disabled': return { dot: 's', text: '停用' };
    case 'unknown': return { dot: 's', text: '未知' };
    default: return { dot: 'g', text: '正常' };
  }
}

/** 失败归因色 → CSS 变量（对齐原型 FAIL_TONE 着色）。 */
const FAIL_COLOR: Record<FailKind, string> = {
  upstream: 'var(--err)',
  first: 'var(--warn)',
  idle: 'var(--warn)',
  canceled: 'var(--t3)',
};

/** 状态条 tone → 圆点 class（对齐原型 strip dot）。 */
const STRIP_DOT: Record<'ok' | 'warn' | 'err' | 'aux', string> = { ok: 'g', warn: 'a', err: 'r', aux: 's' };

const moneyAxis = (v: number) =>
  '$' + (v >= 1000 ? `${(v / 1000).toFixed(1).replace(/\.0$/, '')}k` : String(Math.round(v)));

const KpiSk = () => (
  <div className="card kpi">
    <SkLines rows={['w80', 'w40']} />
  </div>
);

export default function Dashboard() {
  const navigate = useNavigate();
  const c = useChartColors();
  const qc = useQueryClient();
  const [detail, setDetail] = useState<RequestLogItem | null>(null);
  /* 统计窗口:默认最近 1 天(issue #13),预设可切 7/30 或自定义区间 */
  const [range, setRange] = useState(defaultRange);
  const rq = useMemo(() => toQuery(range), [range]);

  const {
    data: overview, isLoading: ovLoading, isError: ovError, refetch: refetchOv,
  } = useQuery({
    queryKey: ['overview', rq],
    queryFn: () => api.getOverview(rq),
    refetchInterval: 15_000,
  });
  const { data: channels = [], isLoading: chLoading, isError: chError, refetch: refetchCh } = useQuery({
    queryKey: ['channels'], queryFn: api.getChannels, refetchInterval: 60_000,
  });
  const { data: tokens = [] } = useQuery({ queryKey: ['tokens'], queryFn: api.getTokens });
  /* 渠道上游额度:一次批量拿全渠道(服务端并发 + 短 TTL 缓存),
     与渠道页共用同一份缓存,不会把上游打成密集轮询。失败静默 —— 首页其余部分照常显示。 */
  const { data: quotaItems = [], isError: quotaError } = useQuery({
    queryKey: ['channels-quota'],
    queryFn: api.channelsQuota,
    refetchInterval: 60_000,
    staleTime: 60_000,
    retry: 0,
  });
  /* 首次额度查询在途(最长 6s)。期间**不能**让状态条断言「全部正常」——额度未知时
     只报「读取中」,否则就是在没数据的时候给人一个假的好消息。
     查询**失败**则不算在途(否则会卡在「读取中」不再前进),退回按已知信息展示 ——
     额度是可选观测层,它挂了不该让整个状态条失真。 */
  const quotaPending = channels.length > 0 && quotaItems.length === 0 && !quotaError;
  /* 客户关注区:余额告警 + 窗口内消耗排行(与首屏窗口同源) */
  const { data: focus, isLoading: foLoading, isError: foError, refetch: refetchFocus } = useQuery({
    queryKey: ['customers', 'focus', rq],
    queryFn: () => api.getCustomerFocus(rq),
    refetchInterval: 60_000,
  });
  const { data: recent = [], isLoading: logLoading, isError: logError, refetch: refetchLogs } = useQuery({
    queryKey: ['recentLogs'],
    queryFn: () => api.recentLogs(8),
    refetchInterval: 15_000,
  });
  /* 窗口内花费 Top5(成本口径,站点侧):与上方曲线同窗口 */
  const { data: rangeUsage } = useQuery({
    queryKey: ['usage', 'model', rq],
    queryFn: () => api.getUsage('model', rq),
    refetchInterval: 60_000,
  });
  const fails = useFailureAttribution();

  const bucket = overview?.bucket ?? 'day';
  const points = overview?.points ?? [];
  const custPoints = overview?.customerPoints ?? [];

  /* ---------- 经营口径(营收/成本/毛利) ---------- */
  const totals = overview?.totals;
  const prev = overview?.prev;
  const revenue = totals?.revenueUsd ?? 0;
  const bizCost = totals?.costUsd ?? 0;
  const margin = totals?.marginUsd ?? 0;
  const marginRate = totals?.marginRate ?? 0;
  /* 环比:预设窗口下后端给 prev(昨日整日);自定义区间 prev=null,不显示。 */
  const revDelta = prev ? delta(revenue, prev.revenueUsd) : null;
  const marginDelta = prev ? delta(margin, prev.marginUsd) : null;
  const bizReqDelta = prev ? delta(totals?.requests ?? 0, prev.requests) : null;

  /* ---------- 全站指标(稳定性,与经营分开) ---------- */
  const totalReq = overview?.totalRequests ?? 0;
  const avgFt = overview?.avgFirstTokenMs ?? 0;
  /* 失败率口径与后端一致:499 客户端中断不计入分子,也不从分母剔除 */
  const failRate = totalReq ? (overview?.totalErrors ?? 0) / totalReq : 0;
  const rl = rangeLabel(range);

  /* ---------- 曲线 ---------- */
  /* 逐桶失败率（%），供"失败率"指标块的迷你趋势线使用 */
  const failPct = points.map(h => (h.requests ? (h.errors / h.requests) * 100 : 0));
  /* 经营曲线取售价/成本,缺失按 0(后端恒返回数值,这里只是类型兜底)。 */
  const moneyOf = (v: number | undefined) => Number((v ?? 0).toFixed(4));

  /* 营收 vs 成本（经营口径,与上方条同源） */
  const moneyChart = {
    labels: custPoints.map(h => axisLabel(bucket, h.ts)),
    series: [
      { name: '营收', data: custPoints.map(h => moneyOf(h.chargeUsd)), color: c.primary, fill: true },
      { name: '成本', data: custPoints.map(h => moneyOf(h.costUsd)), color: c.warn, fill: false },
    ],
    yFmt: moneyAxis,
    tipFmt: (v: number) => fmt.usd(v),
  };

  /* 请求与错误（全站,窗口随筛选器） */
  const reqChart = {
    labels: points.map(h => axisLabel(bucket, h.ts)),
    series: [
      { name: '请求数', data: points.map(h => h.requests), color: c.primary, fill: true },
      { name: '错误数', data: points.map(h => h.errors), color: c.error, fill: false },
    ],
    yFmt: (v: number) => (v >= 1000 ? `${(v / 1000).toFixed(1).replace(/\.0$/, '')}k` : String(Math.round(v))),
    tipFmt: (v: number) => fmt.n(v),
  };

  /* ---------- 渠道健康 ---------- */
  const chRows = useMemo(() => [...channels].sort((a, b) => a.priority - b.priority).slice(0, 6), [channels]);

  /* ---------- 花费 Top5 ---------- */
  const topCost = useMemo(() => {
    const rows = rangeUsage?.rows ?? [];
    return [...rows].sort((a, b) => b.costUsd - a.costUsd).slice(0, 5);
  }, [rangeUsage]);
  const topCostTotal = topCost.reduce((s, r) => s + r.costUsd, 0);
  /* 排行条按分类色板逐条取色 —— 模型间一眼可分,而不是同一根靛蓝条长短不一 */
  const costHues = ['var(--chart-2)', 'var(--chart-3)', 'var(--chart-4)', 'var(--brand)', 'var(--t3)'];

  /* ---------- 额度逼近 ---------- */
  /* 令牌额度:已用 / 上限 ≥ 逼近阈值(60%)。 */
  const nearQuota = useMemo(
    () =>
      tokens
        .filter(t => t.quotaUsd > 0 && t.usedUsd / t.quotaUsd >= QUOTA_WARN)
        .sort((a, b) => b.usedUsd / b.quotaUsd - a.usedUsd / a.quotaUsd),
    [tokens],
  );

  /* ---------- 上游渠道额度 ---------- */
  /* 渠道 id → 额度。批量端点逐渠道返回,数组顺序即渠道顺序,取不到的就是「未知」。 */
  const quotaById = useMemo(() => {
    const m = new Map<number, ChannelQuota>();
    for (const it of quotaItems as ChannelQuotaItem[]) m.set(it.id, it.quota);
    return m;
  }, [quotaItems]);

  /**
   * 上游渠道额度逼近/告警的渠道行。
   *
   * 两条排除规则:
   *   - **查不到 ≠ 告警**:未配置、不支持、查询失败(quotaRatio 为 null)一律不入选 ——
   *     第三方中转的「未配置」是常态,把它算成告警会让首页永远在喊狼来了。
   *   - **停用渠道不算**:已停用的渠道不承载流量,额度见底不影响业务,不做告警。
   */
  const chQuotaRows = useMemo(() => {
    const rows: Array<{ ch: Channel; q: ChannelQuota; ratio: number }> = [];
    for (const ch of channels) {
      if (!ch.enabled) continue;
      const q = quotaById.get(ch.id);
      if (!q) continue;
      const ratio = quotaRatio(q);
      if (ratio != null && ratio >= QUOTA_WARN) rows.push({ ch: ch, q, ratio });
    }
    return rows.sort((a, b) => b.ratio - a.ratio);
  }, [channels, quotaById]);
  const chQuotaAlert = chQuotaRows.filter(r => r.ratio >= QUOTA_ALERT);

  /* ---------- 顶部状态条 ---------- */
  const downCh = channels.filter(ch => ch.status === 'down' || ch.circuitOpen);
  const degradedCh = channels.filter(ch => ch.status === 'degraded');
  const tightTokens = tokens.filter(t => t.quotaUsd > 0 && t.usedUsd / t.quotaUsd >= QUOTA_ALERT);
  const atRisk = focus?.atRisk ?? [];

  const strip = (() => {
    if (ovLoading) return { tone: 'aux' as const, title: '读取中', desc: '正在读取网关状态…' };
    if (ovError) return { tone: 'err' as const, title: '状态不可用', desc: '无法读取网关状态（/overview 请求失败）。' };
    if (channels.length === 0 && tokens.length === 0) {
      return { tone: 'aux' as const, title: '尚未配置', desc: '还没有渠道与令牌，创建后这里会显示全局状态。' };
    }
    /* 站主最该先看到的是「谁要断粮了」——按「客户断粮 > 上游断粮 > 自己断粮」排:
       客户余额告警 → 渠道异常 → 上游渠道额度 → 令牌额度。 */
    if (atRisk.length || downCh.length || chQuotaAlert.length || tightTokens.length) {
      const parts: string[] = [];
      if (atRisk.length) {
        parts.push(`${atRisk.length} 个客户余额告警（${atRisk.map(u => u.username).join('、')}）`);
      }
      if (downCh.length) {
        parts.push(
          `${downCh.length} 个渠道异常（${downCh.map(c => c.name).join('、')}）`,
        );
      }
      if (chQuotaAlert.length) {
        parts.push(
          `${chQuotaAlert.length} 个上游渠道额度已用 ≥85%（${chQuotaAlert.map(r => r.ch.name).join('、')}）`,
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
    /* 上游额度还没读回来(首屏读 6s):先别断言「全部正常」——没读到的部分不敢担保。 */
    if (quotaPending) {
      return { tone: 'aux' as const, title: '读取中', desc: '渠道健康正常，正在读取上游额度…' };
    }
    return {
      tone: 'ok' as const,
      title: '全部正常',
      desc: `${channels.length} 个渠道可用 · 无熔断 · ${tokens.length} 个令牌额度充足 · 无客户欠费`,
    };
  })();

  const refreshAll = () => {
    void refetchOv();
    void refetchCh();
    void refetchLogs();
    void refetchFocus();
    fails.refetch();
    // 额度有服务端 TTL 缓存,这里主动失效一次,让「刷新」真的重新问上游。
    void qc.invalidateQueries({ queryKey: ['channels-quota'] });
  };

  /* 消耗 Top:后端已按营收降序返回,取前 5 展示。 */
  const topCustomers = (focus?.top ?? []).slice(0, 5);
  const topCustTotal = topCustomers.reduce((s, r) => s + r.spendUsd, 0);

  const maxFail = Math.max(...fails.buckets.map(b => b.count), 1);

  return (
    <div className="gw-page">
      <PageHead
        title="运行总览"
        sub={<>今日营收与成本、客户余额风险、渠道健康与失败归因 · 每 15 秒自动刷新</>}
        actions={
          <div style={{ display: 'flex', gap: 10, alignItems: 'center' }}>
            <RangePicker value={range} onChange={setRange} />
            <Btn icon="refresh" onClick={refreshAll}>刷新</Btn>
          </div>
        }
      />

      <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
        {/* 区块 1：状态条 */}
        <Strip
          tone={strip.tone}
          title={<><span className={`dot ${STRIP_DOT[strip.tone]}`} />{strip.title}</>}
          desc={strip.desc}
        />

        {/* 区块 2：经营条 —— 站主每天打开先看这门生意今天赚了多少。
            只算客户归属流量(见后端 MarginTotals):营收=charge_usd、成本=cost,
            毛利=两者之差。改造前的旧日志与站主自用不进这个口径。
            环比基准是「昨日整日」(后端回填 prev),不是前 1×24 小时。 */}
        <div className="kpi-grid" style={{ gridTemplateColumns: 'repeat(4,minmax(0,1fr))' }}>
          {ovLoading ? (
            <>
              {[0, 1, 2, 3].map(i => <KpiSk key={i} />)}
            </>
          ) : (
            <>
              <Kpi
                label="营收"
                value={fmt.usd(revenue)}
                trend={revDelta ? { dir: revDelta.up ? 'up' : 'down', good: true, cmp: revDelta.text } : undefined}
                foot={<span className="cell-sub" style={{ whiteSpace: 'normal' }}>{`${rl} · 客户实付${prev ? ' · 较昨日' : ''}`}</span>}
                spark={<Spark data={custPoints.map(h => moneyOf(h.chargeUsd))} />}
              />
              <Kpi
                label="成本"
                value={fmt.usd(bizCost)}
                foot={<span className="cell-sub" style={{ whiteSpace: 'normal' }}>{`${rl} · 付上游（仅客户流量）`}</span>}
                spark={<Spark data={custPoints.map(h => moneyOf(h.costUsd))} color={c.warn} />}
              />
              <Kpi
                label="毛利"
                value={fmt.usd(margin)}
                trend={marginDelta ? { dir: marginDelta.up ? 'up' : 'down', good: true, cmp: marginDelta.text } : undefined}
                foot={<span className="cell-sub" style={{ whiteSpace: 'normal' }}>{`${rl} · 毛利率 ${fmt.pct(marginRate)}${prev ? ' · 较昨日' : ''}`}</span>}
              />
              <Kpi
                label="客户请求数"
                value={fmt.n(totals?.requests ?? 0)}
                trend={bizReqDelta ? { dir: bizReqDelta.up ? 'up' : 'down', good: true, cmp: bizReqDelta.text } : undefined}
                foot={<span className="cell-sub" style={{ whiteSpace: 'normal' }}>{`${rl} · 全站 ${fmt.n(totalReq)} 次${prev ? ' · 较昨日' : ''}`}</span>}
              />
            </>
          )}
        </div>

        {/* 区块 2b：稳定性条 —— 全站口径的网关健康(与经营的客户口径分开)。
            首屏先答「今天赚了多少」,再答「转得顺不顺」。 */}
        <div className="kpi-grid" style={{ gridTemplateColumns: 'repeat(3,minmax(0,1fr))' }}>
          <Kpi
            label="全站请求数"
            value={fmt.n(totalReq)}
            foot={<span className="cell-sub" style={{ whiteSpace: 'normal' }}>{`${rl} · 含站主自用`}</span>}
          />
          <Kpi
            label="平均首字延迟"
            value={String(Math.round(avgFt))}
            unit="ms"
            foot={<span className="cell-sub" style={{ whiteSpace: 'normal' }}>{`${rl} · 成功流式请求均值`}</span>}
          />
          <Kpi
            label="失败率"
            value={(failRate * 100).toFixed(1)}
            unit="%"
            foot={<span className="cell-sub" style={{ whiteSpace: 'normal' }}>{`${rl} ${fmt.n(overview?.totalErrors ?? 0)} 次错误${fails.canceled ? `；另 ${fails.canceled} 次中断不计入` : ''}`}</span>}
            spark={<Spark data={failPct} color={c.error} />}
          />
        </div>

        {/* 区块 3：营收 vs 成本曲线(经营口径,与上方条同源) */}
        <Card icon="activity" title="营收与成本" note={`${rl} · ${bucket === 'hour' ? '按小时' : '按日'} · 仅客户流量`}>
          {ovLoading ? (
            <SkLines rows={['w80', 'w60', 'w80', 'w40']} />
          ) : ovError ? (
            <Empty icon="warn" title="图表加载失败">
              <Btn size="sm" onClick={() => void refetchOv()}>重试</Btn>
            </Empty>
          ) : (totals?.requests ?? 0) === 0 && custPoints.every(h => h.chargeUsd === 0) ? (
            <Empty icon="box" title="还没有客户流量">
              创建客户账号与令牌后，这里按桶显示营收与成本。站主自用流量不计入经营口径。
              <Btn size="sm" variant="primary" style={{ marginTop: 12 }} onClick={() => navigate('/users')}>管理客户</Btn>
            </Empty>
          ) : (
            <Chart labels={moneyChart.labels} series={moneyChart.series} h={280} yFmt={moneyChart.yFmt} tipFmt={moneyChart.tipFmt} />
          )}
        </Card>

        {/* 区块 4：客户关注区 —— 谁欠费、谁在放量 */}
        <Card
          icon="users"
          title="客户关注"
          note={focus?.window ?? `${rl} · 余额与消耗`}
          extra={<span className="card-link" onClick={() => navigate('/users')}>全部客户 →</span>}
        >
          {foLoading ? (
            <SkLines rows={['w80', 'w60', 'w80']} />
          ) : foError ? (
            <Empty icon="warn" title="客户数据加载失败">
              /customers/focus 请求失败，余额与消耗暂时未知。
              <Btn size="sm" style={{ marginTop: 12 }} onClick={() => void refetchFocus()}>重试</Btn>
            </Empty>
          ) : (
            <div className="two-col">
              {/* 左:余额告警 —— 客户被拒 = 客户在流失,站主最该先处理 */}
              <div>
                <div className="cell-sub" style={{ marginBottom: 8 }}>余额告警</div>
                {atRisk.length === 0 ? (
                  <Empty icon="check" title="无欠费客户">所有客户余额均能覆盖近 1 天消耗。</Empty>
                ) : (
                  <div>
                    {atRisk.map(u => {
                      const meta = RISK_META[u.risk];
                      return (
                        <div className="li" key={u.id}>
                          <div className="r1">
                            <span className="k">{u.username}</span>
                            <span className="n">
                              <span className={`dot ${meta.tone === 'err' ? 'r' : meta.tone === 'warn' ? 'a' : 's'}`} style={{ marginRight: 6 }} />
                              {meta.label}
                            </span>
                            <span className="v mono">{fmt.usd(u.balanceUsd)}</span>
                          </div>
                          <div className="r2">{u.note}</div>
                        </div>
                      );
                    })}
                  </div>
                )}
              </div>

              {/* 右:消耗 Top —— 涨得快的可能是异常或被刷 */}
              <div>
                <div className="cell-sub" style={{ marginBottom: 8 }}>消耗 Top（{rl}）</div>
                {topCustomers.length === 0 ? (
                  <Empty icon="box" title="窗口内无客户消耗">所选范围内还没有客户产生的请求。</Empty>
                ) : (
                  <div>
                    {topCustomers.map((u, i) => (
                      <div className="li" key={u.id}>
                        <div className="r1">
                          <span className="k">{u.username}</span>
                          <span className="n">{fmt.n(u.requests)} 次</span>
                          <span className="v mono">{fmt.usd(u.spendUsd)}</span>
                        </div>
                        <div className="bar" role="img" aria-label={`${u.username} 营收占比 ${fmt.pct(u.spendUsd / (topCustTotal || 1), 0)}`}>
                          <i style={{ width: `${(u.spendUsd / (topCustTotal || 1)) * 100}%`, background: costHues[i % costHues.length] }} />
                        </div>
                      </div>
                    ))}
                  </div>
                )}
              </div>
            </div>
          )}
        </Card>

        {/* 区块 5：请求与错误曲线(全站,窗口随筛选器) */}
        <Card
          icon="activity"
          title="请求与错误"
          note={`${rl} · ${bucket === 'hour' ? '按小时' : '按日'} · 全站（含站主自用）`}
        >
          {ovLoading ? (
            <SkLines rows={['w80', 'w60', 'w80', 'w40']} />
          ) : ovError ? (
            <Empty icon="warn" title="图表加载失败">
              /overview 请求失败，可能是网关管理面不可达。
              <Btn size="sm" style={{ marginTop: 12 }} onClick={() => void refetchOv()}>重试</Btn>
            </Empty>
          ) : totalReq === 0 && points.every(h => h.requests === 0) ? (
            <Empty icon="box" title="还没有请求">
              创建渠道与访问令牌后，这里会逐桶显示请求量与错误量。
              <Btn size="sm" variant="primary" style={{ marginTop: 12 }} onClick={() => navigate('/channels')}>创建渠道</Btn>
            </Empty>
          ) : (
            <Chart labels={reqChart.labels} series={reqChart.series} h={300} yFmt={reqChart.yFmt} tipFmt={reqChart.tipFmt} />
          )}
        </Card>

        {/* 区块 6：渠道健康 × 失败归因(近实时,与经营区在视觉上分开) */}
        <div className="two-col">
          <Card
            icon="server"
            title="渠道健康度"
            note="成功率/首字 · 近 15 分钟；用量 · 今日"
            extra={<span className="card-link" onClick={() => navigate('/channels')}>全部渠道 →</span>}
          >
            {chLoading ? (
              <SkLines rows={['w80', 'w60', 'w80', 'w40']} />
            ) : chError ? (
              <Empty icon="warn" title="渠道数据加载失败">
                无法读取渠道健康度，路由与转发不受影响。
                <Btn size="sm" style={{ marginTop: 12 }} onClick={() => void refetchCh()}>重试</Btn>
              </Empty>
            ) : channels.length === 0 ? (
              <Empty icon="box" title="还没有渠道">
                一条渠道 = 一个上游端点与凭据。至少建一个才能转发请求。
                <Btn size="sm" variant="primary" style={{ marginTop: 12 }} onClick={() => navigate('/channels')}>新建渠道</Btn>
              </Empty>
            ) : (
              <div className="table-wrap">
                <table className="tb">
                  <thead>
                    <tr>
                      <th>渠道</th><th>供应商</th><th>状态</th>
                      <th className="right">成功率</th><th className="right">首字</th>
                      <th className="right">今日 Token</th><th className="right">今日花费</th>
                    </tr>
                  </thead>
                  <tbody>
                    {chRows.map(ch => {
                      const d = chDot(ch.status);
                      return (
                        <tr key={ch.id} className="clickable" tabIndex={0}
                          onClick={() => navigate('/channels')}
                          onKeyDown={e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); navigate('/channels'); }}}>
                          <td className="strong mono" title={ch.name}>{ch.name}</td>
                          <td className="small">{channelLabel(ch)}</td>
                          <td>
                            <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
                              <StatusDot tone={d.dot} />{d.text}
                            </span>
                            {ch.circuitOpen && <span style={{ marginLeft: 6 }}><Badge tone="red">熔断中</Badge></span>}
                          </td>
                          <td className="right mono">{ch.successRate ? fmt.pct(ch.successRate) : '—'}</td>
                          <td className="right mono">
                            {ch.status === 'down' || ch.status === 'unknown' || ch.status === 'disabled'
                              ? '—'
                              : fmt.ms(ch.latencyMs)}
                          </td>
                          <td className="right mono">{ch.todayTokens ? fmt.k(ch.todayTokens) : '—'}</td>
                          <td className="right mono">{ch.todayCostUsd ? fmt.usd(ch.todayCostUsd) : '—'}</td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            )}
          </Card>

          <Card icon="warn" title="失败归因" note="近 24 小时">
            {fails.isLoading ? (
              <SkLines rows={['w60', 'w80', 'w60']} />
            ) : fails.isError ? (
              <Empty icon="warn" title="归因数据加载失败">
                日志聚合查询失败，失败次数暂时未知。
                <div style={{ marginTop: 12 }}><Btn size="sm" onClick={fails.refetch}>重试</Btn></div>
              </Empty>
            ) : channels.length === 0 ? (
              <Empty icon="box" title="还没有请求">有失败时这里会按归因拆开：上游错误 / 首字节超时 / 中途静默，并单列不计入故障的客户端中断。</Empty>
            ) : fails.faultTotal === 0 && fails.canceled === 0 ? (
              <Empty icon="check" title="近 24 小时无失败">没有可归因的失败记录，渠道健康度未被扣分。</Empty>
            ) : (
              <div>
                {fails.buckets.map(b => (
                  <div className="li" key={b.kind}>
                    <div className="r1">
                      <span className="k">{FAIL_LABEL[b.kind]}</span>
                      <span className="n">
                        {b.kind === 'canceled' ? '不计入' : fails.faultTotal ? fmt.pct(b.count / fails.faultTotal, 0) : '0%'}
                      </span>
                      <span className="v mono">{b.count}</span>
                    </div>
                    <div className="bar" role="img" aria-label={`${FAIL_LABEL[b.kind]} ${b.count} 次`}>
                      <i style={{ width: `${(b.count / maxFail) * 100}%`, background: FAIL_COLOR[b.kind] }} />
                    </div>
                    <div className="r2">{FAIL_DESC[b.kind]}</div>
                  </div>
                ))}
              </div>
            )}
          </Card>
        </div>

        {/* 区块 7：花费 Top × 额度逼近 */}
        <div className="two-col">
          <Card
            icon="zap"
            title="花费 Top 5 模型"
            note={`${rl} · 成本口径 · 合计 ${fmt.usd(topCostTotal)}`}
            extra={<span className="card-link" onClick={() => navigate('/logs')}>用量明细 →</span>}
          >
            {topCost.length === 0 ? (
              <Empty icon="box" title="所选范围内还没有花费">产生请求后这里按模型聚合成花费排行。</Empty>
            ) : (
              <div>
                {topCost.map((r, i) => (
                  <div className="li" key={r.name}>
                    <div className="r1">
                      <span className="k mono">{r.name}</span>
                      <span className="n">{fmt.n(r.requests)} 次</span>
                      <span className="v mono">{fmt.usd(r.costUsd)}</span>
                    </div>
                    <div className="bar" role="img" aria-label={`${r.name} 花费占比 ${fmt.pct(r.costUsd / (topCostTotal || 1), 0)}`}>
                      <i style={{ width: `${(r.costUsd / (topCostTotal || 1)) * 100}%`, background: costHues[i % costHues.length] }} />
                    </div>
                  </div>
                ))}
              </div>
            )}
          </Card>

          <Card
            icon="gauge"
            title="额度逼近"
            note="令牌额度 + 上游渠道额度 · 用量 ≥ 60%"
            extra={<span className="card-link" onClick={() => navigate('/tokens')}>全部令牌 →</span>}
          >
            {quotaPending ? (
              <SkLines rows={['w80', 'w60']} />
            ) : nearQuota.length === 0 && chQuotaRows.length === 0 ? (
              tokens.length === 0 ? (
                <Empty icon="box" title="还没有令牌">
                  令牌用满额度后请求会被拒（quota_exceeded）。
                  <Btn size="sm" variant="primary" style={{ marginTop: 12 }} onClick={() => navigate('/tokens')}>新建令牌</Btn>
                </Empty>
              ) : (
                <Empty icon="check" title="没有额度逼近">令牌与上游渠道用量均低于 60%，无耗尽风险。</Empty>
              )
            ) : (
              <div>
                {/* 上游渠道额度:耗尽 = 该渠道所有模型对外不可用,故排在令牌之前。 */}
                {chQuotaRows.map(({ ch, q, ratio }) => {
                  const warn = ratio >= QUOTA_ALERT;
                  const reset = QUOTA_WINS
                    .map(k => q.windows?.[k]?.resetAt)
                    .find(Boolean);
                  return (
                    <div className="li" key={`ch-${ch.id}`}>
                      <div className="r1">
                        <span className="k">
                          {ch.name}
                          <span style={{ marginLeft: 6 }}><Badge tone="slate">上游渠道额度</Badge></span>
                        </span>
                        <span className="n">{pctText(ratio * 100)}%</span>
                        <span className="v mono">
                          {hasCap(q, 'monthly') ? (
                            <>
                              {fmt.usd(q.windows!.monthly!.used ?? 0)}{' '}
                              <span style={{ color: 'var(--t3)', fontWeight: 400 }}>/ {fmt.usd(q.windows!.monthly!.cap!)}</span>
                            </>
                          ) : (
                            <span style={{ color: 'var(--t3)', fontWeight: 400 }}>按窗口百分比</span>
                          )}
                        </span>
                      </div>
                      <div className="bar" role="img" aria-label={`${ch.name} 上游额度已用 ${pctText(ratio * 100)}%`}>
                        <i style={{ width: `${ratio * 100}%`, background: warn ? 'var(--warn)' : 'var(--brand)' }} />
                      </div>
                      <div className="r2">
                        {ratio >= 1
                          ? '上游额度已用尽，该渠道请求会开始失败（进而触发熔断）'
                          : `剩余约 ${pctText((1 - ratio) * 100)}%${reset ? `，${new Date(reset).toLocaleString()} 重置` : '，消耗完该渠道将失败'}`}
                      </div>
                    </div>
                  );
                })}
                {nearQuota.map(t => {
                  const r = t.usedUsd / t.quotaUsd;
                  const warn = r >= QUOTA_ALERT;
                  return (
                    <div className="li" key={`tk-${t.id}`}>
                      <div className="r1">
                        <span className="k">
                          {t.name}
                          <span style={{ marginLeft: 6 }}><Badge tone="slate">令牌额度</Badge></span>
                        </span>
                        <span className="n">{pctText(r * 100)}%</span>
                        <span className="v mono">
                          {fmt.usd(t.usedUsd)}{' '}
                          <span style={{ color: 'var(--t3)', fontWeight: 400 }}>/ {fmt.usd(t.quotaUsd)}</span>
                        </span>
                      </div>
                      <div className="bar" role="img" aria-label={`${t.name} 额度已用 ${pctText(r * 100)}%`}>
                        <i style={{ width: `${r * 100}%`, background: warn ? 'var(--warn)' : 'var(--brand)' }} />
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
          </Card>
        </div>

        {/* 区块 6：最近请求 */}
        <Card
          icon="scroll"
          title="最近请求"
          extra={<span className="card-link" onClick={() => navigate('/logs')}>全部日志 →</span>}
        >
          <div className="table-wrap">
            <table className="tb">
              <thead>
                <tr>
                  <th>时间</th><th>模型</th><th>渠道</th><th>令牌</th><th>状态</th>
                  <th className="right">首字</th><th className="right">总耗时</th><th className="right">花费</th>
                </tr>
              </thead>
              {logLoading && recent.length === 0 ? (
                <tbody><tr><td colSpan={8}><SkLines rows={['w80', 'w60', 'w80']} /></td></tr></tbody>
              ) : recent.length === 0 ? (
                <tbody>
                  <tr>
                    <td colSpan={8}>
                      {logError ? (
                        <Empty icon="warn" title="日志加载失败">
                          /logs 请求失败，不影响网关转发。
                          <Btn size="sm" style={{ marginTop: 12 }} onClick={() => void refetchLogs()}>重试</Btn>
                        </Empty>
                      ) : (
                        <Empty icon="box" title="暂无请求">建好渠道与令牌后打一次请求就能看到</Empty>
                      )}
                    </td>
                  </tr>
                </tbody>
              ) : (
                <tbody>
                  {recent.map(r => (
                    <tr key={r.id} className="clickable" tabIndex={0}
                      onClick={() => setDetail(r)}
                      onKeyDown={e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); setDetail(r); }}}>
                      <td className="mono">{String(r.ts).slice(11, 19)}</td>
                      <td className="strong mono ellip" title={r.model}>{r.model}</td>
                      <td className="mono small">{r.channelName}</td>
                      <td className="small">{r.tokenName}</td>
                      <td>
                        {r.statusCode === STATUS_CLIENT_CLOSED ? (
                          <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
                            <StatusDot tone="s" />中断
                          </span>
                        ) : r.statusCode >= 400 || r.error ? (
                          (() => {
                            const kind: FailKind = classifyError(r.error);
                            return (
                              <span style={{ display: 'inline-flex', alignItems: 'center', gap: 7 }}>
                                <StatusDot tone="r" />失败
                                <Badge tone="red">{FAIL_LABEL[kind]}</Badge>
                              </span>
                            );
                          })()
                        ) : (
                          <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
                            <StatusDot tone="g" />成功
                          </span>
                        )}
                      </td>
                      <td className="right mono">{r.firstTokenMs ? fmt.ms(r.firstTokenMs) : '—'}</td>
                      <td className="right mono">{fmt.ms(r.totalMs)}</td>
                      <td className="right mono">{r.costUsd ? fmt.usd(r.costUsd) : '—'}</td>
                    </tr>
                  ))}
                </tbody>
              )}
            </table>
          </div>
        </Card>
      </div>

      <RequestLogDrawer detail={detail} onClose={() => setDetail(null)} />
    </div>
  );
}
