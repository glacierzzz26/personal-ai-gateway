import { useState } from 'react';
import { Button, Input, Select, Table } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useQuery } from '@tanstack/react-query';
import { Block as BlockCard, Blocks } from '@/components/Block';
import PageHeader from '@/components/PageHeader';
import RequestLogDrawer from '@/components/RequestLogDrawer';
import StatusDot from '@/components/StatusDot';
import { EmptyState, ErrorState, NoResultState } from '@/components/States';
import { IconRefresh } from '@/components/icons';
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
    return (
      <span style={{ display: 'inline-flex', alignItems: 'center', gap: 7 }}>
        <StatusDot status="" text={r.error ? '失败' : String(v)} tone="err" />
        {r.error && <span className="gw-badge">{FAIL_LABEL[classifyError(r.error)]}</span>}
      </span>
    );
  };

  const columns: ColumnsType<RequestLogItem> = [
    {
      title: '时间', dataIndex: 'ts', width: 160,
      render: v => <span className="gw-mono">{v}</span>,
    },
    { title: '状态', dataIndex: 'statusCode', width: 160, render: statusCell },
    { title: '模型', dataIndex: 'model', render: v => <span className="gw-mono">{v}</span> },
    {
      title: '渠道', dataIndex: 'channelName', width: 150, ellipsis: true,
      render: v => v || <span style={{ color: 'var(--gw-text-3)' }}>—</span>,
    },
    {
      title: '令牌', dataIndex: 'tokenName', width: 140, ellipsis: true,
      render: v => <span style={{ color: 'var(--gw-text-3)' }}>{v || '—'}</span>,
    },
    { title: '输入', dataIndex: 'inTokens', align: 'right', width: 100, render: v => <span className="gw-num">{fmt.k(v)}</span> },
    { title: '输出', dataIndex: 'outTokens', align: 'right', width: 100, render: v => <span className="gw-num">{fmt.k(v)}</span> },
    {
      title: '缓存', dataIndex: 'cacheReadTokens', align: 'right', width: 90,
      render: v => (v ? <span className="gw-num">{fmt.k(v)}</span> : <span style={{ color: 'var(--gw-text-3)' }}>—</span>),
    },
    { title: '首字', dataIndex: 'firstTokenMs', align: 'right', width: 90, render: v => <span className="gw-num">{v ? fmt.ms(v) : '—'}</span> },
    { title: '总耗时', dataIndex: 'totalMs', align: 'right', width: 100, render: v => <span className="gw-num">{fmt.ms(v)}</span> },
    { title: '花费', dataIndex: 'costUsd', align: 'right', width: 100, render: v => <span className="gw-num">{fmt.usd(v, 4)}</span> },
  ];

  const errorText = error instanceof Error ? error.message : String(error ?? '加载失败');

  return (
    <div className="gw-page">
      <PageHeader
        title="请求日志"
        desc="逐条请求明细，筛选与分页均由服务端执行；499 客户端中断不计入失败率"
        extra={
          <Button icon={<IconRefresh />} loading={isFetching} onClick={() => void refetch()}>
            刷新
          </Button>
        }
      />

      <Blocks>
        <BlockCard>
          <div className="gw-toolbar">
            <Select
              style={{ width: 230 }}
              value={filters.model}
              onChange={v => applyFilter({ model: v })}
              showSearch
              allowClear
              placeholder="全部模型"
              options={modelList.map(m => ({ value: m.name, label: m.name }))}
              filterOption={(input, o) => String(o?.label ?? '').toLowerCase().includes(input.toLowerCase())}
            />
            <Select
              style={{ width: 190 }}
              value={filters.channel}
              onChange={v => applyFilter({ channel: v })}
              showSearch
              allowClear
              placeholder="全部渠道"
              options={channelList.map(c => ({ value: c.name, label: c.name }))}
              filterOption={(input, o) => String(o?.label ?? '').toLowerCase().includes(input.toLowerCase())}
            />
            <Select
              style={{ width: 140 }}
              value={filters.status || undefined}
              onChange={v => applyFilter({ status: v ?? '' })}
              placeholder="全部状态"
              options={[
                { value: 'ok', label: '成功' },
                { value: 'error', label: '失败' },
                { value: 'canceled', label: '中断 (499)' },
              ]}
            />
            <Input.Search
              allowClear
              placeholder="搜索模型 / 渠道 / 令牌 / IP / 错误"
              style={{ width: 260 }}
              value={kwInput}
              onChange={e => {
                const val = e.target.value;
                setKwInput(val);
                // 点清除按钮时同步清掉已生效的关键词筛选
                if (!val && filters.kw) applyFilter({ kw: '' });
              }}
              onSearch={v => applyFilter({ kw: v.trim() })}
            />
            <Button onClick={reset} disabled={!hasFilter && !kwInput}>重置</Button>
            <span className="count">
              {hasFilter ? (
                <>筛选出 <b>{fmt.n(total)}</b> 条日志</>
              ) : (
                <>共 <b>{fmt.n(total)}</b> 条日志</>
              )}
            </span>
          </div>

          <Table<RequestLogItem>
            rowKey="id"
            size="middle"
            loading={(isLoading || isFetching) && logs.length === 0}
            dataSource={logs}
            columns={columns}
            scroll={{ x: 1420 }}
            locale={{
              emptyText: isError ? (
                <ErrorState
                  title="日志加载失败"
                  desc={errorText}
                  onRetry={() => void refetch()}
                />
              ) : hasFilter ? (
                <NoResultState
                  title="没有符合筛选条件的日志"
                  desc="当前模型 / 渠道 / 状态 / 关键字组合没有命中任何请求。"
                  action={<Button size="small" onClick={reset}>清除筛选</Button>}
                />
              ) : (
                <EmptyState
                  title="还没有请求日志"
                  desc="网关转发过请求后，这里会逐条列出耗时、Token 与花费。"
                />
              ),
            }}
            onRow={r => ({
              onClick: () => setDetail(r),
              onKeyDown: e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); setDetail(r); } },
              tabIndex: 0,
              style: { cursor: 'pointer' },
            })}
            pagination={{
              current: page,
              pageSize: size,
              total,
              showSizeChanger: true,
              pageSizeOptions: PAGE_SIZES.map(String),
              showTotal: t => `共 ${fmt.n(t)} 条`,
              onChange: (p, ps) => {
                // 切换 pageSize 时回到第 1 页,避免超出新分页范围
                setPage(ps !== size ? 1 : p);
                setSize(ps);
              },
            }}
          />
        </BlockCard>
      </Blocks>

      <RequestLogDrawer detail={detail} onClose={() => setDetail(null)} />
    </div>
  );
}
