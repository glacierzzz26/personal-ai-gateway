import { Drawer } from '@/ui/overlay';
import { currentCurrency } from '@/stores/currency';
import type { RequestLogItem } from '@/types';

/**
 * 格式化函数内联说明见 LedgerDrawer.tsx:原 @/utils/format 依赖缺失的
 * @/styles/tokens,故按原 fmt 逻辑等价内联(币种符号跟随 currentCurrency)。
 */
const SYMBOL: Record<string, string> = { CNY: '¥', USD: '$' };
const dec = (v: number, d: number) => (v > 0 && v < 0.005 ? Math.max(d, 4) : d);
const usd = (v: number, d = 2) => `${SYMBOL[currentCurrency()] ?? '$'}${v.toFixed(dec(v, d))}`;
const n = (v: number) => v.toLocaleString('en-US');
const ms = (v: number) => (v >= 1000 ? `${(v / 1000).toFixed(2)}s` : `${Math.round(v)}ms`);

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
      title={detail ? <span className="mono">#{detail.id}</span> : null}
    >
      {detail && (
        <>
          {detail.error && (
            <div
              style={{
                border: '1px solid var(--line)',
                borderLeft: '2px solid var(--err)',
                borderRadius: '5px',
                padding: '10px 12px',
                fontSize: 13,
                color: 'var(--t2)',
                background: 'var(--panel)',
                marginBottom: 16,
                wordBreak: 'break-all',
              }}
            >
              {detail.error}
            </div>
          )}
          <div>
            <div className="kv"><span className="k">请求 ID</span><span className="v mono">{detail.id}</span></div>
            <div className="kv"><span className="k">时间</span><span className="v">{detail.ts}</span></div>
            <div className="kv"><span className="k">模型</span><span className="v mono">{detail.model}</span></div>
            <div className="kv"><span className="k">渠道</span><span className="v">{detail.channelName || '—'}</span></div>
            <div className="kv"><span className="k">令牌</span><span className="v">{detail.tokenName || '—'}</span></div>
            <div className="kv"><span className="k">来源 IP</span><span className="v">{detail.ip || '—'}</span></div>
            <div className="kv">
              <span className="k">状态码</span>
              <span className="v">{detail.statusCode}{okCode(detail.statusCode) ? '' : ' (失败)'}</span>
            </div>
            <div className="kv"><span className="k">输入 Token</span><span className="v num">{n(detail.inTokens)}</span></div>
            <div className="kv"><span className="k">输出 Token</span><span className="v num">{n(detail.outTokens)}</span></div>
            {!!detail.cacheReadTokens && (
              <div className="kv"><span className="k">缓存命中 Token</span><span className="v num">{n(detail.cacheReadTokens)}</span></div>
            )}
            {!!detail.cacheWriteTokens && (
              <div className="kv"><span className="k">缓存写入 Token</span><span className="v num">{n(detail.cacheWriteTokens)}</span></div>
            )}
            <div className="kv"><span className="k">首字延迟</span><span className="v">{detail.firstTokenMs ? ms(detail.firstTokenMs) : '—'}</span></div>
            <div className="kv"><span className="k">总耗时</span><span className="v">{ms(detail.totalMs)}</span></div>
            <div className="kv"><span className="k">花费</span><span className="v">{usd(detail.costUsd, 6)}</span></div>
          </div>
        </>
      )}
    </Drawer>
  );
}
