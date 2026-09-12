import { Descriptions, Drawer } from 'antd';
import type { RequestLogItem } from '@/types';
import { fmt } from '@/utils/format';
import { TOKENS } from '@/styles/tokens';

/** 2xx/3xx 视为成功(与管理面日志语义一致)。 */
const okCode = (code: number) => code >= 100 && code < 400;

/** 单条请求详情 Drawer:Logs 页与 Dashboard「最近请求」共用。 */
export default function RequestLogDrawer({ detail, onClose }: {
  detail: RequestLogItem | null;
  onClose: () => void;
}) {
  return (
    <Drawer
      width={640}
      open={!!detail}
      onClose={onClose}
      title={detail ? <span className="gw-mono">#{detail.id}</span> : null}
      destroyOnClose
    >
      {detail && (
        <>
          {detail.error && (
            <div
              style={{
                border: '1px solid var(--gw-border)', borderLeft: `2px solid ${TOKENS.err}`,
                borderRadius: 'var(--gw-r-card)', padding: '10px 12px', fontSize: 13,
                color: 'var(--gw-text-2)', background: 'var(--gw-bg)', marginBottom: 16,
                wordBreak: 'break-all',
              }}
            >
              {detail.error}
            </div>
          )}
          <Descriptions column={1} size="small" bordered styles={{ label: { width: 116 } }}>
            <Descriptions.Item label="请求 ID"><span className="gw-mono">{detail.id}</span></Descriptions.Item>
            <Descriptions.Item label="时间">{detail.ts}</Descriptions.Item>
            <Descriptions.Item label="模型"><span className="gw-mono">{detail.model}</span></Descriptions.Item>
            <Descriptions.Item label="渠道">{detail.channelName || '—'}</Descriptions.Item>
            <Descriptions.Item label="令牌">{detail.tokenName || '—'}</Descriptions.Item>
            <Descriptions.Item label="来源 IP">{detail.ip || '—'}</Descriptions.Item>
            <Descriptions.Item label="状态码">
              {detail.statusCode}
              {okCode(detail.statusCode) ? '' : ' (失败)'}
            </Descriptions.Item>
            <Descriptions.Item label="输入 Token"><span className="gw-num">{fmt.n(detail.inTokens)}</span></Descriptions.Item>
            <Descriptions.Item label="输出 Token"><span className="gw-num">{fmt.n(detail.outTokens)}</span></Descriptions.Item>
            {!!detail.cacheReadTokens && (
              <Descriptions.Item label="缓存命中 Token">
                <span className="gw-num">{fmt.n(detail.cacheReadTokens!)}</span>
              </Descriptions.Item>
            )}
            <Descriptions.Item label="首字延迟">{detail.firstTokenMs ? fmt.ms(detail.firstTokenMs) : '—'}</Descriptions.Item>
            <Descriptions.Item label="总耗时">{fmt.ms(detail.totalMs)}</Descriptions.Item>
            <Descriptions.Item label="花费">{fmt.usd(detail.costUsd, 6)}</Descriptions.Item>
          </Descriptions>
        </>
      )}
    </Drawer>
  );
}
