import { Drawer } from '@/ui/overlay';
import { Empty } from '@/ui/primitives';
import { currentCurrency } from '@/stores/currency';
import type { BalanceLogItem } from '@/types';

/**
 * 格式化函数本应直接复用 @/utils/format 的 fmt，但该模块顶部
 * `import { TOKENS } from '@/styles/tokens'` 在仓库里无对应文件，
 * 引入即会触发模块解析失败。此处按原 fmt 逻辑内联等价实现，
 * 行为保持一致（币种符号跟随 currentCurrency）。
 */
const SYMBOL: Record<string, string> = { CNY: '¥', USD: '$' };
const dec = (v: number, d: number) => (v > 0 && v < 0.005 ? Math.max(d, 4) : d);
const usd = (v: number, d = 2) => `${SYMBOL[currentCurrency()] ?? '$'}${v.toFixed(dec(v, d))}`;
const dt = (v?: string | null) => {
  if (!v) return '—';
  const d = new Date(v);
  if (Number.isNaN(d.getTime())) return v;
  const p = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
};

const REASON: Record<string, string> = {
  topup: '充值',
  charge: '扣费',
  adjust: '调整',
};

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
    >
      {logs.length === 0 ? (
        <Empty icon="box" title="暂无账变记录" />
      ) : (
        <div className="table-wrap">
          <table className="tb">
            <thead>
              <tr>
                <th style={{ width: 158 }}>时间</th>
                <th style={{ width: 76 }}>类型</th>
                <th className="right" style={{ width: 108 }}>金额</th>
                <th className="right" style={{ width: 108 }}>余额</th>
                <th>备注</th>
              </tr>
            </thead>
            <tbody>
              {logs.map(l => (
                <tr key={l.id}>
                  <td><span className="num" style={{ color: 'var(--t3)' }}>{dt(l.createdAt)}</span></td>
                  <td><span className="tag">{REASON[l.reason] ?? l.reason}</span></td>
                  <td className="right">
                    <span className="num" style={{ color: l.delta < 0 ? 'var(--err)' : 'var(--ok)' }}>
                      {l.delta > 0 ? '+' : ''}{usd(l.delta)}
                    </span>
                  </td>
                  <td className="right"><span className="num">{usd(l.balanceAfter)}</span></td>
                  <td>
                    {l.note
                      ? <span className="ellip">{l.note}</span>
                      : <span style={{ color: 'var(--t3)' }}>—</span>}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Drawer>
  );
}
