import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Block as BlockCard, Blocks } from '@/components/Block';
import RequestLogDrawer from '@/components/RequestLogDrawer';
import StatusDot from '@/components/StatusDot';
import { EmptyState, ErrorState, NoResultState } from '@/components/States';
import { Badge, Btn, Input, PageHead, Pager, Select, badgeTone } from '@/ui/primitives';
import { api } from '@/services/api';
import { STATUS_CLIENT_CLOSED, fmt, failBadge } from '@/utils/format';
import type { LogFilters, RequestLogItem } from '@/types';

const PAGE_SIZES = [20, 50, 100];

const okCode = (code: number) => code >= 100 && code < 400;

/** 请求日志:筛选/分页全部走后端 GET /logs(状态筛选 ok/error 语义与后端一致)。 */
export default function Logs() {
  const [filters, setFilters] = useState<LogFilters>({});
  const [kwInput, setKwInput] = useState('');
  const [page, setPage] = useState(1);
  const [size, setSize] = useState(20);
  const [detail, setDetail] = useState<RequestLogItem | null>(null);

  // 任一筛选变化都回到第一页(关键字在搜索时提交,不逐键触发)
  const applyFilter = (next: Partial<LogFilters>) => {
    setFilters(f => ({ ...f, ...next }));
    setPage(1);
  };

  // 筛选下拉数据源:真实渠道/模型名
  const { data: channelList = [] } = useQuery({ queryKey: ['channels'], queryFn: api.getChannels });
  const { data: modelList = [] } = useQuery({ queryKey: ['models'], queryFn: api.getModels });

  const { data, isLoading, isFetching, refetch, isError, error } = useQuery({
    queryKey: ['logs', filters, page, size],
    queryFn: () => api.getLogs(filters, page, size),
  });
  const logs = data?.items ?? [];
  const total = data?.total ?? 0;
  const hasFilter = !!(filters.model || filters.channel || filters.status || filters.kw);

  const reset = () => {
    setFilters({});
    setKwInput('');
    setPage(1);
  };

  /**
   * 状态列：颜色 + 文字 + 归因徽章。
   * 499（客户端中断）单独表述为「中断」—— 它既不是成功也不是网关故障，
   * 归到「失败」会让人以为上游有问题，归到「成功」又掩盖了请求没跑完。
   */
  const statusCell = (v: number, r: RequestLogItem) => {
    if (v === STATUS_CLIENT_CLOSED) {
      return <StatusDot status="" text="中断" tone="aux" />;
    }
    if (okCode(v)) return <StatusDot status="" text="成功" tone="ok" />;
    // 失败原因徽章换行显示 —— 与圆点同列上下叠放,避免撑宽整列
    return (
      <span style={{ display: 'inline-flex', flexDirection: 'column', alignItems: 'flex-start', gap: 4 }}>
        <StatusDot status="" text={r.error ? '失败' : String(v)} tone="err" />
        {r.error && <Badge tone={badgeTone(failBadge(r.error).tone)}>{failBadge(r.error).label}</Badge>}
      </span>
    );
  };

  const loadingRows = isLoading || isFetching;
  const loading = loadingRows && logs.length === 0;

  const errorText = error instanceof Error ? error.message : String(error ?? '加载失败');

  const emptyNode = isError ? (
    <ErrorState
      title="日志加载失败"
      desc={errorText}
      onRetry={() => void refetch()}
    />
  ) : hasFilter ? (
    <NoResultState
      title="没有符合筛选条件的日志"
      desc="当前模型 / 渠道 / 状态 / 关键字组合没有命中任何请求。"
      action={<Btn size="sm" onClick={reset}>清除筛选</Btn>}
    />
  ) : (
    <EmptyState
      title="还没有请求日志"
      desc="网关转发过请求后，这里会逐条列出耗时、Token 与花费。"
    />
  );

  return (
    <div>
      <PageHead
        title="请求日志"
        sub="逐条请求明细，筛选与分页均由服务端执行；499 客户端中断不计入失败率"
        actions={
          <Btn icon="refresh" loading={isFetching} onClick={() => void refetch()}>
            刷新
          </Btn>
        }
      />

      <Blocks>
        <BlockCard>
          <div className="toolbar" style={{ padding: '12px 14px 0' }}>
            <Select
              style={{ width: 230 }}
              value={filters.model ?? ''}
              onChange={e => applyFilter({ model: e.target.value })}
            >
              <option value="">全部模型</option>
              {modelList.map(m => <option key={m.name} value={m.name}>{m.name}</option>)}
            </Select>
            <Select
              style={{ width: 190 }}
              value={filters.channel ?? ''}
              onChange={e => applyFilter({ channel: e.target.value })}
            >
              <option value="">全部渠道</option>
              {channelList.map(c => <option key={c.name} value={c.name}>{c.name}</option>)}
            </Select>
            <Select
              style={{ width: 140 }}
              value={filters.status || ''}
              onChange={e => applyFilter({ status: e.target.value as LogFilters['status'] })}
            >
              <option value="">全部状态</option>
              <option value="ok">成功</option>
              <option value="error">失败</option>
              <option value="canceled">中断 (499)</option>
            </Select>
            {/* 关键字在回车 / 点「搜索」时提交,不逐键触发 */}
            <Input
              placeholder="搜索模型 / 渠道 / 令牌 / IP / 错误"
              style={{ width: 260 }}
              value={kwInput}
              onChange={e => {
                const val = e.target.value;
                setKwInput(val);
                // 清空输入时同步清掉已生效的关键词筛选
                if (!val && filters.kw) applyFilter({ kw: '' });
              }}
              onKeyDown={e => { if (e.key === 'Enter') applyFilter({ kw: kwInput.trim() }); }}
            />
            <Btn onClick={() => applyFilter({ kw: kwInput.trim() })}>搜索</Btn>
            <Btn onClick={reset} disabled={!hasFilter && !kwInput}>重置</Btn>
            <span className="count">
              {hasFilter ? (
                <>筛选出 <b>{fmt.n(total)}</b> 条日志</>
              ) : (
                <>共 <b>{fmt.n(total)}</b> 条日志</>
              )}
            </span>
          </div>

          <div className="table-wrap" style={{ padding: '8px 6px 0' }}>
            <table className="tb" style={{ tableLayout: 'fixed' }}>
              <thead>
                <tr>
                  <th style={{ width: 138 }}>时间</th>
                  <th style={{ width: 92 }}>状态</th>
                  <th>模型</th>
                  <th style={{ width: 98 }}>渠道</th>
                  <th style={{ width: 92 }}>令牌</th>
                  <th className="right" style={{ width: 78 }}>输入</th>
                  <th className="right" style={{ width: 78 }}>输出</th>
                  <th className="right" style={{ width: 70 }}>缓存读</th>
                  <th className="right" style={{ width: 70 }}>缓存写</th>
                  <th className="right" style={{ width: 70 }}>首字</th>
                  <th className="right" style={{ width: 82 }}>总耗时</th>
                  <th className="right" style={{ width: 86 }}>花费</th>
                </tr>
              </thead>
              <tbody>
                {loading ? (
                  <tr><td colSpan={12} style={{ textAlign: 'center', color: 'var(--t3)', padding: 24 }}>读取中…</td></tr>
                ) : logs.length === 0 ? (
                  <tr><td colSpan={12}>{emptyNode}</td></tr>
                ) : (
                  logs.map(r => (
                    <tr
                      key={r.id}
                      className="clickable"
                      tabIndex={0}
                      style={{ cursor: 'pointer' }}
                      onClick={() => setDetail(r)}
                      onKeyDown={e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); setDetail(r); } }}
                    >
                      <td className="mono">{r.ts}</td>
                      <td>{statusCell(r.statusCode, r)}</td>
                      <td className="mono" title={r.model} style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{r.model}</td>
                      <td title={r.channelName} style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                        {r.channelName || <span style={{ color: 'var(--t3)' }}>—</span>}
                      </td>
                      <td title={r.tokenName} style={{ color: 'var(--t3)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{r.tokenName || '—'}</td>
                      <td className="right num">{fmt.k(r.inTokens)}</td>
                      <td className="right num">{fmt.k(r.outTokens)}</td>
                      <td className="right num">{r.cacheReadTokens ? fmt.k(r.cacheReadTokens) : <span style={{ color: 'var(--t3)' }}>—</span>}</td>
                      <td className="right num">{r.cacheWriteTokens ? fmt.k(r.cacheWriteTokens) : <span style={{ color: 'var(--t3)' }}>—</span>}</td>
                      <td className="right num">{r.firstTokenMs ? fmt.ms(r.firstTokenMs) : '—'}</td>
                      <td className="right num">{fmt.ms(r.totalMs)}</td>
                      <td className="right num">{r.costUsd ? fmt.usd(r.costUsd) : '—'}</td>
                    </tr>
                  ))
                )}
              </tbody>
            </table>
          </div>

          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'flex-end', gap: 10, padding: '4px 14px 10px' }}>
            <Select
              style={{ width: 108 }}
              value={size}
              onChange={e => {
                const ps = Number(e.target.value);
                // 切换 pageSize 时回到第 1 页,避免超出新分页范围
                setPage(ps !== size ? 1 : page);
                setSize(ps);
              }}
            >
              {PAGE_SIZES.map(s => <option key={s} value={s}>{s} 条/页</option>)}
            </Select>
            <Pager page={page} size={size} total={total} onPage={setPage} />
          </div>
        </BlockCard>
      </Blocks>

      <RequestLogDrawer detail={detail} onClose={() => setDetail(null)} />
    </div>
  );
}
