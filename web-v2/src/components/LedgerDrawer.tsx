import { Drawer } from '@/ui/overlay';
import { Badge, Empty, Table } from '@/ui/primitives';
import type { Column } from '@/ui/primitives';
import { fmt } from '@/utils/format';
import type { BalanceLogItem } from '@/types';

const REASON: Record<string, string> = {
  topup: '充值',
  charge: '扣费',
  adjust: '调整',
};

const columns: Column<BalanceLogItem>[] = [
  {
    key: 'createdAt',
    title: '时间',
    width: 158,
    render: r => <span className="mono muted">{fmt.dt(r.createdAt)}</span>,
  },
  {
    key: 'reason',
    title: '类型',
    width: 76,
    render: r => <Badge tone="slate">{REASON[r.reason] ?? r.reason}</Badge>,
  },
  {
    key: 'delta',
    title: '金额',
    align: 'right',
    width: 108,
    render: r => (
      <span className="num" style={{ color: r.delta < 0 ? 'var(--err)' : 'var(--ok)' }}>
        {r.delta > 0 ? '+' : ''}
        {fmt.usd(r.delta)}
      </span>
    ),
  },
  {
    key: 'balanceAfter',
    title: '余额',
    align: 'right',
    width: 108,
    render: r => <span className="num">{fmt.usd(r.balanceAfter)}</span>,
  },
  {
    key: 'note',
    title: '备注',
    render: r => r.note || <span className="muted">—</span>,
  },
];

/**
 * 完整账变流水 Drawer。区块内的「账变流水」卡片只放最近几条摘要 ——
 * 右栏太窄,塞不下可滚动表格;完整明细(含备注)在这里横向铺开,不产生内层滚动条。
 */
export default function LedgerDrawer({ open, logs, onClose }: {
  open: boolean;
  logs: BalanceLogItem[];
  onClose: () => void;
}) {
  if (!open) return null;
  return (
    <div
      className="overlay"
      style={{ zIndex: 70 }}
      onMouseDown={e => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <Drawer title="账变流水" width={720} onClose={onClose}>
        {logs.length === 0 ? (
          <Empty icon="scroll" title="暂无账变记录" />
        ) : (
          <Table<BalanceLogItem> columns={columns} rows={logs} rowKey={r => r.id} />
        )}
      </Drawer>
    </div>
  );
}
