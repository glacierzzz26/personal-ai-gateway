import { useEffect, useMemo, useRef, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import Chart, { ChartLegend } from '@/components/Chart';
import ProviderMark from '@/components/ProviderMark';
import StatusDot from '@/components/StatusDot';
import OfficialPricePanel from '@/components/models/OfficialPricePanel';
import { useSortableRows } from '@/hooks/useSortableRows';
import { api } from '@/services/api';
import { capabilities as ALL_CAPS } from '@/constants';
import { CAP_LABEL, fmt } from '@/utils/format';
import { channelLabel, channelMark } from '@/utils/channel';
import {
  Button, Card, CardBody, CardHead, Checkbox, Empty, Field, Input, Kpi, Note, Seg, Select, Switch, Table, Textarea,
} from '@/ui/primitives';
import type { Column } from '@/ui/primitives';
import { Drawer, Dropdown, Modal, confirm } from '@/ui/overlay';
import { message } from '@/ui/toast';
import type { Capability, Channel, ModelCatalogItem, ModelDraft, ModelOffer, ModelUsageData, OfferDraft } from '@/types';

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
      <span style={{ color: 'var(--t3)', fontSize: 12.5 }} title="未绑定官方价,且该供给源四价为空 —— 没有成本依据。到「官方定价」页把模型绑定到官方价,或手工填兜底价。">
        未知
      </span>
    );
  }
  const derived = c.source === 'official';
  const title = [
    `输入 ${fmt.price(c.in)} · 输出 ${fmt.price(c.out)}`,
    c.cacheRead ? `缓存读 ${fmt.price(c.cacheRead)}` : '',
    c.cacheWrite ? `缓存写 ${fmt.price(c.cacheWrite)}` : '',
    derived
      ? `官方价 × ${c.ratio}${c.ratio === 1 ? '(未设系数,按 1.0 计 —— 会高估成本)' : `(${c.vendor} 渠道系数)`}`
      : '手填兜底价(未绑定官方价)',
    c.warn ?? '',
  ].filter(Boolean).join('\n');
  return (
    <span className="num" style={{ display: 'inline-flex', flexDirection: 'column', alignItems: 'flex-end' }} title={title}>
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
      <span className="num" style={{ color: 'var(--t3)' }} title="无成本依据,本站价无从对照。左列四价是该供给源手填值。">
        {fmt.price(r.inputPriceUsd)}
      </span>
    );
  }
  // 派生:售价 = 官方价 × rate = 成本 ÷ ratio × rate。
  // 兜底:计费走「成本 × rate」,故直接成本 × rate。
  const retail = c.source === 'official'
    ? c.in * (rate / (c.ratio || 1))
    : c.in * rate;
  return (
    <span className="num" title={c.source === 'official'
      ? `官方价 × 倍率 ${rate}`
      : `手填成本 × 倍率 ${rate}(未绑定官方价,计费走成本×倍率)`}>
      {fmt.price(retail)}
    </span>
  );
}

/** 毛利列 = 1 − 成本/售价。**成本未知时一律不显示** —— 那会伪装成 100% 毛利。 */
function MarginCell({ r, rate }: { r: ModelOffer; rate: number }) {
  const c = r.cost;
  if (!c || c.source === 'unknown') {
    return (
      <span style={{ color: 'var(--t3)', fontSize: 12.5 }} title="没有成本依据,算不出毛利。显示 0 或 100% 都是假的。">—</span>
    );
  }
  const ratio = c.source === 'official' ? (c.ratio || 1) : 1;
  // 官方价在 1 − 成本/售价 里约掉了,于是只剩系数与倍率;兜底口径的等价形式是 1 − 1/rate。
  const m = 1 - ratio / (rate || 1);
  return (
    <span className="num" style={{ color: m < 0 ? 'var(--err)' : undefined }} title={c.source === 'official'
      ? `毛利 = 1 − 系数/倍率 = 1 − ${ratio}/${rate}`
      : `毛利 = 1 − 1/倍率 = 1 − 1/${rate}(成本为手填值,精确度取决于手填价)`}>
      {fmt.pct(m, 1)}
    </span>
  );
}

interface Props {
  model: ModelCatalogItem | null;
  onClose: () => void;
  /** 由调用方提供的删除入口（含确认与后续清理） */
  onDeleteModel: () => void;
}

/* —— 供给源 添加/编辑 弹窗 —— */

interface OfferFormState {
  channelId: string;
  inputPriceUsd: string;
  outputPriceUsd: string;
  cacheReadPriceUsd: string;
  cacheWritePriceUsd: string;
  overridePrice: boolean;
  rateLimitRpm: string;
  enabled: boolean;
  note: string;
  upstreamModel: string;
}

const EMPTY_OFFER: OfferFormState = {
  channelId: '',
  inputPriceUsd: '0',
  outputPriceUsd: '0',
  cacheReadPriceUsd: '0',
  cacheWritePriceUsd: '0',
  overridePrice: false,
  rateLimitRpm: '60',
  enabled: true,
  note: '',
  upstreamModel: '',
};

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
  const isEdit = !!editing;
  const [form, setForm] = useState<OfferFormState>(EMPTY_OFFER);
  const [err, setErr] = useState('');

  useEffect(() => {
    if (!open) return;
    if (editing) {
      setForm({
        channelId: String(editing.channelId),
        inputPriceUsd: String(editing.inputPriceUsd),
        outputPriceUsd: String(editing.outputPriceUsd),
        cacheReadPriceUsd: String(editing.cacheReadPriceUsd ?? 0),
        cacheWritePriceUsd: String(editing.cacheWritePriceUsd ?? 0),
        overridePrice: editing.overridePrice,
        rateLimitRpm: String(editing.rateLimitRpm),
        enabled: editing.enabled,
        note: editing.note ?? '',
        upstreamModel: editing.upstreamModel ?? '',
      });
    } else {
      setForm(EMPTY_OFFER);
    }
    setErr('');
  }, [open, editing]);

  // 后端 (model_id, channel_id) 唯一：新建时排除已挂渠道；编辑时渠道不可更换（更新接口不支持改 channel）
  const candidateChannels = useMemo(() => {
    if (editing) return channels.filter(c => c.id === editing.channelId);
    return channels.filter(c => !usedChannelIds.includes(c.id));
  }, [channels, editing, usedChannelIds]);

  const set = <K extends keyof OfferFormState>(k: K, v: OfferFormState[K]) =>
    setForm(f => ({ ...f, [k]: v }));

  const mutate = useMutation({
    mutationFn: (values: OfferFormState) => {
      const draft: OfferDraft = {
        channelId: editing ? editing.channelId : Number(values.channelId),
        inputPriceUsd: Number(values.inputPriceUsd) || 0,
        outputPriceUsd: Number(values.outputPriceUsd) || 0,
        cacheReadPriceUsd: Number(values.cacheReadPriceUsd) || 0,
        cacheWritePriceUsd: Number(values.cacheWritePriceUsd) || 0,
        overridePrice: values.overridePrice,
        rateLimitRpm: Number(values.rateLimitRpm) || 60,
        enabled: values.enabled,
        note: values.note,
        // 编辑时原样回传来源留证(PATCH 全量替换,漏传会清空官方价来源)
        priceSourceUrl: editing?.priceSourceUrl,
        priceFetchedAt: editing?.priceFetchedAt,
        priceCurrency: editing?.priceCurrency,
        priceNativeText: editing?.priceNativeText,
        upstreamModel: values.upstreamModel.trim(),
      };
      return editing ? api.updateOffer(editing.id, draft) : api.createOffer(modelId, draft);
    },
    onSuccess: () => {
      message.success(isEdit ? '供给源已更新' : '供给源已添加');
      onClose();
      qc.invalidateQueries({ queryKey: ['models'] });
      qc.invalidateQueries({ queryKey: ['channels'] });
    },
    onError: (e) => message.error((e as Error)?.message || (isEdit ? '更新失败' : '添加失败')),
  });

  const submit = () => {
    if (!isEdit && !form.channelId) {
      setErr('请选择渠道');
      return;
    }
    setErr('');
    mutate.mutate(form);
  };

  return (
    <div className="overlay" style={{ zIndex: 80 }}>
      <Modal
        title={isEdit ? '编辑供给源' : '添加供给源'}
        width={580}
        onClose={onClose}
        footer={
          <>
            <Button onClick={onClose}>取消</Button>
            <Button variant="primary" loading={mutate.isPending} onClick={submit}>{isEdit ? '保存' : '添加'}</Button>
          </>
        }
      >
        {isEdit ? (
          <div style={{ marginBottom: 16 }}>
            <Note>
              渠道：<b>{editing.channelName}</b>（挂载后不可更换，如需换渠道请删除后重建）
            </Note>
          </div>
        ) : (
          <Field
            label="渠道"
            error={err}
            hint="已为该模型提供过供给源的渠道不会出现在候选中"
          >
            <Select
              value={form.channelId}
              onChange={e => set('channelId', e.target.value)}
              options={[
                { value: '', label: candidateChannels.length ? '选择渠道' : '该模型可挂载的渠道都已存在供给源' },
                ...candidateChannels.map(c => ({
                  value: String(c.id),
                  label: `${c.name} · ${channelLabel(c)}${c.enabled ? '' : '（渠道已停用）'}`,
                })),
              ]}
            />
          </Field>
        )}

        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
          <Field label="输入价（$/1M tokens）">
            <Input type="number" min={0} step={0.01} value={form.inputPriceUsd} placeholder="0.0000" onChange={e => set('inputPriceUsd', e.target.value)} />
          </Field>
          <Field label="输出价（$/1M tokens）">
            <Input type="number" min={0} step={0.01} value={form.outputPriceUsd} placeholder="0.0000" onChange={e => set('outputPriceUsd', e.target.value)} />
          </Field>
          <Field label="缓存命中读价（$/1M tokens）" hint="留 0 = 无依据">
            <Input type="number" min={0} step={0.01} value={form.cacheReadPriceUsd} placeholder="0.0000" onChange={e => set('cacheReadPriceUsd', e.target.value)} />
          </Field>
          <Field label="缓存写入价（$/1M tokens）" hint="留 0 = 无依据（按输入价计）">
            <Input type="number" min={0} step={0.01} value={form.cacheWritePriceUsd} placeholder="0.0000" onChange={e => set('cacheWritePriceUsd', e.target.value)} />
          </Field>
          <Field label="限流（RPM）">
            <Input type="number" min={1} max={100000} step={10} value={form.rateLimitRpm} onChange={e => set('rateLimitRpm', e.target.value)} />
          </Field>
        </div>

        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
          <Field label="覆盖渠道价">
            <Switch checked={form.overridePrice} onChange={v => set('overridePrice', v)} label={form.overridePrice ? '覆盖' : '默认'} />
          </Field>
          <Field label="启用">
            <Switch checked={form.enabled} onChange={v => set('enabled', v)} label={form.enabled ? '启用' : '停用'} />
          </Field>
        </div>

        <Field label="上游模型名" hint={`留空 = 用模型名「${modelName}」。多渠道供同一对外名、但上游真实名不同时,在此填该渠道的真实名。`}>
          <Input className="mono" value={form.upstreamModel} placeholder={modelName || '如 deepseek/deepseek-v4.1-flash'} onChange={e => set('upstreamModel', e.target.value)} />
        </Field>

        <Field label="备注">
          <Textarea rows={2} value={form.note} placeholder="可选，如定价依据、上游备注等" onChange={e => set('note', e.target.value)} />
        </Field>
      </Modal>
    </div>
  );
}

/* —— 模型抽屉 —— */

interface BasicFormState {
  name: string;
  displayName: string;
  contextWindow: string;
  capabilities: Capability[];
  /** 售价倍率;空 = 跟随全局 settings.priceMultiplier */
  rateOverride: string;
}

export default function ModelDrawer({ model, onClose, onDeleteModel }: Props) {
  const qc = useQueryClient();
  const [tab, setTab] = useState<'overview' | 'offers' | 'price' | 'usage'>('overview');
  const [basic, setBasic] = useState<BasicFormState>({ name: '', displayName: '', contextWindow: '0', capabilities: [], rateOverride: '' });
  const prevModelId = useRef<number | null>(null);
  const [offerModal, setOfferModal] = useState<{ open: boolean; editing: ModelOffer | null }>({ open: false, editing: null });
  /** 「原始模型名(渠道侧)」在供给源已各自指定上游名时默认折叠,可展开编辑 */
  const [showRawName, setShowRawName] = useState(false);

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
  const rate = model?.rateOverride ?? globalRate;
  const usageQuery = useQuery({
    queryKey: ['model-usage', model?.id, 7],
    queryFn: () => api.getModelUsage(model!.id, 7),
    enabled: !!model,
  });

  // 基本信息表单：随打开模型切换回填
  useEffect(() => {
    const id = model?.id ?? null;
    if (id !== prevModelId.current) {
      prevModelId.current = id;
      setShowRawName(false);
      setTab('overview');
      if (model) {
        setBasic({
          name: model.originalName,
          displayName: model.displayName ?? '',
          contextWindow: String(model.contextWindow),
          capabilities: model.capabilities,
          rateOverride: model.rateOverride != null ? String(model.rateOverride) : '',
        });
      }
    }
  }, [model]);

  const offers = model?.offers ?? [];
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
      message.success(`优先级已更新：第 ${target + 1} 位`);
    },
    onError: () => message.error('调整失败，请重试'),
  });

  const { onRow, handleProps } = useSortableRows((from, insertAt) => {
    if (!model) return;
    reorder.mutate({ modelId: model.id, from, insertAt });
  });

  /* —— 模型级：启停 / 保存基本信息 —— */
  const toggleModel = useMutation({
    mutationFn: (v: { id: number; enabled: boolean }) => api.toggleModel(v.id, v.enabled),
    onSuccess: (r) => {
      const skipped = r?.skippedZeroPrice ?? [];
      if (skipped.length > 0) {
        // 联动启用时跳过零价供给源(issue #26)—— 说清是哪几条、为什么。
        message.warning(`模型已启用,但有 ${skipped.length} 条供给源因无成本依据未启用:${skipped.join('、')}`);
      } else {
        message.success('模型状态已更新');
      }
      qc.invalidateQueries({ queryKey: ['models'] });
    },
    onError: () => message.error('状态更新失败'),
  });

  const saveModel = useMutation({
    mutationFn: (v: { id: number; draft: ModelDraft }) => api.updateModel(v.id, v.draft),
    onSuccess: () => {
      message.success('模型信息已保存');
      qc.invalidateQueries({ queryKey: ['models'] });
    },
    onError: (e) => message.error((e as Error)?.message || '保存失败'),
  });

  const onSaveBasic = () => {
    if (!model) return;
    if (!basic.name.trim()) {
      message.error('请输入模型名称');
      return;
    }
    saveModel.mutate({
      id: model.id,
      draft: {
        name: basic.name.trim(),
        displayName: basic.displayName.trim(),
        contextWindow: Number(basic.contextWindow) || model.contextWindow,
        capabilities: basic.capabilities as Capability[],
        enabled: model.enabled,
        // 空 = 清空覆盖(回落全局);null 显式传回让后端区分「清空」与「未传」。
        rateOverride: basic.rateOverride.trim() === '' ? null : Number(basic.rateOverride),
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
      message.error(msg || '供给源启停失败');
    },
  });

  const deleteOffer = useMutation({
    mutationFn: (id: number) => api.deleteOffer(id),
    onSuccess: () => {
      message.success('供给源已删除');
      qc.invalidateQueries({ queryKey: ['models'] });
      qc.invalidateQueries({ queryKey: ['channels'] });
    },
    onError: () => message.error('删除失败'),
  });

  const confirmDeleteOffer = (o: ModelOffer) => {
    void confirm({
      title: '移除供给源',
      content: `确定移除「${model?.name}」来自 ${o.channelName} 的供给源吗？`,
      okText: '移除',
      danger: true,
    }).then(ok => {
      if (ok) deleteOffer.mutateAsync(o.id).catch(() => undefined);
    });
  };

  const setPrimary = useMutation({
    mutationFn: (v: { modelId: number; offerId: number }) => api.setPrimaryOffer(v.modelId, v.offerId),
    onSuccess: () => {
      message.success('已设为首选');
      qc.invalidateQueries({ queryKey: ['models'] });
    },
    onError: () => message.error('设置失败'),
  });

  const lowestIn = offers.filter(o => o.enabled).length
    ? Math.min(...offers.filter(o => o.enabled).map(o => o.inputPriceUsd))
    : null;

  const offerCols: Column<ModelOffer>[] = [
    {
      key: 'drag', title: '', width: 32,
      render: (_r, i) => <span {...handleProps(i)} aria-label="拖动调整优先级">⋮⋮</span>,
    },
    {
      key: 'channel', title: '渠道',
      render: r => <b style={{ fontWeight: 500 }}>{r.channelName}</b>,
    },
    {
      key: 'provider', title: '供应商',
      render: r => (
        <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
          <ProviderMark name={channelMark(r)} /> {channelLabel(r)}
        </span>
      ),
    },
    {
      key: 'upstream', title: '上游名',
      render: r =>
        r.upstreamModel ? (
          <span className="mono" style={{ fontSize: 12.5 }}>{r.upstreamModel}</span>
        ) : (
          <span style={{ color: 'var(--t3)', fontSize: 12.5 }} title="未单独指定,出站用模型名">同模型名</span>
        ),
    },
    {
      key: 'inP', title: '输入价', align: 'right',
      render: r => (
        <span className="num" style={{ color: r.enabled && lowestIn !== null && r.inputPriceUsd === lowestIn ? 'var(--brand)' : undefined }}>
          {fmt.price(r.inputPriceUsd)}
        </span>
      ),
    },
    { key: 'outP', title: '输出价', align: 'right', render: r => <span className="num">{fmt.price(r.outputPriceUsd)}</span> },
    { key: 'cr', title: '缓存读价', align: 'right', render: r => <span className="num">{r.cacheReadPriceUsd ? fmt.price(r.cacheReadPriceUsd) : '—'}</span> },
    { key: 'cw', title: '缓存写价', align: 'right', render: r => <span className="num">{r.cacheWritePriceUsd ? fmt.price(r.cacheWritePriceUsd) : '—'}</span> },
    {
      // 成本与本站价都贴着左边三列(手填报价)读 —— 他们回答的是同一个问题:
      // 「这行数字是手填的还是派生的,本站价定得合不合理」。
      key: 'cost', title: '成本', align: 'right', render: r => <CostCell r={r} />,
    },
    { key: 'retail', title: '本站价', align: 'right', render: r => <RetailCell r={r} rate={rate} /> },
    { key: 'margin', title: '毛利', align: 'right', render: r => <MarginCell r={r} rate={rate} /> },
    { key: 'latency', title: '延迟', align: 'right', render: r => <span className="num">{r.latencyMs ? fmt.ms(r.latencyMs) : '—'}</span> },
    { key: 'ok', title: '成功率', align: 'right', render: r => <span className="num">{fmt.pct(r.successRate, 2)}</span> },
    {
      key: 'src', title: '价格来源',
      render: r =>
        r.priceSourceUrl ? (
          <a
            href={r.priceSourceUrl}
            target="_blank"
            rel="noreferrer"
            style={{ fontSize: 12.5 }}
            title={[`来源:${r.priceSourceUrl}`, `抓取:${fmt.dt(r.priceFetchedAt)}`, r.priceNativeText ? `原文:${r.priceNativeText}` : ''].filter(Boolean).join('\n')}
          >
            官方 {r.priceCurrency} ↗
          </a>
        ) : (
          <span style={{ color: 'var(--t3)', fontSize: 12.5 }}>手工</span>
        ),
    },
    { key: 'status', title: '状态', render: r => <StatusDot status={r.enabled ? r.status : 'disabled'} /> },
    {
      key: 'enabled', title: '启用',
      render: r => {
        // 零价供给源:启用即免费放流量,服务端会拒 —— 前端提前置灰并说明差什么。
        const zero = r.cost?.zeroPrice && !r.enabled;
        const sw = (
          <Switch
            checked={r.enabled}
            disabled={zero || (toggleOffer.isPending && toggleOffer.variables?.offerId === r.id)}
            onChange={next => toggleOffer.mutate({ modelId: r.modelId, offerId: r.id, enabled: next })}
          />
        );
        return zero ? (
          <span
            title={`无成本依据,不能启用:${r.cost?.zeroReason || '官方价与兜底四价都为空'}`}
            style={{ display: 'inline-flex' }}
          >
            {sw}
          </span>
        ) : sw;
      },
    },
    {
      key: 'act', title: '', align: 'right', width: 56,
      render: r => (
        <Dropdown
          ariaLabel="更多操作"
          items={[
            { label: '设为首选', icon: 'route', onClick: () => model && setPrimary.mutate({ modelId: model.id, offerId: r.id }) },
            { label: '编辑', icon: 'sliders', onClick: () => setOfferModal({ open: true, editing: r }) },
            { sep: true },
            { label: '删除', icon: 'trash', danger: true, onClick: () => confirmDeleteOffer(r) },
          ]}
        />
      ),
    },
  ];

  /* —— 价格对比图（手绘 SVG） —— */
  const priceSeries = [
    { name: '输入价', data: offers.map(o => o.inputPriceUsd), color: 'var(--chart-2)', kind: 'bar' as const, fmt: (v: number) => fmt.price(v) },
    { name: '输出价', data: offers.map(o => o.outputPriceUsd), color: 'var(--brand)', kind: 'bar' as const, fmt: (v: number) => fmt.price(v) },
  ];

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
    : (model?.successRate ?? 1);

  const totalChReq = byChannel.reduce((a, b) => a + b.requests, 0);
  const totalChCost = byChannel.reduce((a, b) => a + b.costUsd, 0);
  const totalChCharge = byChannel.reduce((a, b) => a + b.chargeUsd, 0);

  const byChannelCols: Column<ModelUsageData['byChannel'][number]>[] = [
    { key: 'channel', title: '渠道', render: r => <b style={{ fontWeight: 500 }}>{r.channelName}</b> },
    { key: 'requests', title: '请求', align: 'right', render: r => <span className="num">{fmt.n(r.requests)}</span> },
    {
      key: 'cost', title: '成本', align: 'right',
      render: r => <span className="num" title={totalChCost ? `占本模型成本 ${((r.costUsd / totalChCost) * 100).toFixed(1)}%` : undefined}>{fmt.usd(r.costUsd)}</span>,
    },
    {
      // 营收 = 实际向客户收的钱。历史窗口里站主自用的行 charge_usd=0(未计费),
      // 与成本并列时毛利会显得是负的 —— 毛利列对这种情况显式标注「含未计费流量」。
      key: 'charge', title: '营收', align: 'right',
      render: r => (
        <span className="num">
          {fmt.usd(r.chargeUsd)}
          {r.chargeUsd === 0 && r.costUsd > 0 && (
            <span style={{ color: 'var(--warn)', marginLeft: 4 }} title="该渠道本时段没有计费收入(站主自用流量不计费,或历史数据早于计费改造)">*</span>
          )}
        </span>
      ),
    },
    {
      key: 'margin', title: '毛利', align: 'right',
      render: r => {
        if (r.costUsd <= 0) {
          return <span style={{ color: 'var(--t3)' }} title="该渠道无成本依据(未绑官方价且手填四价为空),算不出毛利。">—</span>;
        }
        if (r.chargeUsd <= 0) {
          return <span style={{ color: 'var(--t3)' }} title="无营收,无法算毛利率(成本仍照记)。">—</span>;
        }
        const m = (r.chargeUsd - r.costUsd) / r.chargeUsd;
        return (
          <span className="num" style={{ color: m < 0 ? 'var(--err)' : undefined }} title={`毛利 = (营收 ${fmt.usd(r.chargeUsd)} − 成本 ${fmt.usd(r.costUsd)}) / 营收`}>
            {fmt.pct(m, 1)}
          </span>
        );
      },
    },
    {
      key: 'share', title: '占比', align: 'right',
      render: r => (
        <span style={{ display: 'inline-flex', alignItems: 'center', justifyContent: 'flex-end', gap: 8, width: 160 }}>
          <span className="bar" style={{ flex: 1 }} role="img" aria-label="用量占比">
            <i style={{ width: totalChReq ? `${((r.requests / totalChReq) * 100).toFixed(1)}%` : '0%' }} />
          </span>
          <span className="num" style={{ width: 48, textAlign: 'right' }}>
            {totalChReq ? ((r.requests / totalChReq) * 100).toFixed(1) : '0.0'}%
          </span>
        </span>
      ),
    },
  ];

  /** 本模型本时段的合计:成本 / 营收 / 毛利,与按渠道表同源(求和即得)。 */
  const periodMargin = totalChCharge > 0 ? (totalChCharge - totalChCost) / totalChCharge : null;

  const overviewStats: { label: string; value: string }[] = [
    { label: '今日调用', value: fmt.k(model?.todayRequests ?? 0) },
    { label: '成功率', value: fmt.pct(model?.successRate ?? 1) },
    { label: '可用供给源', value: `${usableOffers}/${offers.length}` },
    { label: '平均延迟', value: enabledOffers.length ? fmt.ms(Math.round(enabledOffers.reduce((a, o) => a + o.latencyMs, 0) / enabledOffers.length)) : '—' },
  ];

  const toggleCap = (c: Capability) =>
    setBasic(b => ({ ...b, capabilities: b.capabilities.includes(c) ? b.capabilities.filter(x => x !== c) : [...b.capabilities, c] }));

  return (
    <>
      {model && (
        <div className="overlay" style={{ zIndex: 70 }}>
          <Drawer
            width={960}
            title={
              <span style={{ display: 'inline-flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
                <span className="mono" style={{ fontSize: 16, fontWeight: 600 }}>{model.name}</span>
                {model.capabilities.map(x => <span className="badge b-slate" key={x}>{CAP_LABEL[x]}</span>)}
                <span style={{ fontSize: 12, color: 'var(--t3)' }}>{fmt.ctx(model.contextWindow)} 上下文</span>
                {model.displayName && (
                  <span className="mono" style={{ fontSize: 12, color: 'var(--t3)' }}>原始名 {model.originalName}</span>
                )}
              </span>
            }
            extra={
              <Switch
                checked={model.enabled}
                disabled={toggleModel.isPending}
                onChange={v => toggleModel.mutate({ id: model.id, enabled: v })}
                label={model.enabled ? '启用' : '停用'}
              />
            }
            footer={
              <>
                <Button variant="danger" icon="trash" onClick={onDeleteModel}>删除模型</Button>
                <div style={{ marginLeft: 'auto', display: 'flex', gap: 6 }}>
                  <Button onClick={handleClose}>取消</Button>
                  <Button variant="primary" loading={saveModel.isPending} onClick={onSaveBasic}>保存更改</Button>
                </div>
              </>
            }
            onClose={handleClose}
          >
            <div style={{ marginBottom: 16 }}>
              <Seg
                value={tab}
                onChange={setTab}
                options={[
                  { value: 'overview' as const, label: '概览' },
                  { value: 'offers' as const, label: offers.length ? `供给源 (${offers.length})` : '供给源' },
                  { value: 'price' as const, label: '价格对比' },
                  { value: 'usage' as const, label: '用量' },
                ]}
              />
            </div>

            {tab === 'overview' && (
              <>
                <div className="kpi-grid" style={{ gridTemplateColumns: 'repeat(4, minmax(0,1fr))', marginBottom: 16 }}>
                  {overviewStats.map(s => <Kpi key={s.label} label={s.label} value={s.value} />)}
                </div>

                <Card>
                  <CardHead title="基本信息" />
                  <CardBody>
                    <div style={{ maxWidth: 540 }}>
                      <Field label="统一名称" hint="网关侧对外展示与调用名，留空则用原始名；重命名不改动渠道侧真实模型名">
                        <Input className="mono" value={basic.displayName} placeholder="如 deepseek-v3（留空=用原始名）" onChange={e => setBasic(b => ({ ...b, displayName: e.target.value }))} />
                      </Field>

                      {allUpstream && !showRawName ? (
                        <div style={{ marginBottom: 18 }}>
                          <div style={{ fontSize: 13.5, color: 'var(--t2)', marginBottom: 4 }}>原始模型名（渠道侧）</div>
                          <div style={{ fontSize: 12, color: 'var(--t3)' }}>
                            已在每个供给源单独指定上游名，出站按命中的供给源改写请求体 model；模型级名称仅用于同步去重。
                            <button type="button" className="card-link" style={{ marginLeft: 6 }} onClick={() => setShowRawName(true)}>展开编辑</button>
                          </div>
                        </div>
                      ) : (
                        <Field
                          label="原始模型名（渠道侧）"
                          hint={
                            allUpstream ? (
                              <>
                                渠道上游真实模型名；修改会改变同步去重与出站请求的模型名，请谨慎操作。
                                <button type="button" className="card-link" style={{ marginLeft: 6 }} onClick={() => setShowRawName(false)}>收起</button>
                              </>
                            ) : (
                              <span style={{ color: 'var(--warn)' }}>
                                渠道上游真实模型名。部分供给源未填上游名，出站将回落此名称 —— 建议到「供给源」补齐。
                              </span>
                            )
                          }
                        >
                          <Input className="mono" value={basic.name} invalid={!basic.name.trim()} onChange={e => setBasic(b => ({ ...b, name: e.target.value }))} />
                        </Field>
                      )}

                      <Field label="上下文窗口（tokens）">
                        <Input type="number" min={0} step={1024} value={basic.contextWindow} onChange={e => setBasic(b => ({ ...b, contextWindow: e.target.value }))} />
                      </Field>

                      <Field label="能力">
                        <div className="hstack wrap" style={{ gap: 10, paddingTop: 2 }}>
                          {ALL_CAPS.map(x => (
                            <Checkbox key={x} checked={basic.capabilities.includes(x)} onChange={() => toggleCap(x)}>{CAP_LABEL[x]}</Checkbox>
                          ))}
                        </div>
                      </Field>

                      <Field
                        label="售价倍率"
                        hint={
                          <>
                            本站价 = 官方价 × 倍率，全站同一模型对所有客户同一价。
                            留空则跟随全局倍率（当前 <b className="num">×{globalRate}</b>）。
                            未录官方价的模型回落「成本 × 倍率」。
                          </>
                        }
                      >
                        <Input type="number" min={0.01} step={0.1} placeholder={`留空=用全局倍率（×${globalRate}）`} value={basic.rateOverride} onChange={e => setBasic(b => ({ ...b, rateOverride: e.target.value }))} />
                      </Field>
                    </div>
                    <div style={{ fontSize: 12, color: 'var(--t3)', marginTop: 4 }}>
                      启停开关在抽屉右上角即时生效；其余修改点击底部「保存更改」。
                    </div>
                  </CardBody>
                </Card>
              </>
            )}

            {tab === 'offers' && (
              <>
                <div style={{ marginBottom: 16 }}>
                  <Note>拖动左侧手柄可调整优先级，请求按此顺序尝试，上游失败自动降级到下一个可用供给源。路由规则的优先级高于此处。</Note>
                </div>
                {offers.length ? (
                  <Table<ModelOffer>
                    columns={offerCols}
                    rows={offers}
                    rowKey={o => o.id}
                    rowProps={(r, i) => onRow(r, i)}
                  />
                ) : (
                  <Empty icon="route" title="暂无供给源" desc="点击下方按钮为该模型挂载渠道。" />
                )}
                <Button
                  style={{ marginTop: 16, width: '100%' }}
                  icon="plus"
                  disabled={!canAddOffer}
                  onClick={() => setOfferModal({ open: true, editing: null })}
                >
                  + 添加供给源
                </Button>
              </>
            )}

            {tab === 'price' && (
              <>
                <div style={{ fontSize: 12, color: 'var(--t3)', marginBottom: 12 }}>
                  单位:每百万 tokens(币种随「系统设置 → 计价币种」)
                </div>
                <Card>
                  <CardHead title="各渠道报价" right={<ChartLegend items={priceSeries.map(s => ({ label: s.name, color: s.color }))} />} />
                  <CardBody>
                    {offers.length ? (
                      <Chart
                        labels={offers.map(o => o.channelName)}
                        height={Math.max(140, offers.length * 42 + 70)}
                        leftFmt={v => fmt.price(v)}
                        series={priceSeries}
                      />
                    ) : (
                      <Empty icon="route" title="暂无供给源" />
                    )}
                  </CardBody>
                </Card>

                <div style={{ marginTop: 20 }}>
                  <Table<ModelOffer>
                    columns={[
                      { key: 'channel', title: '渠道', render: r => <b style={{ fontWeight: 500 }}>{r.channelName}</b> },
                      { key: 'ctx', title: '上下文', align: 'right', render: r => <span className="num">{fmt.ctx(r.contextWindow)}</span> },
                      { key: 'rpm', title: '限流', align: 'right', render: r => <span className="num">{r.rateLimitRpm} RPM</span> },
                      { key: 'inP', title: '输入价', align: 'right', render: r => <span className="num">{fmt.price(r.inputPriceUsd)}</span> },
                      { key: 'outP', title: '输出价', align: 'right', render: r => <span className="num">{fmt.price(r.outputPriceUsd)}</span> },
                      { key: 'cr', title: '缓存读价', align: 'right', render: r => <span className="num">{r.cacheReadPriceUsd ? fmt.price(r.cacheReadPriceUsd) : '—'}</span> },
                      { key: 'cw', title: '缓存写价', align: 'right', render: r => <span className="num">{r.cacheWritePriceUsd ? fmt.price(r.cacheWritePriceUsd) : '—'}</span> },
                      { key: 'note', title: '备注', render: r => <span style={{ color: 'var(--t3)' }}>{r.note || '—'}</span> },
                    ]}
                    rows={offers}
                    rowKey={o => o.id}
                  />
                </div>

                <div style={{ marginTop: 20 }}>
                  <OfficialPricePanel model={model} offers={offers} />
                </div>
              </>
            )}

            {tab === 'usage' && (
              <>
                <div className="kpi-grid" style={{ gridTemplateColumns: 'repeat(4, minmax(0,1fr))', marginBottom: 16 }}>
                  <Kpi label="今日请求" value={fmt.k(lastDay?.requests ?? 0)} />
                  <Kpi label="今日花费" value={fmt.usd(lastDay?.costUsd ?? 0)} />
                  <Kpi label="近 7 日请求" value={fmt.k(periodTotal.requests)} />
                  <Kpi label="近 7 日成功率" value={fmt.pct(periodOkRate, 2)} />
                </div>

                <Card id="usage-trend">
                  <CardHead
                    title="近 7 日趋势"
                    right={<ChartLegend items={[{ label: '请求数', color: 'var(--brand)' }, { label: '花费($)', color: 'var(--warn)' }]} />}
                  />
                  <CardBody>
                    {daily.length ? (
                      <Chart
                        labels={daily.map(p => (p.ts.length > 10 ? p.ts.slice(5) : p.ts))}
                        height={260}
                        leftFmt={fmt.k}
                        rightFmt={v => fmt.usd(v)}
                        series={[
                          { name: '请求数', data: daily.map(p => p.requests), color: 'var(--brand)', kind: 'bar', fmt: v => fmt.n(v) },
                          { name: '花费($)', data: daily.map(p => Number(p.costUsd.toFixed(4))), color: 'var(--warn)', kind: 'line', axis: 'right', fmt: v => fmt.usd(v, 4) },
                        ]}
                      />
                    ) : (
                      <Empty icon="activity" title={usageQuery.isFetching ? '加载中…' : '近 7 日暂无用量'} />
                    )}
                  </CardBody>
                </Card>

                <div style={{ marginTop: 16 }}>
                  <Card>
                    <CardHead
                      title="按渠道用量"
                      right={
                        // 合计行口径与表内一致:有营收时给毛利率,无营收时不给(算出来是 −∞ 或 100%,都没意义)。
                        totalChCharge > 0 ? (
                          <span style={{ fontSize: 12.5, color: 'var(--t3)' }}>
                            本时段 成本 <b className="num">{fmt.usd(totalChCost)}</b> · 营收{' '}
                            <b className="num">{fmt.usd(totalChCharge)}</b> · 毛利{' '}
                            <b className="num" style={{ color: (periodMargin ?? 0) < 0 ? 'var(--err)' : undefined }}>{fmt.pct(periodMargin ?? 0, 1)}</b>
                          </span>
                        ) : undefined
                      }
                    />
                    <CardBody style={{ padding: 0 }}>
                      {byChannel.length ? (
                        <Table columns={byChannelCols} rows={byChannel} rowKey={r => r.channelName} />
                      ) : (
                        <div style={{ padding: 24 }}>
                          <Empty icon="activity" title={usageQuery.isFetching ? '加载中…' : '该时段暂无按渠道用量'} />
                        </div>
                      )}
                    </CardBody>
                  </Card>
                </div>
              </>
            )}
          </Drawer>
        </div>
      )}

      {offerModal.open && model && (
        <OfferFormModal
          open={offerModal.open}
          modelId={model.id}
          modelName={model.originalName}
          editing={offerModal.editing}
          channels={channels}
          usedChannelIds={usedChannelIds}
          onClose={() => setOfferModal({ open: false, editing: null })}
        />
      )}
    </>
  );
}
