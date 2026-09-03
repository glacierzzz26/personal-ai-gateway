import { Empty, Modal, Table } from 'antd';
import type { ColumnsType } from 'antd/es/table';
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

  const attrColumns: ColumnsType<AttrRow> = [
    {
      title: '属性', dataIndex: 'label', width: 110,
      render: v => <span style={{ color: 'var(--gw-text-2)' }}>{v}</span>,
    },
    ...models.map((m, i) => ({
      title: <span className="gw-mono">{m.name}</span>,
      key: `m${m.id}`,
      render: (_: unknown, r: AttrRow) => (
        <span
          className="gw-num"
          style={{
            display: 'block',
            padding: '0 4px',
            background: r.diff ? 'var(--gw-fill)' : undefined,
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
      latencyMs: o.latencyMs,
      successRate: o.successRate,
    })),
  );

  const offerColumns: ColumnsType<OfferRow> = [
    {
      title: '模型', dataIndex: 'modelName',
      render: v => <span className="gw-mono">{v}</span>,
    },
    {
      title: '渠道', dataIndex: 'channelName',
      render: v => <b style={{ fontWeight: 500 }}>{v}</b>,
    },
    { title: '供应商', dataIndex: 'provider' },
    {
      title: '输入价', dataIndex: 'inputPriceUsd', align: 'right',
      render: v => <span className="gw-num">{fmt.price(v)}</span>,
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
      title: '延迟', dataIndex: 'latencyMs', align: 'right',
      render: v => <span className="gw-num">{v ? fmt.ms(v) : '—'}</span>,
    },
    {
      title: '成功率', dataIndex: 'successRate', align: 'right',
      render: v => <span className="gw-num">{fmt.pct(v, 2)}</span>,
    },
  ];

  return (
    <Modal
      open={open}
      onCancel={onClose}
      footer={null}
      width={Math.min(1100, 620 + models.length * 130)}
      title={`模型对比 · ${models.length} 个`}
    >
      <Table<AttrRow>
        rowKey="label"
        size="middle"
        dataSource={attrRows}
        columns={attrColumns}
        pagination={false}
        scroll={{ x: 'max-content' }}
      />

      <div
        style={{
          marginTop: 20, fontSize: 14, fontWeight: 600,
          display: 'flex', alignItems: 'center', justifyContent: 'space-between',
        }}
      >
        <span>按供给源报价</span>
        <span style={{ fontSize: 12, color: 'var(--gw-text-3)', fontWeight: 400 }}>
          单位：美元 / 1M tokens（仅列已启用的供给源）
        </span>
      </div>
      {offerRows.length ? (
        <Table<OfferRow>
          rowKey={(r, i) => `${r.modelName}-${r.channelName}-${i}`}
          size="middle"
          style={{ marginTop: 10 }}
          dataSource={offerRows}
          columns={offerColumns}
          pagination={false}
          scroll={{ x: 'max-content' }}
        />
      ) : (
        <Empty style={{ padding: '24px 0' }} description="所选模型均未启用供给源" />
      )}

      <div style={{ fontSize: 12, color: 'var(--gw-text-3)', marginTop: 12 }}>
        上方表格同一行内数值不同时以浅色底标注，便于快速定位差异。
      </div>
    </Modal>
  );
}
