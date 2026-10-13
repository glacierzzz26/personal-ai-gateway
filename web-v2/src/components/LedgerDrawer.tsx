import { Drawer, Empty, Table } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { fmt } from '@/utils/format';
import { TOKENS } from '@/styles/tokens';
import type { BalanceLogItem } from '@/types';

const REASON: Record<string, string> = {
  topup: '充值',
  charge: '扣费',
  adjust: '调整',
};

const columns: ColumnsType<BalanceLogItem> = [
  {
    title: '时间', dataIndex: 'createdAt', width: 158,
    render: v => <span className="gw-num" style={{ color: 'var(--gw-text-3)' }}>{fmt.dt(v)}</span>,
  },
  {
    title: '类型', dataIndex: 'reason', width: 76,
    render: (v: string) => <span className="gw-badge">{REASON[v] ?? v}</span>,
  },
  {
    title: '金额', dataIndex: 'delta', align: 'right', width: 108,
    render: (v: number) => (
      <span className="gw-num" style={{ color: v < 0 ? TOKENS.err : TOKENS.ok }}>
        {v > 0 ? '+' : ''}{fmt.usd(v)}
      </span>
    ),
  },
  {
    title: '余额', dataIndex: 'balanceAfter', align: 'right', width: 108,
    render: v => <span className="gw-num">{fmt.usd(v)}</span>,
  },
  {
    title: '备注', dataIndex: 'note', ellipsis: true,
    render: v => v || <span style={{ color: 'var(--gw-text-3)' }}>—</span>,
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
  return (
    <Drawer
      width={720}
      open={open}
      onClose={onClose}
      title="账变流水"
      styles={{ body: { padding: 0 } }}
    >
      {logs.length === 0 ? (
        <Empty description="暂无账变记录" image={Empty.PRESENTED_IMAGE_SIMPLE} style={{ padding: '48px 0' }} />
      ) : (
        <Table<BalanceLogItem>
          rowKey="id"
          size="middle"
          dataSource={logs}
          columns={columns}
          pagination={false}
        />
      )}
    </Drawer>
  );
}
