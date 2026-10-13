import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Block as BlockCard, Blocks } from '@/components/Block';
import PageHeader from '@/components/PageHeader';
import RequestLogDrawer from '@/components/RequestLogDrawer';
import StatusDot from '@/components/StatusDot';
import { EmptyState, ErrorState, NoResultState } from '@/components/States';
import { Badge, Button, Input, Pager, Seg, Select, Stack, Table } from '@/ui/primitives';
import type { Column } from '@/ui/primitives';
import { api } from '@/services/api';
import { FAIL_LABEL, STATUS_CLIENT_CLOSED, classifyError, fmt } from '@/utils/format';
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

  const columns: Column<RequestLogItem>[] = [
    { key: 'ts', title: '时间', width: 148, render: r => <span className="mono small">{r.ts}</span> },
    {
      key: 'statusCode',
      title: '状态',
      width: 100,
      render: r => {
        const v = r.statusCode;
        /*
         * 状态列：颜色 + 文字 + 归因徽章。
         * 499（客户端中断）单独表述为「中断」—— 它既不是成功也不是网关故障，
         * 归到「失败」会让人以为上游有问题，归到「成功」又掩盖了请求没跑完。
         */
        if (v === STATUS_CLIENT_CLOSED) return <StatusDot status="" text="中断" tone="aux" />;
        if (okCode(v)) return <StatusDot status="" text="成功" tone="ok" />;
        return (
          <span className="hstack" style={{ gap: 7 }}>
            <StatusDot status="" text={r.error ? '失败' : String(v)} tone="err" />
            {r.error && <Badge tone="slate">{FAIL_LABEL[classifyError(r.error)]}</Badge>}
          </span>
        );
      },
    },
    { key: 'model', title: '模型', render: r => <span className="mono ellip" title={r.model}>{r.model}</span> },
    { key: 'channelName', title: '渠道', width: 118, render: r => r.channelName || <span className="muted">—</span> },
    { key: 'tokenName', title: '令牌', width: 108, render: r => <span className="muted">{r.tokenName || '—'}</span> },
    { key: 'inTokens', title: '输入', align: 'right', width: 84, render: r => <span className="num">{fmt.k(r.inTokens)}</span> },
    { key: 'outTokens', title: '输出', align: 'right', width: 84, render: r => <span className="num">{fmt.k(r.outTokens)}</span> },
    {
      key: 'cacheReadTokens',
      title: '缓存读',
      align: 'right',
      width: 78,
      render: r => (r.cacheReadTokens ? <span className="num">{fmt.k(r.cacheReadTokens)}</span> : <span className="muted">—</span>),
    },
    {
      key: 'cacheWriteTokens',
      title: '缓存写',
      align: 'right',
      width: 78,
      render: r => (r.cacheWriteTokens ? <span className="num">{fmt.k(r.cacheWriteTokens)}</span> : <span className="muted">—</span>),
    },
    { key: 'firstTokenMs', title: '首字', align: 'right', width: 78, render: r => <span className="num">{r.firstTokenMs ? fmt.ms(r.firstTokenMs) : '—'}</span> },
    { key: 'totalMs', title: '总耗时', align: 'right', width: 86, render: r => <span className="num">{fmt.ms(r.totalMs)}</span> },
    { key: 'costUsd', title: '花费', align: 'right', width: 90, render: r => <span className="num">{fmt.usd(r.costUsd)}</span> },
  ];

  const errorText = error instanceof Error ? error.message : String(error ?? '加载失败');
  const loading = (isLoading || isFetching) && logs.length === 0;

  return (
    <>
      <PageHeader
        title="请求日志"
        desc="逐条请求明细，筛选与分页均由服务端执行；499 客户端中断不计入失败率"
        extra={
          <Button icon="refresh" loading={isFetching} onClick={() => void refetch()}>
            刷新
          </Button>
        }
      />

      <Blocks>
        <BlockCard>
          <div style={{ padding: '12px 14px 0' }}>
            <div className="toolbar">
              <div className="hstack wrap" style={{ gap: 8 }}>
                <Select
                  style={{ width: 200 }}
                  value={filters.model ?? ''}
                  onChange={e => applyFilter({ model: e.target.value || undefined })}
                  options={[{ value: '', label: '全部模型' }, ...modelList.map(m => ({ value: m.name, label: m.name }))]}
                />
                <Select
                  style={{ width: 180 }}
                  value={filters.channel ?? ''}
                  onChange={e => applyFilter({ channel: e.target.value || undefined })}
                  options={[{ value: '', label: '全部渠道' }, ...channelList.map(c => ({ value: c.name, label: c.name }))]}
                />
                <Select
                  style={{ width: 140 }}
                  value={filters.status ?? ''}
                  onChange={e => applyFilter({ status: e.target.value as LogFilters['status'] })}
                  options={[
                    { value: '', label: '全部状态' },
                    { value: 'ok', label: '成功' },
                    { value: 'error', label: '失败' },
                    { value: 'canceled', label: '中断 (499)' },
                  ]}
                />
                <Input
                  type="search"
                  placeholder="搜索模型 / 渠道 / 令牌 / IP / 错误"
                  style={{ width: 240 }}
                  value={kwInput}
                  onChange={e => {
                    const val = e.target.value;
                    setKwInput(val);
                    // 点清除按钮时同步清掉已生效的关键词筛选
                    if (!val && filters.kw) applyFilter({ kw: '' });
                  }}
                  onKeyDown={e => {
                    if (e.key === 'Enter') applyFilter({ kw: kwInput.trim() });
                  }}
                />
                <Button onClick={reset} disabled={!hasFilter && !kwInput}>重置</Button>
              </div>
              <div className="hstack" style={{ gap: 10 }}>
                <Seg
                  size="sm"
                  value={size}
                  onChange={v => {
                    setSize(Number(v));
                    setPage(1);
                  }}
                  options={PAGE_SIZES.map(n => ({ value: n, label: `${n}/页` }))}
                />
                <span className="cell-sub">
                  {hasFilter ? (
                    <>筛选出 <b>{fmt.n(total)}</b> 条日志</>
                  ) : (
                    <>共 <b>{fmt.n(total)}</b> 条日志</>
                  )}
                </span>
              </div>
            </div>
          </div>

          {loading ? (
            <div style={{ padding: 14 }}>
              <Stack gap="sm">
                {Array.from({ length: 6 }).map((_, i) => (
                  <div key={i} className="sk" style={{ height: 14, width: `${90 - i * 8}%` }} />
                ))}
              </Stack>
            </div>
          ) : isError ? (
            <div style={{ padding: 14 }}>
              <ErrorState title="日志加载失败" desc={errorText} onRetry={() => void refetch()} />
            </div>
          ) : logs.length === 0 ? (
            <div style={{ padding: 14 }}>
              {hasFilter ? (
                <NoResultState
                  title="没有符合筛选条件的日志"
                  desc="当前模型 / 渠道 / 状态 / 关键字组合没有命中任何请求。"
                  action={<Button size="sm" onClick={reset}>清除筛选</Button>}
                />
              ) : (
                <EmptyState title="还没有请求日志" desc="网关转发过请求后，这里会逐条列出耗时、Token 与花费。" />
              )}
            </div>
          ) : (
            <>
              <Table<RequestLogItem> columns={columns} rows={logs} rowKey={r => r.id} onRowClick={r => setDetail(r)} />
              <Pager total={total} page={page} size={size} onChange={setPage} />
            </>
          )}
        </BlockCard>
      </Blocks>

      <RequestLogDrawer detail={detail} onClose={() => setDetail(null)} />
    </>
  );
}
