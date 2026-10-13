import type { ReactNode } from 'react';
import { Drawer } from '@/ui/overlay';
import { Icon } from '@/ui/icons';
import { KV } from '@/ui/primitives';
import type { RequestLogItem } from '@/types';
import { fmt } from '@/utils/format';

/** 2xx/3xx 视为成功(与管理面日志语义一致)。 */
const okCode = (code: number) => code >= 100 && code < 400;

/** 单条请求详情 Drawer:Logs 页与 Dashboard「最近请求」共用。 */
export default function RequestLogDrawer({ detail, onClose }: {
  detail: RequestLogItem | null;
  onClose: () => void;
}) {
  if (!detail) return null;

  const mono = (v: ReactNode) => <span className="mono">{v}</span>;
  const pairs: [ReactNode, ReactNode][] = [
    ['请求 ID', mono(detail.id)],
    ['时间', detail.ts],
    ['模型', mono(detail.model)],
    ['渠道', detail.channelName || '—'],
    ['令牌', detail.tokenName || '—'],
    ['来源 IP', detail.ip || '—'],
    [
      '状态码',
      <span key="sc" style={okCode(detail.statusCode) ? undefined : { color: 'var(--err)' }}>
        {detail.statusCode}
        {okCode(detail.statusCode) ? '' : ' (失败)'}
      </span>,
    ],
    ['输入 Token', <span className="num" key="in">{fmt.n(detail.inTokens)}</span>],
    ['输出 Token', <span className="num" key="out">{fmt.n(detail.outTokens)}</span>],
    ...(detail.cacheReadTokens
      ? ([['缓存命中 Token', <span className="num" key="cr">{fmt.n(detail.cacheReadTokens)}</span>]] as [ReactNode, ReactNode][])
      : []),
    ...(detail.cacheWriteTokens
      ? ([['缓存写入 Token', <span className="num" key="cw">{fmt.n(detail.cacheWriteTokens)}</span>]] as [ReactNode, ReactNode][])
      : []),
    ['首字延迟', detail.firstTokenMs ? fmt.ms(detail.firstTokenMs) : '—'],
    ['总耗时', fmt.ms(detail.totalMs)],
    ['花费', fmt.usd(detail.costUsd, 6)],
  ];

  return (
    <div
      className="overlay"
      style={{ zIndex: 70 }}
      onMouseDown={e => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <Drawer title={mono(`#${detail.id}`)} sub={detail.ts} width={640} onClose={onClose}>
        {detail.error && (
          <div className="note note-warn" style={{ marginBottom: 16, wordBreak: 'break-all' }}>
            <Icon name="warn" size={14} />
            <div style={{ flex: 1, minWidth: 0 }}>{detail.error}</div>
          </div>
        )}
        <KV pairs={pairs} />
      </Drawer>
    </div>
  );
}
