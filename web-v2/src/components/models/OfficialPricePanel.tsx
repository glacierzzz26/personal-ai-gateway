import { useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { DegradedNote } from '@/components/States';
import ProviderMark from '@/components/ProviderMark';
import ManualPriceModal from '@/components/models/ManualPriceModal';
import { Badge, Button, Card, CardBody, CardHead, Empty, Field, Select, Table } from '@/ui/primitives';
import type { Column } from '@/ui/primitives';
import { confirm } from '@/ui/overlay';
import { message } from '@/ui/toast';
import { api } from '@/services/api';
import { currentCurrency } from '@/stores/currency';
import { fmt } from '@/utils/format';
import { buildOfficialIndex, officialFor } from '@/utils/official';
import type {
  BillingShape, ModelCatalogItem, ModelOffer, OfficialPriceView, Provider,
} from '@/types';

/** 计费形态标签。分时/阶梯/折扣必须显式标注 —— 计费按请求时刻选档,展示列的是「生效默认」档。 */
const SHAPE_LABEL: Record<BillingShape, string> = {
  flat: '单一价',
  peak_offpeak: '峰谷分时',
  tiered: '阶梯计价',
  discount: '限时折扣',
};

const dash = <span className="muted">—</span>;

interface Props {
  model: ModelCatalogItem;
  offers: ModelOffer[];
}

/**
 * 官方参考价面板:展示该模型各渠道供应商的官方单价,与本渠道报价并列比对,
 * 支持「指定官方价来源」「绑定到模型」「手工录入」。官方价与供给源报价分表,
 * 绑定是显式动作 —— 而绑定之后,成本与售价都由官方价现算派生。
 */
export default function OfficialPricePanel({ model, offers }: Props) {
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

  // 原生 select 不支持 allowClear,用一个空值选项充当「自动推断 / 未指定」。
  const vendorOptions = useMemo(() => {
    const set = new Set<string>(vendors.map(v => v.provider));
    if (model.officialVendor) set.add(model.officialVendor);
    return [{ value: '', label: '自动推断' }, ...Array.from(set).map(p => ({ value: p, label: p }))];
  }, [vendors, model.officialVendor]);
  const modelNameOptions = useMemo(() => {
    const opts = all
      .filter(op => op.provider === model.officialVendor)
      .map(op => ({ value: op.modelName, label: op.modelName }));
    // 当前绑定名可能尚未出现在已入库官方价里,补一项免得原生 select 落空。
    if (model.officialModelName && !opts.some(o => o.value === model.officialModelName)) {
      opts.unshift({ value: model.officialModelName, label: model.officialModelName });
    }
    return [{ value: '', label: model.officialVendor ? '选择官方页模型名' : '先选厂商' }, ...opts];
  }, [all, model.officialVendor, model.officialModelName]);

  const apply = useMutation({
    mutationFn: (v: { opId: number; offerId: number }) => api.applyOfficialPrice(v.opId, v.offerId, true),
    onSuccess: () => { message.success('已绑定到模型'); invalidate(); },
    onError: (e) => message.error((e as Error)?.message || '绑定失败'),
  });

  /**
   * 「绑定到模型」—— 界面形状不变(仍是每行一个按钮),但动效在成本改造后变了:
   *
   * 改造前它把官方价**写进该 offer 的三价**,而那正是「成本 = 官方挂牌价」这个 bug 的来源
   * (生产里模型 20 的 1.0/4.0/0.02 就是这么来的)。现在成本 = 官方价 × 渠道系数,是现算的,
   * 真正让成本生效的动作是写 models.official_vendor/official_model_name —— 即本按钮。
   * 后端顺带把四价快照进该模型全部 offer,仅作派生不可用时的兜底。
   */
  async function handleApply(o: ModelOffer, op: OfficialPriceView) {
    if (!op.rateSet) {
      message.warning(
        `该官方价原币为 ${op.currency},与当前计价币种不一致且未设汇率 —— 请先到「系统设置」填写 USD/CNY 汇率`,
      );
      return;
    }
    const run = () => apply.mutate({ opId: op.id, offerId: o.id });
    if (o.overridePrice) {
      const ok = await confirm({
        title: '该供给源已手工覆盖报价',
        content: `绑定后该模型全部供给源的成本改由官方价派生,${o.channelName} 的手工兜底价(${fmt.price(o.inputPriceUsd)} / ${fmt.price(o.outputPriceUsd)})会被快照覆盖。是否继续?`,
        okText: '仍要绑定',
        danger: true,
      });
      if (ok) run();
      return;
    }
    run();
  }

  const rows = offers.map(o => ({ offer: o, op: officialFor(index, model, o) }));

  const cols: Column<{ offer: ModelOffer; op?: OfficialPriceView }>[] = [
    {
      key: 'ch', title: '渠道 / 供应商',
      render: ({ offer }) => (
        <div>
          <div className="hstack" style={{ gap: 6 }}>
            <ProviderMark name={offer.provider} /> <span className="strong">{offer.channelName}</span>
          </div>
          <div style={{ fontSize: 12, color: 'var(--t3)' }}>{offer.provider}</div>
        </div>
      ),
    },
    {
      key: 'cur', title: '当前报价', align: 'right',
      render: ({ offer }) => (
        <span className="num">{fmt.price(offer.inputPriceUsd)} / {fmt.price(offer.outputPriceUsd)}</span>
      ),
    },
    {
      key: 'official', title: '官方参考价', align: 'right',
      render: ({ op }) => {
        if (!op) return dash;
        const cur = op.currency === 'CNY' ? '¥' : '$';
        // 官方原币与计价币种一致时,原价即计价金额(无需汇率);不一致才多给一行折算值。
        const needsConvert = op.currency !== currentCurrency();
        return (
          <div>
            <div className="num" style={{ color: 'var(--t2)' }}>
              {cur}{op.inputPrice} / {cur}{op.outputPrice}
            </div>
            {(op.cacheReadPrice || op.cacheWritePrice) && (
              <div className="num" style={{ fontSize: 12, color: 'var(--t3)' }}>
                {op.cacheReadPrice ? `缓存读 ${cur}${op.cacheReadPrice}` : ''}
                {op.cacheReadPrice && op.cacheWritePrice ? ' · ' : ''}
                {op.cacheWritePrice ? `缓存写 ${cur}${op.cacheWritePrice}` : ''}
              </div>
            )}
            {needsConvert && op.rateSet && (
              <div className="num" style={{ fontSize: 12, color: 'var(--t3)' }}>
                ≈ {fmt.price(op.inputPriceUsd)} / {fmt.price(op.outputPriceUsd)}
              </div>
            )}
            {needsConvert && !op.rateSet && (
              <div style={{ fontSize: 12, color: 'var(--warn)' }}>需设汇率</div>
            )}
          </div>
        );
      },
    },
    {
      key: 'shape', title: '形态', width: 96,
      render: ({ op }) =>
        op ? (
          <span
            title={op.billingShape === 'flat' ? '官方单一价' : '官方为分时/阶梯/折扣价。分时按请求时刻自动选峰/谷价;阶梯暂按首档标量计。此处展示的是「生效默认」档'}
          >
            <Badge tone="slate">{SHAPE_LABEL[op.billingShape]}</Badge>
          </span>
        ) : dash,
    },
    {
      key: 'src', title: '来源', width: 150,
      render: ({ op }) =>
        op ? (
          <div style={{ fontSize: 12 }}>
            <a href={op.sourceUrl} target="_blank" rel="noreferrer" className="mono">官方页面 ↗</a>
            <div style={{ color: 'var(--t3)' }}>{fmt.dt(op.fetchedAt)}</div>
            {op.cacheDerived && (
              <span title="官方页面无独立缓存价列,此值为按官方规则推导" style={{ color: 'var(--warn)' }}>
                缓存价推导
              </span>
            )}
          </div>
        ) : dash,
    },
    {
      key: 'act', title: '', align: 'right', width: 110,
      render: ({ offer, op }) =>
        op ? (
          <Button
            size="sm"
            variant="primary"
            loading={apply.isPending && apply.variables?.offerId === offer.id}
            onClick={() => void handleApply(offer, op)}
          >
            绑定到模型
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
    <Card style={{ marginTop: 20 }}>
      <CardHead
        title="官方参考价"
        right={<Button size="sm" onClick={() => setManualOpen(true)}>手工录入</Button>}
      />
      <CardBody>
        <DegradedNote title="成本由官方价派生,此处只做绑定">
          成本 = 官方价 × 渠道系数,售价 = 官方价 × 倍率,都是现算的 —— 官方价一变、系数一改,全站即时重算。
          点「绑定到模型」写下绑定关系(这是让派生成本生效的动作),并顺带把四价快照进供给源,
          仅在该模型派生不可用(未设汇率等)时作兜底。来源 URL 与抓取时间可一键跳转核对。
        </DegradedNote>

        <div className="hstack wrap" style={{ gap: 12, alignItems: 'flex-start', marginTop: 12 }}>
          <Field label="官方价来源(厂商)">
            <Select
              style={{ width: 160 }}
              value={model.officialVendor || ''}
              options={vendorOptions}
              onChange={(e) => onVendor((e.target.value || undefined) as Provider | undefined)}
              disabled={saveBinding.isPending}
            />
          </Field>
          <Field label="官方模型名">
            <Select
              style={{ width: 220 }}
              value={model.officialModelName || ''}
              options={modelNameOptions}
              onChange={(e) => onModelName(e.target.value || undefined)}
              disabled={!model.officialVendor || saveBinding.isPending}
            />
          </Field>
          <div style={{ fontSize: 12, color: 'var(--t3)', maxWidth: 320, paddingTop: 20 }}>{bindHint}</div>
        </div>

        {rows.some(r => r.op) || isLoading ? (
          <div style={{ marginTop: 12 }}>
            <Table
              rows={rows}
              rowKey={r => r.offer.id}
              columns={cols}
            />
          </div>
        ) : (
          <Empty
            title="暂无官方参考价"
            desc="到「官方定价」按厂商获取官网价,或点右上「手工录入」。"
          />
        )}

        <ManualPriceModal
          open={manualOpen}
          defaultProvider={(model.officialVendor as Provider) || offers[0]?.provider}
          defaultModelName={model.officialModelName || offers[0]?.upstreamModel || model.originalName}
          onClose={() => setManualOpen(false)}
        />
      </CardBody>
    </Card>
  );
}
