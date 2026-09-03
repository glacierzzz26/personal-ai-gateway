import { Modal, Table } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { fmt } from '@/utils/format';
import type { ModelCatalogItem } from '@/types';
import { bestPrice } from './ModelCard';

interface Props {
  open: boolean;
  models: ModelCatalogItem[];
  onClose: () => void;
}

type Row = { label: string; values: string[]; diff: boolean };

export default function CompareModal({ open, models, onClose }: Props) {
  const build = (fn: (m: ModelCatalogItem) => string): Row => {
    const values = models.map(fn);
    return { label: '', values, diff: new Set(values).size > 1 };
  };

  const rows: Row[] = [
    { ...build(m => fmt.ctx(m.contextWindow)), label: '上下文' },
    { ...build(m => { const p = bestPrice(m); return p ? fmt.price(p.inP) : '—'; }), label: '最低输入价' },
    { ...build(m => { const p = bestPrice(m); return p ? fmt.price(p.outP) : '—'; }), label: '最低输出价' },
    { ...build(m => (m.capabilities.includes('vision') ? '支持' : '—')), label: '视觉' },
    { ...build(m => (m.capabilities.includes('function') ? '支持' : '—')), label: '函数调用' },
    { ...build(m => (m.capabilities.includes('stream') ? '支持' : '—')), label: '流式' },
    { ...build(m => `${m.offers.length} 家`), label: '供应商数' },
    {
      ...build(m => fmt.ms(Math.round(m.offers.reduce((a, o) => a + o.latencyMs, 0) / (m.offers.length || 1)))),
      label: '平均延迟',
    },
    { ...build(m => fmt.k(m.todayRequests)), label: '今日调用' },
    { ...build(m => (m.enabled ? '已启用' : '已停用')), label: '状态' },
    { ...build(m => m.offers.map(o => o.channelName).join('、') || '—'), label: '供给来源' },
  ];

  const columns: ColumnsType<Row> = [
    {
      title: '属性', dataIndex: 'label', width: 110,
      render: v => <span style={{ color: 'var(--gw-text-2)' }}>{v}</span>,
    },
    ...models.map((m, i) => ({
      title: <span className="gw-mono">{m.name}</span>,
      key: `m${m.id}`,
      render: (_: unknown, r: Row) => (
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

  return (
    <Modal
      open={open}
      onCancel={onClose}
      footer={null}
      width={Math.min(1000, 420 + models.length * 150)}
      title={`模型对比 · ${models.length} 个`}
    >
      <Table<Row>
        rowKey="label"
        size="middle"
        dataSource={rows}
        columns={columns}
        pagination={false}
        scroll={{ x: 'max-content' }}
      />
      <div style={{ fontSize: 12, color: 'var(--gw-text-3)', marginTop: 12 }}>
        同一行内数值不同时以浅色底标注，便于快速定位差异。
      </div>
    </Modal>
  );
}
