import { Btn, Copyable, Empty } from '@/ui/primitives';
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

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={`模型对比 · ${models.length} 个`}
      width={Math.min(1100, 620 + models.length * 130)}
      footer={<Btn onClick={onClose}>关闭</Btn>}
    >
      <div className="table-wrap" style={{ overflowX: 'auto' }}>
        <table className="tb">
          <thead>
            <tr>
              <th>属性</th>
              {models.map(m => (
                <th key={m.id} className="right">{<Copyable text={m.name}>{m.name}</Copyable>}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {attrRows.map(r => (
              <tr key={r.label}>
                <td className="cell-sub">{r.label}</td>
                {r.values.map((v, i) => (
                  <td key={i} className="right" style={r.diff ? { background: 'var(--panel-3)' } : undefined}>
                    <span className="num">{v}</span>
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <div style={{ marginTop: 20, fontSize: 14, fontWeight: 600, display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
        <span>按供给源报价</span>
        <span style={{ fontSize: 12, color: 'var(--t3)', fontWeight: 400 }}>
          单位：每百万 tokens（仅列已启用的供给源）
        </span>
      </div>
      {offerRows.length ? (
        <div className="table-wrap" style={{ marginTop: 10, overflowX: 'auto' }}>
          <table className="tb">
            <thead>
              <tr>
                <th>模型</th>
                <th>渠道</th>
                <th>供应商</th>
                <th className="right">输入价</th>
                <th className="right">输出价</th>
                <th className="right">缓存读价</th>
                <th className="right">缓存写价</th>
                <th className="right">延迟</th>
                <th className="right">成功率</th>
              </tr>
            </thead>
            <tbody>
              {offerRows.map((r, i) => (
                <tr key={i}>
                  <td className="mono strong">{r.modelName}</td>
                  <td><b style={{ fontWeight: 500 }}>{r.channelName}</b></td>
                  <td>{r.provider}</td>
                  <td className="right num">{fmt.price(r.inputPriceUsd)}</td>
                  <td className="right num">{fmt.price(r.outputPriceUsd)}</td>
                  <td className="right num">{r.cacheReadPriceUsd ? fmt.price(r.cacheReadPriceUsd) : '—'}</td>
                  <td className="right num">{r.cacheWritePriceUsd ? fmt.price(r.cacheWritePriceUsd) : '—'}</td>
                  <td className="right num">{r.latencyMs ? fmt.ms(r.latencyMs) : '—'}</td>
                  <td className="right num">{fmt.pct(r.successRate, 2)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <Empty title="所选模型均未启用供给源" />
      )}

      <div className="cell-sub" style={{ fontSize: 12, marginTop: 12 }}>
        上方表格同一行内数值不同时以浅色底标注，便于快速定位差异。
      </div>
    </Modal>
  );
}
