import { useMemo, useState } from 'react';
import { App, Button, Card, Empty, Select, Table, Tooltip } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { DegradedNote } from '@/components/States';
import ProviderMark from '@/components/ProviderMark';
import ManualPriceModal from '@/components/models/ManualPriceModal';
import { api } from '@/services/api';
import { currentCurrency } from '@/stores/currency';
import { fmt } from '@/utils/format';
import { buildOfficialIndex, officialFor } from '@/utils/official';
import type {
  BillingShape, ModelCatalogItem, ModelOffer, OfficialPriceView, Provider,
} from '@/types';

/** 计费形态标签。分时/阶梯/折扣必须显式标注 —— 网关按单一价计费。 */
const SHAPE_LABEL: Record<BillingShape, string> = {
  flat: '单一价',
  peak_offpeak: '峰谷分时',
  tiered: '阶梯计价',
  discount: '限时折扣',
};

const dash = <span style={{ color: 'var(--gw-text-3)' }}>—</span>;

interface Props {
  model: ModelCatalogItem;
  offers: ModelOffer[];
}

/**
 * 官方参考价面板:展示该模型各渠道 provider 的官方单价,与本渠道报价并列比对,
 * 支持「指定官方价来源」「应用官方价」「手工录入」。官方价与手工报价分表,应用是显式动作。
 */
export default function OfficialPricePanel({ model, offers }: Props) {
  const { message, modal } = App.useApp();
  const qc = useQueryClient();
  const [manualOpen, setManualOpen] = useState(false);

  const { data: all = [], isLoading } = useQuery({
    queryKey: ['official-prices'],
    queryFn: () => api.officialPrices(),
    retry: 0,
    staleTime: 30_000,
  });
  const { data: vendors = [] } = useQuery({
    queryKey: ['official-vendors'],
    queryFn: () => api.officialVendors(),
    retry: 0,
    staleTime: 300_000,
  });

  // (provider, 模型名) → 官方价。匹配优先级见 utils/official。
  const index = useMemo(() => buildOfficialIndex(all), [all]);

  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ['official-prices'] });
    qc.invalidateQueries({ queryKey: ['models'] });
  };

  /* —— 官方价来源绑定(手工覆盖;留空 = 自动推断) —— */
  const saveBinding = useMutation({
    mutationFn: (patch: { officialVendor: string; officialModelName: string }) =>
      api.updateModel(model.id, api.modelDraft(model, patch)),
    onSuccess: () => { message.success('官方价来源已更新'); invalidate(); },
    onError: (e) => message.error((e as Error)?.message || '保存失败'),
  });
  const onVendor = (v?: Provider) =>
    saveBinding.mutate({ officialVendor: v ?? '', officialModelName: '' });
  const onModelName = (n?: string) =>
    saveBinding.mutate({ officialVendor: model.officialVendor ?? '', officialModelName: n ?? '' });

  const vendorOptions = useMemo(() => {
    const set = new Set<string>(vendors.map(v => v.provider));
    if (model.officialVendor) set.add(model.officialVendor);
    return Array.from(set).map(p => ({ value: p, label: p }));
  }, [vendors, model.officialVendor]);
  const modelNameOptions = useMemo(
    () => all.filter(op => op.provider === model.officialVendor).map(op => ({ value: op.modelName, label: op.modelName })),
    [all, model.officialVendor],
  );

  const apply = useMutation({
    mutationFn: (v: { opId: number; offerId: number }) => api.applyOfficialPrice(v.opId, v.offerId, true),
    onSuccess: () => { message.success('已应用官方价'); invalidate(); },
    onError: (e) => message.error((e as Error)?.message || '应用失败'),
  });

  function handleApply(o: ModelOffer, op: OfficialPriceView) {
    if (!op.rateSet) {
      message.warning(
        `该官方价原币为 ${op.currency},与当前计价币种不一致且未设汇率 —— 请先到「系统设置」填写 USD/CNY 汇率`,
      );
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

  const rows = offers.map(o => ({ offer: o, op: officialFor(index, model, o) }));

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
      title: '当前报价', key: 'cur', align: 'right',
      render: (_, { offer }) => (
        <span className="gw-num">{fmt.price(offer.inputPriceUsd)} / {fmt.price(offer.outputPriceUsd)}</span>
      ),
    },
    {
      title: '官方参考价', key: 'official', align: 'right',
      render: (_, { op }) => {
        if (!op) return dash;
        const cur = op.currency === 'CNY' ? '¥' : '$';
        // 官方原币与计价币种一致时,原价即计价金额(无需汇率);不一致才多给一行折算值。
        const needsConvert = op.currency !== currentCurrency();
        return (
          <div>
            <div className="gw-num" style={{ color: 'var(--gw-text-2)' }}>
              {cur}{op.inputPrice} / {cur}{op.outputPrice}
            </div>
            {needsConvert && op.rateSet && (
              <div className="gw-num" style={{ fontSize: 12, color: 'var(--gw-text-3)' }}>
                ≈ {fmt.price(op.inputPriceUsd)} / {fmt.price(op.outputPriceUsd)}
              </div>
            )}
            {needsConvert && !op.rateSet && (
              <div style={{ fontSize: 12, color: 'var(--gw-warn)' }}>需设汇率</div>
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

  const bindHint = model.officialVendor
    ? '已显式绑定官方模型名;该模型的官方参考价优先取此绑定行。'
    : model.inferredVendor
      ? `未显式绑定,按模型名自动推断为「${model.inferredVendor}」;若官方页模型名不同,请在此指定。`
      : '未显式绑定,且未能从模型名推断厂商 —— 聚合渠道请在此指定官方厂商与模型名。';

  return (
    <Card
      title="官方参考价"
      style={{ marginTop: 20 }}
      extra={<Button size="small" onClick={() => setManualOpen(true)}>手工录入</Button>}
    >
      <DegradedNote title="官方价仅作核对参考">
        官方价与手工报价分表存放;点「应用官方价」才会写入该渠道报价。分时/阶梯/折扣价展示的是「生效默认」档,
        网关按单一价计费,不随时间/用量自动分段。来源 URL 与抓取时间可一键跳转核对。
      </DegradedNote>

      <div style={{ display: 'flex', gap: 12, alignItems: 'flex-start', flexWrap: 'wrap', marginTop: 12 }}>
        <div>
          <div style={{ fontSize: 12, color: 'var(--gw-text-3)', marginBottom: 4 }}>官方价来源(厂商)</div>
          <Select
            allowClear
            style={{ width: 160 }}
            placeholder="自动推断"
            value={model.officialVendor || undefined}
            options={vendorOptions}
            onChange={onVendor}
            loading={saveBinding.isPending}
          />
        </div>
        <div>
          <div style={{ fontSize: 12, color: 'var(--gw-text-3)', marginBottom: 4 }}>官方模型名</div>
          <Select
            allowClear
            showSearch
            style={{ width: 220 }}
            placeholder={model.officialVendor ? '选择官方页模型名' : '先选厂商'}
            disabled={!model.officialVendor}
            value={model.officialModelName || undefined}
            options={modelNameOptions}
            onChange={onModelName}
            loading={saveBinding.isPending}
            notFoundContent="该厂商暂无已入库官方价,请先到「官方定价」获取或手工录入"
          />
        </div>
        <div style={{ fontSize: 12, color: 'var(--gw-text-3)', maxWidth: 320, paddingTop: 22 }}>{bindHint}</div>
      </div>

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
          style={{ marginTop: 12 }}
          description="暂无官方参考价。到「官方定价」按厂商获取官网价,或点右上「手工录入」。"
        />
      )}

      <ManualPriceModal
        open={manualOpen}
        defaultProvider={(model.officialVendor as Provider) || offers[0]?.provider}
        defaultModelName={model.officialModelName || offers[0]?.upstreamModel || model.originalName}
        onClose={() => setManualOpen(false)}
      />
    </Card>
  );
}
