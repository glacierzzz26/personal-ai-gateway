import { useEffect, useMemo, useRef, useState } from 'react';
import {
  App, Button, Card, Checkbox, Col, Drawer, Dropdown, Empty, Form, Input, InputNumber,
  Modal, Row, Select, Space, Switch, Table, Tabs, Tooltip,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import type { MenuProps } from 'antd';
import { MoreOutlined, PlusOutlined } from '@ant-design/icons';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import Chart from '@/components/Chart';
import ProviderMark from '@/components/ProviderMark';
import StatusDot from '@/components/StatusDot';
import OfficialPricePanel from '@/components/models/OfficialPricePanel';
import { useSortableRows } from '@/hooks/useSortableRows';
import { useChartColors } from '@/hooks/useChartColors';
import { api } from '@/services/api';
import { capabilities as ALL_CAPS } from '@/constants';
import { CAP_LABEL, fmt } from '@/utils/format';
import { channelLabel, channelMark } from '@/utils/channel';
import type { EChartsOption } from 'echarts';
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
      <Tooltip title="未绑定官方价,且该供给源四价为空 —— 没有成本依据。到「官方定价」页把模型绑定到官方价,或手工填兜底价。">
        <span style={{ color: 'var(--gw-text-3)', fontSize: 12.5 }}>未知</span>
      </Tooltip>
    );
  }
  const derived = c.source === 'official';
  return (
    <Tooltip
      title={
        <div style={{ fontSize: 12, lineHeight: 1.8 }}>
          <div>
            输入 {fmt.price(c.in)} · 输出 {fmt.price(c.out)}
            {c.cacheRead ? ` · 缓存读 ${fmt.price(c.cacheRead)}` : ''}
            {c.cacheWrite ? ` · 缓存写 ${fmt.price(c.cacheWrite)}` : ''}
          </div>
          {derived
            ? <div>官方价 × {c.ratio}{c.ratio === 1 ? '(未设系数,按 1.0 计 —— 会高估成本)' : `(${c.vendor} 渠道系数)`}</div>
            : <div>手填兜底价(未绑定官方价)</div>}
          {c.warn && <div style={{ color: 'var(--gw-warn)' }}>{c.warn}</div>}
        </div>
      }
    >
      <span className="gw-num" style={{ display: 'inline-flex', flexDirection: 'column', alignItems: 'flex-end' }}>
        <span>{fmt.price(c.in)}</span>
        <span style={{ fontSize: 11, color: derived && c.ratio === 1 ? 'var(--gw-warn)' : 'var(--gw-text-3)' }}>
          {derived ? (c.ratio === 1 ? '系数未设' : `官方×${c.ratio}`) : '手填'}
        </span>
      </span>
    </Tooltip>
  );
}

/** 本站价列 = 官方价 × 倍率(与计费同源)。 */
function RetailCell({ r, rate }: { r: ModelOffer; rate: number }) {
  const c = r.cost;
  if (!c || c.source === 'unknown') {
    return (
      <Tooltip title="无成本依据,本站价无从对照。左列四价是该供给源手填值。">
        <span className="gw-num" style={{ color: 'var(--gw-text-3)' }}>{fmt.price(r.inputPriceUsd)}</span>
      </Tooltip>
    );
  }
  // 派生:售价 = 官方价 × rate = 成本 ÷ ratio × rate。
  // 兜底:计费走「成本 × rate」,故直接成本 × rate。
  const retail = c.source === 'official'
    ? c.in * (rate / (c.ratio || 1))
    : c.in * rate;
  return (
    <Tooltip title={c.source === 'official'
      ? `官方价 × 倍率 ${rate}`
      : `手填成本 × 倍率 ${rate}(未绑定官方价,计费走成本×倍率)`}>
      <span className="gw-num">{fmt.price(retail)}</span>
    </Tooltip>
  );
}

/** 毛利列 = 1 − 成本/售价。**成本未知时一律不显示** —— 那会伪装成 100% 毛利。 */
function MarginCell({ r, rate }: { r: ModelOffer; rate: number }) {
  const c = r.cost;
  if (!c || c.source === 'unknown') {
    return (
      <Tooltip title="没有成本依据,算不出毛利。显示 0 或 100% 都是假的。">
        <span style={{ color: 'var(--gw-text-3)', fontSize: 12.5 }}>—</span>
      </Tooltip>
    );
  }
  const ratio = c.source === 'official' ? (c.ratio || 1) : 1;
  // 官方价在 1 − 成本/售价 里约掉了,于是只剩系数与倍率;兜底口径的等价形式是 1 − 1/rate。
  const m = 1 - ratio / (rate || 1);
  return (
    <Tooltip title={c.source === 'official'
      ? `毛利 = 1 − 系数/倍率 = 1 − ${ratio}/${rate}`
      : `毛利 = 1 − 1/倍率 = 1 − 1/${rate}(成本为手填值,精确度取决于手填价)`}>
      <span className="gw-num" style={{ color: m < 0 ? 'var(--gw-err)' : undefined }}>{fmt.pct(m, 1)}</span>
    </Tooltip>
  );
}

interface Props {
  model: ModelCatalogItem | null;
  onClose: () => void;
  /** 由调用方提供的删除入口（含确认与后续清理） */
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
  const { message } = App.useApp();
  const qc = useQueryClient();
  const [form] = Form.useForm<OfferFormValues>();
  const isEdit = !!editing;

  useEffect(() => {
    if (!open) return;
    if (editing) {
      form.setFieldsValue({
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
      form.resetFields();
    }
  }, [open, editing, form]);

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
      message.success(isEdit ? '供给源已更新' : '供给源已添加');
      onClose();
      qc.invalidateQueries({ queryKey: ['models'] });
      qc.invalidateQueries({ queryKey: ['channels'] });
    },
    onError: (e) => message.error((e as Error)?.message || (isEdit ? '更新失败' : '添加失败')),
  });

  const submit = async () => {
    const values = await form.validateFields().catch(() => null);
    if (!values) return;
    mutate.mutate(values);
  };

  return (
    <Modal
      title={isEdit ? '编辑供给源' : '添加供给源'}
      open={open}
      onCancel={onClose}
      onOk={submit}
      confirmLoading={mutate.isPending}
      okText={isEdit ? '保存' : '添加'}
      cancelText="取消"
      destroyOnHidden
      width={580}
    >
      <Form
        form={form}
        layout="vertical"
        initialValues={{
          inputPriceUsd: 0,
          outputPriceUsd: 0,
          cacheReadPriceUsd: 0,
          cacheWritePriceUsd: 0,
          overridePrice: false,
          rateLimitRpm: 60,
          enabled: true,
        }}
      >
        {isEdit ? (
          <div
            style={{
              border: '1px solid var(--gw-border)', borderLeft: '2px solid var(--gw-primary)',
              borderRadius: 'var(--gw-r-card)', padding: '8px 10px', fontSize: 13, marginBottom: 16,
              color: 'var(--gw-text-2)', background: 'var(--gw-bg)',
            }}
          >
            渠道：<b>{editing.channelName}</b>（挂载后不可更换，如需换渠道请删除后重建）
          </div>
        ) : (
          <Form.Item
            name="channelId"
            label="渠道"
            rules={[{ required: true, message: '请选择渠道' }]}
            extra="已为该模型提供过供给源的渠道不会出现在候选中"
          >
            <Select
              placeholder="选择渠道"
              options={candidateChannels.map(c => ({
                value: c.id,
                label: `${c.name} · ${channelLabel(c)}${c.enabled ? '' : '（渠道已停用）'}`,
              }))}
              showSearch
              optionFilterProp="label"
              notFoundContent="该模型可挂载的渠道都已存在供给源"
            />
          </Form.Item>
        )}

        <Row gutter={16}>
          <Col span={12}>
            <Form.Item
              name="inputPriceUsd"
              label="输入价（$/1M tokens）"
              rules={[{ required: true, message: '请输入输入价' }]}
            >
              <InputNumber min={0} precision={4} step={0.01} style={{ width: '100%' }} placeholder="0.0000" />
            </Form.Item>
          </Col>
          <Col span={12}>
            <Form.Item
              name="outputPriceUsd"
              label="输出价（$/1M tokens）"
              rules={[{ required: true, message: '请输入输出价' }]}
            >
              <InputNumber min={0} precision={4} step={0.01} style={{ width: '100%' }} placeholder="0.0000" />
            </Form.Item>
          </Col>
        </Row>
        <Row gutter={16}>
          <Col span={12}>
            <Form.Item
              name="cacheReadPriceUsd"
              label="缓存命中读价（$/1M tokens）"
              extra="留 0 = 无依据"
            >
              <InputNumber min={0} precision={4} step={0.01} style={{ width: '100%' }} placeholder="0.0000" />
            </Form.Item>
          </Col>
          <Col span={12}>
            <Form.Item
              name="cacheWritePriceUsd"
              label="缓存写入价（$/1M tokens）"
              extra="留 0 = 无依据（按输入价计）"
            >
              <InputNumber min={0} precision={4} step={0.01} style={{ width: '100%' }} placeholder="0.0000" />
            </Form.Item>
          </Col>
        </Row>
        <Row gutter={16}>
          <Col span={12}>
            <Form.Item name="rateLimitRpm" label="限流（RPM）">
              <InputNumber min={1} max={100000} step={10} style={{ width: '100%' }} />
            </Form.Item>
          </Col>
        </Row>

        <Row gutter={16}>
          <Col span={12}>
            <Form.Item
              name="overridePrice"
              label="覆盖渠道价"
              valuePropName="checked"
              tooltip="标记该供给源按本处填写的报价计费，而不是参考渠道侧的默认价"
            >
              <Switch />
            </Form.Item>
          </Col>
          <Col span={12}>
            <Form.Item name="enabled" label="启用" valuePropName="checked">
              <Switch checkedChildren="启用" unCheckedChildren="停用" />
            </Form.Item>
          </Col>
        </Row>

        <Form.Item
          name="upstreamModel"
          label="上游模型名"
          extra={`留空 = 用模型名「${modelName}」。多渠道供同一对外名、但上游真实名不同时,在此填该渠道的真实名。`}
        >
          <Input className="gw-mono" placeholder={modelName || '如 deepseek/deepseek-v4.1-flash'} />
        </Form.Item>

        <Form.Item name="note" label="备注">
          <Input.TextArea rows={2} placeholder="可选，如定价依据、上游备注等" />
        </Form.Item>
      </Form>
    </Modal>
  );
}

/* —— 模型抽屉 —— */

export default function ModelDrawer({ model, onClose, onDeleteModel }: Props) {
  const { message, modal } = App.useApp();
  const c = useChartColors();
  const qc = useQueryClient();
  const [tab, setTab] = useState('overview');
  const [form] = Form.useForm<BasicFormValues>();
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
      if (model) {
        form.setFieldsValue({
          name: model.originalName,
          displayName: model.displayName ?? '',
          contextWindow: model.contextWindow,
          capabilities: model.capabilities,
          rateOverride: model.rateOverride ?? null,
        });
      }
    }
  }, [model, form]);

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

  const onSaveBasic = async () => {
    if (!model) return;
    const v = await form.validateFields().catch(() => null);
    if (!v) return;
    saveModel.mutate({
      id: model.id,
      draft: {
        name: (v.name ?? model.originalName).trim(),
        displayName: (v.displayName ?? '').trim(),
        contextWindow: v.contextWindow ?? model.contextWindow,
        capabilities: (v.capabilities ?? model.capabilities) as Capability[],
        enabled: model.enabled,
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
    modal.confirm({
      title: '移除供给源',
      content: `确定移除「${model?.name}」来自 ${o.channelName} 的供给源吗？`,
      okText: '移除',
      okButtonProps: { danger: true },
      cancelText: '取消',
      onOk: () => deleteOffer.mutateAsync(o.id).catch(() => undefined),
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

  const offerMenu = (o: ModelOffer): MenuProps => ({
    items: [
      { key: 'primary', label: '设为首选', disabled: o.priority <= 1 },
      { key: 'edit', label: '编辑' },
      { type: 'divider' },
      { key: 'delete', label: '删除', danger: true },
    ],
    onClick: ({ key, domEvent }) => {
      domEvent.stopPropagation();
      if (!model) return;
      if (key === 'primary') setPrimary.mutate({ modelId: model.id, offerId: o.id });
      else if (key === 'edit') setOfferModal({ open: true, editing: o });
      else if (key === 'delete') confirmDeleteOffer(o);
    },
  });

  const lowestIn = offers.filter(o => o.enabled).length
    ? Math.min(...offers.filter(o => o.enabled).map(o => o.inputPriceUsd))
    : null;

  const offerCols: ColumnsType<ModelOffer> = [
    {
      title: '', width: 32,
      render: (_, __, i) => (
        <span {...handleProps(i ?? 0)} aria-label="拖动调整优先级">⋮⋮</span>
      ),
    },
    {
      title: '渠道', dataIndex: 'channelName',
      render: v => <b style={{ fontWeight: 500 }}>{v}</b>,
    },
    {
      title: '供应商', dataIndex: 'provider',
      render: (_, r) => {
        const label = channelLabel(r);
        return (
          <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
            <ProviderMark name={channelMark(r)} /> {label}
          </span>
        );
      },
    },
    {
      title: '上游名', key: 'upstream',
      render: (_, r) =>
        r.upstreamModel ? (
          <span className="gw-mono" style={{ fontSize: 12.5 }}>{r.upstreamModel}</span>
        ) : (
          <Tooltip title="未单独指定,出站用模型名">
            <span style={{ color: 'var(--gw-text-3)', fontSize: 12.5 }}>同模型名</span>
          </Tooltip>
        ),
    },
    {
      title: '输入价', dataIndex: 'inputPriceUsd', align: 'right',
      render: (v, r) => (
        <span className="gw-num" style={{ color: r.enabled && lowestIn !== null && v === lowestIn ? 'var(--gw-primary)' : undefined }}>
          {fmt.price(v)}
        </span>
      ),
    },
    {
      title: '输出价', dataIndex: 'outputPriceUsd', align: 'right',
      render: v => <span className="gw-num">{fmt.price(v)}</span>,
    },
    {
      title: '缓存读价', dataIndex: 'cacheReadPriceUsd', align: 'right',
      render: v => <span className="gw-num">{v ? fmt.price(v) : '—'}</span>,
    },
    {
      title: '缓存写价', dataIndex: 'cacheWritePriceUsd', align: 'right',
      render: v => <span className="gw-num">{v ? fmt.price(v) : '—'}</span>,
    },
    {
      // 成本与本站价都贴着左边三列(手填报价)读 —— 他们回答的是同一个问题:
      // 「这行数字是手填的还是派生的,本站价定得合不合理」。
      title: '成本', key: 'cost', align: 'right',
      render: (_, r) => <CostCell r={r} />,
    },
    {
      title: '本站价', key: 'retail', align: 'right',
      render: (_, r) => <RetailCell r={r} rate={rate} />,
    },
    {
      title: '毛利', key: 'margin', align: 'right',
      render: (_, r) => <MarginCell r={r} rate={rate} />,
    },
    {
      title: '延迟', dataIndex: 'latencyMs', align: 'right',
      render: v => <span className="gw-num">{v ? fmt.ms(v) : '—'}</span>,
    },
    {
      title: '成功率', dataIndex: 'successRate', align: 'right',
      render: v => <span className="gw-num">{fmt.pct(v, 2)}</span>,
    },
    {
      title: '价格来源', key: 'src',
      render: (_, r) =>
        r.priceSourceUrl ? (
          <Tooltip
            title={
              <div style={{ fontSize: 12, lineHeight: 1.7 }}>
                <div>来源:{r.priceSourceUrl}</div>
                <div>抓取:{fmt.dt(r.priceFetchedAt)}</div>
                {r.priceNativeText && <div>原文:{r.priceNativeText}</div>}
              </div>
            }
          >
            <a href={r.priceSourceUrl} target="_blank" rel="noreferrer" style={{ fontSize: 12.5 }}>
              官方 {r.priceCurrency} ↗
            </a>
          </Tooltip>
        ) : (
          <span style={{ color: 'var(--gw-text-3)', fontSize: 12.5 }}>手工</span>
        ),
    },
    {
      title: '状态', dataIndex: 'status',
      render: (_, r) => <StatusDot status={r.enabled ? r.status : 'disabled'} />,
    },
    {
      title: '启用', dataIndex: 'enabled', align: 'center',
      render: (v, r) => {
        // 零价供给源:启用即免费放流量,服务端会拒 —— 前端提前置灰并说明差什么。
        const zero = r.cost?.zeroPrice && !v;
        const sw = (
          <Switch
            size="small"
            checked={v}
            disabled={zero}
            loading={toggleOffer.isPending && toggleOffer.variables?.offerId === r.id}
            onChange={next => toggleOffer.mutate({ modelId: r.modelId, offerId: r.id, enabled: next })}
          />
        );
        return zero ? (
          <Tooltip title={`无成本依据,不能启用:${r.cost?.zeroReason || '官方价与兜底四价都为空'}`}>
            <span>{sw}</span>
          </Tooltip>
        ) : sw;
      },
    },
    {
      title: '', align: 'right', width: 56,
      render: (_, r) => (
        <Dropdown menu={offerMenu(r)} trigger={['click']}>
          <Button type="text" size="small" icon={<MoreOutlined />} aria-label="更多操作" />
        </Dropdown>
      ),
    },
  ];

  const priceOption: EChartsOption = {
    grid: { left: 120, right: 70, top: 34, bottom: 8 },
    tooltip: {
      trigger: 'axis', axisPointer: { type: 'shadow' },
      backgroundColor: c.tooltipBg, borderColor: c.tooltipBorder,
      textStyle: { color: c.text, fontSize: 12 },
    },
    legend: {
      data: ['输入价', '输出价'], right: 0, top: 0,
      itemWidth: 8, itemHeight: 8, textStyle: { color: c.text, fontSize: 12 },
    },
    xAxis: {
      type: 'value', splitLine: { lineStyle: { color: c.line } },
      axisLabel: { color: c.text, fontSize: 11 },
    },
    yAxis: {
      type: 'category', data: offers.map(o => o.channelName),
      axisLine: { lineStyle: { color: c.line } }, axisTick: { show: false },
      axisLabel: { color: c.text, fontSize: 12 },
    },
    series: [
      {
        name: '输入价', type: 'bar', data: offers.map(o => o.inputPriceUsd),
        itemStyle: { color: c.primarySoft, borderRadius: 2 }, barGap: '20%', barCategoryGap: '45%',
      },
      {
        name: '输出价', type: 'bar', data: offers.map(o => o.outputPriceUsd),
        itemStyle: { color: c.primary, borderRadius: 2 },
      },
    ],
  };

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

  const dailyOption: EChartsOption = {
    grid: { left: 48, right: 56, top: 34, bottom: 26 },
    tooltip: {
      trigger: 'axis',
      backgroundColor: c.tooltipBg, borderColor: c.tooltipBorder,
      textStyle: { color: c.text, fontSize: 12 },
    },
    legend: {
      data: ['请求数', '花费($)'], right: 0, top: 0,
      itemWidth: 8, itemHeight: 8, textStyle: { color: c.text, fontSize: 12 },
    },
    xAxis: {
      type: 'category', data: daily.map(p => p.ts),
      axisLine: { lineStyle: { color: c.line } }, axisTick: { show: false },
      axisLabel: { color: c.text, fontSize: 11 },
    },
    yAxis: [
      {
        type: 'value', splitLine: { lineStyle: { color: c.line } },
        axisLabel: { color: c.text, fontSize: 11 },
      },
      {
        type: 'value', splitLine: { show: false },
        axisLabel: { color: c.text, fontSize: 11 },
      },
    ],
    series: [
      {
        name: '请求数', type: 'bar', data: daily.map(p => p.requests),
        itemStyle: { color: c.primary, borderRadius: 3 }, barWidth: '45%',
      },
      {
        name: '花费($)', type: 'line', yAxisIndex: 1,
        data: daily.map(p => Number(p.costUsd.toFixed(4))),
        itemStyle: { color: c.warn }, lineStyle: { width: 2 },
      },
    ],
  };

  const totalChReq = byChannel.reduce((a, b) => a + b.requests, 0);
  const totalChCost = byChannel.reduce((a, b) => a + b.costUsd, 0);
  const totalChCharge = byChannel.reduce((a, b) => a + b.chargeUsd, 0);

  const byChannelCols: ColumnsType<ModelUsageData['byChannel'][number]> = [
    {
      title: '渠道', dataIndex: 'channelName',
      render: v => <b style={{ fontWeight: 500 }}>{v}</b>,
    },
    {
      title: '请求', dataIndex: 'requests', align: 'right',
      render: v => <span className="gw-num">{fmt.n(v)}</span>,
    },
    {
      title: '成本', dataIndex: 'costUsd', align: 'right',
      render: v => (
        <Tooltip title={totalChCost ? `占本模型成本 ${((v / totalChCost) * 100).toFixed(1)}%` : undefined}>
          <span className="gw-num">{fmt.usd(v)}</span>
        </Tooltip>
      ),
    },
    {
      // 营收 = 实际向客户收的钱。历史窗口里站主自用的行 charge_usd=0(未计费),
      // 与成本并列时毛利会显得是负的 —— 毛利列对这种情况显式标注「含未计费流量」。
      title: '营收', dataIndex: 'chargeUsd', align: 'right',
      render: (v, r) => (
        <span className="gw-num">
          {fmt.usd(v)}
          {v === 0 && r.costUsd > 0 && (
            <Tooltip title="该渠道本时段没有计费收入(站主自用流量不计费,或历史数据早于计费改造)">
              <span style={{ color: 'var(--gw-warn)', marginLeft: 4 }}>*</span>
            </Tooltip>
          )}
        </span>
      ),
    },
    {
      title: '毛利', key: 'margin', align: 'right',
      render: (_, r) => {
        if (r.costUsd <= 0) {
          return <Tooltip title="该渠道无成本依据(未绑官方价且手填四价为空),算不出毛利。"><span style={{ color: 'var(--gw-text-3)' }}>—</span></Tooltip>;
        }
        const m = (r.chargeUsd - r.costUsd) / r.chargeUsd;
        if (r.chargeUsd <= 0) {
          return <Tooltip title="无营收,无法算毛利率(成本仍照记)。"><span style={{ color: 'var(--gw-text-3)' }}>—</span></Tooltip>;
        }
        return (
          <Tooltip title={`毛利 = (营收 ${fmt.usd(r.chargeUsd)} − 成本 ${fmt.usd(r.costUsd)}) / 营收`}>
            <span className="gw-num" style={{ color: m < 0 ? 'var(--gw-err)' : undefined }}>{fmt.pct(m, 1)}</span>
          </Tooltip>
        );
      },
    },
    {
      title: '占比', key: 'share', align: 'right',
      render: (_, r) => (
        <span style={{ display: 'inline-flex', alignItems: 'center', justifyContent: 'flex-end', gap: 8, width: 160 }}>
          <span className="gw-bar" style={{ flex: 1 }} role="img" aria-label="用量占比">
            <span
              style={{
                display: 'block', height: '100%', borderRadius: 2,
                background: 'var(--gw-primary)',
                width: totalChReq ? `${((r.requests / totalChReq) * 100).toFixed(1)}%` : '0%',
              }}
            />
          </span>
          <span className="gw-num" style={{ width: 48, textAlign: 'right' }}>
            {totalChReq ? ((r.requests / totalChReq) * 100).toFixed(1) : '0.0'}%
          </span>
        </span>
      ),
    },
  ];

  /** 本模型本时段的合计:成本 / 营收 / 毛利,与按渠道表同源(求和即得)。 */
  const periodMargin = totalChCharge > 0 ? (totalChCharge - totalChCost) / totalChCharge : null;

  const overviewStats: [string, string][] = [
    ['今日调用', fmt.k(model?.todayRequests ?? 0)],
    ['成功率', fmt.pct(model?.successRate ?? 1)],
    ['可用供给源', `${usableOffers}/${offers.length}`],
    ['平均延迟', enabledOffers.length ? fmt.ms(Math.round(enabledOffers.reduce((a, o) => a + o.latencyMs, 0) / enabledOffers.length)) : '—'],
  ];

  return (
    <>
      <Drawer
        width={960}
        open={!!model}
        onClose={handleClose}
        destroyOnClose
        title={
          model ? (
            <div>
              <span className="gw-mono" style={{ fontSize: 16, fontWeight: 600 }}>{model.name}</span>
              <span style={{ marginLeft: 12 }}>
                {model.capabilities.map(x => <span className="gw-badge" key={x}>{CAP_LABEL[x]}</span>)}
              </span>
              <span style={{ fontSize: 12, color: 'var(--gw-text-3)', marginLeft: 8 }}>
                {fmt.ctx(model.contextWindow)} 上下文
              </span>
              {model.displayName && (
                <span className="gw-mono" style={{ fontSize: 12, color: 'var(--gw-text-3)', marginLeft: 8 }}>
                  原始名 {model.originalName}
                </span>
              )}
            </div>
          ) : null
        }
        extra={
          model ? (
            <Switch
              checked={model.enabled}
              loading={toggleModel.isPending}
              onChange={v => toggleModel.mutate({ id: model.id, enabled: v })}
              checkedChildren="启用"
              unCheckedChildren="停用"
            />
          ) : null
        }
        footer={
          model ? (
            <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
              <Button danger onClick={onDeleteModel}>删除模型</Button>
              <div style={{ marginLeft: 'auto' }}>
                <Space>
                  <Button onClick={handleClose}>取消</Button>
                  <Button type="primary" loading={saveModel.isPending} onClick={onSaveBasic}>保存更改</Button>
                </Space>
              </div>
            </div>
          ) : null
        }
      >
        {model && (
          <Tabs
            activeKey={tab}
            onChange={setTab}
            items={[
              {
                key: 'overview',
                label: '概览',
                children: (
                  <>
                    <Row gutter={12} style={{ marginBottom: 16 }}>
                      {overviewStats.map(([label, value]) => (
                        <Col span={6} key={label}>
                          <Card>
                            <div style={{ fontSize: 13, color: 'var(--gw-text-3)' }}>{label}</div>
                            <div className="gw-num" style={{ fontSize: 20, fontWeight: 600, marginTop: 6 }}>{value}</div>
                          </Card>
                        </Col>
                      ))}
                    </Row>
                    <Card title="基本信息">
                      <Form form={form} layout="vertical" style={{ maxWidth: 540 }}>
                        <Form.Item
                          name="displayName"
                          label="统一名称"
                          extra="网关侧对外展示与调用名，留空则用原始名；重命名不改动渠道侧真实模型名"
                        >
                          <Input className="gw-mono" placeholder="如 deepseek-v3（留空=用原始名）" />
                        </Form.Item>
                        {allUpstream && !showRawName ? (
                          <div style={{ marginBottom: 24 }}>
                            <div style={{ fontSize: 14, color: 'var(--gw-text-2)', marginBottom: 4 }}>
                              原始模型名（渠道侧）
                            </div>
                            <div style={{ fontSize: 12, color: 'var(--gw-text-3)' }}>
                              已在每个供给源单独指定上游名，出站按命中的供给源改写请求体 model；模型级名称仅用于同步去重。
                              <button
                                type="button"
                                className="gw-link"
                                style={{ marginLeft: 6 }}
                                onClick={() => setShowRawName(true)}
                              >
                                展开编辑
                              </button>
                            </div>
                          </div>
                        ) : (
                          <Form.Item
                            name="name"
                            label="原始模型名（渠道侧）"
                            rules={[{ required: true, whitespace: true, message: '请输入模型名称' }]}
                            extra={
                              allUpstream ? (
                                <>
                                  渠道上游真实模型名；修改会改变同步去重与出站请求的模型名，请谨慎操作。
                                  <button
                                    type="button"
                                    className="gw-link"
                                    style={{ marginLeft: 6 }}
                                    onClick={() => setShowRawName(false)}
                                  >
                                    收起
                                  </button>
                                </>
                              ) : (
                                <span style={{ color: 'var(--gw-warn)' }}>
                                  渠道上游真实模型名。部分供给源未填上游名，出站将回落此名称 ——
                                  建议到「供给源」补齐。
                                </span>
                              )
                            }
                          >
                            <Input className="gw-mono" />
                          </Form.Item>
                        )}
                        <Form.Item
                          name="contextWindow"
                          label="上下文窗口（tokens）"
                          rules={[{ required: true, message: '请输入上下文窗口' }]}
                        >
                          <InputNumber min={0} step={1024} style={{ width: '100%' }} />
                        </Form.Item>
                        <Form.Item name="capabilities" label="能力">
                          <Checkbox.Group options={ALL_CAPS.map(x => ({ label: CAP_LABEL[x], value: x }))} />
                        </Form.Item>
                        <Form.Item
                          name="rateOverride"
                          label="售价倍率"
                          extra={
                            <>
                              本站价 = 官方价 × 倍率，全站同一模型对所有客户同一价。
                              留空则跟随全局倍率（当前 <b className="gw-num">×{globalRate}</b>）。
                              未录官方价的模型回落「成本 × 倍率」。
                            </>
                          }
                        >
                          <InputNumber
                            style={{ width: '100%' }} min={0.01} step={0.1} precision={2}
                            placeholder={`留空=用全局倍率（×${globalRate}）`}
                          />
                        </Form.Item>
                      </Form>
                      <div style={{ fontSize: 12, color: 'var(--gw-text-3)' }}>
                        启停开关在抽屉右上角即时生效；其余修改点击底部「保存更改」。
                      </div>
                    </Card>
                  </>
                ),
              },
              {
                key: 'offers',
                label: `供给源${offers.length ? ` (${offers.length})` : ''}`,
                children: (
                  <>
                    <div
                      style={{
                        border: '1px solid var(--gw-border)', borderLeft: '2px solid var(--gw-primary)',
                        borderRadius: 'var(--gw-r-card)', padding: '10px 12px', fontSize: 13,
                        color: 'var(--gw-text-2)', background: 'var(--gw-bg)', marginBottom: 16,
                      }}
                    >
                      拖动左侧手柄可调整优先级，请求按此顺序尝试，上游失败自动降级到下一个可用供给源。路由规则的优先级高于此处。
                    </div>
                    <Table<ModelOffer>
                      rowKey="id"
                      size="middle"
                      dataSource={offers}
                      columns={offerCols}
                      pagination={false}
                      onRow={onRow}
                      locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无供给源，点击下方添加" /> }}
                    />
                    <Button
                      type="dashed"
                      block
                      style={{ marginTop: 16 }}
                      icon={<PlusOutlined />}
                      disabled={!canAddOffer}
                      onClick={() => setOfferModal({ open: true, editing: null })}
                    >
                      + 添加供给源
                    </Button>
                  </>
                ),
              },
              {
                key: 'price',
                label: '价格对比',
                children: (
                  <>
                    <div style={{ fontSize: 12, color: 'var(--gw-text-3)', marginBottom: 12 }}>
                      单位:每百万 tokens(币种随「系统设置 → 计价币种」)
                    </div>
                    <Chart option={priceOption} height={Math.max(120, offers.length * 42 + 70)} />
                    <Table<ModelOffer>
                      rowKey="id"
                      size="middle"
                      style={{ marginTop: 20 }}
                      dataSource={offers}
                      pagination={false}
                      columns={[
                        { title: '渠道', dataIndex: 'channelName', render: v => <b style={{ fontWeight: 500 }}>{v}</b> },
                        {
                          title: '上下文', align: 'right',
                          render: (_, r) => <span className="gw-num">{fmt.ctx(r.contextWindow)}</span>,
                        },
                        {
                          title: '限流', align: 'right',
                          render: (_, r) => <span className="gw-num">{r.rateLimitRpm} RPM</span>,
                        },
                        {
                          title: '输入价', align: 'right',
                          render: (_, r) => <span className="gw-num">{fmt.price(r.inputPriceUsd)}</span>,
                        },
                        {
                          title: '输出价', align: 'right',
                          render: (_, r) => <span className="gw-num">{fmt.price(r.outputPriceUsd)}</span>,
                        },
                        {
                          title: '缓存读价', align: 'right',
                          render: (_, r) => <span className="gw-num">{r.cacheReadPriceUsd ? fmt.price(r.cacheReadPriceUsd) : '—'}</span>,
                        },
                        {
                          title: '缓存写价', align: 'right',
                          render: (_, r) => <span className="gw-num">{r.cacheWritePriceUsd ? fmt.price(r.cacheWritePriceUsd) : '—'}</span>,
                        },
                        {
                          title: '备注', dataIndex: 'note',
                          render: v => <span style={{ color: 'var(--gw-text-3)' }}>{v || '—'}</span>,
                        },
                      ]}
                    />
                    <OfficialPricePanel model={model} offers={offers} />
                  </>
                ),
              },
              {
                key: 'usage',
                label: '用量',
                children: (
                  <>
                    <Row gutter={12} style={{ marginBottom: 16 }}>
                      {[
                        ['今日请求', fmt.k(lastDay?.requests ?? 0)],
                        ['今日花费', fmt.usd(lastDay?.costUsd ?? 0)],
                        ['近 7 日请求', fmt.k(periodTotal.requests)],
                        ['近 7 日成功率', fmt.pct(periodOkRate, 2)],
                      ].map(([label, value]) => (
                        <Col span={6} key={label}>
                          <Card>
                            <div style={{ fontSize: 13, color: 'var(--gw-text-3)' }}>{label}</div>
                            <div className="gw-num" style={{ fontSize: 20, fontWeight: 600, marginTop: 6 }}>{value}</div>
                          </Card>
                        </Col>
                      ))}
                    </Row>
                    <Card title="近 7 日趋势" style={{ marginBottom: 16 }}>
                      <Chart option={dailyOption} height={260} />
                    </Card>
                    <Card
                      title="按渠道用量"
                      extra={
                        // 合计行口径与表内一致:有营收时给毛利率,无营收时不给(算出来是 −∞ 或 100%,都没意义)。
                        totalChCharge > 0 ? (
                          <span style={{ fontSize: 12.5, color: 'var(--gw-text-3)' }}>
                            本时段 成本 <b className="gw-num">{fmt.usd(totalChCost)}</b> · 营收{' '}
                            <b className="gw-num">{fmt.usd(totalChCharge)}</b> · 毛利{' '}
                            <b className="gw-num" style={{ color: (periodMargin ?? 0) < 0 ? 'var(--gw-err)' : undefined }}>
                              {fmt.pct(periodMargin ?? 0, 1)}
                            </b>
                          </span>
                        ) : null
                      }
                    >
                      {byChannel.length ? (
                        <Table
                          rowKey="channelName"
                          size="middle"
                          dataSource={byChannel}
                          columns={byChannelCols}
                          pagination={false}
                        />
                      ) : (
                        <Empty
                          image={Empty.PRESENTED_IMAGE_SIMPLE}
                          description={usageQuery.isFetching ? '加载中…' : '该时段暂无按渠道用量'}
                        />
                      )}
                    </Card>
                  </>
                ),
              },
            ]}
          />
        )}
      </Drawer>

      <OfferFormModal
        open={offerModal.open}
        modelId={model?.id ?? 0}
        modelName={model?.originalName ?? ''}
        editing={offerModal.editing}
        channels={channels}
        usedChannelIds={usedChannelIds}
        onClose={() => setOfferModal({ open: false, editing: null })}
      />
    </>
  );
}
