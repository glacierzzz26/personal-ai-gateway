import { useEffect, useMemo, useState } from 'react';
import { useMutation, useQueries, useQuery, useQueryClient } from '@tanstack/react-query';
import type { UseQueryResult } from '@tanstack/react-query';
import { Block, Blocks } from '@/components/Block';
import ProviderMark from '@/components/ProviderMark';
import StatusDot from '@/components/StatusDot';
import { EmptyState, ErrorState, NoResultState } from '@/components/States';
import CostRatioModal from '@/components/models/CostRatioModal';
import { Btn, Field, Input, Note, PageHead, Pager, Select, SortInd, Switch } from '@/ui/primitives';
import { Modal, Dropdown, confirm } from '@/ui/overlay';
import { toast } from '@/ui/toast';
import { Icon } from '@/ui/icons';
import { api } from '@/services/api';
import { channelTypes, egressProtos, providers, quotaShapes } from '@/constants';
import { channelLabel, channelMark, egressLabel } from '@/utils/channel';
import { fmt } from '@/utils/format';
import { QUOTA_ALERT, QUOTA_WINS, pctText, quotaRatio, quotaTone } from '@/utils/quota';
import type {
  Channel, ChannelDraft, ChannelQuota, ChannelType, EgressProto, FetchPricingResult,
  Provider, QuotaShape, QuotaWindow, QuotaWindowKey,
} from '@/types';

/** 官方定价抓取结果弹窗载荷(失败即失败:error 非空时 models 为空)。 */
interface PricingRes {
  name: string;
  provider: Provider;
  result?: FetchPricingResult;
  error?: string;
}

/** 新建渠道表单默认值 */
const DEFAULTS = {
  provider: '' as Provider,
  channelType: 'thirdparty' as ChannelType,
  egressProto: 'openai' as EgressProto,
  priority: 10,
  weight: 1,
  timeoutMs: 60000,
  enabled: true,
  maxFailures: 5,
  cooldownSec: 30,
  tags: [] as string[],
  quotaPath: '',
  quotaShape: '' as QuotaShape | '',
};

const TIMEOUT_OPTIONS = [
  { value: 5000, label: '5s' },
  { value: 10000, label: '10s' },
  { value: 30000, label: '30s' },
  { value: 60000, label: '60s' },
  { value: 120000, label: '2min' },
  { value: 300000, label: '5min' },
];

/** 渠道创建/编辑表单入参(apiKey 编辑态留空 = 不改) */
interface ChannelFormValues {
  name: string;
  provider: Provider;
  channelType: ChannelType;
  egressProto: EgressProto;
  baseUrl: string;
  apiKey?: string;
  priority: number;
  weight: number;
  timeoutMs: number;
  enabled: boolean;
  maxFailures: number;
  cooldownSec: number;
  tags: string[];
  quotaPath?: string;
  quotaShape?: QuotaShape | '';
  note?: string;
}

function errText(e: unknown): string {
  return e instanceof Error ? e.message : '操作失败,请稍后重试';
}

/** 额度窗口展示顺序与标签(rolling≈近5h)。键序与 utils/quota.ts 的 QUOTA_WINS 同源。 */
const QUOTA_WIN_LABELS: Record<QuotaWindowKey, string> = { rolling: '5h', weekly: '周', monthly: '月' };

const dash = <span style={{ color: 'var(--t3)' }}>—</span>;

/** 币种符号:余额按上游原币种展示,不折算(汇率是手工维护的,不该拿它当余额前提)。 */
const CUR_SYMBOL: Record<string, string> = { CNY: '¥', USD: '$' };
const money = (amount: number, currency: string) =>
  `${CUR_SYMBOL[currency] ?? `${currency} `}${amount.toFixed(2)}`;

/** 重置时间显示:只到分钟(额度窗口不需要秒级)。 */
const resetText = (iso: string): string => {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  const p = (n: number) => String(n).padStart(2, '0');
  return `${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
};

/**
 * 该渠道要不要查额度。第三方中转没有统一约定,没配路径就不发请求
 * (后端会回「未配置」,查了也只是白报错)。查询的 enabled 与单元格的
 * 占位判定共用此函数 —— 两处若各写各的,很容易对不上而永远显示读取中。
 */
function quotaEnabled(ch: Channel): boolean {
  return ch.channelType !== 'thirdparty' || !!ch.quotaPath;
}

/** 可用窗口(按展示顺序过滤出 status==='ok' 的)。 */
function okWindows(q: ChannelQuota | undefined): Array<{ key: QuotaWindowKey; label: string; win: QuotaWindow }> {
  return QUOTA_WINS.flatMap(key => {
    const win = q?.windows?.[key];
    return win && win.status === 'ok' ? [{ key, label: QUOTA_WIN_LABELS[key], win }] : [];
  });
}

/** 工具提示里「已用 12/14」的原始用量串(上游给得出才有)。 */
function usedCapText(win: QuotaWindow, currency: string): string {
  if (win.cap == null || win.cap <= 0) return '';
  return `${money(win.used ?? 0, currency)} / ${money(win.cap, currency)}`;
}

/**
 * 额度单元格。行内**不依赖悬停**即可看到:百分比、已用/上限、重置时间 ——
 * 窗口上限与重置时间原本只藏在 tooltip 里,渠道一多就得逐个悬停才扫得出来。
 *
 * 四态:
 *   1. 窗口型(commandcode/opencode/通用信封)→ 每窗口「标签 + 条 + %」,告警行摊开「已用/上限」与重置;
 *   2. 余额型(deepseek/one-api)→ 余额金额 + %(若有窗口)+ 明细(summary 收进 title);
 *   3. 不可查(未配置/不支持)→ 灰色占位 + 可点提示;
 *   4. 查询失败(超时等)→ 灰色占位 + 失败原因(不与「未配置」混为一谈)。
 */
function QuotaCell({ q, ch }: { q: UseQueryResult<ChannelQuota, Error>; ch: Channel }) {
  // 不查的渠道直接给指路占位。用 quotaEnabled 判定而非 fetchStatus ——
  // 查询**成功结束后** fetchStatus 同样是 'idle'(见 query-core 的 success 分支),
  // 拿它当「被禁用」用会把每一条已拿到数据的渠道都误判成灰色占位。
  if (!quotaEnabled(ch)) {
    return <span className="cell-sub" title="第三方渠道需在「编辑」里配置额度查询路径">—</span>;
  }
  if (q.isPending) return <span style={{ color: 'var(--t3)' }}>读取中…</span>;
  const quota = q.data;
  if (q.isError || !quota || !quota.available) {
    const tip = quota?.error
      ? quota.errorKind === 'not_configured'
        ? `${quota.error} —— 点「编辑」进入渠道,填第三方额度路径`
        : quota.error
      : '额度接口未响应';
    return <span className="cell-sub" title={tip}>—</span>;
  }
  const wins = okWindows(quota);
  const balance = quota.balance;
  if (wins.length === 0 && !balance) {
    return <span className="cell-sub" title="该渠道未返回可用额度窗口(不支持或已耗尽未上报)">—</span>;
  }

  // 明细收进原生 title（自绘系统无富文本 hover 面板）。行内已展示余额与逐窗口条+%。
  const lines: string[] = [];
  if (quota.planName) lines.push(`套餐:${quota.planName}`);
  if (balance) lines.push(`剩余额度:${money(balance.amount, balance.currency)}`);
  wins.forEach(w => {
    const raw = usedCapText(w.win, balance?.currency ?? 'USD');
    lines.push(
      `${w.label} 窗口:已用 ${pctText(w.win.percent)}%${raw ? ` (${raw})` : ''}${w.win.resetAt ? ` · ${resetText(w.win.resetAt)} 重置` : ''}`,
    );
  });

  return (
    <span
      title={lines.join('\n')}
      style={{ whiteSpace: 'nowrap', display: 'inline-flex', flexDirection: 'column', gap: 4, alignItems: 'flex-end' }}
    >
      {balance && (
        <span className="num" style={{ fontSize: 12.5 }}>{money(balance.amount, balance.currency)}</span>
      )}
      {wins.map(w => {
        const warn = w.win.percent >= QUOTA_ALERT * 100;
        const raw = usedCapText(w.win, balance?.currency ?? 'USD');
        return (
          <span key={w.key} style={{ display: 'inline-flex', flexDirection: 'column', alignItems: 'flex-end', gap: 1 }}>
            <span style={{ display: 'flex', alignItems: 'center', gap: 7 }}>
              <span style={{ width: 22, color: 'var(--t3)', fontSize: 12.5 }}>{w.label}</span>
              <span
                className="meter-track"
                style={{ width: 62, height: 6 }}
                role="img"
                aria-label={`${w.label} 额度已用 ${pctText(w.win.percent)}%`}
              >
                <i style={{ width: `${Math.min(w.win.percent, 100)}%`, background: warn ? 'var(--warn)' : 'var(--brand)' }} />
              </span>
              <span className="num" style={{ fontSize: 12.5, color: warn ? 'var(--warn)' : 'var(--t2)' }}>
                {pctText(w.win.percent)}%
              </span>
            </span>
            {/* 告警行把「已用/上限」与重置时间摊到行内 —— 不用悬停就能判断还有多久见底 */}
            {warn && (raw || w.win.resetAt) && (
              <span style={{ fontSize: 11.5, color: 'var(--t3)' }}>
                {raw}
                {raw && w.win.resetAt ? ' · ' : ''}
                {w.win.resetAt ? `${resetText(w.win.resetAt)} 重置` : ''}
              </span>
            )}
          </span>
        );
      })}
    </span>
  );
}

export default function Channels() {
  const qc = useQueryClient();
  const [kw, setKw] = useState('');
  const [provider, setProvider] = useState('');
  const [status, setStatus] = useState('');
  const [quotaFilter, setQuotaFilter] = useState('');
  const [testingId, setTestingId] = useState<number | null>(null);
  const [syncingId, setSyncingId] = useState<number | null>(null);
  const [pricingId, setPricingId] = useState<number | null>(null);
  const [pricingRes, setPricingRes] = useState<PricingRes | null>(null);

  // 成本系数编辑器(成本 = 厂商官方价 × 系数)。
  const [ratioChannel, setRatioChannel] = useState<Channel | null>(null);

  // 新建 / 编辑共享弹窗
  const [editing, setEditing] = useState<Channel | null>(null);
  const [open, setOpen] = useState(false);
  const [submitting, setSubmitting] = useState(false);

  // 同步模型结果展示
  const [syncRes, setSyncRes] = useState<{ name: string; added: number; updated: number; models: string[]; modelCount: number } | null>(null);

  const { data: channels = [], isLoading, isError, refetch } = useQuery({
    queryKey: ['channels'],
    queryFn: api.getChannels,
    retry: 0,
  });

  // 官方价来源能力(可抓 / 仅手工)由后端下发,避免前端再抄一份厂商清单。
  const { data: officialVendors = [] } = useQuery({
    queryKey: ['official-vendors'],
    queryFn: () => api.officialVendors(),
    retry: 0,
    staleTime: 300_000,
  });
  const vendorInfo = (p: Provider) => officialVendors.find(v => v.provider === p);

  // 额度:按渠道类型分发到对应上游接口。后端带短 TTL 缓存(见 quota_cache.go),
  // 故这里放宽 staleTime、关掉窗口聚焦重取 —— 免得切回标签页就重打一轮上游。
  const quotaQueries = useQueries({
    queries: channels.map(ch => ({
      queryKey: ['channel-quota', ch.id],
      queryFn: () => api.channelQuota(ch.id),
      enabled: quotaEnabled(ch),
      retry: 0,
      staleTime: 60_000,
      refetchOnWindowFocus: false,
    })),
  });
  const quotaById = useMemo(() => {
    const m = new Map<number, UseQueryResult<ChannelQuota, Error>>();
    channels.forEach((ch, i) => m.set(ch.id, quotaQueries[i]));
    return m;
  }, [channels, quotaQueries]);

  const invalidate = () => qc.invalidateQueries({ queryKey: ['channels'] });

  const test = useMutation({
    mutationFn: (id: number) => api.testChannel(id),
    onSuccess: (r, id) => {
      setTestingId(null);
      const name = channels.find(c => c.id === id)?.name ?? '';
      if (r.ok) toast.ok(`${name} 探测成功 · ${fmt.ms(r.latencyMs)}`);
      else toast.err(`${name} 探测失败:${r.message ?? '未知错误'}`);
      // 后端已把探测结果写回(清熔断 + EWMA 延迟),刷新以让行内/仪表盘展示一致。
      qc.invalidateQueries({ queryKey: ['channels'] });
      qc.invalidateQueries({ queryKey: ['models'] });
    },
    onError: () => {
      setTestingId(null);
      toast.err('探测请求异常');
    },
  });

  // —— 新建 / 编辑 ——
  function openCreate() {
    setEditing(null);
    setOpen(true);
  }

  function openEdit(row: Channel) {
    setEditing(row);
    setOpen(true);
  }

  function closeModal() {
    setOpen(false);
    setEditing(null);
  }

  async function handleSubmit(values: ChannelFormValues) {
    // updateChannel 是整体替换:必须从当前表单快照构造完整 draft。
    // apiKey 仅在显式填新值时下发(undefined / 留空则后端保持原密钥)。
    const base = {
      name: values.name.trim(),
      provider: values.provider,
      channelType: values.channelType,
      egressProto: values.egressProto,
      baseUrl: values.baseUrl.trim(),
      priority: values.priority ?? DEFAULTS.priority,
      weight: values.weight ?? DEFAULTS.weight,
      timeoutMs: values.timeoutMs ?? DEFAULTS.timeoutMs,
      enabled: values.enabled ?? DEFAULTS.enabled,
      maxFailures: values.maxFailures ?? DEFAULTS.maxFailures,
      cooldownSec: values.cooldownSec ?? DEFAULTS.cooldownSec,
      tags: values.tags ?? [],
      // 额度配置只有第三方渠道有意义;切回内置类型时下发空值,免得残留路径日后误导。
      quotaPath: values.channelType === 'thirdparty' ? values.quotaPath?.trim() ?? '' : '',
      quotaShape: values.channelType === 'thirdparty' ? (values.quotaShape ?? '') : '',
      note: values.note?.trim() || undefined,
    };
    setSubmitting(true);
    try {
      if (editing) {
        const draft: ChannelDraft = { ...base, apiKey: values.apiKey?.trim() || undefined };
        await api.updateChannel(editing.id, draft);
        toast.ok(`渠道「${base.name}」已更新`);
      } else {
        const draft: ChannelDraft = { ...base, apiKey: values.apiKey?.trim() ?? '' };
        await api.createChannel(draft);
        toast.ok(`渠道「${base.name}」已创建`);
      }
      await invalidate();
      closeModal();
    } catch (e) {
      toast.err(errText(e));
    } finally {
      setSubmitting(false);
    }
  }

  // —— 删除 ——
  async function handleDelete(row: Channel) {
    const ok = await confirm({
      title: `删除渠道「${row.name}」?`,
      body: '将级联删除该渠道下的全部供给源与挂载关系,且不可恢复。',
      okText: '删除',
      danger: true,
      cancelText: '取消',
    });
    if (!ok) return;
    try {
      await api.deleteChannel(row.id);
      toast.ok(`渠道「${row.name}」已删除`);
      await invalidate();
    } catch (e) {
      toast.err(errText(e));
    }
  }

  // —— 同步模型(拉渠道 /v1/models → 补目录 + 挂停用 offer)——
  async function handleSyncModels(row: Channel) {
    setSyncingId(row.id);
    try {
      const r = await api.syncModels(row.id);
      setSyncRes({ name: row.name, added: r.added, updated: r.updated, models: r.models, modelCount: r.modelCount });
      // 弹窗总数取自后端同口径 modelCount;先把该行乐观对齐,再统一失效刷新,保证与列表同屏一致。
      qc.setQueryData<Channel[]>(['channels'], old =>
        (old ?? []).map(c => (c.id === row.id ? { ...c, modelCount: r.modelCount } : c)),
      );
      qc.invalidateQueries({ queryKey: ['channels'] });
      qc.invalidateQueries({ queryKey: ['models'] });
    } catch (e) {
      toast.err(`同步失败:${errText(e)}`);
    } finally {
      setSyncingId(null);
    }
  }

  // —— 获取官方定价(按 provider 抓厂商官网单价表 → 落「官方参考价」,不改 offer 价)——
  // 失败即失败:抓不到/解析不出/页面改版一律弹窗显式报错,原报价保持不变。
  async function handleFetchPricing(row: Channel) {
    setPricingId(row.id);
    try {
      const r = await api.fetchPricing(row.id);
      setPricingRes({ name: row.name, provider: row.provider, result: r });
      // 官方参考价变化 → 模型抽屉/广场的比对视图需重取。
      qc.invalidateQueries({ queryKey: ['official-prices'] });
    } catch (e) {
      setPricingRes({ name: row.name, provider: row.provider, error: errText(e) });
    } finally {
      setPricingId(null);
    }
  }

  const list = channels.filter(c => {
    if (kw && !`${c.name}${c.baseUrl}`.toLowerCase().includes(kw.toLowerCase())) return false;
    if (provider === '__none__') { if (c.provider) return false; }
    else if (provider && c.provider !== provider) return false;
    if (status && c.status !== status) return false;
    // 额度筛选:只看告警(≥85%)/ 只看逼近(≥60%)。查不到额度的渠道(未配置/失败)
    // 一律不算告警 —— 不能把「查不了」误报成「快没额度了」。
    if (quotaFilter) {
      const tone = quotaTone(quotaById.get(c.id)?.data);
      if (quotaFilter === 'alert' && tone !== 'alert') return false;
      if (quotaFilter === 'warn' && tone !== 'alert' && tone !== 'warn') return false;
    }
    return true;
  });

  // 额度排序(点表头切换 降→升→无)
  const [quotaDir, setQuotaDir] = useState<null | 'asc' | 'desc'>(null);
  const cycleQuota = () => setQuotaDir(d => (d === null ? 'desc' : d === 'desc' ? 'asc' : null));
  const sorted = useMemo(() => {
    if (!quotaDir) return list;
    return [...list].sort((a, b) => {
      const ra = quotaRatio(quotaById.get(a.id)?.data) ?? -1;
      const rb = quotaRatio(quotaById.get(b.id)?.data) ?? -1;
      return quotaDir === 'asc' ? ra - rb : rb - ra;
    });
  }, [list, quotaDir, quotaById]);

  // 分页(仅当渠道很多时)
  const [page, setPage] = useState(1);
  useEffect(() => { setPage(1); }, [kw, provider, status, quotaFilter, quotaDir]);
  const paged = channels.length > 10 ? sorted.slice((page - 1) * 10, page * 10) : sorted;

  const filtering = !!(kw || provider || status || quotaFilter);

  const emptyNode = isError ? (
    <ErrorState
      title="渠道列表加载失败"
      desc="无法读取渠道。若网关管理面仍在运行，已配置的转发不受影响。"
      onRetry={() => void refetch()}
    />
  ) : channels.length === 0 ? (
    <EmptyState
      title="还没有渠道"
      desc="一条渠道 = 一个上游 API 端点与凭据。至少建一个，网关才能把请求转发出去。"
      action={<Btn size="sm" variant="primary" onClick={openCreate}>新建渠道</Btn>}
    />
  ) : (
    <NoResultState
      title="没有符合条件的渠道"
      desc="当前筛选（关键字 / 供应商 / 状态 / 额度）没有命中。"
      action={<Btn size="sm" onClick={() => { setKw(''); setProvider(''); setStatus(''); setQuotaFilter(''); }}>清除筛选</Btn>}
    />
  );

  return (
    <div>
      <PageHead
        title="渠道管理"
        sub="一条渠道 = 一个上游 API 端点与凭据，管的是「怎么连上去」"
        actions={<Btn variant="primary" onClick={openCreate}>新建渠道</Btn>}
      />

      <Blocks>
        <Block>
          <div className="toolbar" style={{ padding: '12px 14px 0' }}>
            <Input value={kw} placeholder="搜索名称或地址" style={{ width: 240 }} onChange={e => setKw(e.target.value)} />
            <Select style={{ width: 150 }} value={provider} onChange={e => setProvider(e.target.value)}>
              <option value="">全部供应商</option>
              {providers.map(p => <option key={p} value={p}>{p}</option>)}
              {/* 聚合渠道 provider 为空,单列一项才能筛出来(空串已被「全部」占用) */}
              <option value="__none__">非厂商 / 聚合</option>
            </Select>
            <Select style={{ width: 140 }} value={status} onChange={e => setStatus(e.target.value)}>
              <option value="">全部状态</option>
              <option value="healthy">健康</option>
              <option value="degraded">降级</option>
              <option value="down">不可用</option>
              <option value="unknown">待观察</option>
              <option value="disabled">已停用</option>
            </Select>
            <Select style={{ width: 160 }} value={quotaFilter} onChange={e => setQuotaFilter(e.target.value)}>
              <option value="">全部额度</option>
              <option value="alert">额度告警 ≥85%</option>
              <option value="warn">额度逼近 ≥60%</option>
            </Select>
            <span className="count">
              {filtering ? (
                <>筛选出 <b>{list.length}</b> / {channels.length} 条渠道</>
              ) : (
                <>共 <b>{channels.length}</b> 条渠道</>
              )}
            </span>
          </div>

          <div className="table-wrap" style={{ padding: '8px 6px 0' }}>
            <table className="tb" style={{ tableLayout: 'fixed' }}>
              <thead>
                <tr>
                  {/* 渠道名 / 供应商 / Base URL 三合一 —— 同格堆叠,弹性列吸收剩余宽度 */}
                  <th>渠道</th>
                  {/* 优先级 / 权重 合并 */}
                  <th className="right" style={{ width: 84 }}>选路</th>
                  {/* 成功率 / 延迟 / 状态 合并 */}
                  <th className="right" style={{ width: 132 }}>健康度</th>
                  {/* 今日 Token / 花费 合并 */}
                  <th className="right" style={{ width: 104 }}>今日用量</th>
                  {/* 额度:窗口型逐条 + 余额型金额;表头可排序 */}
                  <th
                    className="right sortable"
                    style={{ width: 200 }}
                    onClick={cycleQuota}
                  >
                    额度 <SortInd dir={quotaDir} />
                  </th>
                  {/* 高频(测试/编辑)外露,低频(同步/抓价/删除)收进「更多」 */}
                  <th className="right" style={{ width: 160 }}>操作</th>
                </tr>
              </thead>
              <tbody>
                {isLoading && channels.length === 0 ? (
                  <tr><td colSpan={6} style={{ textAlign: 'center', color: 'var(--t3)', padding: 24 }}>读取中…</td></tr>
                ) : paged.length === 0 ? (
                  <tr><td colSpan={6}>{emptyNode}</td></tr>
                ) : (
                  paged.map(r => {
                    const noData = r.status === 'down' || r.status === 'disabled' || r.status === 'unknown';
                    const vi = vendorInfo(r.provider);
                    const canFetch = !!vi && !vi.manualOnly;
                    const manualOnly = !!vi?.manualOnly;
                    return (
                      <tr key={r.id}>
                        <td>
                          <div style={{ minWidth: 0 }}>
                            <div style={{ fontWeight: 500, color: 'var(--t1)' }}>{r.name}</div>
                            <div style={{ display: 'inline-flex', alignItems: 'center', gap: 6, fontSize: 12.5, color: 'var(--t3)' }}>
                              <ProviderMark name={channelMark(r)} size={14} />
                              {channelLabel(r)} · {egressLabel(r.egressProto)} · {r.modelCount} 个模型
                            </div>
                            <div
                              className="mono"
                              title={r.baseUrl}
                              style={{ fontSize: 12, color: 'var(--t3)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}
                            >
                              {r.baseUrl}
                            </div>
                          </div>
                        </td>
                        <td className="right">
                          <span className="num" style={{ color: 'var(--t3)' }}>P{r.priority} · W{r.weight}</span>
                        </td>
                        <td className="right">
                          <span style={{ display: 'inline-flex', flexDirection: 'column', alignItems: 'flex-end', gap: 3 }}>
                            <StatusDot status={r.status} />
                            <span className="num" style={{ fontSize: 12.5 }}>
                              {noData ? '—' : `${fmt.pct(r.successRate, 2)} · ${fmt.ms(r.latencyMs)}`}
                            </span>
                            {r.circuitOpen && <span className="badge b-red">熔断中</span>}
                          </span>
                        </td>
                        <td className="right">
                          <span style={{ display: 'inline-flex', flexDirection: 'column', alignItems: 'flex-end', gap: 2 }}>
                            <span className="num">{fmt.k(r.todayTokens)}</span>
                            <span className="num" style={{ fontSize: 12.5, color: 'var(--t3)' }}>{fmt.usd(r.todayCostUsd)}</span>
                          </span>
                        </td>
                        <td className="right">
                          {(() => {
                            const q = quotaById.get(r.id);
                            return q ? <QuotaCell q={q} ch={r} /> : dash;
                          })()}
                        </td>
                        <td className="right">
                          <span className="taglist" style={{ justifyContent: 'flex-end' }}>
                            <Btn
                              size="sm"
                              loading={testingId === r.id}
                              onClick={() => { setTestingId(r.id); test.mutate(r.id); }}
                            >
                              测试
                            </Btn>
                            <Btn size="sm" onClick={() => openEdit(r)}>编辑</Btn>
                            <Dropdown
                              menu={[
                                { key: 'sync', label: '同步模型', onClick: () => handleSyncModels(r) },
                                { key: 'cost', label: '成本系数', onClick: () => setRatioChannel(r) },
                                ...(canFetch ? [{ key: 'pricing', label: '获取官方定价', onClick: () => handleFetchPricing(r) }] : []),
                                { key: 'delete', label: '删除', danger: true, onClick: () => handleDelete(r) },
                              ]}
                            >
                              {() => {
                                // 同步/抓价进行中：图标换成旋转语义的 refresh 并禁点，防重复触发
                                const busy = syncingId === r.id || pricingId === r.id;
                                return (
                                  <button
                                    type="button"
                                    className="icon-btn"
                                    title={busy ? '处理中…' : (canFetch ? undefined : (manualOnly ? '该厂商官方页为动态渲染,无法稳定抓取。' : '聚合渠道请到「官方定价」页按厂商抓取(本渠道的 provider 是上游协议,不是真实厂商)。'))}
                                    aria-label={`更多操作 ${r.name}`}
                                    style={busy ? { opacity: .55, pointerEvents: 'none' } : undefined}
                                  >
                                    <Icon n={busy ? 'refresh' : 'more'} />
                                  </button>
                                );
                              }}
                            </Dropdown>
                          </span>
                        </td>
                      </tr>
                    );
                  })
                )}
              </tbody>
            </table>
          </div>

          {channels.length > 10 && (
            <div style={{ display: 'flex', justifyContent: 'flex-end', padding: '4px 14px 12px' }}>
              <Pager page={page} size={10} total={sorted.length} onPage={setPage} />
            </div>
          )}
        </Block>
      </Blocks>

      {/* 新建 / 编辑共享弹窗 */}
      <ChannelFormModal
        open={open}
        editing={editing}
        submitting={submitting}
        onClose={closeModal}
        onSubmit={handleSubmit}
      />

      {/* 同步模型结果 */}
      <Modal
        open={!!syncRes}
        onClose={() => setSyncRes(null)}
        title={syncRes ? `同步模型 · ${syncRes.name}` : ''}
        width={560}
        footer={<Btn variant="primary" onClick={() => setSyncRes(null)}>关闭</Btn>}
      >
        {syncRes && (
          <div className="stack-sm stack" style={{ marginBottom: 14 }}>
            <Note>
              <b>本渠道现关联 {syncRes.modelCount} 个模型</b>
              <div style={{ marginTop: 4 }}>本次新增 {syncRes.added}、已存在 {syncRes.updated}。新同步的模型默认停用，需到「模型广场」定价后启用。</div>
            </Note>
            {syncRes.models.length > 0 ? (
              <pre className="code">
                {syncRes.models.join('\n')}
              </pre>
            ) : (
              <div style={{ fontSize: 13.5, color: 'var(--t3)' }}>
                该渠道没有返回新模型（目录中均已存在）。
              </div>
            )}
          </div>
        )}
      </Modal>

      {/* 获取官方定价结果(成功=落库条数;失败=显式报错,原报价不变) */}
      <Modal
        open={!!pricingRes}
        onClose={() => setPricingRes(null)}
        title={pricingRes ? `获取官方定价 · ${pricingRes.name}` : ''}
        width={560}
        footer={<Btn variant="primary" onClick={() => setPricingRes(null)}>关闭</Btn>}
      >
        {pricingRes?.error ? (
          <div style={{ marginBottom: 14 }}>
            <Note tone="warn">
              <b style={{ color: 'var(--err)' }}>获取失败</b>
              <div style={{ marginTop: 4 }}>
                {pricingRes.error}
                <br />
                本次未写入任何价格,原有报价保持不变。请核对官方页面后重试。
              </div>
            </Note>
          </div>
        ) : pricingRes?.result ? (
          <div style={{ marginBottom: 14 }}>
            <Note>
              <b>已从官方计费页抓取 {pricingRes.result.upserted} 个模型的单价</b>
              <div style={{ marginTop: 4 }}>
                来源：
                <a href={pricingRes.result.sourceUrl} target="_blank" rel="noreferrer">{pricingRes.result.sourceUrl}</a>
                <br />
                已存入「官方参考价」,未改动任何现有报价;需到「模型广场」逐个核对并「应用」。
                {pricingRes.result.contentSha256 && (
                  <>
                    <br />
                    <span className="mono" style={{ fontSize: 12 }}>
                      页面指纹 {pricingRes.result.contentSha256.slice(0, 16)}…
                    </span>
                  </>
                )}
              </div>
            </Note>
            {pricingRes.result.models.length > 0 && (
              <pre className="code" style={{ marginTop: 12 }}>
                {pricingRes.result.models.join('\n')}
              </pre>
            )}
          </div>
        ) : null}
      </Modal>

      {/* 成本系数:成本 = 厂商官方价 × 系数,与售价无关 */}
      <CostRatioModal open={!!ratioChannel} channel={ratioChannel} onClose={() => setRatioChannel(null)} />
    </div>
  );
}

/* ============================================================
   新建 / 编辑渠道弹窗 —— 手写 state + Field,校验规则逐条照搬原 antd Form。
   ============================================================ */

/** 弹窗内表单态:数值字段允许 '' 表示未填(校验时判必填)。 */
interface FormState {
  name: string;
  provider: Provider | '';
  channelType: ChannelType;
  egressProto: EgressProto;
  baseUrl: string;
  apiKey: string;
  priority: number | '';
  weight: number | '';
  timeoutMs: number;
  enabled: boolean;
  maxFailures: number | '';
  cooldownSec: number | '';
  tags: string[];
  quotaPath: string;
  quotaShape: QuotaShape | '';
  note?: string;
}

const FORM_BLANK: FormState = {
  name: '',
  provider: '',
  channelType: 'thirdparty',
  egressProto: 'openai',
  baseUrl: '',
  apiKey: '',
  priority: DEFAULTS.priority,
  weight: DEFAULTS.weight,
  timeoutMs: DEFAULTS.timeoutMs,
  enabled: true,
  maxFailures: DEFAULTS.maxFailures,
  cooldownSec: DEFAULTS.cooldownSec,
  tags: [],
  quotaPath: '',
  quotaShape: '',
  note: '',
};

/** 轻量标签输入:回车添加,可逐个移除(替代 antd Select mode="tags")。 */
function TagInput({ value, onChange, placeholder }: { value: string[]; onChange: (v: string[]) => void; placeholder?: string }) {
  const [draft, setDraft] = useState('');
  const add = () => {
    const t = draft.trim();
    if (t && !value.includes(t)) onChange([...value, t]);
    setDraft('');
  };
  return (
    <div style={{ display: 'flex', flexWrap: 'wrap', gap: 5, alignItems: 'center' }}>
      {value.map(t => (
        <span key={t} className="tag" style={{ gap: 4 }}>
          {t}
          <button
            type="button"
            aria-label={`移除 ${t}`}
            onClick={() => onChange(value.filter(x => x !== t))}
            style={{ border: 'none', background: 'transparent', cursor: 'pointer', color: 'var(--t3)', padding: 0, fontSize: 11, lineHeight: 1 }}
          >
            ✕
          </button>
        </span>
      ))}
      <input
        className="input"
        style={{ flex: 1, minWidth: 120, height: 28 }}
        value={draft}
        placeholder={placeholder}
        onChange={e => setDraft(e.target.value)}
        onKeyDown={e => { if (e.key === 'Enter') { e.preventDefault(); add(); } }}
      />
    </div>
  );
}

function ChannelFormModal({ open, editing, submitting, onClose, onSubmit }: {
  open: boolean;
  editing: Channel | null;
  submitting: boolean;
  onClose: () => void;
  onSubmit: (v: ChannelFormValues) => void;
}) {
  const [v, setV] = useState<FormState>(FORM_BLANK);
  const [errs, setErrs] = useState<Partial<Record<keyof FormState, string>>>({});

  // 打开时用服务端快照 / 默认值重置本地编辑态 —— 关掉再开不该看到上次残留。
  useEffect(() => {
    if (!open) return;
    setErrs({});
    if (editing) {
      setV({
        name: editing.name,
        provider: editing.provider,
        channelType: editing.channelType,
        egressProto: editing.egressProto,
        baseUrl: editing.baseUrl,
        apiKey: '',
        priority: editing.priority,
        weight: editing.weight,
        timeoutMs: editing.timeoutMs,
        enabled: editing.enabled,
        maxFailures: editing.maxFailures,
        cooldownSec: editing.cooldownSec,
        tags: editing.tags ?? [],
        quotaPath: editing.quotaPath ?? '',
        quotaShape: (editing.quotaShape as QuotaShape) || '',
        note: editing.note,
      });
    } else {
      setV(FORM_BLANK);
    }
  }, [open, editing]);

  const set = <K extends keyof FormState>(k: K, val: FormState[K]) =>
    setV(p => ({ ...p, [k]: val }));

  // 校验规则逐条照搬原 antd Form.Item rules。
  function validate(): boolean {
    const e: Partial<Record<keyof FormState, string>> = {};
    if (!v.name.trim()) e.name = '请输入渠道名称';
    if (!v.channelType) e.channelType = '请选择渠道类型';
    if (!v.egressProto) e.egressProto = '请选择出站协议';
    if (!v.baseUrl.trim()) e.baseUrl = '请输入上游地址';
    if (v.channelType === 'thirdparty') {
      if (v.quotaPath && !/^\/\S*$/.test(v.quotaPath)) {
        e.quotaPath = '需以 / 开头的相对路径,如 /v1/dashboard/billing/subscription';
      }
      if (v.quotaPath && !v.quotaShape) e.quotaShape = '填写额度路径后需选择形状';
    }
    if (!v.apiKey.trim() && !editing) e.apiKey = '请输入 API Key';
    if (v.priority === '') e.priority = '必填';
    if (v.weight === '') e.weight = '必填';
    if (v.timeoutMs == null) e.timeoutMs = '必填';
    if (v.maxFailures === '') e.maxFailures = '必填';
    if (v.cooldownSec === '') e.cooldownSec = '必填';
    setErrs(e);
    return Object.keys(e).length === 0;
  }

  function buildValues(): ChannelFormValues {
    return {
      name: v.name,
      provider: (v.provider || '') as Provider,
      channelType: v.channelType,
      egressProto: v.egressProto,
      baseUrl: v.baseUrl,
      apiKey: v.apiKey,
      priority: v.priority === '' ? DEFAULTS.priority : v.priority,
      weight: v.weight === '' ? DEFAULTS.weight : v.weight,
      timeoutMs: v.timeoutMs,
      enabled: v.enabled,
      maxFailures: v.maxFailures === '' ? DEFAULTS.maxFailures : v.maxFailures,
      cooldownSec: v.cooldownSec === '' ? DEFAULTS.cooldownSec : v.cooldownSec,
      tags: v.tags,
      quotaPath: v.quotaPath,
      quotaShape: v.quotaShape,
      note: v.note,
    };
  }

  const submit = () => { if (validate()) onSubmit(buildValues()); };

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={editing ? `编辑渠道「${editing.name}」` : '新建渠道'}
      width={640}
      footer={
        <>
          <Btn onClick={onClose}>取消</Btn>
          <Btn variant="primary" loading={submitting} onClick={submit}>{editing ? '保存' : '创建'}</Btn>
        </>
      }
    >
      <Field label="名称" err={errs.name}>
        <Input placeholder="例:DeepSeek 官方" value={v.name} onChange={e => set('name', e.target.value)} err={!!errs.name} />
      </Field>

      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3,1fr)', gap: 12 }}>
        <Field label="渠道类型" hint="决定上游额度怎么查。第三方渠道需手工配置额度路径与形状" err={errs.channelType}>
          <Select value={v.channelType} onChange={e => set('channelType', e.target.value as ChannelType)} err={!!errs.channelType}>
            {channelTypes.map(t => <option key={t.value} value={t.value}>{t.label}</option>)}
          </Select>
        </Field>
        <Field label="出站协议" hint="决定请求怎么发上去,与「是哪家的模型」无关" err={errs.egressProto}>
          <Select value={v.egressProto} onChange={e => set('egressProto', e.target.value as EgressProto)} err={!!errs.egressProto}>
            {egressProtos.map(p => <option key={p.value} value={p.value}>{p.label}</option>)}
          </Select>
        </Field>
        <Field label="供应商" hint="卖的是谁的模型。聚合渠道(如 command code)留空,列表按渠道类型显示">
          <Select value={v.provider} onChange={e => set('provider', e.target.value as Provider | '')}>
            <option value="">— 非厂商 / 聚合</option>
            {providers.map(p => <option key={p} value={p}>{p}</option>)}
          </Select>
        </Field>
      </div>

      <Field label="Base URL" err={errs.baseUrl}>
        <Input className="mono" placeholder="https://api.deepseek.com/v1" value={v.baseUrl} onChange={e => set('baseUrl', e.target.value)} err={!!errs.baseUrl} />
      </Field>

      {v.channelType === 'thirdparty' && (
        <div style={{ display: 'grid', gridTemplateColumns: '1.4fr 1fr', gap: 12 }}>
          <Field
            label="额度路径"
            hint="相对路径,拼在 Base URL 之后。留空则该渠道不查额度"
            err={errs.quotaPath}
          >
            <Input className="mono" placeholder="/v1/dashboard/billing/subscription" value={v.quotaPath} onChange={e => set('quotaPath', e.target.value)} err={!!errs.quotaPath} />
          </Field>
          <Field
            label="额度形状"
            hint="上游返回的 JSON 形状;填了路径就必须选形状"
            err={errs.quotaShape}
          >
            <Select value={v.quotaShape} onChange={e => set('quotaShape', e.target.value as QuotaShape | '')} err={!!errs.quotaShape}>
              <option value="">选择形状</option>
              {quotaShapes.map(s => <option key={s.value} value={s.value} title={s.hint}>{s.label}</option>)}
            </Select>
          </Field>
        </div>
      )}

      <Field label="API Key" hint={editing ? '留空则保持原密钥不变' : undefined} err={errs.apiKey}>
        <Input
          type="password"
          autoComplete="new-password"
          placeholder={editing ? '留空则保持原密钥不变' : 'sk-…'}
          value={v.apiKey}
          onChange={e => set('apiKey', e.target.value)}
          err={!!errs.apiKey}
        />
      </Field>

      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3,1fr)', gap: 12 }}>
        <Field label="优先级" hint="越小越优先参与选路" err={errs.priority}>
          <Input type="number" min={1} value={v.priority} onChange={e => set('priority', e.target.value === '' ? '' : Number(e.target.value))} err={!!errs.priority} />
        </Field>
        <Field label="权重" hint="按权重比例分摊流量(1-100)" err={errs.weight}>
          <Input type="number" min={1} max={100} value={v.weight} onChange={e => set('weight', e.target.value === '' ? '' : Number(e.target.value))} err={!!errs.weight} />
        </Field>
        <Field
          label="超时"
          hint="渠道级超时只作为选路权重参考，不约束单个请求；请求超时以「系统设置」为准"
          err={errs.timeoutMs}
        >
          <Select value={v.timeoutMs} onChange={e => set('timeoutMs', Number(e.target.value))} err={!!errs.timeoutMs}>
            {TIMEOUT_OPTIONS.map(o => <option key={o.value} value={o.value}>{o.label}</option>)}
          </Select>
        </Field>
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3,1fr)', gap: 12 }}>
        <Field label="熔断阈值(次)" hint="连续失败多少次后熔断；499 客户端中断不计入" err={errs.maxFailures}>
          <Input type="number" min={1} value={v.maxFailures} onChange={e => set('maxFailures', e.target.value === '' ? '' : Number(e.target.value))} err={!!errs.maxFailures} />
        </Field>
        <Field label="熔断冷却(s)" hint="熔断后的冷却秒数" err={errs.cooldownSec}>
          <Input type="number" min={1} value={v.cooldownSec} onChange={e => set('cooldownSec', e.target.value === '' ? '' : Number(e.target.value))} err={!!errs.cooldownSec} />
        </Field>
        <Field label="启用" hint="停用后该渠道不再参与选路">
          <Switch checked={v.enabled} onChange={nv => set('enabled', nv)} />
        </Field>
      </div>

      <Field label="标签">
        <TagInput value={v.tags} onChange={tags => set('tags', tags)} placeholder="回车添加,如:主力 / 备用 / 需配额" />
      </Field>

      <Field label="备注">
        <textarea
          className="input"
          rows={2}
          style={{ minHeight: 56, font: 'inherit' }}
          placeholder="可选"
          value={v.note ?? ''}
          onChange={e => set('note', e.target.value)}
        />
      </Field>
    </Modal>
  );
}
