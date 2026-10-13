import { useEffect, useMemo, useRef, useState } from 'react';
import {
  Btn, Card, Check, Empty, Field, Input, Note, Select, StatusDot, Switch,
} from '@/ui/primitives';
import { Drawer, Dropdown, Modal, confirm } from '@/ui/overlay';
import { toast } from '@/ui/toast';
import Chart from '@/components/Chart';
import ProviderMark from '@/components/ProviderMark';
import OfficialPricePanel from '@/components/models/OfficialPricePanel';
import { useSortableRows } from '@/hooks/useSortableRows';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '@/services/api';
import { capabilities as ALL_CAPS } from '@/constants';
import { CAP_LABEL, fmt, STATUS } from '@/utils/format';
import { channelLabel, channelMark } from '@/utils/channel';
import type { Capability, Channel, ModelCatalogItem, ModelDraft, ModelOffer, OfferDraft } from '@/types';

/* —— 成本 / 本站价 / 毛利 三列 ——
 *
 * 这三列是同一个问题的三个切面。计费侧:
 *   - 成本  = 官方价 × 渠道系数 ratio (绑定官方价时) / offer 手填四价 (未绑定)
 *   - 本站价 = 官方价 × 倍率 rate
 *   - 毛利  = 1 − 成本/售价 = 1 − ratio/rate   (官方价在分子分母里约掉了)
 *
 * 最要命的失败模式是把「成本未知」渲染成 0 —— 那会让 91 个无成本依据的模型全都显示毛利率 100%,
 * 而它们在改造前正是这么显示的(成本被当成 0)。source === 'unknown' 是这一点的显式表达,三列都必须认它。
 */

/** 成本列:派生值 + 口径标注(官方派生 / 手填兜底 / 未知)。 */
function CostCell({ r }: { r: ModelOffer }) {
  const c = r.cost;
  if (!c || c.source === 'unknown') {
    return (
      <span className="muted" style={{ fontSize: 12.5 }} title="未绑定官方价,且该供给源四价为空 —— 没有成本依据。到「官方定价」页把模型绑定到官方价,或手工填兜底价。">
        未知
      </span>
    );
  }
  const derived = c.source === 'official';
  const tip = derived
    ? `输入 ${fmt.price(c.in)} · 输出 ${fmt.price(c.out)}${c.cacheRead ? ` · 缓存读 ${fmt.price(c.cacheRead)}` : ''}${c.cacheWrite ? ` · 缓存写 ${fmt.price(c.cacheWrite)}` : ''}\n官方价 × ${c.ratio}${c.ratio === 1 ? '(未设系数,按 1.0 计 —— 会高估成本)' : `(${c.vendor} 渠道系数)`}${c.warn ? `\n${c.warn}` : ''}`
    : `手填兜底价(未绑定官方价)\n输入 ${fmt.price(c.in)} · 输出 ${fmt.price(c.out)}`;
  return (
    <span className="num" style={{ display: 'inline-flex', flexDirection: 'column', alignItems: 'flex-end', gap: 1 }} title={tip}>
      <span>{fmt.price(c.in)}</span>
      <span style={{ fontSize: 11, color: derived && c.ratio === 1 ? 'var(--warn)' : 'var(--t3)' }}>
        {derived ? (c.ratio === 1 ? '系数未设' : `官方×${c.ratio}`) : '手填'}
      </span>
    </span>
  );
}

/** 本站价列 = 官方价 × 倍率(与计费同源)。 */
function RetailCell({ r, rate }: { r: ModelOffer; rate: number }) {
  const c = r.cost;
  if (!c || c.source === 'unknown') {
    return (
      <span className="num muted" title="无成本依据,本站价无从对照。左列四价是该供给源手填值。">{fmt.price(r.inputPriceUsd)}</span>
    );
  }
  const retail = c.source === 'official'
    ? c.in * (rate / (c.ratio || 1))
    : c.in * rate;
  const tip = c.source === 'official' ? `官方价 × 倍率 ${rate}` : `手填成本 × 倍率 ${rate}(未绑定官方价,计费走成本×倍率)`;
  return <span className="num" title={tip}>{fmt.price(retail)}</span>;
}

/** 毛利列 = 1 − 成本/售价。**成本未知时一律不显示** —— 那会伪装成 100% 毛利。 */
function MarginCell({ r, rate }: { r: ModelOffer; rate: number }) {
  const c = r.cost;
  if (!c || c.source === 'unknown') {
    return <span className="muted" style={{ fontSize: 12.5 }} title="没有成本依据,算不出毛利。显示 0 或 100% 都是假的。">—</span>;
  }
  const ratio = c.source === 'official' ? (c.ratio || 1) : 1;
  const m = 1 - ratio / (rate || 1);
  const tip = c.source === 'official'
    ? `毛利 = 1 − 系数/倍率 = 1 − ${ratio}/${rate}`
    : `毛利 = 1 − 1/倍率 = 1 − 1/${rate}(成本为手填值,精确度取决于手填价)`;
  return <span className="num" style={{ color: m < 0 ? 'var(--err)' : undefined }} title={tip}>{fmt.pct(m, 1)}</span>;
}

function statusTone(s: string): 'g' | 'a' | 'r' | 's' {
  if (s === 'healthy' || s === 'active') return 'g';
  if (s === 'degraded') return 'a';
  if (s === 'down' || s === 'expired') return 'r';
  return 's';
}

interface Props {
  model: ModelCatalogItem | null;
  onClose: () => void;
  /** 由调用方提供的删除入口 （含确认与后续清理） */
  onDeleteModel: () => void;
}

interface BasicFormValues {
  name: string;
  displayName?: string;
  contextWindow: number;
  capabilities: Capability[];
  /** 售价倍率;undefined/空 = 跟随全局 settings.priceMultiplier */
  rateOverride?: number | null;
}

/* —— 供给源 添加/编辑 弹窗 —— */

interface OfferFormValues {
  channelId?: number;
  inputPriceUsd?: number;
  outputPriceUsd?: number;
  cacheReadPriceUsd?: number;
  cacheWritePriceUsd?: number;
  overridePrice?: boolean;
  rateLimitRpm?: number;
  enabled?: boolean;
  note?: string;
  upstreamModel?: string;
}

function OfferFormModal({ open, modelId, modelName, editing, channels, usedChannelIds, onClose }: {
  open: boolean;
  modelId: number;
  /** 模型级默认真实名(留空上游名时的出站名),仅用于提示文案 */
  modelName: string;
  editing: ModelOffer | null;
  channels: Channel[];
  usedChannelIds: number[];
  onClose: () => void;
}) {
  const qc = useQueryClient();
  const [f, setF] = useState<OfferFormValues>({});

  const isEdit = !!editing;

  useEffect(() => {
    if (!open) return;
    if (editing) {
      setF({
        inputPriceUsd: editing.inputPriceUsd,
        outputPriceUsd: editing.outputPriceUsd,
        cacheReadPriceUsd: editing.cacheReadPriceUsd ?? 0,
        cacheWritePriceUsd: editing.cacheWritePriceUsd ?? 0,
        overridePrice: editing.overridePrice,
        rateLimitRpm: editing.rateLimitRpm,
        enabled: editing.enabled,
        note: editing.note,
        upstreamModel: editing.upstreamModel ?? '',
      });
    } else {
      setF({ inputPriceUsd: 0, outputPriceUsd: 0, cacheReadPriceUsd: 0, cacheWritePriceUsd: 0, overridePrice: false, rateLimitRpm: 60, enabled: true });
    }
  }, [open, editing]);

  // 后端 (model_id, channel_id) 唯一：新建时排除已挂渠道；编辑时渠道不可更换（更新接口不支持改 channel）
  const candidateChannels = useMemo(() => {
    if (editing) return channels.filter(c => c.id === editing.channelId);
    return channels.filter(c => !usedChannelIds.includes(c.id));
  }, [channels, editing, usedChannelIds]);

  const mutate = useMutation({
    mutationFn: (values: OfferFormValues) => {
      const draft: OfferDraft = {
        channelId: editing ? editing.channelId : (values.channelId ?? 0),
        inputPriceUsd: Number(values.inputPriceUsd) || 0,
        outputPriceUsd: Number(values.outputPriceUsd) || 0,
        cacheReadPriceUsd: Number(values.cacheReadPriceUsd) || 0,
        cacheWritePriceUsd: Number(values.cacheWritePriceUsd) || 0,
        overridePrice: values.overridePrice ?? false,
        rateLimitRpm: values.rateLimitRpm || 60,
        enabled: values.enabled ?? true,
        note: values.note ?? '',
        // 编辑时原样回传来源留证(PATCH 全量替换,漏传会清空官方价来源)
        priceSourceUrl: editing?.priceSourceUrl,
        priceFetchedAt: editing?.priceFetchedAt,
        priceCurrency: editing?.priceCurrency,
        priceNativeText: editing?.priceNativeText,
        upstreamModel: (values.upstreamModel ?? '').trim(),
      };
      return editing ? api.updateOffer(editing.id, draft) : api.createOffer(modelId, draft);
    },
    onSuccess: () => {
      toast.ok(isEdit ? '供给源已更新' : '供给源已添加');
      onClose();
      qc.invalidateQueries({ queryKey: ['models'] });
      qc.invalidateQueries({ queryKey: ['channels'] });
    },
    onError: (e) => toast.err((e as Error)?.message || (isEdit ? '更新失败' : '添加失败')),
  });

  const submit = () => {
    if (!isEdit && !f.channelId) { toast.err('请选择渠道'); return; }
    if (f.inputPriceUsd == null || f.inputPriceUsd < 0) { toast.err('请输入输入价'); return; }
    if (f.outputPriceUsd == null || f.outputPriceUsd < 0) { toast.err('请输入输出价'); return; }
    mutate.mutate(f);
  };

  return (
    <Modal
      open={open}
      title={isEdit ? '编辑供给源' : '添加供给源'}
      width={580}
      onClose={onClose}
      footer={
        <>
          <Btn onClick={onClose}>取消</Btn>
          <Btn variant="primary" loading={mutate.isPending} onClick={submit}>{isEdit ? '保存' : '添加'}</Btn>
        </>
      }
    >
      {isEdit ? (
        <Note style={{ marginBottom: 16 }}>
          渠道：<b>{editing.channelName}</b>（挂载后不可更换，如需换渠道请删除后重建）
        </Note>
      ) : (
        <Field label="渠道" hint="已为该模型提供过供给源的渠道不会出现在候选中">
          <Select
            value={f.channelId ?? ''}
            onChange={e => setF(p => ({ ...p, channelId: Number(e.target.value) }))}
          >
            <option value="">选择渠道</option>
            {candidateChannels.map(c => (
              <option key={c.id} value={c.id}>{c.name} · {channelLabel(c)}{c.enabled ? '' : '（渠道已停用）'}</option>
            ))}
          </Select>
        </Field>
      )}

      <div className="form-grid">
        <Field label="输入价（$/1M tokens）">
          <Input type="number" min="0" step="0.01" value={String(f.inputPriceUsd ?? 0)} onChange={e => setF(p => ({ ...p, inputPriceUsd: Number(e.target.value) }))} />
        </Field>
        <Field label="输出价（$/1M tokens）">
          <Input type="number" min="0" step="0.01" value={String(f.outputPriceUsd ?? 0)} onChange={e => setF(p => ({ ...p, outputPriceUsd: Number(e.target.value) }))} />
        </Field>
      </div>
      <div className="form-grid">
        <Field label="缓存命中读价（$/1M tokens）" hint="留 0 = 无依据">
          <Input type="number" min="0" step="0.01" value={String(f.cacheReadPriceUsd ?? 0)} onChange={e => setF(p => ({ ...p, cacheReadPriceUsd: Number(e.target.value) }))} />
        </Field>
        <Field label="缓存写入价（$/1M tokens）" hint="留 0 = 无依据（按输入价计）">
          <Input type="number" min="0" step="0.01" value={String(f.cacheWritePriceUsd ?? 0)} onChange={e => setF(p => ({ ...p, cacheWritePriceUsd: Number(e.target.value) }))} />
        </Field>
      </div>
      <Field label="限流（RPM）">
        <Input type="number" min="1" max="100000" step="10" value={String(f.rateLimitRpm ?? 60)} onChange={e => setF(p => ({ ...p, rateLimitRpm: Number(e.target.value) }))} />
      </Field>

      <div className="form-grid">
        <Field label="覆盖渠道价" hint="标记该供给源按本处填写的报价计费，而不是参考渠道侧的默认价">
          <Switch checked={!!f.overridePrice} onChange={v => setF(p => ({ ...p, overridePrice: v }))} />
        </Field>
        <Field label="启用">
          <Switch checked={!!f.enabled} onChange={v => setF(p => ({ ...p, enabled: v }))} />
        </Field>
      </div>

      <Field label="上游模型名" hint={`留空 = 用模型名「${modelName}」。多渠道供同一对外名、但上游真实名不同时,在此填该渠道的真实名。`}>
        <Input className="mono" value={f.upstreamModel ?? ''} placeholder={modelName || '如 deepseek/deepseek-v4.1-flash'} onChange={e => setF(p => ({ ...p, upstreamModel: e.target.value }))} />
      </Field>

      <Field label="备注">
        <Input value={f.note ?? ''} onChange={e => setF(p => ({ ...p, note: e.target.value }))} />
      </Field>
    </Modal>
  );
}

/* —— 模型抽屉 —— */

export default function ModelDrawer({ model, onClose, onDeleteModel }: Props) {
  const qc = useQueryClient();
  const [tab, setTab] = useState('overview');
  const [offerModal, setOfferModal] = useState<{ open: boolean; editing: ModelOffer | null }>({ open: false, editing: null });
  /** 「原始模型名(渠道侧)」在供给源已各自指定上游名时默认折叠,可展开编辑 */
  const [showRawName, setShowRawName] = useState(false);

  const lastModel = useRef<ModelCatalogItem | null>(null);
  if (model) lastModel.current = model;
  const dm = model ?? lastModel.current;

  const handleClose = () => {
    setOfferModal({ open: false, editing: null });
    onClose();
  };

  const { data: channels = [] } = useQuery({ queryKey: ['channels'], queryFn: api.getChannels });
  // 全局倍率:作「留空即跟随全局」的参照文案,只读展示,不在此处改动。
  const { data: settings } = useQuery({ queryKey: ['settings'], queryFn: api.getSettings });
  const globalRate = settings?.priceMultiplier ?? 1;
  /**
   * 本模型在计费时的实际倍率:模型级覆盖优先,否则跟随全局。
   * 与 proxy 侧 `rate = plan.RateOverride ?? settings.PriceMultiplier ?? 1.0` 同序,
   * 否则管理面看到的毛利与实收对不上。倍率挂模型上,同一模型的所有供给源共用。
   */
  const rate = dm ? dm.rateOverride ?? globalRate : globalRate;
  const usageQuery = useQuery({
    queryKey: ['model-usage', dm?.id, 7],
    queryFn: () => api.getModelUsage(dm!.id, 7),
    enabled: !!dm,
  });

  // 基本信息表单：随打开模型切换回填(手写 state)
  const [form, setForm] = useState<BasicFormValues>({ name: '', contextWindow: 0, capabilities: [], rateOverride: null });
  const prevModelId = useRef<number | null>(null);
  useEffect(() => {
    const id = dm?.id ?? null;
    if (id !== prevModelId.current) {
      prevModelId.current = id;
      setShowRawName(false);
      if (dm) {
        setForm({
          name: dm.originalName,
          displayName: dm.displayName ?? '',
          contextWindow: dm.contextWindow,
          capabilities: dm.capabilities,
          rateOverride: dm.rateOverride ?? null,
        });
      }
    }
  }, [dm]);

  const offers = dm?.offers ?? [];
  /** 每个供给源都单独指定了上游真实名 → 模型级名称只剩同步去重用途,可折叠 */
  const allUpstream = offers.length > 0 && offers.every(o => (o.upstreamModel ?? '').trim() !== '');
  const enabledOffers = offers.filter(o => o.enabled);
  const usableOffers = enabledOffers.length;
  const usedChannelIds = useMemo(() => offers.map(o => o.channelId), [offers]);
  /** 后端强制 (model_id, channel_id) 唯一：还有未挂载该模型的渠道才可新增 */
  const canAddOffer = useMemo(
    () => channels.some(ch => !usedChannelIds.includes(ch.id)),
    [channels, usedChannelIds],
  );

  /* —— 供给源拖拽重排 —— */
  const reorder = useMutation({
    mutationFn: (v: { modelId: number; from: number; insertAt: number }) =>
      api.reorderOffers(v.modelId, v.from, v.insertAt),
    onSuccess: (_offers, v) => {
      qc.invalidateQueries({ queryKey: ['models'] });
      const target = v.insertAt > v.from ? v.insertAt - 1 : v.insertAt;
      toast.ok(`优先级已更新：第 ${target + 1} 位`);
    },
    onError: () => toast.err('调整失败，请重试'),
  });

  const { onRow, handleProps } = useSortableRows((from, insertAt) => {
    if (!dm) return;
    reorder.mutate({ modelId: dm.id, from, insertAt });
  });

  /* —— 模型级：启停 / 保存基本信息 —— */
  const toggleModel = useMutation({
    mutationFn: (v: { id: number; enabled: boolean }) => api.toggleModel(v.id, v.enabled),
    onSuccess: (r) => {
      const skipped = r?.skippedZeroPrice ?? [];
      if (skipped.length > 0) {
        // 联动启用时跳过零价供给源(issue #26)—— 说清是哪几条、为什么。
        toast.info(`模型已启用,但有 ${skipped.length} 条供给源因无成本依据未启用:${skipped.join('、')}`);
      } else {
        toast.ok('模型状态已更新');
      }
      qc.invalidateQueries({ queryKey: ['models'] });
    },
    onError: () => toast.err('状态更新失败'),
  });

  const saveModel = useMutation({
    mutationFn: (v: { id: number; draft: ModelDraft }) => api.updateModel(v.id, v.draft),
    onSuccess: () => {
      toast.ok('模型信息已保存');
      qc.invalidateQueries({ queryKey: ['models'] });
    },
    onError: (e) => toast.err((e as Error)?.message || '保存失败'),
  });

  const onSaveBasic = () => {
    if (!dm) return;
    const v = form;
    if (!v.name.trim()) { toast.err('请输入模型名称'); return; }
    saveModel.mutate({
      id: dm.id,
      draft: {
        name: (v.name ?? dm.originalName).trim(),
        displayName: (v.displayName ?? '').trim(),
        contextWindow: v.contextWindow ?? dm.contextWindow,
        capabilities: (v.capabilities ?? dm.capabilities) as Capability[],
        enabled: dm.enabled,
        // 空 = 清空覆盖(回落全局);null 显式传回让后端区分「清空」与「未传」。
        rateOverride: v.rateOverride == null ? null : Number(v.rateOverride),
      },
    });
  };

  /* —— 供给源级：启停 / 删除 / 设为首选 —— */
  const toggleOffer = useMutation({
    mutationFn: (v: { modelId: number; offerId: number; enabled: boolean }) =>
      api.toggleOffer(v.modelId, v.offerId, v.enabled),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['models'] }),
    onError: (e) => {
      // 零价闸门(issue #26):服务端拒绝时把成因原样透出,别吞成泛化文案。
      const msg = (e as Error)?.message;
      toast.err(msg || '供给源启停失败');
    },
  });

  const deleteOffer = useMutation({
    mutationFn: (id: number) => api.deleteOffer(id),
    onSuccess: () => {
      toast.ok('供给源已删除');
      qc.invalidateQueries({ queryKey: ['models'] });
      qc.invalidateQueries({ queryKey: ['channels'] });
    },
    onError: () => toast.err('删除失败'),
  });

  const confirmDeleteOffer = (o: ModelOffer) => {
    confirm({
      title: '移除供给源',
      body: `确定移除「${dm?.name}」来自 ${o.channelName} 的供给源吗？`,
      okText: '移除',
      danger: true,
      cancelText: '取消',
    }).then(ok => { if (ok) deleteOffer.mutate(o.id); });
  };

  const setPrimary = useMutation({
    mutationFn: (v: { modelId: number; offerId: number }) => api.setPrimaryOffer(v.modelId, v.offerId),
    onSuccess: () => {
      toast.ok('已设为首选');
      qc.invalidateQueries({ queryKey: ['models'] });
    },
    onError: () => toast.err('设置失败'),
  });

  const lowestIn = offers.filter(o => o.enabled).length
    ? Math.min(...offers.filter(o => o.enabled).map(o => o.inputPriceUsd))
    : null;

  const toggleCap = (c: Capability) =>
    setForm(p => ({ ...p, capabilities: p.capabilities.includes(c) ? p.capabilities.filter(x => x !== c) : [...p.capabilities, c] }));

  /* —— 用量（近 7 日，来自后端） —— */
  const daily = usageQuery.data?.daily ?? [];
  const byChannel = usageQuery.data?.byChannel ?? [];
  const lastDay = daily[daily.length - 1];
  const periodTotal = useMemo(
    () => daily.reduce(
      (acc, p) => ({ requests: acc.requests + p.requests, errors: acc.errors + p.errors, cost: acc.cost + p.costUsd }),
      { requests: 0, errors: 0, cost: 0 },
    ),
    [daily],
  );
  const periodOkRate = periodTotal.requests
    ? 1 - periodTotal.errors / periodTotal.requests
    : (dm?.successRate ?? 1);

  const totalChReq = byChannel.reduce((a, b) => a + b.requests, 0);
  const totalChCost = byChannel.reduce((a, b) => a + b.costUsd, 0);
  const totalChCharge = byChannel.reduce((a, b) => a + b.chargeUsd, 0);

  /** 本模型本时段的合计:成本 / 营收 / 毛利,与按渠道表同源(求和即得)。 */
  const periodMargin = totalChCharge > 0 ? (totalChCharge - totalChCost) / totalChCharge : null;

  const overviewStats: [string, string][] = [
    ['今日调用', fmt.k(dm?.todayRequests ?? 0)],
    ['成功率', fmt.pct(dm?.successRate ?? 1)],
    ['可用供给源', `${usableOffers}/${offers.length}`],
    ['平均延迟', enabledOffers.length ? fmt.ms(Math.round(enabledOffers.reduce((a, o) => a + o.latencyMs, 0) / enabledOffers.length)) : '—'],
  ];

  const TABS = [
    { k: 'overview', l: '概览' },
    { k: 'offers', l: `供给源${offers.length ? ` (${offers.length})` : ''}` },
    { k: 'price', l: '价格对比' },
    { k: 'usage', l: '用量' },
  ];

  return (
    <>
      <Drawer
        width={960}
        open={!!model}
        onClose={handleClose}
        title={dm ? (
          <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
            <span className="mono" style={{ fontSize: 16, fontWeight: 600 }}>{dm.name}</span>
            {dm.capabilities.map(x => <span key={x} className="tag">{CAP_LABEL[x]}</span>)}
            <span className="cell-sub">{fmt.ctx(dm.contextWindow)} 上下文</span>
            {dm.displayName && <span className="cell-sub mono">原始名 {dm.originalName}</span>}
            <span style={{ marginLeft: 'auto' }}>
              <Switch
                checked={dm.enabled}
                loading={toggleModel.isPending}
                onChange={v => toggleModel.mutate({ id: dm.id, enabled: v })}
              />
            </span>
          </div>
        ) : null}
        footer={dm ? (
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <Btn danger onClick={onDeleteModel}>删除模型</Btn>
            <div style={{ marginLeft: 'auto', display: 'flex', gap: 8 }}>
              <Btn onClick={handleClose}>取消</Btn>
              <Btn variant="primary" loading={saveModel.isPending} onClick={onSaveBasic}>保存更改</Btn>
            </div>
          </div>
        ) : null}
      >
        {dm && (
          <>
            <div className="hstack wrap" style={{ gap: 2, borderBottom: '1px solid var(--line)', marginBottom: 16 }}>
              {TABS.map(t => (
                <button
                  key={t.k}
                  type="button"
                  onClick={() => setTab(t.k)}
                  style={{
                    background: 'none', border: 'none', borderBottom: tab === t.k ? '2px solid var(--brand)' : '2px solid transparent',
                    padding: '8px 12px', color: tab === t.k ? 'var(--t1)' : 'var(--t3)', fontSize: 13,
                    fontWeight: tab === t.k ? 600 : 500, cursor: 'pointer', marginBottom: -1,
                  }}
                >
                  {t.l}
                </button>
              ))}
            </div>

            {tab === 'overview' && (
              <>
                <div className="kpi-grid">
                  {overviewStats.map(([label, value]) => (
                    <div key={label} className="card kpi">
                      <div className="kpi-label">{label}</div>
                      <div className="kpi-value">{value}</div>
                    </div>
                  ))}
                </div>
                <Card title="基本信息" style={{ marginTop: 16 }}>
                  <div style={{ maxWidth: 540 }}>
                    <Field label="统一名称" hint="网关侧对外展示与调用名，留空则用原始名；重命名不改动渠道侧真实模型名">
                      <Input className="mono" value={form.displayName ?? ''} placeholder="如 deepseek-v3（留空=用原始名）"
                        onChange={e => setForm(p => ({ ...p, displayName: e.target.value }))} />
                    </Field>
                    {allUpstream && !showRawName ? (
                      <div style={{ marginBottom: 24 }}>
                        <div style={{ fontSize: 14, color: 'var(--t2)', marginBottom: 4 }}>原始模型名（渠道侧）</div>
                        <div className="cell-sub">
                          已在每个供给源单独指定上游名，出站按命中的供给源改写请求体 model；模型级名称仅用于同步去重。
                          <button type="button" className="btn btn-ghost btn-sm" style={{ marginLeft: 6 }} onClick={() => setShowRawName(true)}>展开编辑</button>
                        </div>
                      </div>
                    ) : (
                      <Field
                        label="原始模型名（渠道侧）"
                        hint={
                          allUpstream
                            ? '渠道上游真实模型名；修改会改变同步去重与出站请求的模型名，请谨慎操作。'
                            : '渠道上游真实模型名。部分供给源未填上游名，出站将回落此名称 —— 建议到「供给源」补齐。'
                        }
                      >
                        <Input className="mono" value={form.name ?? ''} onChange={e => setForm(p => ({ ...p, name: e.target.value }))} />
                      </Field>
                    )}
                    <Field label="上下文窗口（tokens）">
                      <Input type="number" min="0" step="1024" value={String(form.contextWindow ?? 0)}
                        onChange={e => setForm(p => ({ ...p, contextWindow: Number(e.target.value) }))} />
                    </Field>
                    <Field label="能力">
                      <div className="hstack wrap" style={{ gap: 12 }}>
                        {ALL_CAPS.map(x => (
                          <Check key={x} checked={form.capabilities.includes(x)} onChange={() => toggleCap(x)}>{CAP_LABEL[x]}</Check>
                        ))}
                      </div>
                    </Field>
                    <Field
                      label="售价倍率"
                      hint={<>本站价 = 官方价 × 倍率，全站同一模型对所有客户同一价。留空则跟随全局倍率（当前 <b className="num">×{globalRate}</b>）。未录官方价的模型回落「成本 × 倍率」。</>}
                    >
                      <Input type="number" min="0.01" step="0.1" placeholder={`留空=用全局倍率（×${globalRate}）`}
                        value={form.rateOverride == null ? '' : String(form.rateOverride)}
                        onChange={e => setForm(p => ({ ...p, rateOverride: e.target.value === '' ? null : Number(e.target.value) }))} />
                    </Field>
                  </div>
                  <div className="cell-sub" style={{ fontSize: 12 }}>
                    启停开关在抽屉右上角即时生效；其余修改点击底部「保存更改」。
                  </div>
                </Card>
              </>
            )}

            {tab === 'offers' && (
              <>
                <Note style={{ marginBottom: 16 }}>
                  拖动左侧手柄可调整优先级，请求按此顺序尝试，上游失败自动降级到下一个可用供给源。路由规则的优先级高于此处。
                </Note>
                <div className="table-wrap">
                  <table className="tb">
                    <thead>
                      <tr>
                        <th style={{ width: 32 }} />
                        <th>渠道</th>
                        <th>供应商</th>
                        <th>上游名</th>
                        <th className="right">输入价</th>
                        <th className="right">输出价</th>
                        <th className="right">缓存读价</th>
                        <th className="right">缓存写价</th>
                        <th className="right">成本</th>
                        <th className="right">本站价</th>
                        <th className="right">毛利</th>
                        <th className="right">延迟</th>
                        <th className="right">成功率</th>
                        <th>价格来源</th>
                        <th>状态</th>
                        <th>启用</th>
                        <th className="right" style={{ width: 56 }} />
                      </tr>
                    </thead>
                    <tbody>
                      {offers.map((r, i) => (
                        <tr key={r.id} {...onRow(i)}>
                          <td><span {...handleProps(i)} title="拖动调整优先级" style={{ cursor: 'grab' }}>⋮⋮</span></td>
                          <td><b style={{ fontWeight: 500 }}>{r.channelName}</b></td>
                          <td>
                            <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
                              <ProviderMark name={channelMark(r)} /> {channelLabel(r)}
                            </span>
                          </td>
                          <td>
                            {r.upstreamModel ? (
                              <span className="mono" style={{ fontSize: 12.5 }}>{r.upstreamModel}</span>
                            ) : (
                              <span className="muted" style={{ fontSize: 12.5 }} title="未单独指定,出站用模型名">同模型名</span>
                            )}
                          </td>
                          <td className="right num" style={{ color: r.enabled && lowestIn !== null && r.inputPriceUsd === lowestIn ? 'var(--brand)' : undefined }}>{fmt.price(r.inputPriceUsd)}</td>
                          <td className="right num">{fmt.price(r.outputPriceUsd)}</td>
                          <td className="right num">{r.cacheReadPriceUsd ? fmt.price(r.cacheReadPriceUsd) : '—'}</td>
                          <td className="right num">{r.cacheWritePriceUsd ? fmt.price(r.cacheWritePriceUsd) : '—'}</td>
                          <td className="right"><CostCell r={r} /></td>
                          <td className="right"><RetailCell r={r} rate={rate} /></td>
                          <td className="right"><MarginCell r={r} rate={rate} /></td>
                          <td className="right num">{r.latencyMs ? fmt.ms(r.latencyMs) : '—'}</td>
                          <td className="right num">{fmt.pct(r.successRate, 2)}</td>
                          <td>
                            {r.priceSourceUrl ? (
                              <a href={r.priceSourceUrl} target="_blank" rel="noreferrer" className="mono" style={{ fontSize: 12.5 }} title={`来源:${r.priceSourceUrl}\n抓取:${fmt.dt(r.priceFetchedAt)}`}>
                                官方 {r.priceCurrency} ↗
                              </a>
                            ) : (
                              <span className="muted" style={{ fontSize: 12.5 }}>手工</span>
                            )}
                          </td>
                          <td>
                            <span className="hstack" style={{ gap: 6 }}>
                              <StatusDot tone={r.enabled ? statusTone(r.status) : 's'} />
                              <span className="cell-sub">{STATUS[r.enabled ? r.status : 'disabled']?.t ?? (r.enabled ? r.status : '已停用')}</span>
                            </span>
                          </td>
                          <td>
                            {(() => {
                              const zero = r.cost?.zeroPrice && !r.enabled;
                              const sw = (
                                <Switch
                                  checked={r.enabled}
                                  disabled={zero}
                                  loading={toggleOffer.isPending && toggleOffer.variables?.offerId === r.id}
                                  onChange={next => toggleOffer.mutate({ modelId: r.modelId, offerId: r.id, enabled: next })}
                                />
                              );
                              return zero ? (
                                <span title={`无成本依据,不能启用:${r.cost?.zeroReason || '官方价与兜底四价都为空'}`}>{sw}</span>
                              ) : sw;
                            })()}
                          </td>
                          <td className="right">
                            <Dropdown
                              menu={[
                                { key: 'primary', label: '设为首选', icon: 'star', disabled: r.priority <= 1, onClick: () => { if (r.priority > 1) setPrimary.mutate({ modelId: r.modelId, offerId: r.id }); } },
                                { key: 'edit', label: '编辑', icon: 'edit', onClick: () => setOfferModal({ open: true, editing: r }) },
                                { key: 'delete', label: '删除', icon: 'trash', danger: true, onClick: () => confirmDeleteOffer(r) },
                              ]}
                            >
                              {() => <Btn size="sm" variant="ghost" icon="more" aria-label="更多操作" />}
                            </Dropdown>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
                <Btn
                  variant="ghost"
                  block
                  style={{ marginTop: 16 }}
                  icon="plus"
                  disabled={!canAddOffer}
                  onClick={() => setOfferModal({ open: true, editing: null })}
                >
                  + 添加供给源
                </Btn>
              </>
            )}

            {tab === 'price' && (
              <>
                <div className="cell-sub" style={{ marginBottom: 12 }}>
                  单位:每百万 tokens(币种随「系统设置 → 计价币种」)
                </div>
                <Chart
                  labels={offers.map(o => o.channelName)}
                  series={[
                    { name: '输入价', data: offers.map(o => o.inputPriceUsd) },
                    { name: '输出价', data: offers.map(o => o.outputPriceUsd) },
                  ]}
                  h={Math.max(160, offers.length * 36 + 40)}
                  yFmt={v => fmt.price(v)}
                  tipFmt={v => fmt.price(v)}
                />
                <div className="table-wrap" style={{ marginTop: 20 }}>
                  <table className="tb">
                    <thead>
                      <tr>
                        <th>渠道</th>
                        <th className="right">上下文</th>
                        <th className="right">限流</th>
                        <th className="right">输入价</th>
                        <th className="right">输出价</th>
                        <th className="right">缓存读价</th>
                        <th className="right">缓存写价</th>
                        <th>备注</th>
                      </tr>
                    </thead>
                    <tbody>
                      {offers.map(o => (
                        <tr key={o.id}>
                          <td><b style={{ fontWeight: 500 }}>{o.channelName}</b></td>
                          <td className="right num">{fmt.ctx(o.contextWindow)}</td>
                          <td className="right num">{o.rateLimitRpm} RPM</td>
                          <td className="right num">{fmt.price(o.inputPriceUsd)}</td>
                          <td className="right num">{fmt.price(o.outputPriceUsd)}</td>
                          <td className="right num">{o.cacheReadPriceUsd ? fmt.price(o.cacheReadPriceUsd) : '—'}</td>
                          <td className="right num">{o.cacheWritePriceUsd ? fmt.price(o.cacheWritePriceUsd) : '—'}</td>
                          <td className="cell-sub">{o.note || '—'}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
                <OfficialPricePanel model={dm} offers={offers} />
              </>
            )}

            {tab === 'usage' && (
              <>
                <div className="kpi-grid">
                  {[
                    ['今日请求', fmt.k(lastDay?.requests ?? 0)],
                    ['今日花费', fmt.usd(lastDay?.costUsd ?? 0)],
                    ['近 7 日请求', fmt.k(periodTotal.requests)],
                    ['近 7 日成功率', fmt.pct(periodOkRate, 2)],
                  ].map(([label, value]) => (
                    <div key={label} className="card kpi">
                      <div className="kpi-label">{label}</div>
                      <div className="kpi-value">{value}</div>
                    </div>
                  ))}
                </div>
                <Card title="近 7 日趋势" style={{ marginTop: 16, marginBottom: 16 }}>
                  <Chart
                    labels={daily.map(p => p.ts)}
                    series={[
                      { name: '请求数', data: daily.map(p => p.requests) },
                      { name: '花费($)', data: daily.map(p => Number(p.costUsd.toFixed(4))) },
                    ]}
                    h={260}
                    yFmt={v => fmt.k(v)}
                    tipFmt={v => fmt.usd(v)}
                  />
                </Card>
                <Card
                  title="按渠道用量"
                  extra={
                    totalChCharge > 0 ? (
                      <span className="cell-sub" style={{ fontSize: 12.5 }}>
                        本时段 成本 <b className="num">{fmt.usd(totalChCost)}</b> · 营收{' '}
                        <b className="num">{fmt.usd(totalChCharge)}</b> · 毛利{' '}
                        <b className="num" style={{ color: (periodMargin ?? 0) < 0 ? 'var(--err)' : undefined }}>
                          {fmt.pct(periodMargin ?? 0, 1)}
                        </b>
                      </span>
                    ) : null
                  }
                >
                  {byChannel.length ? (
                    <div className="table-wrap">
                      <table className="tb">
                        <thead>
                          <tr>
                            <th>渠道</th>
                            <th className="right">请求</th>
                            <th className="right">成本</th>
                            <th className="right">营收</th>
                            <th className="right">毛利</th>
                            <th className="right">占比</th>
                          </tr>
                        </thead>
                        <tbody>
                          {byChannel.map(r => {
                            const margin =
                              r.costUsd <= 0 ? null
                                : r.chargeUsd <= 0 ? null
                                  : (r.chargeUsd - r.costUsd) / r.chargeUsd;
                            return (
                              <tr key={r.channelName}>
                                <td><b style={{ fontWeight: 500 }}>{r.channelName}</b></td>
                                <td className="right num">{fmt.n(r.requests)}</td>
                                <td className="right num" title={totalChCost ? `占本模型成本 ${((r.costUsd / totalChCost) * 100).toFixed(1)}%` : undefined}>{fmt.usd(r.costUsd)}</td>
                                <td className="right num">
                                  {fmt.usd(r.chargeUsd)}
                                  {r.chargeUsd === 0 && r.costUsd > 0 && <span style={{ color: 'var(--warn)', marginLeft: 4 }} title="该渠道本时段没有计费收入(站主自用流量不计费,或历史数据早于计费改造)">*</span>}
                                </td>
                                <td className="right num" title={margin == null ? '该渠道无成本依据(未绑官方价且手填四价为空),算不出毛利。' : `毛利 = (营收 ${fmt.usd(r.chargeUsd)} − 成本 ${fmt.usd(r.costUsd)}) / 营收`}>
                                  {margin == null ? <span className="muted">—</span> : <span style={{ color: margin < 0 ? 'var(--err)' : undefined }}>{fmt.pct(margin, 1)}</span>}
                                </td>
                                <td className="right">
                                  <span style={{ display: 'inline-flex', alignItems: 'center', justifyContent: 'flex-end', gap: 8, width: 160 }}>
                                    <span className="bar" style={{ flex: 1 }}>
                                      <i style={{ width: totalChReq ? `${((r.requests / totalChReq) * 100).toFixed(1)}%` : '0%' }} />
                                    </span>
                                    <span className="num" style={{ width: 48, textAlign: 'right' }}>
                                      {totalChReq ? ((r.requests / totalChReq) * 100).toFixed(1) : '0.0'}%
                                    </span>
                                  </span>
                                </td>
                              </tr>
                            );
                          })}
                        </tbody>
                      </table>
                    </div>
                  ) : (
                    <Empty title="该时段暂无按渠道用量" >
                      <p>{usageQuery.isFetching ? '加载中…' : '该时段暂无按渠道用量'}</p>
                    </Empty>
                  )}
                </Card>
              </>
            )}
          </>
        )}
      </Drawer>

      <OfferFormModal
        open={offerModal.open}
        modelId={dm?.id ?? 0}
        modelName={dm?.originalName ?? ''}
        editing={offerModal.editing}
        channels={channels}
        usedChannelIds={usedChannelIds}
        onClose={() => setOfferModal({ open: false, editing: null })}
      />
    </>
  );
}
