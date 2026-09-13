import { useMemo, useState } from 'react';
import {
  App, Button, Card, Empty, Form, Input, InputNumber, Modal, Select, Space, Table, Tooltip,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { DegradedNote } from '@/components/States';
import ProviderMark from '@/components/ProviderMark';
import { api } from '@/services/api';
import { fmt } from '@/utils/format';
import type {
  BillingShape, ManualPriceDraft, ModelCatalogItem, ModelOffer, OfficialPriceView, Provider,
} from '@/types';

/** 计费形态标签。分时/阶梯/折扣必须显式标注 —— 网关按单一价计费。 */
const SHAPE_LABEL: Record<BillingShape, string> = {
  flat: '单一价',
  peak_offpeak: '峰谷分时',
  tiered: '阶梯计价',
  discount: '限时折扣',
};

/** 仅可手工录入的 provider(官方页动态渲染,见后端 internal/pricing)。 */
const MANUAL_ONLY: Provider[] = ['智谱'];

const dash = <span style={{ color: 'var(--gw-text-3)' }}>—</span>;

interface Props {
  model: ModelCatalogItem;
  offers: ModelOffer[];
}

/**
 * 官方参考价面板:展示该模型各渠道 provider 的官方单价,与本渠道报价并列比对,
 * 支持「应用官方价」「手工录入」。官方价与手工报价分表,应用是显式动作。
 */
export default function OfficialPricePanel({ model, offers }: Props) {
  const { message, modal } = App.useApp();
  const qc = useQueryClient();
  const [manualOpen, setManualOpen] = useState(false);
  const [form] = Form.useForm<ManualPriceDraft>();

  const { data: all = [], isLoading } = useQuery({
    queryKey: ['official-prices'],
    queryFn: () => api.officialPrices(),
    retry: 0,
    staleTime: 30_000,
  });

  // (provider, 模型名) → 官方价。渠道侧真实模型名优先,统一名兜底。
  const index = useMemo(() => {
    const m = new Map<string, OfficialPriceView>();
    for (const op of all) m.set(`${op.provider}|${op.modelName}`, op);
    return m;
  }, [all]);

  const officialFor = (o: ModelOffer): OfficialPriceView | undefined =>
    index.get(`${o.provider}|${model.originalName}`) ?? index.get(`${o.provider}|${model.name}`);

  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ['official-prices'] });
    qc.invalidateQueries({ queryKey: ['models'] });
  };

  const apply = useMutation({
    mutationFn: (v: { opId: number; offerId: number }) => api.applyOfficialPrice(v.opId, v.offerId, true),
    onSuccess: () => { message.success('已应用官方价'); invalidate(); },
    onError: (e) => message.error((e as Error)?.message || '应用失败'),
  });

  function handleApply(o: ModelOffer, op: OfficialPriceView) {
    if (!op.rateSet) {
      message.warning('该官方价为人民币且未设置汇率,请先到「系统设置」填写 USD/CNY 汇率');
      return;
    }
    const run = () => apply.mutate({ opId: op.id, offerId: o.id });
    if (o.overridePrice) {
      modal.confirm({
        title: '该供给源已手工覆盖报价',
        content: `应用官方价会覆盖 ${o.channelName} 当前的手工报价(${fmt.price(o.inputPriceUsd)} / ${fmt.price(o.outputPriceUsd)})。是否继续?`,
        okText: '仍要应用',
        okButtonProps: { danger: true },
        cancelText: '取消',
        onOk: run,
      });
      return;
    }
    run();
  }

  const manual = useMutation({
    mutationFn: (v: ManualPriceDraft) => api.manualOfficialPrice(v),
    onSuccess: () => { message.success('官方参考价已录入'); setManualOpen(false); invalidate(); },
    onError: (e) => message.error((e as Error)?.message || '录入失败'),
  });

  function openManual() {
    form.resetFields();
    form.setFieldsValue({
      provider: offers[0]?.provider ?? '智谱',
      modelName: model.originalName,
      currency: 'CNY',
      inputPrice: 0,
      outputPrice: 0,
      cacheReadPrice: 0,
    });
    setManualOpen(true);
  }

  const rows = offers.map(o => ({ offer: o, op: officialFor(o) }));
  const providers = useMemo(() => Array.from(new Set(offers.map(o => o.provider))), [offers]);
  const canManual = providers.some(p => MANUAL_ONLY.includes(p));

  const cols: ColumnsType<{ offer: ModelOffer; op?: OfficialPriceView }> = [
    {
      title: '渠道 / 供应商', key: 'ch',
      render: (_, { offer }) => (
        <div>
          <div style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
            <ProviderMark name={offer.provider} /> <b style={{ fontWeight: 500 }}>{offer.channelName}</b>
          </div>
          <div style={{ fontSize: 12, color: 'var(--gw-text-3)' }}>{offer.provider}</div>
        </div>
      ),
    },
    {
      title: '当前报价(USD)', key: 'cur', align: 'right',
      render: (_, { offer }) => (
        <span className="gw-num">{fmt.price(offer.inputPriceUsd)} / {fmt.price(offer.outputPriceUsd)}</span>
      ),
    },
    {
      title: '官方参考价', key: 'official', align: 'right',
      render: (_, { op }) => {
        if (!op) return dash;
        const cur = op.currency === 'CNY' ? '¥' : '$';
        return (
          <div>
            <div className="gw-num" style={{ color: 'var(--gw-text-2)' }}>
              {cur}{op.inputPrice} / {cur}{op.outputPrice}
            </div>
            {op.rateSet && op.currency === 'CNY' && (
              <div className="gw-num" style={{ fontSize: 12, color: 'var(--gw-text-3)' }}>
                ≈ {fmt.price(op.inputPriceUsd)} / {fmt.price(op.outputPriceUsd)}
              </div>
            )}
          </div>
        );
      },
    },
    {
      title: '形态', key: 'shape', width: 96,
      render: (_, { op }) =>
        op ? (
          <Tooltip title={op.billingShape === 'flat' ? '官方单一价' : '官方为分时/阶梯/折扣价,网关按单一价计费;此处展示的是「生效默认」档'}>
            <span className="gw-badge">{SHAPE_LABEL[op.billingShape]}</span>
          </Tooltip>
        ) : dash,
    },
    {
      title: '来源', key: 'src', width: 150,
      render: (_, { op }) =>
        op ? (
          <div style={{ fontSize: 12 }}>
            <a href={op.sourceUrl} target="_blank" rel="noreferrer" className="gw-mono">官方页面 ↗</a>
            <div style={{ color: 'var(--gw-text-3)' }}>{fmt.dt(op.fetchedAt)}</div>
            {op.cacheDerived && (
              <Tooltip title="官方页面无独立缓存价列,此值为按官方规则推导">
                <span style={{ color: 'var(--gw-warn)' }}>缓存价推导</span>
              </Tooltip>
            )}
          </div>
        ) : dash,
    },
    {
      title: '', key: 'act', align: 'right', width: 110,
      render: (_, { offer, op }) =>
        op ? (
          <Button
            size="small"
            type="primary"
            loading={apply.isPending && apply.variables?.offerId === offer.id}
            onClick={() => handleApply(offer, op)}
          >
            应用官方价
          </Button>
        ) : dash,
    },
  ];

  return (
    <Card
      title="官方参考价"
      style={{ marginTop: 20 }}
      extra={canManual ? <Button size="small" onClick={openManual}>手工录入</Button> : undefined}
    >
      <DegradedNote title="官方价仅作核对参考">
        官方价与手工报价分表存放;点「应用官方价」才会写入该渠道报价。分时/阶梯/折扣价展示的是「生效默认」档,
        网关按单一价计费,不随时间/用量自动分段。来源 URL 与抓取时间可一键跳转核对。
      </DegradedNote>

      {rows.some(r => r.op) || isLoading ? (
        <Table
          rowKey={r => r.offer.id}
          size="small"
          style={{ marginTop: 12 }}
          loading={isLoading}
          dataSource={rows}
          columns={cols}
          pagination={false}
        />
      ) : (
        <Empty
          image={Empty.PRESENTED_IMAGE_SIMPLE}
          description={
            canManual
              ? '暂无官方参考价。该厂商官方页为动态渲染,请点右上「手工录入」按官网页面填写。'
              : '暂无官方参考价。可到「渠道管理」对 DeepSeek / 通义千问渠道点「获取官方定价」。'
          }
        />
      )}

      <Modal
        title="手工录入官方参考价"
        open={manualOpen}
        onCancel={() => setManualOpen(false)}
        onOk={() => form.submit()}
        confirmLoading={manual.isPending}
        okText="录入"
        cancelText="取消"
        width={560}
        destroyOnHidden
      >
        <Form form={form} layout="vertical" onFinish={v => manual.mutate(v)} requiredMark={false}>
          <div className="gw-note" style={{ marginBottom: 14 }}>
            <span>请对照厂商官网计费页逐项填写,并粘贴官网页面地址作为来源 —— 手工录入同样需要可追溯核对。</span>
          </div>
          <Form.Item name="provider" label="供应商" rules={[{ required: true, message: '必填' }]}>
            <Select options={providers.map(p => ({ value: p, label: p }))} />
          </Form.Item>
          <Form.Item name="modelName" label="模型名(渠道侧真实名)" rules={[{ required: true, whitespace: true, message: '必填' }]}>
            <Input className="gw-mono" />
          </Form.Item>
          <Form.Item
            name="sourceUrl"
            label="来源 URL(官方计费页)"
            rules={[
              { required: true, whitespace: true, message: '必填:手工价也须可追溯' },
              { pattern: /^https?:\/\//, message: '须为 http(s) 地址' },
            ]}
          >
            <Input className="gw-mono" placeholder="https://bigmodel.cn/pricing" />
          </Form.Item>
          <Form.Item name="currency" label="币种" rules={[{ required: true, message: '必填' }]}>
            <Select options={[{ value: 'CNY', label: '人民币 CNY' }, { value: 'USD', label: '美元 USD' }]} />
          </Form.Item>
          <Space size={12} style={{ display: 'flex' }}>
            <Form.Item name="inputPrice" label="输入价(每百万 tokens)" rules={[{ required: true, message: '必填' }]} style={{ flex: 1 }}>
              <InputNumber min={0} precision={4} step={0.01} style={{ width: 160 }} />
            </Form.Item>
            <Form.Item name="outputPrice" label="输出价(每百万 tokens)" rules={[{ required: true, message: '必填' }]} style={{ flex: 1 }}>
              <InputNumber min={0} precision={4} step={0.01} style={{ width: 160 }} />
            </Form.Item>
            <Form.Item name="cacheReadPrice" label="缓存命中读价" style={{ flex: 1 }}>
              <InputNumber min={0} precision={4} step={0.01} style={{ width: 160 }} />
            </Form.Item>
          </Space>
          <Form.Item name="nativeText" label="官网原文(可选)" tooltip="如限时折扣说明、档位描述,便于日后核对">
            <Input placeholder="例:限时 5 折 / 0<Token≤1M" />
          </Form.Item>
          <Form.Item name="note" label="备注(可选)">
            <Input.TextArea rows={2} />
          </Form.Item>
        </Form>
      </Modal>
    </Card>
  );
}
