import { Empty, Table } from '@/ui/primitives';
import type { Column } from '@/ui/primitives';
import { Modal } from '@/ui/overlay';
import { fmt } from '@/utils/format';
import type { ModelCatalogItem } from '@/types';
import { bestPrice } from './ModelCard';

interface Props {
  open: boolean;
  models: ModelCatalogItem[];
  onClose: () => void;
}

type AttrRow = { label: string; values: string[]; diff: boolean };

type OfferRow = {
  modelName: string;
  channelName: string;
  provider: string;
  inputPriceUsd: number;
  outputPriceUsd: number;
  cacheReadPriceUsd: number;
  cacheWritePriceUsd: number;
  latencyMs: number;
  successRate: number;
};

export default function CompareModal({ open, models, onClose }: Props) {
  const build = (fn: (m: ModelCatalogItem) => string): Omit<AttrRow, 'label'> => {
    const values = models.map(fn);
    return { values, diff: new Set(values).size > 1 };
  };

  const attrRows: AttrRow[] = [
    { ...build(m => fmt.ctx(m.contextWindow)), label: '上下文' },
    { ...build(m => { const p = bestPrice(m); return p ? fmt.price(p.inP) : '—'; }), label: '最低输入价' },
    { ...build(m => { const p = bestPrice(m); return p ? fmt.price(p.outP) : '—'; }), label: '最低输出价' },
    { ...build(m => (m.capabilities.includes('vision') ? '支持' : '—')), label: '视觉' },
    { ...build(m => (m.capabilities.includes('function') ? '支持' : '—')), label: '函数调用' },
    { ...build(m => (m.capabilities.includes('stream') ? '支持' : '—')), label: '流式' },
    { ...build(m => (m.capabilities.includes('reasoning') ? '支持' : '—')), label: '推理' },
    { ...build(m => `${m.offers.length} 家`), label: '供给源数' },
    { ...build(m => fmt.ms(Math.round(m.offers.reduce((a, o) => a + o.latencyMs, 0) / (m.offers.length || 1)))), label: '平均延迟' },
    { ...build(m => fmt.k(m.todayRequests)), label: '今日调用' },
    { ...build(m => (m.enabled ? '已启用' : '已停用')), label: '状态' },
    { ...build(m => m.offers.map(o => o.channelName).join('、') || '—'), label: '供给来源' },
  ];

  const attrColumns: Column<AttrRow>[] = [
    {
      key: 'label', title: '属性', width: 110,
      render: r => <span style={{ color: 'var(--t2)' }}>{r.label}</span>,
    },
    ...models.map((m, i) => ({
      key: `m${m.id}`,
      title: <span className="mono">{m.name}</span>,
      render: (r: AttrRow) => (
        <span
          className="num"
          style={{
            display: 'block',
            padding: '0 4px',
            background: r.diff ? 'var(--hover)' : undefined,
            borderRadius: r.diff ? 4 : undefined,
          }}
        >
          {r.values[i]}
        </span>
      ),
    })),
  ];

  const offerRows: OfferRow[] = models.flatMap(m =>
    m.offers.filter(o => o.enabled).map(o => ({
      modelName: m.name,
      channelName: o.channelName,
      provider: o.provider,
      inputPriceUsd: o.inputPriceUsd,
      outputPriceUsd: o.outputPriceUsd,
      cacheReadPriceUsd: o.cacheReadPriceUsd ?? 0,
      cacheWritePriceUsd: o.cacheWritePriceUsd ?? 0,
      latencyMs: o.latencyMs,
      successRate: o.successRate,
    })),
  );

  const offerColumns: Column<OfferRow>[] = [
    { key: 'modelName', title: '模型', render: r => <span className="mono">{r.modelName}</span> },
    { key: 'channelName', title: '渠道', render: r => <b style={{ fontWeight: 500 }}>{r.channelName}</b> },
    { key: 'provider', title: '供应商', render: r => r.provider },
    { key: 'inputPriceUsd', title: '输入价', align: 'right', render: r => <span className="num">{fmt.price(r.inputPriceUsd)}</span> },
    { key: 'outputPriceUsd', title: '输出价', align: 'right', render: r => <span className="num">{fmt.price(r.outputPriceUsd)}</span> },
    { key: 'cacheReadPriceUsd', title: '缓存读价', align: 'right', render: r => <span className="num">{r.cacheReadPriceUsd ? fmt.price(r.cacheReadPriceUsd) : '—'}</span> },
    { key: 'cacheWritePriceUsd', title: '缓存写价', align: 'right', render: r => <span className="num">{r.cacheWritePriceUsd ? fmt.price(r.cacheWritePriceUsd) : '—'}</span> },
    { key: 'latencyMs', title: '延迟', align: 'right', render: r => <span className="num">{r.latencyMs ? fmt.ms(r.latencyMs) : '—'}</span> },
    { key: 'successRate', title: '成功率', align: 'right', render: r => <span className="num">{fmt.pct(r.successRate, 2)}</span> },
  ];

  if (!open) return null;

  return (
    <div className="overlay" style={{ zIndex: 70 }}>
      <Modal
        title={`模型对比 · ${models.length} 个`}
        width={Math.min(1100, 620 + models.length * 130)}
        onClose={onClose}
      >
        <Table<AttrRow> columns={attrColumns} rows={attrRows} rowKey={r => r.label} />

        <div
          style={{
            marginTop: 20, fontSize: 14, fontWeight: 600,
            display: 'flex', alignItems: 'center', justifyContent: 'space-between',
          }}
        >
          <span>按供给源报价</span>
          <span style={{ fontSize: 12, color: 'var(--t3)', fontWeight: 400 }}>
            单位：每百万 tokens（仅列已启用的供给源）
          </span>
        </div>
        {offerRows.length ? (
          <div style={{ marginTop: 10 }}>
            <Table<OfferRow>
              columns={offerColumns}
              rows={offerRows}
              rowKey={(r, i) => `${r.modelName}-${r.channelName}-${i}`}
            />
          </div>
        ) : (
          <div style={{ padding: '24px 0' }}>
            <Empty icon="box" title="所选模型均未启用供给源" />
          </div>
        )}

        <div style={{ fontSize: 12, color: 'var(--t3)', marginTop: 12 }}>
          上方表格同一行内数值不同时以浅色底标注，便于快速定位差异。
        </div>
      </Modal>
    </div>
  );
}
