import { useEffect, useMemo, useState } from 'react';
import { useMutation, useQueries, useQuery, useQueryClient } from '@tanstack/react-query';
import type { UseQueryResult } from '@tanstack/react-query';
import { Block as BlockCard, BlockBody, Blocks } from '@/components/Block';
import PageHeader from '@/components/PageHeader';
import ProviderMark from '@/components/ProviderMark';
import StatusDot from '@/components/StatusDot';
import { EmptyState, ErrorState, NoResultState } from '@/components/States';
import CostRatioModal from '@/components/models/CostRatioModal';
import { Badge, Button, Field, HStack, Input, Note, Pager, Select, Switch, Table, TagList } from '@/ui/primitives';
import type { Column } from '@/ui/primitives';
import { Dropdown, Modal, confirm } from '@/ui/overlay';
import type { MenuItem } from '@/ui/overlay';
import { message } from '@/ui/toast';
import { api } from '@/services/api';
import { channelTypes, egressProtos, providers, quotaShapes } from '@/constants';
import { channelLabel, channelMark, egressLabel } from '@/utils/channel';
import { fmt } from '@/utils/format';
import { QUOTA_ALERT, QUOTA_WINS, pctText, quotaRatio, quotaTone } from '@/utils/quota';
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

const TIMEOUT_OPTIONS = [
  { value: 5000, label: '5s' },
  { value: 10000, label: '10s' },
  { value: 30000, label: '30s' },
  { value: 60000, label: '60s' },
  { value: 120000, label: '2min' },
  { value: 300000, label: '5min' },
];

function errText(e: unknown): string {
  return e instanceof Error ? e.message : '操作失败,请稍后重试';
}

/** 额度窗口展示顺序与标签(rolling≈近5h)。键序与 utils/quota.ts 的 QUOTA_WINS 同源。 */
const QUOTA_WIN_LABELS: Record<QuotaWindowKey, string> = { rolling: '5h', weekly: '周', monthly: '月' };

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

const dash = <span className="muted">—</span>;

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
    return <span className="muted" title="第三方渠道需在「编辑」里配置额度查询路径">—</span>;
  }
  if (q.isPending) return <span className="muted">读取中…</span>;
  const quota = q.data;
  if (q.isError || !quota || !quota.available) {
    const tip = quota?.error
      ? quota.errorKind === 'not_configured'
        ? `${quota.error} —— 点「编辑」进入渠道,填第三方额度路径`
        : quota.error
      : '额度接口未响应';
    return <span className="muted" title={tip}>—</span>;
  }
  const wins = okWindows(quota);
  const balance = quota.balance;
  if (wins.length === 0 && !balance) {
    return <span className="muted" title="该渠道未返回可用额度窗口(不支持或已耗尽未上报)">—</span>;
  }

  const lines: string[] = [];
  if (quota.planName) lines.push(`套餐：${quota.planName}`);
  if (balance) lines.push(`剩余额度  ${money(balance.amount, balance.currency)}`);
  for (const w of wins) {
    const raw = usedCapText(w.win, balance?.currency ?? 'USD');
    lines.push(
      `${w.label} 窗口  已用 ${pctText(w.win.percent)}%${raw ? ` (${raw})` : ''}${
        w.win.resetAt ? ` · ${resetText(w.win.resetAt)} 重置` : ''
      }`,
    );
  }

  return (
    <span title={lines.join('\n')} style={{ display: 'inline-flex', flexDirection: 'column', gap: 4, alignItems: 'flex-end' }}>
      {balance && <span className="num" style={{ fontSize: 12.5 }}>{money(balance.amount, balance.currency)}</span>}
      {wins.map(w => {
        const warn = w.win.percent >= QUOTA_ALERT * 100;
        const raw = usedCapText(w.win, balance?.currency ?? 'USD');
        return (
          <span key={w.key} style={{ display: 'inline-flex', flexDirection: 'column', alignItems: 'flex-end', gap: 1 }}>
            <span className="hstack" style={{ gap: 7 }}>
              <span style={{ width: 22, color: 'var(--t3)', fontSize: 12.5 }}>{w.label}</span>
              <span
                className="bar"
                style={{ width: 62, marginTop: 0 }}
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

/* ============================ 新建 / 编辑表单 ============================ */
/** 表单内部状态:数字字段留字符串,提交时再解析(空串才好校验)。 */
interface FormState {
  name: string;
  provider: string;
  channelType: ChannelType;
  egressProto: EgressProto;
  baseUrl: string;
  apiKey: string;
  priority: string;
  weight: string;
  timeoutMs: number;
  maxFailures: string;
  cooldownSec: string;
  enabled: boolean;
  tags: string[];
  quotaPath: string;
  quotaShape: QuotaShape | '';
  note: string;
}

/** 渠道创建/编辑表单入参(apiKey 编辑态留空 = 不改)。 */
export interface ChannelFormValues {
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

const EMPTY_FORM: FormState = {
  name: '', provider: '', channelType: 'thirdparty', egressProto: 'openai', baseUrl: '', apiKey: '',
  priority: '10', weight: '1', timeoutMs: 60000, maxFailures: '5', cooldownSec: '30',
  enabled: true, tags: [], quotaPath: '', quotaShape: '', note: '',
};

function ChannelFormModal({
  open, editing, onClose, onSubmit,
}: {
  open: boolean;
  editing: Channel | null;
  onClose: () => void;
  onSubmit: (v: ChannelFormValues, id?: number) => Promise<void>;
}) {
  const [f, setF] = useState<FormState>(EMPTY_FORM);
  const [errs, setErrs] = useState<Record<string, string>>({});
  const [saving, setSaving] = useState(false);
  const [tagInput, setTagInput] = useState('');

  useEffect(() => {
    if (!open) return;
    if (editing) {
      setF({
        name: editing.name,
        provider: editing.provider,
        channelType: editing.channelType,
        egressProto: editing.egressProto,
        baseUrl: editing.baseUrl,
        apiKey: '',
        priority: String(editing.priority),
        weight: String(editing.weight),
        timeoutMs: editing.timeoutMs,
        maxFailures: String(editing.maxFailures),
        cooldownSec: String(editing.cooldownSec),
        enabled: editing.enabled,
        tags: editing.tags ?? [],
        quotaPath: editing.quotaPath ?? '',
        quotaShape: (editing.quotaShape as QuotaShape) || '',
        note: editing.note ?? '',
      });
    } else {
      setF(EMPTY_FORM);
    }
    setErrs({});
    setTagInput('');
  }, [open, editing]);

  const set = <K extends keyof FormState>(k: K, v: FormState[K]) => {
    setF((s) => ({ ...s, [k]: v }));
    setErrs((e) => (e[k as string] ? { ...e, [k as string]: '' } : e));
  };

  const validate = (): Record<string, string> => {
    const e: Record<string, string> = {};
    if (!f.name.trim()) e.name = '请输入渠道名称';
    if (!f.baseUrl.trim()) e.baseUrl = '请输入上游地址';
    if (!editing && !f.apiKey.trim()) e.apiKey = '请输入 API Key';
    if (f.channelType === 'thirdparty') {
      if (f.quotaPath.trim() && !/^\/\S*$/.test(f.quotaPath.trim())) {
        e.quotaPath = '需以 / 开头的相对路径,如 /v1/dashboard/billing/subscription';
      }
      if (f.quotaPath.trim() && !f.quotaShape) e.quotaShape = '填写额度路径后需选择形状';
    }
    const num = (k: string, label: string, min: number, max?: number) => {
      const n = Number(f[k as keyof FormState]);
      if (!Number.isFinite(n) || n < min) e[k] = `${label}不能小于 ${min}`;
      else if (max != null && n > max) e[k] = `${label}不能大于 ${max}`;
    };
    num('priority', '优先级', 1);
    num('weight', '权重', 1, 100);
    num('maxFailures', '熔断阈值', 1);
    num('cooldownSec', '冷却秒数', 1);
    return e;
  };

  const submit = async () => {
    const e = validate();
    if (Object.keys(e).length) {
      setErrs(e);
      return;
    }
    const values: ChannelFormValues = {
      name: f.name.trim(),
      provider: f.provider as Provider,
      channelType: f.channelType,
      egressProto: f.egressProto,
      baseUrl: f.baseUrl.trim(),
      apiKey: f.apiKey.trim() || undefined,
      priority: Number(f.priority),
      weight: Number(f.weight),
      timeoutMs: f.timeoutMs,
      enabled: f.enabled,
      maxFailures: Number(f.maxFailures),
      cooldownSec: Number(f.cooldownSec),
      tags: f.tags,
      quotaPath: f.quotaPath.trim(),
      quotaShape: f.quotaShape,
      note: f.note.trim() || undefined,
    };
    setSaving(true);
    try {
      await onSubmit(values, editing?.id);
    } catch {
      // 错误信息已由父级提示,保持弹窗打开以便修改
    } finally {
      setSaving(false);
    }
  };

  if (!open) return null;

  return (
    <div className="overlay" style={{ zIndex: 70 }}>
      <Modal
        title={editing ? `编辑渠道「${editing.name}」` : '新建渠道'}
        onClose={onClose}
        width={640}
        footer={
          <>
            <Button onClick={onClose}>取消</Button>
            <Button variant="primary" loading={saving} onClick={() => void submit()}>
              {editing ? '保存' : '创建'}
            </Button>
          </>
        }
      >
        <div className="stack">
          <Field label="名称" error={errs.name}>
            <Input value={f.name} placeholder="例:DeepSeek 官方" onChange={(e) => set('name', e.target.value)} invalid={!!errs.name} />
          </Field>

          <div className="field-row">
            <Field label="渠道类型" hint="决定上游额度怎么查。第三方渠道需手工配置额度路径与形状">
              <Select
                value={f.channelType}
                onChange={(e) => set('channelType', e.target.value as ChannelType)}
                options={channelTypes.map((t) => ({ value: t.value, label: t.label }))}
              />
            </Field>
            <Field label="出站协议" hint="决定请求怎么发上去,与「是哪家的模型」无关">
              <Select
                value={f.egressProto}
                onChange={(e) => set('egressProto', e.target.value as EgressProto)}
                options={egressProtos.map((p) => ({ value: p.value, label: p.label }))}
              />
            </Field>
            <Field label="供应商" hint="卖的是谁的模型。聚合渠道留空,列表按渠道类型显示">
              <Select
                value={f.provider}
                onChange={(e) => set('provider', e.target.value)}
                options={[{ value: '', label: '— 非厂商 / 聚合' }, ...providers.map((p) => ({ value: p, label: p }))]}
              />
            </Field>
          </div>

          <Field label="Base URL" error={errs.baseUrl}>
            <Input
              className="mono"
              value={f.baseUrl}
              placeholder="https://api.deepseek.com/v1"
              onChange={(e) => set('baseUrl', e.target.value)}
              invalid={!!errs.baseUrl}
            />
          </Field>

          {f.channelType === 'thirdparty' && (
            <div className="field-row">
              <Field label="额度路径" hint="相对路径,拼在 Base URL 之后。留空则该渠道不查额度" error={errs.quotaPath}>
                <Input
                  className="mono"
                  value={f.quotaPath}
                  placeholder="/v1/dashboard/billing/subscription"
                  onChange={(e) => set('quotaPath', e.target.value)}
                  invalid={!!errs.quotaPath}
                />
              </Field>
              <Field label="额度形状" hint="上游返回的 JSON 形状;填了路径就必须选形状" error={errs.quotaShape}>
                <Select
                  value={f.quotaShape}
                  onChange={(e) => set('quotaShape', e.target.value as QuotaShape | '')}
                  options={[{ value: '', label: '选择形状' }, ...quotaShapes.map((s) => ({ value: s.value, label: s.label }))]}
                />
              </Field>
            </div>
          )}

          <Field label="API Key" hint={editing ? '留空则保持原密钥不变' : undefined} error={errs.apiKey}>
            <Input
              type="password"
              autoComplete="new-password"
              value={f.apiKey}
              placeholder={editing ? '留空则保持原密钥不变' : 'sk-…'}
              onChange={(e) => set('apiKey', e.target.value)}
              invalid={!!errs.apiKey}
            />
          </Field>

          <div className="field-row">
            <Field label="优先级" hint="越小越优先参与选路" error={errs.priority}>
              <Input type="number" min={1} value={f.priority} onChange={(e) => set('priority', e.target.value)} invalid={!!errs.priority} />
            </Field>
            <Field label="权重" hint="按权重比例分摊流量(1-100)" error={errs.weight}>
              <Input type="number" min={1} max={100} value={f.weight} onChange={(e) => set('weight', e.target.value)} invalid={!!errs.weight} />
            </Field>
            <Field label="超时" hint="渠道级超时只作选路参考,请求超时以「系统设置」为准">
              <Select
                value={f.timeoutMs}
                onChange={(e) => set('timeoutMs', Number(e.target.value))}
                options={TIMEOUT_OPTIONS}
              />
            </Field>
          </div>

          <div className="field-row">
            <Field label="熔断阈值(次)" hint="连续失败多少次后熔断；499 客户端中断不计入" error={errs.maxFailures}>
              <Input type="number" min={1} value={f.maxFailures} onChange={(e) => set('maxFailures', e.target.value)} invalid={!!errs.maxFailures} />
            </Field>
            <Field label="熔断冷却(s)" hint="熔断后的冷却秒数" error={errs.cooldownSec}>
              <Input type="number" min={1} value={f.cooldownSec} onChange={(e) => set('cooldownSec', e.target.value)} invalid={!!errs.cooldownSec} />
            </Field>
            <Field label="启用" hint="停用后该渠道不再参与选路">
              <Switch checked={f.enabled} onChange={(v) => set('enabled', v)} />
            </Field>
          </div>

          <Field label="标签">
            <Input
              value={tagInput}
              placeholder="输入后回车添加，如:主力 / 备用 / 需配额"
              onChange={(e) => setTagInput(e.target.value)}
              onKeyDown={(e) => {
                if (e.key !== 'Enter') return;
                e.preventDefault();
                const t = tagInput.trim();
                if (t && !f.tags.includes(t)) set('tags', [...f.tags, t]);
                setTagInput('');
              }}
            />
            {f.tags.length > 0 && (
              <TagList>
                {f.tags.map((t) => (
                  <span key={t} className="tag" style={{ cursor: 'pointer' }} title="点击移除" onClick={() => set('tags', f.tags.filter((x) => x !== t))}>
                    {t} ×
                  </span>
                ))}
              </TagList>
            )}
          </Field>

          <Field label="备注">
            <textarea className="input" rows={2} value={f.note} placeholder="可选" onChange={(e) => set('note', e.target.value)} />
          </Field>
        </div>
      </Modal>
    </div>
  );
}

/* ============================ 页面主体 ============================ */
export default function Channels() {
  const qc = useQueryClient();

  const [kw, setKw] = useState('');
  const [provider, setProvider] = useState('');
  const [status, setStatus] = useState('');
  const [quotaFilter, setQuotaFilter] = useState('');
  const [testingId, setTestingId] = useState<number | null>(null);
  const [pricingRes, setPricingRes] = useState<PricingRes | null>(null);
  const [page, setPage] = useState(1);
  const [sort, setSort] = useState<{ key: string; dir: 1 | -1 } | null>(null);

  // 成本系数编辑器(成本 = 厂商官方价 × 系数)。
  const [ratioChannel, setRatioChannel] = useState<Channel | null>(null);

  // 新建 / 编辑共享弹窗
  const [editing, setEditing] = useState<Channel | null>(null);
  const [open, setOpen] = useState(false);

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

  async function handleSubmit(values: ChannelFormValues, id?: number) {
    // updateChannel 是整体替换:必须从当前表单快照构造完整 draft。
    // apiKey 仅在显式填新值时下发(undefined / 留空则后端保持原密钥)。
    const base = {
      name: values.name.trim(),
      provider: values.provider,
      channelType: values.channelType,
      egressProto: values.egressProto,
      baseUrl: values.baseUrl.trim(),
      priority: values.priority,
      weight: values.weight,
      timeoutMs: values.timeoutMs,
      enabled: values.enabled,
      maxFailures: values.maxFailures,
      cooldownSec: values.cooldownSec,
      tags: values.tags ?? [],
      // 额度配置只有第三方渠道有意义;切回内置类型时下发空值,免得残留路径日后误导。
      quotaPath: values.channelType === 'thirdparty' ? values.quotaPath?.trim() ?? '' : '',
      quotaShape: values.channelType === 'thirdparty' ? (values.quotaShape ?? '') : '',
      note: values.note?.trim() || undefined,
    };
    try {
      if (id != null) {
        const draft: ChannelDraft = { ...base, apiKey: values.apiKey?.trim() || undefined };
        await api.updateChannel(id, draft);
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
      throw e;
    }
  }

  // —— 删除 ——
  async function handleDelete(row: Channel) {
    const ok = await confirm({
      title: `删除渠道「${row.name}」?`,
      content: '将级联删除该渠道下的全部供给源与挂载关系,且不可恢复。',
      okText: '删除',
      danger: true,
    });
    if (!ok) return;
    try {
      await api.deleteChannel(row.id);
      message.success(`渠道「${row.name}」已删除`);
      await invalidate();
    } catch (e) {
      message.error(errText(e));
    }
  }

  // —— 同步模型(拉渠道 /v1/models → 补目录 + 挂停用 offer)——
  async function handleSyncModels(row: Channel) {
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
    }
  }

  // —— 获取官方定价(按 provider 抓厂商官网单价表 → 落「官方参考价」,不改 offer 价)——
  // 失败即失败:抓不到/解析不出/页面改版一律弹窗显式报错,原报价保持不变。
  async function handleFetchPricing(row: Channel) {
    try {
      const r = await api.fetchPricing(row.id);
      setPricingRes({ name: row.name, provider: row.provider, result: r });
      // 官方参考价变化 → 模型抽屉/广场的比对视图需重取。
      qc.invalidateQueries({ queryKey: ['official-prices'] });
    } catch (e) {
      setPricingRes({ name: row.name, provider: row.provider, error: errText(e) });
    }
  }

  const list = useMemo(() => channels.filter(c => {
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
  }), [channels, kw, provider, status, quotaFilter, quotaById]);

  // 额度列可点表头排序:余量比率 = 各可用窗口已用率的最大值(见 quotaRatio)。
  const sorted = useMemo(() => {
    if (!sort) return list;
    if (sort.key !== 'quota') return list;
    return [...list].sort(
      (a, b) => ((quotaRatio(quotaById.get(a.id)?.data) ?? -1) - (quotaRatio(quotaById.get(b.id)?.data) ?? -1)) * sort.dir,
    );
  }, [list, sort, quotaById]);

  const onSort = (key: string) => {
    if (key !== 'quota') return;
    setSort(s => (s && s.key === key ? { key, dir: s.dir === 1 ? -1 : 1 } : { key, dir: 1 }));
  };

  const PAGE_SIZE = 10;
  const total = sorted.length;
  const pageCount = Math.max(1, Math.ceil(total / PAGE_SIZE));
  const curPage = Math.min(page, pageCount);
  const paged = total > PAGE_SIZE ? sorted.slice((curPage - 1) * PAGE_SIZE, curPage * PAGE_SIZE) : sorted;

  const columns: Column<Channel>[] = [
    {
      // 渠道名 / 供应商 / Base URL 三合一 —— 都是「这条渠道是什么」,同格堆叠。
      key: 'name',
      title: '渠道',
      render: (r) => (
        <div style={{ minWidth: 0 }}>
          <div className="strong">{r.name}</div>
          <div className="hstack" style={{ gap: 6, fontSize: 12.5, color: 'var(--t3)' }}>
            <ProviderMark name={channelMark(r)} size={14} />
            {channelLabel(r)} · {egressLabel(r.egressProto)} · {r.modelCount} 个模型
          </div>
          <div className="mono ellip" style={{ fontSize: 12, color: 'var(--t3)' }} title={r.baseUrl}>
            {r.baseUrl}
          </div>
        </div>
      ),
    },
    {
      // 优先级 / 权重 合并 —— 两者都是选路参数,同列上下排列
      key: 'route', title: '选路', align: 'right', width: 84,
      render: (r) => <span className="num" style={{ color: 'var(--t3)' }}>P{r.priority} · W{r.weight}</span>,
    },
    {
      // 成功率 / 延迟 / 状态 合并 —— 都是「这条渠道现在健不健康」
      key: 'health', title: '健康度', align: 'right', width: 132,
      render: (r) => {
        // down/disabled/unknown 都没有可报的成功率与延迟:前者在冷却、后两者没跑过流量或没启用。
        const noData = r.status === 'down' || r.status === 'disabled' || r.status === 'unknown';
        return (
          <span style={{ display: 'inline-flex', flexDirection: 'column', alignItems: 'flex-end', gap: 3 }}>
            <StatusDot status={r.status} />
            <span className="num" style={{ fontSize: 12.5 }}>
              {noData ? '—' : fmt.pct(r.successRate, 2)} · {noData ? '—' : fmt.ms(r.latencyMs)}
            </span>
            {r.circuitOpen && <Badge tone="red">熔断中</Badge>}
          </span>
        );
      },
    },
    {
      // 今日 Token / 花费 合并 —— 同一时段的量价,一行显示
      key: 'today', title: '今日用量', align: 'right', width: 104,
      render: (r) => (
        <span style={{ display: 'inline-flex', flexDirection: 'column', alignItems: 'flex-end', gap: 2 }}>
          <span className="num">{fmt.k(r.todayTokens)}</span>
          <span className="num" style={{ fontSize: 12.5, color: 'var(--t3)' }}>{fmt.usd(r.todayCostUsd)}</span>
        </span>
      ),
    },
    {
      // 额度:窗口型逐条(标签+条+%),余额型直接给金额。表头可排序。
      key: 'quota', title: '额度', width: 200, sortable: true,
      render: (r) => {
        const q = quotaById.get(r.id);
        return q ? <QuotaCell q={q} ch={r} /> : dash;
      },
    },
    {
      // 高频（测试/编辑）外露，低频（同步/抓价/删除）收进「更多」
      key: 'actions', title: '操作', align: 'right', width: 160,
      render: (r) => {
        const vi = vendorInfo(r.provider);
        const canFetch = !!vi && !vi.manualOnly;
        const manualOnly = !!vi?.manualOnly;
        const moreItems: MenuItem[] = [
          { label: '同步模型', icon: 'refresh', onClick: () => handleSyncModels(r) },
          { label: '成本系数', icon: 'gauge', onClick: () => setRatioChannel(r) },
        ];
        if (canFetch) moreItems.push({ label: '获取官方定价', icon: 'download', onClick: () => handleFetchPricing(r) });
        moreItems.push({ sep: true }, { label: '删除', icon: 'trash', danger: true, onClick: () => void handleDelete(r) });
        return (
          <HStack gap={4}>
            <Button size="sm" loading={testingId === r.id} onClick={() => { setTestingId(r.id); test.mutate(r.id); }}>
              测试
            </Button>
            <Button size="sm" onClick={() => openEdit(r)}>编辑</Button>
            <Dropdown
              title={canFetch ? undefined : `${manualOnly ? '该厂商官方页为动态渲染,无法稳定抓取。' : ''}聚合渠道请到「官方定价」页按厂商抓取。`}
              ariaLabel={`更多操作 ${r.name}`}
              items={moreItems}
            />
          </HStack>
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
      action={<Button variant="primary" onClick={openCreate}>新建渠道</Button>}
    />
  ) : (
    <NoResultState
      title="没有符合条件的渠道"
      desc="当前筛选（关键字 / 供应商 / 状态 / 额度）没有命中。"
      action={<Button onClick={() => { setKw(''); setProvider(''); setStatus(''); setQuotaFilter(''); }}>清除筛选</Button>}
    />
  );

  return (
    <>
      <PageHeader
        title="渠道管理"
        desc="一条渠道 = 一个上游 API 端点与凭据，管的是「怎么连上去」"
        extra={<Button variant="primary" onClick={openCreate}>新建渠道</Button>}
      />

      <Blocks>
        <BlockCard>
          <BlockBody>
            <div className="toolbar">
              <div className="hstack wrap" style={{ gap: 8 }}>
                <Input
                  placeholder="搜索名称或地址"
                  style={{ width: 240 }}
                  value={kw}
                  onChange={e => setKw(e.target.value)}
                />
                <Select
                  style={{ width: 150 }} value={provider} onChange={e => setProvider(e.target.value)}
                  options={[
                    { value: '', label: '全部供应商' },
                    ...providers.map(p => ({ value: p, label: p })),
                    // 聚合渠道 provider 为空,单列一项才能筛出来(空串已被「全部」占用)
                    { value: '__none__', label: '非厂商 / 聚合' },
                  ]}
                />
                <Select
                  style={{ width: 140 }} value={status} onChange={e => setStatus(e.target.value)}
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
                  style={{ width: 160 }} value={quotaFilter} onChange={e => setQuotaFilter(e.target.value)}
                  options={[
                    { value: '', label: '全部额度' },
                    { value: 'alert', label: '额度告警 ≥85%' },
                    { value: 'warn', label: '额度逼近 ≥60%' },
                  ]}
                />
              </div>
              <span className="cell-sub">
                {filtering ? (
                  <>筛选出 <b>{sorted.length}</b> / {channels.length} 条渠道</>
                ) : (
                  <>共 <b>{channels.length}</b> 条渠道</>
                )}
              </span>
            </div>
          </BlockBody>
        </BlockCard>

        <BlockCard>
          <BlockBody style={{ padding: 0 }}>
            {isLoading && channels.length === 0 ? (
              <div style={{ padding: 14 }}>
                <span className="cell-sub">加载中…</span>
              </div>
            ) : sorted.length === 0 ? (
              emptyNode
            ) : (
              <>
                <Table<Channel> columns={columns} rows={paged} rowKey={r => r.id} sort={sort ?? undefined} onSort={onSort} />
                {total > PAGE_SIZE && <Pager total={total} page={curPage} size={PAGE_SIZE} onChange={setPage} />}
              </>
            )}
          </BlockBody>
        </BlockCard>
      </Blocks>

      {/* 新建 / 编辑共享弹窗 */}
      <ChannelFormModal
        open={open}
        editing={editing}
        onClose={closeModal}
        onSubmit={handleSubmit}
      />

      {/* 同步模型结果 */}
      {syncRes && (
        <div className="overlay" style={{ zIndex: 70 }}>
          <Modal
            title={`同步模型 · ${syncRes.name}`}
            onClose={() => setSyncRes(null)}
            width={560}
            footer={<Button variant="primary" onClick={() => setSyncRes(null)}>关闭</Button>}
          >
            <Note>
              <b>本渠道现关联 {syncRes.modelCount} 个模型</b>
              <div>本次新增 {syncRes.added}、已存在 {syncRes.updated}。新同步的模型默认停用，需到「模型广场」定价后启用。</div>
            </Note>
            {syncRes.models.length > 0 ? (
              <div className="code" style={{ marginTop: 12 }}>{syncRes.models.join('\n')}</div>
            ) : (
              <div style={{ fontSize: 13.5, color: 'var(--t3)', marginTop: 12 }}>
                该渠道没有返回新模型（目录中均已存在）。
              </div>
            )}
          </Modal>
        </div>
      )}

      {/* 获取官方定价结果(成功=落库条数;失败=显式报错,原报价不变) */}
      {pricingRes && (
        <div className="overlay" style={{ zIndex: 70 }}>
          <Modal
            title={`获取官方定价 · ${pricingRes.name}`}
            onClose={() => setPricingRes(null)}
            width={560}
            footer={<Button variant="primary" onClick={() => setPricingRes(null)}>关闭</Button>}
          >
            {pricingRes.error ? (
              <Note tone="warn">
                <b style={{ color: 'var(--err)' }}>获取失败</b>
                <div>
                  {pricingRes.error}
                  <br />
                  本次未写入任何价格,原有报价保持不变。请核对官方页面后重试。
                </div>
              </Note>
            ) : pricingRes.result ? (
              <>
                <Note>
                  <b>已从官方计费页抓取 {pricingRes.result.upserted} 个模型的单价</b>
                  <div>
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
                  <div className="code" style={{ marginTop: 12 }}>{pricingRes.result.models.join('\n')}</div>
                )}
              </>
            ) : null}
          </Modal>
        </div>
      )}

      {/* 成本系数:成本 = 厂商官方价 × 系数,与售价无关 */}
      <CostRatioModal open={!!ratioChannel} channel={ratioChannel} onClose={() => setRatioChannel(null)} />
    </>
  );
}
