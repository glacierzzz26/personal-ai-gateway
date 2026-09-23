import { useMemo, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Button, Empty, Table, Tooltip } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import Chart from '@/components/Chart';
import Sparkline from '@/components/Sparkline';
import StatusDot from '@/components/StatusDot';
import { Block as BlockCard, BlockBody, BlockHead, Blocks, MetricBlock } from '@/components/Block';
import PageHeader from '@/components/PageHeader';
import RangePicker, { defaultRange, rangeLabel, toQuery } from '@/components/RangePicker';
import RequestLogDrawer from '@/components/RequestLogDrawer';
import { EmptyState, ErrorState, NoResultState, SkBlock, SkLines, SkMetric } from '@/components/States';
import { IconRefresh } from '@/components/icons';
import { useFailureAttribution } from '@/hooks/useFailureAttribution';
import { useChartColors } from '@/hooks/useChartColors';
import { api } from '@/services/api';
import { TOKENS } from '@/styles/tokens';
import { channelLabel } from '@/utils/channel';
import {
  FAIL_DESC, FAIL_LABEL, FAIL_TONE, STATUS_CLIENT_CLOSED, TONE_COLOR, classifyError, fmt,
} from '@/utils/format';
import { QUOTA_ALERT, QUOTA_WARN, QUOTA_WINS, hasCap, pctText, quotaRatio } from '@/utils/quota';
import type { EChartsOption } from 'echarts';
import type { FailKind } from '@/utils/format';
import type { Channel, ChannelQuota, ChannelQuotaItem, CustomerRow, RequestLogItem } from '@/types';

/** "2026-09-03 20" → "20:00" */
const hourLabel = (ts: string) => `${ts.slice(11)}:00`;

/** 图表横轴标签:小时桶 → "20:00";日桶 → "09-03"。 */
const axisLabel = (bucket: 'hour' | 'day', ts: string) => (bucket === 'hour' ? hourLabel(ts) : ts.slice(5));

/** 环比：(当前 − 上期) / 上期；上期为 0 时无意义，返回 null。 */
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
      type: 'category', data: points.map(h => axisLabel(bucket, h.ts)), boundaryGap: false,
      axisLine: { lineStyle: { color: c.line } }, axisTick: { show: false },
      /* 桶数随窗口变(1 天 24 个点、30 天 30 个点),固定 interval 会把标签挤成斜排;
         交给 ECharts 按可用宽度自适应,重叠的直接隐藏。 */
      axisLabel: { color: c.text, fontSize: 12, interval: 'auto', rotate: 0, hideOverlap: true },
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
        name: '请求数', type: 'line', data: points.map(h => h.requests),
        showSymbol: false, lineStyle: { width: 2, color: c.primary }, itemStyle: { color: c.primary },
        areaStyle: { color: TOKENS.primary50 },
      },
      {
        name: '错误数', type: 'line', yAxisIndex: 1, data: points.map(h => h.errors),
        showSymbol: false, lineStyle: { width: 1.6, color: c.error }, itemStyle: { color: c.error },
      },
    ],
  };

  /* 营收 vs 成本:只画客户归属流量(与经营条同源),故曲线求和 = 条上营收/成本。 */
  const moneyOption: EChartsOption = {
    grid: { left: 62, right: 24, top: 34, bottom: 26 },
    tooltip: {
      trigger: 'axis',
      backgroundColor: c.tooltipBg, borderColor: c.tooltipBorder,
      textStyle: { color: c.text, fontSize: 13 }, borderWidth: 1,
    },
    legend: {
      data: ['营收', '成本'], right: 0, top: 0,
      itemWidth: 9, itemHeight: 9, textStyle: { color: c.text, fontSize: 13.5 },
    },
    xAxis: {
      type: 'category', data: custPoints.map(h => axisLabel(bucket, h.ts)), boundaryGap: false,
      axisLine: { lineStyle: { color: c.line } }, axisTick: { show: false },
      axisLabel: { color: c.text, fontSize: 12, interval: 'auto', rotate: 0, hideOverlap: true },
    },
    yAxis: {
      type: 'value', splitLine: { lineStyle: { color: c.line } },
      axisLabel: { color: c.text, fontSize: 12 },
    },
    series: [
      {
        name: '营收', type: 'line', data: custPoints.map(h => moneyOf(h.chargeUsd)),
        showSymbol: false, lineStyle: { width: 2, color: c.primary }, itemStyle: { color: c.primary },
        areaStyle: { color: TOKENS.primary50 },
      },
      {
        name: '成本', type: 'line', data: custPoints.map(h => moneyOf(h.costUsd)),
        showSymbol: false, lineStyle: { width: 1.8, color: c.warn }, itemStyle: { color: c.warn },
      },
    ],
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
  const costHues = [TOKENS.c1, TOKENS.c2, TOKENS.c3, TOKENS.c4, TOKENS.c5];

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
    for (const c of channels) {
      if (!c.enabled) continue;
      const q = quotaById.get(c.id);
      if (!q) continue;
      const ratio = quotaRatio(q);
      if (ratio != null && ratio >= QUOTA_WARN) rows.push({ ch: c, q, ratio });
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

  const logCols: ColumnsType<RequestLogItem> = [
    { title: '时间', dataIndex: 'ts', width: 92, render: v => <span className="gw-mono" style={{ fontSize: 13 }}>{String(v).slice(11, 19)}</span> },
    {
      title: '模型', dataIndex: 'model', ellipsis: true,
      render: v => <Tooltip title={v}><span className="gw-mono" style={{ fontSize: 13.5 }}>{v}</span></Tooltip>,
    },
    { title: '渠道', dataIndex: 'channelName', width: 128, ellipsis: true },
    { title: '令牌', dataIndex: 'tokenName', width: 116, ellipsis: true, render: v => <span style={{ color: 'var(--gw-text-3)' }}>{v}</span> },
    {
      title: '状态', dataIndex: 'statusCode', width: 132,
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
    { title: '首字', dataIndex: 'firstTokenMs', align: 'right', width: 76, render: v => <span className="gw-num">{v ? fmt.ms(v) : '—'}</span> },
    { title: '总耗时', dataIndex: 'totalMs', align: 'right', width: 88, render: v => <span className="gw-num">{fmt.ms(v)}</span> },
    { title: '花费', dataIndex: 'costUsd', align: 'right', width: 92, render: v => <span className="gw-num">{v ? fmt.usd(v) : '—'}</span> },
  ];

  const maxFail = Math.max(...fails.buckets.map(b => b.count), 1);

  return (
    <div className="gw-page">
      <PageHeader
        title="运行总览"
        desc={<>今日营收与成本、客户余额风险、渠道健康与失败归因 · 每 15 秒自动刷新</>}
        extra={
          <div style={{ display: 'flex', gap: 10, alignItems: 'center' }}>
            <RangePicker value={range} onChange={setRange} />
            <Button icon={<IconRefresh />} onClick={refreshAll}>
              刷新
            </Button>
          </div>
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

        {/* 区块 2：经营条 —— 站主每天打开先看这门生意今天赚了多少。

            只算客户归属流量(见后端 MarginTotals):营收=charge_usd、成本=cost,
            毛利=两者之差。改造前的旧日志与站主自用不进这个口径。
            环比基准是「昨日整日」(后端回填 prev),不是前 1×24 小时。 */}
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
                label="营收"
                value={fmt.usd(revenue)}
                delta={revDelta?.text}
                deltaGood
                note={`${rl} · 客户实付${prev ? ' · 较昨日' : ''}`}
                spark={<Sparkline values={custPoints.map(h => moneyOf(h.chargeUsd))} />}
              />
              <MetricBlock
                label="成本"
                value={fmt.usd(bizCost)}
                note={`${rl} · 付上游（仅客户流量）`}
                spark={<Sparkline values={custPoints.map(h => moneyOf(h.costUsd))} color={TOKENS.warn} />}
              />
              <MetricBlock
                label="毛利"
                value={fmt.usd(margin)}
                delta={marginDelta?.text}
                deltaGood
                note={`${rl} · 毛利率 ${fmt.pct(marginRate)}${prev ? ' · 较昨日' : ''}`}
              />
              <MetricBlock
                label="客户请求数"
                value={fmt.n(totals?.requests ?? 0)}
                delta={bizReqDelta?.text}
                deltaGood
                note={`${rl} · 全站 ${fmt.n(totalReq)} 次${prev ? ' · 较昨日' : ''}`}
              />
            </>
          )}
        </div>

        {/* 区块 2b：稳定性条 —— 全站口径的网关健康(与经营的客户口径分开)。
            首屏先答「今天赚了多少」,再答「转得顺不顺」。 */}
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3, minmax(0,1fr))', gap: 18 }}>
          <MetricBlock
            label="全站请求数"
            value={fmt.n(totalReq)}
            note={`${rl} · 含站主自用`}
          />
          <MetricBlock
            label="平均首字延迟"
            value={String(Math.round(avgFt))}
            unit="ms"
            note={`${rl} · 成功流式请求均值`}
          />
          <MetricBlock
            label="失败率"
            value={(failRate * 100).toFixed(1)}
            unit="%"
            note={`${rl} ${fmt.n(overview?.totalErrors ?? 0)} 次错误${fails.canceled ? `；另 ${fails.canceled} 次中断不计入` : ''}`}
            spark={<Sparkline values={failPct} color={TOKENS.err} />}
          />
        </div>

        {/* 区块 3：营收 vs 成本曲线(经营口径,与上方条同源) */}
        <BlockCard>
          <BlockHead
            title="营收与成本"
            sub={`${rl} · ${bucket === 'hour' ? '按小时' : '按日'} · 仅客户流量`}
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
            ) : (totals?.requests ?? 0) === 0 && custPoints.every(h => h.chargeUsd === 0) ? (
              <EmptyState
                title="还没有客户流量"
                desc="创建客户账号与令牌后，这里按桶显示营收与成本。站主自用流量不计入经营口径。"
                action={<Button size="small" type="primary" onClick={() => navigate('/users')}>管理客户</Button>}
              />
            ) : (
              <Chart option={moneyOption} height={280} />
            )}
          </BlockBody>
        </BlockCard>

        {/* 区块 4：客户关注区 —— 谁欠费、谁在放量 */}
        <BlockCard>
          <BlockHead
            title="客户关注"
            sub={focus?.window ?? `${rl} · 余额与消耗`}
            right={<button type="button" className="gw-link" onClick={() => navigate('/users')}>全部客户 →</button>}
          />
          <BlockBody>
            {foLoading ? (
              <SkLines rows={['w80', 'w60', 'w80']} />
            ) : foError ? (
              <ErrorState title="客户数据加载失败" desc="/customers/focus 请求失败，余额与消耗暂时未知。" onRetry={() => void refetchFocus()} />
            ) : (
              <div style={{ display: 'grid', gridTemplateColumns: 'minmax(0,1fr) minmax(0,1fr)', gap: 18 }}>
                {/* 左:余额告警 —— 客户被拒 = 客户在流失,站主最该先处理 */}
                <div>
                  <div style={{ fontSize: 14, color: 'var(--gw-text-3)', marginBottom: 10 }}>
                    余额告警
                  </div>
                  {atRisk.length === 0 ? (
                    <NoResultState title="无欠费客户" desc="所有客户余额均能覆盖近 1 天消耗。" />
                  ) : (
                    <div className="gw-list">
                      {atRisk.map(u => {
                        const meta = RISK_META[u.risk];
                        return (
                          <div className="gw-li" key={u.id}>
                            <div className="r1">
                              <span className="k">{u.username}</span>
                              <span className="n">
                                <span className={`gw-dot ${meta.tone}`} style={{ marginRight: 6 }} />
                                {meta.label}
                              </span>
                              <span className="v gw-num">{fmt.usd(u.balanceUsd)}</span>
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
                  <div style={{ fontSize: 14, color: 'var(--gw-text-3)', marginBottom: 10 }}>
                    消耗 Top（{rl}）
                  </div>
                  {topCustomers.length === 0 ? (
                    <NoResultState title="窗口内无客户消耗" desc="所选范围内还没有客户产生的请求。" />
                  ) : (
                    <div className="gw-list">
                      {topCustomers.map((u, i) => (
                        <div className="gw-li" key={u.id}>
                          <div className="r1">
                            <span className="k">{u.username}</span>
                            <span className="n">{fmt.n(u.requests)} 次</span>
                            <span className="v gw-num">{fmt.usd(u.spendUsd)}</span>
                          </div>
                          <div className="gw-bar" role="img" aria-label={`${u.username} 营收占比 ${fmt.pct(u.spendUsd / (topCustTotal || 1), 0)}`}>
                            <i style={{ width: `${(u.spendUsd / (topCustTotal || 1)) * 100}%`, background: costHues[i % costHues.length] }} />
                          </div>
                        </div>
                      ))}
                    </div>
                  )}
                </div>
              </div>
            )}
          </BlockBody>
        </BlockCard>

        {/* 区块 5：请求与错误曲线(全站,窗口随筛选器) */}
        <BlockCard>
          <BlockHead
            title="请求与错误"
            sub={`${rl} · ${bucket === 'hour' ? '按小时' : '按日'} · 全站（含站主自用）`}
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
            ) : totalReq === 0 && points.every(h => h.requests === 0) ? (
              <EmptyState
                title="还没有请求"
                desc="创建渠道与访问令牌后，这里会显示逐桶请求量与错误量。"
                action={<Button size="small" type="primary" onClick={() => navigate('/channels')}>创建渠道</Button>}
              />
            ) : (
              <Chart option={option} height={300} />
            )}
          </BlockBody>
        </BlockCard>

        {/* 区块 6：渠道健康 × 失败归因(近实时,与经营区在视觉上分开) */}
        <div style={{ display: 'grid', gridTemplateColumns: 'minmax(0,2.1fr) minmax(0,1fr)', gap: 18 }}>
          <BlockCard>
            <BlockHead
              title="渠道健康度"
              sub="成功率/首字 · 近 15 分钟；用量 · 今日"
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
              <div>
                <table className="gw-table">
                  <thead>
                    <tr>
                      <th>渠道</th><th>供应商</th><th>状态</th>
                      <th className="num">成功率</th><th className="num">首字</th>
                      <th className="num">今日 Token</th><th className="num">今日花费</th>
                    </tr>
                  </thead>
                  <tbody>
                    {chRows.map(ch => (
                      <tr key={ch.id} className="clickable" tabIndex={0}
                        onClick={() => navigate('/channels')}
                        onKeyDown={e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); navigate('/channels'); } }}>
                        <td className="name" title={ch.name}>{ch.name}</td>
                        <td>{channelLabel(ch)}</td>
                        <td>
                          <StatusDot status={ch.status} />
                          {ch.circuitOpen && <span className="gw-badge" style={{ marginLeft: 6 }}>熔断中</span>}
                        </td>
                        <td className="num">{ch.successRate ? fmt.pct(ch.successRate) : '—'}</td>
                        <td className="num">
                          {ch.status === 'down' || ch.status === 'unknown' || ch.status === 'disabled'
                            ? '—'
                            : fmt.ms(ch.latencyMs)}
                        </td>
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

        {/* 区块 7：花费 Top × 额度逼近 */}
        <div style={{ display: 'grid', gridTemplateColumns: 'minmax(0,1fr) minmax(0,1fr)', gap: 18 }}>
          <BlockCard>
            <BlockHead
              title="花费 Top 5 模型"
              sub={`${rl} · 成本口径 · 合计 ${fmt.usd(topCostTotal)}`}
              right={<button type="button" className="gw-link" onClick={() => navigate('/logs')}>用量明细 →</button>}
            />
            {topCost.length === 0 ? (
              <BlockBody>
                <EmptyState title="所选范围内还没有花费" desc="产生请求后这里按模型聚合成花费排行。" />
              </BlockBody>
            ) : (
              <div className="gw-list">
                {topCost.map((r, i) => (
                  <div className="gw-li" key={r.name}>
                    <div className="r1">
                      <span className="k gw-mono" style={{ fontSize: 13.5 }}>{r.name}</span>
                      <span className="n">{fmt.n(r.requests)} 次</span>
                      <span className="v">{fmt.usd(r.costUsd)}</span>
                    </div>
                    <div className="gw-bar" role="img" aria-label={`${r.name} 花费占比 ${fmt.pct(r.costUsd / (topCostTotal || 1), 0)}`}>
                      <i style={{ width: `${(r.costUsd / (topCostTotal || 1)) * 100}%`, background: costHues[i % costHues.length] }} />
                    </div>
                  </div>
                ))}
              </div>
            )}
          </BlockCard>

          <BlockCard>
            <BlockHead
              title="额度逼近"
              sub="令牌额度 + 上游渠道额度 · 用量 ≥ 60%"
              right={<button type="button" className="gw-link" onClick={() => navigate('/tokens')}>全部令牌 →</button>}
            />
            {quotaPending ? (
              <BlockBody>
                <SkLines rows={['w80', 'w60']} />
              </BlockBody>
            ) : nearQuota.length === 0 && chQuotaRows.length === 0 ? (
              <BlockBody>
                {tokens.length === 0 ? (
                  <EmptyState
                    title="还没有令牌"
                    desc="令牌用满额度后请求会被拒（quota_exceeded）。"
                    action={<Button size="small" type="primary" onClick={() => navigate('/tokens')}>新建令牌</Button>}
                  />
                ) : (
                  <NoResultState title="没有额度逼近" desc="令牌与上游渠道用量均低于 60%，无耗尽风险。" />
                )}
              </BlockBody>
            ) : (
              <div className="gw-list">
                {/* 上游渠道额度:耗尽 = 该渠道所有模型对外不可用,故排在令牌之前。 */}
                {chQuotaRows.map(({ ch, q, ratio }) => {
                  const tone = ratio >= QUOTA_ALERT ? 'warn' : 'primary';
                  const reset = QUOTA_WINS
                    .map(k => q.windows?.[k]?.resetAt)
                    .find(Boolean);
                  return (
                    <div className="gw-li" key={`ch-${ch.id}`}>
                      <div className="r1">
                        <span className="k">
                          {ch.name}
                          <span className="gw-badge" style={{ marginLeft: 6 }}>上游渠道额度</span>
                        </span>
                        <span className="n">{pctText(ratio * 100)}%</span>
                        <span className="v">
                          {hasCap(q, 'monthly') ? (
                            <>
                              {fmt.usd(q.windows!.monthly!.used ?? 0)}{' '}
                              <span style={{ color: 'var(--gw-text-3)', fontWeight: 400 }}>/ {fmt.usd(q.windows!.monthly!.cap!)}</span>
                            </>
                          ) : (
                            <span style={{ color: 'var(--gw-text-3)', fontWeight: 400 }}>按窗口百分比</span>
                          )}
                        </span>
                      </div>
                      <div className="gw-bar" role="img" aria-label={`${ch.name} 上游额度已用 ${pctText(ratio * 100)}%`}>
                        <i style={{ width: `${ratio * 100}%`, background: tone === 'warn' ? TOKENS.warn : TOKENS.c1 }} />
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
                  const tone = r >= QUOTA_ALERT ? 'warn' : 'primary';
                  return (
                    <div className="gw-li" key={`tk-${t.id}`}>
                      <div className="r1">
                        <span className="k">
                          {t.name}
                          <span className="gw-badge" style={{ marginLeft: 6 }}>令牌额度</span>
                        </span>
                        <span className="n">{pctText(r * 100)}%</span>
                        <span className="v">
                          {fmt.usd(t.usedUsd)}{' '}
                          <span style={{ color: 'var(--gw-text-3)', fontWeight: 400 }}>/ {fmt.usd(t.quotaUsd)}</span>
                        </span>
                      </div>
                      <div className="gw-bar" role="img" aria-label={`${t.name} 额度已用 ${pctText(r * 100)}%`}>
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

