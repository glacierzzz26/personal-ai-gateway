import { useMemo, useState } from 'react';
import {
  App, Button, Col, Dropdown, Form, Input, InputNumber, Modal, Row, Select, Space, Switch, Table, Tooltip,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import type { MenuProps } from 'antd';
import { MoreOutlined } from '@ant-design/icons';
import { useMutation, useQueries, useQuery, useQueryClient } from '@tanstack/react-query';
import type { UseQueryResult } from '@tanstack/react-query';
import { Block as BlockCard, Blocks } from '@/components/Block';
import PageHeader from '@/components/PageHeader';
import ProviderMark from '@/components/ProviderMark';
import StatusDot from '@/components/StatusDot';
import { EmptyState, ErrorState, NoResultState } from '@/components/States';
import CostRatioModal from '@/components/models/CostRatioModal';
import { api } from '@/services/api';
import { channelTypes, egressProtos, providers, quotaShapes } from '@/constants';
import { channelLabel, channelMark, egressLabel } from '@/utils/channel';
import { fmt } from '@/utils/format';
import { QUOTA_ALERT, QUOTA_WINS, pctText, quotaRatio, quotaTone } from '@/utils/quota';
import { TOKENS } from '@/styles/tokens';
import type {
  Channel, ChannelDraft, ChannelQuota, ChannelType, EgressProto, FetchPricingResult,
  HealthStatus, Provider, QuotaShape, QuotaWindow, QuotaWindowKey,
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

const dash = <span style={{ color: 'var(--gw-text-3)' }}>—</span>;

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
 *   2. 余额型(deepseek/one-api)→ 余额金额 + %(若有窗口)+ tooltip 明细;
 *   3. 不可查(未配置/不支持)→ 灰色占位 + 可点提示;
 *   4. 查询失败(超时等)→ 灰色占位 + 失败原因(不与「未配置」混为一谈)。
 */
function QuotaCell({ q, ch }: { q: UseQueryResult<ChannelQuota, Error>; ch: Channel }) {
  // 不查的渠道直接给指路占位。用 quotaEnabled 判定而非 fetchStatus ——
  // 查询**成功结束后** fetchStatus 同样是 'idle'(见 query-core 的 success 分支),
  // 拿它当「被禁用」用会把每一条已拿到数据的渠道都误判成灰色占位。
  if (!quotaEnabled(ch)) {
    return <Tooltip title="第三方渠道需在「编辑」里配置额度查询路径">{dash}</Tooltip>;
  }
  if (q.isPending) return <span style={{ color: 'var(--gw-text-3)' }}>读取中…</span>;
  const quota = q.data;
  if (q.isError || !quota || !quota.available) {
    const tip = quota?.error
      ? quota.errorKind === 'not_configured'
        ? `${quota.error} —— 点「编辑」进入渠道,填第三方额度路径`
        : quota.error
      : '额度接口未响应';
    return <Tooltip title={tip}>{dash}</Tooltip>;
  }
  const wins = okWindows(quota);
  const balance = quota.balance;
  if (wins.length === 0 && !balance) {
    return <Tooltip title="该渠道未返回可用额度窗口(不支持或已耗尽未上报)">{dash}</Tooltip>;
  }

  const detail = (
    <div style={{ fontSize: 12.5, lineHeight: 1.9, minWidth: 200 }}>
      {quota.planName && <div style={{ opacity: 0.85 }}>套餐：{quota.planName}</div>}
      {balance && (
        <div style={{ display: 'flex', justifyContent: 'space-between', gap: 20 }}>
          <span>剩余额度</span>
          <span className="gw-num">{money(balance.amount, balance.currency)}</span>
        </div>
      )}
      {wins.map(w => {
        const raw = usedCapText(w.win, balance?.currency ?? 'USD');
        return (
          <div key={w.key} style={{ display: 'flex', justifyContent: 'space-between', gap: 20 }}>
            <span>{w.label} 窗口</span>
            <span className="gw-num">
              已用 {pctText(w.win.percent)}%{raw ? ` (${raw})` : ''}
              {w.win.resetAt ? ` · ${resetText(w.win.resetAt)} 重置` : ''}
            </span>
          </div>
        );
      })}
    </div>
  );

  return (
    <Tooltip title={detail}>
      <span style={{ whiteSpace: 'nowrap', display: 'inline-flex', flexDirection: 'column', gap: 4, alignItems: 'flex-end' }}>
        {balance && (
          <span className="gw-num" style={{ fontSize: 12.5 }}>{money(balance.amount, balance.currency)}</span>
        )}
        {wins.map(w => {
          const warn = w.win.percent >= QUOTA_ALERT * 100;
          const raw = usedCapText(w.win, balance?.currency ?? 'USD');
          return (
            <span key={w.key} style={{ display: 'inline-flex', flexDirection: 'column', alignItems: 'flex-end', gap: 1 }}>
              <span style={{ display: 'flex', alignItems: 'center', gap: 7 }}>
                <span style={{ width: 22, color: 'var(--gw-text-3)', fontSize: 12.5 }}>{w.label}</span>
                <span className="gw-bar" style={{ width: 62 }} role="img" aria-label={`${w.label} 额度已用 ${pctText(w.win.percent)}%`}>
                  <i style={{ width: `${Math.min(w.win.percent, 100)}%`, background: warn ? TOKENS.warn : TOKENS.c1 }} />
                </span>
                <span className="gw-num" style={{ fontSize: 12.5, color: warn ? 'var(--gw-warn)' : 'var(--gw-text-2)' }}>
                  {pctText(w.win.percent)}%
                </span>
              </span>
              {/* 告警行把「已用/上限」与重置时间摊到行内 —— 不用悬停就能判断还有多久见底 */}
              {warn && (raw || w.win.resetAt) && (
                <span style={{ fontSize: 11.5, color: 'var(--gw-text-3)' }}>
                  {raw}
                  {raw && w.win.resetAt ? ' · ' : ''}
                  {w.win.resetAt ? `${resetText(w.win.resetAt)} 重置` : ''}
                </span>
              )}
            </span>
          );
        })}
      </span>
    </Tooltip>
  );
}

export default function Channels() {
  const { message, modal } = App.useApp();
  const qc = useQueryClient();
  const [form] = Form.useForm<ChannelFormValues>();
  // 第三方渠道才展开「额度路径 + 形状」,故需跟随表单实时值渲染。
  const channelType = Form.useWatch('channelType', form);

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
      if (r.ok) message.success(`${name} 探测成功 · ${fmt.ms(r.latencyMs)}`);
      else message.error(`${name} 探测失败:${r.message ?? '未知错误'}`);
      // 后端已把探测结果写回(清熔断 + EWMA 延迟),刷新以让行内/仪表盘展示一致。
      qc.invalidateQueries({ queryKey: ['channels'] });
      qc.invalidateQueries({ queryKey: ['models'] });
    },
    onError: () => {
      setTestingId(null);
      message.error('探测请求异常');
    },
  });

  // —— 新建 / 编辑 ——
  function openCreate() {
    setEditing(null);
    form.resetFields();
    form.setFieldsValue(DEFAULTS);
    setOpen(true);
  }

  function openEdit(row: Channel) {
    setEditing(row);
    form.resetFields();
    form.setFieldsValue({
      name: row.name,
      provider: row.provider,
      channelType: row.channelType,
      egressProto: row.egressProto,
      baseUrl: row.baseUrl,
      priority: row.priority,
      weight: row.weight,
      timeoutMs: row.timeoutMs,
      enabled: row.enabled,
      maxFailures: row.maxFailures,
      cooldownSec: row.cooldownSec,
      tags: row.tags ?? [],
      quotaPath: row.quotaPath ?? '',
      quotaShape: (row.quotaShape as QuotaShape) || '',
      note: row.note,
    });
    setOpen(true);
  }

  function closeModal() {
    setOpen(false);
    setEditing(null);
    form.resetFields();
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
        message.success(`渠道「${base.name}」已更新`);
      } else {
        const draft: ChannelDraft = { ...base, apiKey: values.apiKey?.trim() ?? '' };
        await api.createChannel(draft);
        message.success(`渠道「${base.name}」已创建`);
      }
      await invalidate();
      closeModal();
    } catch (e) {
      message.error(errText(e));
    } finally {
      setSubmitting(false);
    }
  }

  // —— 删除 ——
  function handleDelete(row: Channel) {
    modal.confirm({
      title: `删除渠道「${row.name}」?`,
      content: '将级联删除该渠道下的全部供给源与挂载关系,且不可恢复。',
      okText: '删除',
      okButtonProps: { danger: true },
      cancelText: '取消',
      onOk: async () => {
        try {
          await api.deleteChannel(row.id);
          message.success(`渠道「${row.name}」已删除`);
          await invalidate();
        } catch (e) {
          message.error(errText(e));
        }
      },
    });
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
      message.error(`同步失败:${errText(e)}`);
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

  const columns: ColumnsType<Channel> = [
    {
      // 渠道名 / 供应商 / Base URL 三合一 —— 都是「这条渠道是什么」,同格堆叠。
      // 不设 width:弹性列吸收剩余宽度,表格在 tableLayout="fixed" 下不横向溢出。
      title: '渠道', dataIndex: 'name',
      render: (v, r) => (
        <div style={{ minWidth: 0 }}>
          <div style={{ fontWeight: 500, color: 'var(--gw-text)' }}>{v}</div>
          <div style={{ display: 'inline-flex', alignItems: 'center', gap: 6, fontSize: 12.5, color: 'var(--gw-text-3)' }}>
            <ProviderMark name={channelMark(r)} size={14} />
            {channelLabel(r)} · {egressLabel(r.egressProto)} · {r.modelCount} 个模型
          </div>
          <Tooltip title={r.baseUrl}>
            <div
              className="gw-mono"
              style={{ fontSize: 12, color: 'var(--gw-text-3)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}
            >
              {r.baseUrl}
            </div>
          </Tooltip>
        </div>
      ),
    },
    {
      // 优先级 / 权重 合并 —— 两者都是选路参数,同列上下排列
      title: '选路', key: 'route', align: 'right', width: 84,
      render: (_, r) => (
        <span className="gw-num" style={{ color: 'var(--gw-text-3)' }}>
          P{r.priority} · W{r.weight}
        </span>
      ),
    },
    {
      // 成功率 / 延迟 / 状态 合并 —— 都是「这条渠道现在健不健康」
      title: '健康度', key: 'health', align: 'right', width: 132,
      render: (_, r) => {
        // down/disabled/unknown 都没有可报的成功率与延迟:前者在冷却、后两者没跑过流量或没启用。
        const noData = r.status === 'down' || r.status === 'disabled' || r.status === 'unknown';
        return (
          <span style={{ display: 'inline-flex', flexDirection: 'column', alignItems: 'flex-end', gap: 3 }}>
            <StatusDot status={r.status} />
            <span className="gw-num" style={{ fontSize: 12.5 }}>
              {noData ? '—' : fmt.pct(r.successRate, 2)} · {noData ? '—' : fmt.ms(r.latencyMs)}
            </span>
            {r.circuitOpen && <span className="gw-badge" style={{ color: TOKENS.err, borderColor: TOKENS.err }}>熔断中</span>}
          </span>
        );
      },
    },
    {
      // 今日 Token / 花费 合并 —— 同一时段的量价,一行显示
      title: '今日用量', key: 'today', align: 'right', width: 104,
      render: (_, r) => (
        <span style={{ display: 'inline-flex', flexDirection: 'column', alignItems: 'flex-end', gap: 2 }}>
          <span className="gw-num">{fmt.k(r.todayTokens)}</span>
          <span className="gw-num" style={{ fontSize: 12.5, color: 'var(--gw-text-3)' }}>{fmt.usd(r.todayCostUsd)}</span>
        </span>
      ),
    },
    {
      // 额度:窗口型逐条(标签+条+%),余额型直接给金额。表头可排序 ——
      // 余量比率 = 各可用窗口已用率的最大值(见 quotaRatio),与高亮/筛选同一口径。
      title: '额度', key: 'quota', width: 200,
      sorter: (a, b) => (quotaRatio(quotaById.get(a.id)?.data) ?? -1) - (quotaRatio(quotaById.get(b.id)?.data) ?? -1),
      render: (_, r) => {
        const q = quotaById.get(r.id);
        return q ? <QuotaCell q={q} ch={r} /> : dash;
      },
    },
    {
      // 高频（测试/编辑）外露，低频（同步/抓价/删除）收进「更多」——12 列表格放得进笔记本宽
      title: '操作', align: 'right', width: 160,
      render: (_, r) => {
        const vi = vendorInfo(r.provider);
        const canFetch = !!vi && !vi.manualOnly;
        const manualOnly = !!vi?.manualOnly;
        const menu: MenuProps = {
          items: [
            { key: 'sync', label: '同步模型', disabled: syncingId === r.id },
            { key: 'cost', label: '成本系数' },
            ...(canFetch ? [{ key: 'pricing', label: '获取官方定价', disabled: pricingId === r.id }] : []),
            { type: 'divider' as const },
            { key: 'delete', label: '删除', danger: true },
          ],
          onClick: ({ key, domEvent }) => {
            domEvent.stopPropagation();
            if (key === 'sync') handleSyncModels(r);
            else if (key === 'cost') setRatioChannel(r);
            else if (key === 'pricing') handleFetchPricing(r);
            else if (key === 'delete') handleDelete(r);
          },
        };
        return (
          <Space size={4}>
            <Button
              size="small"
              loading={testingId === r.id}
              onClick={() => { setTestingId(r.id); test.mutate(r.id); }}
            >
              测试
            </Button>
            <Button size="small" onClick={() => openEdit(r)}>编辑</Button>
            <Tooltip
              title={canFetch ? undefined : (
                <span>
                  {manualOnly ? '该厂商官方页为动态渲染,无法稳定抓取。' : ''}
                  聚合渠道请到「官方定价」页按厂商抓取(本渠道的 provider 是上游协议,不是真实厂商)。
                </span>
              )}
            >
              <Dropdown menu={menu} trigger={['click']}>
                <Button type="text" size="small" icon={<MoreOutlined />} aria-label={`更多操作 ${r.name}`} />
              </Dropdown>
            </Tooltip>
          </Space>
        );
      },
    },
  ];

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
      action={<Button size="small" type="primary" onClick={openCreate}>新建渠道</Button>}
    />
  ) : (
    <NoResultState
      title="没有符合条件的渠道"
      desc="当前筛选（关键字 / 供应商 / 状态 / 额度）没有命中。"
      action={<Button size="small" onClick={() => { setKw(''); setProvider(''); setStatus(''); setQuotaFilter(''); }}>清除筛选</Button>}
    />
  );

  return (
    <div className="gw-page">
      <PageHeader
        title="渠道管理"
        desc="一条渠道 = 一个上游 API 端点与凭据，管的是「怎么连上去」"
        extra={<Button type="primary" onClick={openCreate}>新建渠道</Button>}
      />

      <Blocks>
        <BlockCard>
          <div className="gw-toolbar">
            <Input.Search allowClear placeholder="搜索名称或地址" style={{ width: 240 }} value={kw} onChange={e => setKw(e.target.value)} />
            <Select
              style={{ width: 150 }} value={provider} onChange={setProvider}
              options={[
                { value: '', label: '全部供应商' },
                ...providers.map(p => ({ value: p, label: p })),
                // 聚合渠道 provider 为空,单列一项才能筛出来(空串已被「全部」占用)
                { value: '__none__', label: '非厂商 / 聚合' },
              ]}
            />
            <Select
              style={{ width: 140 }} value={status} onChange={setStatus}
              options={[
                { value: '', label: '全部状态' },
                { value: 'healthy' satisfies HealthStatus, label: '健康' },
                { value: 'degraded' satisfies HealthStatus, label: '降级' },
                { value: 'down' satisfies HealthStatus, label: '不可用' },
                { value: 'unknown' satisfies HealthStatus, label: '待观察' },
                { value: 'disabled' satisfies HealthStatus, label: '已停用' },
              ]}
            />
            <Select
              style={{ width: 160 }} value={quotaFilter} onChange={setQuotaFilter}
              options={[
                { value: '', label: '全部额度' },
                { value: 'alert', label: '额度告警 ≥85%' },
                { value: 'warn', label: '额度逼近 ≥60%' },
              ]}
            />
            <span className="count">
              {filtering ? (
                <>筛选出 <b>{list.length}</b> / {channels.length} 条渠道</>
              ) : (
                <>共 <b>{channels.length}</b> 条渠道</>
              )}
            </span>
          </div>

          <Table<Channel>
            rowKey="id"
            size="middle"
            loading={isLoading && channels.length === 0}
            dataSource={list}
            columns={columns}
            tableLayout="fixed"
            pagination={channels.length > 10 ? { pageSize: 10, showSizeChanger: false, size: 'default' } : false}
            locale={{ emptyText: emptyNode }}
          />
        </BlockCard>
      </Blocks>

      {/* 新建 / 编辑共享弹窗 */}
      <Modal
        title={editing ? `编辑渠道「${editing.name}」` : '新建渠道'}
        open={open}
        onOk={() => form.submit()}
        confirmLoading={submitting}
        onCancel={closeModal}
        width={640}
        okText={editing ? '保存' : '创建'}
        cancelText="取消"
      >
        <Form
          form={form}
          layout="vertical"
          onFinish={handleSubmit}
          requiredMark={false}
          initialValues={{ provider: DEFAULTS.provider, enabled: DEFAULTS.enabled }}
        >
          <Form.Item name="name" label="名称" rules={[{ required: true, whitespace: true, message: '请输入渠道名称' }]}>
            <Input placeholder="例:DeepSeek 官方" />
          </Form.Item>

          <Row gutter={12}>
            <Col span={8}>
              <Form.Item
                name="channelType" label="渠道类型" rules={[{ required: true, message: '请选择渠道类型' }]}
                tooltip="决定上游额度怎么查。第三方渠道需手工配置额度路径与形状"
              >
                <Select options={channelTypes.map(t => ({ value: t.value, label: t.label }))} />
              </Form.Item>
            </Col>
            <Col span={8}>
              <Form.Item
                name="egressProto" label="出站协议" rules={[{ required: true, message: '请选择出站协议' }]}
                tooltip="决定请求怎么发上去,与「是哪家的模型」无关"
              >
                <Select options={egressProtos.map(p => ({ value: p.value, label: p.label }))} />
              </Form.Item>
            </Col>
            <Col span={8}>
              <Form.Item
                name="provider" label="供应商"
                tooltip="卖的是谁的模型。聚合渠道(如 command code)留空,列表按渠道类型显示"
              >
                <Select
                  allowClear
                  placeholder="— 非厂商 / 聚合"
                  options={providers.map(p => ({ value: p, label: p }))}
                />
              </Form.Item>
            </Col>
          </Row>

          <Form.Item name="baseUrl" label="Base URL" rules={[{ required: true, whitespace: true, message: '请输入上游地址' }]}>
            <Input className="gw-mono" placeholder="https://api.deepseek.com/v1" />
          </Form.Item>

          {channelType === 'thirdparty' && (
            <Row gutter={12}>
              <Col span={14}>
                <Form.Item
                  name="quotaPath" label="额度路径"
                  tooltip="相对路径,拼在 Base URL 之后。留空则该渠道不查额度"
                  rules={[{
                    pattern: /^\/\S*$/,
                    message: '需以 / 开头的相对路径,如 /v1/dashboard/billing/subscription',
                  }]}
                >
                  <Input className="gw-mono" placeholder="/v1/dashboard/billing/subscription" />
                </Form.Item>
              </Col>
              <Col span={10}>
                <Form.Item
                  name="quotaShape" label="额度形状"
                  tooltip="上游返回的 JSON 形状;填了路径就必须选形状"
                  dependencies={['quotaPath']}
                  rules={[({ getFieldValue }) => ({
                    validator: (_, v) =>
                      getFieldValue('quotaPath') && !v
                        ? Promise.reject(new Error('填写额度路径后需选择形状'))
                        : Promise.resolve(),
                  })]}
                >
                  <Select
                    allowClear placeholder="选择形状"
                    options={quotaShapes.map(s => ({ value: s.value, label: s.label, title: s.hint }))}
                  />
                </Form.Item>
              </Col>
            </Row>
          )}

          <Form.Item
            name="apiKey"
            label="API Key"
            extra={editing ? '留空则保持原密钥不变' : undefined}
            rules={[{ required: !editing, whitespace: true, message: '请输入 API Key' }]}
          >
            <Input.Password
              autoComplete="new-password"
              placeholder={editing ? '留空则保持原密钥不变' : 'sk-…'}
            />
          </Form.Item>

          <Row gutter={12}>
            <Col span={8}>
              <Form.Item name="priority" label="优先级" tooltip="越小越优先参与选路" rules={[{ required: true, message: '必填' }]}>
                <InputNumber min={1} style={{ width: '100%' }} />
              </Form.Item>
            </Col>
            <Col span={8}>
              <Form.Item name="weight" label="权重" tooltip="按权重比例分摊流量(1-100)" rules={[{ required: true, message: '必填' }]}>
                <InputNumber min={1} max={100} style={{ width: '100%' }} />
              </Form.Item>
            </Col>
            <Col span={8}>
              <Form.Item
                name="timeoutMs" label="超时" rules={[{ required: true, message: '必填' }]}
                tooltip="渠道级超时只作为选路权重参考，不约束单个请求；请求超时以「系统设置」为准"
              >
                <Select options={TIMEOUT_OPTIONS} />
              </Form.Item>
            </Col>
          </Row>

          <Row gutter={12}>
            <Col span={8}>
              <Form.Item name="maxFailures" label="熔断阈值(次)" tooltip="连续失败多少次后熔断；499 客户端中断不计入" rules={[{ required: true, message: '必填' }]}>
                <InputNumber min={1} style={{ width: '100%' }} />
              </Form.Item>
            </Col>
            <Col span={8}>
              <Form.Item name="cooldownSec" label="熔断冷却(s)" tooltip="熔断后的冷却秒数" rules={[{ required: true, message: '必填' }]}>
                <InputNumber min={1} style={{ width: '100%' }} />
              </Form.Item>
            </Col>
            <Col span={8}>
              <Form.Item name="enabled" label="启用" valuePropName="checked" tooltip="停用后该渠道不再参与选路">
                <Switch />
              </Form.Item>
            </Col>
          </Row>

          <Form.Item name="tags" label="标签">
            <Select mode="tags" allowClear placeholder="回车添加,如:主力 / 备用 / 需配额" />
          </Form.Item>

          <Form.Item name="note" label="备注">
            <Input.TextArea rows={2} placeholder="可选" />
          </Form.Item>
        </Form>
      </Modal>

      {/* 同步模型结果 */}
      <Modal
        title={syncRes ? `同步模型 · ${syncRes.name}` : ''}
        open={!!syncRes}
        footer={<Button type="primary" onClick={() => setSyncRes(null)}>关闭</Button>}
        onCancel={() => setSyncRes(null)}
        width={560}
      >
        {syncRes && (
          <>
            <div className="gw-note" role="status" style={{ marginBottom: 14 }}>
              <b>本渠道现关联 {syncRes.modelCount} 个模型</b>
              <span>本次新增 {syncRes.added}、已存在 {syncRes.updated}。新同步的模型默认停用，需到「模型广场」定价后启用。</span>
            </div>
            {syncRes.models.length > 0 ? (
              <pre className="gw-pre">
                {syncRes.models.join('\n')}
              </pre>
            ) : (
              <div style={{ fontSize: 13.5, color: 'var(--gw-text-3)' }}>
                该渠道没有返回新模型（目录中均已存在）。
              </div>
            )}
          </>
        )}
      </Modal>

      {/* 获取官方定价结果(成功=落库条数;失败=显式报错,原报价不变) */}
      <Modal
        title={pricingRes ? `获取官方定价 · ${pricingRes.name}` : ''}
        open={!!pricingRes}
        footer={<Button type="primary" onClick={() => setPricingRes(null)}>关闭</Button>}
        onCancel={() => setPricingRes(null)}
        width={560}
      >
        {pricingRes?.error ? (
          <div className="gw-note" role="alert" style={{ borderLeftColor: 'var(--gw-err)' }}>
            <b style={{ color: 'var(--gw-err)' }}>获取失败</b>
            <span>
              {pricingRes.error}
              <br />
              本次未写入任何价格,原有报价保持不变。请核对官方页面后重试。
            </span>
          </div>
        ) : pricingRes?.result ? (
          <>
            <div className="gw-note" role="status" style={{ marginBottom: 14 }}>
              <b>已从官方计费页抓取 {pricingRes.result.upserted} 个模型的单价</b>
              <span>
                来源：
                <a href={pricingRes.result.sourceUrl} target="_blank" rel="noreferrer">{pricingRes.result.sourceUrl}</a>
                <br />
                已存入「官方参考价」,未改动任何现有报价;需到「模型广场」逐个核对并「应用」。
                {pricingRes.result.contentSha256 && (
                  <>
                    <br />
                    <span className="gw-mono" style={{ fontSize: 12 }}>
                      页面指纹 {pricingRes.result.contentSha256.slice(0, 16)}…
                    </span>
                  </>
                )}
              </span>
            </div>
            {pricingRes.result.models.length > 0 && (
              <pre className="gw-pre">
                {pricingRes.result.models.join('\n')}
              </pre>
            )}
          </>
        ) : null}
      </Modal>

      {/* 成本系数:成本 = 厂商官方价 × 系数,与售价无关 */}
      <CostRatioModal open={!!ratioChannel} channel={ratioChannel} onClose={() => setRatioChannel(null)} />
    </div>
  );
}
